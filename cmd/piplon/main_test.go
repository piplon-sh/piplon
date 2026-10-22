package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/piplon-sh/piplon/scenarios"
)

// execute runs the root command with args and returns what it wrote to stdout.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		out, err := execute(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.HasPrefix(out, "piplon ") {
			t.Errorf("%v: got %q, want a line starting with \"piplon \"", args, out)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	if _, err := execute(t, "nope"); err == nil {
		t.Fatal("expected an error for an unknown command")
	}
}

func TestRunIsAStub(t *testing.T) {
	_, err := execute(t, "run")
	if !errors.Is(err, errNotImplemented) {
		t.Fatalf("got %v, want errNotImplemented", err)
	}
}

func TestScenarioShowDefault(t *testing.T) {
	out, err := execute(t, "scenario", "show")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid YAML: %v", err)
	}
	if got["name"] != "standard" || got["version"] != 1 {
		t.Errorf("got name=%v version=%v, want standard / 1", got["name"], got["version"])
	}
}

func TestScenarioShowRoundTrip(t *testing.T) {
	out, err := execute(t, "scenario", "show", "standard-v1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := scenarios.FS.ReadFile("standard-v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := yaml.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	wantText, _ := yaml.Marshal(want)
	gotText, _ := yaml.Marshal(got)
	if !bytes.Equal(wantText, gotText) {
		t.Errorf("printed scenario differs from the shipped file\n--- want\n%s\n--- got\n%s", wantText, gotText)
	}
}

func TestScenarioShowUnknown(t *testing.T) {
	_, err := execute(t, "scenario", "show", "nope-v9")
	if err == nil {
		t.Fatal("expected an error for an unknown scenario")
	}
	if !strings.Contains(err.Error(), "standard-v1") {
		t.Errorf("error %q should list the available scenarios", err)
	}
}
