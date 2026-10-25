import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/core/models/plan.dart';
import 'package:byebyeocs_client/features/plans/plan_editor.dart';

import '../helpers.dart';

void main() {
  for (final width in [390.0, 1440.0]) {
    testWidgets('${width.toInt()}px 安排编辑保留草稿，键盘出现后保存入口可见', (tester) async {
      final plan = LearningPlan.parse(Map<String, dynamic>.from(planFixture));
      await pumpTestPage(
        tester,
        Builder(
            builder: (context) => FilledButton(
                  onPressed: () => showPlanEditor(context, plan: plan),
                  child: const Text('编辑安排'),
                )),
        size: Size(width, 900),
      );
      await tester.tap(find.text('编辑安排'));
      await settle(tester);
      expect(find.byType(BottomSheet),
          width < 760 ? findsOneWidget : findsNothing);
      final editor = tester.getRect(find.byType(PlanEditorSheet));
      expect(editor.right, closeTo(width, 1));
      await tester.enterText(find.byType(TextField).first, '每周课程安排');
      tester.view.viewInsets = const FakeViewPadding(bottom: 260);
      addTearDown(tester.view.resetViewInsets);
      await settle(tester);
      expect(find.text('每周课程安排'), findsOneWidget);
      final save = find.ancestor(
        of: find.text('保存安排'),
        matching: find.byType(FilledButton),
      );
      final bounds = tester.getRect(save);
      expect(bounds.top, greaterThan(0));
      expect(bounds.bottom, lessThanOrEqualTo(641));
      await tester.tap(find.byTooltip('关闭'));
      await settle(tester);
      expect(find.byType(PlanEditorSheet), findsNothing);
    });
  }

  testWidgets('编辑面板内显示 ETag 版本冲突且不覆盖草稿', (tester) async {
    final plan = LearningPlan.parse(Map<String, dynamic>.from(planFixture));
    final server = TestServer(
      overrides: {
        'PATCH /api/v1/plans/p1': (_) async => jsonResponse(
              {
                'error': {
                  'code': 'version_conflict',
                  'message': '安排已更新，请重新加载后编辑。',
                  'request_id': 'req_conflict',
                  'retryable': false,
                  'scope': 'request',
                },
              },
              status: 412,
            ),
      },
    );

    await pumpTestPage(
      tester,
      PlanEditorSheet(plan: plan, hasActiveTasks: true),
      handler: server.call,
    );

    expect(find.text('编辑任务安排'), findsOneWidget);
    expect(find.textContaining('当前任务会保持本轮设置'), findsOneWidget);
    expect(find.text('保存安排'), findsOneWidget);

    await tester.tap(find.text('保存安排'));
    await settle(tester);

    expect(
      find.textContaining('安排已在其他位置更新'),
      findsOneWidget,
    );
    expect(find.text('载入最新版本'), findsOneWidget);
    expect(find.text('设计思维与创新方法'), findsWidgets);
  });
  testWidgets('安排编辑按自动答题展示策略，旧人工确认字段不再生效', (tester) async {
    final planMap = Map<String, dynamic>.from(planFixture);
    planMap['question_policy'] = {
      'enabled': true,
      'model_offering_id': 'qm1',
      'answer_mode': 'always_llm',
      'require_manual_confirmation': true,
      'execution_available': false,
      'submission_available': false,
    };
    Map<String, dynamic>? savedBody;
    final plan = LearningPlan.parse(planMap);
    final server = TestServer(
      overrides: {
        'GET /api/v1/plans/p1': (_) async => jsonResponse(
              planMap,
              headers: {'etag': '"v3"'},
            ),
        'PATCH /api/v1/plans/p1': (request) async {
          savedBody = jsonDecode(request.body) as Map<String, dynamic>;
          return jsonResponse(
            {
              ...planMap,
              'version': 4,
              'current_revision_id': 'r2',
            },
            headers: {'etag': '"v4"'},
          );
        },
      },
    );

    await pumpTestPage(
      tester,
      PlanEditorSheet(plan: plan),
      handler: server.call,
    );

    expect(find.text('题库策略'), findsOneWidget);
    expect(find.text('标准模型 · 文本 · 单价待公布'), findsOneWidget);
    expect(find.text('always_llm'), findsOneWidget);
    expect(find.textContaining('开启后自动查询并提交测验答案'), findsOneWidget);
    expect(find.textContaining('提交结果以平台复核为准'), findsOneWidget);
    expect(find.textContaining('人工确认'), findsNothing);
    expect(find.textContaining('不会自动'), findsNothing);

    await tester.tap(find.text('保存安排'));
    await settle(tester);

    final policy = savedBody!['question_policy'] as Map<String, dynamic>;
    expect(policy['enabled'], isTrue);
    expect(policy['model_offering_id'], 'qm1');
    expect(policy['answer_mode'], 'always_llm');
    expect(policy['require_manual_confirmation'], isFalse);
  });
}
