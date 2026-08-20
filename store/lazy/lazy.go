package lazy

import (
	"errors"
)

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

func (l *Lazy[T]) Fill(row *T, err error) {
	l.row = row
	l.err = err
	l.filled = true
}

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
