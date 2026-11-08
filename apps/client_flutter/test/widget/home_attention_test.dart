import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/features/home/home_page.dart';
import 'package:byebyeocs_client/features/courses/course_detail_page.dart';

import '../helpers.dart';

void main() {
  testWidgets('没有当前安排时历史关闭任务不会产生首页提醒', (tester) async {
    final server = TestServer(overrides: {
      'GET /api/v1/plans': (_) async => jsonResponse({'items': []}),
      'GET /api/v1/tasks': (request) async {
        if (request.url.queryParameters['actionable'] == 'true') {
          expect(request.url.queryParameters['limit'], '1');
          return jsonResponse({'items': [], 'total_count': 0});
        }
        return jsonResponse({
          'items': [
            {
              ...taskFixtures.first,
              'state': 'needs_attention',
              'last_error_code': 'course_closed'
            },
            {...taskFixtures.last, 'state': 'confirmed'},
          ]
        });
      },
    });
    await pumpTestPage(tester, const HomePage(), handler: server.call);
    expect(find.textContaining('项任务需要处理'), findsNothing);
    expect(find.text('有一项任务需要处理'), findsNothing);
    expect(find.text('去处理'), findsNothing);
  });

  testWidgets('首页异常总数使用服务端全量统计而非样本数量', (tester) async {
    final server = TestServer(overrides: {
      'GET /api/v1/tasks': (request) async =>
          request.url.queryParameters['actionable'] == 'true'
              ? jsonResponse({
                  'items': [
                    {
                      ...taskFixtures.first,
                      'state': 'needs_attention',
                      'last_error_code': 'answer_unavailable'
                    }
                  ],
                  'total_count': 137,
                  'next_cursor': 'another-page',
                })
              : jsonResponse({'items': []}),
    });
    await pumpTestPage(tester, const HomePage(), handler: server.call);
    expect(find.text('137 项任务需要处理'), findsOneWidget);
    expect(find.text('有一项任务需要处理'), findsNothing);
  });

  testWidgets('章节关闭的异常样本不会在首页重复要求处理', (tester) async {
    final server = TestServer(overrides: {
      'GET /api/v1/tasks': (_) async => jsonResponse({
            'items': [
              {
                ...taskFixtures.first,
                'state': 'needs_attention',
                'last_error_code': 'answer_unavailable',
                'availability': 'closed'
              }
            ],
            'total_count': 1,
          }),
    });
    await pumpTestPage(tester, const HomePage(), handler: server.call);
    expect(find.text('去处理'), findsNothing);
  });

  testWidgets('关闭且未能读取内容的章节显示平台状态', (tester) async {
    final server = TestServer(overrides: {
      'GET /api/v1/courses/c1': (_) async => jsonResponse({
            ...courseDetailFixture,
            'task_point_count': 0,
            'incomplete_task_point_count': 0,
            'chapters': [
              {
                'id': 'closed-chapter',
                'external_id': '701',
                'title': '关闭章节',
                'availability': 'closed',
                'attachments': [],
              }
            ],
          }),
    });
    await pumpTestPage(tester, const CourseDetailPage(courseId: 'c1'),
        handler: server.call, size: const Size(390, 1000));
    expect(find.text('已关闭'), findsWidgets);
    expect(find.text('本章已关闭，暂不可访问'), findsOneWidget);
    expect(find.text('本章没有内容条目'), findsNothing);
    expect(find.text('无任务点'), findsNothing);
    expect(tester.takeException(), isNull);
  });
}
