package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"aidanwoods.dev/go-paseto"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/razzie-cloud/sudoku/internal/board"
	"github.com/razzie-cloud/sudoku/internal/catalog"
	"github.com/razzie-cloud/sudoku/internal/config"
	"github.com/razzie-cloud/sudoku/internal/db"
	"github.com/razzie-cloud/sudoku/internal/gatewayidentity"
	"github.com/razzie-cloud/sudoku/internal/store"
)

const testOrigin = "https://sudoku.example"
const testIssuer = "https://auth.example"
const testSolution = "534678912672195348198342567859761423426853791713924856961537284287419635345286179"

type testSigner struct {
	key      paseto.V4AsymmetricSecretKey
	id       string
	now      time.Time
	verifier *gatewayidentity.Verifier
}

func newTestSigner(t *testing.T, audience string) testSigner {
	t.Helper()
	s := testSigner{key: paseto.NewV4AsymmetricSecretKey(), now: time.Now().UTC().Truncate(time.Second)}
	id := sha256.Sum256(s.key.Public().ExportBytes())
	s.id = hex.EncodeToString(id[:16])
	discovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": testIssuer, "expires_at": s.now.Add(5 * time.Minute),
			"keys": []map[string]any{{
				"kid": s.id, "public_key": s.key.Public().ExportHex(),
				"not_before": s.now.Add(-time.Minute), "expires_at": s.now.Add(time.Hour),
			}},
		})
	}))
	t.Cleanup(discovery.Close)
	var err error
	s.verifier, err = gatewayidentity.New(t.Context(), discovery.URL, audience)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (s testSigner) sign(r *http.Request, audience string, owner uuid.UUID) {
	token := paseto.NewToken()
	token.SetIssuer(testIssuer)
	token.SetAudience(audience)
	token.SetSubject(owner.String())
	token.SetIssuedAt(s.now)
	token.SetNotBefore(s.now)
	token.SetExpiration(s.now.Add(time.Minute))
	token.SetString("name", "Alice")
	token.SetString("method", r.Method)
	token.SetString("uri", r.URL.RequestURI())
	_ = token.Set("admin", false)
	token.SetFooter([]byte(s.id))
	r.Header.Set(gatewayidentity.Header, token.V4Sign(s.key, nil))
	r.Header.Set("X-Auth-Request-User", owner.String())
	r.Header.Set("X-Auth-Request-Name", "Alice")
}

func TestProtectedRoutes(t *testing.T) {
	signer := newTestSigner(t, testOrigin)
	handler, err := New(config.Config{Origin: testOrigin}, nil, signer.verifier, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, origin, body, contentType string
		sign                                    bool
		want                                    int
	}{
		{"GET", "/games/new", "", "", "", false, 401},
		{"GET", "/games/new", "", "", "", true, 200},
		{"GET", "/healthz", "", "", "", true, 404},
		{"GET", "http://127.0.0.1:8080/healthz", "", "", "", false, 200},
		{"GET", "https://other.example/games/new", "", "", "", true, 404},
		{"POST", "/games", "https://other.example", "", "", true, 403},
		{"POST", "/games", testOrigin, strings.Repeat("x", 4200), "application/x-www-form-urlencoded", true, 422},
	} {
		t.Run(tc.method+"_"+tc.path+"_"+http.StatusText(tc.want), func(t *testing.T) {
			target := tc.path
			if strings.HasPrefix(target, "/") {
				target = testOrigin + target
			}
			req := httptest.NewRequest(tc.method, target, strings.NewReader(tc.body))
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
				req.Header.Set("Content-Type", tc.contentType)
			}
			owner := uuid.New()
			if tc.sign {
				signer.sign(req, testOrigin, owner)
			} else {
				req.Header.Set("X-Auth-Request-User", owner.String())
				req.Header.Set("X-Auth-Request-Name", "Alice")
			}
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, req)
			if out.Code != tc.want || out.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d, body=%s headers=%v", out.Code, out.Body, out.Header())
			}
		})
	}
}

func testStorage(t *testing.T, collections []catalog.Collection) *store.Store {
	t.Helper()
	value := os.Getenv("TEST_DATABASE_URL")
	if value == "" {
		t.Skip("TEST_DATABASE_URL unset; disposable PostgreSQL test")
	}
	dsn, err := url.Parse(value)
	if err != nil || dsn.Scheme != "postgres" && dsn.Scheme != "postgresql" {
		t.Fatal("invalid test database URL")
	}
	host := dsn.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Fatal("test database requires disposable loopback PostgreSQL")
	}
	admin, err := sql.Open("pgx", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "sudoku_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
	})
	query := dsn.Query()
	query.Set("search_path", schema)
	dsn.RawQuery = query.Encode()
	database, err := sql.Open("pgx", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	storage := store.New(database)
	if err := storage.RegisterCollections(t.Context(), collections); err != nil {
		t.Fatal(err)
	}
	return storage
}

func testPuzzles(t *testing.T) ([]catalog.Collection, board.Board, board.Board) {
	t.Helper()
	solution, err := board.Parse(testSolution)
	if err != nil {
		t.Fatal(err)
	}
	givens := solution
	givens[0], givens[1] = 0, 0
	return []catalog.Collection{{Key: "easy-1", Title: "Easy", Difficulty: board.Easy, Puzzles: []board.Board{givens}}}, givens, solution
}

func TestAttemptConcurrentSaveAndCompletion(t *testing.T) {
	collections, givens, solution := testPuzzles(t)
	storage := testStorage(t, collections)
	ctx := t.Context()
	owner, other := uuid.New(), uuid.New()
	a, b := store.Edit{Entries: givens}, store.Edit{Entries: givens}
	a.Entries[0] = solution[0]
	b.Notes[0] = 1 << (solution[0] - 1)
	var outcomes [2]struct {
		state store.State
		err   error
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	edits := []store.Edit{a, b}
	for i, edit := range edits {
		wg.Go(func() {
			<-start
			outcomes[i].state, outcomes[i].err = storage.SaveAttempt(ctx, owner, "easy-1", 1, givens, edit)
		})
	}
	close(start)
	wg.Wait()
	winner := -1
	for i, out := range outcomes {
		if out.err == nil && winner < 0 {
			winner = i
		} else if !errors.Is(out.err, store.ErrConflict) {
			t.Fatalf("unexpected outcomes %+v", outcomes)
		}
		if out.state.Revision != 1 {
			t.Fatalf("unexpected state %+v", out.state)
		}
	}
	if winner < 0 {
		t.Fatal("no edit succeeded")
	}
	retry, err := storage.SaveAttempt(ctx, owner, "easy-1", 1, givens, edits[winner])
	if err != nil || retry.Revision != 1 || retry.Entries != edits[winner].Entries || retry.Notes != edits[winner].Notes {
		t.Fatalf("identical retry %+v err=%v", retry, err)
	}
	otherState, err := storage.Attempt(ctx, other, "easy-1", 1, givens)
	if err != nil || otherState.Revision != 0 || otherState.Entries != givens {
		t.Fatalf("other owner's board %+v err=%v", otherState, err)
	}
	solved, err := storage.SaveAttempt(ctx, owner, "easy-1", 1, givens, store.Edit{Entries: solution, Revision: 1})
	if err != nil || solved.CompletedAt == nil {
		t.Fatalf("completion %+v err=%v", solved, err)
	}
	_, err = storage.SaveAttempt(ctx, owner, "easy-1", 1, givens, store.Edit{Entries: givens, Revision: solved.Revision})
	if !errors.Is(err, store.ErrClosed) {
		t.Fatalf("completed puzzle accepted edit: %v", err)
	}
	progress, err := storage.Progress(ctx, owner)
	if err != nil || len(progress) != 1 || !progress[0].Completed {
		t.Fatalf("progress %+v err=%v", progress, err)
	}
	collections[0].Puzzles[0][2] = 0
	if err := storage.RegisterCollections(ctx, collections); err == nil {
		t.Fatal("catalog accepted changed published board")
	}
}

func TestRandomGameLifecycle(t *testing.T) {
	collections, givens, solution := testPuzzles(t)
	storage := testStorage(t, collections)
	signer := newTestSigner(t, testOrigin)
	handler, err := New(config.Config{Origin: testOrigin}, storage, signer.verifier, nil, collections)
	if err != nil {
		t.Fatal(err)
	}
	owner, other := uuid.New(), uuid.New()
	request := func(method, path, body, contentType string, owner uuid.UUID) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, testOrigin+path, strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Origin", testOrigin)
		signer.sign(req, testOrigin, owner)
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	ctx := t.Context()
	generatedID := uuid.New()
	data := url.Values{"id": {generatedID.String()}, "difficulty": {"expert"}}.Encode()
	form := "application/x-www-form-urlencoded"
	path := "/games/" + generatedID.String()
	for range 2 {
		out := request("POST", "/games", data, form, owner)
		if out.Code != 303 || out.Header().Get("Location") != path {
			t.Fatalf("create status %d, body %s", out.Code, out.Body)
		}
	}
	generated, err := storage.Game(ctx, owner, generatedID)
	if err != nil || board.Rate(generated.Givens) != board.Expert {
		t.Fatalf("generated board %+v err=%v", generated, err)
	}
	if out := request("GET", path, "", "", other); out.Code != 404 {
		t.Fatalf("other owner read game: %d", out.Code)
	}
	id := uuid.New()
	game, err := storage.CreateGame(ctx, owner, id, board.Easy, givens)
	if err != nil {
		t.Fatal(err)
	}
	path = "/games/" + id.String()
	out := request("POST", path+"/archive", "revision=1&action=archive", form, owner)
	if out.Code != 303 || out.Header().Get("Location") != "/archive" {
		t.Fatalf("archive status %d: %s", out.Code, out.Body)
	}
	out = request("POST", path+"/archive", "revision=2&action=resume", form, other)
	if out.Code != 404 {
		t.Fatalf("other owner resumed game: %d", out.Code)
	}
	out = request("POST", path+"/archive", "revision=2&action=resume", form, owner)
	if out.Code != 303 || out.Header().Get("Location") != path {
		t.Fatalf("resume status %d: %s", out.Code, out.Body)
	}
	game, err = storage.Game(ctx, owner, game.ID)
	if err != nil || game.Archived || game.Revision != 3 {
		t.Fatalf("resume %+v err=%v", game, err)
	}
	body, err := json.Marshal(store.Edit{Entries: solution, Revision: game.Revision})
	if err != nil {
		t.Fatal(err)
	}
	out = request("POST", path+"/save", string(body), "application/json", owner)
	var saved saveResponse
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &saved) != nil || saved.State.CompletedAt == nil {
		t.Fatalf("save completion status %d: %s", out.Code, out.Body)
	}
	archive, _, err := storage.Games(ctx, owner, true, store.Cursor{})
	if err != nil || len(archive) != 1 || archive[0].ID != id || archive[0].Entries != solution {
		t.Fatalf("archive %+v err=%v", archive, err)
	}
	sessions, _, err := storage.Games(ctx, owner, false, store.Cursor{})
	if err != nil || len(sessions) != 1 || sessions[0].ID != generatedID {
		t.Fatalf("sessions %+v err=%v", sessions, err)
	}
}

