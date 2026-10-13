package room

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

const servedTail = 64 * 1024

type Served struct {
	At              time.Time `json:"at"`
	Priority        int       `json:"priority"`
	Prompt          int       `json:"prompt"`
	Cached          int       `json:"cached"`
	Completion      int       `json:"completion"`
	PromptPerSecond float64   `json:"prompt_per_second"`
	DecodePerSecond float64   `json:"decode_per_second"`
}

func servedPath(home, model string) string {
	return filepath.Join(home, "logs", model+".served")
}

func Record(home, model string, s Served) error {
	line, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(servedPath(home, model), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func Recent(home, model string, n int) []Served {
	out := []Served{}
	f, err := os.Open(servedPath(home, model))
	if err != nil {
		return out
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return out
	}
	from := info.Size() - servedTail
	if from < 0 {
		from = 0
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return out
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return out
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if from > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for _, line := range lines {
		var s Served
		if json.Unmarshal(line, &s) == nil {
			out = append(out, s)
		}
	}
	return out
}
