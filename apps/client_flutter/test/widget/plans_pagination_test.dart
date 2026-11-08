import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:byebyeocs_client/features/plans/plans_page.dart';
import '../helpers.dart';

List<Map<String, dynamic>> _tasks(int count, {String state = 'running'}) =>
    List.generate(
        count,
        (i) => {
              ...taskFixtures.first,
              'id': 'paged-$state-$i',
              'title': '分页内容 ${i + 1}',
              'state': state,
            });
Future<void> _bottom(WidgetTester tester) async {
  final scroll = tester.state<ScrollableState>(find.byType(Scrollable).first);
  scroll.position.jumpTo(scroll.position.maxScrollExtent);
  await tester.pump();
}

void main() {
  testWidgets('不足一页不显示页码且任务行没有状态胶囊', (tester) async {
    final server = TestServer(overrides: {
      'GET /api/v1/tasks': (r) async =>
          jsonResponse(taskPageFixture(r, _tasks(7, state: 'preparing')))
    });
    await pumpTestPage(tester, const PlansPage(), handler: server.call);
    expect(find.text('分页内容 7'), findsOneWidget);
    expect(find.byTooltip('下一页'), findsNothing);
    expect(find.text('准备中'), findsNothing);
  });
  testWidgets('服务端分类17项显示两页，前后翻页不重复不回退末页', (tester) async {
    final requests = <String?>[];
    final all = [..._tasks(17, state: 'needs_attention'), ..._tasks(26)];
    final server = TestServer(overrides: {
      'GET /api/v1/tasks': (r) async {
        requests.add(r.url.queryParameters['cursor']);
        return jsonResponse(taskPageFixture(r, all));
      }
    });
    await pumpTestPage(tester, const PlansPage(), handler: server.call);
    await tester.tap(find.text('需要处理 17'));
    await settle(tester);
    expect(find.text('分页内容 1'), findsOneWidget);
    expect(find.text('分页内容 10'), findsOneWidget);
    expect(find.text('共 17 条'), findsOneWidget);
    expect(find.text('1 / 2'), findsOneWidget);
    await _bottom(tester);
    await tester.tap(find.byTooltip('下一页'));
    await settle(tester);
    expect(requests.last, 'fixture:10');
    expect(find.text('分页内容 1'), findsNothing);
    expect(find.text('分页内容 11'), findsOneWidget);
    expect(find.text('分页内容 17'), findsOneWidget);
    expect(find.text('2 / 2'), findsOneWidget);
    await _bottom(tester);
    final next = find.descendant(
        of: find.byTooltip('下一页'), matching: find.byType(InkWell));
    expect(tester.widget<InkWell>(next).onTap, isNull);
    await tester.tap(find.byTooltip('上一页'));
    await settle(tester);
    expect(requests.last, isNull);
    expect(find.text('分页内容 1'), findsOneWidget);
    expect(find.text('分页内容 11'), findsNothing);
  });
  testWidgets('切换分类重置页码并请求新分类而非过滤旧页', (tester) async {
    final categories = <String?>[];
    final all = [..._tasks(17, state: 'needs_attention'), ..._tasks(26)];
    final server = TestServer(overrides: {
      'GET /api/v1/tasks': (r) async {
        categories.add(r.url.queryParameters['category']);
        return jsonResponse(taskPageFixture(r, all));
      }
    });
    await pumpTestPage(tester, const PlansPage(), handler: server.call);
    await _bottom(tester);
    await tester.tap(find.byTooltip('下一页'));
    await settle(tester);
    final scroll = tester.state<ScrollableState>(find.byType(Scrollable).first);
    scroll.position.jumpTo(0);
    await tester.pump();
    await tester.ensureVisible(find.text('需要处理 17'));
    await tester.tap(find.text('需要处理 17'));
    await settle(tester);
    expect(categories.last, 'attention');
    expect(find.text('1 / 2'), findsOneWidget);
    expect(find.text('分页内容 1'), findsOneWidget);
  });
}
