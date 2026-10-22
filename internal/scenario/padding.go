package scenario

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
)

const wordsPerLine = 12

// padding returns n words drawn from words by a SHA-256 stream over seed.
// The algorithm is frozen: changing it changes every scenario's bytes.
func padding(seed string, n int) string {
	var b strings.Builder
	var block [sha256.Size]byte
	for i := range n {
		if i%sha256.Size == 0 {
			in := binary.BigEndian.AppendUint64([]byte(seed), uint64(i/sha256.Size))
			block = sha256.Sum256(in)
		}
		switch {
		case i == 0:
		case i%wordsPerLine == 0:
			b.WriteByte('\n')
		default:
			b.WriteByte(' ')
		}
		b.WriteString(words[block[i%sha256.Size]])
	}
	return b.String()
}

// words has exactly 256 entries so one hash byte picks one word without bias.
// Common lower-case English words, each a single token for major tokenizers.
// Frozen: editing this list changes every scenario's bytes.
var words = [256]string{
	"able", "about", "above", "across", "act", "add", "after", "again", "age", "ago", "air", "all", "almost", "along", "also", "always",
	"among", "and", "animal", "answer", "any", "area", "arm", "around", "art", "ask", "away", "back", "bad", "ball", "bank", "base",
	"bed", "before", "begin", "behind", "best", "better", "big", "bird", "black", "blue", "boat", "body", "book", "both", "box", "boy",
	"bring", "build", "busy", "call", "came", "car", "care", "carry", "case", "cat", "cause", "center", "change", "check", "city", "class",
	"clear", "close", "cold", "color", "come", "common", "cook", "corn", "cost", "could", "country", "cover", "cross", "cut", "dark", "day",
	"deep", "did", "door", "down", "draw", "dream", "drive", "dry", "during", "each", "early", "earth", "east", "easy", "eat", "edge",
	"egg", "end", "enough", "even", "ever", "every", "eye", "face", "fact", "fall", "far", "farm", "fast", "father", "feel", "few",
	"field", "fill", "find", "fine", "fire", "first", "fish", "five", "floor", "fly", "food", "foot", "form", "four", "free", "friend",
	"front", "full", "game", "garden", "give", "glass", "gold", "good", "green", "ground", "group", "grow", "half", "hand", "hard", "head",
	"hear", "heat", "help", "high", "hill", "hold", "home", "horse", "hot", "hour", "house", "idea", "inch", "iron", "island", "just",
	"keep", "kind", "king", "knew", "land", "large", "last", "late", "lead", "learn", "leave", "left", "less", "letter", "level", "life",
	"light", "line", "list", "little", "live", "long", "look", "low", "made", "main", "make", "many", "map", "mark", "market", "may",
	"mean", "milk", "mind", "minute", "money", "moon", "more", "morning", "most", "mother", "move", "music", "name", "near", "need", "never",
	"new", "next", "night", "north", "note", "number", "ocean", "off", "often", "old", "once", "only", "open", "order", "other", "page",
	"paper", "part", "pass", "path", "people", "piece", "place", "plain", "plan", "plant", "play", "point", "port", "power", "quiet", "rain",
	"read", "ready", "red", "rest", "river", "road", "rock", "room", "round", "rule", "run", "safe", "sail", "salt", "same", "sand",
}