func TestSaveValidation(t *testing.T) {
	collections, givens, _ := testPuzzles(t)
	storage := testStorage(t, collections)
	signer := newTestSigner(t, testOrigin)
	handler, err := New(config.Config{Origin: testOrigin}, storage, signer.verifier, nil, collections)
	if err != nil {
		t.Fatal(err)
	}
	owner, id := uuid.New(), uuid.New()
	if _, err := storage.CreateGame(t.Context(), owner, id, board.Easy, givens); err != nil {
		t.Fatal(err)
	}
	for i, path := range []string{"/puzzles/easy-1/1/save", "/games/" + id.String() + "/save"} {
		revision := int64(i)
		valid := store.Edit{Entries: givens, Revision: revision}
		valid.Entries[0] = 5
		changedClue := valid
		changedClue.Entries[2] = 0
		invalidNotes := valid
		invalidNotes.Notes[0] = 1
		body := func(edit store.Edit) string {
			encoded, err := json.Marshal(edit)
			if err != nil {
				t.Fatal(err)
			}
			return string(encoded)
		}
		for _, tc := range []struct {
			name, body string
			status     int
		}{
			{"malformed", "{", 422},
			{"unknown-field", strings.TrimSuffix(body(valid), "}") + `,"owner":"elsewhere"}`, 422},
			{"oversized", body(valid) + strings.Repeat(" ", 4096), 422},
			{"changed-clue", body(changedClue), 422},
			{"notes-under-digit", body(invalidNotes), 422},
			{"partial", body(valid), 200},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				req := httptest.NewRequest("POST", testOrigin+path, strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Origin", testOrigin)
				signer.sign(req, testOrigin, owner)
				out := httptest.NewRecorder()
				handler.ServeHTTP(out, req)
				if out.Code != tc.status {
					t.Fatalf("status=%d want=%d: %s", out.Code, tc.status, out.Body)
				}
				if tc.status == 200 {
					var saved saveResponse
					if err := json.Unmarshal(out.Body.Bytes(), &saved); err != nil || saved.State.CompletedAt != nil ||
						saved.State.Revision != revision+1 || saved.State.Entries != valid.Entries {
						t.Fatalf("partial save=%+v err=%v", saved, err)
					}
				}
			})
		}
	}
}
