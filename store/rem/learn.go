package rem

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/mrsirg97-rgb/rig/v2/store"
	remdom "github.com/mrsirg97-rgb/rig/v2/store/rem/domain"
	"github.com/mrsirg97-rgb/rig/v2/store/sqlx"
)

type LearnInput struct {
	Content       string
	Kind          string
	Importance    float64
	ImportanceSet bool
	Scope         string
	Source        string
	Supersedes    []int64
}

type ReflectInput struct {
	Content       string
	Importance    float64
	ImportanceSet bool
	Scope         string
	Source        string
}

type writeResult struct {
	reply    string
	mem      *remdom.Memory
	existing bool
}

const maxContentBytes = 64 * 1024

func Learn(ctx context.Context, db store.DB, cwd string, in LearnInput) (string, *remdom.Memory, bool, error) {
	if in.Content == "" {
		return "", nil, false, fmt.Errorf("rem: action 'learn' requires content")
	}
	if len(in.Content) > maxContentBytes {
		return "", nil, false, fmt.Errorf("rem: content must be at most %d bytes, got %d", maxContentBytes, len(in.Content))
	}
	res, err := transact(ctx, db, func(bound context.Context, tx *sql.Tx) (writeResult, error) {
		mem, existing, err := storeOrTouch(bound, writeShape{
			content:       in.Content,
			kind:          in.Kind,
			importance:    in.Importance,
			importanceSet: in.ImportanceSet,
			source:        in.Source,
			scope:         in.Scope,
			supersedes:    in.Supersedes,
		}, cwd)
		if err != nil {
			return writeResult{}, err
		}
		if existing {
			note := fmt.Sprintf("already known m%d", mem.Id)
			if in.ImportanceSet {
				note += fmt.Sprintf(" · importance → %g", in.Importance)
			}
			return writeResult{reply: note, mem: mem, existing: true}, nil
		}
		return writeResult{reply: fmt.Sprintf("learned m%d (%s · %s · %g)", mem.Id, mem.ScopeLabel, mem.Kind, mem.Importance), mem: mem}, nil
	})
	if err != nil {
		return "", nil, false, err
	}
	return res.reply, res.mem, res.existing, nil
}

func Reflect(ctx context.Context, db store.DB, cwd string, in ReflectInput) (string, *remdom.Memory, bool, error) {
	if in.Content == "" {
		return "", nil, false, fmt.Errorf("rem: action 'reflect' requires content")
	}
	if len(in.Content) > maxContentBytes {
		return "", nil, false, fmt.Errorf("rem: content must be at most %d bytes, got %d", maxContentBytes, len(in.Content))
	}
	res, err := transact(ctx, db, func(bound context.Context, tx *sql.Tx) (writeResult, error) {
		mem, existing, err := storeOrTouch(bound, writeShape{
			content:       in.Content,
			kind:          kindReflection,
			importance:    in.Importance,
			importanceSet: in.ImportanceSet,
			source:        in.Source,
			sourceSet:     in.Source != "",
			scope:         in.Scope,
		}, cwd)
		if err != nil {
			return writeResult{}, err
		}
		if existing {
			note := fmt.Sprintf("already known m%d", mem.Id)
			if in.ImportanceSet {
				note += fmt.Sprintf(" · importance → %g", in.Importance)
			}
			if in.Source != "" {
				note += " · source updated"
			}
			return writeResult{reply: note, mem: mem, existing: true}, nil
		}
		return writeResult{reply: fmt.Sprintf("reflected m%d (%s · %s · %g)", mem.Id, mem.ScopeLabel, mem.Kind, mem.Importance), mem: mem}, nil
	})
	if err != nil {
		return "", nil, false, err
	}
	return res.reply, res.mem, res.existing, nil
}

type writeShape struct {
	content       string
	kind          string
	importance    float64
	importanceSet bool
	source        string
	sourceSet     bool
	scope         string
	supersedes    []int64
}

func storeOrTouch(bound context.Context, sh writeShape, cwd string) (*remdom.Memory, bool, error) {
	scopeKey, scopeLabel, err := writeScope(sh.scope, cwd)
	if err != nil {
		return nil, false, err
	}
	digest := sha256hex(sh.content)

	tx, err := sqlx.TxFrom(bound)
	if err != nil {
		return nil, false, err
	}
	var existingID int64
	err = tx.QueryRowContext(bound, `SELECT id FROM memories WHERE scope = $1 AND content_sha256 = $2`, scopeKey, digest).Scan(&existingID)
	switch {
	case err == nil:

	case err == sql.ErrNoRows:
		existingID = 0
	default:
		return nil, false, fmt.Errorf("rem: dedup seek: %w", err)
	}

	if existingID > 0 {
		row, err := remdom.NewMemoryDomain().GetMemory(bound, existingID).Row()
		if err != nil {
			return nil, false, fmt.Errorf("rem: existing row: %w", err)
		}
		if row == nil {
			return nil, false, fmt.Errorf("rem: natural key resolved to an absent row")
		}
		if len(sh.supersedes) > 0 {
			if err := applySupersedes(bound, row.Id, sh.supersedes, scopeKey); err != nil {
				return nil, false, err
			}
		}
		touched := false
		if sh.importanceSet {
			row.Importance = sh.importance
			touched = true
		}
		if sh.sourceSet && sh.source != "" && (row.Source == nil || *row.Source != sh.source) {
			s := sh.source
			row.Source = &s
			touched = true
		}
		if touched {
			updated, err := remdom.NewMemoryDomain().UpdateMemory(bound, *row)
			if err != nil {
				return nil, false, fmt.Errorf("rem: existing row: %w", err)
			}
			return updated, true, nil
		}
		return row, true, nil
	}

	id, err := mintID(bound)
	if err != nil {
		return nil, false, err
	}
	kind := sh.kind
	if kind == "" {
		kind = "fact"
	}
	now := nowISO()
	var source *string
	if sh.source != "" {
		s := sh.source
		source = &s
	}
	row := remdom.Memory{
		Id:                 id,
		Scope:              scopeKey,
		ScopeLabel:         scopeLabel,
		Kind:               kind,
		Content:            sh.content,
		Source:             source,
		Importance:         sh.importance,
		Strength:           clamp01(sh.importance),
		CreatedAt:          now,
		LastConsolidatedAt: now,
		ContentSha256:      digest,
	}
	fresh, err := remdom.NewMemoryDomain().InsertMemory(bound, row)
	if err != nil {
		return nil, false, fmt.Errorf("rem: insert: %w", err)
	}
	if err := insertGrams(bound, id, gramsOf(sh.content)); err != nil {
		return nil, false, err
	}
	if ftsEnabled() {

		if _, err := tx.ExecContext(bound, `INSERT INTO memory_fts (rowid, content) VALUES ($1, $2)`, id, sh.content); err != nil {
			return nil, false, fmt.Errorf("rem: fts insert: %w", err)
		}
	}
	if len(sh.supersedes) > 0 {
		if err := applySupersedes(bound, id, sh.supersedes, scopeKey); err != nil {
			return nil, false, err
		}
	}
	return fresh, false, nil
}

func insertGrams(bound context.Context, id int64, grams []string) error {
	for _, gram := range grams {
		if _, err := remdom.NewTrigramDomain().InsertTrigram(bound, remdom.Trigram{MemoryId: id, Gram: gram}); err != nil {
			return fmt.Errorf("rem: grams: %w", err)
		}
	}
	return nil
}
