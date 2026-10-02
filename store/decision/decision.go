package decision

import (
	"path/filepath"

	decisionddl "github.com/mrsirg97-rgb/rig/v2/store/decision/ddl"
	decisionmeta "github.com/mrsirg97-rgb/rig/v2/store/decision/metadata"
)

const SchemaVersion = 2

func DDL() []string { return decisionddl.Statements() }

func Statements() []string {
	out := decisionddl.Statements()
	return append(out, decisionmeta.ExtraStatements()...)
}

func FilePath(home string) string {
	return filepath.Join(home, "decision", "decision.sqlite")
}
