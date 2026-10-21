import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:byebyeocs_client/core/config/server_config.dart';

void main() {
  test('服务地址只接受 HTTPS，并规范到 API 前缀', () {
    expect(
      normalizeServiceBase('https://api.example.test')?.toString(),
      'https://api.example.test/api/v1/',
    );
    expect(
      normalizeServiceBase('https://api.example.test/api/v1')?.toString(),
      'https://api.example.test/api/v1/',
    );
    expect(
      normalizeServiceBase('https://api.example.test/service/')?.toString(),
      'https://api.example.test/service/',
    );
  });

  test('正式环境拒绝明文 HTTP、用户信息与查询参数', () {
    expect(normalizeServiceBase('http://api.example.test'), isNull);
    expect(normalizeServiceBase('https://user:pass@api.example.test'), isNull);
    expect(normalizeServiceBase('https://api.example.test?redirect=x'), isNull);
    expect(normalizeServiceBase(''), isNull);
  });

  test('调试环境只允许本机与私有网段 HTTP', () {
    expect(
      normalizeServiceBase('http://127.0.0.1:8090', allowDebugHttp: true)
          ?.toString(),
      'http://127.0.0.1:8090/api/v1/',
    );
    expect(
      normalizeServiceBase('http://192.168.1.20:8090', allowDebugHttp: true),
      isNotNull,
    );
    expect(
      normalizeServiceBase('http://example.com', allowDebugHttp: true),
      isNull,
    );
  });

  test('原生客户端保存服务地址到本机偏好', () async {
    SharedPreferences.setMockInitialValues({});
    final container = ProviderContainer();
    addTearDown(container.dispose);
    await Future<void>.delayed(const Duration(milliseconds: 20));

    final result = await container
        .read(serverConfigProvider.notifier)
        .setBaseUrl('https://api.example.test');
    expect(result.ok, isTrue);
    expect(
      container.read(serverConfigProvider).baseUri?.toString(),
      'https://api.example.test/api/v1/',
    );
    final prefs = await SharedPreferences.getInstance();
    expect(
      prefs.getString(ServerConfigController.preferenceKey),
      'https://api.example.test/api/v1/',
    );
  });
}
