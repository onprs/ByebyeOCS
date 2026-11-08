# 题面字形解码与维护

本文面向开发维护环境。正式服务的参考字体与目录随 Go 程序内置，公开运行环境无需安装字体、Python、浏览器或 OCR 服务。

学习通把题面文字替换为随机码点，再下发一份字体把这些码点画成原来的字。平台 `cmap` 只能说明“密文码点使用哪个字形”，无法直接给出原文 Unicode。当前保存的字体标识为 Source Han Sans CN Normal 1.000；是否可解码仍由实际几何轮廓决定，不单凭字体名称判断。

## 当前方案

默认路径使用原版字体的完整 Unicode 目录，自动建立几何参考，替代按课程抓页面、识字、补签名的常规维护方式。

1. 提取内联或平台允许的外部字体；支持 TrueType、CFF OpenType 与 WOFF1。内联样式和外部样式的 `@import` 按各自地址解析，循环导入去重，最多读取 8 份外部样式、尝试 12 个字体声明。一个声明包含多个 `src` 时继续尝试可解析格式。
2. 只在实际使用码点时解析轮廓。未使用的异常字形不阻断题面；实际使用的损坏字形仍返回失败，只有确实为空的轮廓才作为空格。
3. 将轮廓统一到 1000 单位/em，按最大 0.2 单位的曲线展开误差自适应取样；位置、大小、基线与笔画全部保留。
4. 用轮廓数量和包围范围分桶查候选，再逐轮廓一对一核对双向边界距离。轮廓顺序、起点、方向，以及三次曲线转换为二次曲线，不要求相同。允许的绝对边界误差为 6/1000 em，用于容纳平台字体转换误差；多个不同字符同时满足时保留歧义，不按最近的候选任选一个。
5. Unicode 17.0 的 `Equivalent_Unified_Ideograph` 属性、汉字兼容规范化和原字体明确共享的注记别名负责归并部首。不同符号、大小写、全半角不作一般性归并。
6. 原版轮廓结果优先。仍有歧义或缺少参考时，只使用既有已核对的精确签名兼容；正式路径不再采用旧 24×24 特征的相对置信差距猜字。
7. 有 `font-cxsecret` 容器时，只改写容器及后代的文本与 `aria-label`、`title`。脚本、样式和模板跳过，其他明文保持原样。外部字体候选必须实际产生码点还原，避免把无关界面字体误当成题面字体。

字体资源继续使用无 Cookie 的静态客户端和平台 HTTPS 地址限制。正常答题的自动取题、查询、填写、提交与独立平台核验流程保持原有语义；本方案不增加提交审批。

## 参考数据与来源

- `server/internal/adapters/chaoxing/secretfont_reference.bin.gz`：SFR2 数据，含原版字体与自动生成的 44,264 条轮廓目录条目；覆盖源字体完整 cmap，跳过空白并归并明确别名。请求路径按索引找候选，不遍历整份字符表逐字渲染。
- 原字体：Adobe Source Han Sans **1.000**，固定提交 `f25136e6c310c99be8354d447b7e17404075415f`，文件 `Normal/OTC/SourceHanSansSC-Normal.otf`。
- 字体 SHA-256：`36eb5329cfd4850e8b514b6a2184da169f319fc349b60f23462ab23a5b622d14`。
- `secretfont_unicode.go`：从 Unicode 17.0 `EquivalentUnifiedIdeograph.txt` 转换的 348 条标准等价映射。
- `secretfont_index.txt`、`secretfont_curated.txt`：保留历史已核对签名，作为少量视觉别名的兼容资料。
- `secretfont_features.bin.gz` 与旧特征生成工具：保留维护对照能力，正式解码不加载或采用其最近邻结果。

参考包为 14,104,053 字节，解压数据 18,571,120 字节；这些数字不包含 Go 对象、索引与请求开销。参考库只初始化一次，跨页面轮廓结果缓存最多 4,096 条，并发相同轮廓使用 singleflight 合并；缓存键包含 unitsPerEm 与轮廓签名，不以每页随机码点作为共享键。

来源与授权保存在根目录 `NOTICE` 和 `licenses/`，发布包同时收录。

## 重建参考库

以下为维护环境步骤，在仓库根目录执行。`font-build/` 是维护员自行创建的临时目录，不是公开运行前提。

