// Package app serves private Sudoku pages and board saves.
package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/razzie-cloud/sudoku/internal/board"
	"github.com/razzie-cloud/sudoku/internal/catalog"
	"github.com/razzie-cloud/sudoku/internal/config"
	"github.com/razzie-cloud/sudoku/internal/gatewayidentity"
	"github.com/razzie-cloud/sudoku/internal/health"
	"github.com/razzie-cloud/sudoku/internal/logging"
	"github.com/razzie-cloud/sudoku/internal/store"
	"github.com/razzie-cloud/sudoku/internal/web"
)

type Server struct {
	cfg         config.Config
	store       *store.Store
	verifier    *gatewayidentity.Verifier
	health      *health.Monitor
	host        string
	mux         *http.ServeMux
	collections []catalog.Collection
	generating  chan struct{}
}

type accountKey struct{}

type account struct {
	ID    uuid.UUID
	Name  string
	Admin bool
}

func user(r *http.Request) account {
	return r.Context().Value(accountKey{}).(account)
}

func New(cfg config.Config, storage *store.Store, verifier *gatewayidentity.Verifier, monitor *health.Monitor, collections []catalog.Collection) (http.Handler, error) {
	origin, err := url.Parse(cfg.Origin)
	if err != nil || origin.Host == "" || verifier == nil {
		return nil, errors.New("Sudoku requires a configured origin and signed gateway identity")
	}
	a := &Server{cfg: cfg, store: storage, verifier: verifier, health: monitor, host: origin.Host, mux: http.NewServeMux(), collections: collections, generating: make(chan struct{}, 2)}
	a.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(web.Static()))))
	a.mux.HandleFunc("GET /{$}", a.games)
	a.mux.HandleFunc("GET /sessions", a.games)
	a.mux.HandleFunc("GET /archive", a.games)
	a.mux.HandleFunc("GET /games/new", a.newGame)
	a.mux.HandleFunc("POST /games", a.createGame)
	a.mux.HandleFunc("GET /games/{id}", a.game)
	a.mux.HandleFunc("POST /games/{id}/save", a.saveGame)
	a.mux.HandleFunc("POST /games/{id}/archive", a.archiveGame)
	a.mux.HandleFunc("GET /puzzles", a.puzzles)
	a.mux.HandleFunc("GET /puzzles/{collection}/{slot}", a.puzzle)
	a.mux.HandleFunc("POST /puzzles/{collection}/{slot}/save", a.savePuzzle)
	return logging.Middleware(a), nil
}

func (a *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	if r.Host != a.host {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		ip := net.ParseIP(host)
		if (host == "localhost" || ip != nil && (ip.IsLoopback() || ip.IsPrivate())) && a.health.Handle(w, r) {
			return
		}
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	_, pattern := a.mux.Handler(r)
	logging.SetRoute(ctx, pattern)
	if a.verifier.Verify(r, false) != nil {
		w.Header().Set("HX-Refresh", "true")
		http.Error(w, "Your cloud session expired. Sign in again.", http.StatusUnauthorized)
		return
	}
	id, err := uuid.Parse(r.Header.Get("X-Auth-Request-User"))
	if err != nil || id == uuid.Nil {
		http.Error(w, "Verified identity required.", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodPost {
		if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != a.cfg.Origin {
			http.Error(w, "Submit this form from Sudoku.", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	}
	ctx = context.WithValue(ctx, accountKey{}, account{id, r.Header.Get("X-Auth-Request-Name"), a.verifier.IsAdmin(r)})
	a.mux.ServeHTTP(w, r.WithContext(ctx))
}

func reply(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

type saveResponse struct {
	State store.State `json:"state"`
	Error string      `json:"error,omitempty"`
}

func saveReply(w http.ResponseWriter, r *http.Request, state store.State, err error) {
	status, message := result(r, err)
	if status == http.StatusInternalServerError || status == http.StatusNotFound {
		http.Error(w, message, status)
		return
	}
	reply(w, status, saveResponse{state, message})
}

func result(r *http.Request, err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound, "This board is unavailable."
	case errors.Is(err, store.ErrInvalid):
		return http.StatusUnprocessableEntity, "The board or revision is invalid."
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict, "The board changed in another tab or device."
	case errors.Is(err, store.ErrClosed):
		return http.StatusConflict, "This board is complete or archived."
	default:
		logging.Fail(r.Context(), slog.LevelError, "player_state", err)
		return http.StatusInternalServerError, "Sudoku could not load or save this board."
	}
}

func parseForm(r *http.Request) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && contentType == "application/x-www-form-urlencoded" && r.ParseForm() == nil
}

func field(r *http.Request, name string) string {
	if values := r.PostForm[name]; len(values) == 1 {
		return values[0]
	}
	return ""
}

func parseEdit(r *http.Request) (store.Edit, bool) {
	var input struct {
		Entries  *board.Board `json:"entries"`
		Notes    *board.Notes `json:"notes"`
		Revision *int64       `json:"revision"`
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return store.Edit{}, false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || input.Entries == nil || input.Notes == nil || input.Revision == nil {
		return store.Edit{}, false
	}
	return store.Edit{Entries: *input.Entries, Notes: *input.Notes, Revision: *input.Revision}, true
}
