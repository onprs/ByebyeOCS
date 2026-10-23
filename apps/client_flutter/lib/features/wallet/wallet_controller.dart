import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/models/question.dart';
import '../../core/models/wallet.dart';
import '../../core/realtime/realtime_event.dart';

class WalletState {
  const WalletState({
    required this.wallet,
    this.pricing,
    this.entries = const <WalletEntry>[],
    this.orders = const <RechargeOrder>[],
    this.usage = const <UsageRecord>[],
    this.queries = const <QuestionQuery>[],
    this.entriesCursor,
    this.ordersCursor,
    this.usageCursor,
    this.queriesCursor,
    this.refreshing = false,
    this.creatingOrder = false,
    this.queryDetailsAvailable = true,
    this.pricingError,
    this.entriesError,
    this.ordersError,
    this.usageError,
    this.actionError,
    this.notice,
  });

  final WalletSnapshot wallet;
  final PricingInfo? pricing;
  final List<WalletEntry> entries;
  final List<RechargeOrder> orders;
  final List<UsageRecord> usage;
  final List<QuestionQuery> queries;
  final String? entriesCursor;
  final String? ordersCursor;
  final String? usageCursor;
  final String? queriesCursor;
  final bool refreshing;
  final bool creatingOrder;
  final bool queryDetailsAvailable;
  final String? pricingError;
  final String? entriesError;
  final String? ordersError;
  final String? usageError;
  final String? actionError;
  final String? notice;

  RechargeOrder? orderById(String id) {
    for (final order in orders) {
      if (order.id == id) return order;
    }
    return null;
  }

  List<RechargeOrder> get activeOrders => orders
      .where((order) =>
          order.stage == RechargeStage.pendingPayment ||
          order.stage == RechargeStage.paidWaitingCredit ||
          order.stage == RechargeStage.needsReview)
      .toList(growable: false);

  WalletState copyWith({
    WalletSnapshot? wallet,
    PricingInfo? pricing,
    List<WalletEntry>? entries,
    List<RechargeOrder>? orders,
    List<UsageRecord>? usage,
    List<QuestionQuery>? queries,
    String? entriesCursor,
    String? ordersCursor,
    String? usageCursor,
    String? queriesCursor,
    bool? refreshing,
    bool? creatingOrder,
    bool? queryDetailsAvailable,
    String? pricingError,
    String? entriesError,
    String? ordersError,
    String? usageError,
    String? actionError,
    String? notice,
    bool clearPricingError = false,
    bool clearEntriesError = false,
    bool clearOrdersError = false,
    bool clearUsageError = false,
    bool clearActionError = false,
    bool clearNotice = false,
    bool clearEntriesCursor = false,
    bool clearOrdersCursor = false,
    bool clearUsageCursor = false,
    bool clearQueriesCursor = false,
  }) {
    return WalletState(
      wallet: wallet ?? this.wallet,
      pricing: pricing ?? this.pricing,
      entries: entries ?? this.entries,
      orders: orders ?? this.orders,
      usage: usage ?? this.usage,
      queries: queries ?? this.queries,
      entriesCursor: clearEntriesCursor
          ? null
          : entriesCursor ?? this.entriesCursor,
      ordersCursor:
          clearOrdersCursor ? null : ordersCursor ?? this.ordersCursor,
      usageCursor: clearUsageCursor ? null : usageCursor ?? this.usageCursor,
      queriesCursor:
          clearQueriesCursor ? null : queriesCursor ?? this.queriesCursor,
      refreshing: refreshing ?? this.refreshing,
      creatingOrder: creatingOrder ?? this.creatingOrder,
      queryDetailsAvailable:
          queryDetailsAvailable ?? this.queryDetailsAvailable,
      pricingError:
          clearPricingError ? null : pricingError ?? this.pricingError,
      entriesError:
          clearEntriesError ? null : entriesError ?? this.entriesError,
      ordersError: clearOrdersError ? null : ordersError ?? this.ordersError,
      usageError: clearUsageError ? null : usageError ?? this.usageError,
      actionError: clearActionError ? null : actionError ?? this.actionError,
      notice: clearNotice ? null : notice ?? this.notice,
    );
  }
}

class _WalletSection<T> {
  const _WalletSection(this.value, this.error);

  final T? value;
  final String? error;
}

class WalletController extends AsyncNotifier<WalletState> {
  StreamSubscription<RealtimeEvent>? _events;

