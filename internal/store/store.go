// Package store owns player state in PostgreSQL. Every key includes the owner.
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/razzie-cloud/sudoku/internal/board"
)

var (
	ErrInvalid  = errors.New("invalid board or revision")
	ErrConflict = errors.New("the board changed since it was loaded")
	ErrClosed   = errors.New("the game is archived or complete")
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

type Edit struct {
	Entries  board.Board `json:"entries"`
	Notes    board.Notes `json:"notes"`
	Revision int64       `json:"revision"`
}

type State struct {
	Edit
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type Game struct {
	State
	ID         uuid.UUID        `json:"id"`
	Difficulty board.Difficulty `json:"difficulty"`
	Givens     board.Board      `json:"givens"`
	Archived   bool             `json:"archived"`
}

type Cursor struct {
	UpdatedAt time.Time
	ID        uuid.UUID
}

const gameColumns = "id, difficulty, givens, entries, notes, revision, updated_at, completed_at, archived"

type scanner interface{ Scan(...any) error }

func readGame(row scanner) (Game, error) {
	var g Game
	var givens, entries, notes string
	err := row.Scan(&g.ID, &g.Difficulty, &givens, &entries, &notes, &g.Revision, &g.UpdatedAt, &g.CompletedAt, &g.Archived)
	if err != nil {
		return Game{}, err
	}
	if g.Givens, err = board.Parse(givens); err != nil {
		return Game{}, err
	}
	err = readEdit(&g.Edit, entries, notes)
	return g, err
}

func readEdit(edit *Edit, entries, notes string) error {
	var err error
	if edit.Entries, err = board.Parse(entries); err != nil {
		return err
	}
	return edit.Notes.UnmarshalText([]byte(notes))
}

func notesText(n board.Notes) string {
	encoded, _ := n.MarshalText()
	return string(encoded)
}

func (s *Store) Game(ctx context.Context, owner, id uuid.UUID) (Game, error) {
	return readGame(s.db.QueryRowContext(ctx, "SELECT "+gameColumns+" FROM random_games WHERE owner=$1 AND id=$2", owner, id))
}

// CreateGame allows a form submission to be retried with the same game ID.
func (s *Store) CreateGame(ctx context.Context, owner, id uuid.UUID, difficulty board.Difficulty, givens board.Board) (Game, error) {
	row := s.db.QueryRowContext(ctx, `INSERT INTO random_games
		(owner, id, difficulty, givens, entries, notes, revision)
		VALUES ($1, $2, $3, $4, $4, $5, 1)
		ON CONFLICT (owner, id) DO NOTHING RETURNING `+gameColumns,
		owner, id, difficulty, givens.String(), notesText(board.Notes{}))
	game, err := readGame(row)
	if errors.Is(err, sql.ErrNoRows) {
		return s.Game(ctx, owner, id)
	}
	return game, err
}

const PageSize = 24

func (s *Store) Games(ctx context.Context, owner uuid.UUID, archived bool, cursor Cursor) ([]Game, *Cursor, error) {
	query := "SELECT " + gameColumns + " FROM random_games WHERE owner=$1 AND archived=$2"
	args := []any{owner, archived, PageSize + 1}
	if !cursor.UpdatedAt.IsZero() {
		query += " AND (updated_at, id) < ($4, $5)"
		args = append(args, cursor.UpdatedAt, cursor.ID)
	}
	query += " ORDER BY updated_at DESC, id DESC LIMIT $3"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	games := make([]Game, 0, PageSize)
	for rows.Next() {
		game, err := readGame(rows)
		if err != nil {
			return nil, nil, err
		}
		if len(games) == PageSize {
			if err := rows.Err(); err != nil {
				return nil, nil, err
			}
			last := games[len(games)-1]
			return games, &Cursor{last.UpdatedAt, last.ID}, nil
		}
		games = append(games, game)
	}
	return games, nil, rows.Err()
}

func lockedGame(ctx context.Context, tx *sql.Tx, owner, id uuid.UUID) (Game, error) {
	return readGame(tx.QueryRowContext(ctx, "SELECT "+gameColumns+" FROM random_games WHERE owner=$1 AND id=$2 FOR UPDATE", owner, id))
}

func sameEdits(current, incoming Edit) bool {
	return current.Entries == incoming.Entries && current.Notes == incoming.Notes
}

func (s *Store) SaveGame(ctx context.Context, owner, id uuid.UUID, edit Edit) (Game, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Game{}, err
	}
	defer tx.Rollback()
	g, err := lockedGame(ctx, tx, owner, id)
	if err != nil {
		return Game{}, err
	}
	if !edit.Entries.ValidEdits(g.Givens, edit.Notes) || edit.Revision < 1 {
		return g, ErrInvalid
	}
	if sameEdits(g.Edit, edit) && edit.Revision <= g.Revision {
		return g, nil
	}
	if g.Archived {
		return g, ErrClosed
	}
	if g.Revision != edit.Revision {
		return g, ErrConflict
	}
	solved := edit.Entries.Solved()
	g, err = readGame(tx.QueryRowContext(ctx, `UPDATE random_games
		SET entries=$3, notes=$4, revision=revision+1, updated_at=clock_timestamp(),
			archived=$5, completed_at=CASE WHEN $5 THEN clock_timestamp() END
		WHERE owner=$1 AND id=$2 RETURNING `+gameColumns,
		owner, id, edit.Entries.String(), notesText(edit.Notes), solved))
	if err != nil {
		return Game{}, err
	}
	return g, tx.Commit()
}

func (s *Store) ArchiveGame(ctx context.Context, owner, id uuid.UUID, archive bool, revision int64) (Game, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Game{}, err
	}
	defer tx.Rollback()
	g, err := lockedGame(ctx, tx, owner, id)
	if err != nil {
		return Game{}, err
	}
	if revision < 1 {
		return g, ErrInvalid
	}
	if g.Archived == archive && revision <= g.Revision {
		return g, nil
	}
	if g.CompletedAt != nil {
		return g, ErrClosed
	}
	if g.Revision != revision {
		return g, ErrConflict
	}
	g, err = readGame(tx.QueryRowContext(ctx, `UPDATE random_games
		SET archived=$3, revision=revision+1, updated_at=clock_timestamp()
		WHERE owner=$1 AND id=$2 RETURNING `+gameColumns, owner, id, archive))
	if err != nil {
		return Game{}, err
	}
	return g, tx.Commit()
}
