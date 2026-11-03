package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/spf13/cobra"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/scenario"
	"github.com/piplon-sh/piplon/internal/serve"
	"github.com/piplon-sh/piplon/internal/store"
	"github.com/piplon-sh/piplon/internal/version"
)

func newServeCmd() *cobra.Command {
	var path, listen string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve /metrics, /healthz and /readyz from the store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serveStore(cmd.Context(), path, listen, newLogger(cmd.ErrOrStderr()))
		},
	}
	cmd.Flags().StringVarP(&path, "config", "c", "piplon.yaml", "path to the config file")
	cmd.Flags().StringVar(&listen, "listen", "", "host:port to listen on (default serve.listen, else "+config.DefaultListen+")")
	return cmd
}

// serveStore serves the store named by the config until ctx is cancelled.
// It never probes: piplon run, here or elsewhere, fills the store.
func serveStore(ctx context.Context, path, listen string, log *slog.Logger) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	switch {
	case listen != "":
		if err := config.CheckListen(listen); err != nil {
			return fmt.Errorf("--listen: %w", err)
		}
	case cfg.Serve.Listen != "":
		listen = cfg.Serve.Listen
	default:
		listen = config.DefaultListen
	}
	sc, err := scenario.Load(cfg.Scenario)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.Store.Path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	serveHTTP, err := listenHTTP(ctx, db, cfg, sc, listen, log)
	if err != nil {
		return err
	}
	log.Info("piplon serving", "store", cfg.Store.Path)
	if err := serveHTTP(ctx); err != nil {
		return err
	}
	log.Info("piplon stopped")
	return nil
}

// listenHTTP builds the handler on db and binds addr, so a store that
// cannot be read or an address in use fails before anything runs. The
// returned function serves until ctx is cancelled.
func listenHTTP(ctx context.Context, db store.Store, cfg *config.Config, sc *scenario.Scenario, addr string,
	log *slog.Logger) (func(context.Context) error, error) {
	h, err := serve.Handler(ctx, db, serve.Options{
		Targets:   cfg.Targets,
		DailyUSD:  cfg.Budget.DailyUSD,
		CacheFrom: sc.Cache.ExpectHitFromTurn,
		Version:   version.Version,
		Log:       log,
	})
	if err != nil {
		return nil, err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("serve.listen: %w", err)
	}
	log.Info("serving /metrics, /healthz and /readyz", "addr", ln.Addr().String())
	return func(ctx context.Context) error { return serve.Serve(ctx, ln, h) }, nil
}
