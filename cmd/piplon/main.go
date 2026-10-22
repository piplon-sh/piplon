// Command piplon is an independent blackbox probe for LLM provider APIs.
//
// This is the entry point only. Subcommands are implemented in cmd/piplon/*.go
// and the real work lives under internal/.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "piplon:", err)
		os.Exit(1)
	}
}
