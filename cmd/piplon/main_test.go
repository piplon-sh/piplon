package main

import "testing"

func TestRunVersion(t *testing.T) {
	if err := run([]string{"version"}); err != nil {
		t.Fatalf("version: %v", err)
	}
}

func TestRunUnknown(t *testing.T) {
	if err := run([]string{"nope"}); err == nil {
		t.Fatal("expected an error for an unknown command")
	}
}
