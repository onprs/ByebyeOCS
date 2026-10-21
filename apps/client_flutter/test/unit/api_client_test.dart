import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:byebyeocs_client/core/api/api_client.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/core/session/session_manager.dart';

void main() {
  test('Web 写请求携带 CSRF，不使用 Bearer', () async {
    final store = InMemoryTokenStore();
    final session = SessionManager(
      channel: ClientChannel.web,
      store: store,
    );
    await session.acceptCsrf('csrf-token-value');
    http.Request? captured;
    final client = ApiClient(
      config: AppConfig(
        apiBaseUri: Uri.parse('https://api.test/api/v1/'),
        channel: ClientChannel.web,
      ),
      session: session,
      httpClient: MockClient((request) async {
        captured = request;
        return http.Response(
          jsonEncode({'ok': true}),
          200,
          headers: {'content-type': 'application/json'},
        );
      }),
    );

    await client.post('me/data-exports', body: {'format': 'json'});

    expect(captured, isNotNull);
    expect(captured!.headers['X-CSRF-Token'], 'csrf-token-value');
    expect(captured!.headers.containsKey('Authorization'), isFalse);
    expect(captured!.headers['Content-Type'], contains('application/json'));
  });

  test('App 请求使用 Bearer，401 后轮换刷新令牌并重试', () async {
    final store = InMemoryTokenStore();
    final session = SessionManager(
      channel: ClientChannel.app,
      store: store,
    );
    await session.acceptLogin(
      accessToken: 'access-old',
      refreshToken: 'refresh-old',
      accessExpiresAt: DateTime.now().add(const Duration(minutes: 10)),
    );
    var refreshCalls = 0;
    var taskCalls = 0;
    final client = ApiClient(
      config: AppConfig(
        apiBaseUri: Uri.parse('https://api.test/api/v1/'),
        channel: ClientChannel.app,
      ),
      session: session,
      httpClient: MockClient((request) async {
        if (request.url.path == '/api/v1/auth/refresh') {
          refreshCalls++;
          expect(
            jsonDecode(request.body)['refresh_token'],
            'refresh-old',
          );
          return http.Response(
            jsonEncode({
              'access_token': 'access-new',
              'refresh_token': 'refresh-new',
              'expires_at': DateTime.now()
                  .add(const Duration(minutes: 10))
                  .toUtc()
                  .toIso8601String(),
            }),
            200,
            headers: {'content-type': 'application/json'},
          );
        }
        taskCalls++;
        if (request.headers['Authorization'] == 'Bearer access-new') {
          return http.Response(
            jsonEncode({'items': []}),
            200,
            headers: {'content-type': 'application/json'},
          );
        }
        return http.Response(
          jsonEncode({
            'error': {
              'code': 'unauthenticated',
              'message': '登录已失效',
              'retryable': false,
              'scope': 'request',
            },
          }),
          401,
          headers: {'content-type': 'application/json'},
        );
      }),
    );

    final result = await client.get('tasks');
    expect(result.statusCode, 200);
    expect(refreshCalls, 1);
    expect(taskCalls, 2);
    final stored = await store.read();
    expect(stored?.accessToken, 'access-new');
    expect(stored?.refreshToken, 'refresh-new');
  });
}
