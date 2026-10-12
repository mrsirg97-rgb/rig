package room

import "hash/fnv"

func KeyOf(model string) int64 {
	h := fnv.New64a()
	h.Write([]byte(model))
	return int64(h.Sum64() &^ (1 << 63))
}

func TableKey() int64 { return KeyOf("models-table") }
