package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/tomanagle/yullu/internal/ai"
	"github.com/tomanagle/yullu/internal/applog"
	"github.com/tomanagle/yullu/internal/config"
	"github.com/tomanagle/yullu/internal/server"
	"github.com/tomanagle/yullu/internal/store"
)

// runStdio implements `yullu stdio` - the legacy MCP-over-stdio entry for
// clients that don't speak HTTP MCP. Reads config, opens the store, and
// streams JSON-RPC on stdin/stdout until the client closes the pipe or the
// process gets SIGINT/SIGTERM.
//
// Prefer the HTTP MCP endpoint exposed by the desktop server (`yullu`) -
// it shares state with the UI, supports multiple clients at once, and
// avoids spawning a process per session.
func runStdio() int {
	logger := applog.New()

	cfgPath := config.MustDefaultPath()
	cfg := config.MustLoad(cfgPath)
	logger.Info("config loaded",
		"path", cfgPath,
		"embedding_provider", cfg.Embedding.Provider,
		"reasoning_provider", cfg.Reasoning.Provider,
		"sync_enabled", cfg.Sync.Enabled,
	)

	recRef := ai.NewRecorderRef()
	embedder := ai.MustBuildEmbedder(cfg, recRef)
	reasoner, err := ai.BuildReasoner(cfg, recRef)
	if err != nil {
		panic(fmt.Errorf("init reasoner: %w", err))
	}

	dbPath := store.MustDefaultDBPath()
	st := store.MustOpen(dbPath, embedder.ID(), embedder.Dim())
	defer st.Close()
	recRef.Set(st)

	reasonerID := "sampling"
	if reasoner != nil {
		reasonerID = reasoner.ID()
	}
	logger.Info("store opened",
		"db_path", dbPath,
		"embedder", embedder.ID(),
		"embedder_dim", embedder.Dim(),
		"reasoner", reasonerID,
	)

	srv := server.New(st, embedder, reasoner, cfg.Sync, cfg.Dreaming, logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if cfg.Sync.Enabled && cfg.Sync.AutoReconcileOnStartup {
		srv.LogReconcile(ctx)
	}
	srv.StartScheduler(ctx)

	logger.Info("serving over stdio")
	if err := srv.ServeStdio(ctx); err != nil {
		logger.Error("stdio serve exited", "err", err.Error())
		return 1
	}
	return 0
}
