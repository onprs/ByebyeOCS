import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:byebyeocs_client/app/providers.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/models/auth.dart';
import 'package:byebyeocs_client/core/models/capabilities.dart';
import 'package:byebyeocs_client/core/models/preferences.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/design/theme.dart';
import 'package:byebyeocs_client/features/auth/auth_controller.dart';

typedef RequestHandler = Future<http.Response> Function(http.Request request);

const testUser = UserProfile(
  id: '11111111-1111-4111-8111-111111111111',
  status: 'active',
  displayName: '沈知夏',
  school: '海岚大学',
  provider: 'chaoxing',
  platform: PlatformSessionInfo(state: 'active'),
  session: SessionInfo(
    channel: 'web',
    fresh: true,
  ),
);

const testCapabilities = Capabilities(
  apiVersion: 'v1',
  auth: AuthCapability(
    provider: 'chaoxing',
    methods: ['password'],
    verification: 'configured',
  ),
  features: {
    'course_sync': true,
    'scheduling': true,
    'task_execution': true,
    'wallet': true,
    'payments': true,
    'tiku': true,
    'events': true,
    'preferences': true,
  },
  pricing: PricingCapability(
    serviceConfigured: false,
    tikuConfigured: false,
    automaticCharging: false,
  ),
  taskTypes: [
    TaskTypeCapability(id: 'video', name: '视频'),
    TaskTypeCapability(id: 'document', name: '文档'),
    TaskTypeCapability(id: 'quiz', name: '测验'),
  ],
);

AuthState authenticatedState({
  UserPreferences preferences = const UserPreferences(),
}) {
  return AuthState(
    status: AuthStatus.authenticated,
    user: testUser,
    capabilities: testCapabilities,
    preferences: preferences,
  );
}

AuthState anonymousState({
  Capabilities capabilities = testCapabilities,
  String? notice,
}) {
  return AuthState(
    status: AuthStatus.anonymous,
    capabilities: capabilities,
    notice: notice,
  );
}

class FakeAuthController extends AuthController {
  FakeAuthController(this.initial);

  final AuthState initial;

  @override
  AuthState build() => initial;
}

class TestServer {
  TestServer({Map<String, RequestHandler>? overrides})
      : _overrides = overrides ?? const {};

  final Map<String, RequestHandler> _overrides;

  Future<http.Response> call(http.Request request) async {
    final key = '${request.method} ${request.url.path}';
    final override = _overrides[key] ?? _overrides[request.url.path];
    if (override != null) return override(request);
    return switch (request.url.path) {
      '/api/v1/courses' => jsonResponse(coursesFixture),
      '/api/v1/courses/c1' => jsonResponse(courseDetailFixture),
      '/api/v1/course-syncs' => jsonResponse(
          {
            'id': 'sync1',
            'status': 'queued',
            'created': true,
            'course_count': 0,
          },
          status: 202,
        ),
      '/api/v1/course-syncs/latest' => jsonResponse(_latestCourseSyncFixture()),
      '/api/v1/plans' => jsonResponse({
          'items': [planFixture],
          'next_cursor': '',
        }),
      '/api/v1/plans/p1' => jsonResponse(
          planFixture,
          headers: {'etag': '"v3"'},
        ),
      '/api/v1/tasks' => jsonResponse({
          'items': taskFixtures,
          'next_cursor': '',
        }),
      '/api/v1/wallet' => jsonResponse(walletFixture),
      '/api/v1/pricing' => jsonResponse(pricingFixture),
      '/api/v1/wallet/entries' => jsonResponse({
          'items': entryFixtures,
          'next_cursor': '',
        }),
      '/api/v1/recharge-orders' => jsonResponse({
          'items': orderFixtures,
          'next_cursor': '',
        }),
      '/api/v1/usage' => jsonResponse({
          'items': usageFixtures,
          'next_cursor': '',
        }),
      '/api/v1/question-queries' => jsonResponse({
          'items': questionQueryFixtures,
          'next_cursor': '',
        }),
      '/api/v1/question-models' => jsonResponse({
          'items': questionModelFixtures,
          'next_cursor': '',
        }),
      '/api/v1/me/data-exports' => jsonResponse({
          'items': dataExportFixtures,
          'next_cursor': '',
        }),
      '/api/v1/me/deletion-requests' => jsonResponse({
          'items': deletionRequestFixtures,
          'next_cursor': '',
        }),
      '/api/v1/me/preferences' => jsonResponse(
          preferencesFixture,
          headers: {'etag': '"v1"'},
        ),
      '/api/v1/auth/session' => jsonResponse({
          'authenticated': false,
          'kind': 'user',
          'capabilities': {},
        }),
      _ => jsonResponse(
          {
            'error': {
              'code': 'not_found',
              'message': '测试接口不存在',
              'request_id': 'req_test',
              'retryable': false,
              'scope': 'request',
            },
          },
          status: 404,
        ),
    };
  }
}

