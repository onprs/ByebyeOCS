import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
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
  final _accountController = TextEditingController();
  final _passwordController = TextEditingController();
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
    _accountController.dispose();
    _passwordController.dispose();
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
                        child: LayoutBuilder(
                          builder: (context, panel) => SingleChildScrollView(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 40,
                              vertical: 32,
                            ),
                            child: ConstrainedBox(
                              constraints: BoxConstraints(
                                minHeight: (panel.maxHeight - 64).clamp(
                                  0.0,
                                  double.infinity,
                                ),
                              ),
                              child: Center(
                                child: ConstrainedBox(
                                  constraints:
                                      const BoxConstraints(maxWidth: 430),
                                  child: _buildContent(context, auth, login),
                                ),
                              ),
                            ),
                          ),
                        ),
                      ),
                    ],
                  )
                : SingleChildScrollView(
                    padding: const EdgeInsets.fromLTRB(22, 26, 22, 32),
                    child: Center(
                      child: ConstrainedBox(
                        constraints: const BoxConstraints(maxWidth: 520),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            const AppLogo(),
                            const SizedBox(height: 24),
                            _MobileIntro(colors: colors),
                            const SizedBox(height: 24),
                            _buildContent(context, auth, login),
                            const SizedBox(height: 28),
                            const SiteFooter(compact: true),
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
    final wide = MediaQuery.sizeOf(context).width >= 980;
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
            if (wide) ...[
              Text(
                '账号登录',
                style: TextStyle(
                  color: colors.accent,
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  letterSpacing: 2.4,
                ),
              ),
              const SizedBox(height: 14),
            ],
            Semantics(
              header: true,
              child: Text(
                '使用学习通身份进入',
                style: TextStyle(
                  color: colors.text,
                  fontSize: 28,
                  height: 1.25,
                  fontWeight: FontWeight.w600,
                  letterSpacing: -0.8,
                ),
              ),
            ),
            const SizedBox(height: 10),
            Text(
              '课程、任务安排与学习记录会关联到同一学习通账号。',
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 13.5,
                height: 1.75,
              ),
            ),
            const SizedBox(height: 28),
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
            const SizedBox(height: 18),
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
              '身份由学习通验证。平台不会保存学习通密码，也不会绕过平台校验。',
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
            '请使用本人学习通账号。验证通过后，平台读取课程、章节与任务点信息，用于课程同步和任务安排。学习通密码仅用于本次验证，平台不保存；学校等身份信息以学习通返回为准。'),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(context), child: const Text('知道了'))
        ],
      ),
    );
  }

  void _submit() {
    ref.read(loginControllerProvider.notifier).submit(
          account: _accountController.text,
          password: _passwordController.text,
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
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [
            Color.alphaBlend(
              colors.accent.withValues(alpha: colors.isDark ? 0.12 : 0.09),
              colors.heroBg,
            ),
            colors.heroBg,
            colors.surfaceSubtle,
          ],
          stops: const [0, 0.58, 1],
        ),
        border: Border(right: BorderSide(color: colors.border)),
      ),
      child: Stack(
        fit: StackFit.expand,
        children: [
          IgnorePointer(
            child: ClipRect(
              child: CustomPaint(painter: _LoginBackdropPainter(colors)),
            ),
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(56, 46, 56, 40),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const AppLogo(),
                const Spacer(flex: 5),
                Row(
                  children: [
                    Container(
                      width: 26,
                      height: 2,
                      color: colors.accent,
                    ),
                    const SizedBox(width: 10),
                    Text(
                      '学习通身份接入',
                      style: TextStyle(
                        color: colors.accent,
                        fontSize: 12,
                        fontWeight: FontWeight.w600,
                        letterSpacing: 2,
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 24),
                Text(
                  '让每一次学习\n都有据可依。',
                  style: TextStyle(
                    color: colors.heroText,
                    fontSize: 42,
                    height: 1.26,
                    fontWeight: FontWeight.w600,
                    letterSpacing: -1.2,
                  ),
                ),
                const SizedBox(height: 20),
                ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 470),
                  child: Text(
                    '同步课程与任务点，按章节安排学习；\n完成情况以平台返回结果为准。',
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 15,
                      height: 1.8,
                    ),
                  ),
                ),
                const SizedBox(height: 36),
                _CapabilityRow(colors: colors),
                const Spacer(flex: 4),
                Row(
                  children: [
                    Icon(Icons.lock_outline, size: 14, color: colors.muted),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        '密码仅用于本次身份验证，不会保存在平台。',
                        style: TextStyle(
                          color: colors.muted,
                          fontSize: 12,
                          height: 1.6,
                        ),
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _MobileIntro extends StatelessWidget {
  const _MobileIntro({required this.colors});
  final AppColors colors;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          '让每一次学习都有据可依。',
          style: TextStyle(
            color: colors.heroText,
            fontSize: 21,
            height: 1.4,
            fontWeight: FontWeight.w600,
            letterSpacing: -0.4,
          ),
        ),
        const SizedBox(height: 8),
        Text(
          '同步课程与任务点，按章节安排学习。',
          style: TextStyle(
            color: colors.textSecondary,
            fontSize: 13.5,
            height: 1.7,
          ),
        ),
      ],
    );
  }
}

class _CapabilityRow extends StatelessWidget {
  const _CapabilityRow({required this.colors});
  final AppColors colors;

  @override
  Widget build(BuildContext context) {
    const items = [
      (Icons.sync_alt, '课程同步'),
      (Icons.event_note_outlined, '任务安排'),
      (Icons.verified_outlined, '平台确认'),
    ];
    return Wrap(
      spacing: 26,
      runSpacing: 14,
      children: [
        for (final (icon, label) in items)
          Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 16, color: colors.accent),
              const SizedBox(width: 8),
              Text(
                label,
                style: TextStyle(
                  color: colors.textSecondary,
                  fontSize: 13,
                  letterSpacing: 0.4,
                ),
              ),
            ],
          ),
      ],
    );
  }
}

