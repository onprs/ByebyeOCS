package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/onprs/ByebyeOCS/server/internal/command"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := command.Run(ctx, os.Args[1:], os.LookupEnv, os.Stdout, os.Stderr)
	stop()
	if err != nil {
		command.WriteError(os.Stderr, err)
		os.Exit(1)
	}
}
