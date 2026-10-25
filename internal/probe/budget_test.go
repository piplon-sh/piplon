package probe

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestBudgetReserveAndSettle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := NewBudget(1.00)
		if !b.Reserve(0.60) {
			t.Fatal("first reservation must fit")
		}
		if b.Reserve(0.50) {
			t.Fatal("0.60 + 0.50 must not fit in 1.00")
		}
		b.Settle(0.60, 0.10) // the probe cost less than estimated
		if got := b.Spent(); !near(got, 0.10) {
			t.Fatalf("spent: got %v, want 0.10", got)
		}
		if !b.Reserve(0.50) {
			t.Fatal("0.10 + 0.50 must fit after settling")
		}
	})
}

func TestBudgetResetsAtUTCMidnight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// The synctest clock starts at 2000-01-01 00:00 UTC.
		b := NewBudget(1.00)
		b.Seed(0.95)
		if b.Reserve(0.10) {
			t.Fatal("seeded spend must count")
		}
		time.Sleep(23*time.Hour + 59*time.Minute)
		if b.Reserve(0.10) {
			t.Fatal("still the same UTC day")
		}
		time.Sleep(time.Minute)
		if !b.Reserve(0.10) {
			t.Fatal("a new UTC day must start from zero")
		}
		if got := b.Spent(); !near(got, 0.10) {
			t.Fatalf("spent after reset: got %v, want 0.10", got)
		}
	})
}

func TestBudgetSettleNeverGoesNegative(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := NewBudget(1.00)
		b.Reserve(0.20)
		time.Sleep(24 * time.Hour) // day rolls over between reserve and settle
		b.Settle(0.20, 0.05)
		if got := b.Spent(); got < 0 {
			t.Fatalf("spent went negative: %v", got)
		}
	})
}

func near(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }
