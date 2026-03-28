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

	agentpkg "github.com/CMGS/gua/agent"
	"github.com/CMGS/gua/agent/claude"
	"github.com/CMGS/gua/agent/codex"
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

// agentConfig holds parsed CLI flags needed to create any agent.
type agentConfig struct {
	name      string
	model     string
	workDir   string
	claudeCmd string
	codexCmd  string
	bridgeBin string
	tmuxName  string
}

func cmdStart(ctx context.Context, args []string) int {
	logger := log.WithFunc("cmd.start")

	fs := flag.NewFlagSet("start", flag.ExitOnError)
	backendName := fs.String("backend", defaultBackend, "backend name")
	agentName := fs.String("agent", defaultAgent, "agent name (claude, codex)")
	workDir := fs.String("work-dir", "", "working directory for agent sessions (required)")
	model := fs.String("model", "", "model name (default: sonnet for claude, gpt default for codex)")
	claudeCmd := fs.String("claude-cmd", "claude", "path to claude CLI binary")
	codexCmd := fs.String("codex-cmd", "codex", "path to codex CLI binary")
	bridgeBin := fs.String("bridge-bin", "", "path to bridge binary (required for claude)")
	tmuxName := fs.String("tmux-name", "gua", "tmux session name for runtime")
	promptFile := fs.String("prompt", "", "path to a custom .md file appended to the init prompt")
	_ = fs.Parse(args)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if *workDir == "" {
		logger.Errorf(ctx, nil, "--work-dir is required")
		return 1
	}
	if *agentName == defaultAgent && *bridgeBin == "" {
		logger.Errorf(ctx, nil, "--bridge-bin is required for claude agent")
		return 1
	}

	switch *agentName {
	case "claude", "codex":
	default:
		logger.Errorf(ctx, fmt.Errorf("unknown agent: %s", *agentName), "supported: claude, codex")
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

	ac := &agentConfig{
		name:      *agentName,
		model:     *model,
		workDir:   *workDir,
		claudeCmd: *claudeCmd,
		codexCmd:  *codexCmd,
		bridgeBin: *bridgeBin,
		tmuxName:  *tmuxName,
	}

	switch *backendName {
	case "telegram":
		return startTelegram(ctx, ac, userPrompt)
	default:
		return startWechat(ctx, *backendName, ac, userPrompt)
	}
}

// buildInitPrompt assembles the agent init prompt from base + agent + channel parts.
func buildInitPrompt(agentPrompt, channelPrompt string, ch channel.Channel, userPrompt string) string {
	initPrompt := config.BaseMD + "\n\n" + agentPrompt + "\n\n" + channelPrompt + "\n\n" + ch.Presenter().MediaInstructions()
	if userPrompt != "" {
		initPrompt += "\n\n" + userPrompt
	}
	return initPrompt
}

// createAgent creates an agent based on the config.
func createAgent(ctx context.Context, ac *agentConfig, ch channel.Channel, channelPrompt, userPrompt string) (agentpkg.Agent, error) {
	rt := runtmux.New(ac.tmuxName)

	switch ac.name {
	case "claude":
		agentMD := buildInitPrompt(claude.PromptMD, channelPrompt, ch, userPrompt)
		return claude.New(ctx,
			claude.WithRuntime(rt),
			claude.WithClaudeCmd(ac.claudeCmd),
			claude.WithBridgeBin(ac.bridgeBin),
			claude.WithModel(ac.model),
			claude.WithWorkDir(ac.workDir),
			claude.WithClaudeMD(agentMD),
		)
	case "codex":
		agentMD := buildInitPrompt(codex.PromptMD, channelPrompt, ch, userPrompt)
		return codex.New(ctx,
			codex.WithRuntime(rt),
			codex.WithCodexCmd(ac.codexCmd),
			codex.WithModel(ac.model),
			codex.WithWorkDir(ac.workDir),
			codex.WithInitPrompt(agentMD),
		)
	default:
		return nil, fmt.Errorf("unknown agent: %s", ac.name)
	}
}

func startTelegram(ctx context.Context, ac *agentConfig, userPrompt string) int {
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
	a, err := createAgent(ctx, ac, b, tg.PromptMD, userPrompt)
	if err != nil {
		logger.Errorf(ctx, err, "create agent")
		return 1
	}

	srv := server.New(b, a)
	logger.Infof(ctx, "telegram bot running: agent=%s", ac.name)
	if err := srv.Run(ctx); err != nil {
		logger.Warnf(ctx, "server exited: %v", err)
	}
	return 0
}

func startWechat(ctx context.Context, backendName string, ac *agentConfig, userPrompt string) int {
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
			logger.Infof(ctx, "starting account %s: backend=%s agent=%s", creds.ILinkBotID, backendName, ac.name)
			runAccount(ctx, creds, backendName, ac, dir, userPrompt)
		}(creds)
	}

	go watchNewAccounts(ctx, dir, known, backendName, ac, userPrompt)

	wg.Wait()
	return 0
}

func runAccount(ctx context.Context, creds *types.Credentials, backendName string, ac *agentConfig, acctDir, userPrompt string) {
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

	a, err := createAgent(ctx, ac, b, wechat.PromptMD, userPrompt)
	if err != nil {
		logger.Errorf(ctx, err, "create agent for account %s", botID)
		return
	}

	srv := server.New(b, a)
	logger.Infof(ctx, "account %s running: backend=%s agent=%s", botID, backendName, ac.name)
	if err := srv.Run(ctx); err != nil {
		logger.Warnf(ctx, "account %s server exited: %v", botID, err)
	}
}

func watchNewAccounts(ctx context.Context, dir string, known map[string]bool, backendName string, ac *agentConfig, userPrompt string) {
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
			go runAccount(ctx, creds, backendName, ac, dir, userPrompt)
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
