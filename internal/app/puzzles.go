package app

import (
	"net/http"
	"strconv"

	"github.com/razzie-cloud/sudoku/internal/board"
	"github.com/razzie-cloud/sudoku/internal/catalog"
	"github.com/razzie-cloud/sudoku/internal/store"
)

type tile struct {
	Slot   int         `json:"slot"`
	Givens board.Board `json:"givens"`
	Status string      `json:"status"`
}

type collectionView struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Completed int    `json:"completed"`
	Tiles     []tile `json:"tiles"`
}

type puzzlePage struct {
	store.State
	Collection string           `json:"collection"`
	Title      string           `json:"title"`
	Difficulty board.Difficulty `json:"difficulty"`
	Givens     board.Board      `json:"givens"`
	Slot       int              `json:"slot"`
	Next       string           `json:"next,omitempty"`
}

type puzzleKey struct {
	collection string
	slot       int
}

func (a *Server) progress(r *http.Request) (map[puzzleKey]bool, error) {
	attempts, err := a.store.Progress(r.Context(), user(r).ID)
	if err != nil {
		return nil, err
	}
	progress := make(map[puzzleKey]bool, len(attempts))
	for _, p := range attempts {
		progress[puzzleKey{p.Collection, p.Slot}] = p.Completed
	}
	return progress, nil
}

func (a *Server) puzzles(w http.ResponseWriter, r *http.Request) {
	progress, err := a.progress(r)
	if err != nil {
		status, message := result(r, err)
		http.Error(w, message, status)
		return
	}
	collections := make([]collectionView, 0, len(a.collections))
	for _, c := range a.collections {
		view := collectionView{Key: c.Key, Title: c.Title, Tiles: make([]tile, 0, len(c.Puzzles))}
		for i, givens := range c.Puzzles {
			slot := i + 1
			status := "unstarted"
			if completed, started := progress[puzzleKey{c.Key, slot}]; completed {
				status = "completed"
				view.Completed++
			} else if started {
				status = "in-progress"
			}
			view.Tiles = append(view.Tiles, tile{slot, givens, status})
		}
		collections = append(collections, view)
	}
	a.page(w, r, "collections", collections, http.StatusOK)
}

func (a *Server) definition(r *http.Request) (catalog.Collection, int, bool) {
	key := r.PathValue("collection")
	slot, err := strconv.Atoi(r.PathValue("slot"))
	if err == nil && slot > 0 {
		for _, c := range a.collections {
			if c.Key == key && slot <= len(c.Puzzles) {
				return c, slot, true
			}
		}
	}
	return catalog.Collection{}, 0, false
}

func (a *Server) puzzle(w http.ResponseWriter, r *http.Request) {
	c, slot, ok := a.definition(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	givens := c.Puzzles[slot-1]
	state, err := a.store.Attempt(r.Context(), user(r).ID, c.Key, slot, givens)
	if err != nil {
		status, message := result(r, err)
		http.Error(w, message, status)
		return
	}
	progress, err := a.progress(r)
	if err != nil {
		status, message := result(r, err)
		http.Error(w, message, status)
		return
	}
	page := puzzlePage{State: state, Collection: c.Key, Title: c.Title, Difficulty: c.Difficulty, Givens: givens, Slot: slot}
	for offset := 1; offset < len(c.Puzzles); offset++ {
		next := (slot-1+offset)%len(c.Puzzles) + 1
		if !progress[puzzleKey{c.Key, next}] {
			page.Next = "/puzzles/" + c.Key + "/" + strconv.Itoa(next)
			break
		}
	}
	a.page(w, r, "puzzle", page, http.StatusOK)
}

func (a *Server) savePuzzle(w http.ResponseWriter, r *http.Request) {
	c, slot, ok := a.definition(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	edit, ok := parseEdit(r)
	if !ok {
		saveReply(w, r, store.State{}, store.ErrInvalid)
		return
	}
	state, err := a.store.SaveAttempt(r.Context(), user(r).ID, c.Key, slot, c.Puzzles[slot-1], edit)
	saveReply(w, r, state, err)
}
