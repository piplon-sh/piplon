package serve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/piplon-sh/piplon/internal/config"
	"github.com/piplon-sh/piplon/internal/store"
)

// shutdownTimeout bounds how long Serve waits for scrapes in flight, well
// inside the 5 s piplon has to stop.
const shutdownTimeout = 2 * time.Second

// Options says what the endpoints report on.
type Options struct {
	Targets   []config.Target // the configured ones: their prices, and the targets the last-probe gauges cover
	DailyUSD  float64         // budget.daily_usd
	CacheFrom int             // the scenario's first turn expected to read the cache, 0 for none
	Version   string
	Log       *slog.Logger     // nil uses slog.Default
	Now       func() time.Time // nil uses time.Now
}

// Handler returns the mux for /metrics, /healthz and /readyz, reading db.
// It reads the store once before returning, so a store it cannot read is an
// error at start.
func Handler(ctx context.Context, db store.Store, opts Options) (http.Handler, error) {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	m, err := newMetrics(ctx, db, opts)
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	m.reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	expose := promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
	var mu sync.Mutex // one scrape at a time: a refresh moves the cursor

	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if err := m.refresh(r.Context()); err != nil {
			opts.Log.Error("metrics not read from the store", "err", err)
			http.Error(w, "store not readable", http.StatusServiceUnavailable)
			return
		}
		expose.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			opts.Log.Error("store not readable", "err", err)
			http.Error(w, "store not readable", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprintln(w, "ok")
	})
	return mux, nil
}

// Serve serves h on ln until ctx is cancelled, then waits up to 2 s for
// requests in flight. It closes ln.
func Serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return fmt.Errorf("serve %s: %w", ln.Addr(), err)
	case <-ctx.Done():
	}
	stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	err := srv.Shutdown(stop)
	if served := <-done; !errors.Is(served, http.ErrServerClosed) {
		err = errors.Join(err, served)
	}
	if err != nil {
		return fmt.Errorf("serve %s: %w", ln.Addr(), err)
	}
	return nil
}
