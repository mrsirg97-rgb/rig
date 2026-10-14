package main

import (
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/models"
)

func TestTheRuntimeTableServesTheBaseRowOnASwitch(t *testing.T) {
	base, err := models.New(
		defaultRow(),
		models.Model{Role: models.RoleInteractive, ID: "other", Window: 32768, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384},
	)
	if err != nil {
		t.Fatal(err)
	}
	launch := defaultRow()
	launch.Window = 999999
	table := runtimeTable(base, "local", launch)
	got, ok := table.Get("local")
	if !ok || got.Window != 999999 {
		t.Fatalf("the launch row carries the launch env overlay: got %+v", got)
	}
	got, ok = table.Get("other")
	if !ok || got.Window != 32768 {
		t.Fatalf("a later switch serves the base row, the launch overlay is not a dial: got %+v", got)
	}
}
