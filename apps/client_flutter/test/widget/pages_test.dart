import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/core/api/api_error.dart';
import 'package:byebyeocs_client/core/models/capabilities.dart';
import 'package:byebyeocs_client/design/components.dart';
import 'package:byebyeocs_client/features/auth/login_page.dart';
import 'package:byebyeocs_client/features/courses/course_detail_page.dart';
import 'package:byebyeocs_client/features/courses/courses_page.dart';
import 'package:byebyeocs_client/features/home/home_page.dart';
import 'package:byebyeocs_client/features/plans/plans_page.dart';
import 'package:byebyeocs_client/features/settings/connect_service_page.dart';
import 'package:byebyeocs_client/features/settings/settings_page.dart';
import 'package:byebyeocs_client/features/wallet/wallet_page.dart';

import '../helpers.dart';
import 'package:byebyeocs_client/features/auth/auth_controller.dart';

void main() {
  testWidgets('登录页只提供学习通身份，不出现平台注册入口', (tester) async {
    await pumpTestPage(tester, const LoginPage(), auth: anonymousState());

    expect(find.text('使用学习通身份进入'), findsOneWidget);
    expect(find.text('学校'), findsOneWidget);
    expect(find.text('学习通账号'), findsOneWidget);
    expect(find.text('密码'), findsOneWidget);
    expect(find.text('验证码（如平台要求）'), findsOneWidget);
    expect(find.text('验证并进入'), findsOneWidget);
    expect(find.text('学习通官方入口找回密码'), findsOneWidget);
    expect(find.widgetWithText(FilledButton, '注册'), findsNothing);
  });

  testWidgets('桌面登录表单垂直居中，品牌区使用大标题', (tester) async {
    await pumpTestPage(
      tester,
      const LoginPage(),
      auth: anonymousState(),
      size: const Size(1440, 900),
    );

    expect(find.text('让每一次学习\n都有据可依。'), findsOneWidget);
    expect(find.text('课程同步'), findsOneWidget);
    expect(find.text('任务安排'), findsOneWidget);
    expect(find.text('平台确认'), findsOneWidget);
    final form = tester.getRect(find.byType(Form));
    expect((form.top + form.bottom) / 2, closeTo(450, 60));
    expect(form.top, greaterThan(80));
  });

  testWidgets('手机登录页保留品牌标题且表单可滚动', (tester) async {
    await pumpTestPage(
      tester,
      const LoginPage(),
      auth: anonymousState(),
      size: const Size(390, 844),
    );

    expect(find.text('让每一次学习都有据可依。'), findsOneWidget);
    expect(find.text('使用学习通身份进入'), findsOneWidget);
    await tester.dragUntilVisible(
      find.text('学习通官方入口找回密码'),
      find.byType(SingleChildScrollView).first,
      const Offset(0, -220),
    );
    expect(find.text('学习通官方入口找回密码'), findsOneWidget);
  });

  testWidgets('学习通未配置时显示明确不可用状态', (tester) async {
    const unavailable = Capabilities(
      auth: AuthCapability(
        provider: 'chaoxing',
        methods: [],
        verification: 'disabled',
        enabled: false,
      ),
    );
    await pumpTestPage(
      tester,
      const LoginPage(),
      auth: anonymousState(capabilities: unavailable),
    );

    expect(find.text('学习通登录尚未开放'), findsOneWidget);
    expect(find.text('验证并进入'), findsNothing);
  });

  testWidgets('首页展示真实课程、钱包与任务摘要', (tester) async {
    await pumpTestPage(tester, const HomePage());

    expect(find.textContaining('沈知夏'), findsOneWidget);
    expect(find.text('我的钱包'), findsOneWidget);
    expect(find.text('¥54.50'), findsOneWidget);
    expect(find.text('设计思维与创新方法'), findsWidgets);
    expect(find.text('已同步课程'), findsOneWidget);
    expect(find.text('你的学习安排'), findsOneWidget);
    expect(find.text('最近确认'), findsOneWidget);
  });

  testWidgets('课程列表支持搜索与同步入口', (tester) async {
    await pumpTestPage(tester, const CoursesPage());

    expect(find.text('课程与任务点'), findsOneWidget);
    expect(find.text('设计思维与创新方法'), findsOneWidget);
    expect(find.text('数据结构与算法'), findsOneWidget);
    expect(find.text('2 个任务点待完成'), findsOneWidget);
    expect(find.text('同步课程'), findsWidgets);
    expect(find.text('全部 2'), findsOneWidget);
    expect(find.text('待完成 1'), findsOneWidget);
  });

  testWidgets('课程详情区分任务点与普通附件', (tester) async {
    await pumpTestPage(
      tester,
      const CourseDetailPage(courseId: 'c1'),
    );

    expect(find.text('课程内容'), findsOneWidget);
    expect(find.text('1.1 发现真正的问题'), findsOneWidget);
    expect(find.text('普通附件 · 不计入任务点'), findsOneWidget);
    expect(find.text('学习资料'), findsWidgets);
    expect(find.text('平台已确认'), findsWidgets);
    expect(find.text('安排这门课程'), findsOneWidget);
  });

  testWidgets('安排页分组展示执行中、等待平台与无播放记录', (tester) async {
    await pumpTestPage(tester, const PlansPage());

    expect(find.text('本周学习安排'), findsWidgets);
    expect(find.text('执行中 · 1'), findsOneWidget);
    expect(find.text('等待平台确认 · 1'), findsOneWidget);
    expect(find.text('1.1 发现真正的问题'), findsOneWidget);
    expect(find.text('无播放记录'), findsOneWidget);
    expect(find.text('暂停后续任务'), findsOneWidget);
    expect(find.textContaining('预计'), findsWidgets);
  });

  testWidgets('安排页说明题库策略自动执行，不要求人工确认', (tester) async {
    final planMap = Map<String, dynamic>.from(planFixture);
    planMap['question_policy'] = {
      'enabled': true,
      'model_offering_id': 'qm1',
      'answer_mode': 'always_llm',
      'require_manual_confirmation': true,
    };
    final server = TestServer(
      overrides: {
        'GET /api/v1/plans': (_) async => jsonResponse({
              'items': [planMap],
              'next_cursor': '',
            }),
      },
    );

    await pumpTestPage(tester, const PlansPage(), handler: server.call);

    expect(find.text('题库策略已开启'), findsOneWidget);
    expect(find.textContaining('开启后自动查询并提交测验答案'), findsOneWidget);
    expect(find.textContaining('人工确认'), findsNothing);
    expect(find.textContaining('不会自动执行答题'), findsNothing);
  });

  testWidgets('钱包区分待支付、支付成功待入账与已到账', (tester) async {
    await pumpTestPage(tester, const WalletPage());

    expect(find.text('余额与订单'), findsOneWidget);
    expect(find.text('¥54.50'), findsOneWidget);
    expect(find.text('待支付'), findsWidgets);
    expect(find.text('支付成功 · 入账处理中'), findsWidgets);
    expect(find.text('已到账'), findsWidgets);
    expect(find.text('题库用量 1'), findsOneWidget);
    await tester.tap(find.text('题库用量 1'));
    await settle(tester);
    expect(find.textContaining('单价待公布'), findsWidgets);
  });

  testWidgets('钱包题库费用说明指向公开模型价格，不回显全局旧费率', (tester) async {
    final server = TestServer(
      overrides: {
        'GET /api/v1/pricing': (_) async => jsonResponse({
              ...pricingFixture,
              'automatic_charging': true,
              'tiku': {
                'scope': 'tiku_tokens',
                'configured': true,
                'state': 'active',
                'unit_label': 'tokens',
                'unit_precision': 0,
                'input_micros_per_million': '7654321',
                'output_micros_per_million': '1234567',
              },
            }),
      },
    );

    await pumpTestPage(tester, const WalletPage(), handler: server.call);

    expect(find.text('题库：按所选公开模型的 token 价格单独计费。'), findsOneWidget);
    expect(find.textContaining('7.654321'), findsNothing);
    expect(find.textContaining('1.234567'), findsNothing);
  });

  testWidgets('支付回跳只查询服务端，不本地增加余额', (tester) async {
    final server = TestServer(
      overrides: {
        'GET /api/v1/recharge-orders/o3': (_) async => jsonResponse({
              'id': 'o3',
              'amount_minor': '1000',
              'currency': 'CNY',
              'channel': 'alipay',
              'status': 'completed',
              'payment_status': 'paid',
              'credit_status': 'credited',
              'created_at': '2026-10-20T02:20:00Z',
              'updated_at': '2026-10-20T02:22:00Z',
              'expires_at': '2026-10-20T02:50:00Z',
              'paid_at': '2026-10-20T02:21:00Z',
              'credited_at': '2026-10-20T02:22:00Z',
              'version': 3,
              'checkout_state': 'not_available',
            }),
      },
    );
    await pumpTestPage(
      tester,
      const WalletPage(orderId: 'o3'),
      handler: server.call,
    );

    expect(find.text('¥54.50'), findsOneWidget);
    expect(find.text('¥64.50'), findsNothing);
  });

  testWidgets('未连接服务时提供连接入口', (tester) async {
    await pumpTestPage(
      tester,
      const LoginPage(),
      auth: const AuthState(
        status: AuthStatus.unavailable,
        error: ApiException(
          statusCode: 0,
          code: 'client_not_configured',
          message: '当前客户端尚未连接服务，请稍后重试',
        ),
        capabilities: testCapabilities,
      ),
    );

    expect(find.textContaining('尚未连接服务'), findsOneWidget);
    expect(find.text('连接服务'), findsOneWidget);
  });

  testWidgets('连接服务拒绝 HTTP 并保存 HTTPS 地址', (tester) async {
    await pumpTestPage(
      tester,
      const ConnectServicePage(),
      auth: anonymousState(),
    );

    await tester.enterText(
      find.byKey(const Key('connect-service-url')),
      'http://example.com',
    );
    await tester.tap(find.text('保存并连接'));
    await settle(tester);
    expect(find.textContaining('https://'), findsWidgets);

    await tester.enterText(
      find.byKey(const Key('connect-service-url')),
      'https://api.example.test',
    );
    await tester.tap(find.text('保存并连接'));
    await settle(tester);
    expect(find.textContaining('服务地址已保存'), findsOneWidget);

    final prefs = await SharedPreferences.getInstance();
    expect(
      prefs.getString('bbo.server.api_base'),
      'https://api.example.test/api/v1/',
    );
  });

  testWidgets('导出下载需要重新验证时提供重新验证入口', (tester) async {
    final server = TestServer(
      overrides: {
        'GET /api/v1/me/data-exports/de1/download': (_) async => jsonResponse(
              {
                'error': {
                  'code': 'step_up_required',
                  'message': '下载前请重新验证身份',
                  'request_id': 'req_test',
                  'retryable': false,
                  'scope': 'request',
                },
              },
              status: 403,
            ),
      },
    );
    await pumpTestPage(tester, const SettingsPage(), handler: server.call);

    final download = find.widgetWithText(AsyncButton, '下载 JSON');
    await tester.ensureVisible(download);
    await tester.tap(download);
    await settle(tester);

    expect(find.text('下载前请重新验证身份'), findsOneWidget);
    expect(find.widgetWithText(TextButton, '重新验证'), findsOneWidget);
  });

  testWidgets('设置页展示身份、外观、通知与数据请求', (tester) async {
    await pumpTestPage(tester, const SettingsPage());

    expect(find.text('设置'), findsOneWidget);
    expect(find.text('学习通身份'), findsOneWidget);
    expect(find.text('沈知夏'), findsOneWidget);
    expect(find.text('石墨'), findsOneWidget);
    expect(find.text('青禾'), findsOneWidget);
    expect(find.text('靛蓝'), findsOneWidget);
    expect(find.text('任务需要处理时提醒'), findsOneWidget);
    expect(find.text('题库查询'), findsOneWidget);
    expect(find.widgetWithText(ListTile, '题库查询'), findsOneWidget);
    expect(find.text('为什么任务显示等待验证？'), findsNothing);
    await tester.ensureVisible(find.text('常见问题'));
    await tester.tap(find.text('常见问题'));
    await settle(tester);
    expect(find.text('为什么任务显示等待验证？'), findsOneWidget);
    await tester.tap(find.byTooltip('关闭'));
    await settle(tester);
    expect(find.text('服务连接'), findsOneWidget);
    expect(find.text('申请导出'), findsOneWidget);
    expect(find.text('申请删除'), findsOneWidget);
    expect(find.text('下载 JSON'), findsOneWidget);
    expect(find.text('待人工核对'), findsWidgets);
    expect(find.text('退出登录'), findsWidgets);
  });
}