class _LoginBackdropPainter extends CustomPainter {
  _LoginBackdropPainter(this.colors);
  final AppColors colors;

  @override
  void paint(Canvas canvas, Size size) {
    final accent = colors.accent;
    final center = Offset(size.width * 0.9, size.height * 0.18);
    for (final (radius, opacity) in [
      (120.0, 0.11),
      (210.0, 0.075),
      (300.0, 0.05),
    ]) {
      canvas.drawCircle(
        center,
        radius,
        Paint()
          ..style = PaintingStyle.stroke
          ..strokeWidth = 1
          ..color = accent.withValues(alpha: opacity),
      );
    }
    final frameCenter = Offset(size.width * 0.14, size.height * 0.79);
    final frame = RRect.fromRectAndRadius(
      Rect.fromCenter(center: frameCenter, width: 230, height: 230),
      const Radius.circular(58),
    );
    canvas.save();
    canvas.translate(frameCenter.dx, frameCenter.dy);
    canvas.rotate(-0.18);
    canvas.translate(-frameCenter.dx, -frameCenter.dy);
    canvas.drawRRect(
      frame,
      Paint()
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1.2
        ..color = accent.withValues(alpha: 0.09),
    );
    canvas.restore();
    final dot = Paint()..color = accent.withValues(alpha: 0.13);
    for (var row = 0; row < 6; row++) {
      for (var column = 0; column < 6; column++) {
        canvas.drawCircle(
          Offset(size.width - 156 + column * 22, size.height - 168 + row * 22),
          1.4,
          dot,
        );
      }
    }
  }

  @override
  bool shouldRepaint(_LoginBackdropPainter oldDelegate) =>
      oldDelegate.colors != colors;
}

AuthCapability? unusedCapability(AuthState state) => state.capabilities.auth;
