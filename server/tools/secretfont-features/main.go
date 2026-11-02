// 维护工具：从官方字体生成字形兜底特征表（FRB2）。
//
// 生成结果由 server/internal/adapters/chaoxing/secretfont_features.bin 使用；
// 运行时只读取，不参与请求路径的字体解析。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/adapters/chaoxing"
)

func main() {
	fontPath := flag.String("font", "", "源字体文件（含 glyf 的 TTF）")
	out := flag.String("out", "", "输出特征表")
	ranges := flag.String("ranges", "0x20-0x7e,0x3000-0x303f,0xff01-0xff5e", "十六进制码点区间，逗号分隔")
	charsFile := flag.String("chars-file", "", "额外字符文件（UTF-8 文本，取其中每个字符）")
	flag.Parse()
	if *fontPath == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "需要 -font 与 -out")
		os.Exit(2)
	}
	payload, err := os.ReadFile(*fontPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	codes := map[rune]bool{}
	for _, item := range strings.Split(*ranges, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		low, high, found := strings.Cut(item, "-")
		start, err := strconv.ParseInt(strings.TrimSpace(low), 0, 32)
		if err != nil {
			fmt.Fprintln(os.Stderr, "区间无效:", item)
			os.Exit(2)
		}
		end := start
		if found {
			value, err := strconv.ParseInt(strings.TrimSpace(high), 0, 32)
			if err != nil {
				fmt.Fprintln(os.Stderr, "区间无效:", item)
				os.Exit(2)
			}
			end = value
		}
		for code := start; code <= end; code++ {
			codes[rune(code)] = true
		}
	}
	if *charsFile != "" {
		body, err := os.ReadFile(*charsFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, code := range string(body) {
			if code > 0x20 {
				codes[code] = true
			}
		}
	}
	list := make([]rune, 0, len(codes))
	for code := range codes {
		list = append(list, code)
	}
	sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	table, err := chaoxing.BuildSecretFeatureTable(payload, list)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	writer, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer writer.Close()
	buffered := bufio.NewWriter(writer)
	if _, err := buffered.Write(table); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := buffered.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("写入 %d 个码点特征，%d 字节\n", len(list), len(table))
}
