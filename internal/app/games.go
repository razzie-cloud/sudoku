package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/razzie-cloud/sudoku/internal/board"
	"github.com/razzie-cloud/sudoku/internal/store"
)

type setupPage struct {
	ID           uuid.UUID           `json:"id"`
	Difficulty   board.Difficulty    `json:"difficulty"`
	Difficulties [5]board.Difficulty `json:"difficulties"`
	Error        string              `json:"error,omitempty"`
}

type gamesPage struct {
	Games   []store.Game `json:"games"`
	Archive bool         `json:"archive"`
	Next    string       `json:"next,omitempty"`
}

type gamePage struct {
	store.Game
	Error string `json:"error,omitempty"`
}

func (a *Server) newGame(w http.ResponseWriter, r *http.Request) {
	a.page(w, r, "setup", setupPage{ID: uuid.New(), Difficulty: board.Medium, Difficulties: board.Difficulties()}, http.StatusOK)
}

func (a *Server) createGame(w http.ResponseWriter, r *http.Request) {
	if !parseForm(r) {
		http.Error(w, "The form is too large or invalid.", http.StatusUnprocessableEntity)
		return
	}
	difficulty := board.Difficulty(field(r, "difficulty"))
	id, err := uuid.Parse(field(r, "id"))
	if err != nil || id == uuid.Nil || !difficulty.Valid() {
		a.page(w, r, "setup", setupPage{uuid.New(), board.Medium, board.Difficulties(), "Choose a difficulty."}, http.StatusUnprocessableEntity)
		return
	}
	ctx, owner := r.Context(), user(r).ID
	_, err = a.store.Game(ctx, owner, id)
	if errors.Is(err, sql.ErrNoRows) {
		generation, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var givens board.Board
		select {
		case a.generating <- struct{}{}:
			givens, err = board.Generate(generation, difficulty, rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())))
			<-a.generating
		case <-generation.Done():
			err = generation.Err()
		}
		if err != nil {
			a.page(w, r, "setup", setupPage{id, difficulty, board.Difficulties(), "Generation timed out. Try again."}, http.StatusServiceUnavailable)
			return
		}
		_, err = a.store.CreateGame(ctx, owner, id, difficulty, givens)
	}
	if err != nil {
		status, message := result(r, err)
		a.page(w, r, "setup", setupPage{id, difficulty, board.Difficulties(), message}, status)
		return
	}
	http.Redirect(w, r, "/games/"+id.String(), http.StatusSeeOther)
}

func (a *Server) games(w http.ResponseWriter, r *http.Request) {
	archived := r.URL.Path == "/archive"
	cursor, err := decodeCursor(r.URL.Query().Get("before"))
	if err != nil {
		http.Error(w, "Invalid page cursor.", http.StatusBadRequest)
		return
	}
	games, next, err := a.store.Games(r.Context(), user(r).ID, archived, cursor)
	if err != nil {
		status, message := result(r, err)
		http.Error(w, message, status)
		return
	}
	page := gamesPage{Games: games, Archive: archived}
	if next != nil {
		page.Next = (&url.URL{Path: r.URL.Path, RawQuery: url.Values{"before": {encodeCursor(*next)}}.Encode()}).String()
	}
	a.page(w, r, "games", page, http.StatusOK)
}

func encodeCursor(c store.Cursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.UpdatedAt.UTC().Format(time.RFC3339Nano) + "/" + c.ID.String()))
}

func decodeCursor(value string) (store.Cursor, error) {
	if value == "" {
		return store.Cursor{}, nil
	}
	if len(value) > 100 {
		return store.Cursor{}, errors.New("invalid cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return store.Cursor{}, err
	}
	timestamp, key, _ := strings.Cut(string(raw), "/")
	t, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return store.Cursor{}, err
	}
	id, err := uuid.Parse(key)
	return store.Cursor{UpdatedAt: t, ID: id}, err
}

func (a *Server) game(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	game, err := a.store.Game(r.Context(), user(r).ID, id)
	if err != nil {
		status, message := result(r, err)
		http.Error(w, message, status)
		return
	}
	a.page(w, r, "game", gamePage{Game: game}, http.StatusOK)
}

func (a *Server) saveGame(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	edit, ok := parseEdit(r)
	if !ok {
		saveReply(w, r, store.State{}, store.ErrInvalid)
		return
	}
	game, err := a.store.SaveGame(r.Context(), user(r).ID, id, edit)
	saveReply(w, r, game.State, err)
}

func (a *Server) archiveGame(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !parseForm(r) {
		http.Error(w, "The form is too large or invalid.", http.StatusUnprocessableEntity)
		return
	}
	revision, err := strconv.ParseInt(field(r, "revision"), 10, 64)
	action := field(r, "action")
	if err != nil || (action != "archive" && action != "resume") {
		http.Error(w, "Invalid archive request.", http.StatusUnprocessableEntity)
		return
	}
	game, err := a.store.ArchiveGame(r.Context(), user(r).ID, id, action == "archive", revision)
	if err != nil {
		status, message := result(r, err)
		if status == http.StatusConflict {
			a.page(w, r, "game", gamePage{Game: game, Error: message}, status)
			return
		}
		http.Error(w, message, status)
		return
	}
	if game.Archived {
		http.Redirect(w, r, "/archive", http.StatusSeeOther)
	} else {
		http.Redirect(w, r, "/games/"+id.String(), http.StatusSeeOther)
	}
}
