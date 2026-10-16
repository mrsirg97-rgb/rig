package anthropic

import (
	"github.com/mrsirg97-rgb/rig/v3/imagemarker"
	"github.com/mrsirg97-rgb/rig/v3/provider/wire"
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
	_, encoded, note := wire.VerifyBlob(s.dir, ref, maxInlineBlobBytes, true)
	if note != "" {
		return blob{}, note
	}
	return blob{mime: ref.Mime, data: encoded}, ""
}
