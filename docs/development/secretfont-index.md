# 字形索引维护

学习通在未作答的章节测验页面上把题干与选项文字替换成随机码点，并在页面 `cxSecretStyle` 样式块里内嵌一份字体。码点每页随机，字形本身来自固定字体，所以“码点 → 真实字符”的映射每页都不同，而“字形 → 真实字符”的映射是稳定的。

`server/internal/adapters/chaoxing/secretfont.go` 只做字形签名查表：

1. 从页面取出内联 base64 字体，解析 `cmap`、`loca`、`glyf`，复合字形递归展开。
2. 把每个字形还原成绝对坐标轮廓点，按闭合轮廓、端点、坐标生成截断 SHA-256 签名。
3. 用签名查 `secretfont_index.txt`；命中即替换文本节点与 `aria-label`/`title` 属性。

实际使用的字形中只要有一个没有收录，整个页面解码失败并按“字体加密，无法可信解码”转人工。这里不做模糊匹配，避免生成可疑题干后提交错误答案。

## 索引文件

- `secretfont_index.txt`：自动生成，每行 `签名<TAB>字符`。
- `secretfont_curated.txt`：人工确认条目，优先于自动投票，用于语境唯一但字形高度相近的字。

两者都由 `server/tools/secretfont-index` 合并写出，不要在运行时修改。

## 打标与生成

`scripts/secretfont/label_pages.py` 对每页每个码点做两级匹配：

1. **栅格粗筛**：把页面字形与参考字形都画进固定 em 框（基线固定），缩放到 32×32，按灰度差排序取前 40。
2. **轮廓精排**：页面字形轮廓与候选轮廓按采样点做对称倒角距离（chamfer），得到分数与最佳/次佳差距。

参考字形来自本机思源黑体同族字体（`NotoSansCJK-DemiLight.ttc` 字体序号 2，即 Source Han Sans Normal 等效字重）。签名与投票由 Go 工具完成：同一签名在不同页面必须给出相同标签，冲突条目丢弃。

```bash
# 1) 抓取未作答测验页面并打标（维护环境，凭据只从仓库根 .env 读取）
python3 scripts/secretfont/label_pages_parallel.py 6 .local/probe/pages/*.html

# 2) 生成索引（可带人工确认条目）
go -C server run ./tools/secretfont-index \
  -manifest .local/probe/secretfont-labels-v3.json \
  -curated internal/adapters/chaoxing/secretfont_curated.txt \
  -out internal/adapters/chaoxing/secretfont_index.txt
```

只有未作答页面才加密；已提交页面是明文。因此补索引要针对尚未作答的章节测验，抓取脚本遇到明文页面会跳过。

## 形状兜底（全量特征表）

精确签名未收录时，解码器按形状兜底：把页面字形渲染到固定坐标系（480×480、字号 360、基线 y=384、按字形宽度居中），盒式平均降采样成 16×16 粗特征与 24×24 灰度精特征，先粗筛前 40 个候选，再按精特征最近邻排序，用 `1 - 最优/次优` 作置信度。

- 特征表 `secretfont_features.bin.gz`（约 0.35MB gzip，7016 个码点）由 `server/tools/secretfont-features` 离线生成。
- 参考字体使用 Source Han Sans CN Normal 的等效实例：Noto Sans SC 可变字体实例化到 `wght=350`。实测该字重与平台内嵌字体最接近（300/350/400 三档中 350 的标签一致率最高）。
- 兜底阈值 `secretFeatureMargin = 0.7`：在 155 个页面上按精确索引真值评估，990 个字形中保留 897 个正确结果、0 个错判；阈值 0.6 会多保留 54 个正确但放进 1 个错判（士→土）。宁可转人工也不放错字。
- 没有特征表或置信度不足时仍然失败，不猜测。

生成命令：

```bash
# 用可变字体实例化出静态 TTF（需要 fonttools）
python3 - <<'PY'
from fontTools.ttLib import TTFont
from fontTools.varLib.instancer import instantiateVariableFont
vf = TTFont("NotoSansSC[wght].ttf", lazy=False)
instantiateVariableFont(vf, {"wght": 350}, inplace=False, updateFontNames=False).save("noto-sc-w350.ttf")
PY
go -C server run ./tools/secretfont-features -font noto-sc-w350.ttf \
  -out internal/adapters/chaoxing/secretfont_features.bin -chars-file gb2312.txt
gzip -9 -c internal/adapters/chaoxing/secretfont_features.bin > internal/adapters/chaoxing/secretfont_features.bin.gz
```

## 索引审计

精确索引来自打标投票，仍可能有个别错标。维护时用兜底匹配做交叉审计：把索引标签当真值，全量跑一遍兜底匹配，人工核对不一致条目的原文语境。

本轮审计发现并修正 1 处：`嵑` 出现在“先变小再变大 / 先变大再变小”选项里，索引误标成“太”，已写入 `secretfont_curated.txt` 改为“大”。

## 覆盖范围与限制

索引只收录已经抓取并打标过的字形。补字形时优先处理解码失败页面的缺失签名，人工确认后写入 `secretfont_curated.txt`，不需要修改运行时代码。

维护环境实测：155 个加密页面、997 条签名，全部页面可完整解码（缺失签名 0）。新增课程或新题型出现未收录字形时，按上面的流程补抓、补标、重建索引即可。
