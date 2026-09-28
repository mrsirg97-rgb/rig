package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	blockTags  = []string{"script", "style", "noscript", "svg", "head"}
	commentRE  = regexp.MustCompile(`(?s)<!--.*?-->`)
	blockEndRE = regexp.MustCompile(`(?i)</(p|div|li|tr|h[1-6]|blockquote|pre|section|article)>|<br\s*\/?>`)
	tagRE      = regexp.MustCompile(`<[^>]+>`)
	inlineWSE  = regexp.MustCompile(`[ \t\r]+`)
	blank3RE   = regexp.MustCompile(`\n{3,}`)
)

func HtmlToText(html string) string {
	for _, tag := range blockTags {
		re := regexp.MustCompile(`(?is)<` + tag + `\b.*?</` + tag + `>`)
		html = re.ReplaceAllString(html, " ")
	}
	html = commentRE.ReplaceAllString(html, " ")
	html = blockEndRE.ReplaceAllString(html, "\n")
	html = tagRE.ReplaceAllString(html, " ")
	html = decodeEntities(html)
	lines := strings.Split(html, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(inlineWSE.ReplaceAllString(l, " "))
	}
	return strings.TrimSpace(blank3RE.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

var entityRE = regexp.MustCompile(`&(#x?[0-9a-fA-F]+|\w+);`)

var entities = map[string]string{
	"amp":  "&",
	"lt":   "<",
	"gt":   ">",
	"quot": `"`,
	"apos": "'",
	"nbsp": " ",
	"#39":  "'",
}

func decodeEntities(text string) string {
	return entityRE.ReplaceAllStringFunc(text, func(m string) string {
		name := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(m, "&"), ";"))
		if v, ok := entities[name]; ok {
			return v
		}
		if strings.HasPrefix(name, "#x") {
			if n, err := strconv.ParseUint(name[2:], 16, 32); err == nil && n > 0 {
				return string(rune(n))
			}
			return m
		}
		if strings.HasPrefix(name, "#") {
			if n, err := strconv.ParseUint(name[1:], 10, 32); err == nil && n > 0 {
				return string(rune(n))
			}
			return m
		}
		return m
	})
}

func CapChars(text string, max int) string {
	r := []rune(text)
	if len(r) <= max {
		return text
	}
	return string(r[:max]) + fmt.Sprintf(
		"\n\n[TRUNCATED: showing %d of %d chars. Refetch with a larger maxChars or a more specific URL.]",
		max, len(r))
}

func runTrafilatura(ctx context.Context, bin, html string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, trafilaturaTime)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = strings.NewReader(html)
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", errors.New(strings.SplitN(detail, "\n", 2)[0])
	}
	if stdout.Len() > trafilaturaCap {
		stdout.Truncate(trafilaturaCap)
	}
	return stdout.String(), nil
}

func ExtractReadable(ctx context.Context, html string, trafilatura *string) (string, string) {
	bin := DefaultTrafilatura()
	explicit := trafilatura != nil
	if explicit {
		bin = *trafilatura
	}
	if bin == "" {
		return HtmlToText(html), "[trafilatura unavailable; stdlib text pass used]"
	}
	out, err := runTrafilatura(ctx, bin, html)
	if err != nil {
		return HtmlToText(html), "[trafilatura failed (" + err.Error() + "); stdlib text pass used]"
	}
	if strings.TrimSpace(out) == "" {
		return HtmlToText(html), "[trafilatura produced no output; stdlib text pass used]"
	}
	return strings.TrimSpace(out), ""
}
