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
                  fontSize: 26,
                  height: 1.25,
                  fontWeight: FontWeight.w600,
                  letterSpacing: -0.7,
                ),
              ),
            ),
            const SizedBox(height: 9),
            const SizedBox(height: 24),
            if (auth.notice != null && login.message == null) ...[
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
              secondary: TextButton(
                  onPressed: _showPrivacy, child: const Text('隐私说明')),
              title: Text(
                '同意身份验证与课程读取',
                style: TextStyle(
                  color: colors.textSecondary,
                  fontSize: 13,
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
          ],
        ),
      ),
    );
  }

  void _showPrivacy() {
    showDialog<void>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('身份验证与隐私'),
        content: const Text(
            '请使用本人学习通账号。验证通过后，平台读取课程、章节与任务点信息，用于课程同步和任务安排。学习通密码仅用于本次验证，平台不保存。验证码与身份核验由学习通完成。'),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(context), child: const Text('知道了'))
        ],
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
              fontSize: 16,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 8),
          Text(
            message,
            style: TextStyle(
              color: colors.textSecondary,
              fontSize: 14,
              height: 1.65,
            ),
          ),
          if (requestId != null && requestId!.isNotEmpty) ...[
            const SizedBox(height: 8),
            Text(
              '问题编号 $requestId',
              style: TextStyle(
                color: colors.muted,
                fontSize: 12,
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
      padding: const EdgeInsets.all(48),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const AppLogo(),
          const Spacer(),
          Text('同步课程，管理学习安排。',
              style: TextStyle(color: colors.text, fontSize: 16, height: 1.6)),
          const Spacer(),
        ],
      ),
    );
  }
}

AuthCapability? unusedCapability(AuthState state) => state.capabilities.auth;
