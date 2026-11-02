// 维护工具：根据页面字形标签生成字形签名索引。
//
// 输入是标签清单（页面字体与每个随机码点的候选字符），输出 server/internal/adapters/chaoxing
// 使用的索引文件。同一签名投票不一致的条目会被丢弃，避免把猜测写进运行时数据。
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/adapters/chaoxing"
)

type labelEntry struct {
	Label  string  `json:"label"`
	Score  float64 `json:"score"`
	Margin float64 `json:"margin"`
}

type pageInput struct {
	HTML   string                `json:"html"`
	Labels map[string]labelEntry `json:"labels"`
}

func main() {
	manifest := flag.String("manifest", "", "标签清单 JSON")
	out := flag.String("out", "", "输出索引文件")
	curated := flag.String("curated", "", "人工确认的签名标签文件（每行 签名<TAB>字符）")
	minMargin := flag.Float64("min-margin", 3000, "单页标签的最小置信差距")
	maxScore := flag.Float64("max-score", 8000, "低差距标签允许的最大绝对差距")
	lowMargin := flag.Float64("low-margin", 800, "低于最小差距时仍可接受的下限")
	flag.Parse()
	if *manifest == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "需要 -manifest 与 -out")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*manifest)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var pages []pageInput
	if err := json.Unmarshal(raw, &pages); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	votes := map[string]map[rune]int{}
	accepted := map[string]int{}
	for _, page := range pages {
		payload, err := os.ReadFile(page.HTML)
		if err != nil {
			continue
		}
		entries, err := chaoxing.SecretFontSignatures(payload)
		if err != nil {
			fmt.Fprintln(os.Stderr, "跳过", page.HTML, err)
			continue
		}
		for code, signature := range entries {
			entry, found := page.Labels[string(code)]
			if !found || len([]rune(entry.Label)) != 1 {
				continue
			}
			// 形状几乎完全重合的候选可放宽差距要求；其余必须达到最小差距。
			if entry.Margin < *lowMargin || (entry.Margin < *minMargin && entry.Score > *maxScore) {
				continue
			}
			if votes[signature] == nil {
				votes[signature] = map[rune]int{}
			}
			votes[signature][[]rune(entry.Label)[0]]++
			accepted[signature]++
		}
	}
	type pair struct {
		signature string
		glyph     rune
		count     int
		total     int
	}
	items := make([]pair, 0, len(votes))
	conflicts := 0
	for signature, counted := range votes {
		var best rune
		bestCount, second := 0, 0
		for glyph, count := range counted {
			if count > bestCount {
				best, bestCount = glyph, count
			}
		}
		for _, count := range counted {
			if count != bestCount && count > second {
				second = count
			}
		}
		if bestCount == second && len(counted) > 1 {
			conflicts++
			continue
		}
		items = append(items, pair{signature: signature, glyph: best, count: bestCount, total: accepted[signature]})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].signature < items[j].signature })
	file, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	fmt.Fprintln(writer, "# 字形签名索引，由 server/tools/secretfont-index 生成；每行 “签名<TAB>字符”。")
	unique := map[string]bool{}
	if *curated != "" {
		if err := appendCurated(writer, *curated, unique); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	for _, item := range items {
		if unique[item.signature] {
			continue
		}
		unique[item.signature] = true
		fmt.Fprintf(writer, "%s\t%c\n", item.signature, item.glyph)
	}
	writer.Flush()
	fmt.Printf("写入 %d 条签名（冲突丢弃 %d 条）\n", len(unique), conflicts)
}

// appendCurated 写入人工确认的标签；人工条目优先于自动投票结果。
func appendCurated(writer *bufio.Writer, path string, written map[string]bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		signature, glyph, found := strings.Cut(line, "\t")
		runes := []rune(strings.TrimSpace(glyph))
		if !found || len(signature) != 32 || len(runes) != 1 || written[signature] {
			continue
		}
		written[signature] = true
		fmt.Fprintf(writer, "%s\t%c\n", signature, runes[0])
	}
	return scanner.Err()
}
