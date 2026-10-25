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
  String? _idempotencySignature;
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
    final data = async.valueOrNull;
    if (data == null) {
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
              data: (_) => const SizedBox.shrink(),
            ),
          ),
        ),
      );
    }
    return LayoutBuilder(
      builder: (context, constraints) {
        final narrow = context.isNarrow;
        // 与 ResponsiveContent 保持一致：窄屏 18、宽屏 28，超过 1180 居中。
        final baseInset = narrow ? 18.0 : 28.0;
        final centering = constraints.maxWidth > 1180
            ? (constraints.maxWidth - 1180) / 2
            : 0.0;
        final inset = baseInset + centering;
        // 原 SingleChildScrollView 底部 12/28 加 ResponsiveContent 底部 16/22。
        final bottom = narrow ? 28.0 : 50.0;
        return RefreshIndicator(
          onRefresh: () =>
              ref.read(walletControllerProvider.notifier).refresh(silent: true),
          child: CustomScrollView(
            controller: _scrollController,
            physics: const AlwaysScrollableScrollPhysics(),
            slivers: [
              SliverPadding(
                padding: EdgeInsets.fromLTRB(
                  inset,
                  narrow ? 16 : 22,
                  inset,
                  0,
                ),
                sliver: SliverToBoxAdapter(
                  child: _buildSummary(context, data),
                ),
              ),
              SliverPadding(
                padding: EdgeInsets.symmetric(horizontal: inset),
                sliver: _buildLedgerSection(context, data),
              ),
              SliverPadding(padding: EdgeInsets.only(bottom: bottom)),
            ],
          ),
        );
      },
    );
  }

  Widget _buildSummary(BuildContext context, WalletState data) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        PageHeader(
          title: '余额与订单',
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
      ],
    );
  }

  Widget _buildLedgerSection(BuildContext context, WalletState data) {
    final colors = context.colors;
    return DecoratedSliver(
      decoration: BoxDecoration(
        color: colors.surface,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      sliver: SliverMainAxisGroup(
        slivers: [
          SliverToBoxAdapter(
            child: SectionCardHeader(
              title: switch (_tab) {
                _WalletTab.orders => '充值订单',
                _WalletTab.entries => '资金流水',
                _WalletTab.usage => '用量记录',
              },
              action: _ledgerAction(data),
            ),
          ),
          _buildLedgerSliver(context, data),
        ],
      ),
    );
  }

  Widget? _ledgerAction(WalletState data) {
    return switch (_tab) {
      _WalletTab.orders => data.ordersCursor == null
          ? null
          : TextButton(
              onPressed: () =>
                  ref.read(walletControllerProvider.notifier).loadOrders(),
              child: const Text('加载更多'),
            ),
      _WalletTab.entries => data.entriesCursor == null
          ? null
          : TextButton(
              onPressed: () =>
                  ref.read(walletControllerProvider.notifier).loadEntries(),
              child: const Text('加载更多'),
            ),
      _WalletTab.usage => data.usageCursor == null
          ? null
          : TextButton(
              onPressed: () =>
                  ref.read(walletControllerProvider.notifier).loadUsage(),
              child: const Text('加载更多'),
            ),
    };
  }

  Widget _buildLedgerSliver(BuildContext context, WalletState data) {
    switch (_tab) {
      case _WalletTab.orders:
        return _listSectionSliver(
          error: data.ordersError,
          empty: data.orders.isEmpty,
          emptyState: const EmptyState(
            icon: Icons.receipt_long_outlined,
            title: '还没有充值订单',
            message: '创建充值订单后，可在这里查看到账进度。',
            compact: true,
          ),
          count: data.orders.length,
          itemBuilder: (context, index) {
            final order = data.orders[index];
            return _OrderTile(
              key: ValueKey('wallet-order-${order.id}'),
              order: order,
              active: order.id == _activeOrderId,
              onOpen: () => _showOrder(order.id),
              onQuery: () => _refreshOrder(order.id),
              onPay: () => _openPayment(order),
            );
          },
        );
      case _WalletTab.entries:
        return _listSectionSliver(
          error: data.entriesError,
          empty: data.entries.isEmpty,
          emptyState: const EmptyState(
            icon: Icons.swap_vert_outlined,
            title: '还没有资金流水',
            message: '充值到账、任务消费与预占释放会记录在这里。',
            compact: true,
          ),
          count: data.entries.length,
          itemBuilder: (context, index) => _EntryTile(
            key: ValueKey(
              'wallet-entry-${data.entries[index].id}',
            ),
            entry: data.entries[index],
          ),
        );
      case _WalletTab.usage:
        return _buildUsageSliver(context, data);
    }
  }

  Widget _listSectionSliver({
    required String? error,
    required bool empty,
    required Widget emptyState,
    required int count,
    required NullableIndexedWidgetBuilder itemBuilder,
  }) {
    final Widget content;
    if (error != null) {
      content = SliverToBoxAdapter(
        child: ErrorPanel(
          message: error,
          compact: true,
          onRetry: () => ref.read(walletControllerProvider.notifier).refresh(),
        ),
      );
    } else if (empty) {
      content = SliverToBoxAdapter(child: emptyState);
    } else {
      content = SliverList(
        delegate: SliverChildBuilderDelegate(itemBuilder, childCount: count),
      );
    }
    return SliverPadding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 16),
      sliver: content,
    );
  }

  Widget _buildUsageSliver(BuildContext context, WalletState data) {
    final colors = context.colors;
    final cacheHits = data.queries.where((query) => query.cacheHit).length;
    final totalTokens = data.queries.fold<int>(
      0,
      (sum, query) => sum + query.usage.totalTokens,
    );
    final slivers = <Widget>[
      SliverToBoxAdapter(
        child: Wrap(
          spacing: 10,
          runSpacing: 10,
          children: [
            _UsageMetric(label: '查询记录', value: '${data.queries.length}'),
            _UsageMetric(label: '缓存命中', value: '$cacheHits'),
            _UsageMetric(label: '合计 token', value: formatCount(totalTokens)),
          ],
        ),
      ),
      const SliverToBoxAdapter(child: SizedBox(height: 16)),
    ];
    if (data.queries.isNotEmpty) {
      slivers.addAll([
        SliverToBoxAdapter(
          child: Row(
            children: [
              Text(
                '题库查询',
                style: TextStyle(
                  color: colors.text,
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const Spacer(),
              if (data.queriesCursor != null)
                TextButton(
                  onPressed: () =>
                      ref.read(walletControllerProvider.notifier).loadQueries(),
                  child: const Text('更多'),
                ),
            ],
          ),
        ),
        const SliverToBoxAdapter(child: SizedBox(height: 8)),
        SliverList(
          delegate: SliverChildBuilderDelegate(
            (context, index) => _QueryUsageTile(
              key: ValueKey(
                'wallet-query-${data.queries[index].id}',
              ),
              query: data.queries[index],
            ),
            childCount: data.queries.length,
          ),
        ),
      ]);
    } else if (data.queryDetailsAvailable) {
      slivers.add(
        SliverToBoxAdapter(
          child: Text(
            '还没有题库查询记录。',
            style: TextStyle(color: colors.textSecondary, fontSize: 13),
          ),
        ),
      );
    } else {
      slivers.add(
        const SliverToBoxAdapter(
          child: InlineNotice(
            icon: Icons.info_outline,
            message: '详细的题库查询记录暂不可用；已结算用量仍会在下方显示。',
          ),
        ),
      );
    }
    if (data.usage.isNotEmpty) {
      slivers.addAll([
        const SliverToBoxAdapter(child: SizedBox(height: 16)),
        SliverToBoxAdapter(
          child: Text(
            '计费用量',
            style: TextStyle(
              color: colors.text,
              fontSize: 13,
              fontWeight: FontWeight.w600,
            ),
          ),
        ),
        const SliverToBoxAdapter(child: SizedBox(height: 8)),
        SliverList(
          delegate: SliverChildBuilderDelegate(
            (context, index) => _LedgerUsageTile(
              key: ValueKey(
                'wallet-usage-${data.usage[index].id}',
              ),
              usage: data.usage[index],
            ),
            childCount: data.usage.length,
          ),
        ),
      ]);
    }
    if (data.usageError != null) {
      slivers.addAll([
        const SliverToBoxAdapter(child: SizedBox(height: 10)),
        SliverToBoxAdapter(
          child: ErrorPanel(
            message: data.usageError!,
            compact: true,
            onRetry: () =>
                ref.read(walletControllerProvider.notifier).refresh(),
          ),
        ),
      ]);
    }
    return SliverPadding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 16),
      sliver: SliverMainAxisGroup(slivers: slivers),
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
      // 幂等键只能代表同一金额与支付方式；输入变化后必须生成新键，
      // 否则服务端会把重试视为“同一键对应不同请求”而拒绝。
      final signature = '$minor|$channel';
      if (_idempotencySignature != signature || _idempotencyKey == null) {
        _idempotencySignature = signature;
        _idempotencyKey = newIdempotencyKey();
      }
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
        _idempotencySignature = null;
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
                  fontSize: 13,
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
              fontSize: 34,
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
                fontSize: 12,
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
              fontSize: 12,
              height: 1.5,
            ),
          ),
          if (wallet.updatedAt != null) ...[
            const SizedBox(height: 4),
            Text(
              '更新于 ${formatDateTime(wallet.updatedAt)}',
              style: TextStyle(
                color: colors.heroText.withValues(alpha: 0.5),
                fontSize: 12,
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
                fontSize: 12,
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
                    fontSize: 14,
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
                style: TextStyle(color: colors.warning, fontSize: 12),
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
    return Align(
      alignment: Alignment.centerLeft,
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 560),
        child: SlidingSelectionBar(
          labels: [
            '订单 ${data.orders.length}',
            '流水 ${data.entries.length}',
            '题库用量 ${data.queries.length}',
          ],
          selectedIndex: _WalletTab.values.indexOf(selected),
          onSelected: (index) => onChanged(_WalletTab.values[index]),
        ),
      ),
    );
  }
}

