package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/piplon-sh/piplon/internal/analyze"
	"github.com/piplon-sh/piplon/internal/probe"
	"github.com/piplon-sh/piplon/internal/store"
)

// redrawEvery keeps the LAST column current between probes.
const redrawEvery = time.Second

// liveTable redraws the target table in place on a terminal. It replaces the
// logs there: probe failures show in STATE and NOTE, and system errors (store,
// retention) on a line under the table.
type liveTable struct {
	w     io.Writer
	width func() int

	mu      sync.Mutex
	header  func() string
	db      store.Store
	rows    map[string]*row
	lastErr string
	drawn   int // lines of the previous frame, to move back over them
}

// logger returns a logger whose errors appear under the table.
func (l *liveTable) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(errLine{l}, &slog.HandlerOptions{
		Level: slog.LevelError,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey) {
				return slog.Attr{} // the line is the latest error; time and level add nothing
			}
			return a
		},
	}))
}

type errLine struct{ l *liveTable }

func (e errLine) Write(p []byte) (int, error) {
	e.l.mu.Lock()
	defer e.l.mu.Unlock()
	e.l.lastErr = strings.TrimSpace(string(p))
	return len(p), nil
}

// start shows one pending row per target. header is called on every frame.
func (l *liveTable) start(header func() string, targets []string, db store.Store) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.header, l.db = header, db
	l.rows = make(map[string]*row, len(targets))
	for _, t := range targets {
		l.rows[t] = &row{Target: t, State: statePending, Cache: "-"}
	}
}

// update applies one outcome, the target's status after it and its last hour
// of samples.
func (l *liveTable) update(ctx context.Context, o probe.Outcome, st analyze.Status) {
	samples, err := l.db.Samples(ctx, store.Query{Target: o.Target, Since: time.Now().Add(-time.Hour)})
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.rows[o.Target]
	if r == nil {
		return
	}
	r.apply(o, st)
	if err == nil {
		r.applyStats(samples)
	}
}

// run redraws every redrawEvery until ctx is cancelled, then draws a last
// frame so the final state stays on screen.
func (l *liveTable) run(ctx context.Context) {
	t := time.NewTicker(redrawEvery)
	defer t.Stop()
	for {
		l.draw(time.Now())
		select {
		case <-ctx.Done():
			l.draw(time.Now())
			return
		case <-t.C:
		}
	}
}

func (l *liveTable) draw(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rows := make([]row, 0, len(l.rows))
	for _, r := range l.rows {
		rows = append(rows, *r)
	}
	width := l.width()
	frame := truncate(l.header(), width) + "\n\n" + render(rows, width, now) + "\n"
	if l.lastErr != "" {
		frame += "\n" + truncate("! "+l.lastErr, width) + "\n"
	}
	if l.drawn > 0 {
		_, _ = fmt.Fprintf(l.w, "\x1b[%dF\x1b[J", l.drawn) // back to the frame's first line, clear below
	}
	_, _ = io.WriteString(l.w, frame)
	l.drawn = strings.Count(frame, "\n")
}
