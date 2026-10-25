import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:byebyeocs_client/core/api/api_client.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/core/repositories/auth_repository.dart';
import 'package:byebyeocs_client/core/session/session_manager.dart';

void main() {
  test('登录校验只提交账号与密码，不发送学校或验证码', () async {
    final session = SessionManager(
      channel: ClientChannel.web,
      store: InMemoryTokenStore(),
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
          jsonEncode({'mode': 'password', 'csrf_token': 'csrf-token-value'}),
          200,
          headers: {'content-type': 'application/json'},
        );
      }),
    );
    final repository = AuthRepository(api: client, session: session);

    await repository.verifyChallenge(
      id: 'challenge-1',
      account: 'learner-a',
      password: 'secret-value',
    );

    expect(captured, isNotNull);
    expect(captured!.url.path,
        endsWith('/auth/chaoxing/challenges/challenge-1/verify'));
    final body = jsonDecode(captured!.body) as Map<String, dynamic>;
    expect(body.keys.toSet(), {'account', 'password'});
    expect(body['account'], 'learner-a');
    expect(body['password'], 'secret-value');
  });
}
