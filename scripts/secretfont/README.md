# 字形标签维护工具

给未作答测验页面的内嵌字体打标，输出供 `server/tools/secretfont-index` 生成字形索引。

需要 Python 3.12、Pillow 与 fontTools，以及本机思源黑体/Noto Sans CJK SC DemiLight（`/usr/share/fonts/opentype/noto/NotoSansCJK-DemiLight.ttc`，字体序号 2）。

```bash
# 抓取未作答测验页面（需要本地维护库与仓库根 .env 凭据）
python3 scripts/secretfont/fetch_pages.py

```bash
python3 -m venv .local/probe/venv
.local/probe/venv/bin/pip install pillow fonttools
VIRTUAL_ENV=.local/probe/venv .local/probe/venv/bin/python scripts/secretfont/label_pages_parallel.py 6 .local/probe/pages/*.html
go -C server run ./tools/secretfont-index -manifest .local/probe/secretfont-labels-v3.json \
  -curated internal/adapters/chaoxing/secretfont_curated.txt \
  -out internal/adapters/chaoxing/secretfont_index.txt
```

细节见 `docs/development/secretfont-index.md`。
