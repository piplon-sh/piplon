package analyze

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/piplon-sh/piplon/internal/probe"
	"github.com/piplon-sh/piplon/internal/store"
)

func openStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "piplon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

var (
	badKey     = probe.Turn{Error: "http 401: authentication_error: invalid x-api-key", HTTPStatus: 401, ProviderError: "authentication_error"}
	overloaded = probe.Turn{Error: "http 529: overloaded_error: Overloaded", HTTPStatus: 529, ProviderError: "overloaded_error"}
)

// at is the probe n minutes after monday.
func at(o probe.Outcome, n int) probe.Outcome {
	o.Start = monday.Add(time.Duration(n) * time.Minute)
	return o
}

func TestIncidentLifecycle(t *testing.T) {
	db := openStore(t)
	a := New(db, "standard-v1", 2)
	ok := outcome(turn(1000*ms, 0), turn(900*ms, 4000), turn(900*ms, 4000))
	steps := []struct {
		o     probe.Outcome
		state State
		event Event
	}{
		{outcome(badKey), Down, ""}, // down at once, but no incident on one probe
		{outcome(badKey), Down, ""},
		{outcome(badKey), Down, Opened},
		{outcome(turn(1000*ms, 0), overloaded), Down, ""},
		{outcome(turn(1000*ms, 0), overloaded), Down, ""},
		{outcome(turn(1000*ms, 0), overloaded), Errors, Updated},
		{ok, Errors, ""},
		{ok, Errors, ""},
		{ok, OK, Closed},
		{ok, OK, ""},
	}
	var id int64
	for i, s := range steps {
		st, err := a.Observe(t.Context(), at(s.o, i))
		if err != nil {
			t.Fatal(err)
		}
		if st.State != s.state || st.Event != s.event {
			t.Fatalf("probe %d: got state %s event %q, want %s %q", i+1, st.State, st.Event, s.state, s.event)
		}
		switch st.Event {
		case Opened:
			id = st.Incident.ID
			if st.Incident.State != "down" || st.Incident.Cause != cause(Down, badKey) || !st.Incident.Opened.Equal(monday.Add(2*time.Minute)) {
				t.Errorf("opened: %+v", st.Incident)
			}
		case Updated:
			if st.Incident.ID != id || st.Incident.State != "errors" || st.Incident.Cause != "the provider is overloaded" {
				t.Errorf("updated: %+v", st.Incident)
			}
		case Closed:
			if st.Incident.ID != id || !st.Incident.Resolved || st.Incident.CloseCause != closeRecovered {
				t.Errorf("closed: %+v", st.Incident)
			}
		}
	}
	if open, _ := db.OpenIncidents(t.Context()); len(open) != 0 {
		t.Errorf("still open in the store: %+v", open)
	}
}

func TestRestoreResumesAndClosesRemovedTargets(t *testing.T) {
	db := openStore(t)
	for _, target := range []string{"t", "old"} {
		if _, err := db.OpenIncident(t.Context(), store.Incident{Target: target, Scenario: "standard-v1", State: "down", Opened: monday}); err != nil {
			t.Fatal(err)
		}
	}
	a := New(db, "standard-v1", 2)
	closed, err := a.Restore(t.Context(), []string{"t"}, monday.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(closed) != 1 || closed[0].Target != "old" || closed[0].Resolved || closed[0].CloseCause != "target removed from config" {
		t.Fatalf("administrative closes: got %+v", closed)
	}

	st, _ := a.Observe(t.Context(), at(outcome(badKey), 61))
	if st.State != Down || st.Event != "" || st.Incident.Target != "t" {
		t.Errorf("after restart: got %+v, want the incident resumed, not a new one", st)
	}
	ok := outcome(turn(1000*ms, 0), turn(900*ms, 4000), turn(900*ms, 4000))
	for i := range Confirm {
		st, _ = a.Observe(t.Context(), at(ok, 62+i))
	}
	if st.Event != Closed || !st.Incident.Resolved {
		t.Errorf("after 3 ok: got %+v", st)
	}
	if open, _ := db.OpenIncidents(t.Context()); len(open) != 0 {
		t.Errorf("still open: %+v", open)
	}
}

func TestCauses(t *testing.T) {
	for _, tt := range []struct {
		s    State
		t    probe.Turn
		want string
	}{
		{Down, badKey, "the key was rejected: check its value, its permissions and the account's billing"},
		{Errors, overloaded, "the provider is overloaded"},
		{Down, probe.Turn{Error: "http 429: rate_limit_error: slow down", HTTPStatus: 429}, "rate limit or quota reached for this key"},
		{Down, probe.Turn{Error: "timeout"}, "no complete answer within the timeout: the provider or the network is slow"},
		{Down, probe.Turn{Error: "dial tcp: lookup api.example.com: no such host"}, "no HTTP answer: DNS, TLS or the network between this host and the provider"},
		{Errors, probe.Turn{Error: "stream ended before message_stop", HTTPStatus: 200, StreamCut: true}, "the stream was cut before the end of the answer"},
		{Down, probe.Turn{Error: "http 502", HTTPStatus: 502}, "the provider or a gateway returned a server error"},
		{Gone, probe.Turn{Error: "http 404", HTTPStatus: 404}, "the model is not found: retired, renamed, or not enabled for this key or region"},
		{Refused, probe.Turn{Error: "refused: bio"}, "the model declined the scenario (category bio)"},
		{Refused, probe.Turn{Error: "refused"}, "the model declined the scenario"},
		{Errors, probe.Turn{}, "the answer did not have the expected shape"},
	} {
		if got := cause(tt.s, tt.t); got != tt.want {
			t.Errorf("%s %q: got %q, want %q", tt.s, tt.t.Error, got, tt.want)
		}
	}
}
