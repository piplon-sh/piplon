package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestSecretIsRedacted(t *testing.T) {
	s := Secret("sk-very-secret")
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("probe", "key", s)
	out, err := yaml.Marshal(struct{ Key Secret }{s})
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{
		fmt.Sprint(s), fmt.Sprintf("%s %v %+v %#v %q", s, s, s, s, s),
		logs.String(), string(out),
	} {
		if strings.Contains(rendered, "sk-very-secret") {
			t.Errorf("secret leaked in %q", rendered)
		}
	}
	if s.Reveal() != "sk-very-secret" {
		t.Error("Reveal must return the raw value")
	}
}

func TestDuration(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "90d", want: 90 * 24 * time.Hour},
		{in: "36h", want: 36 * time.Hour},
		{in: "1d12h", wantErr: true},
		{in: "forever", wantErr: true},
		{in: "-3d", wantErr: true},
	}
	for _, tt := range tests {
		var d Duration
		err := yaml.Unmarshal([]byte(tt.in), &d)
		if (err != nil) != tt.wantErr {
			t.Errorf("%q: err %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if err == nil && time.Duration(d) != tt.want {
			t.Errorf("%q: got %v, want %v", tt.in, time.Duration(d), tt.want)
		}
	}
}
