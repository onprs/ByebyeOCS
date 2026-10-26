import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/core/models/course.dart';
import 'package:byebyeocs_client/core/models/preferences.dart';
import 'package:byebyeocs_client/design/theme.dart';
import 'package:byebyeocs_client/features/courses/course_sync_controller.dart';
import 'package:byebyeocs_client/features/courses/course_sync_notice.dart';
import 'package:byebyeocs_client/features/courses/courses_page.dart';

import '../helpers.dart';

void main() {
  Widget build(CourseSyncState state, {VoidCallback? onRetry}) {
    return MaterialApp(
      theme: AppTheme.build(ColorSchemeId.graphite, Brightness.light),
      home: Scaffold(
        body: CourseSyncNotice(
          state: state,
          onDismiss: () {},
          onRetry: onRetry ?? () {},
        ),
      ),
    );
  }

  testWidgets('排队与运行中的同步展示内联进度', (tester) async {
    await tester.pumpWidget(build(
      const CourseSyncState(
        phase: CourseSyncPhase.queued,
        result: CourseSyncResult(id: 'sync-1', status: 'queued'),
      ),
    ));

    expect(find.text('课程同步已排队，正在等待开始…'), findsOneWidget);
    expect(find.byType(CircularProgressIndicator), findsOneWidget);

    await tester.pumpWidget(build(
      const CourseSyncState(
        phase: CourseSyncPhase.running,
        result: CourseSyncResult(
          id: 'sync-1',
          status: 'running',
          courseCount: 12,
        ),
      ),
    ));

    expect(find.text('正在同步课程… 已读取 12 门'), findsOneWidget);
    expect(find.byTooltip('关闭'), findsNothing);
  });

  testWidgets('失败内联展示重试与关闭，不出现手动同步入口', (tester) async {
    var retries = 0;
    await tester.pumpWidget(build(
      const CourseSyncState(
        phase: CourseSyncPhase.failed,
        error: '课程同步未完成，请重试',
      ),
      onRetry: () => retries++,
    ));

    expect(find.text('课程同步未完成，请重试'), findsOneWidget);
    expect(find.text('同步课程'), findsNothing);
    expect(find.byTooltip('关闭'), findsOneWidget);
    await tester.tap(find.text('重试'));
    expect(retries, 1);
  });

  testWidgets('成功提示可关闭，关闭后同一 sync id 不再出现', (tester) async {
    const result = CourseSyncResult(
      id: 'sync-2',
      status: 'succeeded',
      courseCount: 3,
    );
    await tester.pumpWidget(build(
      const CourseSyncState(
        phase: CourseSyncPhase.succeeded,
        result: result,
      ),
    ));

    expect(find.text('课程同步已完成，共 3 门课程。'), findsOneWidget);
    expect(find.text('重试'), findsNothing);

    await tester.pumpWidget(build(
      const CourseSyncState(
        phase: CourseSyncPhase.succeeded,
        result: result,
        dismissedKey: 'sync-2',
      ),
    ));

    expect(find.text('课程同步已完成，共 3 门课程。'), findsNothing);
  });

  testWidgets('部分课程结构不兼容只提示关闭，不提供无意义重试', (tester) async {
    await tester.pumpWidget(build(
      const CourseSyncState(
        phase: CourseSyncPhase.partial,
        result: CourseSyncResult(
          id: 'sync-3',
          status: 'needs_attention',
          errorCode: 'course_structure_partial',
        ),
      ),
    ));

    expect(find.text('部分课程结构待适配，已同步可用课程。'), findsOneWidget);
    expect(find.text('重试'), findsNothing);
    expect(find.byTooltip('关闭'), findsOneWidget);
  });

  testWidgets('课程页只展示一处同步提示，关闭后不会重新出现', (tester) async {
    final server = TestServer(
      overrides: {
        '/api/v1/course-syncs/latest': (_) async => jsonResponse({
              'id': 'sync-page-failed',
              'status': 'failed',
              'course_count': 0,
              'requested_at': '2026-10-26T02:00:00Z',
              'finished_at': '2026-10-26T02:05:00Z',
            }),
      },
    );
    await pumpTestPage(tester, const CoursesPage(), handler: server.call);

    expect(find.text('课程同步未完成，请重试'), findsOneWidget);
    expect(find.text('重试'), findsOneWidget);
    expect(find.text('知道了'), findsNothing);
    expect(find.byType(CourseSyncNotice), findsOneWidget);

    await tester.tap(find.byTooltip('关闭'));
    await settle(tester);

    expect(find.byType(CourseSyncNotice), findsNothing);
    expect(find.text('课程同步未完成，请重试'), findsNothing);
  });

  testWidgets('课程概要不把尚未读取的任务显示为空课程', (tester) async {
    final server = TestServer(overrides: {
      '/api/v1/courses': (_) async => jsonResponse({
            'items': [
              {
                'id': 'pending-course',
                'title': '内容待加载的课程',
                'snapshot_version': 0,
                'stale': true,
                'chapter_count': 0,
                'task_point_count': 0,
              },
            ],
          }),
      '/api/v1/course-syncs/latest': (_) async => jsonResponse({
            'id': 'pending-sync',
            'status': 'running',
            'course_count': 0,
          }),
    });
    await pumpTestPage(tester, const CoursesPage(), handler: server.call);
    expect(find.text('内容待加载的课程'), findsOneWidget);
    expect(find.text('章节待同步'), findsOneWidget);
    expect(find.text('课程内容待同步'), findsOneWidget);
    expect(find.text('课程暂无任务点'), findsNothing);
    expect(tester.takeException(), isNull);
  });
}
