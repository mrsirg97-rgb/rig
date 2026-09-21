package view_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/core"
	"github.com/mrsirg97-rgb/rig/imagemarker"
	"github.com/mrsirg97-rgb/rig/tool/view"
)

const (
	maxSourceBytes = 20 << 20
	maxPixels      = 1 << 24
	maxSide        = 1568
)

func execAt(t *testing.T, blobs, path string) (string, error) {
	t.Helper()
	return view.New(blobs).Exec(context.Background(), jsonArgs(t, path))
}

func jsonArgs(t *testing.T, path string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeImage(t *testing.T, dir, name string, img image.Image) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg") {
		if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 95}); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return p
}

func blocks(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: uint8((x * y) % 256), A: 255})
		}
	}
	return img
}

func transparent(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 10, G: 20, B: 30, A: uint8((x + y) % 256)})
		}
	}
	return img
}

func mustParse(t *testing.T, line string) imagemarker.Ref {
	t.Helper()
	ref, ok := imagemarker.Parse(line)
	if !ok {
		t.Fatalf("the reply is not a marker line: %q", line)
	}
	return ref
}

func blobBytes(t *testing.T, blobs string, ref imagemarker.Ref) []byte {
	t.Helper()
	b, err := os.ReadFile(imagemarker.BlobPath(blobs, ref.SHA256))
	if err != nil {
		t.Fatalf("the marker's address holds no blob: %v", err)
	}
	return b
}

func decodeBlob(t *testing.T, blobs string, ref imagemarker.Ref) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(blobBytes(t, blobs, ref)))
	if err != nil {
		t.Fatalf("the blob does not decode: %v", err)
	}
	return img
}

func TestViewDownscalesToTheLongestSideBound(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	src := writeImage(t, dir, "shot.png", blocks(2560, 1440))
	line, err := execAt(t, blobs, src)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	ref := mustParse(t, line)
	if ref.W != maxSide || ref.H != 882 {
		t.Fatalf("sent dimensions = %dx%d, want 1568x882", ref.W, ref.H)
	}
	if ref.OrigW != 2560 || ref.OrigH != 1440 {
		t.Fatalf("orig = %dx%d, want 2560x1440", ref.OrigW, ref.OrigH)
	}
	if got := decodeBlob(t, blobs, ref); got.Bounds().Dx() != maxSide || got.Bounds().Dy() != 882 {
		t.Fatalf("the blob is %v, want 1568x882", got.Bounds())
	}
}

func TestViewDownscalesATallImageOnItsLongSide(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	src := writeImage(t, dir, "tall.png", blocks(1000, 4000))
	line, err := execAt(t, blobs, src)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	ref := mustParse(t, line)
	if ref.W != 392 || ref.H != maxSide {
		t.Fatalf("sent dimensions = %dx%d, want 392x1568", ref.W, ref.H)
	}
}

func TestViewKeepsAnImageInsideTheBoundAtItsOwnSize(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	src := writeImage(t, dir, "small.png", blocks(800, 600))
	line, err := execAt(t, blobs, src)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	ref := mustParse(t, line)
	if ref.W != 800 || ref.H != 600 || ref.OrigW != 800 || ref.OrigH != 600 {
		t.Fatalf("inside the bound the size must stand: %dx%d (orig %dx%d)", ref.W, ref.H, ref.OrigW, ref.OrigH)
	}
}

