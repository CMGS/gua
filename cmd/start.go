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

	"github.com/CMGS/gua/agent/claude"
	"github.com/CMGS/gua/channel"
	tg "github.com/CMGS/gua/channel/telegram"
	"github.com/CMGS/gua/channel/wechat"
	"github.com/CMGS/gua/config"
	"github.com/CMGS/gua/libc/wechat/auth"
	"github.com/CMGS/gua/libc/wechat/types"
	runtmux "github.com/CMGS/gua/runtime/tmux"
	"github.com/CMGS/gua/server"
	"github.com/CMGS/gua/utils"
)

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
	promptFile := fs.String("prompt", "", "path to a custom .md file appended to the init prompt")
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

	var userPrompt string
	if *promptFile != "" {
		data, readErr := os.ReadFile(*promptFile) //nolint:gosec
		if readErr != nil {
			logger.Errorf(ctx, readErr, "read prompt file %s", *promptFile)
			return 1
		}
		userPrompt = string(data)
	}

	agentOpts := []claude.Option{
		claude.WithRuntime(runtmux.New(*tmuxName)),
		claude.WithClaudeCmd(*claudeCmd),
		claude.WithBridgeBin(*bridgeBin),
		claude.WithModel(*model),
		claude.WithWorkDir(*workDir),
	}

	if *agentName != "claude" {
		logger.Errorf(ctx, fmt.Errorf("unknown agent: %s", *agentName), "only 'claude' is supported")
		return 1
	}

	switch *backendName {
	case "telegram":
		return startTelegram(ctx, userPrompt, agentOpts...)
	default:
		return startWechat(ctx, *backendName, *agentName, userPrompt, agentOpts...)
	}
}

// buildInitPrompt assembles the agent init prompt from base + agent + channel parts.
func buildInitPrompt(channelPrompt string, ch channel.Channel, userPrompt string) string {
	initPrompt := config.BaseMD + "\n\n" + claude.PromptMD + "\n\n" + channelPrompt + "\n\n" + ch.Presenter().MediaInstructions()
	if userPrompt != "" {
		initPrompt += "\n\n" + userPrompt
	}
	return initPrompt
}

func startTelegram(ctx context.Context, userPrompt string, opts ...claude.Option) int {
	logger := log.WithFunc("cmd.startTelegram")

	type tokenCreds struct {
		Token string `json:"token"`
	}

	dir := accountsDir("telegram")
	creds, err := utils.ReadJSONFile[tokenCreds](filepath.Join(dir, "bot.json"))
	if err != nil {
		logger.Errorf(ctx, err, "load telegram credentials from %s", dir)
		return 1
	}
	if creds.Token == "" {
		logger.Errorf(ctx, nil, "empty token in %s, run setup first", dir)
		return 1
	}

	b := tg.New(creds.Token)
	opts = append(opts, claude.WithClaudeMD(buildInitPrompt(tg.PromptMD, b, userPrompt)))

	a, err := claude.New(ctx, opts...)
	if err != nil {
		logger.Errorf(ctx, err, "create agent")
		return 1
	}

	srv := server.New(b, a)
	logger.Info(ctx, "telegram bot running")
	if err := srv.Run(ctx); err != nil {
		logger.Warnf(ctx, "server exited: %v", err)
	}
	return 0
}

func startWechat(ctx context.Context, backendName, agentName, userPrompt string, opts ...claude.Option) int {
	logger := log.WithFunc("cmd.startWechat")

	dir := accountsDir(backendName)
	allCreds, err := loadAllAccounts(ctx, dir)
	if err != nil {
		logger.Errorf(ctx, err, "load accounts from %s", dir)
		return 1
	}
	if len(allCreds) == 0 {
		logger.Errorf(ctx, nil, "no accounts in %s, run setup first", dir)
		return 1
	}

	known := make(map[string]bool, len(allCreds))
	var wg sync.WaitGroup
	for _, creds := range allCreds {
		known[creds.ILinkBotID] = true
		wg.Add(1)
		go func(creds *types.Credentials) {
			defer wg.Done()
			logger.Infof(ctx, "starting account %s: backend=%s agent=%s", creds.ILinkBotID, backendName, agentName)
			runAccount(ctx, creds, backendName, agentName, dir, userPrompt, opts...)
		}(creds)
	}

	go watchNewAccounts(ctx, dir, known, backendName, agentName, userPrompt, opts...)

	wg.Wait()
	return 0
}

func runAccount(ctx context.Context, creds *types.Credentials, backendName, agentName, acctDir, userPrompt string, opts ...claude.Option) {
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

	opts = append(opts, claude.WithClaudeMD(buildInitPrompt(wechat.PromptMD, b, userPrompt)))

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

func watchNewAccounts(ctx context.Context, dir string, known map[string]bool, backendName, agentName, userPrompt string, opts ...claude.Option) {
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
			go runAccount(ctx, creds, backendName, agentName, dir, userPrompt, opts...)
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
