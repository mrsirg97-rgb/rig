package diff

import (
	"fmt"
	"strings"
	"testing"
)

func chunks(raw string) [][]byte {
	var out [][]byte
	sizes := []int{1, 3, 17, 4096, 32768}
	i := 0
	for len(raw) > 0 {
		n := sizes[i%len(sizes)]
		i++
		if n > len(raw) {
			n = len(raw)
		}
		out = append(out, []byte(raw[:n]))
		raw = raw[n:]
	}
	return out
}

func TestFilesCaptureMatchesTheWholeBufferReply(t *testing.T) {
	reference := func(raw string) string {
		body := strings.TrimSuffix(raw, "\n")
		if body == "" {
			return "no changes"
		}
		ln := strings.Split(body, "\n")
		if len(ln) <= capLines {
			return body
		}
		k := len(ln) - (capLines - 1)
		return strings.Join(ln[:capLines-1], "\n") + "\n… " + fmt.Sprint(k) + " more lines"
	}
	cases := map[string]string{
		"empty":             "",
		"one newline":       "\n",
		"one line":          "a\n",
		"one line no nl":    "a",
		"two lines":         "a\nb\n",
		"two lines no nl":   "a\nb",
		"blank middle":      "a\n\nb\n",
		"double trailing":   "a\n\n",
		"triple newline":    "\n\n\n",
		"at the cap":        strings.Repeat("x\n", capLines),
		"one over the cap":  strings.Repeat("x\n", capLines+1),
		"cap minus one":     strings.Repeat("x\n", capLines-1),
		"far over the cap":  strings.Repeat("x\n", capLines+120),
		"no nl far over":    strings.Repeat("x\n", capLines+119) + "tail",
		"long line kept":    strings.Repeat("a", 1<<16) + "\n" + strings.Repeat("b\n", capLines+5),
		"long line dropped": strings.Repeat("x\n", capLines+1) + strings.Repeat("y", 1<<20) + "\n",
		"crs split lines":   strings.Repeat(strings.Repeat("q", 100)+"\n", capLines+9),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			c := &lineCap{}
			for _, chunk := range chunks(raw) {
				n, err := c.Write(chunk)
				if err != nil || n != len(chunk) {
					t.Fatalf("write: %d %v", n, err)
				}
			}
			if got, want := c.reply(), reference(raw); got != want {
				t.Fatalf("reply drift:\n got %d bytes %q\nwant %d bytes %q", len(got), got, len(want), want)
			}
		})
	}
}
