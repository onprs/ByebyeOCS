import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/design/components.dart';
import 'package:byebyeocs_client/features/questions/question_query_page.dart';
import 'package:byebyeocs_client/features/questions/question_widgets.dart';
import 'package:byebyeocs_client/core/models/question.dart';

import '../helpers.dart';

const _answeredQuery = {
  'id': 'q1',
  'status': 'answered',
  'model': {'offering_id': 'qm1', 'display_name': '标准模型'},
  'mode': 'cache_first',
  'cache': {'hit': false},
  'answer': 'B. 第二项',
  'question': {
    'title': '下列说法中正确的是？',
    'options': 'A. 第一项\nB. 第二项',
    'type': 'single',
    'image_count': 0,
  },
  'usage': {
    'input_tokens': 120,
    'output_tokens': 10,
    'total_tokens': 130,
    'complete': true,
    'calls': [],
  },
  'pricing': {
    'state': 'quoted',
    'cost_micros': '1300',
    'amount_minor': '1',
  },
  'created_at': '2026-10-20T05:00:00Z',
};

const _pendingQuery = {
  'id': 'q1',
  'status': 'pending',
  'model': {'offering_id': 'qm1', 'display_name': '标准模型'},
  'mode': 'cache_first',
  'cache': {'hit': false},
  'question': {
    'title': '下列说法中正确的是？',
    'options': 'A. 第一项\nB. 第二项',
    'type': 'single',
    'image_count': 0,
  },
  'usage': {'complete': false, 'calls': []},
  'pricing': {'state': 'unpriced'},
  'created_at': '2026-10-20T05:00:00Z',
};

