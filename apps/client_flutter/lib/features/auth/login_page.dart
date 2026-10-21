import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/format/formatters.dart';
import '../../core/models/capabilities.dart';
import '../../core/platform/external_link.dart';
import '../../design/components.dart';
import '../../design/palette.dart';
import '../../design/theme.dart';
import '../auth/auth_controller.dart';
import '../auth/login_controller.dart';

class LoginPage extends ConsumerStatefulWidget {
  const LoginPage({super.key});

  @override
  ConsumerState<LoginPage> createState() => _LoginPageState();
}

class _LoginPageState extends ConsumerState<LoginPage> {
  final _schoolController = TextEditingController();
  final _accountController = TextEditingController();
  final _passwordController = TextEditingController();
  final _verificationController = TextEditingController();
  final _formKey = GlobalKey<FormState>();
  bool _obscurePassword = true;
  bool _consent = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      ref.read(loginControllerProvider.notifier).reset();
    });
  }

  @override
  void dispose() {
    _schoolController.dispose();
    _accountController.dispose();
    _passwordController.dispose();
    _verificationController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final auth = ref.watch(authControllerProvider);
    final login = ref.watch(loginControllerProvider);
    final colors = context.colors;
    final wide = MediaQuery.sizeOf(context).width >= 980;
    if (auth.isAuthenticated) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) context.go('/home');
      });
    }
    return Scaffold(
      backgroundColor: colors.background,
      body: SafeArea(
        child: LayoutBuilder(
          builder: (context, constraints) {
            final content = wide
                ? Row(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Expanded(
                        flex: 6,
                        child: _StoryPanel(colors: colors),
                      ),
                      Expanded(
                        flex: 5,
                        child: SingleChildScrollView(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 46,
                            vertical: 40,
                          ),
                          child: Center(
                            child: ConstrainedBox(
                              constraints: const BoxConstraints(maxWidth: 460),
                              child: _buildContent(context, auth, login),
                            ),
                          ),
                        ),
                      ),
                    ],
                  )
                : SingleChildScrollView(
                    padding: const EdgeInsets.fromLTRB(20, 22, 20, 32),
                    child: Center(
                      child: ConstrainedBox(
                        constraints: const BoxConstraints(maxWidth: 520),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            const AppLogo(),
                            const SizedBox(height: 26),
                            _buildContent(context, auth, login),
                          ],
                        ),
                      ),
                    ),
                  );
            return GestureDetector(
              behavior: HitTestBehavior.translucent,
              onTap: () => FocusScope.of(context).unfocus(),
              child: content,
            );
          },
        ),
      ),
    );
  }

  Widget _buildContent(
    BuildContext context,
    AuthState auth,
    LoginState login,
  ) {
    final colors = context.colors;
    if (auth.status == AuthStatus.unavailable) {
      return _AuthUnavailable(
        message: auth.error?.userMessage ?? '服务暂不可用，请稍后重试',
        requestId: auth.error?.requestId,
        onRetry: () => ref.read(authControllerProvider.notifier).bootstrap(),
        showConnect: !kIsWeb,
        onConnect: () => context.go('/connect'),
      );
    }
    final capability = auth.capabilities.auth;
    if (capability.explicitlyUnavailable) {
      return _AuthUnavailable(
        title: '学习通登录尚未开放',
        message: '当前服务尚未配置学习通身份验证，暂时无法登录。',
        onRetry: () => ref.read(authControllerProvider.notifier).bootstrap(),
        showConnect: !kIsWeb,
        onConnect: () => context.go('/connect'),
      );
    }
    return Form(
      key: _formKey,
      child: AutofillGroup(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Semantics(
              header: true,
              child: Text(
                '使用学习通身份进入',
                style: TextStyle(
                  color: colors.text,
                  fontSize: 28,
                  height: 1.25,
                  fontWeight: FontWeight.w600,
                  letterSpacing: -0.7,
                ),
              ),
            ),
            const SizedBox(height: 9),
            Text(
              '无需注册新账号。验证通过后，课程、任务安排与钱包会关联到同一个学习通身份。',
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 14,
                height: 1.7,
              ),
            ),
            const SizedBox(height: 24),
            if (auth.notice != null) ...[
              InlineNotice(
                tone: StatusTone.warning,
                icon: Icons.shield_outlined,
                message: auth.notice!,
              ),
              const SizedBox(height: 16),
            ],
            if (login.message != null) ...[
              InlineNotice(
                tone: login.error?.isUnauthenticated == true
                    ? StatusTone.danger
                    : StatusTone.warning,
                icon: login.error?.isUnauthenticated == true
                    ? Icons.error_outline
                    : Icons.info_outline,
                message: login.message!,
                action: login.expired || login.locked
                    ? TextButton(
                        onPressed: () {
                          ref.read(loginControllerProvider.notifier).restart();
                          setState(() {});
                        },
                        child: const Text('重新发起'),
                      )
                    : null,
              ),
              const SizedBox(height: 16),
            ],
            if (login.expired || login.locked)
              Padding(
                padding: const EdgeInsets.only(bottom: 16),
                child: AsyncButton(
                  label: '重新发起验证',
                  icon: Icons.refresh,
                  onPressed: () {
                    ref.read(loginControllerProvider.notifier).restart();
                    setState(() {});
                  },
                  expand: true,
                ),
              ),
            TextFormField(
              key: const Key('login-school'),
              controller: _schoolController,
              enabled: !login.submitting,
              textInputAction: TextInputAction.next,
              autofillHints: const [AutofillHints.organizationName],
              decoration: const InputDecoration(
                labelText: '学校',
                hintText: '填写学习通中的学校名称',
              ),
              validator: (value) =>
                  (value == null || value.trim().isEmpty) ? '请填写学校名称' : null,
            ),
            const SizedBox(height: 14),
            TextFormField(
              key: const Key('login-account'),
              controller: _accountController,
              enabled: !login.submitting,
              textInputAction: TextInputAction.next,
              autocorrect: false,
              autofillHints: const [AutofillHints.username],
              decoration: const InputDecoration(
                labelText: '学习通账号',
                hintText: '手机号或学号',
              ),
              validator: (value) =>
                  (value == null || value.trim().isEmpty) ? '请填写学习通账号' : null,
            ),
            const SizedBox(height: 14),
            TextFormField(
              key: const Key('login-password'),
              controller: _passwordController,
              enabled: !login.submitting,
              obscureText: _obscurePassword,
              textInputAction: TextInputAction.next,
              autofillHints: const [AutofillHints.password],
              decoration: InputDecoration(
                labelText: '密码',
                hintText: '学习通登录密码',
                suffixIcon: IconButton(
                  tooltip: _obscurePassword ? '显示密码' : '隐藏密码',
                  onPressed: () {
                    setState(() => _obscurePassword = !_obscurePassword);
                  },
                  icon: Icon(
                    _obscurePassword
                        ? Icons.visibility_outlined
                        : Icons.visibility_off_outlined,
                    size: 19,
                  ),
                ),
              ),
              validator: (value) =>
                  (value == null || value.isEmpty) ? '请填写学习通密码' : null,
            ),
            const SizedBox(height: 14),
            TextFormField(
              key: const Key('login-verification'),
              controller: _verificationController,
              enabled: !login.submitting,
              textInputAction: TextInputAction.done,
              keyboardType: TextInputType.number,
              inputFormatters: [
                FilteringTextInputFormatter.allow(RegExp(r'[0-9A-Za-z]')),
                LengthLimitingTextInputFormatter(12),
              ],
              decoration: const InputDecoration(
                labelText: '验证码（如平台要求）',
                hintText: '没有提示时可留空',
              ),
              onFieldSubmitted: (_) => _submit(),
            ),
            const SizedBox(height: 16),
            CheckboxListTile(
              value: _consent,
              onChanged: login.submitting
                  ? null
                  : (value) => setState(() => _consent = value ?? false),
              controlAffinity: ListTileControlAffinity.leading,
              contentPadding: EdgeInsets.zero,
              visualDensity: VisualDensity.compact,
              title: Text(
                '我确认这是本人学习通账号，并授权平台在验证后读取课程、章节与任务点信息，用于课程同步和任务安排。',
                style: TextStyle(
                  color: colors.textSecondary,
                  fontSize: 12.5,
                  height: 1.6,
                ),
              ),
            ),
            const SizedBox(height: 10),
            if (login.hasChallenge)
              Padding(
                padding: const EdgeInsets.only(bottom: 10),
                child: Wrap(
                  spacing: 12,
                  runSpacing: 4,
                  children: [
                    Text(
                      '本次验证有效期至 ${formatShortDateTime(login.expiresAt)}',
                      style: TextStyle(
                        color: colors.muted,
                        fontSize: 12,
                      ),
                    ),
                    if (login.attemptsRemaining != null)
                      Text(
                        '剩余 ${login.attemptsRemaining} 次尝试',
                        style: TextStyle(
                          color: colors.muted,
                          fontSize: 12,
                        ),
                      ),
                  ],
                ),
              ),
            AsyncButton(
              label: login.submitting ? '正在验证学习通身份' : '验证并进入',
              icon: Icons.arrow_forward,
              busy: login.submitting,
              expand: true,
              onPressed: login.expired || login.locked
                  ? null
                  : () {
                      if (_formKey.currentState?.validate() ?? false) {
                        _submit();
                      }
                    },
            ),
            const SizedBox(height: 12),
            Text(
              '验证由学习通完成。平台不会保存学习通密码，也不会绕过验证码或身份校验。',
              style: TextStyle(
                color: colors.muted,
                fontSize: 12,
                height: 1.6,
              ),
            ),
            const SizedBox(height: 22),
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(
                  child: TextButton.icon(
                    onPressed: () async {
                      final opened = await openExternalHttps(
                        'https://i.chaoxing.com/',
                      );
                      if (!context.mounted) return;
                      if (!opened) {
                        showAppMessage(context, '无法打开学习通官方入口');
                      }
                    },
                    icon: const Icon(Icons.open_in_new, size: 15),
                    label: const Text('学习通官方入口找回密码'),
                  ),
                ),
              ],
            ),
            Text(
              '平台不提供注册入口，也不会重置学习通密码。',
              style: TextStyle(
                color: colors.muted,
                fontSize: 11.5,
              ),
            ),
            if (login.error?.requestId != null) ...[
              const SizedBox(height: 10),
              Text(
                '问题编号 ${login.error!.requestId}',
                style: TextStyle(
                  color: colors.muted,
                  fontSize: 10.5,
                  fontFamily: 'monospace',
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }

  void _submit() {
    ref.read(loginControllerProvider.notifier).submit(
          school: _schoolController.text,
          account: _accountController.text,
          password: _passwordController.text,
          verification: _verificationController.text,
          consent: _consent,
        );
  }
}

class _AuthUnavailable extends StatelessWidget {
  const _AuthUnavailable({
    this.title = '暂时无法登录',
    required this.message,
    this.requestId,
    this.onRetry,
    this.showConnect = false,
    this.onConnect,
  });

  final String title;
  final String message;
  final String? requestId;
  final VoidCallback? onRetry;
  final bool showConnect;
  final VoidCallback? onConnect;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return SectionCard(
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 46,
            height: 46,
            decoration: BoxDecoration(
              color: colors.warningBg,
              shape: BoxShape.circle,
            ),
            child: Icon(
              Icons.lock_person_outlined,
              color: colors.warning,
              size: 22,
            ),
          ),
          const SizedBox(height: 18),
          Text(
            title,
            style: TextStyle(
              color: colors.text,
              fontSize: 21,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 8),
          Text(
            message,
            style: TextStyle(
              color: colors.textSecondary,
              fontSize: 13.5,
              height: 1.65,
            ),
          ),
          if (requestId != null && requestId!.isNotEmpty) ...[
            const SizedBox(height: 8),
            Text(
              '问题编号 $requestId',
              style: TextStyle(
                color: colors.muted,
                fontSize: 11,
                fontFamily: 'monospace',
              ),
            ),
          ],
          if (onRetry != null) ...[
            const SizedBox(height: 18),
            AsyncButton(
              label: '重新检查服务状态',
              icon: Icons.refresh,
              onPressed: onRetry,
            ),
          ],
          if (showConnect && onConnect != null) ...[
            const SizedBox(height: 10),
            OutlinedButton.icon(
              onPressed: onConnect,
              icon: const Icon(Icons.link, size: 16),
              label: const Text('连接服务'),
            ),
          ],
        ],
      ),
    );
  }
}

