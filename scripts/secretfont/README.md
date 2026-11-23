# 测验题面字形维护辅助工具（开发维护环境）

> **适用范围**：开发维护环境。公开部署与生产运行**不需要**使用本目录脚本；正式 Go 服务已内置原版字体几何轮廓参考库（`secretfont_reference.bin.gz`），可在无外部字体、无 Python、无浏览器的环境下直接还原学习通动态混淆题面。

## 工具用途

学习通未作答测验页面会下发随机码点子集字体（`font-cxsecret`）。正式适配器默认采用 Source Han Sans CN Normal 1.000 的完整 Unicode 几何轮廓目录进行精确边界匹配，仅在极少数历史视觉别名场景下参考已人工核对的精确签名索引（`secretfont_curated.txt` / `secretfont_index.txt`）。

本目录下的 Python 脚本仅供维护者在分析新样本字体、渲染比对字形或维护历史签名兼容表时使用。

## 维护环境依赖

- Go `1.26+`
- Python `3.12+`，并在隔离虚拟环境中安装 `pillow` 与 `fonttools`
- 对照渲染时需本机具备思源黑体 / Noto Sans CJK SC 字库文件

## 常用维护命令

在仓库根目录准备隔离 Python 环境并对样本 HTML 页面执行离线字形打标与索引生成（所有抓取样本与中间产物均放在被 `.gitignore` 忽略的 `.local/` 下）：

```bash
# 1. 创建隔离虚拟环境并安装依赖
python3 -m venv .local/probe/venv
.local/probe/venv/bin/pip install pillow fonttools

# 2. 可选：从本地维护环境配置抓取未作答测验样本页（需仓库根私密 .env 授权）
.local/probe/venv/bin/python scripts/secretfont/fetch_pages.py

# 3. 并行提取样本页内嵌字体并生成候选标签清单
VIRTUAL_ENV=.local/probe/venv .local/probe/venv/bin/python \
  scripts/secretfont/label_pages_parallel.py 6 .local/probe/pages/*.html

# 4. 结合人工核对表生成历史兼容签名索引
go -C server run ./tools/secretfont-index \
  -manifest .local/probe/secretfont-labels-v3.json \
  -curated internal/adapters/chaoxing/secretfont_curated.txt \
  -out internal/adapters/chaoxing/secretfont_index.txt
```

原版字体几何轮廓参考库（`secretfont_reference.bin.gz`）的算法原理、SHA-256 校验值与可复现重建命令详见 [题面字形几何还原与索引维护](../../docs/development/secretfont-index.md)。
