package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/jataicloud/cli/internal/cli"
)

// version é injetada pelo GoReleaser (-X main.version=…).
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	// Depois do primeiro Ctrl-C, o segundo encerra na hora.
	context.AfterFunc(ctx, stop)
	code := cli.Main(ctx, version, os.Args[1:])
	stop()
	os.Exit(code)
}
