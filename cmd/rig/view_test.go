package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/models"
)

func visionRow() models.Model {
	m := defaultRow()
	m.ID = "seeing"
	m.Vision = true
	return m
}

func tableWith(t *testing.T, rows ...models.Model) models.Table {
	t.Helper()
	tbl, err := models.New(rows...)
	if err != nil {
		t.Fatal(err)
	}
	return tbl
}

func wiredToolNames(k interface{ SortedToolNames() []string }) string {
	return strings.Join(k.SortedToolNames(), ",")
}

func TestViewIsRegisteredOnlyWhenTheRowHasVision(t *testing.T) {
	text := testRoot(nullFrontend{})
	if got := wiredToolNames(wire(text)); strings.Contains(got, "view") {
		t.Fatalf("a non-vision row must not be offered view: %s", got)
	}

	seeing := testRoot(nullFrontend{})
	seeing.row = visionRow()
	seeing.runtime = tableWith(t, defaultRow(), visionRow())
	if got := wiredToolNames(wire(seeing)); !strings.Contains(got, "view,") && !strings.HasSuffix(got, ",view") {
		t.Fatalf("a vision row must be offered view among its natives: %s", got)
	}
}

func TestTheWireToolPrefixGrowsByViewAndNothingElse(t *testing.T) {
	text := wire(testRoot(nullFrontend{}))
	seeing := testRoot(nullFrontend{})
	seeing.row = visionRow()
	seeing.runtime = tableWith(t, defaultRow(), visionRow())
	vision := wire(seeing)

	if len(vision.Tools) != len(text.Tools)+1 {
		t.Fatalf("view must be the only difference: %d tools vs %d", len(vision.Tools), len(text.Tools))
	}
	textNames := map[string]bool{}
	for _, tool := range text.Tools {
		textNames[tool.Name()] = true
	}
	grew := 0
	for _, tool := range vision.Tools {
		if !textNames[tool.Name()] {
			grew++
			if tool.Name() != "view" {
				t.Fatalf("the vision table grew by %q", tool.Name())
			}
		}
	}
	if grew != 1 {
		t.Fatalf("the vision table must grow by view alone, grew by %d", grew)
	}
}

func TestViewSpecIsPinned(t *testing.T) {
	seeing := testRoot(nullFrontend{})
	seeing.row = visionRow()
	seeing.runtime = tableWith(t, defaultRow(), visionRow())
	var spec struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Schema      json.RawMessage `json:"schema"`
	}
	for _, tool := range wire(seeing).Tools {
		if tool.Name() == "view" {
			spec.Name, spec.Description, spec.Schema = tool.Name(), tool.Description(), tool.Schema()
		}
	}
	if spec.Name != "view" {
		t.Fatal("view is not registered for a vision row")
	}
	b, err := json.Marshal([]any{spec.Name, spec.Description, string(spec.Schema)})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	const golden = "173c772e06b76b81067c945761a3613b620d35205071e1050e2743c21004f621"
	if got := hex.EncodeToString(sum[:]); got != golden {
		t.Fatalf("the view spec changed: sha256 %s (want %s) — its description and schema are wire bytes; update the golden deliberately", got, golden)
	}
}

func TestViewIsConcurrentAndNeverMutating(t *testing.T) {
	if !concurrentNatives["view"] {
		t.Fatal("view is read-only: it runs beside its admitted neighbours")
	}
	if mutatingNatives["view"] {
		t.Fatal("view never writes outside its own store: manual mode must not pause on it")
	}
	r := testRoot(nullFrontend{})
	r.row = visionRow()
	r.runtime = tableWith(t, defaultRow(), visionRow())
	r.natives = map[string]bool{}
	for _, n := range effectiveNativeNames(nil) {
		r.natives[n] = true
	}
	if r.isMutating("view") {
		t.Fatal("the approval gate must never ask for view")
	}
}

