# 测验题面字形几何还原与参考库维护

> **适用范围**：开发维护环境。正式 Go 服务已通过 `embed` 内置编译好的原版字体几何轮廓参考库（`secretfont_reference.bin.gz`），**公开生产运行环境无需安装任何字体文件、Python、浏览器或 OCR 组件**。

## 1. 动态字体混淆原理与几何匹配方案

学习通在未作答测验页面中会将部分题干与选项汉字替换为每次随机分配的私有区/生僻码点（包裹于 `.font-cxsecret` 容器内），并随页面下发一份子集字体，使浏览器在屏幕上绘制出原字形状。由于子集字体的 `cmap` 仅映射“随机密文码点 → 字形轮廓”，无法直接反查原文 Unicode，适配器采用 **原版字体全量几何轮廓目录匹配（SFR2）** 方案精确还原文字：

1. **多源样式与多格式字体提取**：支持解析页面内联 `@font-face` 及平台可信 HTTPS 外部样式表（支持 `@import` 相对解析与循环导入去重，单页最多读取 `8` 份外部样式、尝试 `12` 个字体声明；无 Cookie 静态客户端拉取）。全面支持 **TrueType（TTF）、CFF OpenType（OTF）与 WOFF1** 格式。
2. **按需解析与空轮廓识别**：仅对页面 `.font-cxsecret` 容器（文本节点、`aria-label`、`title`，自动跳过 `<script>`/`<style>`）实际引用到的码点解析字形轮廓；未被引用的异常字形不阻断任务，被引用的损坏轮廓立即报错中止，确为空轮廓的码点还原为空格。
3. **归一化自适应采样与双向边界比对**：
   - 将字形轮廓统一缩放至 `1000 units/em` 坐标系，以最大 `0.2` 单位的弦高误差将二次/三次贝塞尔曲线自适应展开为折线点集，完整保留笔画位置、比例与基线。
   - 先按轮廓闭合环数量与外包围盒（Bounding Box）快速分桶检索候选字，再对每条闭合轮廓执行不依赖轮廓排列顺序、起点位置、绕行方向及三/二次曲线转换差异的一对一双向边界距离核验（容差阈值为 `6 / 1000 em`，用于吸收平台子集化转换舍入误差）。
4. **Unicode 17.0 部首等价归并与零猜测原则**：
   - 利用 Unicode 17.0 `Equivalent_Unified_Ideograph` 标准属性（348 条映射）及原版字体明确共享轮廓的部首/汉字注记，自动消解康熙部首与常用统一汉字同形异码造成的伪歧义；不同标点、大小写及全半角字符不做模糊合并。
   - 若多个语义不同的汉字同时落在 `6 / 1000 em` 误差范围内，仅在命中人工核对的历史精确签名兼容表（`secretfont_curated.txt` / `secretfont_index.txt`）时消歧；**凡仍存未解歧义或缺少匹配参考时，立即停止本次测验任务并转入人工处理，严禁按最近邻猜测题面**。

## 2. 内置参考库资产与运行缓存

- **核心参考包**：`server/internal/adapters/chaoxing/secretfont_reference.bin.gz`（SFR2 格式，压缩体积约 `14.1 MiB`，覆盖 `44,264` 条有效非空轮廓目录条目）。
- **基准上游字体**：Adobe Source Han Sans **1.000**（固定提交 `f25136e6c310c99be8354d447b7e17404075415f`，源文件 `Normal/OTC/SourceHanSansSC-Normal.otf`，`SHA-256 = 36eb5329cfd4850e8b514b6a2184da169f319fc349b60f23462ab23a5b622d14`）。
- **运行期内存控制**：参考索引在进程内单次延迟加载，跨页面共享最多 `4,096` 条以 `(unitsPerEm, 轮廓几何摘要)` 为键的解码结果 LRU 缓存，并通过 `singleflight` 合并并发相同轮廓计算。
- **开源许可归档**：Source Han Sans（Apache-2.0）与 Unicode 17.0（Unicode-3.0）许可声明随根目录 [NOTICE](../../NOTICE) 与 `licenses/` 一并打包进所有发布归档。

## 3. 可复现重建参考库（维护环境）

如需在开发维护环境从上游固定版本字体重新生成并校验 `secretfont_reference.bin.gz`（构建确定性输出，相同输入字节级一致）：

```bash
mkdir -p .local/font-build
curl --fail --location \
  https://raw.githubusercontent.com/adobe-fonts/source-han-sans/f25136e6c310c99be8354d447b7e17404075415f/Normal/OTC/SourceHanSansSC-Normal.otf \
  --output .local/font-build/SourceHanSansSC-Normal.otf

go -C server run ./tools/secretfont-reference \
  -font ../.local/font-build/SourceHanSansSC-Normal.otf \
  -sha256 36eb5329cfd4850e8b514b6a2184da169f319fc349b60f23462ab23a5b622d14 \
  -out internal/adapters/chaoxing/secretfont_reference.bin.gz

go -C server test -race ./internal/adapters/chaoxing/...
```

辅助页面字形采样与历史兼容签名维护脚本见 [scripts/secretfont/README.md](../../scripts/secretfont/README.md)。
