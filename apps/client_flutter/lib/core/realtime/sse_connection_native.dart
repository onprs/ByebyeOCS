import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

import 'sse_connection_base.dart';

class NativeSseConnection implements SseConnection {
  NativeSseConnection(Uri uri, {String? bearerToken, String? lastEventId}) {
    _connect(uri, bearerToken, lastEventId);
  }

  final StreamController<SseMessage> _controller =
      StreamController<SseMessage>.broadcast();
  final http.Client _client = http.Client();
  StreamSubscription<String>? _subscription;
  bool _closed = false;

  @override
  Stream<SseMessage> get messages => _controller.stream;

  Future<void> _connect(Uri uri, String? token, String? lastEventId) async {
    try {
      final request = http.Request('GET', uri)
        ..headers['Accept'] = 'text/event-stream'
        ..headers['Cache-Control'] = 'no-cache';
      if (token != null && token.isNotEmpty) {
        request.headers['Authorization'] = 'Bearer $token';
      }
      if (lastEventId != null && lastEventId.isNotEmpty) {
        request.headers['Last-Event-ID'] = lastEventId;
      }
      final response = await _client.send(request);
      if (response.statusCode != 200) {
        _controller.addError(
          StateError('事件流不可用（${response.statusCode}）'),
        );
        return;
      }
      final buffer = _SseLineBuffer();
      _subscription = response.stream.transform(utf8.decoder).listen(
        (chunk) {
          for (final message in buffer.add(chunk)) {
            _controller.add(message);
          }
        },
        onError: (Object error) {
          if (!_closed) _controller.addError(error);
        },
        onDone: () {
          if (!_closed) {
            _controller.addError(StateError('事件流已结束'));
          }
        },
        cancelOnError: false,
      );
    } catch (error) {
      if (!_closed) _controller.addError(error);
    }
  }

  @override
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    await _subscription?.cancel();
    _client.close();
    await _controller.close();
  }
}

class _SseLineBuffer {
  String _pending = '';
  String _event = 'message';
  final StringBuffer _data = StringBuffer();
  String? _id;

  List<SseMessage> add(String chunk) {
    _pending += chunk;
    final messages = <SseMessage>[];
    while (true) {
      final index = _pending.indexOf('\n');
      if (index < 0) break;
      var line = _pending.substring(0, index);
      _pending = _pending.substring(index + 1);
      if (line.endsWith('\r')) {
        line = line.substring(0, line.length - 1);
      }
      if (line.isEmpty) {
        if (_data.isNotEmpty) {
          messages.add(
            SseMessage(
              event: _event,
              data: _data.toString(),
              id: _id,
            ),
          );
        }
        _event = 'message';
        _data.clear();
        _id = null;
        continue;
      }
      if (line.startsWith(':')) continue;
      final separator = line.indexOf(':');
      final field = separator < 0 ? line : line.substring(0, separator);
      var value = separator < 0 ? '' : line.substring(separator + 1);
      if (value.startsWith(' ')) value = value.substring(1);
      switch (field) {
        case 'event':
          _event = value;
        case 'data':
          if (_data.isNotEmpty) _data.write('\n');
          _data.write(value);
        case 'id':
          _id = value;
      }
    }
    return messages;
  }
}

SseConnection connectPlatformSse(
  Uri uri, {
  String? bearerToken,
  String? lastEventId,
}) {
  return NativeSseConnection(
    uri,
    bearerToken: bearerToken,
    lastEventId: lastEventId,
  );
}
