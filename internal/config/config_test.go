package config

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validEnv() map[string]string {
	return map[string]string{"ORIGIN": "http://sudoku.localhost:8080", "ACCESS_GATEWAY_URL": "http://access-gateway:8080", "POSTGRES_URI": "postgres://sudoku:password@localhost/sudoku"}
}

func TestLoadOriginsAndFlags(t *testing.T) {
	values := validEnv()
	c, err := Load([]string{"--port", "9090", "--log-level", "debug"}, func(k string) string { return values[k] })
	if err != nil || c.Port != "9090" || c.LogLevel != "DEBUG" || c.Address() != ":9090" {
		t.Fatalf("flag precedence: %+v, %v", c, err)
	}
	for _, tc := range []struct{ key, value string }{
		{"ORIGIN", "http://sudoku.localhost/"}, {"ORIGIN", "http://user:pass@sudoku.localhost"},
		{"ORIGIN", "http://sudoku.localhost?"}, {"ORIGIN", "http://sudoku.localhost:70000"},
		{"ORIGIN", "http://sudoku.localhost#"},
		{"ORIGIN", "http://sudoku.localhost:"},
		{"ACCESS_GATEWAY_URL", ""}, {"SERVICE_PORT", "0"}, {"LOG_LEVEL", "trace"},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			env := validEnv()
			env[tc.key] = tc.value
			if _, err := Load(nil, func(k string) string { return env[k] }); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestDirectURIWinsAndPasswordFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte("secret@with:reserved\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{PostgresURI: "postgres://sudoku:old@localhost/sudoku", PostgresURIFrom: "invalid", BrokerTokenFile: "does-not-exist", PasswordFile: path}
	got, err := c.DatabaseURI(t.Context())
	if err != nil || !strings.Contains(got, "secret%40with%3Areserved@") {
		t.Fatalf("direct database config: %q %v", got, err)
	}
}

func TestAuthenticatedDiscovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	token := "v1.sudoku.1." + strings.Repeat("A", 43)
	if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("client") != "sudoku" || r.URL.Query().Get("token") != token {
			t.Error("missing scoped token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte("postgres://sudoku:fixture@localhost/sudoku"))
	}))
	defer server.Close()
	c := Config{PostgresURIFrom: server.URL + "/v1/instances/postgres/sudoku/uri?client=sudoku", BrokerTokenFile: path}
	got, err := c.DatabaseURI(t.Context())
	if err != nil || got != "postgres://sudoku:fixture@localhost/sudoku" {
		t.Fatalf("discovery: %q %v", got, err)
	}
}
