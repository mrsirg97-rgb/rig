package view

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/mrsirg97-rgb/rig/v3/imagemarker"
	"github.com/mrsirg97-rgb/rig/v3/tool"
	_ "golang.org/x/image/webp"
)

const (
	maxSourceBytes = 20 << 20
	maxPixels      = 1 << 24
	maxSide        = 1568
	jpegQuality    = 90

	ctxRowStride = 32

	defaultFrames = 6
	maxFrames     = 12

	lossy = "jpeg"
	still = "png"
)

type Config struct {
	Blobs  string
	FFmpeg string
}

type Clip struct {
	Path   string
	At     string
	Frames int
}

type given struct {
	Action string `json:"action"`
	Path   string `json:"path"`
	At     string `json:"at"`
	Frames *int   `json:"frames"`
}

type View interface {
	tool.Definition
	Exec(ctx context.Context, args json.RawMessage) (string, error)

	Image(ctx context.Context, path string) (string, error)
	Video(ctx context.Context, clip Clip) (string, error)
}

type toolView struct {
	tool.Definition
	cfg Config
}

func New(blobsDir string) View {
	return NewWith(Config{Blobs: blobsDir})
}

func NewWith(cfg Config) View {
	return &toolView{Definition: tool.Def("view"), cfg: cfg}
}

func (v *toolView) Exec(ctx context.Context, data json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		data = json.RawMessage(`{}`)
	}
	var a given
	if err := tool.StrictDecode(data, &a); err != nil {
		return "", fmt.Errorf("view: %v", err)
	}
	switch a.Action {
	case "", "image":
		return v.Image(ctx, a.Path)
	case "video":
		clip := Clip{Path: a.Path, At: a.At}
		if a.Frames != nil {
			clip.Frames = *a.Frames
		}
		return v.Video(ctx, clip)
	case "browser":
		return "", errors.New("view: the browser source is not in this build")
	default:
		return "", fmt.Errorf("view: unknown action %q; the actions are image (or omit it), video", a.Action)
	}
}

func (v *toolView) Image(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if path == "" {
		return "", errors.New("view: path required (the image file to look at)")
	}
	if v.cfg.Blobs == "" {
		return "", errors.New("view: no blob store is configured, so an image has nowhere to be kept")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("view: %v", err)
	}
	if imagemarker.HasControl(path) {
		return "", fmt.Errorf("view: %q: the path carries a control character, so the marker line could be broken; rename the file", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("view: %v", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("view: %s is not a regular file", path)
	}
	if info.Size() > maxSourceBytes {
		return "", fmt.Errorf("view: %s is %s, over the %s source cap: crop it or downscale it first", path, humanBytes(info.Size()), humanBytes(maxSourceBytes))
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("view: %v", err)
	}
	defer f.Close()
	src, err := io.ReadAll(io.LimitReader(f, maxSourceBytes+1))
	if err != nil {
		return "", fmt.Errorf("view: %s: %v", path, err)
	}
	if int64(len(src)) > maxSourceBytes {
		return "", fmt.Errorf("view: %s grew past the %s source cap while it was read", path, humanBytes(maxSourceBytes))
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return "", fmt.Errorf("view: %s is not a png, jpeg, webp, or gif image: %v", path, err)
	}
	pixels := int64(cfg.Width) * int64(cfg.Height)
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return "", fmt.Errorf("view: %s has no pixels to look at", path)
	}
	if pixels > maxPixels {
		return "", fmt.Errorf("view: %s is %dx%d (%d pixels), over the %d-pixel cap: crop it or downscale it first", path, cfg.Width, cfg.Height, pixels, maxPixels)
	}

	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return "", fmt.Errorf("view: %s: %v", path, err)
	}
	if b := img.Bounds(); b.Dx() > 0 && b.Dy() > 0 && int64(b.Dx())*int64(b.Dy()) > maxPixels {
		return "", fmt.Errorf("view: %s decoded to %dx%d, over the %d-pixel cap", path, b.Dx(), b.Dy(), maxPixels)
	}
	return v.publish(ctx, img, format, path)
}

