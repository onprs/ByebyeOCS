import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:byebyeocs_client/app/providers.dart';
import 'package:byebyeocs_client/core/config/server_config.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
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
  test('切换服务前清除旧令牌并回到未登录状态', () async {
    SharedPreferences.setMockInitialValues({});
    final store = InMemoryTokenStore();
    await store.write(
      const StoredTokens(
        accessToken: 'old-access',
        refreshToken: 'old-refresh',
      ),
    );
    final container = ProviderContainer(
      overrides: [
        appConfigProvider.overrideWithValue(
          AppConfig(
            apiBaseUri: Uri.parse('https://old.example.test/api/v1/'),
            channel: ClientChannel.app,
          ),
        ),
        serverConfigProvider.overrideWith(_LoadedServerConfig.new),
        tokenStoreProvider.overrideWithValue(store),
        httpClientProvider.overrideWithValue(
          MockClient((request) async {
            if (request.url.path == '/api/v1/auth/session') {
              return http.Response(
                jsonEncode({
                  'authenticated': false,
                  'kind': 'user',
                  'capabilities': {},
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
    await Future<void>.delayed(const Duration(milliseconds: 80));
    container.read(authControllerProvider);
    for (var index = 0; index < 20; index++) {
      await Future<void>.delayed(const Duration(milliseconds: 20));
      if (container.read(authControllerProvider).status != AuthStatus.loading) {
        break;
      }
    }
    expect(await store.read(), isNotNull);

    await container
        .read(authControllerProvider.notifier)
        .clearForServerChange();

    expect(await store.read(), isNull);
    for (var index = 0; index < 40; index++) {
      await Future<void>.delayed(const Duration(milliseconds: 20));
      if (container.read(authControllerProvider).status ==
          AuthStatus.anonymous) {
        break;
      }
    }
    expect(
      container.read(authControllerProvider).status,
      AuthStatus.anonymous,
    );
  });
}
