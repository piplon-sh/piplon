package probe

import (
	"sync"
	"time"
)

// Budget caps spending per UTC day. It is safe for concurrent use.
type Budget struct {
	mu    sync.Mutex
	daily float64
	day   time.Time // UTC midnight of the day spent belongs to
	spent float64
}

// NewBudget returns a budget of dailyUSD per UTC day.
func NewBudget(dailyUSD float64) *Budget {
	return &Budget{daily: dailyUSD, day: today()}
}

func today() time.Time { return time.Now().UTC().Truncate(24 * time.Hour) }

// rollover starts a new day at zero when the UTC date changed. Callers hold mu.
func (b *Budget) rollover() {
	if d := today(); !d.Equal(b.day) {
		b.day, b.spent = d, 0
	}
}

// Seed sets what was already spent today, for example after a restart.
func (b *Budget) Seed(spentUSD float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollover()
	b.spent = spentUSD
}

// Reserve books usd if it fits in what is left today and reports whether it did.
func (b *Budget) Reserve(usd float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollover()
	if b.spent+usd > b.daily {
		return false
	}
	b.spent += usd
	return true
}

// Settle replaces a reservation with what the probe actually cost.
func (b *Budget) Settle(reserved, actual float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollover()
	b.spent = max(0, b.spent-reserved+actual)
}

// Spent returns today's spend, reservations included.
func (b *Budget) Spent() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollover()
	return b.spent
}
