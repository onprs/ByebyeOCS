import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/features/wallet/wallet_page.dart';

import '../helpers.dart';

void main() {
  testWidgets('钱包长流水使用懒构建，未滚动时不生成全部记录', (tester) async {
    final entries = List<Map<String, Object>>.generate(120, (index) {
      return <String, Object>{
        'journal_id': 'e$index',
        'kind': 'recharge',
        'reason': '测试流水 $index',
        'available_delta_minor': '100',
        'reserved_delta_minor': '0',
        'order_id': 'o${index + 1}',
        'created_at': '2026-10-20T02:42:00Z',
      };
    });
    final server = TestServer(
      overrides: {
        'GET /api/v1/wallet/entries': (_) async => jsonResponse({
              'items': entries,
              'next_cursor': '',
            }),
      },
    );

    await pumpTestPage(tester, const WalletPage(), handler: server.call);
    await tester.tap(find.text('流水 120'));
    await settle(tester);

    // 长列表只构建视口附近的记录，最后一条尚不存在于元素树。
    expect(find.byKey(const ValueKey('wallet-entry-e0')), findsOneWidget);
    expect(find.byKey(const ValueKey('wallet-entry-e119')), findsNothing);

    final scrollable = find.byType(CustomScrollView);
    await tester.drag(scrollable, const Offset(0, -6000));
    await settle(tester);
    await tester.drag(scrollable, const Offset(0, -6000));
    await settle(tester);

    expect(
      find.byKey(const ValueKey('wallet-entry-e119')),
      findsOneWidget,
    );
  });
}
