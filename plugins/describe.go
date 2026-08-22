package plugins

import (
	"os"
	"strings"
)

func DescriptionOf(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return StaticDescription(string(data))
}

func StaticDescription(src string) string {
	idx := strings.Index(src, "DESCRIPTION")
	if idx == -1 {
		return ""
	}
	rest := src[idx+len("DESCRIPTION"):]
	eq := strings.IndexByte(rest, '=')
	if eq == -1 {
		return ""
	}
	rest = strings.TrimLeft(rest[eq+1:], " \t")
	paren := false
	if strings.HasPrefix(rest, "(") {
		paren = true
		rest = rest[1:]
	}
	var parts []string
	for {
		rest = strings.TrimLeft(rest, " \t\r\n")
		if rest == "" {
			break
		}
		if rest[0] == '#' {
			if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
				rest = rest[nl+1:]
				continue
			}
			break
		}
		if rest[0] == 'f' || rest[0] == 'r' || rest[0] == 'b' {
			if len(rest) > 1 && (rest[1] == '"' || rest[1] == '\'') {
				rest = rest[1:]
			}
		}
		if rest[0] != '"' && rest[0] != '\'' {
			break
		}
		lit, after, ok := stringLiteral(rest)
		if !ok {
			break
		}
		parts = append(parts, lit)
		rest = after
		if !paren {
			break
		}
	}
	return strings.Join(parts, "")
}

func stringLiteral(s string) (string, string, bool) {
	q := s[0]
	if len(s) >= 3 && s[1] == q && s[2] == q {
		quote := s[:3]
		end := strings.Index(s[3:], quote)
		if end == -1 {
			return "", "", false
		}
		return s[3 : 3+end], s[3+end+3:], true
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s):
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte(s[i])
			}
		case c == q:
			return b.String(), s[i+1:], true
		case c == '\n':
			return "", "", false
		default:
			b.WriteByte(c)
		}
	}
	return "", "", false
}
