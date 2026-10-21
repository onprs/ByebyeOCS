import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/core/models/plan.dart';
import 'package:byebyeocs_client/features/plans/plan_editor.dart';

import '../helpers.dart';

void main() {
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
  testWidgets('安排编辑载入已发布模型与作答模式，不显示自动提交能力', (tester) async {
    final planMap = Map<String, dynamic>.from(planFixture);
    planMap['question_policy'] = {
      'enabled': true,
      'model_offering_id': 'qm1',
      'answer_mode': 'always_llm',
      'require_manual_confirmation': true,
      'execution_available': false,
      'submission_available': false,
    };
    final plan = LearningPlan.parse(planMap);
    final server = TestServer(
      overrides: {
        'GET /api/v1/plans/p1': (_) async => jsonResponse(
              planMap,
              headers: {'etag': '"v3"'},
            ),
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
    expect(find.textContaining('固定开启'), findsOneWidget);
    expect(find.textContaining('当前版本不会自动答题，也不会自动提交'), findsOneWidget);
    expect(find.textContaining('自动提交已具备'), findsNothing);
  });
}
