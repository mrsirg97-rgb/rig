package tui_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrsirg97-rgb/rig/frontend/tui"
	"github.com/mrsirg97-rgb/rig/imagemarker"
)

const viewSHA = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"

func viewMarker(t *testing.T, src string, w, h, ow, oh, bytes int) string {
	t.Helper()
	return imagemarker.Format(imagemarker.Ref{
		SHA256: viewSHA, Mime: "image/png",
		W: w, H: h, OrigW: ow, OrigH: oh, Bytes: bytes, Src: src,
	})
}

func TestViewRowShowsPathBothDimensionsAndSize(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	content := viewMarker(t, "/home/ng/shot.png", 1568, 882, 2560, 1440, 421888)
	got := tui.RenderToolBlock(th, "view", json.RawMessage(`{"path":"shot.png"}`), content, false, 120*time.Millisecond)

	for _, want := range []string{"view", "shot.png", "2560x1440 -> 1568x882", "412 KB"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the view row must carry %q:\n%s", want, got)
		}
	}
	want := th.Paint("accent", "●") + " " + th.Paint("accent", "view") +
		th.Paint("dim", " · ") + th.Paint("text", "shot.png · 2560x1440 -> 1568x882 · 412 KB")
	if open := firstLine(got); open != want {
		t.Fatalf("opening = %q, want %q", open, want)
	}
}

func TestViewRowNeverPrintsTheMarkerOrAPicture(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	content := viewMarker(t, "/home/ng/shot.png", 1568, 882, 2560, 1440, 421888)
	got := tui.RenderToolBlock(th, "view", json.RawMessage(`{"path":"/home/ng/shot.png"}`), content, false, time.Second)
	for _, banned := range []string{"[[rig:image", "sha256=", "data:image", "image/png"} {
		if strings.Contains(got, banned) {
			t.Fatalf("no inline rendering and no raw marker on the row (%q):\n%s", banned, got)
		}
	}
	if strings.Contains(got, viewSHA) {
		t.Fatalf("the address is the store's business, not the row's:\n%s", got)
	}
}

func TestViewRowDropsTheArrowWhenNothingWasRescaled(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	content := viewMarker(t, "/home/ng/shot.png", 800, 600, 800, 600, 1024)
	got := tui.RenderToolBlock(th, "view", json.RawMessage(`{"path":"shot.png"}`), content, false, time.Second)
	if strings.Contains(got, "->") {
		t.Fatalf("an image sent at its own size needs no arrow:\n%s", got)
	}
	if !strings.Contains(got, "800x600") || !strings.Contains(got, "1 KB") {
		t.Fatalf("the row still shows the dimensions and the bytes:\n%s", got)
	}
}

func TestViewRowSurvivesAReplyThatIsNotAMarker(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	got := tui.RenderToolBlock(th, "view", json.RawMessage(`{"path":"shot.png"}`), "view: not an image", true, time.Second)
	if !strings.Contains(got, "view") || !strings.Contains(got, "shot.png") {
		t.Fatalf("a refusal still prints its row: %q", got)
	}
	if strings.Contains(got, "->") || strings.Contains(got, " KB") {
		t.Fatalf("a refusal carries no dimensions: %q", got)
	}
}

func TestViewRowSizesAreHumanReadable(t *testing.T) {
	th, err := tui.ResolveTheme("oled", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		bytes int
		want  string
	}{
		{512, "512 B"},
		{421888, "412 KB"},
		{3145728, "3.0 MB"},
	} {
		content := viewMarker(t, "/home/ng/shot.png", 1568, 784, 2000, 1000, c.bytes)
		got := tui.RenderToolBlock(th, "view", json.RawMessage(`{"path":"shot.png"}`), content, false, time.Second)
		if !strings.Contains(got, c.want) {
			t.Fatalf("%d bytes must read as %s:\n%s", c.bytes, c.want, got)
		}
	}
}
