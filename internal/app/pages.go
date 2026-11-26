package app

import (
	"bytes"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/razzie-cloud/sudoku/internal/board"
	"github.com/razzie-cloud/sudoku/internal/logging"
	"github.com/razzie-cloud/sudoku/internal/store"
	"github.com/razzie-cloud/sudoku/internal/web"
)

type pageView struct {
	Kind, Title, Nav, Error string
	Home, Account           string
	User                    account
	Data                    any
	Board                   *boardView
}

type boardView struct {
	store.State
	Title, URL, BackURL, BackLabel, Next, Account string
	Givens                                        board.Board
	Digits                                        [9]int
	ReadOnly, Completed, Puzzle                   bool
}

func (b boardView) NotesText() string {
	s, _ := b.Notes.MarshalText()
	return string(s)
}

func (a *Server) page(w http.ResponseWriter, r *http.Request, name string, data any, status int) {
	p := pageView{Kind: name, Home: a.cfg.LandingOrigin, Account: a.verifier.AccountURL(a.cfg.LandingOrigin), User: user(r), Data: data}
	switch v := data.(type) {
	case setupPage:
		p.Title, p.Nav, p.Error = "New game", "new", v.Error
	case gamesPage:
		p.Title, p.Nav = "Sessions", "sessions"
		if v.Archive {
			p.Title, p.Nav = "Archive", "archive"
		}
	case []collectionView:
		p.Title, p.Nav = "Puzzles", "puzzles"
	case gamePage:
		p.Title, p.Nav, p.Error = v.Difficulty.Title()+" game", "sessions", v.Error
		backURL, backLabel := "/sessions", "← Sessions"
		if v.Archived {
			p.Nav, backURL, backLabel = "archive", "/archive", "← Archive"
		}
		p.Board = &boardView{State: v.State, Title: p.Title, URL: "/games/" + v.ID.String(), Givens: v.Givens,
			BackURL: backURL, BackLabel: backLabel, ReadOnly: v.Archived, Completed: v.CompletedAt != nil}
	case puzzlePage:
		p.Title, p.Nav = v.Title+" "+strconv.Itoa(v.Slot), "puzzles"
		p.Board = &boardView{State: v.State, Title: p.Title, URL: "/puzzles/" + v.Collection + "/" + strconv.Itoa(v.Slot), Givens: v.Givens,
			BackURL: "/puzzles", BackLabel: "← Puzzles", ReadOnly: v.CompletedAt != nil,
			Completed: v.CompletedAt != nil, Puzzle: true, Next: v.Next}
	}
	if p.Board != nil {
		p.Board.Account = p.Account
		p.Board.Digits = [9]int{1, 2, 3, 4, 5, 6, 7, 8, 9}
	}
	var out bytes.Buffer
	if err := web.Render(&out, p); err != nil {
		logging.Fail(r.Context(), slog.LevelError, "page_render", err)
		http.Error(w, "Sudoku could not load this page.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(out.Bytes())
}
