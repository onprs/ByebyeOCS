enum ClientChannel { web, app }

/// 运行配置。Web 固定同源 `/api/v1`；App 由构建参数或用户连接服务提供地址。
class AppConfig {
  const AppConfig({required this.apiBaseUri, required this.channel});

  final Uri? apiBaseUri;
  final ClientChannel channel;

  bool get hasApiBase => apiBaseUri != null;

  Uri resolve(String path, [Map<String, String>? query]) {
    final base = apiBaseUri;
    if (base == null) {
      throw StateError('API 根地址未配置');
    }
    final normalized = path.startsWith('/') ? path.substring(1) : path;
    final resolved = base.resolve(normalized);
    if (query == null || query.isEmpty) return resolved;
    return resolved.replace(queryParameters: {
      ...resolved.queryParameters,
      ...query,
    });
  }
}
