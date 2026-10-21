import 'dart:async';
import 'dart:convert';

import '../api/json.dart';
import '../config/app_config.dart';
import '../session/session_manager.dart';
import 'realtime_event.dart';
import 'sse_connection.dart';

/// 先由页面读取快照，再连接 SSE；断开时使用有界轮询降级并用资源版本去重。
class RealtimeService {
  RealtimeService({required AppConfig config, required SessionManager session})
      : _config = config,
        _session = session;

  final AppConfig _config;
  final SessionManager _session;
  final StreamController<RealtimeEvent> _controller =
      StreamController<RealtimeEvent>.broadcast();
  final Map<String, int> _versions = <String, int>{};

  SseConnection? _connection;
  StreamSubscription<SseMessage>? _subscription;
  Timer? _pollTimer;
  Timer? _retryTimer;
  Timer? _reconnectTimer;
  bool _running = false;
  bool _foreground = true;
  bool _polling = false;
  int _failures = 0;
  bool _disposed = false;
  String? _lastEventId;

  Stream<RealtimeEvent> get events => _controller.stream;

  bool get usingPolling => _polling;

  void start() {
    if (_disposed || _running) return;
    _running = true;
    _failures = 0;
    _emit(const RealtimeEvent(type: 'client.resume'));
    _connectSse();
  }

  void pause() {
    _foreground = false;
    _closeTransports();
  }

  void resume() {
    if (_disposed) return;
    _foreground = true;
    if (!_running) return;
    _emit(const RealtimeEvent(type: 'client.resume'));
    _connectSse();
  }

  void stop() {
    _running = false;
    _foreground = true;
    _versions.clear();
    _lastEventId = null;
    _closeTransports();
    _retryTimer?.cancel();
    _retryTimer = null;
  }

  void dispose() {
    _disposed = true;
    stop();
    _controller.close();
  }

  void _connectSse() {
    if (!_running || !_foreground || _disposed) return;
    if (!_config.hasApiBase) return;
    _closeSse();
    _pollTimer?.cancel();
    _pollTimer = null;
    _polling = false;
    _retryTimer?.cancel();
    _retryTimer = null;
    final base = _config.apiBaseUri;
    if (base == null) return;
    try {
      final connection = connectPlatformSse(
        _config.resolve('events'),
        bearerToken: _session.isWeb ? null : _session.accessToken,
        lastEventId: _lastEventId,
      );
      _connection = connection;
      _subscription = connection.messages.listen(
        _handleMessage,
        onError: (Object _) => _handleFailure(),
        onDone: _handleFailure,
      );
    } catch (_) {
      _handleFailure();
    }
  }

  void _handleMessage(SseMessage message) {
    _failures = 0;
    if (message.id != null && message.id!.isNotEmpty) {
      _lastEventId = message.id;
    }
    if (message.event == 'heartbeat' || message.event == 'connected') return;
    if (message.event == 'reauth_required') {
      _session.notifyExpired();
      return;
    }
    Object? decoded;
    try {
      decoded = jsonDecode(message.data);
    } catch (_) {
      return;
    }
    final envelope = asObject(decoded) ?? const <String, dynamic>{};
    final payload = asObject(envelope['data']) ?? const <String, dynamic>{};
    final type = message.event == 'message'
        ? readString(payload, 'type') ?? readString(envelope, 'type')
        : message.event;
    if (type == null || type.isEmpty) return;
    final resourceId = readString(envelope, 'resource_id') ??
        readString(payload, 'resource_id');
    final version = readInt(envelope, 'resource_version') ??
        readInt(payload, 'resource_version');
    final event = RealtimeEvent(
      type: type,
      resourceId: resourceId == null || resourceId.isEmpty ? null : resourceId,
      version: version,
      data: payload,
    );
    final key = event.key;
    if (key != null && version != null && version > 0) {
      final known = _versions[key] ?? 0;
      if (version <= known) return;
      _versions[key] = version;
    }
    _emit(event);
  }

  void _handleFailure() {
    if (_disposed || !_running) return;
    _failures++;
    _closeSse();
    if (_failures >= 3) {
      if (!_polling) {
        _emit(const RealtimeEvent(type: 'client.resume'));
      }
      _startPolling();
      _retryTimer?.cancel();
      _retryTimer = Timer(const Duration(seconds: 60), () {
        if (_running && _foreground) _connectSse();
      });
      return;
    }
    _reconnectTimer?.cancel();
    final delay = Duration(seconds: 3 * _failures);
    _reconnectTimer = Timer(delay, () {
      if (_running && _foreground) _connectSse();
    });
  }

  void _startPolling() {
    if (_polling || !_running || !_foreground) return;
    _polling = true;
    _pollTimer?.cancel();
    _pollTimer = Timer.periodic(const Duration(seconds: 20), (_) {
      if (_running && _foreground) {
        _emit(const RealtimeEvent(type: 'client.poll'));
      }
    });
  }

  void _closeSse() {
    _subscription?.cancel();
    _subscription = null;
    final connection = _connection;
    _connection = null;
    if (connection != null) {
      unawaited(connection.close());
    }
  }

  void _closeTransports() {
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _closeSse();
    _pollTimer?.cancel();
    _pollTimer = null;
    _polling = false;
  }

  void _emit(RealtimeEvent event) {
    if (!_disposed) _controller.add(event);
  }
}