  /// 账户代次：重建后旧账户的在途响应不得写入新用户状态。
  int _accountEpoch = 0;

  /// 列表代次与翻页锁：刷新后旧游标结果必须丢弃，重复点击只加载一次。
  int _listEpoch = 0;
  bool _loadingEntries = false;
  bool _loadingOrders = false;
  bool _loadingUsage = false;
  bool _loadingQueries = false;

  @override
  Future<WalletState> build() async {
    ++_accountEpoch;
    _listEpoch++;
    _loadingEntries = false;
    _loadingOrders = false;
    _loadingUsage = false;
    _loadingQueries = false;
    ref.watch(sessionEpochProvider);
    final user = ref.watch(currentUserProvider);
    _events = ref.watch(realtimeServiceProvider).events.listen(_onEvent);
    ref.onDispose(() {
      _events?.cancel();
      _events = null;
    });
    if (user == null) {
      throw const ApiException(
        statusCode: 401,
        code: 'unauthenticated',
        message: '请先登录',
      );
    }
    final repository = ref.watch(walletRepositoryProvider);
    final wallet = await repository.fetchWallet();
    // 钱包、价格、流水、订单、用量与查询互不依赖；并行读取避免串行等待多个往返。
    final pricingFuture = _loadSection(repository.fetchPricing, '价格暂时无法加载');
    final entriesFuture = _loadSection(repository.fetchEntries, '流水暂时无法加载');
    final ordersFuture = _loadSection(repository.fetchOrders, '订单暂时无法加载');
    final usageFuture = _loadSection(repository.fetchUsage, '用量暂时无法加载');
    final queriesFuture = _loadSection(
      () => ref.read(questionRepositoryProvider).fetchQueries(),
      '题库查询记录暂时无法获取',
    );
    final (pricing, entries, orders, usage, queries) = await (
      pricingFuture,
      entriesFuture,
      ordersFuture,
      usageFuture,
      queriesFuture,
    ).wait;
    return WalletState(
      wallet: wallet,
      pricing: pricing.value,
      entries: entries.value?.items ?? const <WalletEntry>[],
      orders: orders.value?.items ?? const <RechargeOrder>[],
      usage: usage.value?.items ?? const <UsageRecord>[],
      queries: queries.value?.items ?? const <QuestionQuery>[],
      entriesCursor: entries.value?.nextCursor,
      ordersCursor: orders.value?.nextCursor,
      usageCursor: usage.value?.nextCursor,
      queriesCursor: queries.value?.nextCursor,
      queryDetailsAvailable: queries.value != null,
      pricingError: pricing.error,
      entriesError: entries.error,
      ordersError: orders.error,
      usageError: usage.error,
    );
  }

  Future<_WalletSection<T>> _loadSection<T>(
    Future<T> Function() run,
    String fallback,
  ) async {
    try {
      return _WalletSection(await run(), null);
    } on ApiException catch (error) {
      return _WalletSection(null, error.userMessage);
    } catch (_) {
      return _WalletSection(null, fallback);
    }
  }

  void _onEvent(RealtimeEvent event) {
    final current = state.valueOrNull;
    if (current == null) return;
    if (event.type == 'client.resume') {
      unawaited(refresh(silent: true));
      return;
    }
    if (event.type == 'client.poll') {
      if (current.activeOrders.isNotEmpty) {
        unawaited(refresh(silent: true));
      }
      return;
    }
    if (event.type.startsWith('wallet.') ||
        event.type.startsWith('recharge_order.')) {
      unawaited(refresh(silent: true));
    }
  }