http.Response jsonResponse(
  Object? body, {
  int status = 200,
  Map<String, String> headers = const {},
}) {
  return http.Response(
    jsonEncode(body),
    status,
    headers: {
      'content-type': 'application/json; charset=utf-8',
      ...headers,
    },
  );
}

/// 默认提供最近成功的同步，避免页面测试因课程样例时间而过期触发同步轮询。
Map<String, Object?> _latestCourseSyncFixture() {
  final finishedAt = DateTime.now().toUtc().subtract(
        const Duration(minutes: 2),
      );
  return {
    'id': 'sync-latest',
    'status': 'succeeded',
    'course_count': coursesFixture.length,
    'requested_at':
        finishedAt.subtract(const Duration(minutes: 1)).toIso8601String(),
    'finished_at': finishedAt.toIso8601String(),
  };
}

Future<void> pumpTestPage(
  WidgetTester tester,
  Widget page, {
  AuthState? auth,
  RequestHandler? handler,
  Size size = const Size(1200, 1600),
}) async {
  SharedPreferences.setMockInitialValues({});
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });
  final server = TestServer();
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        appConfigProvider.overrideWithValue(
          const AppConfig(
            apiBaseUri: null,
            channel: ClientChannel.web,
          ).copyWithBase(Uri.parse('https://api.test/api/v1/')),
        ),
        tokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        httpClientProvider.overrideWithValue(
          MockClient(handler ?? server.call),
        ),
        authControllerProvider.overrideWith(
          () => FakeAuthController(auth ?? authenticatedState()),
        ),
      ],
      child: MaterialApp(
        theme: AppTheme.build(ColorSchemeId.graphite, Brightness.light),
        darkTheme: AppTheme.build(ColorSchemeId.graphite, Brightness.dark),
        themeMode: ThemeMode.light,
        home: Scaffold(
          body: SafeArea(child: page),
        ),
      ),
    ),
  );
  await settle(tester);
}

extension on AppConfig {
  AppConfig copyWithBase(Uri base) =>
      AppConfig(apiBaseUri: base, channel: channel);
}

Future<void> settle(WidgetTester tester) async {
  for (var index = 0; index < 12; index++) {
    await tester.pump(const Duration(milliseconds: 80));
  }
}

final coursesFixture = [
  {
    'id': 'c1',
    'title': '设计思维与创新方法',
    'teacher': '林知远',
    'term': '秋季学期',
    'chapter_count': 2,
    'task_point_count': 4,
    'incomplete_task_point_count': 2,
    'resource_count': 1,
    'snapshot_version': 1,
    'synced_at': '2026-10-20T06:28:00Z',
    'stale': false,
  },
  {
    'id': 'c2',
    'title': '数据结构与算法',
    'teacher': '陈序',
    'term': '秋季学期',
    'chapter_count': 3,
    'task_point_count': 6,
    'incomplete_task_point_count': 0,
    'resource_count': 0,
    'snapshot_version': 1,
    'synced_at': '2026-10-20T06:28:00Z',
    'stale': false,
  },
];

final courseDetailFixture = {
  ...coursesFixture.first,
  'chapters': [
    {
      'id': 'ch1',
      'external_id': '1',
      'title': '认识设计，认识问题',
      'sort_order': 0,
      'attachments': [
        {
          'id': 'a1',
          'object_id': 'obj1',
          'task_point_id': 'tp1',
          'kind': 'video',
          'title': '1.1 发现真正的问题',
          'duration_seconds': 900,
          'played_seconds': 120,
          'platform_passed': true,
          'passed_known': true,
          'is_task_point': true,
          'counted_incomplete': false,
          'progress_checked_at': '2026-10-20T06:28:00Z',
        },
        {
          'id': 'a2',
          'object_id': 'obj2',
          'kind': 'document',
          'title': '拓展阅读 · 设计访谈资料',
          'duration_seconds': 0,
          'played_seconds': 0,
          'platform_passed': false,
          'passed_known': false,
          'is_task_point': false,
          'counted_incomplete': false,
        },
      ],
    },
    {
      'id': 'ch2',
      'external_id': '2',
      'title': '设计的基本语言',
      'sort_order': 1,
      'attachments': [
        {
          'id': 'a3',
          'object_id': 'obj3',
          'task_point_id': 'tp2',
          'kind': 'video',
          'title': '2.1 从观察到洞察',
          'duration_seconds': 1200,
          'played_seconds': 0,
          'platform_passed': false,
          'passed_known': true,
          'is_task_point': true,
          'counted_incomplete': true,
        },
      ],
    },
  ],
};

