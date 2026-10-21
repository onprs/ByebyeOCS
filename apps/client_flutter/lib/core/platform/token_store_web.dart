import 'token_store_base.dart';

/// Web 使用 HttpOnly Cookie，JS 无法读取会话令牌，也不写入 localStorage。
class CookieTokenStore implements TokenStore {
  const CookieTokenStore();

  @override
  Future<StoredTokens?> read() async => null;

  @override
  Future<void> write(StoredTokens tokens) async {
    // 服务端通过 Set-Cookie 下发会话；前端不接触令牌。
  }

  @override
  Future<void> clear() async {
    // 服务端负责清除 Cookie。
  }
}

TokenStore createTokenStore() => const CookieTokenStore();
