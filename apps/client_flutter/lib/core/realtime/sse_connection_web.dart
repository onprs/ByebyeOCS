import 'dart:async';
import 'dart:js_interop';

import 'package:web/web.dart' as web;

import 'sse_connection_base.dart';

const _eventNames = <String>[
  'message',
  'connected',
  'heartbeat',
  'reauth_required',
  'task.claimed',
  'task.state_changed',
  'task.attempt_finished',
  'task.confirmed',
  'task.recovery_verification',
  'task.platform_confirmed_after_cancel',
  'task.control_applied',
  'task.control_accepted',
  'plan.revised',
  'plan.control_applied',
  'course.snapshot_created',
  'course.sync_requested',
  'course.sync_finished',
  'wallet.updated',
  'recharge_order.updated',
  'question.query_updated',
  'question.submission_updated',
  'account.operation_applied',
  'privacy.deletion_requested',
];

class WebSseConnection implements SseConnection {
  WebSseConnection(Uri uri) {
    _source = web.EventSource(
      uri.toString(),
      web.EventSourceInit(withCredentials: true),
    );
    for (final name in _eventNames) {
      _source.addEventListener(
        name,
        ((web.MessageEvent event) {
          final data = event.data?.dartify();
          _controller.add(
            SseMessage(
              event: name == 'message' ? 'message' : name,
              data: data is String ? data : '',
              id: event.lastEventId.isEmpty ? null : event.lastEventId,
            ),
          );
        }).toJS,
      );
    }
    _source.addEventListener(
      'error',
      ((web.Event event) {
        _controller.addError(StateError('事件流连接中断'));
      }).toJS,
    );
  }

  final StreamController<SseMessage> _controller =
      StreamController<SseMessage>.broadcast();
  late final web.EventSource _source;
  bool _closed = false;

  @override
  Stream<SseMessage> get messages => _controller.stream;

  @override
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    _source.close();
    await _controller.close();
  }
}

SseConnection connectPlatformSse(
  Uri uri, {
  String? bearerToken,
  String? lastEventId,
}) {
  return WebSseConnection(uri);
}
