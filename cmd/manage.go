package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/projecteru2/core/log"

	"github.com/CMGS/gua/libc/wechat/auth"
	"github.com/CMGS/gua/libc/wechat/types"
	"github.com/CMGS/gua/utils"
)

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
			logger.Errorf(ctx, nil, "usage: gua-server sessions remove <user-id> --work-dir <path>")
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
