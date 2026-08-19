package tui

import (
	"strings"
	"unicode"
)

// The code highlight (SPEC_TUI 11, amended): a fenced block's lines
// paint by a small lexical pass, line-local like the rest of the
// markdown pass — keywords, strings, comments, numbers — for the
// languages a model emits in a chat. Not a grammar: a string or a block
// comment that spans lines loses its paint at the line break (the price
// of never buffering, named). An unknown info string paints the block
// dim as before. The slots are the palette's own: keywords accent,
// strings ember, comments dim, numbers reasoning-grey; the rest text.

// langKeywords is the keyword table per info string (the common
// spellings and aliases map to one table).
var langKeywords = map[string][]string{
	"go":     {"break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select", "struct", "switch", "type", "var", "nil", "true", "false", "iota", "error", "string", "int", "int64", "bool", "byte", "rune", "any"},
	"python": {"and", "as", "assert", "async", "await", "break", "class", "continue", "def", "del", "elif", "else", "except", "finally", "for", "from", "global", "if", "import", "in", "is", "lambda", "nonlocal", "not", "or", "pass", "raise", "return", "try", "while", "with", "yield", "None", "True", "False", "self"},
	"js":     {"async", "await", "break", "case", "catch", "class", "const", "continue", "default", "delete", "do", "else", "export", "extends", "finally", "for", "from", "function", "if", "import", "in", "instanceof", "let", "new", "of", "return", "static", "super", "switch", "this", "throw", "try", "typeof", "var", "void", "while", "yield", "null", "undefined", "true", "false", "interface", "type", "enum", "implements", "readonly", "as", "string", "number", "boolean"},
	"shell":  {"if", "then", "else", "elif", "fi", "for", "in", "do", "done", "while", "until", "case", "esac", "function", "return", "export", "local", "readonly", "exit", "echo", "cd", "set", "unset", "source", "alias"},
	"sql":    {"SELECT", "FROM", "WHERE", "AND", "OR", "NOT", "NULL", "IS", "IN", "AS", "ON", "JOIN", "LEFT", "RIGHT", "INNER", "OUTER", "GROUP", "BY", "ORDER", "LIMIT", "OFFSET", "INSERT", "INTO", "VALUES", "UPDATE", "SET", "DELETE", "CREATE", "TABLE", "INDEX", "PRIMARY", "KEY", "DROP", "ALTER", "DISTINCT", "COUNT", "SUM", "MAX", "MIN", "HAVING", "UNION", "CASE", "WHEN", "THEN", "ELSE", "END", "WITH", "RETURNING", "EXISTS", "BETWEEN", "LIKE"},
	"rust":   {"as", "async", "await", "break", "const", "continue", "crate", "dyn", "else", "enum", "extern", "fn", "for", "if", "impl", "in", "let", "loop", "match", "mod", "move", "mut", "pub", "ref", "return", "self", "Self", "static", "struct", "super", "trait", "type", "unsafe", "use", "where", "while", "true", "false", "Some", "None", "Ok", "Err"},
}

// langOf resolves an info string to a keyword table's name ("" for
// unknown: the block paints dim).
func langOf(info string) string {
	info = strings.ToLower(strings.TrimSpace(info))
	if i := strings.IndexAny(info, " \t{"); i >= 0 {
		info = info[:i]
	}
	switch info {
	case "go", "golang":
		return "go"
	case "python", "py", "python3":
		return "python"
	case "js", "javascript", "ts", "typescript", "tsx", "jsx", "mjs":
		return "js"
	case "sh", "bash", "shell", "zsh", "console":
		return "shell"
	case "sql", "sqlite", "postgres", "postgresql", "mysql":
		return "sql"
	case "rust", "rs":
		return "rust"
	case "json", "yaml", "yml", "toml":
		return "data" // strings and numbers only, no keywords
	}
	return ""
}

// lineComment is the comment opener per language.
func lineComment(lang string) string {
	switch lang {
	case "go", "js", "rust":
		return "//"
	case "python", "shell", "data":
		return "#"
	case "sql":
		return "--"
	}
	return ""
}

// highlightLine paints one line of a fenced block for lang (a name
// from langOf; "" paints the whole line dim). The line's leading
// indent is preserved unpainted.
func highlightLine(th Theme, lang, line string) string {
	if lang == "" {
		return th.Paint(SlotDim, line)
	}
	kw := map[string]bool{}
	for _, k := range langKeywords[lang] {
		kw[k] = true
	}
	caseless := lang == "sql"
	comment := lineComment(lang)
	rs := []rune(line)
	var out strings.Builder
	var cur strings.Builder
	curSlot := SlotText
	flush := func() {
		if cur.Len() > 0 {
			out.WriteString(th.Paint(curSlot, cur.String()))
			cur.Reset()
		}
	}
	emit := func(slot, text string) {
		if slot != curSlot {
			flush()
			curSlot = slot
		}
		cur.WriteString(text)
	}
	i := 0
	for i < len(rs) {
		r := rs[i]
		rest := string(rs[i:])
		switch {
		case comment != "" && strings.HasPrefix(rest, comment):
			emit(SlotDim, rest)
			i = len(rs)
		case r == '"' || r == '\'' || r == '`':
			// a string to the matching quote on the line (an escape
			// skips a char); unclosed runs to the line's end.
			j := i + 1
			for j < len(rs) && rs[j] != r {
				if rs[j] == '\\' && j+1 < len(rs) {
					j++
				}
				j++
			}
			if j < len(rs) {
				j++
			}
			emit(SlotEmber, string(rs[i:j]))
			i = j
		case unicode.IsDigit(r) && (i == 0 || !isWordRune(rs[i-1])):
			j := i
			for j < len(rs) && (unicode.IsDigit(rs[j]) || rs[j] == '.' || rs[j] == '_' || rs[j] == 'x' || (rs[j] >= 'a' && rs[j] <= 'f') || (rs[j] >= 'A' && rs[j] <= 'F')) {
				j++
			}
			emit(SlotReasoning, string(rs[i:j]))
			i = j
		case isWordRune(r) && !unicode.IsDigit(r):
			j := i
			for j < len(rs) && isWordRune(rs[j]) {
				j++
			}
			word := string(rs[i:j])
			key := word
			if caseless {
				key = strings.ToUpper(word)
			}
			if kw[key] {
				emit(SlotAccent, word)
			} else {
				emit(SlotText, word)
			}
			i = j
		default:
			emit(SlotText, string(r))
			i++
		}
	}
	flush()
	return out.String()
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
