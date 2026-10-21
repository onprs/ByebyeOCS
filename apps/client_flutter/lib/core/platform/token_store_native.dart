import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import 'token_store_base.dart';

/// Android/iOS 使用系统安全存储保存短期访问令牌与轮换刷新令牌。
class SecureTokenStore implements TokenStore {
  const SecureTokenStore();

  static const _storage = FlutterSecureStorage();
  static const _accessKey = 'bbo.session.access';
  static const _refreshKey = 'bbo.session.refresh';
  static const _expiresKey = 'bbo.session.access_expires';

  @override
  Future<StoredTokens?> read() async {
    final access = await _storage.read(key: _accessKey);
    final refresh = await _storage.read(key: _refreshKey);
    if (access == null ||
        access.isEmpty ||
        refresh == null ||
        refresh.isEmpty) {
      return null;
    }
    final expiresRaw = await _storage.read(key: _expiresKey);
    return StoredTokens(
      accessToken: access,
      refreshToken: refresh,
      accessExpiresAt:
          expiresRaw == null ? null : DateTime.tryParse(expiresRaw),
    );
  }

  @override
  Future<void> write(StoredTokens tokens) async {
    await _storage.write(key: _accessKey, value: tokens.accessToken);
    await _storage.write(key: _refreshKey, value: tokens.refreshToken);
    final expires = tokens.accessExpiresAt;
    if (expires == null) {
      await _storage.delete(key: _expiresKey);
    } else {
      await _storage.write(
        key: _expiresKey,
        value: expires.toUtc().toIso8601String(),
      );
    }
  }

  @override
  Future<void> clear() async {
    await _storage.delete(key: _accessKey);
    await _storage.delete(key: _refreshKey);
    await _storage.delete(key: _expiresKey);
  }
}

TokenStore createTokenStore() => const SecureTokenStore();
