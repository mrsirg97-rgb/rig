package file

import (
	"sync"
)

type Indexer interface {
	Touch(path string)
}

var indexMu sync.Mutex

var indexer Indexer

func SetIndexer(i Indexer) {
	indexMu.Lock()
	indexer = i
	indexMu.Unlock()
}

func touchIndex(path string) {
	indexMu.Lock()
	i := indexer
	indexMu.Unlock()
	if i != nil {
		i.Touch(path)
	}
}
