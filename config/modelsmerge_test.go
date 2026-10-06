package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v2/models"
)

const (
	mergeTablePath = "config/models.json (embedded)"
	mergeOperator  = "/home/operator/.rig/models.json"
	localTableRow  = `{"id": "local", "window": 65536, "maxTokens": 8192, "reserve": 8192, "keepRecent": 16384, "role": "interactive", "efforts": ["low", "medium", "xhigh"]}`
	brainTableRow  = `{"id": "brain", "window": 262144, "maxTokens": 16384, "reserve": 16384, "keepRecent": 32768}`
	workerTableRow = `{"id": "hand", "window": 32768, "maxTokens": 4096, "reserve": 4096, "keepRecent": 8192, "role": "worker"}`
)

func localRow() models.Model {
	return models.Model{ID: "local", Window: 65536, MaxTokens: 8192, Reserve: 8192, KeepRecent: 16384, Role: models.RoleInteractive, Efforts: []string{"low", "medium", "xhigh"}}
}

func brainRow() models.Model {
	return models.Model{ID: "brain", Window: 262144, MaxTokens: 16384, Reserve: 16384, KeepRecent: 32768, Role: models.RoleInteractive}
}

func mergeTableOf(t *testing.T, rows ...string) models.Table {
	t.Helper()
	docs, err := parseRows([]byte("["+strings.Join(rows, ",")+"]"), mergeTablePath)
	if err != nil {
		t.Fatalf("the table side: %v", err)
	}
	tbl, err := mergeRows(models.Table{}, docs, mergeTablePath)
	if err != nil {
		t.Fatalf("the table side: %v", err)
	}
	return tbl
}

func merged(t *testing.T, tbl models.Table, rows ...string) (models.Table, error) {
	t.Helper()
	docs, err := parseRows([]byte("["+strings.Join(rows, ",")+"]"), mergeOperator)
	if err != nil {
		return models.Table{}, err
	}
	return mergeRows(tbl, docs, mergeOperator)
}

func mergedOK(t *testing.T, tbl models.Table, rows ...string) models.Table {
	t.Helper()
	out, err := merged(t, tbl, rows...)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	return out
}

func TestMergeOverAnEmptyTableIsTheOperatorsTableVerbatim(t *testing.T) {
	tbl := mergedOK(t, mergeTableOf(t), localTableRow, brainTableRow)
	if got := tbl.Known(); !reflect.DeepEqual(got, []string{"brain", "local"}) {
		t.Fatalf("known = %v, want the operator's two rows in id order", got)
	}
	for _, want := range []models.Model{localRow(), brainRow()} {
		got, ok := tbl.Get(want.ID)
		if !ok {
			t.Fatalf("the operator's row %q is gone", want.ID)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("row %s = %+v, want the operator's row untouched (+%v)", want.ID, got, want)
		}
	}
}

func TestMergeOfAnEmptyFileOverAnEmptyTableIsAnEmptyTable(t *testing.T) {
	tbl := mergedOK(t, mergeTableOf(t))
	if got := tbl.Known(); len(got) != 0 {
		t.Fatalf("known = %v, want no rows: nothing wrote one", got)
	}
}

func TestMergeOverlaysEachSetFieldAndKeepsTheUnsetOnTheTableRow(t *testing.T) {
	tbl := mergedOK(t, mergeTableOf(t, localTableRow), `{"id": "local", "window": 32768}`)
	m, ok := tbl.Get("local")
	if !ok {
		t.Fatal("the row the file names is gone")
	}
	want := localRow()
	want.Window = 32768
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("overlay = %+v, want the file's window over the row's fields (+%+v)", m, want)
	}
}

func TestMergeOverlaysTheHostedFieldsOntoTheTableRow(t *testing.T) {
	tbl := mergedOK(t, mergeTableOf(t, localTableRow), `{"id": "local", "baseUrl": "https://api.deepseek.com", "provider": "deepseek", "retries": 5}`)
	m, _ := tbl.Get("local")
	want := localRow()
	want.Remote = true
	want.Provider = "deepseek"
	want.BaseURL = "https://api.deepseek.com"
	want.Retries = 5
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("hosted overlay = %+v, want the file's run site over the row's fields (+%+v)", m, want)
	}
}

