import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/core/api/api_response.dart';
import 'package:byebyeocs_client/core/format/formatters.dart';
import 'package:byebyeocs_client/core/models/plan.dart';
import 'package:byebyeocs_client/core/models/wallet.dart';

void main() {
  group('资金格式与状态', () {
    test('整数分格式化为人民币，不做浮点换算', () {
      expect(formatMinor(5450), '54.50');
      expect(formatMinor(0), '0.00');
      expect(formatMinor(-350), '-3.50');
      expect(formatMinor(123456789), '1,234,567.89');
      expect(const Money(5450).text, '￥54.50');
      expect(const Money(-350).text, '-￥3.50');
      expect(const Money(100).signedText, '+￥1.00');
    });

    test('充值输入只接受最多两位小数', () {
      expect(parseYuanInput('30'), 3000);
      expect(parseYuanInput('30.5'), 3050);
      expect(parseYuanInput('30.50'), 3050);
      expect(parseYuanInput('30.555'), isNull);
      expect(parseYuanInput('0'), 0);
      expect(parseYuanInput('abc'), isNull);
    });

    test('订单支付与到账状态独立', () {
      final pending = RechargeOrder.parse({
        'id': 'o1',
        'amount_minor': '1000',
        'status': 'pending_payment',
        'payment_status': 'unpaid',
        'credit_status': 'not_credited',
      });
      expect(pending.stage, RechargeStage.pendingPayment);
      expect(pending.stageLabel, '待支付');

      final paid = RechargeOrder.parse({
        'id': 'o2',
        'amount_minor': '1000',
        'status': 'pending_payment',
        'payment_status': 'paid',
        'credit_status': 'not_credited',
      });
      expect(paid.stage, RechargeStage.paidWaitingCredit);
      expect(paid.stageLabel, '支付成功 · 入账处理中');
      expect(paid.amount.text, '￥10.00');

      final credited = RechargeOrder.parse({
        'id': 'o3',
        'amount_minor': '1000',
        'status': 'completed',
        'payment_status': 'paid',
        'credit_status': 'credited',
      });
      expect(credited.stage, RechargeStage.credited);
      expect(credited.stageLabel, '已到账');

      final review = RechargeOrder.parse({
        'id': 'o4',
        'amount_minor': '1000',
        'status': 'needs_review',
        'payment_status': 'unknown',
        'credit_status': 'needs_review',
      });
      expect(review.stage, RechargeStage.needsReview);

      final refunded = RechargeOrder.parse({
        'id': 'o5',
        'amount_minor': '1000',
        'status': 'completed',
        'payment_status': 'refunded',
        'credit_status': 'credited',
      });
      expect(refunded.stage, RechargeStage.refunded);
      expect(refunded.stageLabel, '已退款');
    });
  });

  group('版本与安排解析', () {
    test('ETag 解析为版本号', () {
      expect(parseVersion('"v3"'), 3);
      expect(parseVersion('v12'), 12);
      expect(parseVersion('7'), 7);
      expect(parseVersion(null), isNull);
      expect(formatVersionEtag(3), '"v3"');
    });

    test('安排解析保留版本、范围与优先级', () {
      final plan = LearningPlan.parse({
        'id': 'p1',
        'name': '本周学习安排',
        'status': 'active',
        'version': 7,
        'priority': 80,
        'task_types': ['video', 'quiz'],
        'scope': {
          'kind': 'selected',
          'course_ids': ['c1'],
          'chapters': [
            {'course_id': 'c1', 'chapter_id': 'ch1'},
          ],
        },
      });
      expect(plan.version, 7);
      expect(plan.isActive, isTrue);
      expect(plan.priorityLabel, '优先处理');
      expect(plan.scope.isSelected, isTrue);
      expect(plan.scope.chapters.single.chapterId, 'ch1');
      expect(plan.questionPolicy.enabled, isFalse);
      expect(plan.questionPolicy.modelOfferingId, isNull);
    });

    test('安排题库策略按冻结字段解析与提交', () {
      final plan = LearningPlan.parse({
        'id': 'p1',
        'name': '题库安排',
        'status': 'active',
        'version': 8,
        'priority': 20,
        'task_types': ['quiz'],
        'scope': {
          'kind': 'all',
          'course_ids': ['c1'],
        },
        'question_policy': {
          'enabled': true,
          'model_offering_id': 'qm_standard',
          'answer_mode': 'always_llm',
          'require_manual_confirmation': true,
          'execution_available': false,
          'submission_available': false,
        },
      });
      expect(plan.questionPolicy.enabled, isTrue);
      expect(plan.questionPolicy.modelOfferingId, 'qm_standard');
      expect(plan.questionPolicy.answerMode, AnswerMode.alwaysLlm);
      expect(plan.questionPolicy.requireManualConfirmation, isFalse);

      const draft = PlanDraft(
        name: '题库安排',
        courseIds: ['c1'],
        taskTypes: ['quiz'],
        questionPolicy: QuestionPolicy(
          enabled: true,
          modelOfferingId: 'qm_standard',
          answerMode: AnswerMode.alwaysLlm,
        ),
      );
      final json = draft.toJson();
      final policy = json['question_policy'] as Map<String, dynamic>;
      expect(policy['enabled'], isTrue);
      expect(policy['model_offering_id'], 'qm_standard');
      expect(policy['answer_mode'], 'always_llm');
      expect(policy['require_manual_confirmation'], isFalse);
      expect(policy.containsKey('mode'), isFalse);
      expect(policy.containsKey('upstream'), isFalse);

      const missing = PlanDraft(
        name: '题库安排',
        courseIds: ['c1'],
        taskTypes: ['quiz'],
        questionPolicy: QuestionPolicy(enabled: true),
      );
      expect(missing.validate(), contains('开启题库策略时必须选择一个已发布模型'));
    });

    test('人工确认兼容字段始终按 false 提交并允许自动执行', () {
      const draft = PlanDraft(
        name: '自动答题安排',
        courseIds: ['c1'],
        taskTypes: ['quiz'],
        questionPolicy: QuestionPolicy(
          enabled: true,
          modelOfferingId: 'qm_standard',
          requireManualConfirmation: true,
        ),
      );
      final policy = draft.toJson()['question_policy'] as Map<String, dynamic>;
      expect(policy['require_manual_confirmation'], isFalse);
      expect(draft.validate(), isNot(contains('当前版本只支持提交前人工确认')));

      final missingField = LearningPlan.parse({
        'id': 'p1',
        'name': '自动答题安排',
        'status': 'active',
        'version': 1,
        'priority': 50,
        'scope': {
          'kind': 'all',
          'course_ids': ['c1']
        },
        'question_policy': {'enabled': true, 'model_offering_id': 'qm1'},
      });
      expect(missingField.questionPolicy.requireManualConfirmation, isFalse);
    });

    test('关闭题库策略时提交 null 模型且不要求人工确认', () {
      final json = const PlanDraft(
        name: '普通安排',
        courseIds: ['c1'],
        taskTypes: ['video'],
      ).toJson();
      final policy = json['question_policy'] as Map<String, dynamic>;
      expect(policy['enabled'], isFalse);
      expect(policy['model_offering_id'], isNull);
      expect(policy['answer_mode'], 'cache_first');
      expect(policy['require_manual_confirmation'], isFalse);
    });

    test('安排草稿校验章节与课程', () {
      const draft = PlanDraft(
        name: '英语复习',
        courseIds: ['c1'],
        scopeKind: 'selected',
      );
      expect(draft.validate(), contains('选择章节范围时，请至少勾选一个章节'));
    });
  });
}
