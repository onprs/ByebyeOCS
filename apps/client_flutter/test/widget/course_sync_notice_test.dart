import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/core/models/course.dart';
import 'package:byebyeocs_client/design/theme.dart';
import 'package:byebyeocs_client/features/courses/course_sync_notice.dart';
import 'package:byebyeocs_client/features/courses/courses_controller.dart';
import 'package:byebyeocs_client/core/models/preferences.dart';

void main() {
  Widget build(CourseListState state, VoidCallback retry) {
    return MaterialApp(
      theme: AppTheme.build(ColorSchemeId.graphite, Brightness.light),
      home: Scaffold(
        body: CourseSyncNotice(state: state, onRetry: retry),
      ),
    );
  }

  testWidgets('自动同步中展示实时课程数量', (tester) async {
    await tester.pumpWidget(build(
      const CourseListState(
        syncing: true,
        syncResult: CourseSyncResult(
          id: 'sync-1',
          status: 'running',
          courseCount: 12,
        ),
      ),
      () {},
    ));

    expect(find.text('正在同步课程… 已读取 12 门'), findsOneWidget);
    expect(find.byType(CircularProgressIndicator), findsOneWidget);
  });

  testWidgets('失败时给出重试且不显示手动同步入口', (tester) async {
    var retries = 0;
    await tester.pumpWidget(build(
      const CourseListState(syncError: '课程同步未完成，请重试'),
      () => retries++,
    ));

    expect(find.text('课程同步未完成，请重试'), findsOneWidget);
    expect(find.text('同步课程'), findsNothing);
    await tester.tap(find.text('重试'));
    expect(retries, 1);
  });
}