void main() {
  testWidgets('问题报告在页内展开，失败保留草稿，成功后收起', (tester) async {
    var attempts = 0;
    await pumpTestPage(
        tester,
        QuestionResultCard(
          query: QuestionQuery.parse(Map<String, dynamic>.from(_answeredQuery)),
          reporting: false,
          onReport: (reason) async {
            attempts++;
            return attempts > 1;
          },
        ));
    await tester.tap(find.widgetWithText(AsyncButton, '报告答案问题'));
    await settle(tester);
    expect(find.byType(AlertDialog), findsNothing);
    await tester.enterText(find.byType(TextField), '答案与题目要求不符');
    await tester.tap(find.widgetWithText(AsyncButton, '提交'));
    await settle(tester);
    expect(find.text('答案与题目要求不符'), findsOneWidget);
    await tester.tap(find.widgetWithText(AsyncButton, '提交'));
    await settle(tester);
    expect(attempts, 2);
    expect(find.byType(TextField), findsNothing);
  });

  testWidgets('独立题库查询提交题目并展示答案，可报告问题', (tester) async {
    Map<String, dynamic>? queryBody;
    Map<String, dynamic>? reportBody;
    String? idempotencyKey;
    final server = TestServer(
      overrides: {
        'POST /api/v1/question-queries': (request) async {
          queryBody = jsonDecode(request.body) as Map<String, dynamic>;
          idempotencyKey = request.headers['Idempotency-Key'];
          return jsonResponse(_answeredQuery);
        },
        'POST /api/v1/question-cache-reports': (request) async {
          reportBody = jsonDecode(request.body) as Map<String, dynamic>;
          return jsonResponse(
            {
              'id': 'r1',
              'status': 'pending',
              'created_at': '2026-10-20T05:01:00Z',
            },
            status: 201,
          );
        },
      },
    );

    await pumpTestPage(tester, const QuestionQueryPage(), handler: server.call);

    expect(find.text('按题查询答案'), findsOneWidget);
    expect(find.textContaining('标准模型'), findsOneWidget);

    final fields = find.byType(TextField);
    await tester.enterText(fields.at(0), '下列说法中正确的是？');
    await tester.enterText(fields.at(1), 'A. 第一项\nB. 第二项');
    await tester.tap(find.widgetWithText(AsyncButton, '查询答案'));
    await settle(tester);

    expect(queryBody?['model_offering_id'], 'qm1');
    expect(queryBody?['mode'], 'cache_first');
    expect(queryBody?['question'], {
      'title': '下列说法中正确的是？',
      'options': 'A. 第一项\nB. 第二项',
      'type': 'single',
      'images': [],
    });
    expect((queryBody?['budget'] as Map<String, dynamic>)['max_output_tokens'],
        1024);
    expect(idempotencyKey, isNotNull);
    expect(idempotencyKey, isNotEmpty);
    expect(find.text('B. 第二项'), findsOneWidget);
    expect(find.text('答案已返回'), findsOneWidget);

    await tester.tap(find.widgetWithText(AsyncButton, '报告答案问题'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).last, '答案与题目不符');
    await tester.tap(find.widgetWithText(FilledButton, '提交'));
    await settle(tester);

    expect(reportBody, {'query_id': 'q1', 'reason': '答案与题目不符'});
    expect(find.text('问题已提交复核'), findsOneWidget);
  });

  testWidgets('未发布模型时不提供查询动作', (tester) async {
    final server = TestServer(
      overrides: {
        'GET /api/v1/question-models': (_) async => jsonResponse({
              'items': <Object>[],
              'next_cursor': null,
            }),
      },
    );

    await pumpTestPage(tester, const QuestionQueryPage(), handler: server.call);

    expect(find.text('管理员尚未发布可用模型'), findsOneWidget);
    expect(find.widgetWithText(AsyncButton, '查询答案'), findsNothing);
  });

  testWidgets('查询失败后相同输入重试复用幂等键', (tester) async {
    final keys = <String>[];
    var calls = 0;
    final server = TestServer(
      overrides: {
        'POST /api/v1/question-queries': (request) async {
          calls++;
          final key = request.headers['Idempotency-Key'];
          if (key != null) keys.add(key);
          if (calls == 1) {
            return jsonResponse(
              {
                'error': {
                  'code': 'service_unavailable',
                  'message': '服务暂不可用，请稍后重试',
                  'request_id': 'req_test',
                  'retryable': true,
                  'scope': 'request',
                },
              },
              status: 503,
            );
          }
          return jsonResponse(_answeredQuery);
        },
      },
    );

    await pumpTestPage(tester, const QuestionQueryPage(), handler: server.call);
    await tester.enterText(find.byType(TextField).at(0), '重试幂等题');
    await tester.tap(find.widgetWithText(AsyncButton, '查询答案'));
    await settle(tester);
    expect(find.text('服务暂不可用，请稍后重试'), findsOneWidget);

    await tester.tap(find.widgetWithText(AsyncButton, '查询答案'));
    await settle(tester);

    expect(keys, hasLength(2));
    expect(keys.first, keys.last);
    expect(find.text('B. 第二项'), findsOneWidget);
  });

  testWidgets('模型未启用缓存时按每次调用模型提交', (tester) async {
    Map<String, dynamic>? queryBody;
    final server = TestServer(
      overrides: {
        'GET /api/v1/question-models': (_) async => jsonResponse({
              'items': [
                {
                  'id': 'qm1',
                  'display_name': '无缓存模型',
                  'capabilities': ['text'],
                  'default': true,
                  'sort_order': 1,
                  'cache_policy': {
                    'allow': false,
                    'scope': 'user',
                    'ttl_days': 7,
                  },
                  'health': 'healthy',
                },
              ],
              'next_cursor': null,
            }),
        'POST /api/v1/question-queries': (request) async {
          queryBody = jsonDecode(request.body) as Map<String, dynamic>;
          return jsonResponse(_answeredQuery);
        },
      },
    );

    await pumpTestPage(tester, const QuestionQueryPage(), handler: server.call);
    expect(find.text('该模型未启用答案缓存，每次都会调用模型。'), findsOneWidget);
    await tester.enterText(find.byType(TextField).at(0), '无缓存题目');
    await tester.tap(find.widgetWithText(AsyncButton, '查询答案'));
    await settle(tester);

    expect(queryBody?['mode'], 'always_llm');
  });

  testWidgets('待处理查询可刷新结果', (tester) async {
    final server = TestServer(
      overrides: {
        'POST /api/v1/question-queries': (_) async =>
            jsonResponse(_pendingQuery),
        'GET /api/v1/question-queries/q1': (_) async =>
            jsonResponse(_answeredQuery),
      },
    );

    await pumpTestPage(tester, const QuestionQueryPage(), handler: server.call);
    await tester.enterText(find.byType(TextField).at(0), '待处理题目');
    await tester.tap(find.widgetWithText(AsyncButton, '查询答案'));
    await settle(tester);

    expect(find.text('答案仍在处理中'), findsOneWidget);
    expect(find.widgetWithText(AsyncButton, '刷新结果'), findsOneWidget);

    await tester.tap(find.widgetWithText(AsyncButton, '刷新结果'));
    await settle(tester);

    expect(find.text('B. 第二项'), findsOneWidget);
    expect(find.text('答案已刷新'), findsOneWidget);
  });
}
