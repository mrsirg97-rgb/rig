package view

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v3/imagemarker"
)

const (
	maxVideoBytes  = 512 << 20
	maxClipSeconds = 3600

	probeTimeout  = 20 * time.Second
	frameTimeout  = 20 * time.Second
	frameBufBytes = 32 << 20
	stderrTail    = 8 << 10
)

var (
	durationRE = regexp.MustCompile(`Duration: (\d+):(\d+):(\d+(?:\.\d+)?)`)
	fpsRE      = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?) fps`)
)

type frame struct {
	img image.Image
	at  float64
}

func (v *toolView) Video(ctx context.Context, clip Clip) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if clip.Path == "" {
		return "", errors.New("view: path required (the video file to sample)")
	}
	if v.cfg.Blobs == "" {
		return "", errors.New("view: no blob store is configured, so an image has nowhere to be kept")
	}
	path, err := filepath.Abs(clip.Path)
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
	if info.Size() > maxVideoBytes {
		return "", fmt.Errorf("view: %s is %s, over the %s video cap: trim it to the clip you need, or take one frame with at", path, humanBytes(info.Size()), humanBytes(maxVideoBytes))
	}
	bin, err := v.ffmpeg()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(clip.At) != "" {
		at, err := parseStamp(clip.At)
		if err != nil {
			return "", fmt.Errorf("view: at=%q: %v; give seconds (12.5) or hh:mm:ss", clip.At, err)
		}
		img, err := extract(ctx, bin, path, at)
		if err != nil {
			return "", err
		}
		return v.publish(ctx, img, lossy, path)
	}

	n := clip.Frames
	if n == 0 {
		n = defaultFrames
	}
	if n < 1 || n > maxFrames {
		return "", fmt.Errorf("view: frames=%d is outside 1..%d: ask for at most %d, or take one frame with at", n, maxFrames, maxFrames)
	}
	dur, fps, err := probe(ctx, bin, path)
	if err != nil {
		return "", err
	}
	if dur > maxClipSeconds {
		return "", fmt.Errorf("view: %s runs %s, over the %ds clip cap: trim it, or take one frame with at", path, humanSeconds(dur), maxClipSeconds)
	}
	step := 0.0
	if fps > 0 {
		step = 1 / fps
	} else {
		step = dur / float64(n)
	}
	last := dur - step
	frames := make([]frame, 0, n)
	for i := 0; i < n; i++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		at := (float64(i) + 0.5) * dur / float64(n)
		if at > last {
			at = last
		}
		if at < 0 {
			at = 0
		}
		img, err := extract(ctx, bin, path, at)
		if err != nil {
			return "", err
		}
		frames = append(frames, frame{img: img, at: at})
	}
	sheet, err := contactSheet(ctx, frames)
	if err != nil {
		return "", fmt.Errorf("view: %s: %v", path, err)
	}
	return v.publish(ctx, sheet, lossy, path)
}

func (v *toolView) ffmpeg() (string, error) {
	if v.cfg.FFmpeg != "" {
		bin := v.cfg.FFmpeg
		if filepath.IsAbs(bin) {
			if fi, err := os.Stat(bin); err != nil || fi.Mode()&0o111 == 0 {
				return "", fmt.Errorf("view: the configured ffmpeg %q is not an executable file; set RIG_FFMPEG or settings.ffmpeg to one", bin)
			}
			return bin, nil
		}
		if p, err := exec.LookPath(bin); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("view: the configured ffmpeg %q is not on PATH; set RIG_FFMPEG or settings.ffmpeg", bin)
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p, nil
	}
	return "", errors.New("view: no ffmpeg was found for video: set RIG_FFMPEG or settings.ffmpeg, or put ffmpeg on PATH")
}

func probe(ctx context.Context, bin, path string) (float64, float64, error) {
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "-hide_banner", "-nostdin", "-i", path)
	var tail tailBuffer
	cmd.Stdout = &tail
	cmd.Stderr = &tail
	_ = cmd.Run()
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	m := durationRE.FindStringSubmatch(tail.String())
	if m == nil {
		return 0, 0, fmt.Errorf("view: %s has no readable duration for ffmpeg: %s", path, firstLine(tail.String()))
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	sec, _ := strconv.ParseFloat(m[3], 64)
	dur := float64(h)*3600 + float64(min)*60 + sec
	if dur <= 0 {
		return 0, 0, fmt.Errorf("view: %s has no duration to sample: %s", path, firstLine(tail.String()))
	}
	var fps float64
	if f := fpsRE.FindStringSubmatch(tail.String()); f != nil {
		fps, _ = strconv.ParseFloat(f[1], 64)
	}
	return dur, fps, nil
}

func extract(ctx context.Context, bin, path string, at float64) (image.Image, error) {
	cctx, cancel := context.WithTimeout(ctx, frameTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin,
		"-hide_banner", "-nostdin",
		"-ss", strconv.FormatFloat(at, 'f', 3, 64),
		"-i", path,
		"-frames:v", "1",
		"-f", "image2pipe", "-c:v", "png", "-",
	)
	out := capWriter{limit: frameBufBytes}
	var tail tailBuffer
	cmd.Stdout = &out
	cmd.Stderr = &tail
	runErr := cmd.Run()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if out.overflow {
		return nil, fmt.Errorf("view: %s at %s: the extracted frame is over the %s cap; take a smaller frame or downscale the clip", path, stampLabel(at), humanBytes(frameBufBytes))
	}
	if runErr != nil {
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("view: %s at %s: ffmpeg did not finish within %s", path, stampLabel(at), frameTimeout)
		}
		return nil, fmt.Errorf("view: %s at %s: ffmpeg failed: %s", path, stampLabel(at), firstLine(tail.String()))
	}
	header := bytes.NewReader(out.buf.Bytes())
	cfg, _, err := image.DecodeConfig(header)
	if err != nil {
		return nil, fmt.Errorf("view: %s at %s: ffmpeg gave no decodable frame: %v", path, stampLabel(at), err)
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, fmt.Errorf("view: %s at %s is %dx%d, over the %d-pixel cap: downscale the clip first", path, stampLabel(at), cfg.Width, cfg.Height, maxPixels)
	}
	img, _, err := image.Decode(bytes.NewReader(out.buf.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("view: %s at %s: %v", path, stampLabel(at), err)
	}
	return img, nil
}

func parseStamp(s string) (float64, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, errors.New("not a timestamp")
	}
	var sec float64
	for _, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil || v < 0 {
			return 0, errors.New("not a timestamp")
		}
		sec = sec*60 + v
	}
	return sec, nil
}

func stampLabel(sec float64) string {
	d := math.Round(sec*10) / 10
	m := int(d) / 60
	s := d - float64(m*60)
	if s >= 60 {
		m++
		s -= 60
	}
	return fmt.Sprintf("%d:%04.1f", m, s)
}

func humanSeconds(sec float64) string {
	if sec >= 3600 {
		return fmt.Sprintf("%dh%02dm", int(sec)/3600, (int(sec)%3600)/60)
	}
	return fmt.Sprintf("%dm%02ds", int(sec)/60, int(sec)%60)
}

func firstLine(s string) string {
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return "no output"
}

type tailBuffer struct {
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > stderrTail {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-stderrTail:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }

type capWriter struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.limit {
		w.overflow = true
		return 0, errors.New("frame over the cap")
	}
	return w.buf.Write(p)
}
