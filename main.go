// Yul'lu is the desktop server. It serves the React
// frontend, a REST API, and the MCP endpoint all on the same port. The
// browser is the UI - there's no native window, just a long-running HTTP
// server. Register MCP clients against http://localhost:47823/mcp.
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tomanagle/yullu/internal/handlers"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed skills/body.md
var skillBody string

const httpAddr = ":47823"

func main() {
	log.SetOutput(os.Stderr)

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			os.Exit(runInstall(os.Args[2:]))
		case "uninstall":
			os.Exit(runUninstall(os.Args[2:]))
		case "stdio":
			os.Exit(runStdio())
		case "version", "--version", "-v":
			println("Yul'lu " + version)
			return
		case "help", "--help", "-h":
			printUsage()
			return
		default:
			println("yullu: unknown subcommand:", os.Args[1])
			printUsage()
			os.Exit(2)
		}
	}

	app := NewApp()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	app.startup(ctx)
	defer app.shutdown(ctx)

	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatalf("embed sub: %v", err)
	}

	mux := http.NewServeMux()
	handlers.Register(mux, handlers.RegisterParams{
		Status:               app,
		Config:               app,
		Memory:               app,
		Editor:               app,
		Projects:             app,
		Graph:                app,
		Stats:                app,
		Usage:                app,
		Dreamer:              app,
		Session:              app,
		DreamContextMemories: app.cfg.Dreaming.ContextMemories,
	})
	mux.Handle("/mcp", mcpProxy{app: app})
	mux.Handle("/mcp/", mcpProxy{app: app})
	mux.Handle("/", spaHandler(dist))

	srv := &http.Server{
		Addr:              httpAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	}()

	app.logger.Info("yul'lu starting",
		"url", "http://localhost"+httpAddr,
		"mcp", "http://localhost"+httpAddr+"/mcp",
	)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("listen: %v", err)
	}
}

// spaHandler serves the embedded SPA. Static files (assets/*.js, /favicon.ico,
// etc.) are served as-is; anything else falls back to index.html so the React
// router can resolve client-side routes on hard refresh.
func spaHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			fileServer.ServeHTTP(w, r)
			return
		}
		if _, err := dist.Open(path); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// mcpProxy delegates each /mcp request to whichever Server the App currently
// holds. SaveConfig swaps the underlying *Server when the embedder or
// reasoner changes, so capturing a single handler at boot would leave the
// transport pointing at a closed store after the user reconfigures.
type mcpProxy struct {
	app *App
}

func (p mcpProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := p.app.mcpHandlerHTTP()
	if h == nil {
		http.Error(w, "yullu: finish setup in the UI before connecting an MCP client", http.StatusServiceUnavailable)
		return
	}
	h.ServeHTTP(w, r)
}

const version = "0.1.0"

func printUsage() {
	println(`Yul'lu - persistent memory for AI coding assistants

Usage:
  yullu                     Start the desktop server (UI + REST + MCP on :47823)
  yullu install [target…]   Install skill + register MCP for an assistant
                            (targets: claude, codex; default: all detected)
  yullu uninstall [target…] Reverse install
  yullu stdio               Run MCP over stdio (legacy - prefer HTTP via the server)
  yullu version             Print version
  yullu help                Show this help

Once running, register manually with:
  claude mcp add yullu --transport http http://localhost:47823/mcp`)
}
