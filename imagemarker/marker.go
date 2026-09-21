package imagemarker

import (
	"path/filepath"
	"strconv"
	"strings"
)

const (
	prefix   = "[[rig:image "
	suffix   = "]]"
	srcKey   = " src="
	fixedLen = 6
	StoreDir = "blobs"
)

type Ref struct {
	SHA256 string
	Mime   string
	W      int
	H      int
	OrigW  int
	OrigH  int
	Bytes  int
	Src    string
}

func Format(r Ref) string {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString("sha256=")
	b.WriteString(r.SHA256)
	b.WriteString(" mime=")
	b.WriteString(r.Mime)
	b.WriteString(" w=")
	b.WriteString(strconv.Itoa(r.W))
	b.WriteString(" h=")
	b.WriteString(strconv.Itoa(r.H))
	b.WriteString(" orig=")
	b.WriteString(strconv.Itoa(r.OrigW))
	b.WriteByte('x')
	b.WriteString(strconv.Itoa(r.OrigH))
	b.WriteString(" bytes=")
	b.WriteString(strconv.Itoa(r.Bytes))
	b.WriteString(srcKey)
	b.WriteString(r.Src)
	b.WriteString(suffix)
	return b.String()
}

func Parse(line string) (Ref, bool) {
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
		return Ref{}, false
	}
	if len(line) <= len(prefix)+len(suffix) {
		return Ref{}, false
	}
	body := line[len(prefix) : len(line)-len(suffix)]
	cut := strings.Index(body, srcKey)
	if cut < 0 {
		return Ref{}, false
	}
	src := body[cut+len(srcKey):]
	if src == "" {
		return Ref{}, false
	}
	fixed := strings.Split(body[:cut], " ")
	if len(fixed) != fixedLen {
		return Ref{}, false
	}
	var ref Ref
	if !matchSHA(&ref.SHA256, fixed[0]) || !matchMime(&ref.Mime, fixed[1]) {
		return Ref{}, false
	}
	if !matchInt(&ref.W, fixed[2], "w=") || !matchInt(&ref.H, fixed[3], "h=") {
		return Ref{}, false
	}
	if !matchDims(&ref.OrigW, &ref.OrigH, fixed[4]) {
		return Ref{}, false
	}
	if !matchInt(&ref.Bytes, fixed[5], "bytes=") {
		return Ref{}, false
	}
	if !keysInOrder(fixed) {
		return Ref{}, false
	}
	ref.Src = src
	return ref, true
}

func Find(content string) (Ref, bool) {
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if ref, ok := Parse(line); ok {
			return ref, true
		}
	}
	return Ref{}, false
}

func BlobPath(dir, sha string) string {
	if !IsAddress(sha) {
		return ""
	}
	return filepath.Join(dir, sha)
}

func BlobsDir(home string) string {
	return filepath.Join(home, StoreDir)
}

func IsAddress(sha string) bool {
	if len(sha) != 64 {
		return false
	}
	return allHex(sha)
}

var keyOrder = [...]string{"sha256", "mime", "w", "h", "orig", "bytes"}

func keysInOrder(fixed []string) bool {
	for i, key := range keyOrder {
		if !strings.HasPrefix(fixed[i], key+"=") {
			return false
		}
	}
	return true
}

func matchSHA(out *string, token string) bool {
	const key = "sha256="
	if !strings.HasPrefix(token, key) {
		return false
	}
	v := token[len(key):]
	if !IsAddress(v) {
		return false
	}
	*out = v
	return true
}

func matchMime(out *string, token string) bool {
	const key = "mime="
	if !strings.HasPrefix(token, key) {
		return false
	}
	v := token[len(key):]
	if !isMIME(v) {
		return false
	}
	*out = v
	return true
}

func matchInt(out *int, token, key string) bool {
	if !strings.HasPrefix(token, key) {
		return false
	}
	v, ok := positive(token[len(key):])
	if !ok {
		return false
	}
	*out = v
	return true
}

func matchDims(w, h *int, token string) bool {
	const key = "orig="
	if !strings.HasPrefix(token, key) {
		return false
	}
	a, b, found := strings.Cut(token[len(key):], "x")
	if !found {
		return false
	}
	aw, ok := positive(a)
	if !ok {
		return false
	}
	bh, ok := positive(b)
	if !ok {
		return false
	}
	*w, *h = aw, bh
	return true
}

func positive(s string) (int, bool) {
	if s == "" || len(s) > 10 {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func isMIME(v string) bool {
	top, sub, found := strings.Cut(v, "/")
	if !found || top == "" || sub == "" {
		return false
	}
	if strings.ContainsAny(v, " \t\n\r") {
		return false
	}
	return true
}

func allHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
