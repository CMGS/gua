package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/projecteru2/core/log"

	tg "github.com/CMGS/gua/channel/telegram"
	"github.com/CMGS/gua/channel/wechat"
	"github.com/CMGS/gua/libc/wechat/auth"
	"github.com/CMGS/gua/utils"
)

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
	case "telegram":
		fmt.Print("Enter Telegram bot token (from @BotFather): ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			logger.Errorf(ctx, nil, "failed to read token")
			return 1
		}
		token := strings.TrimSpace(scanner.Text())
		if token == "" {
			logger.Errorf(ctx, nil, "token is empty")
			return 1
		}

		t := tg.New(token)
		if err := t.Setup(ctx); err != nil {
			logger.Errorf(ctx, err, "setup failed")
			return 1
		}
		creds := map[string]string{"token": token}
		credPath := filepath.Join(accountsDir(*backendName), "bot.json")
		if err := utils.WriteJSONFile(credPath, &creds, 0o600); err != nil {
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