func TestTheBoundIsTheOnlyScaleAndOnePixelIsNeverZero(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	for _, tc := range []struct{ w, h, wantW, wantH int }{
		{1, 1, 1, 1},
		{1568, 1, 1568, 1},
		{1, 1568, 1, 1568},
		{1569, 1, 1568, 1},
		{3000, 2, 1568, 1},
		{3137, 3137, 1568, 1568},
	} {
		src := writeImage(t, dir, "edge.png", blocks(tc.w, tc.h))
		line, err := execAt(t, blobs, src)
		if err != nil {
			t.Fatalf("%dx%d: %v", tc.w, tc.h, err)
		}
		ref := mustParse(t, line)
		if ref.W != tc.wantW || ref.H != tc.wantH {
			t.Fatalf("%dx%d sent %dx%d, want %dx%d", tc.w, tc.h, ref.W, ref.H, tc.wantW, tc.wantH)
		}
		if ref.W < 1 || ref.H < 1 {
			t.Fatalf("%dx%d sent a zero edge", tc.w, tc.h)
		}
		if ref.W > maxSide || ref.H > maxSide {
			t.Fatalf("%dx%d sent over the bound: %dx%d", tc.w, tc.h, ref.W, ref.H)
		}
	}
}

func TestViewReplyIsOneLineAndNothingElse(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	src := writeImage(t, dir, "shot.png", blocks(64, 32))
	line, err := execAt(t, blobs, src)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if strings.Contains(line, "\n") {
		t.Fatalf("the reply is one line: %q", line)
	}
	if !strings.HasPrefix(line, "[[rig:image ") || !strings.HasSuffix(line, "]]") {
		t.Fatalf("the reply is the marker and nothing else: %q", line)
	}
}

func TestViewNamesTheSentBytesSizeAndSource(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	src := writeImage(t, dir, "shot.png", blocks(64, 32))
	line, err := execAt(t, blobs, src)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	ref := mustParse(t, line)
	b := blobBytes(t, blobs, ref)
	if ref.Bytes != len(b) {
		t.Fatalf("bytes = %d, want the blob length %d", ref.Bytes, len(b))
	}
	if ref.Src != src {
		t.Fatalf("src = %q, want %q", ref.Src, src)
	}
	sum := sha256.Sum256(b)
	if ref.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 = %s, want the digest of the sent bytes", ref.SHA256)
	}
}

func TestViewEncodesAnOpaqueLossySourceAsJPEG(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	src := writeImage(t, dir, "photo.jpg", blocks(64, 48))
	line, err := execAt(t, blobs, src)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if ref := mustParse(t, line); ref.Mime != "image/jpeg" {
		t.Fatalf("mime = %q, want image/jpeg for an opaque lossy source", ref.Mime)
	}
}

func TestViewEncodesALossyWebPAsJPEG(t *testing.T) {
	blobs := t.TempDir()
	line, err := execAt(t, blobs, fixture(t, "lossy.webp"))
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	ref := mustParse(t, line)
	if ref.Mime != "image/jpeg" {
		t.Fatalf("mime = %q, want image/jpeg", ref.Mime)
	}
	if ref.W != 16 || ref.H != 16 {
		t.Fatalf("sent %dx%d, want 16x16", ref.W, ref.H)
	}
}

func TestViewEncodesAWebPWithAlphaAsPNG(t *testing.T) {
	blobs := t.TempDir()
	line, err := execAt(t, blobs, fixture(t, "alpha.webp"))
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	ref := mustParse(t, line)
	if ref.Mime != "image/png" {
		t.Fatalf("mime = %q, want image/png for a source with alpha", ref.Mime)
	}
}

func TestViewEncodesAPNGSourceAsPNG(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	src := writeImage(t, dir, "shot.png", blocks(64, 48))
	line, err := execAt(t, blobs, src)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if ref := mustParse(t, line); ref.Mime != "image/png" {
		t.Fatalf("mime = %q, want image/png for a lossless source", ref.Mime)
	}
}

func TestViewEncodesATransparentSourceAsPNG(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	src := writeImage(t, dir, "alpha.png", transparent(64, 48))
	line, err := execAt(t, blobs, src)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if ref := mustParse(t, line); ref.Mime != "image/png" {
		t.Fatalf("mime = %q, want image/png for alpha", ref.Mime)
	}
}

