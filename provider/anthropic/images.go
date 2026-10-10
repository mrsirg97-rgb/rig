package anthropic

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"strconv"

	"github.com/mrsirg97-rgb/rig/v2/imagemarker"
)

const (
	viewToolName = "view"

	maxInlineBlobBytes = 5 << 20
)

type blobStore struct {
	dir string
}

type blob struct {
	mime string
	data string
}

func (s *blobStore) source(ref imagemarker.Ref) (blob, string) {
	path := imagemarker.BlobPath(s.dir, ref.SHA256)
	if path == "" {
		return blob{}, "the marker names no blob address"
	}
	note := "the image blob " + shortAddress(ref.SHA256)
	info, err := os.Stat(path)
	if err != nil {
		return blob{}, note + " is missing"
	}
	if encoded := base64.StdEncoding.EncodedLen(int(info.Size())); encoded > maxInlineBlobBytes {
		return blob{}, note + " is " + strconv.Itoa(encoded) + " bytes encoded, over the " + strconv.Itoa(maxInlineBlobBytes) + "-byte inline bound"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return blob{}, note + " is unreadable"
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != ref.SHA256 {
		return blob{}, note + " no longer matches its address"
	}
	return blob{mime: ref.Mime, data: base64.StdEncoding.EncodeToString(data)}, ""
}

func shortAddress(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