```bash
mkdir -p font-build
curl --fail --location \
  https://raw.githubusercontent.com/adobe-fonts/source-han-sans/f25136e6c310c99be8354d447b7e17404075415f/Normal/OTC/SourceHanSansSC-Normal.otf \
  --output font-build/SourceHanSansSC-Normal.otf

go -C server run ./tools/secretfont-reference \
  -font ../font-build/SourceHanSansSC-Normal.otf \
  -sha256 36eb5329cfd4850e8b514b6a2184da169f319fc349b60f23462ab23a5b622d14 \
  -out internal/adapters/chaoxing/secretfont_reference.bin.gz
```

构建读取源字体自己的 Unicode cmap，不读取课程、凭据或人工字形标签。相同源文件必须生成相同二进制；回归测试检查可复现性、压缩校验及完整目录载入。更换源字体时必须重新核对字体版本、授权与真实样本，不根据“同族”或字重名称直接替换。

## 只读审计

`secretfont-audit` 只读取已保存的 HTML，不发送平台请求，不调用题库，不作答或提交；报告仅含文件名、计数和固定解码原因。

```bash
go -C server run ./tools/secretfont-audit \
  -pages ../font-build/pages \
  -report ../font-build/audit.json

# 禁用历史签名，单独检查原版轮廓覆盖
go -C server run ./tools/secretfont-audit \
  -pages ../font-build/pages -legacy=false
```

本地维护样本回归：155 个已保存加密页面全部解码通过；共 5,389 次页面内去重用字，5,380 次由原版轮廓还原，9 次由历史精确签名处理别名。该批扫描约 4.8 秒，属于本机只读结果，不代表生产吞吐或实际提交成功。

另对保存字体中的 1,078 种不同轮廓核对：1,075 种有唯一原版结果，3 种存在视觉别名；原版结果与 3 个历史标签不同，在页面审计中出现 11 次。当前优先采用原版几何结果，避免旧打标结果覆盖它。回归还通过重新分配码点的小型字体验证未收录的罕用字和扩展汉字，旧签名表置空后仍可解码。

回归覆盖曲线转换、轮廓起点/方向变化、非等价字符冲突、共享缓存并发与容量、未使用损坏字形、实际损坏字形、Unicode cmap、内联 WOFF、无关明文、嵌套/循环导入、格式回退和界面字体排除。常规检查：

```bash
go -C server vet ./...
go -C server test -race ./...
```

## 参考项目

- [OCS 5.0 字体解码模块](https://github.com/ocsjs/ocsjs/tree/91bf7611a281fdeb6937e70d9cc49759a805de1e/packages/scripts/src/utils/font-decrypt)：从原版字体生成位图特征，粗筛、精排与增量缓存。借鉴源字体参考和增量处理思路；本实现用几何边界的绝对误差与唯一性决定是否采用结果。
- [Samueli924/chaoxing 字体模块](https://github.com/Samueli924/chaoxing/blob/45d2106b16764231c2de2038fb042e71b23d878f/api/cxsecret_font.py)：提取字形坐标计算 MD5，查询静态映射并处理部首别名。固定点序哈希无法统一轮廓方向、起点和曲线转换，因此仅保留本项目历史精确签名作为兼容。
- [Adobe 原版字体](https://github.com/adobe-fonts/source-han-sans/tree/f25136e6c310c99be8354d447b7e17404075415f)与 [Unicode 等价属性](https://www.unicode.org/Public/17.0.0/ucd/EquivalentUnifiedIdeograph.txt)提供实际参考数据。

## 边界

此方案解决当前“随机码点与字体子集”机制，消除了常规新增课程对逐字补表的依赖。任意字体并不存在仅凭随机码点就能恢复原文的通用逆运算：源字体完全更换、轮廓实质改变、字体文件缺失，或不同 Unicode 字符显示为相同轮廓时，仍可能需要新的整份参考字体或明确兼容依据。

当前直接支持 TTF、CFF OpenType、WOFF1；只提供 WOFF2 或解析器不支持的 CFF2 时仍返回未支持，存在其他 `src` 则自动回退。没有可信字体且题面仍含私用码点、实际使用字形损坏或无法消除歧义时保持明确失败，不把乱码送给模型或继续提交。