class _StoryPanel extends StatelessWidget {
  const _StoryPanel({required this.colors});

  final AppColors colors;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: colors.heroBg,
        border: Border(right: BorderSide(color: colors.border)),
      ),
      padding: const EdgeInsets.fromLTRB(52, 46, 52, 46),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const AppLogo(),
          const Spacer(),
          Text(
            '让计划向前，\n让时间留给你。',
            style: TextStyle(
              color: colors.heroText,
              fontSize: 40,
              height: 1.28,
              fontWeight: FontWeight.w600,
              letterSpacing: -1.2,
            ),
          ),
          const SizedBox(height: 18),
          Text(
            '课程同步、任务安排与平台确认结果，都在一个清晰的学习空间里。',
            style: TextStyle(
              color: colors.heroText.withValues(alpha: 0.8),
              fontSize: 15,
              height: 1.8,
            ),
          ),
          const SizedBox(height: 32),
          Wrap(
            spacing: 10,
            runSpacing: 10,
            children: const [
              _StoryChip(icon: Icons.verified_outlined, label: '平台确认再计完成'),
              _StoryChip(icon: Icons.sync_outlined, label: '课程状态可同步'),
              _StoryChip(
                  icon: Icons.account_balance_wallet_outlined,
                  label: '支付与到账分开记录'),
            ],
          ),
          const Spacer(),
          Text(
            '把时间留给更重要的事。',
            style: TextStyle(
              color: colors.heroText.withValues(alpha: 0.62),
              fontSize: 12.5,
            ),
          ),
        ],
      ),
    );
  }
}

class _StoryChip extends StatelessWidget {
  const _StoryChip({required this.icon, required this.label});

  final IconData icon;
  final String label;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 9),
      decoration: BoxDecoration(
        color: colors.surface.withValues(alpha: 0.55),
        borderRadius: BorderRadius.circular(999),
        border: Border.all(color: colors.heroText.withValues(alpha: 0.12)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 15, color: colors.heroText),
          const SizedBox(width: 6),
          Text(
            label,
            style: TextStyle(
              color: colors.heroText,
              fontSize: 12.5,
              fontWeight: FontWeight.w500,
            ),
          ),
        ],
      ),
    );
  }
}

AuthCapability? unusedCapability(AuthState state) => state.capabilities.auth;
