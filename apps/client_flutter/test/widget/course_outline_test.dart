import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/core/models/plan.dart';
import 'package:byebyeocs_client/features/courses/course_detail_page.dart';
import 'package:byebyeocs_client/features/plans/plan_editor.dart';

import '../fixtures/course_outline.dart';
import '../helpers.dart';

void main() {
  for (final width in [390.0, 1280.0]) {
    testWidgets('${width.toInt()}px 目录按原始层级折叠且刷新后保留分组展开状态', (tester) async {
      var fixture = Map<String, dynamic>.from(courseOutlineFixture);
      final server = TestServer(overrides: {
        'GET /api/v1/courses/c1': (_) async => jsonResponse(fixture),
      });
      await pumpTestPage(
        tester,
        const CourseDetailPage(courseId: 'c1'),
        handler: server.call,
        size: Size(width, 1100),
      );
      expect(find.text('1 课程导学'), findsOneWidget);
      expect(find.text('1.1 导学说明'), findsOneWidget);
      expect(find.text('1.1.1 学习目标'), findsOneWidget);
      expect(find.text('1.1.1.1 学习要求'), findsOneWidget);
      expect(find.text('导学视频任务'), findsOneWidget);
      expect(find.text('2.1 专题概述'), findsNothing);
      expect(tester.takeException(), isNull);

      final chapters =
          List<Map<String, dynamic>>.from(fixture['chapters'] as List);
      fixture = {
        ...fixture,
        'chapters': [...chapters.skip(5), ...chapters.take(5)],
      };
      await tester.tap(find.byTooltip('刷新课程'));
      await settle(tester);
      expect(find.text('2.1 专题概述'), findsNothing);
      expect(find.text('1.1.1.1 学习要求'), findsOneWidget);

      await tester.ensureVisible(find.text('1 课程导学'));
      await tester.tap(find.text('1 课程导学'));
      await settle(tester);
      expect(find.text('1.1 导学说明'), findsNothing);
      await tester.ensureVisible(find.text('2 专题学习'));
      await tester.tap(find.text('2 专题学习'));
      await settle(tester);
      expect(find.text('2.1 专题概述'), findsOneWidget);
      expect(find.text('专题视频任务'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('手机展示深层目录时缩进有界且保留所有层级', (tester) async {
    final chapters = List.generate(
        32,
        (index) => <String, dynamic>{
              'id': 'chapter-$index',
              'external_id': '$index',
              'parent_external_id': index == 0 ? '' : '${index - 1}',
              'number': List.filled(index + 1, '1').join('.'),
              'title': '第${index + 1}级目录',
              'attachments': <Map<String, dynamic>>[],
            });
    final server = TestServer(overrides: {
      'GET /api/v1/courses/c1': (_) async => jsonResponse({
            ...courseOutlineFixture,
            'chapter_count': 32,
            'task_point_count': 0,
            'incomplete_task_point_count': 0,
            'resource_count': 0,
            'chapters': chapters,
          }),
    });
    await pumpTestPage(tester, const CourseDetailPage(courseId: 'c1'),
        handler: server.call, size: const Size(390, 1100));
    expect(find.textContaining('第32级目录'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('课程卡片的学习时长只作为平台进度展示', (tester) async {
    await pumpTestPage(tester, const CourseDetailPage(courseId: 'c1'));
    final task = find.text('1.1 发现真正的问题');
    await tester.ensureVisible(task);
    await tester.tap(task);
    await settle(tester);
    expect(find.text('平台记录时长'), findsOneWidget);
    expect(find.textContaining('平台记录的学习时长'), findsOneWidget);
    expect(find.text('实际播放记录'), findsOneWidget);
    expect(find.text('无播放记录'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('安排勾选目录分支后保存实际章节，排除目录分组和其他分支', (tester) async {
    final planMap = <String, dynamic>{
      ...planFixture,
      'scope': {
        'kind': 'selected',
        'course_ids': ['c1'],
        'chapters': <Map<String, dynamic>>[],
      },
    };
    Map<String, dynamic>? saved;
    final server = TestServer(overrides: {
      'GET /api/v1/courses/c1': (_) async => jsonResponse(courseOutlineFixture),
      'GET /api/v1/plans/p1': (_) async =>
          jsonResponse(planMap, headers: {'etag': '"v3"'}),
      'PATCH /api/v1/plans/p1': (request) async {
        saved = jsonDecode(request.body) as Map<String, dynamic>;
        return jsonResponse({...planMap, 'version': 4},
            headers: {'etag': '"v4"'});
      },
    });
    await pumpTestPage(
        tester, PlanEditorSheet(plan: LearningPlan.parse(planMap)),
        handler: server.call);
    final group = find.widgetWithText(CheckboxListTile, '1 课程导学');
    await tester.ensureVisible(group);
    await tester.tap(group);
    await settle(tester);
    expect(tester.widget<CheckboxListTile>(group).value, isTrue);
    await tester.tap(find.text('保存安排'));
    await settle(tester);
    expect(saved, isNotNull);
    final scope = saved!['scope'] as Map<String, dynamic>;
    final selected = (scope['chapters'] as List)
        .cast<Map<String, dynamic>>()
        .map((chapter) => chapter['chapter_id'])
        .toSet();
    expect(
        selected, {'chapter-900', 'chapter-100', 'chapter-700', 'chapter-50'});
    expect(tester.takeException(), isNull);
  });
}
