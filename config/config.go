package config

import _ "embed"

//go:embed base.md
var BaseMD string

//go:embed wechat.md
var WechatMD string

// MergedMD returns the combined CLAUDE.md content for the given backend.
func MergedMD(backendName string) string {
	md := BaseMD
	switch backendName {
	case "wechat":
		md += "\n\n" + WechatMD
	}
	return md
}
