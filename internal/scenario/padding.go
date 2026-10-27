package scenario

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
)

// inventoryHeader opens the padding and names the row columns.
const inventoryHeader = "Service inventory (name | region | status | latency | replicas):"

// maxRowTokens bounds the estimated tokens one row adds. The longest row
// (notifications-999, ap-southeast-1, maintenance, p50 275 ms, p99 1295 ms,
// 9 replicas) is 89 bytes with its newline, 23 tokens.
const maxRowTokens = 24

// padding returns an inventory of fictional services, a header then one row
// per line, as long as it fits in budget estimated tokens. Rows come from a
// SHA-256 stream over seed, so a larger budget extends a smaller one.
// The algorithm is frozen: changing it changes every scenario's bytes.
func padding(seed string, budget int) string {
	if estimateTokens(inventoryHeader) > budget {
		return ""
	}
	r := hashStream{seed: seed}
	var b strings.Builder
	b.WriteString(inventoryHeader)
	for i := 1; ; i++ {
		p50 := 20 + int(r.next())
		row := fmt.Sprintf("\n%s-%03d | %s | %s | p50 %d ms | p99 %d ms | %d replicas",
			services[r.next()%32], i, regions[r.next()%16], statuses[r.next()%8],
			p50, p50+4*int(r.next()), 2+r.next()%8)
		if estimateTokens(b.String()+row) > budget {
			return b.String()
		}
		b.WriteString(row)
	}
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
