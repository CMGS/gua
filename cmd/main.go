package main

import (
	"context"
	"fmt"
	"os"

	"github.com/projecteru2/core/log"
	coretypes "github.com/projecteru2/core/types"

	"github.com/CMGS/gua/version"
)

const (
	defaultBackend = "wechat"
	defaultAgent   = "claude"
	defaultModel   = "sonnet"

	subcmdList   = "list"
	subcmdRemove = "remove"
)

func main() {
	ctx := context.Background()
	initLogging(ctx)

	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "-h", "--help", "help":
		_, _ = fmt.Fprintf(os.Stdout, "gua %s (rev=%s, built=%s)\n\n", version.VERSION, version.REVISION, version.BUILTAT)
		printUsage(os.Stdout)
	case "-v", "--version", "version":
		fmt.Printf("gua %s (rev=%s, built=%s)\n", version.VERSION, version.REVISION, version.BUILTAT)
	case "setup":
		os.Exit(cmdSetup(ctx, os.Args[2:]))
	case "start":
		os.Exit(cmdStart(ctx, os.Args[2:]))
	case "accounts":
		os.Exit(cmdAccounts(ctx, os.Args[2:]))
	case "sessions":
		os.Exit(cmdSessions(ctx, os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage(os.Stderr)
		os.Exit(1)
	}
}

func initLogging(ctx context.Context) {
	if err := log.SetupLog(ctx, &coretypes.ServerLogConfig{
		Level:   "debug",
		UseJSON: false,
	}, ""); err != nil {
		fmt.Fprintf(os.Stderr, "init log: %v\n", err)
		os.Exit(1)
	}
}

func printUsage(w *os.File) {
	_, _ = fmt.Fprintln(w, "Usage: gua <command>")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Commands:")
	_, _ = fmt.Fprintln(w, "  setup      Setup backend authentication (QR code login)")
	_, _ = fmt.Fprintln(w, "  start      Start the server")
	_, _ = fmt.Fprintln(w, "  accounts   Manage bot accounts (list, remove)")
	_, _ = fmt.Fprintln(w, "  sessions   Manage user chat sessions (list, remove)")
}
