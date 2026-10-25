import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/providers.dart';
import '../../core/config/server_config.dart';
import '../../core/api/api_error.dart';
import '../../core/format/formatters.dart';
import '../../core/models/auth.dart';
import '../../core/models/data_request.dart';
import '../../core/models/preferences.dart';
import '../../app/scroll_controllers.dart';
import '../../design/components.dart';
import '../../design/palette.dart';
import '../../design/theme.dart';
import '../auth/auth_controller.dart';
import 'preferences_controller.dart';
import 'privacy_controller.dart';

class SettingsPage extends ConsumerStatefulWidget {
  const SettingsPage({super.key});

  @override
  ConsumerState<SettingsPage> createState() => _SettingsPageState();
}

class _SettingsPageState extends ConsumerState<SettingsPage> {
  @override
  Widget build(BuildContext context) {
    final user = ref.watch(currentUserProvider);
    final auth = ref.watch(authControllerProvider);
    final appearance = ref.watch(appearanceControllerProvider);
    final preferencesAsync = ref.watch(preferencesControllerProvider);
    final privacyAsync = ref.watch(privacyControllerProvider);
    final colors = context.colors;
    final scroll = ref.watch(tabScrollControllerProvider('settings'));
    return SingleChildScrollView(
      key: const PageStorageKey('settings-scroll'),
      controller: scroll,
      primary: false,
      child: ResponsiveContent(
        maxWidth: 960,
        child:
            Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
          const PageHeader(title: '设置'),
          SectionGroup(
              child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                _IdentityCard(
                    ref: ref,
                    user: user,
                    auth: auth,
                    onReverify: _logoutForReverify),
                if (!kIsWeb)
                  _ServerConnectionCard(
                      config: ref.watch(serverConfigProvider),
                      onOpen: () => context.go('/connect')),
                Divider(height: 1, color: colors.border),
                SectionCard(
                  title: '外观与通知',
                  action: appearance.syncing
                      ? const SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : null,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      LayoutBuilder(
                        builder: (context, constraints) {
                          const columns = 3;
                          const spacing = 8.0;
                          final width =
                              (constraints.maxWidth - spacing * (columns - 1)) /
                                  columns;
                          return Wrap(
                            spacing: spacing,
                            runSpacing: spacing,
                            children: [
                              for (final id in ColorSchemeId.values)
                                SizedBox(
                                  width: width,
                                  child: _PaletteChoice(
                                    id: id,
                                    selected: appearance.colorScheme == id,
                                    onTap: () => _changeColorScheme(id),
                                  ),
                                ),
                            ],
                          );
                        },
                      ),
                      const SizedBox(height: 16),
                      Text(
                        '浅深模式',
                        style: TextStyle(
                          color: colors.textSecondary,
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      const SizedBox(height: 8),
                      SegmentedButton<AppThemeMode>(
                        segments: const [
                          ButtonSegment(
                            value: AppThemeMode.system,
                            label: Text('跟随系统'),
                          ),
                          ButtonSegment(
                            value: AppThemeMode.light,
                            label: Text('浅色'),
                          ),
                          ButtonSegment(
                            value: AppThemeMode.dark,
                            label: Text('深色'),
                          ),
                        ],
                        selected: {appearance.themeMode},
                        showSelectedIcon: false,
                        onSelectionChanged: (value) async {
                          await ref
                              .read(appearanceControllerProvider.notifier)
                              .setThemeMode(value.first);
                          await _savePreferences(themeMode: value.first);
                        },
                      ),
                      if (appearance.error != null) ...[
                        const SizedBox(height: 12),
                        InlineNotice(
                          tone: StatusTone.warning,
                          message: appearance.error!,
                          action: TextButton(
                            onPressed: () => ref
                                .read(preferencesControllerProvider.notifier)
                                .reload(),
                            child: const Text('重新加载'),
                          ),
                        ),
                      ] else if (appearance.notice != null) ...[
                        const SizedBox(height: 12),
                        InlineNotice(
                          tone: StatusTone.success,
                          message: appearance.notice!,
                        ),
                      ],
                      if (preferencesAsync.hasError) ...[
                        const SizedBox(height: 12),
                        InlineNotice(
                          tone: StatusTone.neutral,
                          icon: Icons.cloud_off_outlined,
                          message: '账户偏好暂时无法读取，本机外观选择仍然生效。',
                          action: TextButton(
                            onPressed: () => ref
                                .read(preferencesControllerProvider.notifier)
                                .reload(),
                            child: const Text('重试'),
                          ),
                        ),
                      ],
                    ],
                  ),
                ),
                Divider(height: 1, color: colors.border),
                SectionCard(
                  title: '通知偏好',
                  child: preferencesAsync.when(
                    loading: () => const LoadingList(rows: 2, height: 48),
                    error: (error, _) => ErrorPanel(
                      message: error is ApiException
                          ? error.userMessage
                          : '通知偏好暂时无法加载',
                      compact: true,
                      onRetry: () => ref
                          .read(preferencesControllerProvider.notifier)
                          .reload(),
                    ),
                    data: (snapshot) => Column(
                      children: [
                        Material(
                          color: Colors.transparent,
                          child: SwitchListTile(
                            value: snapshot.preferences.taskAttention,
                            onChanged: appearance.syncing
                                ? null
                                : (value) =>
                                    _savePreferences(taskAttention: value),
                            contentPadding: EdgeInsets.zero,
                            title: const Text('任务需要处理时提醒'),
                          ),
                        ),
                        Divider(color: context.colors.border),
                        Material(
                          color: Colors.transparent,
                          child: SwitchListTile(
                            value: snapshot.preferences.walletChanges,
                            onChanged: appearance.syncing
                                ? null
                                : (value) =>
                                    _savePreferences(walletChanges: value),
                            contentPadding: EdgeInsets.zero,
                            title: const Text('钱包状态变化时提醒'),
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
                Divider(height: 1, color: colors.border),
                SectionCard(
                  title: '数据与隐私',
                  action: TextButton.icon(
                    onPressed: privacyAsync.valueOrNull?.refreshing == true
                        ? null
                        : () => ref
                            .read(privacyControllerProvider.notifier)
                            .refresh(),
                    icon: const Icon(Icons.refresh, size: 15),
                    label: const Text('刷新状态'),
                  ),
                  child: _buildPrivacy(context, privacyAsync),
                ),
                Divider(height: 1, color: colors.border),
                SectionCard(
                    title: '帮助',
                    child: Column(children: [
                      if (!auth.capabilities.questionsExplicitlyDisabled)
                        ListTile(
                            contentPadding: EdgeInsets.zero,
                            title: const Text('题库查询'),
                            trailing: const Icon(Icons.chevron_right, size: 16),
                            onTap: () => context.go('/questions')),
                      ListTile(
                          contentPadding: EdgeInsets.zero,
                          title: const Text('常见问题'),
                          trailing: const Icon(Icons.chevron_right, size: 16),
                          onTap: _showHelp),
                      const SizedBox(height: 12),
                      Align(
                          alignment: Alignment.centerRight,
                          child: AsyncButton(
                              label: '退出登录',
                              icon: Icons.logout,
                              busy: auth.busy,
                              variant: AsyncButtonVariant.outline,
                              onPressed: _logout)),
                    ])),
              ])),
        ]),
      ),
    );
  }

  void _showHelp() {
    showAdaptivePanel<void>(
        context: context,
        builder: (context) => SafeArea(
                child: SizedBox(
              height: MediaQuery.sizeOf(context).height * .8,
              child: Column(children: [
                Padding(
                    padding: const EdgeInsets.all(20),
                    child: SheetHeader(
                        title: '常见问题',
                        trailing: IconButton(
                            tooltip: '关闭',
                            onPressed: () => Navigator.pop(context),
                            icon: const Icon(Icons.close, size: 20)))),
                Expanded(
                    child: SingleChildScrollView(
                        padding: const EdgeInsets.all(20),
                        child: Column(
                            crossAxisAlignment: CrossAxisAlignment.stretch,
                            children: [
                              const _FaqItem(
                                  question: '为什么任务显示等待验证？',
                                  answer: '执行结束后会再次查询学习通。平台确认前不会计为完成。'),
                              Divider(height: 32, color: context.colors.border),
                              const _FaqItem(
                                  question: '支付成功但余额没有变化？',
                                  answer: '请回到钱包查询订单状态，余额以账户流水为准。'),
                              Divider(height: 32, color: context.colors.border),
                              const _FaqItem(
                                  question: '学习通密码忘记怎么办？',
                                  answer: '请在学习通官方入口找回密码。'),
                            ]))),
              ]),
            )));
  }

  Future<void> _changeColorScheme(ColorSchemeId id) async {
    await ref.read(appearanceControllerProvider.notifier).setColorScheme(id);
    await _savePreferences(colorScheme: id);
  }

  Future<void> _savePreferences({
    ColorSchemeId? colorScheme,
    AppThemeMode? themeMode,
    bool? taskAttention,
    bool? walletChanges,
  }) async {
    final snapshot = ref.read(preferencesControllerProvider).valueOrNull;
    if (snapshot == null) return;
    await ref.read(preferencesControllerProvider.notifier).save(
          colorScheme: colorScheme,
          themeMode: themeMode,
          taskAttention: taskAttention,
          walletChanges: walletChanges,
        );
  }

  Widget _buildPrivacy(BuildContext context, AsyncValue<PrivacyState> async) {
    final privacy = async.valueOrNull;
    final error = async.error;
    if (privacy == null) {
      return async.isLoading
          ? const LoadingList(rows: 2, height: 52)
          : ErrorPanel(
              message: error is ApiException ? error.userMessage : '数据请求暂时无法加载',
              compact: true,
              onRetry: () =>
                  ref.read(privacyControllerProvider.notifier).refresh(),
            );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (privacy.error != null || privacy.notice != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 14),
            child: InlineNotice(
              tone: privacy.error != null
                  ? StatusTone.warning
                  : StatusTone.success,
              message: privacy.error ?? privacy.notice!,
              action: privacy.errorCode == 'step_up_required'
                  ? Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        TextButton(
                          onPressed: _logoutForReverify,
                          child: const Text('重新验证'),
                        ),
                        IconButton(
                          tooltip: '关闭',
                          onPressed: () => ref
                              .read(privacyControllerProvider.notifier)
                              .clearBanner(),
                          icon: const Icon(Icons.close, size: 16),
                        ),
                      ],
                    )
                  : IconButton(
                      tooltip: '关闭',
                      onPressed: () => ref
                          .read(privacyControllerProvider.notifier)
                          .clearBanner(),
                      icon: const Icon(Icons.close, size: 16),
                    ),
            ),
          ),
        _DataActionRow(
          icon: Icons.file_download_outlined,
          title: '申请数据导出',
          description: '导出课程、安排、订单与流水等账户数据，不包含密码、Cookie 或会话令牌。',
          buttonLabel: privacy.requestingExport ? '正在提交' : '申请导出',
          onPressed: privacy.requestingExport ? null : _requestExport,
        ),
        Divider(height: 24, color: context.colors.border),
        _DataActionRow(
          icon: Icons.delete_outline,
          title: '申请删除账户数据',
          description: '删除前会核对资金、未结算用量与必要保留记录，不会自动转移余额。',
          buttonLabel: privacy.requestingDeletion ? '正在提交' : '申请删除',
          danger: true,
          onPressed: privacy.requestingDeletion ? null : _requestDeletion,
        ),
        Divider(height: 24, color: context.colors.border),
        Row(
          children: [
            Text(
              '导出记录',
              style: TextStyle(
                color: context.colors.text,
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
            const Spacer(),
            if (privacy.exportsCursor != null)
              TextButton(
                onPressed: () => ref
                    .read(privacyControllerProvider.notifier)
                    .loadMoreExports(),
                child: const Text('更多'),
              ),
          ],
        ),
        const SizedBox(height: 6),
        if (privacy.exports.isEmpty)
          Text(
            '还没有导出申请。',
            style: TextStyle(
              color: context.colors.textSecondary,
              fontSize: 12,
            ),
          )
        else
          for (final item in privacy.exports)
            _DataRequestTile(
              request: item,
              downloading: privacy.downloadingId == item.id,
              onDownload: item.isReady
                  ? () => ref
                      .read(privacyControllerProvider.notifier)
                      .download(item)
                  : null,
            ),
        const SizedBox(height: 16),
        Row(
          children: [
            Text(
              '删除申请',
              style: TextStyle(
                color: context.colors.text,
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
            const Spacer(),
            if (privacy.deletionsCursor != null)
              TextButton(
                onPressed: () => ref
                    .read(privacyControllerProvider.notifier)
                    .loadMoreDeletions(),
                child: const Text('更多'),
              ),
          ],
        ),
        const SizedBox(height: 6),
        if (privacy.deletions.isEmpty)
          Text(
            '还没有删除申请。',
            style: TextStyle(
              color: context.colors.textSecondary,
              fontSize: 12,
            ),
          )
        else
          for (final item in privacy.deletions) _DataRequestTile(request: item),
      ],
    );
  }

  Future<void> _requestExport() async {
    final confirmed = await showConfirmDialog(
      context,
      title: '申请导出账户数据？',
      message: '导出需要一些时间处理，下载前可能需要重新验证身份。导出内容不包含密码、Cookie 或会话令牌。',
      confirmLabel: '提交导出申请',
    );
    if (!confirmed || !mounted) return;
    await ref.read(privacyControllerProvider.notifier).requestExport();
  }

  Future<void> _requestDeletion() async {
    final confirmed = await showConfirmDialog(
      context,
      title: '申请删除账户数据？',
      message: '提交后会停止后续安排并冻结账号，资金、未结算用量与必要财务记录将单独核对。'
          '此操作不会自动退款或转移余额。',
      confirmLabel: '提交删除申请',
      danger: true,
    );
    if (!confirmed || !mounted) return;
    final created =
        await ref.read(privacyControllerProvider.notifier).requestDeletion();
    if (created == null || !mounted) return;
    final auth = ref.read(authControllerProvider.notifier);
    // 先进入公开回执页，再清本地会话，避免路由在退出时把回执页顶回登录页。
    context.go('/deletion-receipt');
    await auth.endSessionAfterDeletion();
  }

  Future<void> _logout() async {
    final confirmed = await showConfirmDialog(
      context,
      title: '退出当前账户？',
      message: '本机会话和用户缓存会被清除。再次进入需要重新验证学习通身份。',
      confirmLabel: '退出登录',
      danger: true,
    );
    if (!confirmed || !mounted) return;
    await ref.read(authControllerProvider.notifier).logout();
    if (!mounted) return;
    context.go('/login');
  }

  Future<void> _logoutForReverify() async {
    final confirmed = await showConfirmDialog(
      context,
      title: '重新验证学习通身份',
      message: '当前学习通会话需要重新验证。退出当前会话后，可用学习通身份重新登录。',
      confirmLabel: '重新验证',
    );
    if (!confirmed || !mounted) return;
    await ref.read(authControllerProvider.notifier).logout();
    if (!mounted) return;
    context.go('/login');
  }
}

class _IdentityCard extends StatelessWidget {
  const _IdentityCard({
    required this.ref,
    required this.user,
    required this.auth,
    required this.onReverify,
  });

  final WidgetRef ref;
  final UserProfile? user;
  final AuthState auth;
  final VoidCallback onReverify;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final initial = user?.displayLabel.characters.first ?? '我';
    return SectionCard(
      title: '学习通身份',
      subtitle: '普通用户无需注册第二套账号。'
          '密码、Cookie 与会话令牌不会显示在这里。',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Container(
                width: 46,
                height: 46,
                alignment: Alignment.center,
                decoration: BoxDecoration(
                  color: colors.accentSoft,
                  shape: BoxShape.circle,
                ),
                child: Text(
                  initial,
                  style: TextStyle(
                    color: colors.accent,
                    fontSize: 16,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      user?.displayLabel ?? '身份信息待加载',
                      style: TextStyle(
                        color: colors.text,
                        fontSize: 16,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const SizedBox(height: 2),
                    Text(
                      user?.schoolLabel ?? '学校信息待同步',
                      style: TextStyle(
                        color: colors.textSecondary,
                        fontSize: 12,
                      ),
                    ),
                  ],
                ),
              ),
              StatusPill(
                label: user?.platformActive == true ? '身份有效' : '需要验证',
                tone: user?.platformActive == true
                    ? StatusTone.success
                    : StatusTone.warning,
              ),
            ],
          ),
          const SizedBox(height: 16),
          Wrap(
            spacing: 18,
            runSpacing: 12,
            children: [
              _IdentityFact(
                label: '登录方式',
                value: '学习通',
              ),
              _IdentityFact(
                label: '身份验证',
                value: formatDateTime(user?.identityVerifiedAt),
              ),
              _IdentityFact(
                label: '平台会话',
                value: user == null
                    ? '—'
                    : user!.platform.isActive
                        ? '有效至 ${formatDateTime(user!.platform.expiresAt)}'
                        : user!.platform.label,
              ),
              _IdentityFact(
                label: '客户端会话',
                value: formatDateTime(user?.session.expiresAt),
              ),
            ],
          ),
          if (user?.platformActive == false) ...[
            const SizedBox(height: 14),
            InlineNotice(
              tone: StatusTone.warning,
              icon: Icons.shield_outlined,
              message: '学习通会话需要重新验证，相关任务不会继续自动执行。',
              action: TextButton(
                onPressed: onReverify,
                child: const Text('重新验证'),
              ),
            ),
          ],
          if (auth.error != null &&
              auth.status == AuthStatus.authenticated) ...[
            const SizedBox(height: 10),
            InlineNotice(
              tone: StatusTone.warning,
              message: auth.error!.userMessage,
              action: TextButton(
                onPressed: () =>
                    ref.read(authControllerProvider.notifier).refreshAccount(),
                child: const Text('刷新身份'),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _IdentityFact extends StatelessWidget {
  const _IdentityFact({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 210,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label,
            style: TextStyle(color: context.colors.muted, fontSize: 12),
          ),
          const SizedBox(height: 3),
          Text(
            value,
            style: TextStyle(
              color: context.colors.textSecondary,
              fontSize: 12,
              height: 1.5,
            ),
          ),
        ],
      ),
    );
  }
}

class _PaletteChoice extends StatelessWidget {
  const _PaletteChoice({
    required this.id,
    required this.selected,
    required this.onTap,
  });

  final ColorSchemeId id;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final palette = AppPalette.resolve(id, Theme.of(context).brightness);
    return Semantics(
        button: true,
        selected: selected,
        label: '${id.label}配色',
        child: InkWell(
          onTap: onTap,
          borderRadius: colors.cardRadius,
          child: Container(
            padding: const EdgeInsets.all(8),
            decoration: BoxDecoration(
                color: palette.background,
                border:
                    Border.all(color: selected ? colors.accent : colors.border),
                borderRadius: colors.cardRadius),
            child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  ClipRRect(
                      borderRadius: BorderRadius.circular(6),
                      child: Container(
                          height: 32,
                          color: palette.surface,
                          child: Row(children: [
                            Container(width: 12, color: palette.sidebar),
                            const SizedBox(width: 8),
                            Expanded(
                                child: Container(
                                    height: 8, color: palette.accentSoft)),
                            const SizedBox(width: 8),
                          ]))),
                  const SizedBox(height: 8),
                  Row(children: [
                    Expanded(
                        child: Text(id.label,
                            style:
                                TextStyle(color: palette.text, fontSize: 13))),
                    if (selected)
                      Icon(Icons.check, size: 14, color: palette.accent)
                  ]),
                ]),
          ),
        ));
  }
}

