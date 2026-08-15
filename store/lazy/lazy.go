// Package lazy is the deferred result under direct accessor execution: a
// domain runs the query on the tx and hands back a filled Lazy — the
// result decorated, the error carried through the fill path.
package lazy

import (
	"errors"
)

// ScanRow is the minimal row the accessors scan: one method, which both
// the stdlib and pgx rows satisfy. the scanners stay decoupled from the
// wire layer.
type ScanRow interface {
	Scan(dest ...any) error
}

type Lazy[T any] struct {
	scan   func(ScanRow) (T, error)
	row    *T
	rows   []T
	err    error
	filled bool
}

func New[T any](scan func(ScanRow) (T, error)) *Lazy[T] {
	return &Lazy[T]{scan: scan}
}

// Fill lands a single row directly: the accessor path — the query ran
// immediately on the tx, the result decorates the lazy. nil row, nil
// error is an absent read, matching domain get semantics.
func (l *Lazy[T]) Fill(row *T, err error) {
	l.row = row
	l.err = err
	l.filled = true
}

// FillAll lands a whole result directly: the batch or list accessor
// path, rows collected by the caller and decorated here.
func (l *Lazy[T]) FillAll(rows []T, err error) {
	l.rows = rows
	l.err = err
	l.filled = true
}

func (l *Lazy[T]) Row() (*T, error) {
	if !l.filled && l.err == nil {
		return nil, errors.New("lazy: read before the flush")
	}
	return l.row, l.err
}

func (l *Lazy[T]) Rows() ([]T, error) {
	if !l.filled && l.err == nil {
		return nil, errors.New("lazy: read before the flush")
	}
	return l.rows, l.err
}
