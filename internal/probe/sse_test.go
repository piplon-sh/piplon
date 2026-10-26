package probe

import (
	"reflect"
	"strings"
	"testing"
)

func TestReadSSE(t *testing.T) {
	type ev struct{ event, data string }
	tests := []struct {
		name string
		in   string
		want []ev
	}{
		{"event and data", "event: a\ndata: {\"x\":1}\n\n", []ev{{"a", `{"x":1}`}}},
		{"data only", "data: hi\n\n", []ev{{"", "hi"}}},
		{"multi-line data", "data: one\ndata: two\n\n", []ev{{"", "one\ntwo"}}},
		{"comments ignored", ": keep-alive\n\ndata: x\n\n", []ev{{"", "x"}}},
		{"no space after colon", "data:x\n\n", []ev{{"", "x"}}},
		{"unterminated last event dropped", "data: a\n\ndata: b\n", []ev{{"", "a"}}},
		{"empty data line", "data:\n\n", []ev{{"", ""}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []ev
			if err := readSSE(strings.NewReader(tt.in), func(e string, d []byte) bool {
				got = append(got, ev{e, string(d)})
				return true
			}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadSSEStopsWhenAsked(t *testing.T) {
	n := 0
	_ = readSSE(strings.NewReader("data: 1\n\ndata: 2\n\n"), func(string, []byte) bool {
		n++
		return false
	})
	if n != 1 {
		t.Errorf("callback ran %d times, want 1", n)
	}
}
