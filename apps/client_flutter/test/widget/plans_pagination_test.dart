import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/features/plans/plans_page.dart';

import '../helpers.dart';

void main() {
  testWidgets('安排页任务列表可加载下一页并继续按状态分组', (tester) async {
    final server = TestServer(
      overrides: {
        'GET /api/v1/tasks': (request) async {
          final cursor = request.url.queryParameters['cursor'];
          if (cursor == 'next-1') {
            return jsonResponse({
              'items': [taskFixtures[1]],
              'next_cursor': '',
            });
          }
          return jsonResponse({
            'items': [taskFixtures[0]],
            'next_cursor': 'next-1',
          });
        },
      },
    );

    await pumpTestPage(tester, const PlansPage(), handler: server.call);
    expect(find.text('1.1 发现真正的问题'), findsOneWidget);
    expect(find.text('2.1 从观察到洞察'), findsNothing);

    await tester.tap(find.text('加载更多'));
    await settle(tester);

    expect(find.text('2.1 从观察到洞察'), findsOneWidget);
    expect(find.text('等待平台确认 · 1'), findsOneWidget);
    expect(find.text('加载更多'), findsNothing);
  });
}