func TestViewReadsTheFirstFrameOfAGIF(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	p := filepath.Join(dir, "anim.gif")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	pal := color.Palette{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}}
	g := gif.GIF{Config: image.Config{Width: 8, Height: 8, ColorModel: pal}, Delay: []int{1, 1}}
	first := image.NewPaletted(image.Rect(0, 0, 8, 8), pal)
	second := image.NewPaletted(image.Rect(0, 0, 8, 8), pal)
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			first.Set(x, y, color.RGBA{255, 0, 0, 255})
			second.Set(x, y, color.RGBA{0, 0, 255, 255})
		}
	}
	g.Image = []*image.Paletted{first, second}
	if err := gif.EncodeAll(f, &g); err != nil {
		t.Fatal(err)
	}
	f.Close()

	line, err := execAt(t, blobs, p)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	ref := mustParse(t, line)
	img := decodeBlob(t, blobs, ref)
	r, _, b, _ := img.At(3, 3).RGBA()
	if r>>8 != 255 || b>>8 != 0 {
		t.Fatalf("the first frame is not what was sent: rgba=%d,%d", r>>8, b>>8)
	}
}

func TestViewEncodesATransparentGIFAsPNG(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	p := filepath.Join(dir, "t.gif")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	pal := color.Palette{color.RGBA{0, 0, 0, 0}, color.RGBA{0, 255, 0, 255}}
	img := image.NewPaletted(image.Rect(0, 0, 8, 8), pal)
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if x%2 == 0 {
				img.Set(x, y, pal[0])
				continue
			}
			img.Set(x, y, pal[1])
		}
	}
	if err := gif.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
	f.Close()

	line, err := execAt(t, blobs, p)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if ref := mustParse(t, line); ref.Mime != "image/png" {
		t.Fatalf("mime = %q, want image/png for a transparent gif", ref.Mime)
	}
}

func TestSameSourceBytesGiveTheSameAddressAndBlob(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	payload := encodePNGBytes(t, blocks(48, 32))
	a := filepath.Join(dir, "a.png")
	b := filepath.Join(dir, "b.png")
	if err := os.WriteFile(a, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	first := mustParse(t, viewAt(t, blobs, a))
	again := mustParse(t, viewAt(t, blobs, a))
	if first != again {
		t.Fatalf("the same file gave two different markers: %+v vs %+v", first, again)
	}
	copied := mustParse(t, viewAt(t, blobs, b))
	if copied.SHA256 != first.SHA256 {
		t.Fatalf("identical bytes at a different path must land on the same address: %s vs %s", copied.SHA256, first.SHA256)
	}
	if copied.Src == first.Src {
		t.Fatal("src names the file that was read")
	}
}

func encodePNGBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func viewAt(t *testing.T, blobs, path string) string {
	t.Helper()
	line, err := execAt(t, blobs, path)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	return line
}

func TestTheBlobIsWrittenOnceAndNeverRewritten(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	src := writeImage(t, dir, "shot.png", blocks(48, 32))
	ref := mustParse(t, viewAt(t, blobs, src))
	addr := imagemarker.BlobPath(blobs, ref.SHA256)
	stale := time.Unix(1000000000, 0)
	if err := os.Chtimes(addr, stale, stale); err != nil {
		t.Fatal(err)
	}
	mustParse(t, viewAt(t, blobs, src))
	info, err := os.Stat(addr)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(stale) {
		t.Fatalf("a blob that is already there must be kept, not rewritten (mtime %v, want %v)", info.ModTime(), stale)
	}
	entries, err := os.ReadDir(blobs)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("a replay left debris in the store: %v", names(entries))
	}
}

