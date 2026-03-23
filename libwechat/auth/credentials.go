package auth

import (
	"strings"

	"github.com/CMGS/gua/libwechat/types"
	"github.com/CMGS/gua/libwechat/utils/jsonfile"
)

var accountIDReplacer = strings.NewReplacer("@", "-", ".", "-", ":", "-")

// SaveCredentials marshals credentials to JSON and writes them to path
// with 0600 permissions. Parent directories are created as needed.
func SaveCredentials(path string, creds *types.Credentials) error {
	return jsonfile.Write(path, creds, 0o600)
}

// LoadCredentials reads and unmarshals credentials from a JSON file.
func LoadCredentials(path string) (*types.Credentials, error) {
	return jsonfile.Read[types.Credentials](path)
}

// NormalizeAccountID replaces @, ., and : with - for filesystem safety.
func NormalizeAccountID(raw string) string {
	return accountIDReplacer.Replace(raw)
}
