package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/razzie-cloud/sudoku/internal/board"
	"github.com/razzie-cloud/sudoku/internal/catalog"
)

// RegisterCollections binds each edition to its ordered boards on first use.
func (s *Store) RegisterCollections(ctx context.Context, collections []catalog.Collection) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, collection := range collections {
		digest := sha256.New()
		for _, puzzle := range collection.Puzzles {
			_, _ = digest.Write([]byte(puzzle.String()))
		}
		value := digest.Sum(nil)
		if _, err := tx.ExecContext(ctx, "INSERT INTO puzzle_collections (key, digest) VALUES ($1, $2) ON CONFLICT DO NOTHING", collection.Key, value); err != nil {
			return err
		}
		var matches bool
		if err := tx.QueryRowContext(ctx, "SELECT digest=$2 FROM puzzle_collections WHERE key=$1", collection.Key, value).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			return fmt.Errorf("published collection %q has changed", collection.Key)
		}
	}
	return tx.Commit()
}

type Progress struct {
	Collection string
	Slot       int
	Completed  bool
}

const attemptColumns = "entries, notes, revision, updated_at, completed_at"

func readAttempt(row scanner) (State, error) {
	var state State
	var entries, notes string
	if err := row.Scan(&entries, &notes, &state.Revision, &state.UpdatedAt, &state.CompletedAt); err != nil {
		return State{}, err
	}
	err := readEdit(&state.Edit, entries, notes)
	return state, err
}

func initialState(givens board.Board) State { return State{Edit: Edit{Entries: givens}} }

func (s *Store) Attempt(ctx context.Context, owner uuid.UUID, collection string, slot int, givens board.Board) (State, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+attemptColumns+" FROM puzzle_attempts WHERE owner=$1 AND collection=$2 AND slot=$3", owner, collection, slot)
	state, err := readAttempt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return initialState(givens), nil
	}
	return state, err
}

func (s *Store) Progress(ctx context.Context, owner uuid.UUID) ([]Progress, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT collection, slot, completed_at IS NOT NULL FROM puzzle_attempts WHERE owner=$1", owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var progress []Progress
	for rows.Next() {
		var item Progress
		if err := rows.Scan(&item.Collection, &item.Slot, &item.Completed); err != nil {
			return nil, err
		}
		progress = append(progress, item)
	}
	return progress, rows.Err()
}

func (s *Store) SaveAttempt(ctx context.Context, owner uuid.UUID, collection string, slot int, givens board.Board, edit Edit) (State, error) {
	if !edit.Entries.ValidEdits(givens, edit.Notes) || edit.Revision < 0 {
		return State{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return State{}, err
	}
	defer tx.Rollback()
	selectQuery := "SELECT " + attemptColumns + " FROM puzzle_attempts WHERE owner=$1 AND collection=$2 AND slot=$3 FOR UPDATE"
	state, err := readAttempt(tx.QueryRowContext(ctx, selectQuery, owner, collection, slot))
	solved := edit.Entries.Solved()
	if errors.Is(err, sql.ErrNoRows) {
		state = initialState(givens)
		if edit.Revision != 0 {
			return state, ErrConflict
		}
		if sameEdits(state.Edit, edit) {
			return state, nil
		}
		row := tx.QueryRowContext(ctx, `INSERT INTO puzzle_attempts
			(owner, collection, slot, entries, notes, revision, completed_at)
			VALUES ($1, $2, $3, $4, $5, 1, CASE WHEN $6 THEN clock_timestamp() END)
			ON CONFLICT (owner, collection, slot) DO NOTHING RETURNING `+attemptColumns,
			owner, collection, slot, edit.Entries.String(), notesText(edit.Notes), solved)
		state, err = readAttempt(row)
		if err == nil {
			return state, tx.Commit()
		}
		if errors.Is(err, sql.ErrNoRows) {
			// Another replica created the attempt after the first SELECT.
			state, err = readAttempt(tx.QueryRowContext(ctx, selectQuery, owner, collection, slot))
		}
	}
	if err != nil {
		return State{}, err
	}
	if sameEdits(state.Edit, edit) && edit.Revision <= state.Revision {
		return state, nil
	}
	if state.CompletedAt != nil {
		return state, ErrClosed
	}
	if state.Revision != edit.Revision {
		return state, ErrConflict
	}
	state, err = readAttempt(tx.QueryRowContext(ctx, `UPDATE puzzle_attempts
		SET entries=$4, notes=$5, revision=revision+1, updated_at=clock_timestamp(),
			completed_at=CASE WHEN $6 THEN clock_timestamp() END
		WHERE owner=$1 AND collection=$2 AND slot=$3 RETURNING `+attemptColumns,
		owner, collection, slot, edit.Entries.String(), notesText(edit.Notes), solved))
	if err != nil {
		return State{}, err
	}
	return state, tx.Commit()
}
