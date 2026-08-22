package web

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/plugins"
)

type Plugin struct {
	Name        string
	Description string
	File        string
	Pending     bool
}

func listPlugins(home string) (loaded, pending, disabled []Plugin, err error) {
	files, err := plugins.List(home)
	if err != nil {
		return nil, nil, nil, err
	}
	loaded = make([]Plugin, 0, len(files))
	for _, f := range files {
		loaded = append(loaded, pluginRow(f, false))
	}
	pfiles, err := plugins.Zone(home, "pending")
	if err != nil {
		return nil, nil, nil, err
	}
	pending = make([]Plugin, 0, len(pfiles))
	for _, f := range pfiles {
		pending = append(pending, pluginRow(f, true))
	}
	dfiles, err := plugins.Zone(home, "disabled")
	if err != nil {
		return nil, nil, nil, err
	}
	disabled = make([]Plugin, 0, len(dfiles))
	for _, f := range dfiles {
		disabled = append(disabled, pluginRow(f, false))
	}
	return loaded, pending, disabled, nil
}

func pluginRow(file string, pending bool) Plugin {
	name := strings.TrimSuffix(filepath.Base(file), ".py")
	return Plugin{Name: name, Description: descriptionOf(file), File: file, Pending: pending}
}

func descriptionOf(file string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	return extractDescription(string(data))
}

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
