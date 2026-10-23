package view_test

import (
	"context"
	"encoding/json"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/imagemarker"
	"github.com/mrsirg97-rgb/rig/v3/tool/view"
)

const maxVideoBytes = 512 << 20

func requireFFmpeg(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on PATH; the video cases need one (RIG_FFMPEG names it)")
	}
	return p
}

func testVideo(t *testing.T, dir string, size string, seconds string) string {
	t.Helper()
	bin := requireFFmpeg(t)
	p := filepath.Join(dir, "clip.mp4")
	cmd := exec.Command(bin, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=duration="+seconds+":size="+size+":rate=10",
		"-pix_fmt", "yuv420p", p)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the fixture video: %v\n%s", err, out)
	}
	return p
}

func videoArgs(t *testing.T, params map[string]any) []byte {
	t.Helper()
	params["action"] = "video"
	b, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decodeVideoBlob(t *testing.T, blobs, line string) image.Image {
	t.Helper()
	ref, ok := imagemarker.Parse(line)
	if !ok {
		t.Fatalf("the reply is not one marker line: %q", line)
	}
	img := decodeBlob(t, blobs, ref)
	if got := img.Bounds().Dx(); got != ref.W {
		t.Fatalf("the marker says w=%d but the blob is %dx%d", ref.W, got, img.Bounds().Dy())
	}
	return img
}

func TestAVideoRepliesOneSheetOfEvenlySpacedLabelledFrames(t *testing.T) {
	dir, blobs := realTemp(t), realTemp(t)
	clip := testVideo(t, dir, "320x240", "4")
	tl := view.New(blobs)

	first, err := tl.Exec(context.Background(), videoArgs(t, map[string]any{"path": clip}))
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	ref, ok := imagemarker.Parse(first)
	if !ok {
		t.Fatalf("a sheet is one marker line: %q", first)
	}
	if ref.Src != clip {
		t.Fatalf("src = %q, want the video's absolute path %q", ref.Src, clip)
	}
	if ref.Mime != "image/jpeg" {
		t.Fatalf("a video frame is lossy and opaque: mime = %q", ref.Mime)
	}
	if ref.W > maxSide || ref.H > maxSide {
		t.Fatalf("the sheet is %dx%d, over the %d bound", ref.W, ref.H, maxSide)
	}
	if ref.W != 2*320+3*4 || ref.H != 3*240+4*4 {
		t.Fatalf("6 frames of 320x240 tile 2x3: got %dx%d, want the grid's own size", ref.W, ref.H)
	}
	img := decodeVideoBlob(t, blobs, first)
	if img.Bounds().Dx() != ref.W || img.Bounds().Dy() != ref.H {
		t.Fatalf("the blob is %dx%d, the marker %dx%d", img.Bounds().Dx(), img.Bounds().Dy(), ref.W, ref.H)
	}
	if !hasInk(img) {
		t.Fatal("the sheet carries no timestamp labels")
	}

	again, err := tl.Exec(context.Background(), videoArgs(t, map[string]any{"path": clip}))
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if again != first {
		t.Fatalf("the same file and arguments must give the same blob:\n%s\n%s", first, again)
	}
}

func hasInk(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Min.Y+15; y++ {
		for x := b.Min.X; x < b.Min.X+120 && x < b.Max.X; x++ {
			c := img.At(x, y)
			r, g, bl, _ := c.RGBA()
			if r > 0x2000 || g > 0x2000 || bl > 0x2000 {
				return true
			}
		}
	}
	return false
}

func TestASheetOfLargeFramesStaysUnderTheBound(t *testing.T) {
	dir, blobs := realTemp(t), realTemp(t)
	clip := testVideo(t, dir, "2560x1440", "1")
	line, err := view.New(blobs).Exec(context.Background(), videoArgs(t, map[string]any{"path": clip}))
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	ref, ok := imagemarker.Parse(line)
	if !ok {
		t.Fatalf("marker: %q", line)
	}
	if ref.W > maxSide || ref.H > maxSide {
		t.Fatalf("the sheet is %dx%d, over the %d bound", ref.W, ref.H, maxSide)
	}
	img := decodeVideoBlob(t, blobs, line)
	if img.Bounds().Dx() != ref.W || img.Bounds().Dy() != ref.H {
		t.Fatalf("the blob is %dx%d, the marker %dx%d", img.Bounds().Dx(), img.Bounds().Dy(), ref.W, ref.H)
	}
}

func TestASingleAtFrameIsThatFrameAloneAtTheBound(t *testing.T) {
	dir, blobs := realTemp(t), realTemp(t)
	clip := testVideo(t, dir, "2560x1440", "2")
	tl := view.New(blobs)

	a, err := tl.Exec(context.Background(), videoArgs(t, map[string]any{"path": clip, "at": "1"}))
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	ra, ok := imagemarker.Parse(a)
	if !ok {
		t.Fatalf("marker: %q", a)
	}
	if ra.W != 1568 || ra.H != 882 {
		t.Fatalf("one frame rides the 1568 bound: %dx%d, want 1568x882", ra.W, ra.H)
	}
	if ra.OrigW != 2560 || ra.OrigH != 1440 {
		t.Fatalf("orig = %dx%d", ra.OrigW, ra.OrigH)
	}
	b, err := tl.Exec(context.Background(), videoArgs(t, map[string]any{"path": clip, "at": "00:00:01.5"}))
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if a == b {
		t.Fatal("two different timestamps of a changing clip must not be one blob")
	}
	again, err := tl.Exec(context.Background(), videoArgs(t, map[string]any{"path": clip, "at": "1"}))
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if again != a {
		t.Fatalf("the same timestamp must give the same blob:\n%s\n%s", a, again)
	}
}

func TestTheFrameCountCapNamesItselfAndTheWayAround(t *testing.T) {
	dir, blobs := realTemp(t), realTemp(t)
	clip := testVideo(t, dir, "320x240", "1")
	_, err := view.New(blobs).Exec(context.Background(), videoArgs(t, map[string]any{"path": clip, "frames": 40}))
	if err == nil || !strings.Contains(err.Error(), "1..12") || !strings.Contains(err.Error(), "at") {
		t.Fatalf("the frame-count refusal must name the cap and the way around it: %v", err)
	}
}

func TestTheVideoSourceCapRefusesBeforeFFmpegReadsIt(t *testing.T) {
	dir, blobs := realTemp(t), realTemp(t)
	big := filepath.Join(dir, "big.mp4")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxVideoBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, err = view.New(blobs).Exec(context.Background(), videoArgs(t, map[string]any{"path": big}))
	if err == nil || !strings.Contains(err.Error(), "video cap") || !strings.Contains(err.Error(), "512.0 MiB") {
		t.Fatalf("the source cap must name itself: %v", err)
	}
}

func TestTheDurationCapRefusesTheSheetAndOffersOneFrame(t *testing.T) {
	dir, blobs := realTemp(t), realTemp(t)
	bin := requireFFmpeg(t)
	long := filepath.Join(dir, "long.m4a")
	cmd := exec.Command(bin, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "anullsrc=r=8000:cl=mono", "-t", "4000", "-c:a", "aac", "-b:a", "8k", long)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the long fixture: %v\n%s", err, out)
	}
	_, err := view.New(blobs).Exec(context.Background(), videoArgs(t, map[string]any{"path": long}))
	if err == nil || !strings.Contains(err.Error(), "clip cap") || !strings.Contains(err.Error(), "at") {
		t.Fatalf("the duration cap must name itself and the way around it: %v", err)
	}
}

