package main

import (
	"reflect"

	"github.com/mrsirg97-rgb/rig/v3/models"
)

func runtimeTable(t models.Table, active string, resolved models.Model) models.Table {
	if base, ok := t.Get(active); ok && reflect.DeepEqual(base, resolved) {
		return t
	}
	rows := make([]models.Model, 0, len(t.Known())+1)
	for _, id := range t.Known() {
		if id == active {
			continue
		}
		m, ok := t.Get(id)
		if !ok {
			panic("rig: runtime table: " + id + ": known without a readable row")
		}
		rows = append(rows, m)
	}
	rows = append(rows, resolved)
	t2, err := models.New(rows...)
	if err != nil {
		panic("rig: runtime table: " + err.Error())
	}
	return t2
}
