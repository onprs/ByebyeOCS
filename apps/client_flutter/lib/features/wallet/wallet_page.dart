import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/api/api_error.dart';
import '../../core/api/json.dart';
import '../../core/format/formatters.dart';
import '../../core/models/question.dart';
import '../../core/models/plan.dart';
import '../../core/models/wallet.dart';
import '../../core/platform/external_link.dart';
import '../../design/components.dart';
import '../../design/theme.dart';
import 'wallet_controller.dart';

enum _WalletTab { orders, entries, usage }

class WalletPage extends ConsumerStatefulWidget {
  const WalletPage({super.key, this.orderId});

  final String? orderId;

  @override
  ConsumerState<WalletPage> createState() => _WalletPageState();
}

class _WalletPageState extends ConsumerState<WalletPage> {
  final _amountController = TextEditingController();
  final _scrollController = ScrollController();
  _WalletTab _tab = _WalletTab.orders;
  String? _selectedChannel;
  String? _idempotencyKey;
  String? _activeOrderId;
  String? _amountError;
  bool _returnHandled = false;
  bool _launchingPayment = false;

  @override
  void initState() {
    super.initState();
    _activeOrderId = widget.orderId;
    WidgetsBinding.instance.addPostFrameCallback((_) => _handleReturn());
  }

  @override
  void dispose() {
    _amountController.dispose();
    _scrollController.dispose();
    super.dispose();
  }

  Future<void> _handleReturn() async {
    if (_returnHandled || !mounted) return;
    _returnHandled = true;
    if (widget.orderId != null && widget.orderId!.isNotEmpty) {
      await ref
          .read(walletControllerProvider.notifier)
          .refreshAfterReturn(widget.orderId);
    }
  }

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(walletControllerProvider);
    return RefreshIndicator(
      onRefresh: () =>
          ref.read(walletControllerProvider.notifier).refresh(silent: true),
      child: SingleChildScrollView(
        controller: _scrollController,
        physics: const AlwaysScrollableScrollPhysics(),
        padding: EdgeInsets.only(bottom: context.isNarrow ? 12 : 28),
        child: ResponsiveContent(
          child: async.when(
            loading: () => const LoadingList(rows: 3, height: 160),
            error: (error, _) => ErrorPanel(
              message: error is ApiException ? error.userMessage : '钱包暂时无法加载',
              requestId: error is ApiException ? error.requestId : null,
              onRetry: () => ref.invalidate(walletControllerProvider),
            ),
            data: (data) => _buildData(context, data),
          ),
        ),
      ),
    );
  }

  Widget _buildData(BuildContext context, WalletState data) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        PageHeader(
          eyebrow: '我的钱包',
          title: '余额与订单',
          description: '付款成功与余额到账分开记录，余额以账户流水为准。',
          actions: [
            AsyncButton(
              label: '刷新',
              icon: Icons.refresh,
              busy: data.refreshing,
              variant: AsyncButtonVariant.outline,
              onPressed: () => ref
                  .read(walletControllerProvider.notifier)
                  .refresh(silent: true),
            ),
          ],
        ),
        if (data.actionError != null || data.notice != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 16),
            child: InlineNotice(
              tone: data.actionError != null
                  ? StatusTone.warning
                  : StatusTone.success,
              icon: data.actionError != null
                  ? Icons.info_outline
                  : Icons.check_circle_outline,
              message: data.actionError ?? data.notice!,
              action: IconButton(
                tooltip: '关闭',
                onPressed: () =>
                    ref.read(walletControllerProvider.notifier).clearBanner(),
                icon: const Icon(Icons.close, size: 17),
              ),
            ),
          ),
        LayoutBuilder(
          builder: (context, constraints) {
            final wide = constraints.maxWidth >= 900;
            final balance = _BalanceCard(data: data);
            final recharge = _RechargeCard(
              data: data,
              amountController: _amountController,
              amountError: _amountError,
              selectedChannel: _selectedChannel,
              activeOrder: _activeOrderId == null
                  ? null
                  : data.orderById(_activeOrderId!),
              launchingPayment: _launchingPayment,
              onChannelChanged: (value) =>
                  setState(() => _selectedChannel = value),
              onCreate: _createOrder,
              onPay: _openPayment,
              onRefreshOrder: _refreshOrder,
            );
            if (!wide) {
              return Column(
                children: [
                  balance,
                  const SizedBox(height: 14),
                  recharge,
                ],
              );
            }
            return Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(child: balance),
                const SizedBox(width: 16),
                Expanded(flex: 2, child: recharge),
              ],
            );
          },
        ),
        if (data.pricing != null) ...[
          const SizedBox(height: 14),
          _PricingSummary(pricing: data.pricing!),
        ],
        const SizedBox(height: 18),
        _WalletTabs(
          selected: _tab,
          data: data,
          onChanged: (value) => setState(() => _tab = value),
        ),
        const SizedBox(height: 12),
        SectionCard(
          padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
          title: switch (_tab) {
            _WalletTab.orders => '充值订单',
            _WalletTab.entries => '资金流水',
            _WalletTab.usage => '用量记录',
          },
          subtitle: switch (_tab) {
            _WalletTab.orders => '支付成功与到账状态分别展示。',
            _WalletTab.entries => '充值、预占、结算与释放都会记入账户流水。',
            _WalletTab.usage => '题库按 token 记录；未定价时不显示虚假金额。',
          },
          action: switch (_tab) {
            _WalletTab.orders => data.ordersCursor == null
                ? null
                : TextButton(
                    onPressed: () => ref
                        .read(walletControllerProvider.notifier)
                        .loadOrders(),
                    child: const Text('加载更多'),
                  ),
            _WalletTab.entries => data.entriesCursor == null
                ? null
                : TextButton(
                    onPressed: () => ref
                        .read(walletControllerProvider.notifier)
                        .loadEntries(),
                    child: const Text('加载更多'),
                  ),
            _WalletTab.usage => data.usageCursor == null
                ? null
                : TextButton(
                    onPressed: () =>
                        ref.read(walletControllerProvider.notifier).loadUsage(),
                    child: const Text('加载更多'),
                  ),
          },
          child: switch (_tab) {
            _WalletTab.orders => _buildOrders(context, data),
            _WalletTab.entries => _buildEntries(context, data),
            _WalletTab.usage => _buildUsage(context, data),
          },
        ),
        const SizedBox(height: 16),
        InlineNotice(
          icon: Icons.lock_outline,
          message: data.pricing == null
              ? '计费信息暂不可用。余额和订单仍以实际记录为准。'
              : data.pricing!.automaticCharging
                  ? '服务按平台确认的有效任务点计量，题库按 token 单独记录。'
                  : '单价尚未全部公布，当前未启用自动扣费。',
        ),
      ],
    );
  }

  Future<void> _createOrder() async {
    final data = ref.read(walletControllerProvider).valueOrNull;
    if (data == null) return;
    final pricing = data.pricing;
    if (pricing == null || !pricing.recharge.enabled) {
      setState(() => _amountError = '充值暂未开放');
      return;
    }
    final minor = parseYuanInput(_amountController.text);
    final min = pricing.recharge.minMinor;
    final max = pricing.recharge.maxMinor;
    if (minor == null || minor <= 0) {
      setState(() => _amountError = '请输入有效充值金额，最多两位小数');
      return;
    }
    if (min != null && minor < min) {
      setState(() => _amountError = '充值金额不能低于 ${Money(min).text}');
      return;
    }
    if (max != null && minor > max) {
      setState(() => _amountError = '充值金额不能高于 ${Money(max).text}');
      return;
    }
    final channels = pricing.recharge.channels;
    final channel =
        _selectedChannel != null && channels.contains(_selectedChannel)
            ? _selectedChannel!
            : channels.isNotEmpty
                ? channels.first
                : null;
    if (channel == null) {
      setState(() => _amountError = '当前没有可用的支付方式');
      return;
    }
    setState(() {
      _amountError = null;
      _selectedChannel = channel;
      _idempotencyKey ??= newIdempotencyKey();
    });
    final order = await ref.read(walletControllerProvider.notifier).createOrder(
          amountMinor: minor,
          channel: channel,
          idempotencyKey: _idempotencyKey!,
        );
    if (!mounted) return;
    if (order != null) {
      setState(() {
        _activeOrderId = order.id;
        _idempotencyKey = null;
      });
      final message = order.hasCheckout
          ? '订单已创建，请完成支付后回到本页查询到账状态。'
          : '订单已创建，支付入口暂不可用。请稍后查询订单状态。';
      showAppMessage(context, message);
    }
  }

  Future<void> _openPayment(RechargeOrder order) async {
    if (_launchingPayment) return;
    final url = order.checkoutUrl;
    if (url == null || url.isEmpty) {
      showAppMessage(context, '当前订单没有可用的支付入口');
      return;
    }
    setState(() => _launchingPayment = true);
    final opened = await openExternalHttps(url);
    if (!mounted) return;
    setState(() => _launchingPayment = false);
    if (!opened) {
      showAppMessage(context, '无法打开支付页面');
    } else {
      showAppMessage(context, '支付完成后请返回本页查询到账状态');
    }
  }

  Future<void> _refreshOrder(String orderId) async {
    final order =
        await ref.read(walletControllerProvider.notifier).refreshOrder(orderId);
    if (!mounted) return;
    if (order != null) {
      showAppMessage(context, order.stageLabel);
    }
  }

  Widget _buildOrders(BuildContext context, WalletState data) {
    if (data.ordersError != null) {
      return ErrorPanel(
        message: data.ordersError!,
        compact: true,
        onRetry: () => ref.read(walletControllerProvider.notifier).refresh(),
      );
    }
    if (data.orders.isEmpty) {
      return const EmptyState(
        icon: Icons.receipt_long_outlined,
        title: '还没有充值订单',
        message: '创建充值订单后，可在这里查看到账进度。',
        compact: true,
      );
    }
    return Column(
      children: [
        for (final order in data.orders)
          _OrderTile(
            order: order,
            active: order.id == _activeOrderId,
            onOpen: () => _showOrder(order.id),
            onQuery: () => _refreshOrder(order.id),
            onPay: () => _openPayment(order),
          ),
      ],
    );
  }

  Widget _buildEntries(BuildContext context, WalletState data) {
    if (data.entriesError != null) {
      return ErrorPanel(
        message: data.entriesError!,
        compact: true,
        onRetry: () => ref.read(walletControllerProvider.notifier).refresh(),
      );
    }
    if (data.entries.isEmpty) {
      return const EmptyState(
        icon: Icons.swap_vert_outlined,
        title: '还没有资金流水',
        message: '充值到账、任务消费与预占释放会记录在这里。',
        compact: true,
      );
    }
    return Column(
      children: [
        for (final entry in data.entries) _EntryTile(entry: entry),
      ],
    );
  }

  Widget _buildUsage(BuildContext context, WalletState data) {
    final colors = context.colors;
    final cacheHits = data.queries.where((query) => query.cacheHit).length;
    final totalTokens = data.queries.fold<int>(
      0,
      (sum, query) => sum + query.usage.totalTokens,
    );
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Wrap(
          spacing: 10,
          runSpacing: 10,
          children: [
            _UsageMetric(label: '查询记录', value: '${data.queries.length}'),
            _UsageMetric(label: '缓存命中', value: '$cacheHits'),
            _UsageMetric(label: '合计 token', value: formatCount(totalTokens)),
          ],
        ),
        const SizedBox(height: 16),
        if (data.queries.isNotEmpty) ...[
          Text(
            '题库查询',
            style: TextStyle(
              color: colors.text,
              fontSize: 13,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 8),
          for (final query in data.queries) _QueryUsageTile(query: query),
        ] else if (data.queryDetailsAvailable)
          Text(
            '还没有题库查询记录。',
            style: TextStyle(color: colors.textSecondary, fontSize: 12.5),
          )
        else
          const InlineNotice(
            icon: Icons.info_outline,
            message: '详细的题库查询记录暂不可用；已结算用量仍会在下方显示。',
          ),
        if (data.usage.isNotEmpty) ...[
          const SizedBox(height: 16),
          Text(
            '计费用量',
            style: TextStyle(
              color: colors.text,
              fontSize: 13,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 8),
          for (final usage in data.usage) _LedgerUsageTile(usage: usage),
        ],
        if (data.usageError != null) ...[
          const SizedBox(height: 10),
          ErrorPanel(
            message: data.usageError!,
            compact: true,
            onRetry: () =>
                ref.read(walletControllerProvider.notifier).refresh(),
          ),
        ],
      ],
    );
  }

  Future<void> _showOrder(String orderId) async {
    final result = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      builder: (context) => _OrderDetailSheet(orderId: orderId),
    );
    if (result == true) {
      final data = ref.read(walletControllerProvider).valueOrNull;
      final order = data?.orderById(orderId);
      if (order != null && order.hasCheckout) {
        await _openPayment(order);
      }
    }
  }
}

