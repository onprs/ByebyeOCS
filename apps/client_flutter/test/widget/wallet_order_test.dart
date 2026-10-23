import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:byebyeocs_client/features/wallet/wallet_page.dart';

import '../helpers.dart';

void main() {
  testWidgets('修改充值金额后重试使用新的幂等键', (tester) async {
    final server = TestServer();
    final keys = <String>[];
    var attempts = 0;

    Future<http.Response> handler(http.Request request) {
      if (request.method == 'POST' &&
          request.url.path == '/api/v1/recharge-orders') {
        keys.add(request.headers['Idempotency-Key'] ?? '');
        attempts++;
        if (attempts == 1) {
          return Future<http.Response>.value(
            jsonResponse(
              {
                'error': {
                  'code': 'service_unavailable',
                  'message': '充值订单暂时未能创建',
                  'request_id': 'req_test',
                  'retryable': true,
                  'scope': 'request',
                },
              },
              status: 500,
            ),
          );
        }
        return Future<http.Response>.value(
          jsonResponse(<String, dynamic>{
            ...orderFixtures[2],
            'id': 'o-new',
          }),
        );
      }
      return server.call(request);
    }

    await pumpTestPage(tester, const WalletPage(), handler: handler);

    await tester.enterText(find.byType(TextField).first, '10');
    await tester.tap(find.text('创建充值订单'));
    await settle(tester);
    expect(attempts, 1);

    await tester.enterText(find.byType(TextField).first, '20');
    await tester.tap(find.text('创建充值订单'));
    await settle(tester);

    expect(attempts, 2);
    expect(keys, hasLength(2));
    expect(keys.first, isNotEmpty);
    expect(
      keys[1],
      isNot(keys[0]),
      reason: '金额变化后复用旧幂等键会被服务端判定为同一键对应不同请求',
    );
  });
}