const planFixture = {
  'id': 'p1',
  'name': '本周学习安排',
  'status': 'active',
  'version': 3,
  'priority': 50,
  'task_types': ['video', 'quiz'],
  'scope': {
    'kind': 'all',
    'course_ids': ['c1'],
    'chapters': [],
  },
  'question_policy': {
    'enabled': false,
    'model_offering_id': null,
    'answer_mode': 'cache_first',
    'require_manual_confirmation': true,
    'execution_available': false,
    'submission_available': false,
  },
  'current_revision_id': 'r1',
  'created_at': '2026-10-19T02:00:00Z',
  'updated_at': '2026-10-20T02:00:00Z',
};

const taskFixtures = [
  {
    'id': 't1',
    'state': 'running',
    'desired_state': 'run',
    'version': 2,
    'priority': 50,
    'task_kind': 'video',
    'title': '1.1 发现真正的问题',
    'course_id': 'c1',
    'course_title': '设计思维与创新方法',
    'chapter_id': 'ch1',
    'chapter_title': '认识设计，认识问题',
    'plan_refs': [
      {'plan_id': 'p1', 'revision_id': 'r1', 'version': 3},
    ],
    'attempt_count': 1,
    'failure_count': 0,
    'verification_attempts': 0,
    'platform': {'status': 'unconfirmed', 'passed': false},
    'observation': {
      'fraction': 0.64,
      'source': 'executor',
      'observed_at': '2026-10-20T06:30:00Z',
    },
    'media_evidence': {'available': false},
    'billing': {'state': 'disabled', 'configured': false},
    'estimate': {
      'remaining_seconds_min': 300,
      'remaining_seconds_max': 600,
      'estimated': true,
    },
    'created_at': '2026-10-20T06:20:00Z',
    'updated_at': '2026-10-20T06:30:00Z',
  },
  {
    'id': 't2',
    'state': 'waiting_verification',
    'desired_state': 'run',
    'version': 3,
    'priority': 50,
    'task_kind': 'video',
    'title': '2.1 从观察到洞察',
    'course_id': 'c1',
    'course_title': '设计思维与创新方法',
    'chapter_id': 'ch2',
    'chapter_title': '设计的基本语言',
    'plan_refs': [
      {'plan_id': 'p1', 'revision_id': 'r1', 'version': 3},
    ],
    'attempt_count': 1,
    'platform': {'status': 'unconfirmed', 'passed': false},
    'observation': {
      'fraction': 1.0,
      'source': 'executor',
      'observed_at': '2026-10-20T06:35:00Z',
    },
    'media_evidence': {'available': true, 'detail': '播放器记录 20 分钟'},
    'billing': {'state': 'disabled', 'configured': false},
    'estimate': {'estimated': false},
    'created_at': '2026-10-20T06:20:00Z',
    'updated_at': '2026-10-20T06:35:00Z',
  },
  {
    'id': 't3',
    'state': 'confirmed',
    'desired_state': 'run',
    'version': 5,
    'task_kind': 'video',
    'title': '3.1 发现真正的问题',
    'course_id': 'c1',
    'course_title': '设计思维与创新方法',
    'chapter_title': '认识设计，认识问题',
    'plan_refs': [
      {'plan_id': 'p1', 'revision_id': 'r1', 'version': 3},
    ],
    'platform': {
      'status': 'confirmed',
      'passed': true,
      'checked_at': '2026-10-20T06:32:46Z',
    },
    'media_evidence': {'available': false},
    'billing': {'state': 'disabled', 'configured': false},
    'estimate': {'estimated': false},
    'created_at': '2026-10-20T06:20:00Z',
    'updated_at': '2026-10-20T06:32:46Z',
  },
];

const walletFixture = {
  'id': 'w1',
  'currency': 'CNY',
  'available_minor': '5450',
  'reserved_minor': '300',
  'tiku_accrued_micros': '0',
  'frozen': false,
  'version': 4,
  'updated_at': '2026-10-20T06:40:00Z',
};

const pricingFixture = {
  'currency': 'CNY',
  'automatic_charging': false,
  'service': {
    'scope': 'service_task_point',
    'configured': false,
    'state': 'unconfigured',
  },
  'tiku': {
    'scope': 'tiku_tokens',
    'configured': false,
    'state': 'unconfigured',
  },
  'recharge': {
    'provider': 'zpay',
    'enabled': true,
    'state': 'available',
    'channels': ['alipay', 'wxpay'],
    'default_channel': 'alipay',
    'min_minor': '100',
    'max_minor': '100000',
  },
};

const entryFixtures = [
  {
    'journal_id': 'j1',
    'kind': 'recharge',
    'reason': 'ZPay 充值到账',
    'available_delta_minor': '3000',
    'reserved_delta_minor': '0',
    'order_id': 'o1',
    'created_at': '2026-10-20T02:42:00Z',
  },
  {
    'journal_id': 'j2',
    'kind': 'hold',
    'reason': '预占余额',
    'available_delta_minor': '-300',
    'reserved_delta_minor': '300',
    'created_at': '2026-10-20T03:00:00Z',
  },
];

