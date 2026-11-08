// 维护工具：从已核对来源的原版字体生成完整几何轮廓参考库。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"

	"github.com/onprs/ByebyeOCS/server/internal/adapters/chaoxing"
)

func main() {
	fontPath := flag.String("font", "", "源字体（TTF/OTF，使用完整 Unicode cmap）")
	outPath := flag.String("out", "", "输出 SFR2 gzip 轮廓参考库")
	expected := flag.String("sha256", "", "源字体预期 SHA-256，提供时必须一致")
	flag.Parse()
	if *fontPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "需要 -font 与 -out")
		os.Exit(2)
	}
	data, err := os.ReadFile(*fontPath)
	if err != nil {
		fail(err)
	}
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if *expected != "" && *expected != actual {
		fail(fmt.Errorf("源字体 SHA-256 不符：%s", actual))
	}
	result, err := chaoxing.BuildSecretFontReference(data)
	if err != nil {
		fail(err)
	}
	if err = os.WriteFile(*outPath, result, 0644); err != nil {
		fail(err)
	}
	fmt.Printf("已生成轮廓参考库：%d 字节；源字体 SHA-256=%s\n", len(result), actual)
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
