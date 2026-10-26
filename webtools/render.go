package webtools

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

const (
	dirName       = "external"
	searxPort     = "8888"
	proxyPort     = "8889"
	subnet        = "172.30.30.0/24"
	gateway       = "172.30.30.1"
	valkeyVolume  = "rig-valkey-data"
	cacheVolume   = "rig-searxng-cache"
	proxyUser     = "rig"
	hexPassword   = 32
	hexSecret     = 40
	minPassword   = 16
	maxPassword   = 128
	searxUID      = "977:977"
	valkeyUID     = "999:1000"
	helperImage   = "alpine:3.23"
	unreleased    = "unreleased"
	valkeyPath    = "searxng/settings.yml"
	proxyConfPath = "tinyproxy/tinyproxy.conf"
)

func newHex(chars int) (string, error) {
	b := make([]byte, chars/2)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("webtools: no random bytes for the %d-hex value: %v", chars, err)
	}
	return hex.EncodeToString(b), nil
}

type render struct {
	pw      string
	secret  string
	version string
}

func (r render) slots() *strings.Replacer {
	version := r.version
	if version == "" {
		version = unreleased
	}
	return strings.NewReplacer(
		"{{PW}}", r.pw,
		"{{SECRET}}", r.secret,
		"{{VERSION}}", version,
		"{{SUBNET}}", subnet,
		"{{GATEWAY}}", gateway,
		"{{SEARX_PORT}}", searxPort,
		"{{PROXY_PORT}}", proxyPort,
	)
}

func (r render) apply(text string) string { return r.slots().Replace(text) }

func modeFor(rel string) os.FileMode {
	if rel == proxyConfPath {
		return 0o600
	}
	return 0o644
}

func lineValue(content, marker string) string {
	for _, l := range strings.Split(content, "\n") {
		if strings.HasPrefix(l, marker) {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(l, marker)), `"'`)
		}
	}
	return ""
}

func passwordOf(conf string) string { return lineValue(conf, "BasicAuth rig ") }

func secretOf(settings string) string { return lineValue(settings, "  secret_key: ") }

func badCharName(r rune) string {
	switch r {
	case ' ':
		return "space"
	case '"':
		return "quote"
	case '#':
		return "hash"
	case ',':
		return "comma"
	case '@':
		return "at sign"
	case ':':
		return "colon"
	}
	return fmt.Sprintf("%q character", r)
}

func passwordError(pw string) error {
	floor := fmt.Sprintf("want 16 to 128 characters of letters, digits, dot, underscore or dash (the BasicAuth line is unquoted, so anything else reaches the parser, and the same bytes go into settings.json): %d", minPassword)
	for _, r := range pw {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '.' || r == '_' || r == '-') {
			return fmt.Errorf("webtools: the proxy password carries a %s: %s", badCharName(r), floor)
		}
	}
	if len(pw) < minPassword {
		return fmt.Errorf("webtools: the proxy password is under the floor: %d characters, %s", len(pw), floor)
	}
	if len(pw) > maxPassword {
		return fmt.Errorf("webtools: the proxy password is over the ceiling: %d characters, %s", len(pw), floor)
	}
	return nil
}