func TestTheModelSwitchMovesViewWithTheRow(t *testing.T) {
	r := testRoot(nullFrontend{})
	r.runtime = tableWith(t, defaultRow(), visionRow())
	r.row = defaultRow()
	r.activeID = "local"
	wire(r)
	if names := liveNames(r); strings.Contains(names, "view") {
		t.Fatalf("the live table must not carry view for a text row: %s", names)
	}
	if _, err := r.switchModel(t.Context(), "seeing"); err != nil {
		t.Fatalf("switchModel: %v", err)
	}
	if names := liveNames(r); !strings.Contains(names, "view") {
		t.Fatalf("switching to a vision row must offer view on the next turn: %s", names)
	}
	if _, err := r.switchModel(t.Context(), "local"); err != nil {
		t.Fatalf("switchModel back: %v", err)
	}
	if names := liveNames(r); strings.Contains(names, "view") {
		t.Fatalf("switching away must take view with the row: %s", names)
	}
}

func liveNames(r *root) string {
	var out []string
	for _, spec := range r.live.Specs() {
		out = append(out, spec.Name)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestNativeNamesCarryViewAfterTheReadOnlyGroup(t *testing.T) {
	for i, n := range nativeToolNames {
		if n != "view" {
			continue
		}
		if i == 0 || nativeToolNames[i-1] != "grep" {
			t.Fatalf("view belongs with the read-only filesystem group: %v", nativeToolNames)
		}
		return
	}
	t.Fatalf("view is not a native: %v", nativeToolNames)
}

func TestViewThroughTheBinarySendsAPNGDataURL(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(cfgDir(t, home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir(t, home), "models.json"),
		[]byte(`[{"id": "local", "vision": true}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	png := gradientPNG(t, 400, 300)
	if err := os.WriteFile(filepath.Join(work, "shot.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}

	ps := &pluginSrv{replies: []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"id":"c1","type":"function","function":{"name":"view","arguments":"{\"path\":\"shot.png\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
			"data: [DONE]\n\n",
		`data: {"choices":[{"delta":{"content":"a blue-green gradient"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}` + "\n\n" +
			"data: [DONE]\n\n",
	}}
	srv := newPluginSrv(t, ps)

	bin := buildBin(t, t.TempDir())
	cmd := exec.Command(bin, "-p", "what is in this screenshot?", "-base-url", srv.URL+"/v1")
	cmd.Dir = work
	cmd.Env = rigEnv(home, "")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the run must succeed: %v\n%s", err, out)
	}

	if len(bodiesAll(ps)) != 2 {
		t.Fatalf("requests = %d, want the tool call and the answer\n%s", len(bodiesAll(ps)), out)
	}

	toolMsg := toolLinesOf(t, bodiesAll(ps)[1])
	if !strings.HasPrefix(toolMsg, "[[rig:image ") {
		t.Fatalf("the transcript's tool message = %q, want the marker line", toolMsg)
	}

	ref := imagePartOf(t, bodiesAll(ps)[1])
	if !strings.HasPrefix(ref, "data:image/png;base64,") {
		t.Fatalf("the image part = %.60q, want a PNG data URL", ref)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ref, "data:image/png;base64,"))
	if err != nil {
		t.Fatalf("the data URL must carry base64: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the data URL must carry a decodable image: %v", err)
	}
	if cfg.Width != 400 || cfg.Height != 300 {
		t.Fatalf("the sent image is %dx%d, want the source inside the bound at its own size", cfg.Width, cfg.Height)
	}

	sum := sha256.Sum256(raw)
	blob := filepath.Join(cfgDir(t, home), "blobs", hex.EncodeToString(sum[:]))
	if _, err := os.Stat(blob); err != nil {
		t.Fatalf("the blob must sit at the marker's address under the rig home: %v", err)
	}
}

func toolLinesOf(t *testing.T, body []byte) string {
	t.Helper()
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal the captured body: %v", err)
	}
	last := ""
	for _, m := range req.Messages {
		if m.Role != "tool" {
			continue
		}
		var text string
		if err := json.Unmarshal(m.Content, &text); err != nil {
			t.Fatalf("a tool message must stay a string content: %s", m.Content)
		}
		last = text
	}
	return last
}

func imagePartOf(t *testing.T, body []byte) string {
	t.Helper()
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal the captured body: %v", err)
	}
	for _, m := range req.Messages {
		if m.Role != "user" {
			continue
		}
		var parts []struct {
			Type     string `json:"type"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		if err := json.Unmarshal(m.Content, &parts); err != nil {
			continue
		}
		for _, p := range parts {
			if p.Type == "image_url" {
				return p.ImageURL.URL
			}
		}
	}
	return ""
}

func gradientPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			im.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
