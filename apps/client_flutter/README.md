# ByebyeOCS 用户客户端

共享 Flutter Web、Android 与 iOS 源码。用户使用学习通身份进入，管理课程同步、任务安排、实时进度、钱包与个人设置。

## 本地检查

```bash
flutter pub get
flutter analyze
flutter test
flutter build web --release --no-web-resources-cdn
```

Web 固定连接同源 `/api/v1`。Android/iOS 构建默认不内置服务地址，首次启动可在“连接服务”中填写服务方提供的 HTTPS 地址；也可以使用 `--dart-define=API_BASE_URL=https://example.com/api/v1/` 配置构建默认值。切换服务会先清除本机会话与用户缓存。

Android 调试构建：

```bash
flutter build apk --debug
```

正式签名、iOS 签名与分发配置不包含在本地调试构建中。
