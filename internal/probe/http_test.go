package probe

import (
	"strings"
	"testing"

	"github.com/piplon-sh/piplon/internal/config"
)

func TestProviderMessageRedactsKeys(t *testing.T) {
	for msg, want := range map[string]string{
		"Incorrect API key provided: sk-proj-****************Ab12. You can find": "Incorrect API key provided: [redacted]. You can find",
		"invalid key sk-ant-api03-AbCdEf_12-xyz":                                 "invalid key [redacted]",
		"key gsk_0123456789abcdef is revoked":                                    "key [redacted] is revoked",
		"API key AIzaSyA-0123456789abcdefghijklmnopqrstu not valid":              "API key [redacted] not valid",
		"key ending in ****wxyz was rejected":                                    "key ending in [redacted] was rejected",
		"Overloaded":                                                             "Overloaded",
		"sk-short and a task-id 1234":                                            "sk-short and a task-id 1234",
	} {
		if got := providerMessage("e", msg, ""); got != "e: "+want {
			t.Errorf("%q: got %q, want %q", msg, got, "e: "+want)
		}
	}
}

func TestRedactionHappensBeforeTheCut(t *testing.T) {
	msg := strings.Repeat("x", maxErrorMessage-10) + " sk-proj-0123456789abcdefghij"
	if got := providerMessage("e", msg, ""); strings.Contains(got, "sk-") || !strings.Contains(got, " [redact") {
		t.Errorf("got %q", got)
	}
}

func TestProviderMessageRedactsTheExactKey(t *testing.T) {
	key := config.Secret("piplon-test-key-0123")
	msg := "key piplon-test-key-0123 has no access to this model"
	if got := providerMessage("e", msg, key); got != "e: key [redacted] has no access to this model" {
		t.Errorf("got %q", got)
	}
	// Cut to length after the redaction, so not even its start survives.
	long := strings.Repeat("x", maxErrorMessage-10) + " " + string(key)
	if got := providerMessage("e", long, key); strings.Contains(got, "piplon-te") {
		t.Errorf("part of the key survived the cut: %q", got)
	}
	// Below 8 characters, ordinary words would be replaced too.
	if got := redact("the key abc is short", "abc"); got != "the key abc is short" {
		t.Errorf("short key: got %q", got)
	}
}

func TestTransportErrorsNameTheCauseNotTheURL(t *testing.T) {
	key := config.Secret("piplon-test-key-0123")
	// Port 1 on loopback refuses at once; the URL path quotes the key.
	for _, p := range []Prober{
		&Anthropic{URL: "http://127.0.0.1:1/piplon-test-key-0123", Model: "m", Key: key},
		&OpenAI{URL: "http://127.0.0.1:1/piplon-test-key-0123", Model: "m", Key: key},
	} {
		res := p.Probe(t.Context(), loadScenario(t))
		if len(res.Turns) != 1 {
			t.Fatalf("got %+v", res.Turns)
		}
		msg := res.Turns[0].Error
		for _, leak := range []string{string(key), "http://", "/piplon-test", "/v1/messages", "/chat/completions"} {
			if strings.Contains(msg, leak) {
				t.Errorf("%T: error %q contains %q", p, msg, leak)
			}
		}
		if !strings.Contains(msg, "connection refused") {
			t.Errorf("%T: error %q does not name the cause", p, msg)
		}
	}
}
