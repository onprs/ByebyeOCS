package chaoxing

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
	"sync"
)

// secretfont_features.bin.gz 由 server/tools/secretfont-features 从官方字体生成，
// 只用于精确签名未收录时的形状兜底；维护步骤见 docs/development/secretfont-index.md。
//
//go:embed secretfont_features.bin.gz
var secretFeatureData []byte

var (
	secretFeatureOnce  sync.Once
	secretFeatureCache *secretFeatureTable
)

// secretFeatures 返回兜底特征表；数据缺失或损坏时返回 nil，解码会自动退化为精确索引。
func secretFeatures() *secretFeatureTable {
	secretFeatureOnce.Do(func() {
		if len(secretFeatureData) == 0 {
			return
		}
		reader, err := gzip.NewReader(bytes.NewReader(secretFeatureData))
		if err != nil {
			return
		}
		payload, err := io.ReadAll(reader)
		if err != nil {
			return
		}
		table, err := parseSecretFeatureTable(payload)
		if err != nil {
			return
		}
		secretFeatureCache = table
	})
	return secretFeatureCache
}
