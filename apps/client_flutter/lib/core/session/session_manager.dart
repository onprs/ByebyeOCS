import '../config/app_config.dart';
import '../platform/token_store.dart';

/// 会话只保存短期访问令牌、轮换刷新令牌与内存中的 CSRF 值。
class SessionManager {
  SessionManager({required this.channel, required TokenStore store})
      : _store = store;

  final ClientChannel channel;
  final TokenStore _store;

  StoredTokens? _tokens;
  bool _loaded = false;
  bool _expiredNotified = false;
  String? csrfToken;
  String? challengeBinding;
  void Function()? onExpired;

  bool get isWeb => channel == ClientChannel.web;

  String? get accessToken => _tokens?.accessToken;

  String? get refreshToken => _tokens?.refreshToken;

  Future<void> ensureLoaded() async {
    if (_loaded || isWeb) {
      _loaded = true;
      return;
    }
    _tokens = await _store.read();
    _loaded = true;
  }

  Future<void> acceptLogin({
    String? accessToken,
    String? refreshToken,
    DateTime? accessExpiresAt,
    String? csrf,
  }) async {
    if (csrf != null && csrf.isNotEmpty) {
      csrfToken = csrf;
    }
    if (!isWeb && accessToken != null && refreshToken != null) {
      final tokens = StoredTokens(
        accessToken: accessToken,
        refreshToken: refreshToken,
        accessExpiresAt: accessExpiresAt,
      );
      _tokens = tokens;
      await _store.write(tokens);
    }
    _expiredNotified = false;
  }

  Future<void> acceptCsrf(String? token) async {
    if (token != null && token.isNotEmpty) {
      csrfToken = token;
    }
  }

  Future<void> acceptRotatedTokens({
    required String accessToken,
    required String refreshToken,
    DateTime? accessExpiresAt,
  }) async {
    if (isWeb) return;
    final tokens = StoredTokens(
      accessToken: accessToken,
      refreshToken: refreshToken,
      accessExpiresAt: accessExpiresAt,
    );
    _tokens = tokens;
    await _store.write(tokens);
  }

  void setChallengeBinding(String? value) {
    challengeBinding = value == null || value.isEmpty ? null : value;
  }

  Future<void> clear() async {
    _tokens = null;
    csrfToken = null;
    challengeBinding = null;
    await _store.clear();
  }

  void notifyExpired() {
    if (_expiredNotified) return;
    _expiredNotified = true;
    onExpired?.call();
  }

  void resetExpiredNotification() {
    _expiredNotified = false;
  }
}
