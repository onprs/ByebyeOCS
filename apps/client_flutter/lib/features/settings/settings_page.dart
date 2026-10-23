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
    return SingleChildScrollView(
      padding: EdgeInsets.only(bottom: context.isNarrow ? 12 : 28),
      child: ResponsiveContent(
        maxWidth: 960,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const PageHeader(
              eyebrow: '个人设置',
              title: '身份、外观与隐私',
              description: '管理学习通身份状态、外观偏好、通知与数据请求。',
            ),
            _IdentityCard(
              ref: ref,
              user: user,
              auth: auth,
              onReverify: _logoutForReverify,
            ),
            if (!kIsWeb) ...[
              const SizedBox(height: 14),
              _ServerConnectionCard(
                config: ref.watch(serverConfigProvider),
                onOpen: () => context.go('/connect'),
              ),
            ],
            const SizedBox(height: 14),
            SectionCard(
              title: '外观',
              subtitle: '选择配色与浅深模式。未登录时使用本机选择，登录后跟随账户偏好。',
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
                      final columns = constraints.maxWidth >= 620 ? 3 : 1;
                      final spacing = 10.0;
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
            const SizedBox(height: 14),
            SectionCard(
              title: '通知',
              subtitle: '通知偏好保存到账户，跨设备保持一致。',
              child: preferencesAsync.when(
                loading: () => const LoadingList(rows: 2, height: 48),
                error: (error, _) => ErrorPanel(
                  message:
                      error is ApiException ? error.userMessage : '通知偏好暂时无法加载',
                  compact: true,
                  onRetry: () =>
                      ref.read(preferencesControllerProvider.notifier).reload(),
                ),
                data: (snapshot) => Column(
                  children: [
                    Material(
                      color: Colors.transparent,
                      child: SwitchListTile(
                        value: snapshot.preferences.taskAttention,
                        onChanged: appearance.syncing
                            ? null
                            : (value) => _savePreferences(taskAttention: value),
                        contentPadding: EdgeInsets.zero,
                        title: const Text('任务需要处理时提醒'),
                        subtitle: const Text('身份失效、平台限制或任务需要人工处理时显示提醒。'),
                      ),
                    ),
                    Divider(color: context.colors.border),
                    Material(
                      color: Colors.transparent,
                      child: SwitchListTile(
                        value: snapshot.preferences.walletChanges,
                        onChanged: appearance.syncing
                            ? null
                            : (value) => _savePreferences(walletChanges: value),
                        contentPadding: EdgeInsets.zero,
                        title: const Text('钱包状态变化时提醒'),
                        subtitle: const Text('充值到账与账本变动会在应用内提示。'),
                      ),
                    ),
                  ],
                ),
              ),
            ),
            const SizedBox(height: 14),
            if (!auth.capabilities.questionsExplicitlyDisabled)
              SectionCard(
                title: '题库查询',
                subtitle: '输入题目后查询答案；发现答案或缓存有误可以报告复核。',
                action: TextButton.icon(
                  onPressed: () => context.go('/questions'),
                  icon: const Icon(Icons.arrow_forward, size: 15),
                  label: const Text('打开'),
                ),
                child: Text(
                  '单独查询不写入课程任务，安排内的测验仍由任务自动完成。',
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 11.5,
                    height: 1.5,
                  ),
                ),
              ),
            const SizedBox(height: 14),
            SectionCard(
              title: '数据与隐私',
              subtitle: '导出和删除的处理状态与文件以下发结果为准。',
              action: TextButton.icon(
                onPressed: privacyAsync.valueOrNull?.refreshing == true
                    ? null
                    : () =>
                        ref.read(privacyControllerProvider.notifier).refresh(),
                icon: const Icon(Icons.refresh, size: 15),
                label: const Text('刷新状态'),
              ),
              child: _buildPrivacy(context, privacyAsync),
            ),
            const SizedBox(height: 14),
            SectionCard(
              title: '帮助与安全',
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  _FaqItem(
                    question: '为什么任务显示等待验证？',
                    answer: '执行结束后需要再次查询学习通。平台确认前不会计为完成，也不会因本地计时自动变为成功。',
                  ),
                  Divider(height: 20, color: colors.border),
                  _FaqItem(
                    question: '支付成功但余额没有变化？',
                    answer: '支付成功和余额到账是两个步骤。请回到钱包查询订单状态，余额以账户流水为准。',
                  ),
                  Divider(height: 20, color: colors.border),
                  _FaqItem(
                    question: '学习通密码忘记怎么办？',
                    answer: '请在学习通官方入口找回密码。本平台不会重置学习通密码，也不会接收你的密码用于后续自动登录。',
                  ),
                ],
              ),
            ),
            const SizedBox(height: 14),
            Container(
              padding: const EdgeInsets.all(16),
              decoration: BoxDecoration(
                color: colors.surface,
                borderRadius: colors.cardRadius,
                border: Border.all(color: colors.border),
              ),
              child: Row(
                children: [
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          '退出登录',
                          style: TextStyle(
                            color: colors.text,
                            fontSize: 13.5,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                        const SizedBox(height: 3),
                        Text(
                          '退出会清除本机会话与用户缓存。持续安排是否继续运行以当前授权状态为准。',
                          style: TextStyle(
                            color: colors.textSecondary,
                            fontSize: 11.5,
                            height: 1.5,
                          ),
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(width: 12),
                  AsyncButton(
                    label: '退出登录',
                    icon: Icons.logout,
                    busy: auth.busy,
                    variant: AsyncButtonVariant.outline,
                    onPressed: _logout,
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
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
              fontSize: 11.5,
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
              fontSize: 11.5,
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
                    fontSize: 18,
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
            style: TextStyle(color: context.colors.muted, fontSize: 10.5),
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
    final dark = Theme.of(context).brightness == Brightness.dark;
    final palette =
        AppPalette.resolve(id, dark ? Brightness.dark : Brightness.light);
    return Semantics(
      button: true,
      selected: selected,
      label: '${id.label}配色',
      child: InkWell(
        onTap: onTap,
        borderRadius: colors.cardRadius,
        child: Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: palette.background,
            borderRadius: colors.cardRadius,
            border: Border.all(
              color: selected ? colors.accent : colors.border,
              width: selected ? 1.6 : 1,
            ),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  for (final color in [
                    palette.surface,
                    palette.accent,
                    palette.heroBg,
                    palette.sidebar,
                  ])
                    Container(
                      width: 20,
                      height: 20,
                      margin: const EdgeInsets.only(right: 5),
                      decoration: BoxDecoration(
                        color: color,
                        shape: BoxShape.circle,
                        border: Border.all(color: palette.border),
                      ),
                    ),
                  const Spacer(),
                  if (selected)
                    Icon(
                      Icons.check_circle,
                      size: 17,
                      color: palette.accent,
                    ),
                ],
              ),
              const SizedBox(height: 10),
              Text(
                id.label,
                style: TextStyle(
                  color: palette.text,
                  fontSize: 13.5,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 2),
              Text(
                switch (id) {
                  ColorSchemeId.graphite => '清晰克制，信息密度高',
                  ColorSchemeId.grove => '暖白与森林绿，舒展留白',
                  ColorSchemeId.indigo => '墨蓝与靛蓝，品牌感更强',
                },
                style: TextStyle(
                  color: palette.textSecondary,
                  fontSize: 10.5,
                  height: 1.4,
                ),
              ),
            ],
          ),
        ),
      ),
    );
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
          color: colors.surfaceSubtle,
          borderRadius: BorderRadius.circular(colors.radius - 2),
          border: Border.all(color: colors.border),
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
                          fontSize: 12.5,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      const SizedBox(height: 3),
                      Text(
                        '提交于 ${formatDateTime(request.createdAt)}'
                        '${request.updatedAt == null ? '' : ' · 更新于 ${formatDateTime(request.updatedAt)}'}',
                        style: TextStyle(color: colors.muted, fontSize: 10.5),
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
                  fontSize: 11.5,
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
                  fontSize: 11.5,
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
            fontSize: 12.5,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 3),
        Text(
          answer,
          style: TextStyle(
            color: colors.textSecondary,
            fontSize: 11.5,
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
      subtitle: '切换服务会退出当前登录，并清除本机会话、令牌与用户缓存。',
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
            style: TextStyle(color: colors.muted, fontSize: 10.5),
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
