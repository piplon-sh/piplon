package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/piplon-sh/piplon/internal/store"
)

// Channel delivers notifications to one destination.
type Channel interface {
	// Name identifies the channel in the outbox and the logs. It never
	// contains a secret.
	Name() string
	Send(ctx context.Context, n store.Notification) error
}

const (
	pollEvery   = 5 * time.Second  // between drains when nothing wakes the dispatcher
	batch       = 50               // notifications read per round
	sendTimeout = 10 * time.Second // per attempt
	firstRetry  = 30 * time.Second // doubled after each failure
	maxRetry    = 30 * time.Minute
	giveUpAfter = 24 * time.Hour // past this an event is stale: abandoned, not sent
)

// Dispatcher drains the store's outbox to its channels.
type Dispatcher struct {
	db       store.Store
	log      *slog.Logger
	channels map[string]Channel
	names    []string
	wake     chan struct{}
	now      func() time.Time
}

// NewDispatcher returns a Dispatcher sending to channels.
func NewDispatcher(db store.Store, log *slog.Logger, channels ...Channel) *Dispatcher {
	d := &Dispatcher{db: db, log: log, channels: map[string]Channel{}, wake: make(chan struct{}, 1), now: time.Now}
	for _, c := range channels {
		d.channels[c.Name()] = c
		d.names = append(d.names, c.Name())
	}
	return d
}

// Names returns the channel names, in the order given to NewDispatcher.
func (d *Dispatcher) Names() []string { return d.names }

// Wake asks for a drain now rather than at the next poll. It never blocks.
func (d *Dispatcher) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Run drains the outbox at start, on every Wake and every pollEvery, until
// ctx is cancelled. A send cut short by shutdown stays pending for the next
// start.
func (d *Dispatcher) Run(ctx context.Context) {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		d.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-d.wake:
		case <-t.C:
		}
	}
}

// drain sends what is due, round after round: a delivery can let the next
// event of the same incident through.
func (d *Dispatcher) drain(ctx context.Context) {
	for ctx.Err() == nil {
		due, err := d.db.Due(ctx, d.now(), batch)
		if err != nil {
			if ctx.Err() == nil {
				d.log.Error("outbox read failed", "err", err)
			}
			return
		}
		if len(due) == 0 {
			return
		}
		for _, n := range due {
			d.deliver(ctx, n)
		}
	}
}

// deliver sends one notification and records the result.
func (d *Dispatcher) deliver(ctx context.Context, n store.Notification) {
	attrs := []any{"channel", n.Channel, "event", n.Event, "target", n.Incident.Target, "incident", n.Incident.ID}
	c, ok := d.channels[n.Channel]
	if !ok {
		d.record(ctx, d.db.Abandon(ctx, n.ID, d.now(), "channel no longer configured"))
		d.log.Warn("notification abandoned: channel no longer configured", attrs...)
		return
	}
	if age := d.now().Sub(n.At); age > giveUpAfter {
		reason := fmt.Sprintf("not delivered within %s: %s", giveUpAfter, n.LastError)
		d.record(ctx, d.db.Abandon(ctx, n.ID, d.now(), reason))
		d.log.Error("notification abandoned", append(attrs, "attempts", n.Attempts, "err", n.LastError)...)
		return
	}
	sctx, cancel := context.WithTimeout(ctx, sendTimeout)
	err := c.Send(sctx, n)
	cancel()
	switch {
	case ctx.Err() != nil:
		return // shutdown: still pending, sent again at the next start
	case err == nil:
		d.record(ctx, d.db.Delivered(ctx, n.ID, d.now()))
		d.log.Info("notification sent", attrs...)
	default:
		next := d.now().Add(backoff(n.Attempts))
		d.record(ctx, d.db.Failed(ctx, n.ID, next, err.Error()))
		d.log.Warn("notification failed", append(attrs, "attempts", n.Attempts+1, "next", next, "err", err)...)
	}
}

func (d *Dispatcher) record(ctx context.Context, err error) {
	if err != nil && ctx.Err() == nil {
		d.log.Error("outbox write failed", "err", err)
	}
}

// backoff is the wait after a failure, given the attempts before it.
func backoff(attempts int) time.Duration {
	if attempts >= 6 { // 30 s << 6 is past maxRetry already
		return maxRetry
	}
	return min(firstRetry<<attempts, maxRetry)
}
