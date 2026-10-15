package lazy

import (
	"errors"
	"testing"
)

func TestAReadBeforeTheFlushRefuses(t *testing.T) {
	l := New[int](func(ScanRow) (int, error) { return 0, nil })
	if _, err := l.Row(); err == nil || err.Error() != "lazy: read before the flush" {
		t.Fatalf("Row before the flush = %v, want the fail-closed refusal", err)
	}
	if _, err := l.Rows(); err == nil || err.Error() != "lazy: read before the flush" {
		t.Fatalf("Rows before the flush = %v, want the fail-closed refusal", err)
	}
}

func TestAnAbsentFillReadsNilWithNoError(t *testing.T) {
	l := New[int](func(ScanRow) (int, error) { return 0, nil })
	l.Fill(nil, nil)
	row, err := l.Row()
	if err != nil || row != nil {
		t.Fatalf("an absent read = (%v, %v), want (nil, nil)", row, err)
	}
	l2 := New[int](func(ScanRow) (int, error) { return 0, nil })
	l2.FillAll(nil, nil)
	rows, err := l2.Rows()
	if err != nil || rows != nil {
		t.Fatalf("an absent read-all = (%v, %v), want (nil, nil)", rows, err)
	}
}

func TestTheFillErrorRidesTheRead(t *testing.T) {
	boom := errors.New("boom")
	l := New[int](func(ScanRow) (int, error) { return 0, nil })
	l.Fill(nil, boom)
	row, err := l.Row()
	if !errors.Is(err, boom) || row != nil {
		t.Fatalf("Row = (%v, %v), want the fill's error", row, err)
	}
	l2 := New[int](func(ScanRow) (int, error) { return 0, nil })
	l2.FillAll(nil, boom)
	rows, err := l2.Rows()
	if !errors.Is(err, boom) || rows != nil {
		t.Fatalf("Rows = (%v, %v), want the fill's error", rows, err)
	}
}

func TestAFilledLazyCarriesItsRows(t *testing.T) {
	l := New[int](func(ScanRow) (int, error) { return 0, nil })
	want := []int{1, 2, 3}
	l.FillAll(want, nil)
	got, err := l.Rows()
	if err != nil || len(got) != 3 || got[2] != 3 {
		t.Fatalf("Rows = (%v, %v), want the flushed rows", got, err)
	}
	seven := 7
	l2 := New[int](func(ScanRow) (int, error) { return 0, nil })
	l2.Fill(&seven, nil)
	row, err := l2.Row()
	if err != nil || row == nil || *row != 7 {
		t.Fatalf("Row = (%v, %v), want the flushed row", row, err)
	}
}