func TestTheAbsentFFmpegRefusalNamesTheKnob(t *testing.T) {
	dir, blobs := realTemp(t), realTemp(t)
	clip := testVideo(t, dir, "320x240", "1")
	t.Setenv("PATH", "")
	_, err := view.New(blobs).Exec(context.Background(), videoArgs(t, map[string]any{"path": clip}))
	if err == nil || !strings.Contains(err.Error(), "RIG_FFMPEG") || !strings.Contains(err.Error(), "settings.ffmpeg") {
		t.Fatalf("an absent ffmpeg names its knob: %v", err)
	}
}

func TestAConfiguredButMissingFFmpegRefusesByName(t *testing.T) {
	dir, blobs := realTemp(t), realTemp(t)
	clip := testVideo(t, dir, "320x240", "1")
	tl := view.NewWith(view.Config{Blobs: blobs, FFmpeg: filepath.Join(dir, "no-ffmpeg")})
	_, err := tl.Exec(context.Background(), videoArgs(t, map[string]any{"path": clip}))
	if err == nil || !strings.Contains(err.Error(), "not an executable") {
		t.Fatalf("a configured-but-missing ffmpeg must refuse by name: %v", err)
	}
}

func TestVideoRefusesBadStampsAndAMissingPath(t *testing.T) {
	dir, blobs := realTemp(t), realTemp(t)
	clip := testVideo(t, dir, "320x240", "1")
	tl := view.New(blobs)
	for _, at := range []string{"abc", "-2", "1:2:3:4"} {
		if _, err := tl.Exec(context.Background(), videoArgs(t, map[string]any{"path": clip, "at": at})); err == nil {
			t.Fatalf("at=%q must refuse", at)
		}
	}
	if _, err := tl.Exec(context.Background(), videoArgs(t, map[string]any{})); err == nil || !strings.Contains(err.Error(), "path required") {
		t.Fatalf("a video without a path refuses: %v", err)
	}
}

func TestTheVideoAndImageDoorsKeepOneMarkerContract(t *testing.T) {
	dir, blobs := realTemp(t), realTemp(t)
	clip := testVideo(t, dir, "320x240", "1")
	line, err := view.New(blobs).Exec(context.Background(), videoArgs(t, map[string]any{"path": clip, "at": "0.5"}))
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if strings.Count(line, "[[rig:image ") != 1 || strings.Contains(line, "\n") {
		t.Fatalf("one reply is one marker line: %q", line)
	}
	entries, err := os.ReadDir(blobs)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("one call writes one blob, got %d", len(entries))
	}
}
