package config

import (
	"os"
	"path/filepath"
)

var workerToolNames = []string{"scheduler", "delegate"}

func appendWorkerTools(allow []string) []string {
	set := make(map[string]bool, len(allow))
	for _, n := range allow {
		set[n] = true
	}
	out := append([]string{}, allow...)
	for _, n := range workerToolNames {
		if !set[n] {
			out = append(out, n)
		}
	}
	return out
}

func workersRetired(dir string) (bool, error) {
	_, err := os.ReadFile(filepath.Join(dir, "workers.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, readErr(filepath.Join(dir, "workers.json"), err)
	}
	return true, nil
}