class _OrderTile extends StatelessWidget {
  const _OrderTile({
    super.key,
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
                        fontSize: 14,
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
                style: TextStyle(color: colors.textSecondary, fontSize: 12),
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
  const _EntryTile({super.key, required this.entry});

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
                      fontSize: 13,
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
                      fontSize: 12,
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
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                    fontFeatures: const [FontFeature.tabularFigures()],
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  formatShortDateTime(entry.createdAt),
                  style: TextStyle(color: colors.muted, fontSize: 12),
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
  const _QueryUsageTile({super.key, required this.query});

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
                      fontSize: 13,
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
                fontSize: 12,
                height: 1.5,
              ),
            ),
            const SizedBox(height: 2),
            Text(
              '${query.pricing.label}${query.createdAt == null ? '' : ' · ${formatShortDateTime(query.createdAt)}'}',
              style: TextStyle(color: colors.muted, fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}

class _LedgerUsageTile extends StatelessWidget {
  const _LedgerUsageTile({super.key, required this.usage});

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
                      fontSize: 13,
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
                      fontSize: 12,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    formatShortDateTime(usage.createdAt),
                    style: TextStyle(color: colors.muted, fontSize: 12),
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
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  usage.statusLabel,
                  style: TextStyle(color: colors.muted, fontSize: 12),
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
              fontSize: 16,
              fontWeight: FontWeight.w600,
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
          const SizedBox(height: 2),
          Text(
            label,
            style: TextStyle(color: colors.textSecondary, fontSize: 12),
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
                    fontSize: 12,
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

class _OrderDetailSheet extends ConsumerStatefulWidget {
  const _OrderDetailSheet({required this.orderId});

  final String orderId;

  @override
  ConsumerState<_OrderDetailSheet> createState() => _OrderDetailSheetState();
}

class _OrderDetailSheetState extends ConsumerState<_OrderDetailSheet> {
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    // 列表接口不返回支付入口；打开详情时读取订单详情，恢复可用的收银台地址。
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  Future<void> _load() async {
    if (!mounted) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    final order = await ref
        .read(walletControllerProvider.notifier)
        .refreshOrder(widget.orderId);
    if (!mounted) return;
    setState(() {
      _loading = false;
      if (order == null) {
        _error = ref.read(walletControllerProvider).valueOrNull?.actionError ??
            '订单状态暂时无法查询';
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final data = ref.watch(walletControllerProvider).valueOrNull;
    final order = data?.orderById(widget.orderId);
    final colors = context.colors;
    return Container(
      constraints: const BoxConstraints(maxWidth: 680),
      padding: const EdgeInsets.fromLTRB(22, 4, 22, 22),
      child: order == null
          ? SizedBox(
              height: 280,
              child: _loading
                  ? const Center(
                      child: SizedBox(
                        width: 22,
                        height: 22,
                        child: CircularProgressIndicator(strokeWidth: 2.2),
                      ),
                    )
                  : _error != null
                      ? ErrorPanel(
                          message: _error!,
                          compact: true,
                          onRetry: _load,
                        )
                      : const EmptyState(
                          title: '订单不存在',
                          message: '订单可能已失效，请重新刷新订单列表。',
                        ),
            )
          : Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                if (_loading) ...[
                  const LinearProgressIndicator(minHeight: 2),
                  const SizedBox(height: 10),
                ],
                if (_error != null) ...[
                  InlineNotice(
                    tone: StatusTone.warning,
                    message: _error!,
                    action: TextButton(
                      onPressed: _load,
                      child: const Text('重试'),
                    ),
                  ),
                  const SizedBox(height: 10),
                ],
                SheetHeader(
                  title: '充值订单',
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
                        fontSize: 26,
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
                fontSize: 12,
              ),
            ),
          ),
          Expanded(
            child: Text(
              value,
              style: TextStyle(
                color: context.colors.text,
                fontSize: 12,
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
    if (order.paymentStatus == 'refunded') return '已退款';
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
    final serviceText = service.active
        ? '${service.priceMicros == null ? '价格已发布' : formatMicrosYuan(service.priceMicros!)}${service.unitLabel == null ? '' : ' / ${service.unitLabel}'}'
        : '主服务单价待公布';
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
                  fontSize: 13,
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
                color: colors.textSecondary, fontSize: 12, height: 1.5),
          ),
          const SizedBox(height: 2),
          Text(
            pricing.automaticCharging
                ? '题库：按所选公开模型的 token 价格单独计费。'
                : '题库：按所选公开模型的 token 价格记录用量，自动扣费未开启。',
            style: TextStyle(
                color: colors.textSecondary, fontSize: 12, height: 1.5),
          ),
        ],
      ),
    );
  }
}