func TestMergeKeepsATableRowTheOperatorsFileDoesNotList(t *testing.T) {
	tbl := mergedOK(t, mergeTableOf(t, localTableRow, brainTableRow), workerTableRow)
	if got := tbl.Known(); !reflect.DeepEqual(got, []string{"brain", "hand", "local"}) {
		t.Fatalf("known = %v, want the unlisted rows kept beside the new one", got)
	}
	m, _ := tbl.Get("local")
	if !reflect.DeepEqual(m, localRow()) {
		t.Fatalf("the unlisted row = %+v, want it untouched (+%+v)", m, localRow())
	}
}

func TestMergeAddsAnIdTheTableDoesNotKnowWithItsDefaults(t *testing.T) {
	tbl := mergedOK(t, mergeTableOf(t, localTableRow), `{"id": "fresh", "window": 8192, "maxTokens": 1024, "reserve": 1024, "keepRecent": 2048}`)
	m, ok := tbl.Get("fresh")
	if !ok {
		t.Fatal("a new id must be added")
	}
	if m.Role != models.RoleInteractive || m.Effort != "" {
		t.Fatalf("new row = %+v, want the defaults role interactive and effort empty (the policy's medium)", m)
	}
}

func TestMergeVisionFalseDescendsOntoTheTableRow(t *testing.T) {
	withVision := mergeTableOf(t, `{"id": "local", "window": 65536, "maxTokens": 8192, "reserve": 8192, "keepRecent": 16384, "vision": true}`)
	if m, _ := withVision.Get("local"); !m.Vision {
		t.Fatal("the row's own vision is not in effect")
	}
	off := mergedOK(t, withVision, `{"id": "local", "vision": false}`)
	if m, _ := off.Get("local"); m.Vision {
		t.Fatalf("vision = %+v, want the explicit false to descend (the presence-aware field)", m)
	}
	kept := mergedOK(t, withVision, `{"id": "local", "window": 32768}`)
	if m, _ := kept.Get("local"); !m.Vision {
		t.Fatalf("vision = %+v, want an unset key to keep the row's true", m)
	}
}

func TestMergeRowViolationNamesTheIdAndTheClause(t *testing.T) {
	_, err := merged(t, mergeTableOf(t, localTableRow), `{"id": "local", "reserve": 81920}`)
	want := "config: " + mergeOperator + ": local: Reserve 81920 must be in [0, Window 65536): as large as the window, the trigger fires at every estimate (the pi shape)"
	if err == nil || err.Error() != want {
		t.Fatalf("the voice = %v, want %q", err, want)
	}
}

func TestMergeRowViolationOnANewRowNamesTheIdAndTheClause(t *testing.T) {
	_, err := merged(t, mergeTableOf(t), `{"id": "wild", "window": 4096, "maxTokens": 512, "reserve": 512, "keepRecent": 8192}`)
	want := "config: " + mergeOperator + ": wild: KeepRecent 8192 must be in [0, Window-Reserve 3584): the usable window must leave room for the summary beside the tail"
	if err == nil || err.Error() != want {
		t.Fatalf("the voice = %v, want %q", err, want)
	}
}

func TestEmbeddedModelsTableCarriesNoRows(t *testing.T) {
	docs, err := parseRows(mustEmbeddedRows(t), mergeTablePath)
	if err != nil {
		t.Fatalf("the embedded file must still parse as a row array: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("the embedded table carries %d rows (%s): the table is the operator's file", len(docs), localTableRow)
	}
}

func mustEmbeddedRows(t *testing.T) []byte {
	t.Helper()
	data, err := embedded.ReadFile("models.json")
	if err != nil {
		t.Fatalf("the embedded models.json must stay embedded: %v", err)
	}
	return data
}
