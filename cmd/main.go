package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/projecteru2/core/log"
	coretypes "github.com/projecteru2/core/types"

	"github.com/CMGS/gua/agent/claude"
	"github.com/CMGS/gua/channel"
	"github.com/CMGS/gua/channel/wechat"
	"github.com/CMGS/gua/config"
	"github.com/CMGS/gua/libc/wechat/auth"
	"github.com/CMGS/gua/libc/wechat/types"
	runtmux "github.com/CMGS/gua/runtime/tmux"
	"github.com/CMGS/gua/server"
	"github.com/CMGS/gua/utils"
)

const (
	defaultBackend = "wechat"
	defaultAgent   = "claude"
	defaultModel   = "sonnet"
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
		printUsage(os.Stdout)
	case "setup":
		os.Exit(cmdSetup(ctx, os.Args[2:]))
	case "start":
		os.Exit(cmdStart(ctx, os.Args[2:]))
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
	_, _ = fmt.Fprintln(w, "  setup    Setup backend authentication")
	_, _ = fmt.Fprintln(w, "  start    Start the server")
}

func cmdSetup(ctx context.Context, args []string) int {
	logger := log.WithFunc("cmd.setup")

	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	backendName := fs.String("backend", defaultBackend, "backend to setup")
	_ = fs.Parse(args)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	switch *backendName {
	case "wechat":
		w := wechat.New(nil)
		if err := w.Setup(ctx); err != nil {
			logger.Errorf(ctx, err, "setup failed")
			return 1
		}
		creds := w.Creds()
		credPath := filepath.Join(accountsDir(*backendName), utils.NormalizeID(creds.ILinkBotID)+".json")
		if err := auth.SaveCredentials(credPath, creds); err != nil {
			logger.Errorf(ctx, err, "save credentials")
			return 1
		}
		logger.Infof(ctx, "credentials saved to %s", credPath)
	default:
		logger.Errorf(ctx, nil, "unknown backend: %s", *backendName)
		return 1
	}
	return 0
}

func cmdStart(ctx context.Context, args []string) int {
	logger := log.WithFunc("cmd.start")

	fs := flag.NewFlagSet("start", flag.ExitOnError)
	backendName := fs.String("backend", defaultBackend, "backend name")
	agentName := fs.String("agent", defaultAgent, "agent name")
	workDir := fs.String("work-dir", "", "working directory for agent sessions (required)")
	model := fs.String("model", defaultModel, "model name")
	claudeCmd := fs.String("claude-cmd", "claude", "path to claude CLI binary")
	bridgeBin := fs.String("bridge-bin", "", "path to bridge binary (required)")
	tmuxName := fs.String("tmux-name", "gua", "tmux session name for runtime")
	_ = fs.Parse(args)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if *workDir == "" {
		logger.Errorf(ctx, nil, "--work-dir is required")
		return 1
	}
	if *bridgeBin == "" {
		logger.Errorf(ctx, nil, "--bridge-bin is required")
		return 1
	}

	dir := accountsDir(*backendName)
	allCreds, err := loadAllAccounts(ctx, dir)
	if err != nil {
		logger.Errorf(ctx, err, "load accounts from %s", dir)
		return 1
	}
	if len(allCreds) == 0 {
		logger.Errorf(ctx, nil, "no accounts in %s, run setup first", dir)
		return 1
	}

	var wg sync.WaitGroup
	for _, creds := range allCreds {
		wg.Add(1)
		go func(creds *types.Credentials) {
			defer wg.Done()
			botID := creds.ILinkBotID
			logger.Infof(ctx, "starting account %s: backend=%s agent=%s", botID, *backendName, *agentName)
			rt := runtmux.New(*tmuxName)
			runAccount(ctx, creds, *backendName, *agentName,
				claude.WithRuntime(rt),
				claude.WithClaudeCmd(*claudeCmd),
				claude.WithBridgeBin(*bridgeBin),
				claude.WithModel(*model),
				claude.WithWorkDir(*workDir),
			)
		}(creds)
	}
	wg.Wait()
	return 0
}

func runAccount(ctx context.Context, creds *types.Credentials, backendName, agentName string, opts ...claude.Option) {
	logger := log.WithFunc("cmd.runAccount")
	botID := creds.ILinkBotID

	var b channel.Channel
	switch backendName {
	case "wechat":
		b = wechat.New(creds)
	default:
		logger.Errorf(ctx, fmt.Errorf("unknown backend: %s", backendName), "account %s", botID)
		return
	}

	// Init prompt = base (security) + agent (claude) + backend (wechat) + media instructions
	initPrompt := config.BaseMD + "\n\n" + claude.PromptMD + "\n\n" + wechat.PromptMD + "\n\n" + b.Presenter().MediaInstructions()
	opts = append(opts, claude.WithClaudeMD(initPrompt))

	switch agentName {
	case "claude":
		a, err := claude.New(ctx, opts...)
		if err != nil {
			logger.Errorf(ctx, err, "create agent for account %s", botID)
			return
		}

		srv := server.New(b, a)
		logger.Infof(ctx, "account %s running: backend=%s agent=%s", botID, backendName, agentName)
		if err := srv.Run(ctx); err != nil {
			logger.Warnf(ctx, "account %s server exited: %v", botID, err)
		}
	default:
		logger.Errorf(ctx, fmt.Errorf("unknown agent: %s", agentName), "account %s", botID)
	}
}

func loadAllAccounts(ctx context.Context, dir string) ([]*types.Credentials, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read accounts dir %s: %w", dir, err)
	}

	var accounts []*types.Credentials
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		creds, err := auth.LoadCredentials(path)
		if err != nil {
			log.WithFunc("cmd.loadAllAccounts").Warnf(ctx, "skip invalid account file %s: %v", path, err)
			continue
		}
		accounts = append(accounts, creds)
	}
	return accounts, nil
}

func accountsDir(backendName string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".gua", backendName, "accounts")
}
