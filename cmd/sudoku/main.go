package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/razzie-cloud/sudoku/internal/app"
	"github.com/razzie-cloud/sudoku/internal/catalog"
	"github.com/razzie-cloud/sudoku/internal/config"
	"github.com/razzie-cloud/sudoku/internal/db"
	"github.com/razzie-cloud/sudoku/internal/gatewayidentity"
	"github.com/razzie-cloud/sudoku/internal/health"
	"github.com/razzie-cloud/sudoku/internal/healthcheck"
	"github.com/razzie-cloud/sudoku/internal/logging"
	"github.com/razzie-cloud/sudoku/internal/store"
)

func run() error {
	cfg, err := config.Load(os.Args[1:], os.Getenv)
	if err != nil {
		return logging.Wrap("config", err)
	}
	if err := logging.SetupLevel("sudoku", cfg.LogLevel); err != nil {
		return logging.Wrap("logging", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	uri, err := cfg.DatabaseURI(startup)
	if err != nil {
		return logging.Wrap("database_discovery", err)
	}
	database, err := sql.Open("pgx", uri)
	if err != nil {
		return logging.Wrap("postgres_open", err)
	}
	defer database.Close()
	database.SetMaxOpenConns(20)
	database.SetMaxIdleConns(4)
	database.SetConnMaxLifetime(30 * time.Minute)
	if err = database.PingContext(startup); err != nil {
		return logging.Wrap("postgres_connect", err)
	}
	if err = db.Migrate(startup, database); err != nil {
		return logging.Wrap("migrate_postgres", err)
	}
	storage := store.New(database)
	collections, err := catalog.Load()
	if err != nil {
		return logging.Wrap("load_puzzles", err)
	}
	if err = storage.RegisterCollections(startup, collections); err != nil {
		return logging.Wrap("register_puzzles", err)
	}
	identity, err := gatewayidentity.New(startup, cfg.AccessGatewayURL, cfg.Origin)
	if err != nil {
		return logging.Wrap("gateway_identity", err)
	}
	monitor := health.New(health.Check{Name: "postgres", Ping: database.PingContext})
	handler, err := app.New(cfg, storage, identity, monitor, collections)
	if err != nil {
		return logging.Wrap("app_init", err)
	}
	stopHealth := monitor.Start(ctx)
	defer stopHealth()
	server := &http.Server{
		Addr: cfg.Address(), Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 32 << 10, ErrorLog: logging.ServerLogger(),
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			slog.Warn("Graceful shutdown failed", "event", "server.shutdown_failed", "error_kind", logging.Kind(err))
			_ = server.Close()
		}
	}()
	slog.Info("Server listening", "event", "server.listening", "address", cfg.Address())
	err = server.ListenAndServe()
	stop()
	// Shutdown closes the listener before draining requests. Keep database and
	// verifier dependencies alive until all requests finish.
	<-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return logging.Wrap("serve", err)
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck.Run())
	}
	// Configuration validates LOG_LEVEL after command-line overrides are applied.
	// A provisional INFO logger keeps configuration failures structured.
	_ = logging.Setup("sudoku")
	if err := run(); err != nil {
		slog.Error("Service failed", append([]any{"event", "server.failed"}, logging.ErrorAttrs(err)...)...)
		os.Exit(1)
	}
}
