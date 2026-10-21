# Flutter 客户端下载与分发

状态：用户已选择自有下载站，地址暂缓。用户端正式技术栈为 Flutter Web + Flutter Android/iOS；当前 React 手机原型只作为交互基准。

## 1. 发布组织

首期先交付 Flutter Web 与已验证原生任务能力，Android/iOS App 分期。下载地址使用待配置的 HTTPS 站点，不硬编码维护者主机、内网地址或临时域名。

下载站是安装包与更新说明的托管入口，不承担用户认证、学习执行或钱包入账。客户端下载地址由版本/能力 API 返回；未配置时隐藏对应下载/更新入口，不展示虚假链接。

## 2. Android

发布正常签名的 release APK，由下载站提供。用户通过系统允许的“安装未知应用”交互完成安装，不尝试静默安装或绕过系统保护。

版本清单包括：平台、应用 ID、版本号、最小服务端/API 版本、文件大小、SHA-256、HTTPS 下载地址和受信签名。更新必须保留签名连续性；签名密钥受限保存，不能进仓库或后台普通日志。

应用校验更新来源与摘要，展示新版本说明和所需操作后交由系统安装。若将来变更为其他 Android 分发渠道，再核对相应开发者验证、地区政策和支付规则，不把自托管视为所有渠道通用豁免。

官方依据：[Android Alternative Distribution](https://developer.android.com/distribute/marketing-tools/alternative-distribution)。

## 3. iOS / iPadOS

自有下载站可以托管下载说明、已获准的安装资产或正式分发入口，但不能仅上传 IPA 就承诺普通用户均可安装。

本轮读取的 Apple 官方 Web Distribution 文档，明确针对符合条件的开发者和欧盟用户，要求授权、Notarization、注册域名及适用系统版本；企业开发计划则针对组织内部员工私有分发，不能直接当作面向普通学生的公开分发方案。

因此先保留 Flutter Web 作为 iOS 用户可访问的入口，原生 App 仍为后续目标。有效的签名、用户地区和分发条件未确认前，不展示“iOS 直接下载安装”承诺，也不使用无效企业签名替代。

已读来源：

- [Apple Web Distribution](https://developer.apple.com/support/web-distribution-eu/)
- [Apple Developer Enterprise Program](https://developer.apple.com/programs/enterprise/)

实际账号是否符合某项计划需在分发阶段核查；本文不宣称已有可用 Apple 分发资质。

## 4. 支付能力

已确认的 ZPay 供应商为 zpayz.cn。Web 与自托管 Android 的支付方案按其商户类别、接口和实际渠道能力联调。iOS 是否可以使用同一外部支付入口，仍按有效分发方式、用户地区和平台政策核对。

支付跳转和 App 回跳只定位本地订单，不直接增加余额；重新进入前台查询服务端支付与到账状态。钱包正常到账余额原则上不自助退回；异常资金和预占释放独立处理。

## 5. 尚未提供的部署信息

自有下载站域名/路径、发布签名配置、异机静态资产存储和 iOS 实际可用分发条件。地址暂缓不阻塞 Flutter 工程、API 或服务端基础设计，不默认填入虚假生产 URL。
