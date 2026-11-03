package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/store"
)

var listening = regexp.MustCompile(`"msg":"serving /metrics, /healthz and /readyz","addr":"([^"]+)"`)

// addrIn waits for the endpoints to be listening and returns their address.
func addrIn(t *testing.T, logs *syncBuffer) string {
	t.Helper()
	var addr string
	waitFor(t, "the endpoints to listen", func() bool {
		m := listening.FindStringSubmatch(logs.String())
		if m != nil {
			addr = m[1]
		}
		return m != nil
	})
	return addr
}

func fetch(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func TestRunServesTheEndpointsWhenListenIsSet(t *testing.T) {
	url, _ := fakeAnthropic(t)
	path, _ := writeConfig(t, "anthropic", url, 5, "serve: {listen: 127.0.0.1:0}")
	var logs syncBuffer
	stop := startRun(t, path, &logs)
	base := "http://" + addrIn(t, &logs)
	want := []string{
		`piplon_probes_total{result="ok",scenario="standard-v1",target="fake"} 1`,
		`piplon_target_state{scenario="standard-v1",state="ok",target="fake"} 1`,
	}
	waitFor(t, "the first probe and its state in /metrics", func() bool {
		_, body := fetch(t, base+"/metrics")
		return strings.Contains(body, want[0]) && strings.Contains(body, want[1])
	})
	if code, _ := fetch(t, base+"/readyz"); code != http.StatusOK {
		t.Errorf("/readyz: got %d, want 200", code)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", strings.TrimPrefix(base, "http://")); err == nil {
		t.Error("the endpoints are still listening after run stopped")
	}
}

func TestRunWithoutListenServesNothing(t *testing.T) {
	url, _ := fakeAnthropic(t)
	path, _ := writeConfig(t, "anthropic", url, 5)
	var logs syncBuffer
	stop := startRun(t, path, &logs)
	waitFor(t, "the first probe to finish", func() bool { return strings.Contains(logs.String(), "probe done") })
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "serving /metrics") {
		t.Error("run served the endpoints without serve.listen")
	}
}

func TestRunFailsWhenTheAddressIsTaken(t *testing.T) {
	taken, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	url, requests := fakeAnthropic(t)
	path, _ := writeConfig(t, "anthropic", url, 5, "serve: {listen: '"+taken.Addr().String()+"'}")
	err = run(t.Context(), path, slog.New(slog.DiscardHandler), nil)
	if err == nil || !strings.Contains(err.Error(), "serve.listen") {
		t.Fatalf("got %v, want an error naming serve.listen", err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d requests were sent before the endpoints failed", n)
	}
}

func TestServeServesTheStore(t *testing.T) {
	path, dbPath := writeConfig(t, "anthropic", "https://example.invalid", 5)
	db, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Insert(t.Context(), store.Sample{ProbeID: "p", Time: time.Now(), Target: "fake", Scenario: "standard-v1",
		Turn: 1, HTTPStatus: 200, ExpectOK: true})
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	var logs syncBuffer
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- serveStore(ctx, path, "127.0.0.1:0", slog.New(slog.NewJSONHandler(&logs, nil))) }()
	base := "http://" + addrIn(t, &logs)
	code, body := fetch(t, base+"/metrics")
	if code != http.StatusOK || !strings.Contains(body, `piplon_last_probe_success{scenario="standard-v1",target="fake"} 1`) {
		t.Errorf("got %d, want the stored probe in:\n%s", code, body)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop within 5 s")
	}
}

func TestServeRejectsABadListenFlag(t *testing.T) {
	path, _ := writeConfig(t, "anthropic", "https://example.invalid", 5)
	_, err := execute(t, "serve", "--config", path, "--listen", "nowhere")
	if err == nil || !strings.Contains(err.Error(), "--listen") {
		t.Fatalf("got %v, want an error naming --listen", err)
	}
}
