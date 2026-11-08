// 临时诊断工具：比较页面字形签名与索引覆盖，定位未收录签名。
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/adapters/chaoxing"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "用法: check-signatures <页面.html> <索引.txt>")
		os.Exit(2)
	}
	payload, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	signatures, err := chaoxing.SecretFontSignatures(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, "签名读取失败:", err)
		os.Exit(1)
	}
	index := map[string]bool{}
	file, err := os.Open(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		signature, _, found := strings.Cut(line, "\t")
		if found {
			index[signature] = true
		}
	}
	missing := 0
	for code, signature := range signatures {
		if !index[signature] {
			missing++
			fmt.Printf("missing %U %s\n", code, signature)
		}
	}
	fmt.Printf("total=%d missing=%d\n", len(signatures), missing)
}
