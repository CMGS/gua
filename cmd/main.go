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

	"github.com/fsnotify/fsnotify"
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
	defaultBackend   = "wechat"
	defaultAgent     = "claude"
	defaultModel     = "sonnet"
	defaultClaudeCmd = "claude"

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
		printUsage(os.Stdout)
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

func cmdSetup(ctx context.Context, args []string) int {
	logger := log.WithFunc("cmd.setup")

	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	backendName := fs.String("backend", defaultBackend, "backend to setup")
	_ = fs.Parse(args)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	switch *backendName {
	case "wechat":
		w := wechat.New(nil, "")
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

	agentOpts := []claude.Option{
		claude.WithRuntime(runtmux.New(*tmuxName)),
		claude.WithClaudeCmd(*claudeCmd),
		claude.WithBridgeBin(*bridgeBin),
		claude.WithModel(*model),
		claude.WithWorkDir(*workDir),
	}

	known := make(map[string]bool, len(allCreds))
	var wg sync.WaitGroup
	for _, creds := range allCreds {
		known[creds.ILinkBotID] = true
		wg.Add(1)
		go func(creds *types.Credentials) {
			defer wg.Done()
			logger.Infof(ctx, "starting account %s: backend=%s agent=%s", creds.ILinkBotID, *backendName, *agentName)
			runAccount(ctx, creds, *backendName, *agentName, dir, agentOpts...)
		}(creds)
	}

	// Watch accounts directory for new accounts (e.g. from /share).
	go watchNewAccounts(ctx, dir, known, *backendName, *agentName, agentOpts...)

	wg.Wait()
	return 0
}

func runAccount(ctx context.Context, creds *types.Credentials, backendName, agentName, acctDir string, opts ...claude.Option) {
	logger := log.WithFunc("cmd.runAccount")
	botID := creds.ILinkBotID

	var b channel.Channel
	switch backendName {
	case "wechat":
		b = wechat.New(creds, acctDir)
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

func cmdAccounts(ctx context.Context, args []string) int {
	logger := log.WithFunc("cmd.accounts")

	var backend, subcmd string
	var positional []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--backend" && i+1 < len(args):
			backend = args[i+1]
			i++
		case args[i] == subcmdList || args[i] == subcmdRemove:
			subcmd = args[i]
		default:
			if !strings.HasPrefix(args[i], "-") {
				positional = append(positional, args[i])
			}
		}
	}
	if subcmd == "" {
		subcmd = subcmdList
	}
	if backend == "" {
		backend = defaultBackend
	}

	dir := accountsDir(backend)

	switch subcmd {
	case subcmdList:
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No accounts found. Run 'setup' first.")
				return 0
			}
			logger.Errorf(ctx, err, "read accounts dir")
			return 1
		}
		found := false
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			found = true
			name := strings.TrimSuffix(entry.Name(), ".json")
			info, err := entry.Info()
			if err != nil {
				continue
			}
			fmt.Printf("  %s  (created: %s)\n", name, info.ModTime().Format("2006-01-02 15:04"))
		}
		if !found {
			fmt.Println("No accounts found. Run 'setup' first.")
		}

	case subcmdRemove:
		if len(positional) == 0 {
			logger.Errorf(ctx, nil, "usage: gua-server accounts remove <account-id> [--backend wechat]")
			return 1
		}
		accountID := positional[0]
		normalized := utils.NormalizeID(accountID)
		path := filepath.Join(dir, normalized+".json")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			logger.Errorf(ctx, nil, "account not found: %s", accountID)
			return 1
		}
		if err := os.Remove(path); err != nil {
			logger.Errorf(ctx, err, "remove account %s", accountID)
			return 1
		}
		fmt.Printf("Removed account: %s\n", accountID)
	}

	return 0
}

func cmdSessions(ctx context.Context, args []string) int {
	logger := log.WithFunc("cmd.sessions")

	// Manual arg parsing: flag.Parse stops at the first non-flag, so
	// "users list --work-dir /tmp" would never parse --work-dir.
	var workDir, subcmd string
	var positional []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--work-dir" && i+1 < len(args):
			workDir = args[i+1]
			i++
		case args[i] == subcmdList || args[i] == subcmdRemove:
			subcmd = args[i]
		default:
			if !strings.HasPrefix(args[i], "-") {
				positional = append(positional, args[i])
			}
		}
	}
	if subcmd == "" {
		subcmd = subcmdList
	}
	if workDir == "" {
		logger.Errorf(ctx, nil, "--work-dir is required")
		return 1
	}

	sessionsDir := filepath.Join(workDir, "sessions")

	switch subcmd {
	case subcmdList:
		entries, err := os.ReadDir(sessionsDir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No users found.")
				return 0
			}
			logger.Errorf(ctx, err, "read sessions dir")
			return 1
		}
		found := false
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			found = true
			info, err := entry.Info()
			if err != nil {
				continue
			}
			fmt.Printf("  %s  (last modified: %s)\n", entry.Name(), info.ModTime().Format("2006-01-02 15:04"))
		}
		if !found {
			fmt.Println("No users found.")
		}

	case subcmdRemove:
		if len(positional) == 0 {
			logger.Errorf(ctx, nil, "usage: gua-server users remove <user-id> --work-dir <path>")
			return 1
		}
		userID := positional[0]
		normalized := utils.NormalizeID(userID)
		userDir := filepath.Join(sessionsDir, normalized)

		if _, err := os.Stat(userDir); os.IsNotExist(err) {
			userDir = filepath.Join(sessionsDir, userID)
			if _, err := os.Stat(userDir); os.IsNotExist(err) {
				logger.Errorf(ctx, nil, "user not found: %s", userID)
				return 1
			}
		}

		if err := os.RemoveAll(userDir); err != nil {
			logger.Errorf(ctx, err, "remove user %s", userID)
			return 1
		}
		fmt.Printf("Removed user: %s\n", userID)
	}

	return 0
}

// watchNewAccounts uses fsnotify to detect new credential files in the accounts
// directory. When a new .json file appears (e.g. from /share), starts the account.
func watchNewAccounts(ctx context.Context, dir string, known map[string]bool, backendName, agentName string, opts ...claude.Option) {
	logger := log.WithFunc("cmd.watchNewAccounts")

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Warnf(ctx, "fsnotify init failed, new accounts require restart: %v", err)
		return
	}
	defer func() { _ = watcher.Close() }()

	if err := watcher.Add(dir); err != nil {
		logger.Warnf(ctx, "watch %s failed: %v", dir, err)
		return
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create == 0 || !strings.HasSuffix(event.Name, ".json") {
				continue
			}
			creds, loadErr := auth.LoadCredentials(event.Name)
			if loadErr != nil {
				logger.Warnf(ctx, "load new account %s: %v", event.Name, loadErr)
				continue
			}
			if known[creds.ILinkBotID] {
				continue
			}
			known[creds.ILinkBotID] = true
			logger.Infof(ctx, "new account detected: %s", creds.ILinkBotID)
			go runAccount(ctx, creds, backendName, agentName, dir, opts...)
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			logger.Warnf(ctx, "fsnotify error: %v", err)
		case <-ctx.Done():
			return
		}
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
