// 维护工具：只读核查已保存的加密页面，不访问平台，不触发作答。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/adapters/chaoxing"
)

type pageResult struct {
	File string `json:"file"`
	chaoxing.SecretFontDecodeAudit
	Error string `json:"error,omitempty"`
}

type auditReport struct {
	Pages        int   `json:"pages"`
	Passed       int   `json:"passed"`
	Failed       int   `json:"failed"`
	Milliseconds int64 `json:"milliseconds"`
	UseLegacy    bool  `json:"use_legacy"`
	chaoxing.SecretFontDecodeAudit
	Results []pageResult `json:"results"`
}

func main() {
	dir := flag.String("pages", "", "已保存的 HTML 页面目录")
	out := flag.String("report", "", "可选 JSON 报告文件，不包含题目文字")
	legacy := flag.Bool("legacy", true, "启用已核对签名对视觉别名的兼容")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "需要 -pages")
		os.Exit(2)
	}
	files, err := filepath.Glob(filepath.Join(*dir, "*.html"))
	if err != nil || len(files) == 0 {
		fmt.Fprintln(os.Stderr, "未找到 HTML 页面")
		os.Exit(2)
	}
	report := auditReport{Pages: len(files), UseLegacy: *legacy}
	start := time.Now()
	for _, file := range files {
		payload, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(os.Stderr, "读取页面失败")
			os.Exit(1)
		}
		summary, err := chaoxing.AuditSecretFontDecoding(payload, *legacy)
		item := pageResult{File: filepath.Base(file), SecretFontDecodeAudit: summary}
		if err != nil {
			item.Error = err.Error()
			report.Failed++
		} else {
			report.Passed++
		}
		report.Resolved += summary.Resolved
		report.Reference += summary.Reference
		report.Legacy += summary.Legacy
		report.Empty += summary.Empty
		report.CorrectedLegacy += summary.CorrectedLegacy
		report.Results = append(report.Results, item)
	}
	report.Milliseconds = time.Since(start).Milliseconds()
	if *out != "" {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err = os.WriteFile(*out, append(encoded, '\n'), 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("页面=%d 通过=%d 失败=%d 原版轮廓=%d 兼容签名=%d 空白=%d 旧标签差异=%d 耗时=%dms\n", report.Pages, report.Passed, report.Failed, report.Reference, report.Legacy, report.Empty, report.CorrectedLegacy, report.Milliseconds)
	if report.Failed > 0 {
		os.Exit(1)
	}
}
