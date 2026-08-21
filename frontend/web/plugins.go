package web

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/plugins"
)

// Plugin is one listing row (SPEC_SERVE): the loaded set or the pending
// zone, by name with the file's DESCRIPTION.
type Plugin struct {
	Name        string
	Description string
	File        string
	Pending     bool
}

// listPlugins reads the loaded set (plugins.List) and the pending zone
// (home/plugins/pending), each with the file's DESCRIPTION (SPEC_SERVE).
// A read-only description read, not discovery: no kernel, no execution.
func listPlugins(home string) (loaded, pending []Plugin, err error) {
	files, err := plugins.List(home)
	if err != nil {
		return nil, nil, err
	}
	loaded = make([]Plugin, 0, len(files))
	for _, f := range files {
		loaded = append(loaded, pluginRow(f, false))
	}
	pdir := filepath.Join(home, "plugins", "pending")
	entries, err := os.ReadDir(pdir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		return loaded, nil, nil
	}
	pending = make([]Plugin, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".py") {
			continue
		}
		pending = append(pending, pluginRow(filepath.Join(pdir, e.Name()), true))
	}
	return loaded, pending, nil
}

func pluginRow(file string, pending bool) Plugin {
	name := strings.TrimSuffix(filepath.Base(file), ".py")
	return Plugin{Name: name, Description: descriptionOf(file), File: file, Pending: pending}
}

// descriptionOf reads the file's DESCRIPTION (the plugin contract: a str),
// single- or triple-quoted. A read failure is an empty description, not an
// error: one bad file must not brick the listing.
func descriptionOf(file string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	return extractDescription(string(data))
}

// extractDescription pulls the DESCRIPTION value (the plugin contract):
// the token, the =, then a single- or triple-quoted string.
func extractDescription(src string) string {
	idx := strings.Index(src, "DESCRIPTION")
	if idx == -1 {
		return ""
	}
	rest := src[idx+len("DESCRIPTION"):]
	eq := strings.IndexByte(rest, '=')
	if eq == -1 {
		return ""
	}
	rest = strings.TrimSpace(rest[eq+1:])
	if len(rest) < 1 {
		return ""
	}
	q := rest[0]
	if q != '"' && q != '\'' {
		return ""
	}
	if len(rest) >= 3 && rest[1] == q && rest[2] == q {
		quote := string(rest[0:3])
		end := strings.Index(rest[3:], quote)
		if end == -1 {
			return ""
		}
		return rest[3 : 3+end]
	}
	end := 1
	for end < len(rest) {
		if rest[end] == '\\' {
			end += 2
			continue
		}
		if rest[end] == q {
			break
		}
		end++
	}
	if end >= len(rest) {
		return ""
	}
	return unquote(rest[1:end], q)
}

func unquote(body string, quote byte) string {
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' && i+1 < len(body) {
			i++
			switch body[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte(body[i])
			}
			continue
		}
		b.WriteByte(body[i])
	}
	return b.String()
}
