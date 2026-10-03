package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrsirg97-rgb/rig/v2/models"

	"github.com/mrsirg97-rgb/rig/v2/tool"
)

var ErrResumeWithPrompt = errors.New("rig: -resume is not available with -p (one-shot stays one-shot)")
var ErrSessionIDWithResume = errors.New("rig: -session-id and -resume cannot be combined")

func checkOneShot(prompt, resumeID string) error {
	if prompt != "" && resumeID != "" {
		return ErrResumeWithPrompt
	}
	return nil
}

func checkSessionID(sessionID, resumeID string) error {
	if sessionID != "" && resumeID != "" {
		return ErrSessionIDWithResume
	}
	return nil
}

func userHome() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return os.Getenv("HOME")
}

var conditionalNatives = map[string]bool{"decide": true}

var nativeToolNames = func() []string {
	var out []string
	for _, name := range tool.Names() {
		if !conditionalNatives[name] {
			out = append(out, name)
		}
	}
	return out
}()

func effectiveNativeNames() []string {
	return append([]string{}, nativeToolNames...)
}

func registeredNativeNames(vision bool) []string {
	names := effectiveNativeNames()
	if vision {
		return names
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if name == "view" {
			continue
		}
		out = append(out, name)
	}
	return out
}

func rigHome() (string, error) {
	if v := os.Getenv("RIG_HOME"); v != "" {
		return v, nil
	}
	if h := userHome(); h == "" {
		return "", errors.New("cannot resolve the home directory (set $HOME or RIG_HOME)")
	} else {
		newHome := filepath.Join(h, ".rig")
		oldHome := filepath.Join(h, ".config", "rig")
		if fi, err := os.Stat(oldHome); err == nil && fi.IsDir() {
			if _, err := os.Stat(newHome); errors.Is(err, os.ErrNotExist) {
				if err := os.Rename(oldHome, newHome); err != nil {
					return "", fmt.Errorf("migrate the config home: %s -> %s: %v", oldHome, newHome, err)
				}
				fmt.Fprintf(os.Stderr, "rig: migrated the config home: %s -> %s\n", oldHome, newHome)
			} else if err == nil {
				fmt.Fprintf(os.Stderr, "rig: the old config home still exists: %s (the home here won: %s; merge or prune it by hand)\n", oldHome, newHome)
			}
		}
		return newHome, nil
	}
}

func tuiTrueColor() bool {
	ct := os.Getenv("COLORTERM")
	return ct == "truecolor" || ct == "24bit"
}

func splitCSV(csv string) []string {
	var out []string
	for _, part := range strings.Split(csv, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func execArgIndex(args []string) int {
	for i, a := range args[1:] {
		if a == "-exec" || strings.HasPrefix(a, "-exec=") {
			return i + 1
		}
	}
	return -1
}

func execDoor(args []string, spec string) int {
	if spec == "" {
		return -1
	}
	return execArgIndex(args)
}

func resolveModel(id string, table models.Table) models.Model {
	m, err := models.Resolve(table, id, os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		os.Exit(1)
	}
	return m
}