  Future<void> refresh({bool silent = false}) async {
    final current = state.valueOrNull;
    if (current == null) {
      ref.invalidateSelf();
      return;
    }
    if (current.refreshing) return;
    final epoch = _accountEpoch;
    ++_listEpoch;
    state = AsyncData(
      current.copyWith(refreshing: true, clearActionError: true),
    );
    final repository = ref.read(walletRepositoryProvider);
    try {
      final wallet = await repository.fetchWallet();
      if (epoch != _accountEpoch) return;
      final pricingFuture = _loadSection(repository.fetchPricing, '价格暂时无法加载');
      final entriesFuture = _loadSection(repository.fetchEntries, '流水暂时无法加载');
      final ordersFuture = _loadSection(repository.fetchOrders, '订单暂时无法加载');
      final usageFuture = _loadSection(repository.fetchUsage, '用量暂时无法加载');
      final queriesFuture = _loadSection(
        () => ref.read(questionRepositoryProvider).fetchQueries(),
        '题库查询记录暂时无法获取',
      );
      final (pricing, entries, orders, usage, queries) = await (
        pricingFuture,
        entriesFuture,
        ordersFuture,
        usageFuture,
        queriesFuture,
      ).wait;
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          wallet: wallet,
          pricing: pricing.value,
          entries: entries.value?.items,
          orders: orders.value?.items,
          usage: usage.value?.items,
          queries: queries.value?.items,
          entriesCursor: entries.value?.nextCursor,
          ordersCursor: orders.value?.nextCursor,
          usageCursor: usage.value?.nextCursor,
          queriesCursor: queries.value?.nextCursor,
          clearEntriesCursor:
              entries.value != null && entries.value!.nextCursor == null,
          clearOrdersCursor:
              orders.value != null && orders.value!.nextCursor == null,
          clearUsageCursor:
              usage.value != null && usage.value!.nextCursor == null,
          clearQueriesCursor:
              queries.value != null && queries.value!.nextCursor == null,
          refreshing: false,
          queryDetailsAvailable: queries.value != null,
          pricingError: pricing.error,
          entriesError: entries.error,
          ordersError: orders.error,
          usageError: usage.error,
          clearPricingError: pricing.error == null,
          clearEntriesError: entries.error == null,
          clearOrdersError: orders.error == null,
          clearUsageError: usage.error == null,
        ),
      );
    } on ApiException catch (error) {
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          refreshing: false,
          actionError: error.userMessage,
        ),
      );
    } catch (_) {
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          refreshing: false,
          actionError: '钱包暂时无法刷新',
        ),
      );
    }
  }

  Future<RechargeOrder?> createOrder({
    required int amountMinor,
    required String channel,
    required String idempotencyKey,
  }) async {
    final current = state.valueOrNull;
    if (current == null || current.creatingOrder) return null;
    final epoch = _accountEpoch;
    state = AsyncData(
      current.copyWith(
        creatingOrder: true,
        clearActionError: true,
        clearNotice: true,
      ),
    );
    try {
      final order = await ref.read(walletRepositoryProvider).createOrder(
            amountMinor: amountMinor,
            channel: channel,
            idempotencyKey: idempotencyKey,
          );
      if (epoch != _accountEpoch) return order;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          creatingOrder: false,
          orders: [
            order,
            ...latest.orders.where((item) => item.id != order.id)
          ],
          notice: '充值订单已创建，请在支付完成后回到这里查询到账状态',
        ),
      );
      return order;
    } on ApiException catch (error) {
      if (epoch != _accountEpoch) return null;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          creatingOrder: false,
          actionError: error.userMessage,
        ),
      );
      return null;
    } catch (_) {
      if (epoch != _accountEpoch) return null;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          creatingOrder: false,
          actionError: '充值订单未创建，请稍后重试',
        ),
      );
      return null;
    }
  }

  Future<RechargeOrder?> refreshOrder(String id) async {
    final current = state.valueOrNull;
    if (current == null) return null;
    final epoch = _accountEpoch;
    try {
      final order = await ref.read(walletRepositoryProvider).fetchOrder(id);
      if (epoch != _accountEpoch) return order;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          orders: [
            order,
            ...latest.orders.where((item) => item.id != order.id),
          ],
          clearActionError: true,
        ),
      );
      if (order.stage == RechargeStage.credited) {
        await refresh(silent: true);
      }
      return order;
    } on ApiException catch (error) {
      if (epoch != _accountEpoch) return null;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(actionError: error.userMessage),
      );
      return null;
    } catch (_) {
      if (epoch != _accountEpoch) return null;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(actionError: '订单状态暂时无法查询'),
      );
      return null;
    }
  }

  Future<void> refreshAfterReturn(String? orderId) async {
    final current = state.valueOrNull;
    if (current == null) {
      ref.invalidateSelf();
      return;
    }
    if (orderId != null && orderId.isNotEmpty) {
      await refreshOrder(orderId);
    }
    await refresh(silent: true);
  }

  Future<void> loadEntries() async {
    final current = state.valueOrNull;
    if (current == null || current.entriesCursor == null || _loadingEntries) {
      return;
    }
    final epoch = _accountEpoch;
    final listEpoch = _listEpoch;
    _loadingEntries = true;
    try {
      final page = await ref.read(walletRepositoryProvider).fetchEntries(
            cursor: current.entriesCursor,
          );
      if (epoch != _accountEpoch || listEpoch != _listEpoch) return;
      final latest = state.valueOrNull ?? current;
      final known = latest.entries.map((item) => item.id).toSet();
      final appended =
          page.items.where((item) => known.add(item.id)).toList(growable: false);
      state = AsyncData(
        latest.copyWith(
          entries: [...latest.entries, ...appended],
          entriesCursor: page.nextCursor,
          clearEntriesCursor: page.nextCursor == null,
        ),
      );
    } catch (_) {
      if (epoch != _accountEpoch || listEpoch != _listEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(entriesError: '更多流水暂时无法加载'),
      );
    } finally {
      _loadingEntries = false;
    }
  }

  Future<void> loadOrders() async {
    final current = state.valueOrNull;
    if (current == null || current.ordersCursor == null || _loadingOrders) {
      return;
    }
    final epoch = _accountEpoch;
    final listEpoch = _listEpoch;
    _loadingOrders = true;
    try {
      final page = await ref.read(walletRepositoryProvider).fetchOrders(
            cursor: current.ordersCursor,
          );
      if (epoch != _accountEpoch || listEpoch != _listEpoch) return;
      final latest = state.valueOrNull ?? current;
      final known = latest.orders.map((item) => item.id).toSet();
      final appended =
          page.items.where((item) => known.add(item.id)).toList(growable: false);
      state = AsyncData(
        latest.copyWith(
          orders: [...latest.orders, ...appended],
          ordersCursor: page.nextCursor,
          clearOrdersCursor: page.nextCursor == null,
        ),
      );
    } catch (_) {
      if (epoch != _accountEpoch || listEpoch != _listEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(ordersError: '更多订单暂时无法加载'),
      );
    } finally {
      _loadingOrders = false;
    }
  }

  Future<void> loadUsage() async {
    final current = state.valueOrNull;
    if (current == null || current.usageCursor == null || _loadingUsage) {
      return;
    }
    final epoch = _accountEpoch;
    final listEpoch = _listEpoch;
    _loadingUsage = true;
    try {
      final page = await ref.read(walletRepositoryProvider).fetchUsage(
            cursor: current.usageCursor,
          );
      if (epoch != _accountEpoch || listEpoch != _listEpoch) return;
      final latest = state.valueOrNull ?? current;
      final known = latest.usage.map((item) => item.id).toSet();
      final appended =
          page.items.where((item) => known.add(item.id)).toList(growable: false);
      state = AsyncData(
        latest.copyWith(
          usage: [...latest.usage, ...appended],
          usageCursor: page.nextCursor,
          clearUsageCursor: page.nextCursor == null,
        ),
      );
    } catch (_) {
      if (epoch != _accountEpoch || listEpoch != _listEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(usageError: '更多用量记录暂时无法加载'),
      );
    } finally {
      _loadingUsage = false;
    }
  }

  Future<void> loadQueries() async {
    final current = state.valueOrNull;
    if (current == null ||
        !current.queryDetailsAvailable ||
        current.queriesCursor == null ||
        _loadingQueries) {
      return;
    }
    final epoch = _accountEpoch;
    final listEpoch = _listEpoch;
    _loadingQueries = true;
    try {
      final page = await ref.read(questionRepositoryProvider).fetchQueries(
            cursor: current.queriesCursor,
          );
      if (epoch != _accountEpoch || listEpoch != _listEpoch) return;
      final latest = state.valueOrNull ?? current;
      final known = latest.queries.map((item) => item.id).toSet();
      final appended =
          page.items.where((item) => known.add(item.id)).toList(growable: false);
      state = AsyncData(
        latest.copyWith(
          queries: [...latest.queries, ...appended],
          queriesCursor: page.nextCursor,
          clearQueriesCursor: page.nextCursor == null,
        ),
      );
    } catch (_) {
      if (epoch != _accountEpoch || listEpoch != _listEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(usageError: '更多题库查询记录暂时无法加载'),
      );
    } finally {
      _loadingQueries = false;
    }
  }

  void clearBanner() {
    final current = state.valueOrNull;
    if (current == null) return;
    state = AsyncData(
      current.copyWith(clearActionError: true, clearNotice: true),
    );
  }
}

final walletControllerProvider =
    AsyncNotifierProvider<WalletController, WalletState>(WalletController.new);
