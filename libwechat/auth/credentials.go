package auth

import (
	"github.com/CMGS/gua/libwechat/types"
	"github.com/CMGS/gua/utils"
)

// SaveCredentials marshals credentials to JSON and writes them to path
// with 0600 permissions. Parent directories are created as needed.
func SaveCredentials(path string, creds *types.Credentials) error {
	return utils.WriteJSONFile(path, creds, 0o600)
}

// LoadCredentials reads and unmarshals credentials from a JSON file.
func LoadCredentials(path string) (*types.Credentials, error) {
	return utils.ReadJSONFile[types.Credentials](path)
}

// NormalizeAccountID replaces @, ., and : with - for filesystem safety.
// Delegates to utils.NormalizeID.
func NormalizeAccountID(raw string) string {
	return utils.NormalizeID(raw)
}
