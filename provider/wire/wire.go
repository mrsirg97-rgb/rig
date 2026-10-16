// Package wire is the shared plumbing of the two provider twins: the SSE
// line reader, the retry backoff, the blob verification preamble, and the
// address shortener. It carries no request or response shapes — those stay
// per wire.
package wire

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mrsirg97-rgb/rig/v3/imagemarker"
)

const (
	// HeaderTimeoutOff is the sentinel that means "no header timeout": one
	// home for both twins; the root names it when wiring a row.
	HeaderTimeoutOff time.Duration = -1
	// DefaultHeaderTimeout is the wait both twins embed for a response
	// header to arrive.
	DefaultHeaderTimeout = 5 * time.Minute
	// MaxRetryAfterWait caps a server-named Retry-After: a server asking
	// for longer than the header timeout is asking to be refused.
	MaxRetryAfterWait = DefaultHeaderTimeout
)

// SSEData reads one SSE data line: the payload after "data:", and false for
// any other field line (event:, id:, retry:) — those carry no payload and
// the twins skip them.
func SSEData(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "data:")), true
}

// Backoff is the retry wait: a server-named Retry-After (seconds) wins,
// capped at MaxRetryAfterWait; otherwise exponential with jitter.
func Backoff(base time.Duration, jitter func() float64, attempts int, h http.Header) time.Duration {
	if v := h.Get("Retry-After"); v != "" {
		if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && n >= 0 {
			if n >= float64(MaxRetryAfterWait/time.Second) {
				return MaxRetryAfterWait
			}
			wait := time.Duration(n * float64(time.Second))
			if wait > MaxRetryAfterWait {
				wait = MaxRetryAfterWait
			}
			return wait
		}
	}
	return time.Duration(float64(base*(1<<(attempts-1))) * (1 + jitter()))
}

// ShortAddress is the address a refusal names: the first 12 hex characters.
func ShortAddress(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// VerifyBlob walks the preamble both twins run before shipping a marked
// image: the blob must exist, fit the inline bound (measured encoded or
// raw, per the API's own limit), read, and still hash to its address. It
// returns the bytes, the base64 form, and a note: a non-empty note is the
// refusal — the image stays out of the request and the note says why.
func VerifyBlob(dir string, ref imagemarker.Ref, maxBytes int64, encodedCap bool) (data []byte, encoded string, note string) {
	path := imagemarker.BlobPath(dir, ref.SHA256)
	if path == "" {
		return nil, "", "the marker names no blob address"
	}
	note = "the image blob " + ShortAddress(ref.SHA256)
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", note + " is missing"
	}
	encodedLen := int64(base64.StdEncoding.EncodedLen(int(info.Size())))
	if encodedCap && encodedLen > maxBytes {
		return nil, "", note + " is " + strconv.FormatInt(encodedLen, 10) + " bytes encoded, over the " + strconv.FormatInt(maxBytes, 10) + "-byte inline bound"
	}
	if !encodedCap && info.Size() > maxBytes {
		return nil, "", note + " is " + strconv.FormatInt(info.Size(), 10) + " bytes, over the " + strconv.FormatInt(maxBytes, 10) + "-byte inline bound"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", note + " is unreadable"
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != ref.SHA256 {
		return nil, "", note + " no longer matches its address"
	}
	return raw, base64.StdEncoding.EncodeToString(raw), ""
}
