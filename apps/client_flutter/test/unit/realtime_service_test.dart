import 'dart:async';

import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/core/realtime/realtime_service.dart';
import 'package:byebyeocs_client/core/realtime/sse_connection.dart';
import 'package:byebyeocs_client/core/session/session_manager.dart';

class _FakeSseSubscription implements StreamSubscription<SseMessage> {
  bool paused = false;
  bool cancelled = false;

  @override
  Future<void> cancel() async {
    cancelled = true;
  }

  @override
  void onData(void Function(SseMessage data)? handleData) {}

  @override
  void onError(Function? handleError) {}

  @override
  void onDone(void Function()? handleDone) {}

  @override
  void pause([Future<void>? resumeSignal]) {
    paused = true;
  }

  @override
  void resume() {
    paused = false;
  }

  @override
  bool get isPaused => paused;

  @override
  Future<E> asFuture<E>([E? futureValue]) {
    return Completer<E>().future;
  }
}

/// 测试替身：允许同一连接重复上报 onError 与 onDone，验证服务端去重逻辑。
class _FakeSseStream extends Stream<SseMessage> {
  Function? _onError;
  void Function()? _onDone;

  @override
  StreamSubscription<SseMessage> listen(
    void Function(SseMessage event)? onData, {
    Function? onError,
    void Function()? onDone,
    bool? cancelOnError,
  }) {
    _onError = onError;
    _onDone = onDone;
    return _FakeSseSubscription();
  }

  void failBoth() {
    _onError?.call(StateError('事件流连接中断'));
    _onDone?.call();
  }
}

class _FakeSseConnection implements SseConnection {
  final _FakeSseStream _stream = _FakeSseStream();
  bool closed = false;

  @override
  Stream<SseMessage> get messages => _stream;

  void failBoth() => _stream.failBoth();

  @override
  Future<void> close() async {
    closed = true;
  }
}

void main() {
  test('同一 SSE 连接同时上报错误与结束只计一次失败', () async {
    final connections = <_FakeSseConnection>[];
    final service = RealtimeService(
      config: AppConfig(
        apiBaseUri: Uri.parse('https://api.test/api/v1/'),
        channel: ClientChannel.app,
      ),
      session: SessionManager(
        channel: ClientChannel.app,
        store: InMemoryTokenStore(),
      ),
      connector: (uri, {bearerToken, lastEventId}) {
        final connection = _FakeSseConnection();
        connections.add(connection);
        return connection;
      },
    );
    addTearDown(service.dispose);

    service.start();
    expect(connections, hasLength(1));
    connections.single.failBoth();
    await Future<void>.delayed(Duration.zero);

    expect(service.failureCount, 1);
    expect(service.usingPolling, isFalse);
    expect(connections.single.closed, isTrue);
  });

  test('暂停重连后旧连接迟到回调不得影响当前连接', () async {
    final connections = <_FakeSseConnection>[];
    final service = RealtimeService(
      config: AppConfig(
        apiBaseUri: Uri.parse('https://api.test/api/v1/'),
        channel: ClientChannel.app,
      ),
      session: SessionManager(
        channel: ClientChannel.app,
        store: InMemoryTokenStore(),
      ),
      connector: (uri, {bearerToken, lastEventId}) {
        final connection = _FakeSseConnection();
        connections.add(connection);
        return connection;
      },
    );
    addTearDown(service.dispose);

    service.start();
    service.pause();
    service.resume();
    expect(connections, hasLength(2));

    connections[0].failBoth();
    await Future<void>.delayed(Duration.zero);
    expect(service.failureCount, 0);

    connections[1].failBoth();
    await Future<void>.delayed(Duration.zero);
    expect(service.failureCount, 1);
    expect(service.usingPolling, isFalse);
  });
}
