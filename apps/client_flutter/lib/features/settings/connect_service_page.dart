import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/config/server_config.dart';
import '../../design/components.dart';
import '../../design/theme.dart';
import '../auth/auth_controller.dart';

class ConnectServicePage extends ConsumerStatefulWidget {
  const ConnectServicePage({super.key});

  @override
  ConsumerState<ConnectServicePage> createState() => _ConnectServicePageState();
}

class _ConnectServicePageState extends ConsumerState<ConnectServicePage> {
  final _controller = TextEditingController();
  final _formKey = GlobalKey<FormState>();
  bool _saving = false;
  String? _error;
  String? _notice;

  @override
  void initState() {
    super.initState();
    final current = ref.read(serverConfigProvider).baseUri;
    if (current != null) {
      _controller.text = current.toString();
    }
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final server = ref.watch(serverConfigProvider);
    return Scaffold(
      backgroundColor: colors.background,
      appBar: AppBar(
        leading: IconButton(
          tooltip: '返回',
          onPressed: () {
            if (context.canPop()) {
              context.pop();
            } else {
              context.go('/login');
            }
          },
          icon: const Icon(Icons.arrow_back, size: 20),
        ),
        title: const Text('连接服务'),
      ),
      body: SafeArea(
        child: SingleChildScrollView(
          padding: const EdgeInsets.fromLTRB(20, 20, 20, 32),
          child: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 560),
              child: kIsWeb
                  ? _WebSameOriginNotice(
                      baseUri: server.baseUri,
                      onBack: () => context.go('/login'),
                    )
                  : Form(
                      key: _formKey,
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          const AppLogo(),
                          const SizedBox(height: 24),
                          Text(
                            server.configured ? '切换学习服务' : '连接学习服务',
                            style: TextStyle(
                              color: colors.text,
                              fontSize: 26,
                              fontWeight: FontWeight.w600,
                              letterSpacing: -0.6,
                            ),
                          ),
                          const SizedBox(height: 8),
                          Text(
                            '输入服务提供方给出的 HTTPS 地址。切换服务会退出当前登录，并清除本机会话和用户缓存。',
                            style: TextStyle(
                              color: colors.textSecondary,
                              fontSize: 14,
                              height: 1.7,
                            ),
                          ),
                          const SizedBox(height: 20),
                          if (_notice != null)
                            Padding(
                              padding: const EdgeInsets.only(bottom: 14),
                              child: InlineNotice(
                                tone: StatusTone.success,
                                icon: Icons.check_circle_outline,
                                message: _notice!,
                              ),
                            ),
                          if (_error != null)
                            Padding(
                              padding: const EdgeInsets.only(bottom: 14),
                              child: InlineNotice(
                                tone: StatusTone.danger,
                                icon: Icons.error_outline,
                                message: _error!,
                              ),
                            ),
                          TextFormField(
                            key: const Key('connect-service-url'),
                            controller: _controller,
                            enabled: !_saving,
                            keyboardType: TextInputType.url,
                            textInputAction: TextInputAction.done,
                            autocorrect: false,
                            decoration: const InputDecoration(
                              labelText: '服务地址',
                              hintText: 'https://your-service.example',
                              helperText: '正式环境必须使用 HTTPS',
                            ),
                            validator: (value) {
                              if (value == null || value.trim().isEmpty) {
                                return '请填写服务地址';
                              }
                              final normalized = normalizeServiceBase(
                                value,
                                allowDebugHttp: kDebugMode,
                              );
                              if (normalized == null) {
                                return '请填写以 https:// 开头的完整服务地址';
                              }
                              return null;
                            },
                          ),
                          const SizedBox(height: 18),
                          AsyncButton(
                            label: server.configured ? '切换并重新验证' : '保存并连接',
                            icon: Icons.link,
                            busy: _saving,
                            expand: true,
                            onPressed: _save,
                          ),
                          const SizedBox(height: 12),
                          if (server.configured)
                            Text(
                              '当前服务 ${server.baseUri}',
                              style: TextStyle(
                                color: colors.muted,
                                fontSize: 12,
                                height: 1.5,
                              ),
                            ),
                          if (_notice != null) ...[
                            const SizedBox(height: 10),
                            OutlinedButton.icon(
                              onPressed: () => context.go('/login'),
                              icon: const Icon(Icons.login, size: 16),
                              label: const Text('返回登录'),
                            ),
                          ],
                          if (kDebugMode) ...[
                            const SizedBox(height: 12),
                            Text(
                              '调试构建允许本机与私有网段的 HTTP 地址；正式包只接受 HTTPS。',
                              style: TextStyle(
                                color: colors.muted,
                                fontSize: 12,
                                height: 1.5,
                              ),
                            ),
                          ],
                        ],
                      ),
                    ),
            ),
          ),
        ),
      ),
    );
  }

  Future<void> _save() async {
    if (_saving) return;
    if (!(_formKey.currentState?.validate() ?? false)) return;
    final normalized = normalizeServiceBase(
      _controller.text,
      allowDebugHttp: kDebugMode,
    );
    if (normalized == null) return;
    final auth = ref.read(authControllerProvider);
    if (auth.isAuthenticated) {
      final confirmed = await showConfirmDialog(
        context,
        title: '切换服务地址？',
        message: '切换后会退出当前登录，并清除本机会话、令牌与用户缓存。需要在新服务上重新验证学习通身份。',
        confirmLabel: '切换服务',
        danger: true,
      );
      if (!confirmed || !mounted) return;
    }
    setState(() {
      _saving = true;
      _error = null;
      _notice = null;
    });
    await ref.read(authControllerProvider.notifier).clearForServerChange();
    final result = await ref
        .read(serverConfigProvider.notifier)
        .setBaseUrl(_controller.text);
    if (!mounted) return;
    if (!result.ok) {
      setState(() {
        _saving = false;
        _error = result.error;
      });
      return;
    }
    setState(() {
      _saving = false;
      _notice = '服务地址已保存，请重新验证学习通身份';
    });
    await ref.read(authControllerProvider.notifier).bootstrap();
  }
}

class _WebSameOriginNotice extends StatelessWidget {
  const _WebSameOriginNotice({required this.baseUri, required this.onBack});

  final Uri? baseUri;
  final VoidCallback onBack;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const AppLogo(),
        const SizedBox(height: 24),
        SectionCard(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'Web 使用同源服务',
                style: TextStyle(
                  color: context.colors.text,
                  fontSize: 16,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 8),
              Text(
                '网页客户端固定连接当前站点，不会根据链接参数切换服务地址。',
                style: TextStyle(
                  color: context.colors.textSecondary,
                  fontSize: 13,
                  height: 1.65,
                ),
              ),
              if (baseUri != null) ...[
                const SizedBox(height: 12),
                Text(
                  '$baseUri',
                  style: TextStyle(
                    color: context.colors.muted,
                    fontSize: 12,
                  ),
                ),
              ],
              const SizedBox(height: 18),
              OutlinedButton(
                onPressed: onBack,
                child: const Text('返回登录'),
              ),
            ],
          ),
        ),
      ],
    );
  }
}