func (v *toolView) publish(ctx context.Context, img image.Image, format, src string) (string, error) {
	if v.cfg.Blobs == "" {
		return "", errors.New("view: no blob store is configured, so an image has nowhere to be kept")
	}
	if imagemarker.HasControl(src) {
		return "", fmt.Errorf("view: %q: the source carries a control character, so the marker line could be broken", src)
	}
	scaled, opaque, err := resample(ctx, img)
	if err != nil {
		return "", err
	}
	enc, mime, err := encode(format, opaque, scaled)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(enc)
	if err := writeBlob(v.cfg.Blobs, sum, enc); err != nil {
		return "", err
	}
	return imagemarker.Format(imagemarker.Ref{
		SHA256: hex.EncodeToString(sum[:]),
		Mime:   mime,
		W:      scaled.Bounds().Dx(),
		H:      scaled.Bounds().Dy(),
		OrigW:  img.Bounds().Dx(),
		OrigH:  img.Bounds().Dy(),
		Bytes:  len(enc),
		Src:    src,
	}), nil
}

func resample(ctx context.Context, src image.Image) (*image.NRGBA, bool, error) {
	b := src.Bounds()
	dw, dh := targetSize(b.Dx(), b.Dy())
	return resampleTo(ctx, src, dw, dh)
}

func resampleTo(ctx context.Context, src image.Image, dw, dh int) (*image.NRGBA, bool, error) {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	opaque := true
	for y := 0; y < dh; y++ {
		if y%ctxRowStride == 0 {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
		}
		y0, y1 := band(y, sh, dh)
		for x := 0; x < dw; x++ {
			x0, x1 := band(x, sw, dw)
			var r, g, bl, al, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					c := toNRGBA(src.At(b.Min.X+sx, b.Min.Y+sy))
					if c.A < 255 {
						opaque = false
					}
					r += uint64(c.R)
					g += uint64(c.G)
					bl += uint64(c.B)
					al += uint64(c.A)
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(r / n),
				G: uint8(g / n),
				B: uint8(bl / n),
				A: uint8(al / n),
			})
		}
	}
	return dst, opaque, nil
}

func targetSize(w, h int) (int, int) {
	if w <= maxSide && h <= maxSide {
		return w, h
	}
	if w >= h {
		return maxSide, atLeastOne(h * maxSide / w)
	}
	return atLeastOne(w * maxSide / h), maxSide
}

func atLeastOne(v int) int {
	if v < 1 {
		return 1
	}
	return v
}

func band(i, span, out int) (int, int) {
	lo := i * span / out
	hi := (i + 1) * span / out
	if hi <= lo {
		hi = lo + 1
	}
	if hi > span {
		hi = span
	}
	if lo >= span {
		lo = span - 1
		hi = span
	}
	return lo, hi
}

func toNRGBA(c color.Color) color.NRGBA {
	if n, ok := c.(color.NRGBA); ok {
		return n
	}
	n, _ := color.NRGBAModel.Convert(c).(color.NRGBA)
	return n
}

func encode(format string, opaque bool, img image.Image) ([]byte, string, error) {
	var buf bytes.Buffer
	if opaque && (format == "jpeg" || format == "webp") {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/jpeg", nil
	}
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/png", nil
}

func writeBlob(dir string, sum [sha256.Size]byte, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	address := hex.EncodeToString(sum[:])
	final := imagemarker.BlobPath(dir, address)
	if _, err := os.Stat(final); err == nil {
		return nil
	}
	tmp, err := os.CreateTemp(dir, ".view-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if _, statErr := os.Stat(tmpName); statErr == nil {
			os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, final)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/float64(1<<20), 'f', 1, 64) + " MiB"
	case n >= 1<<10:
		return strconv.FormatInt(n/(1<<10), 10) + " KiB"
	default:
		return strconv.FormatInt(n, 10) + " B"
	}
}
