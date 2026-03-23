package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/projecteru2/core/log"
	coretypes "github.com/projecteru2/core/types"

	"github.com/CMGS/gua/agent/claude"
	"github.com/CMGS/gua/backend/wechat"
	"github.com/CMGS/gua/config"
	"github.com/CMGS/gua/libwechat/auth"
	"github.com/CMGS/gua/server"
)

const (
	defaultBackend   = "wechat"
	defaultAgent     = "claude"
	defaultModel     = "sonnet"
	defaultClaudeCmd = "claude"
)

func main() {
	initLogging()

	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "-h", "--help", "help":
		printUsage(os.Stdout)
	case "setup":
		cmdSetup(os.Args[2:])
	case "start":
		cmdStart(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage(os.Stderr)
		os.Exit(1)
	}
}

func initLogging() {
	if err := log.SetupLog(context.Background(), &coretypes.ServerLogConfig{
		Level:   "info",
		UseJSON: false,
	}, ""); err != nil {
		fmt.Fprintf(os.Stderr, "init log: %v\n", err)
		os.Exit(1)
	}
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, "Usage: gua <command>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  setup    Setup backend authentication")
	fmt.Fprintln(w, "  start    Start the server")
}

func cmdSetup(args []string) {
	logger := log.WithFunc("cmd.setup")

	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	backendName := fs.String("backend", defaultBackend, "backend to setup")
	fs.Parse(args) //nolint:errcheck

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	switch *backendName {
	case "wechat":
		w := wechat.New(nil)
		if err := w.Setup(ctx); err != nil {
			logger.Errorf(ctx, err, "%s", "setup failed")
			os.Exit(1)
		}
		credPath := credsPath("wechat")
		if err := auth.SaveCredentials(credPath, w.Creds()); err != nil {
			logger.Errorf(ctx, err, "%s", "save credentials")
			os.Exit(1)
		}
		logger.Infof(ctx, "credentials saved to %s", credPath)
	default:
		fmt.Fprintf(os.Stderr, "unknown backend: %s\n", *backendName)
		os.Exit(1)
	}
}

func cmdStart(args []string) {
	logger := log.WithFunc("cmd.start")

	fs := flag.NewFlagSet("start", flag.ExitOnError)
	backendName := fs.String("backend", defaultBackend, "backend name")
	agentName := fs.String("agent", defaultAgent, "agent name")
	workDir := fs.String("work-dir", "", "working directory for agent sessions (required)")
	model := fs.String("model", defaultModel, "model name")
	claudeCmd := fs.String("claude-cmd", defaultClaudeCmd, "path to claude CLI binary")
	bridgeBin := fs.String("bridge-bin", "", "path to bridge binary (required)")
	fs.Parse(args) //nolint:errcheck

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if *workDir == "" {
		fmt.Fprintln(os.Stderr, "--work-dir is required")
		os.Exit(1)
	}
	if *bridgeBin == "" {
		fmt.Fprintln(os.Stderr, "--bridge-bin is required")
		os.Exit(1)
	}

	credPath := credsPath(*backendName)
	creds, err := auth.LoadCredentials(credPath)
	if err != nil {
		logger.Errorf(ctx, err, "load credentials from %s", credPath)
		os.Exit(1)
	}

	var b *wechat.WeChat
	switch *backendName {
	case "wechat":
		b = wechat.New(creds)
	default:
		fmt.Fprintf(os.Stderr, "unknown backend: %s\n", *backendName)
		os.Exit(1)
	}

	claudeMD := config.MergedMD(*backendName)

	switch *agentName {
	case "claude":
		a, err := claude.New(ctx,
			claude.WithClaudeCmd(*claudeCmd),
			claude.WithBridgeBin(*bridgeBin),
			claude.WithModel(*model),
			claude.WithWorkDir(*workDir),
			claude.WithClaudeMD(claudeMD),
		)
		if err != nil {
			logger.Errorf(ctx, err, "%s", "create agent")
			os.Exit(1)
		}

		srv := server.New(b, a)
		logger.Infof(ctx, "starting gua: backend=%s agent=%s workdir=%s", *backendName, *agentName, *workDir)
		if err := srv.Run(ctx); err != nil {
			logger.Warnf(ctx, "server exited: %v", err)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown agent: %s\n", *agentName)
		os.Exit(1)
	}
}

func credsPath(backendName string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".gua", backendName, "account.json")
}
