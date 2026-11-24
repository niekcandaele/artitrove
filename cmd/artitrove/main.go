// Command artitrove runs the Artitrove server, or with the healthcheck argument probes it.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/niekcandaele/artitrove/internal/api"
	"github.com/niekcandaele/artitrove/internal/artifact"
	"github.com/niekcandaele/artitrove/internal/config"
	"github.com/niekcandaele/artitrove/internal/store"
	"github.com/niekcandaele/artitrove/internal/web"
)

const shutdownTimeout = 10 * time.Second

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, getenv func(string) string) (code int) {
	if len(args) > 1 || (len(args) == 1 && args[0] != "healthcheck") {
		fmt.Fprintln(os.Stderr, "usage: artitrove [healthcheck]")
		return 2
	}
	cfg, err := config.Load(getenv)
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		return 1
	}
	if len(args) == 1 {
		if err := healthcheck(ctx, cfg.Listen); err != nil {
			slog.Error("healthcheck failed", "error", err)
			return 1
		}
		return 0
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		slog.Error("open store", "error", err)
		return 1
	}
	defer func() {
		if err := st.Close(); err != nil {
			slog.Error("close store", "error", err)
			code = 1
		}
	}()
	h := newHandler(cfg, st)
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		slog.Error("listen", "error", err)
		return 1
	}
	if err := serve(ctx, ln, h); err != nil {
		slog.Error("serve", "error", err)
		return 1
	}
	return 0
}

// newHandler wires every route and wraps them in the cross-cutting response headers.
// There is no access log: request paths contain the credential ID.
func newHandler(cfg config.Config, st artifact.Store) http.Handler {
	mux := http.NewServeMux()
	api.Register(mux, cfg, st)
	web.Register(mux, cfg, st, api.RawHandler(st))
	return securityHeaders(mux)
}

// serve serves h on ln until ctx is cancelled, then shuts down gracefully. It returns only
// after in-flight handlers finish or the shutdown timeout expires.
func serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second, // no ReadTimeout: slow large uploads must finish
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
	}
	slog.Info("listening", "addr", ln.Addr().String())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	shutdownErr := srv.Shutdown(sctx)
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return shutdownErr
}
