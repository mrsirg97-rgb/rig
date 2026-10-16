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
	HeaderTimeoutOff     time.Duration = -1
	DefaultHeaderTimeout               = 5 * time.Minute
	MaxRetryAfterWait                  = DefaultHeaderTimeout
)

func SSEData(line string) (string, bool) {
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "data:")), true
}

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

func ShortAddress(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

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