const orderFixtures = [
  {
    'id': 'o1',
    'amount_minor': '3000',
    'currency': 'CNY',
    'channel': 'alipay',
    'status': 'completed',
    'payment_status': 'paid',
    'credit_status': 'credited',
    'provider': 'zpay',
    'merchant_order_no': 'm1',
    'late_payment': false,
    'created_at': '2026-10-20T02:40:00Z',
    'updated_at': '2026-10-20T02:42:00Z',
    'expires_at': '2026-10-20T03:10:00Z',
    'paid_at': '2026-10-20T02:41:00Z',
    'credited_at': '2026-10-20T02:42:00Z',
    'version': 3,
    'checkout_state': 'not_available',
  },
  {
    'id': 'o2',
    'amount_minor': '5000',
    'currency': 'CNY',
    'channel': 'wxpay',
    'status': 'pending_payment',
    'payment_status': 'paid',
    'credit_status': 'not_credited',
    'provider': 'zpay',
    'merchant_order_no': 'm2',
    'late_payment': false,
    'created_at': '2026-10-20T02:30:00Z',
    'updated_at': '2026-10-20T02:31:00Z',
    'expires_at': '2026-10-20T03:00:00Z',
    'paid_at': '2026-10-20T02:31:00Z',
    'version': 2,
    'checkout_state': 'not_available',
  },
  {
    'id': 'o3',
    'amount_minor': '1000',
    'currency': 'CNY',
    'channel': 'alipay',
    'status': 'pending_payment',
    'payment_status': 'unpaid',
    'credit_status': 'not_credited',
    'provider': 'zpay',
    'merchant_order_no': 'm3',
    'late_payment': false,
    'created_at': '2026-10-20T02:20:00Z',
    'updated_at': '2026-10-20T02:20:00Z',
    'expires_at': '2026-10-20T02:50:00Z',
    'version': 1,
    'checkout_state': 'available',
    'checkout_url': 'https://pay.example.test/checkout',
  },
];

const usageFixtures = [
  {
    'id': 'u1',
    'scope': 'tiku_tokens',
    'status': 'unpriced',
    'input_tokens': 1284,
    'output_tokens': 96,
    'total_tokens': 1380,
    'usage_complete': true,
    'quote_micros': '0',
    'quote_minor': '0',
    'cost_micros': '0',
    'charged_minor': '0',
    'created_at': '2026-10-20T05:00:00Z',
  },
];

const questionQueryFixtures = [
  {
    'id': 'q1',
    'status': 'answered',
    'model': {'offering_id': 'qm1', 'display_name': '标准模型'},
    'mode': 'cache_first',
    'cache': {'hit': true, 'created_at': '2026-10-19T08:00:00Z'},
    'answer': 'A',
    'question': {
      'title': '下列说法中正确的是？',
      'options': 'A. ...\nB. ...',
      'type': 'single',
      'images': [],
    },
    'usage': {
      'input_tokens': 0,
      'output_tokens': 0,
      'total_tokens': 0,
      'complete': true,
      'calls': [],
    },
    'pricing': {'state': 'unpriced'},
    'created_at': '2026-10-20T05:00:00Z',
  },
];

const questionModelFixtures = [
  {
    'id': 'qm1',
    'display_name': '标准模型',
    'description': '日常选择题与简答题。',
    'capabilities': ['text'],
    'default': true,
    'sort_order': 1,
    'price_version': '',
    'cache_policy': {'allow': true, 'scope': 'user', 'ttl_days': 30},
    'health': 'healthy',
  },
];

const preferencesFixture = {
  'color_scheme': 'graphite',
  'theme_mode': 'system',
  'notifications': {
    'task_attention': true,
    'wallet_changes': true,
  },
  'version': 1,
};

const dataExportFixtures = [
  {
    'id': 'de1',
    'kind': 'export',
    'status': 'ready',
    'created_at': '2026-10-21T06:00:00Z',
    'updated_at': '2026-10-21T06:05:00Z',
    'expires_at': '2026-10-22T06:05:00Z',
    'review_reason': '',
    'error_code': '',
  },
  {
    'id': 'de2',
    'kind': 'export',
    'status': 'processing',
    'created_at': '2026-10-21T06:10:00Z',
    'updated_at': '2026-10-21T06:10:00Z',
  },
];

const deletionRequestFixtures = [
  {
    'id': 'dr1',
    'kind': 'deletion',
    'status': 'needs_review',
    'created_at': '2026-10-21T06:00:00Z',
    'updated_at': '2026-10-21T06:01:00Z',
    'review_reason': '资金与保留记录待核对',
  },
];
