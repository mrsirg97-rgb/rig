package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	tokenFileName = "serve.token"
	cookieName    = "rig_serve"
)

func tokenPath(home string) string {
	return filepath.Join(home, tokenFileName)
}

func LoadToken(home string) (string, error) {
	data, err := os.ReadFile(tokenPath(home))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" {
		return "", errors.New("serve: the token file is empty")
	}
	return tok, nil
}

func MintToken(home string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	if err := os.WriteFile(tokenPath(home), []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}

func EnsureToken(home string) (string, bool, error) {
	tok, err := LoadToken(home)
	if err != nil {
		return "", false, err
	}
	if tok != "" {
		return tok, false, nil
	}
	fresh, err := MintToken(home)
	if err != nil {
		return "", false, err
	}
	return fresh, true, nil
}

func tokenMatch(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func gate(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := bearerToken(r)
		fromQuery := false
		if got == "" {
			got = cookieToken(r)
		}
		if got == "" {
			got = r.URL.Query().Get("token")
			fromQuery = got != ""
		}
		if !tokenMatch(got, token) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if fromQuery {
			http.SetCookie(w, &http.Cookie{
				Name:     cookieName,
				Value:    token,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[len("Bearer "):])
	}
	return ""
}

func cookieToken(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}
