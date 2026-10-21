/// 会话令牌只保存在系统安全存储中；不写入普通偏好或日志。
class StoredTokens {
  const StoredTokens({
    required this.accessToken,
    required this.refreshToken,
    this.accessExpiresAt,
  });

  final String accessToken;
  final String refreshToken;
  final DateTime? accessExpiresAt;

  bool get hasRefreshToken => refreshToken.isNotEmpty;
}

abstract interface class TokenStore {
  Future<StoredTokens?> read();
  Future<void> write(StoredTokens tokens);
  Future<void> clear();
}

/// 测试与本地流程使用；生产 Web 不使用任何令牌存储。
class InMemoryTokenStore implements TokenStore {
  StoredTokens? _tokens;

  @override
  Future<StoredTokens?> read() async => _tokens;

  @override
  Future<void> write(StoredTokens tokens) async {
    _tokens = tokens;
  }

  @override
  Future<void> clear() async {
    _tokens = null;
  }
}
