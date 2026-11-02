//go:build !devfixture

package app

import (
	"github.com/onprs/ByebyeOCS/server/internal/auth"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
)

// localDevLoginVerifier 只在显式带 devfixture 构建标签的本地二进制中编译。
func localDevLoginVerifier(config.Config) (auth.Verifier, bool, error) {
	return nil, false, nil
}
