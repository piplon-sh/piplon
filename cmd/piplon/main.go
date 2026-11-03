// Command piplon is an independent blackbox probe for LLM provider APIs.
//
// This is the entry point only. Subcommands are implemented in cmd/piplon/*.go
// and the real work lives under internal/.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newRootCmd().ExecuteContext(ctx)
	stop()
	if err == nil {
		return
	}
	code := 1
	var exit *exitError
	if errors.As(err, &exit) {
		code, err = exit.code, exit.err
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "piplon:", err)
	}
	os.Exit(code)
}

// exitError ends piplon with code, printing err when it is not nil. piplon
// check uses it for its exit codes; any other error exits 1.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit %d", e.code)
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }
