import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:byebyeocs_client/features/courses/courses_page.dart';

import '../helpers.dart';

void main() {
  testWidgets('搜索进行中保留搜索框，不被整页加载态移除', (tester) async {
    final pendingSearch = Completer<http.Response>();
    final server = TestServer();
    var searchRequests = 0;

    Future<http.Response> handler(http.Request request) {
      if (request.url.path == '/api/v1/courses' &&
          request.url.queryParameters.containsKey('search')) {
        searchRequests++;
        return pendingSearch.future;
      }
      return server.call(request);
    }

    await pumpTestPage(tester, const CoursesPage(), handler: handler);
    expect(find.byType(TextField), findsOneWidget);

    await tester.enterText(find.byType(TextField), '设计');
    await tester.pump(const Duration(milliseconds: 400));
    await tester.pump();

    expect(searchRequests, 1);
    // 搜索请求尚未完成时仍能看到并继续编辑搜索词。
    expect(find.byType(TextField), findsOneWidget);

    pendingSearch.complete(
      jsonResponse({'items': coursesFixture, 'next_cursor': ''}),
    );
    await settle(tester);
    expect(find.text('设计思维与创新方法'), findsWidgets);
  });

  testWidgets('课程卡片把已关闭任务点从待完成中排除并单独标注', (tester) async {
    final course = <String, dynamic>{
      ...coursesFixture.first,
      'task_point_count': 10,
      'incomplete_task_point_count': 4,
      'closed_task_point_count': 2,
    };
    final server = TestServer(
      overrides: {
        'GET /api/v1/courses': (_) async => jsonResponse({
              'items': [course],
              'next_cursor': '',
            }),
      },
    );

    await pumpTestPage(tester, const CoursesPage(), handler: server.call);

    expect(find.textContaining('2 个任务点待完成'), findsOneWidget);
    expect(find.textContaining('2 个已关闭'), findsWidgets);
  });

  testWidgets('已结束课程显示独立状态且不计待完成', (tester) async {
    final course = <String, dynamic>{
      ...coursesFixture.first,
      'task_point_count': 10,
      'incomplete_task_point_count': 4,
      'closed_task_point_count': 0,
      'platform_state': 'closed',
    };
    final server = TestServer(
      overrides: {
        'GET /api/v1/courses': (_) async => jsonResponse({
              'items': [course],
              'next_cursor': '',
            }),
      },
    );

    await pumpTestPage(tester, const CoursesPage(), handler: server.call);

    expect(find.text('已结束'), findsOneWidget);
    expect(find.textContaining('课程已结束，不再派发任务'), findsOneWidget);
    expect(find.textContaining('个任务点待完成'), findsNothing);
  });
}
