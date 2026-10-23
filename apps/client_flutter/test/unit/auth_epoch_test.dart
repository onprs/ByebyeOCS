import 'dart:async';
import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:byebyeocs_client/app/providers.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/config/server_config.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/features/auth/auth_controller.dart';

class _LoadedServerConfig extends ServerConfigController {
  @override
  ServerConfigState build() => ServerConfigState(
        baseUri: Uri.parse('https://old.example.test/api/v1/'),
        loaded: true,
      );
}

void main() {
  test('切换服务后，切换前发出的会话请求不得把用户重新置为已登录', () async {
    SharedPreferences.setMockInitialValues({});
    final sessionGate = Completer<http.Response>();
    final sessionRequested = Completer<void>();

    final container = ProviderContainer(
      overrides: [
        appConfigProvider.overrideWithValue(
          AppConfig(
            apiBaseUri: Uri.parse('https://old.example.test/api/v1/'),
            channel: ClientChannel.web,
          ),
        ),
        serverConfigProvider.overrideWith(_LoadedServerConfig.new),
        tokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        httpClientProvider.overrideWithValue(
          MockClient((request) async {
            switch (request.url.path) {
              case '/api/v1/auth/session':
                if (sessionRequested.isCompleted == false) {
                  sessionRequested.complete();
                }
                return sessionGate.future;
              case '/api/v1/capabilities':
                return http.Response(
                  jsonEncode({'api_version': 'v1'}),
                  200,
                  headers: {'content-type': 'application/json'},
                );
              case '/api/v1/me':
                return http.Response(
                  jsonEncode({
                    'id': 'stale-user',
                    'status': 'active',
                    'display_name': '旧会话用户',
                    'school': '旧学校',
                    'provider': 'chaoxing',
                    'platform_session': {'state': 'active'},
                    'session': {'channel': 'web', 'fresh': true},
                  }),
                  200,
                  headers: {'content-type': 'application/json'},
                );
              case '/api/v1/me/preferences':
                return http.Response(
                  jsonEncode({
                    'color_scheme': 'graphite',
                    'theme_mode': 'system',
                    'version': 1,
                  }),
                  200,
                  headers: {'content-type': 'application/json'},
                );
            }
            return http.Response(
              jsonEncode({
                'error': {
                  'code': 'not_found',
                  'message': '不存在',
                  'retryable': false,
                  'scope': 'request',
                },
              }),
              404,
              headers: {'content-type': 'application/json'},
            );
          }),
        ),
      ],
    );
    addTearDown(container.dispose);
    container.listen(authControllerProvider, (_, __) {});

    await sessionRequested.future;
    await container
        .read(authControllerProvider.notifier)
        .clearForServerChange();
    expect(
      container.read(authControllerProvider).status,
      AuthStatus.anonymous,
    );

    sessionGate.complete(
      http.Response(
        jsonEncode({
          'authenticated': true,
          'kind': 'user',
          'capabilities': {'features': <String, bool>{}},
        }),
        200,
        headers: {'content-type': 'application/json'},
      ),
    );
    for (var index = 0; index < 40; index++) {
      await Future<void>.delayed(Duration.zero);
    }

    final state = container.read(authControllerProvider);
    expect(state.status, AuthStatus.anonymous);
    expect(state.user, isNull);
  });
}
