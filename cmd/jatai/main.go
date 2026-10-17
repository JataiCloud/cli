package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/jataicloud/cli/internal/cli"
)

// version é injetada pelo GoReleaser (-X main.version=…).
var version = "dev"

func main() {
	// SIGTERM é como os clientes MCP encerram o `jatai mcp`.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// Depois do primeiro Ctrl-C, o segundo encerra na hora.
	context.AfterFunc(ctx, stop)
	code := cli.Main(ctx, version, os.Args[1:])
	stop()
	os.Exit(code)
}
