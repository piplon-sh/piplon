package scenario

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
)

// inventoryHeader opens the padding and names the row columns.
const inventoryHeader = "Service inventory (name | region | status | latency | replicas):"

// maxRows keeps row numbers to three digits.
const maxRows = 999

// padding returns an inventory of fictional services: a header, then rows
// lines. Rows come from a SHA-256 stream over seed, so more rows extend fewer.
// The algorithm is frozen: changing it changes every scenario's bytes.
func padding(seed string, rows int) string {
	if rows < 1 {
		return ""
	}
	r := hashStream{seed: seed}
	var b strings.Builder
	b.WriteString(inventoryHeader)
	for i := 1; i <= rows; i++ {
		p50 := 20 + int(r.next())
		fmt.Fprintf(&b, "\n%s-%03d | %s | %s | p50 %d ms | p99 %d ms | %d replicas",
			services[r.next()%32], i, regions[r.next()%16], statuses[r.next()%8],
			p50, p50+4*int(r.next()), 2+r.next()%8)
	}
	return b.String()
}

// hashStream yields the bytes of SHA-256(seed ‖ uint64BE(0)), then of
// SHA-256(seed ‖ uint64BE(1)), and so on.
type hashStream struct {
	seed  string
	n     uint64
	block [sha256.Size]byte
}

func (h *hashStream) next() byte {
	i := h.n % sha256.Size
	if i == 0 {
		h.block = sha256.Sum256(binary.BigEndian.AppendUint64([]byte(h.seed), h.n/sha256.Size))
	}
	h.n++
	return h.block[i]
}

// The list sizes are powers of two so one hash byte picks without bias.
// Frozen: editing a list changes every scenario's bytes.
var (
	services = [32]string{
		"auth", "billing", "checkout", "search", "catalog", "cart", "payments", "invoices",
		"accounts", "profiles", "sessions", "orders", "shipping", "inventory", "pricing", "reviews",
		"notifications", "email-sender", "sms-gateway", "media-store", "thumbnails", "exports", "imports", "audit-log",
		"feature-flags", "rate-limiter", "api-gateway", "webhooks", "scheduler", "reporting", "ledger", "geo-lookup",
	}
	regions = [16]string{
		"us-east-1", "us-east-2", "us-west-1", "us-west-2", "eu-west-1", "eu-west-2", "eu-west-3", "eu-central-1",
		"eu-north-1", "ap-south-1", "ap-northeast-1", "ap-northeast-2", "ap-southeast-1", "ap-southeast-2", "ca-central-1", "sa-east-1",
	}
	// Repeats weight the healthy status, as in a real fleet.
	statuses = [8]string{"healthy", "healthy", "healthy", "healthy", "healthy", "healthy", "degraded", "maintenance"}
)