func names(entries []os.DirEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestTheBlobStoreIsCreatedOnDemand(t *testing.T) {
	dir := t.TempDir()
	blobs := filepath.Join(dir, "no", "such", "blobs")
	src := writeImage(t, dir, "shot.png", blocks(16, 16))
	mustParse(t, viewAt(t, blobs, src))
	if info, err := os.Stat(blobs); err != nil || !info.IsDir() {
		t.Fatalf("the store must be created: %v", err)
	}
}

func TestViewRefusesAFileOverTheSourceCap(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	p := filepath.Join(dir, "huge.png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxSourceBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := execAt(t, blobs, p); err == nil {
		t.Fatal("a source over 20 MiB must refuse")
	} else if !strings.Contains(err.Error(), "20") || !strings.Contains(err.Error(), p) {
		t.Fatalf("the refusal must name the cap and the file: %v", err)
	}
	if entries, err := os.ReadDir(blobs); err == nil && len(entries) != 0 {
		t.Fatalf("a refused source writes nothing: %v", names(entries))
	}
}

func TestViewRefusesAnEmptySource(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	p := filepath.Join(dir, "empty.png")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := execAt(t, blobs, p); err == nil {
		t.Fatal("an empty file must refuse")
	}
}

func TestViewRefusesACanvasOverThePixelCapBeforeAllocating(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	p := filepath.Join(dir, "bomb.png")
	if err := os.WriteFile(p, hugePNGHeader(t, 8192, 8192), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := execAt(t, blobs, p); err == nil {
		t.Fatal("a 64-megapixel canvas must refuse")
	} else if !strings.Contains(err.Error(), p) {
		t.Fatalf("the refusal must name the file: %v", err)
	}
}

func hugePNGHeader(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8], ihdr[9], ihdr[10], ihdr[11], ihdr[12] = 8, 2, 0, 0, 0
	writeChunk(t, &b, "IHDR", ihdr)
	writeChunk(t, &b, "IEND", nil)
	if b.Len() > maxSourceBytes {
		t.Fatalf("the fixture must stay under the source cap")
	}
	return b.Bytes()
}

func writeChunk(t *testing.T, b *bytes.Buffer, typ string, payload []byte) {
	t.Helper()
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(payload)))
	b.Write(n[:])
	b.WriteString(typ)
	b.Write(payload)
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], crc32.ChecksumIEEE(append([]byte(typ), payload...)))
	b.Write(c[:])
}

func TestThePixelCapIsTheHeaderSizeAndNotTheFileSize(t *testing.T) {
	if (8192 * 8192) < maxPixels {
		t.Fatalf("the fixture is not over the pixel cap")
	}
}

func TestViewRefusesAFormatOutsideTheAcceptedSet(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	fixtures := map[string][]byte{
		"note.txt":  []byte("just text, no image anywhere here\n"),
		"bmp.png":   []byte("BM\x1e\x00\x00\x00\x00\x00\x28\x00\x00\x00"),
		"tiff.png":  []byte("II\x2a\x00\x08\x00\x00\x00"),
		"avif.png":  []byte("RIFF\x00\x00\x00\x00AVIF"),
		"empty.bin": nil,
	}
	for name, payload := range fixtures {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, payload, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := execAt(t, blobs, p); err == nil {
			t.Fatalf("%s: a %s-magic file must refuse", name, name)
		} else if !strings.Contains(err.Error(), name) {
			t.Fatalf("%s: the refusal must name the file: %v", name, err)
		}
	}
}

func TestTheFormatIsWhatTheBytesSayNotTheExtension(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	p := filepath.Join(dir, "lies.jpg")
	if err := os.WriteFile(p, encodePNGBytes(t, blocks(16, 16)), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := mustParse(t, viewAt(t, blobs, p))
	if ref.Mime != "image/png" {
		t.Fatalf("mime = %q: a png reading as png whatever its name, and png stays lossless", ref.Mime)
	}
	if _, err := execAt(t, blobs, filepath.Join(dir, "missing.png")); err == nil {
		t.Fatal("a missing file must refuse")
	}
}

func TestViewRefusesAMissingFileAndADirectory(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	if _, err := execAt(t, blobs, filepath.Join(dir, "nope.png")); err == nil {
		t.Fatal("a missing path must refuse")
	}
	if _, err := execAt(t, blobs, dir); err == nil {
		t.Fatal("a directory is not an image")
	}
}

func TestViewRefusesAnEmptyPathAndUnknownArgs(t *testing.T) {
	blobs := t.TempDir()
	tool := view.New(blobs)
	if _, err := tool.Exec(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("an empty path must refuse")
	}
	if _, err := tool.Exec(context.Background(), []byte(`{"path":"/tmp/a.png","scale":2}`)); err == nil {
		t.Fatal("an unknown argument must refuse")
	}
}

func TestViewRefusesACancelledContext(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	src := writeImage(t, dir, "shot.png", blocks(64, 64))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := view.New(blobs).Exec(ctx, jsonArgs(t, src)); err == nil {
		t.Fatal("a cancelled context refuses before touching the filesystem")
	}
	entries, err := os.ReadDir(blobs)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a cancelled call writes nothing: %v", names(entries))
	}
}

func TestViewNamesTheAbsolutePathItRead(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	src := writeImage(t, dir, "shot.png", blocks(16, 16))
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	rel := mustParse(t, viewAt(t, blobs, "shot.png"))
	if !filepath.IsAbs(rel.Src) {
		t.Fatalf("src = %q, want an absolute path", rel.Src)
	}
	abs := mustParse(t, viewAt(t, blobs, src))
	if rel.SHA256 != abs.SHA256 || rel.Src != abs.Src {
		t.Fatalf("a relative and an absolute path to one file must agree: %+v vs %+v", rel, abs)
	}
}

func TestViewRecordsNoFileState(t *testing.T) {
	dir, blobs := t.TempDir(), t.TempDir()
	src := writeImage(t, dir, "shot.png", blocks(16, 16))
	sess := core.NewSession()
	ctx := core.WithSession(context.Background(), sess)
	if _, err := view.New(blobs).Exec(ctx, jsonArgs(t, src)); err != nil {
		t.Fatalf("view: %v", err)
	}
	if len(sess.Files) != 0 {
		t.Fatalf("a downscale is not a license to edit: %v", sess.Files)
	}
}

func TestViewSchemaNamesOneRequiredPath(t *testing.T) {
	var schema struct {
		Type       string                    `json:"type"`
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	if err := json.Unmarshal(view.New("/tmp/blobs").Schema(), &schema); err != nil {
		t.Fatalf("the schema is not valid JSON: %v", err)
	}
	if schema.Type != "object" {
		t.Fatalf("schema type = %q", schema.Type)
	}
	if len(schema.Properties) != 1 {
		t.Fatalf("the schema is one field: %v", schema.Properties)
	}
	if _, ok := schema.Properties["path"]; !ok {
		t.Fatalf("the argument is named path so the ~ boundary and the walls apply (SPEC_FS): %v", schema.Properties)
	}
	if !reflect.DeepEqual(schema.Required, []string{"path"}) {
		t.Fatalf("required = %v, want [path]", schema.Required)
	}
}

func TestViewCarriesTheToolContractOnTheWire(t *testing.T) {
	tool := view.New("/tmp/blobs")
	if tool.Name() != "view" {
		t.Fatalf("name = %q", tool.Name())
	}
	if !strings.Contains(tool.Description(), "Guidelines:") {
		t.Fatalf("every description carries the Guidelines clause (SPEC_CORE): %q", tool.Description())
	}
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join("testdata", name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("no %s fixture: %v", p, err)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestAViewWithoutAStoreRefusesInsteadOfWritingRelative(t *testing.T) {
	dir := t.TempDir()
	src := writeImage(t, dir, "shot.png", blocks(40, 30))
	_, err := view.New("").Exec(t.Context(), jsonArgs(t, src))
	if err == nil {
		t.Fatal("a view with no store must refuse, not write beside the working directory")
	}
	if !strings.Contains(err.Error(), "view") || !strings.Contains(err.Error(), "store") {
		t.Fatalf("the refusal must say what is missing: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "blobs")); statErr == nil {
		t.Fatal("nothing was written")
	}
}