class _DataRequestTile extends StatelessWidget {
  const _DataRequestTile({
    required this.request,
    this.downloading = false,
    this.onDownload,
  });

  final DataRequest request;
  final bool downloading;
  final VoidCallback? onDownload;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final tone = switch (request.status) {
      'ready' || 'completed' => StatusTone.success,
      'needs_review' || 'failed' || 'rejected' => StatusTone.warning,
      'expired' => StatusTone.neutral,
      'processing' => StatusTone.info,
      _ => StatusTone.neutral,
    };
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          border: Border(bottom: BorderSide(color: colors.border)),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        request.isExport ? '账户数据导出' : '账户数据删除',
                        style: TextStyle(
                          color: colors.text,
                          fontSize: 13,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      const SizedBox(height: 3),
                      Text(
                        '提交于 ${formatDateTime(request.createdAt)}'
                        '${request.updatedAt == null ? '' : ' · 更新于 ${formatDateTime(request.updatedAt)}'}',
                        style: TextStyle(color: colors.muted, fontSize: 12),
                      ),
                    ],
                  ),
                ),
                StatusPill(
                  label: request.statusLabel,
                  tone: tone,
                  compact: true,
                ),
              ],
            ),
            if (request.detailLabel != null) ...[
              const SizedBox(height: 6),
              Text(
                request.detailLabel!,
                style: TextStyle(
                  color: colors.textSecondary,
                  fontSize: 12,
                  height: 1.5,
                ),
              ),
            ],
            if (request.isReady && onDownload != null) ...[
              const SizedBox(height: 8),
              Align(
                alignment: Alignment.centerLeft,
                child: AsyncButton(
                  label: downloading ? '准备下载' : '下载 JSON',
                  icon: Icons.download_outlined,
                  busy: downloading,
                  variant: AsyncButtonVariant.outline,
                  onPressed: onDownload,
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _DataActionRow extends StatelessWidget {
  const _DataActionRow({
    required this.icon,
    required this.title,
    required this.description,
    required this.buttonLabel,
    required this.onPressed,
    this.danger = false,
  });

  final IconData icon;
  final String title;
  final String description;
  final String buttonLabel;
  final VoidCallback? onPressed;
  final bool danger;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(
          icon,
          size: 20,
          color: danger ? colors.danger : colors.textSecondary,
        ),
        const SizedBox(width: 10),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                title,
                style: TextStyle(
                  color: colors.text,
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 3),
              Text(
                description,
                style: TextStyle(
                  color: colors.textSecondary,
                  fontSize: 12,
                  height: 1.55,
                ),
              ),
            ],
          ),
        ),
        const SizedBox(width: 10),
        OutlinedButton(
          onPressed: onPressed,
          style: danger
              ? OutlinedButton.styleFrom(
                  foregroundColor: colors.danger,
                  side: BorderSide(color: colors.danger.withValues(alpha: 0.4)),
                )
              : null,
          child: Text(buttonLabel),
        ),
      ],
    );
  }
}

class _FaqItem extends StatelessWidget {
  const _FaqItem({required this.question, required this.answer});

  final String question;
  final String answer;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          question,
          style: TextStyle(
            color: colors.text,
            fontSize: 13,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 3),
        Text(
          answer,
          style: TextStyle(
            color: colors.textSecondary,
            fontSize: 12,
            height: 1.55,
          ),
        ),
      ],
    );
  }
}

class _ServerConnectionCard extends StatelessWidget {
  const _ServerConnectionCard({required this.config, required this.onOpen});

  final ServerConfigState config;
  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return SectionCard(
      title: '服务连接',
      action: OutlinedButton.icon(
        onPressed: onOpen,
        icon: const Icon(Icons.link, size: 15),
        label: const Text('切换服务'),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            config.configured ? '当前服务' : '尚未连接服务',
            style: TextStyle(color: colors.muted, fontSize: 12),
          ),
          const SizedBox(height: 3),
          Text(
            config.baseUri?.toString() ?? '请先连接服务后再验证学习通身份',
            style: TextStyle(
              color: colors.textSecondary,
              fontSize: 12,
              height: 1.5,
            ),
          ),
        ],
      ),
    );
  }
}
