package config

import (
	"errors"
	"testing"
)

func TestExpand(t *testing.T) {
	vars := env(map[string]string{"A": "1", "B": "2", "EMPTY": ""})
	tests := []struct {
		in, want string
		wantErr  error
	}{
		{in: "plain", want: "plain"},
		{in: "${A}", want: "1"},
		{in: "pre-${A}-post", want: "pre-1-post"},
		{in: "${A}${B}", want: "12"},
		{in: "$A stays literal", want: "$A stays literal"},
		{in: "p4$$word", want: "p4$$word"},
		{in: "${unterminated", want: "${unterminated"},
		{in: "${MISSING}", wantErr: ErrMissingEnv},
		{in: "${EMPTY}", wantErr: ErrMissingEnv},
	}
	for _, tt := range tests {
		got, err := expand(tt.in, vars)
		if !errors.Is(err, tt.wantErr) {
			t.Errorf("expand(%q): err %v, want %v", tt.in, err, tt.wantErr)
			continue
		}
		if err == nil && got != tt.want {
			t.Errorf("expand(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