class _BalanceCard extends StatelessWidget {
  const _BalanceCard({required this.data});

  final WalletState data;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final wallet = data.wallet;
    return Container(
      padding: const EdgeInsets.all(20),
      decoration: BoxDecoration(
        color: colors.heroBg,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(
                width: 34,
                height: 34,
                decoration: BoxDecoration(
                  color: colors.surface.withValues(alpha: 0.6),
                  borderRadius: BorderRadius.circular(9),
                ),
                child: Icon(
                  Icons.account_balance_wallet_outlined,
                  size: 18,
                  color: colors.heroText,
                ),
              ),
              const SizedBox(width: 10),
              Text(
                '可用余额',
                style: TextStyle(
                  color: colors.heroText.withValues(alpha: 0.8),
                  fontSize: 12.5,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const Spacer(),
              if (wallet.frozen)
                const StatusPill(
                  label: '钱包已冻结',
                  tone: StatusTone.warning,
                  compact: true,
                ),
            ],
          ),
          const SizedBox(height: 20),
          Text(
            wallet.available.text,
            style: TextStyle(
              color: colors.heroText,
              fontSize: 38,
              height: 1.1,
              fontWeight: FontWeight.w600,
              letterSpacing: -1,
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
          const SizedBox(height: 8),
          Text(
            '预占 ${wallet.reserved.text} · ${wallet.currency}',
            style: TextStyle(
              color: colors.heroText.withValues(alpha: 0.7),
              fontSize: 12,
            ),
          ),
          if (wallet.frozen && wallet.frozenReason != null) ...[
            const SizedBox(height: 8),
            Text(
              wallet.frozenReason!,
              style: TextStyle(
                color: colors.heroText,
                fontSize: 11.5,
                height: 1.5,
              ),
            ),
          ],
          const SizedBox(height: 18),
          Divider(color: colors.heroText.withValues(alpha: 0.14)),
          const SizedBox(height: 12),
          Text(
            '余额以账户流水为准。支付返回不会直接增加余额。',
            style: TextStyle(
              color: colors.heroText.withValues(alpha: 0.7),
              fontSize: 11.5,
              height: 1.5,
            ),
          ),
          if (wallet.updatedAt != null) ...[
            const SizedBox(height: 4),
            Text(
              '更新于 ${formatDateTime(wallet.updatedAt)}',
              style: TextStyle(
                color: colors.heroText.withValues(alpha: 0.5),
                fontSize: 10.5,
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _RechargeCard extends ConsumerWidget {
  const _RechargeCard({
    required this.data,
    required this.amountController,
    required this.amountError,
    required this.selectedChannel,
    required this.activeOrder,
    required this.launchingPayment,
    required this.onChannelChanged,
    required this.onCreate,
    required this.onPay,
    required this.onRefreshOrder,
  });

  final WalletState data;
  final TextEditingController amountController;
  final String? amountError;
  final String? selectedChannel;
  final RechargeOrder? activeOrder;
  final bool launchingPayment;
  final ValueChanged<String> onChannelChanged;
  final VoidCallback onCreate;
  final ValueChanged<RechargeOrder> onPay;
  final ValueChanged<String> onRefreshOrder;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = context.colors;
    final pricing = data.pricing;
    final recharge = pricing?.recharge;
    final enabled = recharge?.enabled == true;
    final channels = recharge?.channels ?? const <String>[];
    final selected =
        selectedChannel != null && channels.contains(selectedChannel)
            ? selectedChannel!
            : channels.isNotEmpty
                ? channels.first
                : null;
    return SectionCard(
      title: '充值',
      subtitle: '选择金额与支付方式；支付成功后还需要等待到账。',
      action: pricing == null
          ? null
          : StatusPill(
              label: enabled ? '充值可用' : '充值未开放',
              tone: enabled ? StatusTone.success : StatusTone.warning,
              compact: true,
            ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (data.pricingError != null)
            Padding(
              padding: const EdgeInsets.only(bottom: 12),
              child: ErrorPanel(
                message: data.pricingError!,
                compact: true,
                onRetry: () =>
                    ref.read(walletControllerProvider.notifier).refresh(),
              ),
            ),
          if (pricing == null)
            const InlineNotice(
              icon: Icons.hourglass_empty,
              message: '计费与支付配置正在加载。',
            )
          else if (!enabled)
            InlineNotice(
              tone: StatusTone.warning,
              icon: Icons.lock_outline,
              message: recharge?.state == 'not_configured'
                  ? '充值支付尚未配置或运营未启用。'
                  : '充值当前不可用，请稍后再试。',
            )
          else ...[
            TextField(
              controller: amountController,
              enabled: !data.creatingOrder,
              keyboardType:
                  const TextInputType.numberWithOptions(decimal: true),
              inputFormatters: [
                FilteringTextInputFormatter.allow(
                    RegExp(r'^\d{0,9}(\.\d{0,2})?')),
              ],
              decoration: InputDecoration(
                labelText: '充值金额',
                prefixText: '¥ ',
                hintText: '0.00',
                errorText: amountError,
                helperText: _rangeLabel(recharge),
              ),
            ),
            const SizedBox(height: 14),
            Text(
              '支付方式',
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 8),
            if (channels.isEmpty)
              const InlineNotice(
                tone: StatusTone.warning,
                message: '当前没有可用的支付方式。',
              )
            else
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  for (final channel in channels)
                    ChoiceChip(
                      label: Text(_channelLabel(channel)),
                      selected: selected == channel,
                      onSelected: data.creatingOrder
                          ? null
                          : (_) => onChannelChanged(channel),
                    ),
                ],
              ),
            const SizedBox(height: 14),
            AsyncButton(
              label: '创建充值订单',
              icon: Icons.add_card_outlined,
              busy: data.creatingOrder,
              expand: true,
              onPressed: channels.isEmpty ? null : onCreate,
            ),
            if (activeOrder != null) ...[
              const SizedBox(height: 16),
              _ActiveOrderCard(
                order: activeOrder!,
                launching: launchingPayment,
                onPay: () => onPay(activeOrder!),
                onRefresh: () => onRefreshOrder(activeOrder!.id),
              ),
            ],
            const SizedBox(height: 10),
            Text(
              '充值到账余额原则上不退回；异常支付与必要更正单独处理。',
              style: TextStyle(
                color: colors.muted,
                fontSize: 10.5,
                height: 1.5,
              ),
            ),
          ],
        ],
      ),
    );
  }

  String? _rangeLabel(RechargePolicy? recharge) {
    if (recharge == null) return null;
    final min = recharge.minMinor;
    final max = recharge.maxMinor;
    if (min == null && max == null) return null;
    if (min != null && max != null) {
      return '单笔 ${Money(min).text} – ${Money(max).text}';
    }
    if (min != null) return '单笔不低于 ${Money(min).text}';
    return '单笔不高于 ${Money(max!).text}';
  }

  String _channelLabel(String channel) {
    return switch (channel) {
      'alipay' => '支付宝',
      'wxpay' => '微信支付',
      _ => channel,
    };
  }
}

class _ActiveOrderCard extends StatelessWidget {
  const _ActiveOrderCard({
    required this.order,
    required this.launching,
    required this.onPay,
    required this.onRefresh,
  });

  final RechargeOrder order;
  final bool launching;
  final VoidCallback onPay;
  final VoidCallback onRefresh;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      padding: const EdgeInsets.all(13),
      decoration: BoxDecoration(
        color: colors.surfaceSubtle,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  '订单 ${order.amount.text}',
                  style: TextStyle(
                    color: colors.text,
                    fontSize: 13.5,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              StatusPill(
                label: order.stageLabel,
                tone: _stageTone(order.stage),
                compact: true,
              ),
            ],
          ),
          const SizedBox(height: 10),
          _StagePipeline(stage: order.stage),
          const SizedBox(height: 10),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              if (order.hasCheckout)
                AsyncButton(
                  label: '去支付',
                  icon: Icons.open_in_new,
                  busy: launching,
                  onPressed: onPay,
                ),
              AsyncButton(
                label: '查询状态',
                icon: Icons.refresh,
                variant: AsyncButtonVariant.outline,
                onPressed: onRefresh,
              ),
            ],
          ),
          if (!order.hasCheckout && order.stage == RechargeStage.pendingPayment)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(
                order.checkoutState == 'not_configured'
                    ? '支付入口尚未配置，请联系服务提供方。'
                    : '当前订单没有可用的支付入口。',
                style: TextStyle(color: colors.warning, fontSize: 11),
              ),
            ),
        ],
      ),
    );
  }
}

class _WalletTabs extends StatelessWidget {
  const _WalletTabs({
    required this.selected,
    required this.data,
    required this.onChanged,
  });

  final _WalletTab selected;
  final WalletState data;
  final ValueChanged<_WalletTab> onChanged;

  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: 8,
      runSpacing: 8,
      children: [
        _TabButton(
          label: '订单 ${data.orders.length}',
          selected: selected == _WalletTab.orders,
          onTap: () => onChanged(_WalletTab.orders),
        ),
        _TabButton(
          label: '流水 ${data.entries.length}',
          selected: selected == _WalletTab.entries,
          onTap: () => onChanged(_WalletTab.entries),
        ),
        _TabButton(
          label: '题库用量 ${data.queries.length}',
          selected: selected == _WalletTab.usage,
          onTap: () => onChanged(_WalletTab.usage),
        ),
      ],
    );
  }
}

class _TabButton extends StatelessWidget {
  const _TabButton({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(999),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 9),
        decoration: BoxDecoration(
          color: selected ? colors.accentSoft : colors.surface,
          borderRadius: BorderRadius.circular(999),
          border: Border.all(
            color: selected
                ? colors.accent.withValues(alpha: 0.25)
                : colors.border,
          ),
        ),
        child: Text(
          label,
          style: TextStyle(
            color: selected ? colors.accent : colors.textSecondary,
            fontSize: 12,
            fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
          ),
        ),
      ),
    );
  }
}

class _OrderTile extends StatelessWidget {
  const _OrderTile({
    required this.order,
    required this.active,
    required this.onOpen,
    required this.onQuery,
    required this.onPay,
  });

  final RechargeOrder order;
  final bool active;
  final VoidCallback onOpen;
  final VoidCallback onQuery;
  final VoidCallback onPay;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: InkWell(
        onTap: onOpen,
        borderRadius: BorderRadius.circular(colors.radius - 2),
        child: Container(
          padding: const EdgeInsets.all(13),
          decoration: BoxDecoration(
            color: active ? colors.accentSoft : colors.surfaceSubtle,
            borderRadius: BorderRadius.circular(colors.radius - 2),
            border: Border.all(
              color: active
                  ? colors.accent.withValues(alpha: 0.25)
                  : colors.border,
            ),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      order.amount.text,
                      style: TextStyle(
                        color: colors.text,
                        fontSize: 15,
                        fontWeight: FontWeight.w600,
                        fontFeatures: const [FontFeature.tabularFigures()],
                      ),
                    ),
                  ),
                  StatusPill(
                    label: order.stageLabel,
                    tone: _stageTone(order.stage),
                    compact: true,
                  ),
                ],
              ),
              const SizedBox(height: 4),
              Text(
                '${order.channelLabel} · 创建于 ${formatDateTime(order.createdAt)}',
                style: TextStyle(color: colors.textSecondary, fontSize: 11.5),
              ),
              const SizedBox(height: 8),
              Wrap(
                spacing: 6,
                runSpacing: 6,
                children: [
                  if (order.hasCheckout)
                    TextButton(
                      onPressed: onPay,
                      child: const Text('去支付'),
                    ),
                  if (order.stage == RechargeStage.pendingPayment ||
                      order.stage == RechargeStage.paidWaitingCredit)
                    TextButton(
                      onPressed: onQuery,
                      child: const Text('查询状态'),
                    ),
                  TextButton(
                    onPressed: onOpen,
                    child: const Text('查看订单'),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _EntryTile extends StatelessWidget {
  const _EntryTile({required this.entry});

  final WalletEntry entry;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final positive = entry.availableDeltaMinor > 0;
    final negative = entry.availableDeltaMinor < 0;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: colors.surfaceSubtle,
          borderRadius: BorderRadius.circular(colors.radius - 2),
          border: Border.all(color: colors.border),
        ),
        child: Row(
          children: [
            Container(
              width: 30,
              height: 30,
              decoration: BoxDecoration(
                color: positive
                    ? colors.successBg
                    : negative
                        ? colors.warningBg
                        : colors.surface,
                borderRadius: BorderRadius.circular(7),
              ),
              child: Icon(
                positive
                    ? Icons.south_west
                    : negative
                        ? Icons.north_east
                        : Icons.swap_horiz,
                size: 15,
                color: positive
                    ? colors.success
                    : negative
                        ? colors.warning
                        : colors.textSecondary,
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    entry.label,
                    style: TextStyle(
                      color: colors.text,
                      fontSize: 12.5,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    entry.reason ??
                        (entry.reservedDeltaMinor != 0
                            ? '预占变动 ${Money(entry.reservedDeltaMinor).signedText}'
                            : '账本记录'),
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 11,
                    ),
                  ),
                ],
              ),
            ),
            Column(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Text(
                  entry.availableDelta.signedText,
                  style: TextStyle(
                    color: positive
                        ? colors.success
                        : negative
                            ? colors.text
                            : colors.textSecondary,
                    fontSize: 12.5,
                    fontWeight: FontWeight.w600,
                    fontFeatures: const [FontFeature.tabularFigures()],
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  formatShortDateTime(entry.createdAt),
                  style: TextStyle(color: colors.muted, fontSize: 10),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _QueryUsageTile extends StatelessWidget {
  const _QueryUsageTile({required this.query});

  final QuestionQuery query;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
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
              children: [
                Expanded(
                  child: Text(
                    query.modelName ?? '题库查询',
                    style: TextStyle(
                      color: colors.text,
                      fontSize: 12.5,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
                StatusPill(
                  label: query.cacheHit ? '缓存命中 · 0 token' : '模型作答',
                  tone: query.cacheHit ? StatusTone.success : StatusTone.accent,
                  compact: true,
                ),
              ],
            ),
            const SizedBox(height: 4),
            Text(
              '${query.mode.label} · 输入 ${formatCount(query.usage.inputTokens)} · 输出 ${formatCount(query.usage.outputTokens)} · 合计 ${formatCount(query.usage.totalTokens)} token',
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 11,
                height: 1.5,
              ),
            ),
            const SizedBox(height: 2),
            Text(
              '${query.pricing.label}${query.createdAt == null ? '' : ' · ${formatShortDateTime(query.createdAt)}'}',
              style: TextStyle(color: colors.muted, fontSize: 10.5),
            ),
          ],
        ),
      ),
    );
  }
}

class _LedgerUsageTile extends StatelessWidget {
  const _LedgerUsageTile({required this.usage});

  final UsageRecord usage;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: colors.surfaceSubtle,
          borderRadius: BorderRadius.circular(colors.radius - 2),
          border: Border.all(color: colors.border),
        ),
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    usage.scopeLabel,
                    style: TextStyle(
                      color: colors.text,
                      fontSize: 12.5,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 3),
                  Text(
                    usage.scope == 'tiku_tokens'
                        ? '输入 ${formatCount(usage.inputTokens)} · 输出 ${formatCount(usage.outputTokens)} · 合计 ${formatCount(usage.totalTokens)} token'
                        : '平台确认计量 · ${usage.usageComplete ? '用量完整' : '用量待核对'}',
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 11,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    formatShortDateTime(usage.createdAt),
                    style: TextStyle(color: colors.muted, fontSize: 10),
                  ),
                ],
              ),
            ),
            Column(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Text(
                  usage.chargeLabel,
                  style: TextStyle(
                    color: usage.chargedMinor > 0
                        ? colors.text
                        : colors.textSecondary,
                    fontSize: 12.5,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  usage.statusLabel,
                  style: TextStyle(color: colors.muted, fontSize: 10),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _UsageMetric extends StatelessWidget {
  const _UsageMetric({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      width: 130,
      padding: const EdgeInsets.all(11),
      decoration: BoxDecoration(
        color: colors.surfaceSubtle,
        borderRadius: BorderRadius.circular(colors.radius - 3),
        border: Border.all(color: colors.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            value,
            style: TextStyle(
              color: colors.text,
              fontSize: 17,
              fontWeight: FontWeight.w600,
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
          const SizedBox(height: 2),
          Text(
            label,
            style: TextStyle(color: colors.textSecondary, fontSize: 10.5),
          ),
        ],
      ),
    );
  }
}

class _StagePipeline extends StatelessWidget {
  const _StagePipeline({required this.stage});

  final RechargeStage stage;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final steps = const [
      '订单已创建',
      '待支付',
      '支付成功 · 入账处理中',
      '已到账',
    ];
    final activeIndex = switch (stage) {
      RechargeStage.pendingPayment => 1,
      RechargeStage.paidWaitingCredit => 2,
      RechargeStage.credited => 3,
      _ => -1,
    };
    final failed = stage == RechargeStage.needsReview ||
        stage == RechargeStage.closed ||
        stage == RechargeStage.refunded;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            for (var index = 0; index < steps.length; index++) ...[
              Container(
                width: 18,
                height: 18,
                alignment: Alignment.center,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  color: failed
                      ? colors.dangerBg
                      : index <= activeIndex
                          ? colors.accent
                          : colors.surface,
                  border: Border.all(
                    color: failed
                        ? colors.danger
                        : index <= activeIndex
                            ? colors.accent
                            : colors.border,
                  ),
                ),
                child: index <= activeIndex && !failed
                    ? Icon(Icons.check, size: 11, color: colors.accentContrast)
                    : failed && index == 2
                        ? Icon(Icons.priority_high,
                            size: 11, color: colors.danger)
                        : null,
              ),
              if (index != steps.length - 1)
                Expanded(
                  child: Container(
                    height: 2,
                    color: !failed && index < activeIndex
                        ? colors.accent
                        : colors.border,
                  ),
                ),
            ],
          ],
        ),
        const SizedBox(height: 6),
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            for (var index = 0; index < steps.length; index++)
              Expanded(
                child: Text(
                  steps[index],
                  textAlign: index == 0
                      ? TextAlign.left
                      : index == steps.length - 1
                          ? TextAlign.right
                          : TextAlign.center,
                  style: TextStyle(
                    color: failed && index == 2
                        ? colors.danger
                        : index <= activeIndex
                            ? colors.textSecondary
                            : colors.muted,
                    fontSize: 9.5,
                    height: 1.4,
                  ),
                ),
              ),
          ],
        ),
      ],
    );
  }
}

StatusTone _stageTone(RechargeStage stage) {
  return switch (stage) {
    RechargeStage.credited => StatusTone.success,
    RechargeStage.paidWaitingCredit => StatusTone.info,
    RechargeStage.pendingPayment => StatusTone.warning,
    RechargeStage.needsReview => StatusTone.danger,
    RechargeStage.refunded => StatusTone.accent,
    RechargeStage.closed => StatusTone.neutral,
    RechargeStage.unknown => StatusTone.neutral,
  };
}

class _OrderDetailSheet extends ConsumerWidget {
  const _OrderDetailSheet({required this.orderId});

  final String orderId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final data = ref.watch(walletControllerProvider).valueOrNull;
    final order = data?.orderById(orderId);
    final colors = context.colors;
    return Container(
      constraints: const BoxConstraints(maxWidth: 680),
      padding: const EdgeInsets.fromLTRB(22, 4, 22, 22),
      child: order == null
          ? const SizedBox(
              height: 280,
              child: EmptyState(
                title: '订单不存在',
                message: '订单可能已失效，请重新刷新订单列表。',
              ),
            )
          : Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                SheetHeader(
                  title: '充值订单',
                  subtitle: '支付状态与到账状态分别记录。',
                  trailing: IconButton(
                    tooltip: '关闭',
                    onPressed: () => Navigator.pop(context),
                    icon: const Icon(Icons.close, size: 20),
                  ),
                ),
                const SizedBox(height: 14),
                Row(
                  children: [
                    Text(
                      order.amount.text,
                      style: TextStyle(
                        color: colors.text,
                        fontSize: 28,
                        fontWeight: FontWeight.w600,
                        fontFeatures: const [FontFeature.tabularFigures()],
                      ),
                    ),
                    const Spacer(),
                    StatusPill(
                      label: order.stageLabel,
                      tone: _stageTone(order.stage),
                    ),
                  ],
                ),
                const SizedBox(height: 6),
                Text(
                  '${order.channelLabel} · ${order.currency}',
                  style: TextStyle(color: colors.textSecondary, fontSize: 12),
                ),
                const SizedBox(height: 18),
                _StagePipeline(stage: order.stage),
                const SizedBox(height: 14),
                Container(
                  padding: const EdgeInsets.all(13),
                  decoration: BoxDecoration(
                    color: colors.surfaceSubtle,
                    borderRadius: colors.cardRadius,
                    border: Border.all(color: colors.border),
                  ),
                  child: Column(
                    children: [
                      _orderFact(context, '支付状态', _paymentLabel(order)),
                      _orderFact(context, '到账状态', _creditLabel(order)),
                      _orderFact(
                          context, '创建时间', formatDateTime(order.createdAt)),
                      _orderFact(context, '支付时间', formatDateTime(order.paidAt)),
                      _orderFact(
                          context, '到账时间', formatDateTime(order.creditedAt)),
                      if (order.expiresAt != null)
                        _orderFact(
                            context, '订单有效期', formatDateTime(order.expiresAt)),
                    ],
                  ),
                ),
                if (order.stage == RechargeStage.paidWaitingCredit)
                  const Padding(
                    padding: EdgeInsets.only(top: 12),
                    child: InlineNotice(
                      icon: Icons.hourglass_top,
                      message: '支付已成功，余额到账需要单独处理。请稍后查询，不会因返回本页直接增加余额。',
                    ),
                  ),
                if (order.stage == RechargeStage.needsReview)
                  const Padding(
                    padding: EdgeInsets.only(top: 12),
                    child: InlineNotice(
                      tone: StatusTone.warning,
                      message: '订单需要核对，请保留订单信息并联系服务提供方。',
                    ),
                  ),
                const SizedBox(height: 16),
                Wrap(
                  spacing: 10,
                  runSpacing: 10,
                  children: [
                    if (order.hasCheckout)
                      AsyncButton(
                        label: '去支付',
                        icon: Icons.open_in_new,
                        onPressed: () async {
                          final opened =
                              await openExternalHttps(order.checkoutUrl!);
                          if (!context.mounted) return;
                          if (!opened) {
                            showAppMessage(context, '无法打开支付页面');
                          }
                        },
                      ),
                    AsyncButton(
                      label: '查询支付状态',
                      icon: Icons.refresh,
                      variant: AsyncButtonVariant.outline,
                      onPressed: () => ref
                          .read(walletControllerProvider.notifier)
                          .refreshOrder(order.id),
                    ),
                    OutlinedButton(
                      onPressed: () => Navigator.pop(context),
                      child: const Text('关闭'),
                    ),
                  ],
                ),
              ],
            ),
    );
  }

  Widget _orderFact(BuildContext context, String label, String value) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 7),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 98,
            child: Text(
              label,
              style: TextStyle(
                color: context.colors.textSecondary,
                fontSize: 11.5,
              ),
            ),
          ),
          Expanded(
            child: Text(
              value,
              style: TextStyle(
                color: context.colors.text,
                fontSize: 11.5,
                height: 1.5,
              ),
            ),
          ),
        ],
      ),
    );
  }

  String _paymentLabel(RechargeOrder order) {
    return switch (order.paymentStatus) {
      'unpaid' => '未支付',
      'paid' => '已支付',
      'failed' => '支付失败',
      'unknown' => '支付状态待核对',
      'refunded' => '已退款',
      _ => '状态待确认',
    };
  }

  String _creditLabel(RechargeOrder order) {
    return switch (order.creditStatus) {
      'credited' => '已到账',
      'needs_review' => '待核对',
      'not_credited' => order.paymentStatus == 'paid' ? '入账处理中' : '未到账',
      _ => '状态待确认',
    };
  }
}

class _PricingSummary extends StatelessWidget {
  const _PricingSummary({required this.pricing});

  final PricingInfo pricing;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final service = pricing.service;
    final tiku = pricing.tiku;
    final serviceText = service.active
        ? '${service.priceMicros == null ? '价格已发布' : formatMicrosYuan(service.priceMicros!)}${service.unitLabel == null ? '' : ' / ${service.unitLabel}'}'
        : '主服务单价待公布';
    final tikuText = tiku.active
        ? '输入 ${tiku.inputMicrosPerMillion == null ? '待公布' : '${formatMicrosYuan(tiku.inputMicrosPerMillion!)} / 百万 token'} · 输出 ${tiku.outputMicrosPerMillion == null ? '待公布' : '${formatMicrosYuan(tiku.outputMicrosPerMillion!)} / 百万 token'}'
        : '题库单价待公布';
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: colors.surfaceSubtle,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.receipt_long_outlined,
                  size: 16, color: colors.textSecondary),
              const SizedBox(width: 6),
              Text(
                '计费说明',
                style: TextStyle(
                  color: colors.text,
                  fontSize: 12.5,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const Spacer(),
              StatusPill(
                label: pricing.automaticCharging ? '自动计费已开启' : '未开启自动扣费',
                tone: pricing.automaticCharging
                    ? StatusTone.success
                    : StatusTone.neutral,
                compact: true,
              ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            serviceText.startsWith('主服务') ? serviceText : '主服务：$serviceText',
            style: TextStyle(
                color: colors.textSecondary, fontSize: 11.5, height: 1.5),
          ),
          const SizedBox(height: 2),
          Text(
            tikuText.startsWith('题库') ? tikuText : '题库：$tikuText',
            style: TextStyle(
                color: colors.textSecondary, fontSize: 11.5, height: 1.5),
          ),
        ],
      ),
    );
  }
}
