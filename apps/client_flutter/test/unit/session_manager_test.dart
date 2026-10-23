import 'dart:async';

import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/core/session/session_manager.dart';

class _DelayedReadStore extends InMemoryTokenStore {
  _DelayedReadStore(this._value);

  final StoredTokens _value;
  final Completer<StoredTokens?> _read = Completer<StoredTokens?>();

  void completeRead() => _read.complete(_value);

  @override
  Future<StoredTokens?> read() => _read.future;
}

void main() {
  test('本机令牌读取未完成时退出，不被迟到的读取结果重新写回', () async {
    final store = _DelayedReadStore(
      const StoredTokens(
        accessToken: 'old-access',
        refreshToken: 'old-refresh',
      ),
    );
    final session = SessionManager(
      channel: ClientChannel.app,
      store: store,
    );

    final loading = session.ensureLoaded();
    await session.clear();
    store.completeRead();
    await loading;

    expect(session.accessToken, isNull);
    expect(session.refreshToken, isNull);
  });

  test('登录写入新令牌后，早先的读取结果不得覆盖本次登录', () async {
    final store = _DelayedReadStore(
      const StoredTokens(
        accessToken: 'stale-access',
        refreshToken: 'stale-refresh',
      ),
    );
    final session = SessionManager(
      channel: ClientChannel.app,
      store: store,
    );

    final loading = session.ensureLoaded();
    await session.acceptLogin(
      accessToken: 'new-access',
      refreshToken: 'new-refresh',
    );
    store.completeRead();
    await loading;

    expect(session.accessToken, 'new-access');
    expect(session.refreshToken, 'new-refresh');
  });
}
