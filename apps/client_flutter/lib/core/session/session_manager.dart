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

  /// 读取代次：清除或写入新令牌后，早先发起但未完成的读取结果必须丢弃。
  int _storeEpoch = 0;
  Future<void>? _pendingLoad;

  bool get isWeb => channel == ClientChannel.web;

  String? get accessToken => _tokens?.accessToken;

  String? get refreshToken => _tokens?.refreshToken;

  Future<void> ensureLoaded() async {
    if (_loaded || isWeb) {
      _loaded = true;
      return;
    }
    final pending = _pendingLoad;
    if (pending != null) return pending;
    final load = _loadFromStore();
    _pendingLoad = load;
    await load;
  }

  Future<void> _loadFromStore() async {
    final epoch = _storeEpoch;
    try {
      final tokens = await _store.read();
      // 读取期间发生退出、切换服务或新登录时，以内存中的新事实为准。
      if (epoch != _storeEpoch) return;
      _tokens = tokens;
      _loaded = true;
    } finally {
      _pendingLoad = null;
    }
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
      _storeEpoch++;
      final tokens = StoredTokens(
        accessToken: accessToken,
        refreshToken: refreshToken,
        accessExpiresAt: accessExpiresAt,
      );
      _tokens = tokens;
      _loaded = true;
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
    _storeEpoch++;
    final tokens = StoredTokens(
      accessToken: accessToken,
      refreshToken: refreshToken,
      accessExpiresAt: accessExpiresAt,
    );
    _tokens = tokens;
    _loaded = true;
    await _store.write(tokens);
  }

  void setChallengeBinding(String? value) {
    challengeBinding = value == null || value.isEmpty ? null : value;
  }

  Future<void> clear() async {
    _storeEpoch++;
    _tokens = null;
    _loaded = true;
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
