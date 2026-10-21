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
  }) {
    return WalletState(
      wallet: wallet ?? this.wallet,
      pricing: pricing ?? this.pricing,
      entries: entries ?? this.entries,
      orders: orders ?? this.orders,
      usage: usage ?? this.usage,
      queries: queries ?? this.queries,
      entriesCursor: entriesCursor ?? this.entriesCursor,
      ordersCursor: ordersCursor ?? this.ordersCursor,
      usageCursor: usageCursor ?? this.usageCursor,
      queriesCursor: queriesCursor ?? this.queriesCursor,
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

class WalletController extends AsyncNotifier<WalletState> {
  StreamSubscription<RealtimeEvent>? _events;

  @override
  Future<WalletState> build() async {
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
    PricingInfo? pricing;
    String? pricingError;
    try {
      pricing = await repository.fetchPricing();
    } on ApiException catch (error) {
      pricingError = error.userMessage;
    }
    List<WalletEntry> entries = const [];
    String? entriesCursor;
    String? entriesError;
    try {
      final page = await repository.fetchEntries();
      entries = page.items;
      entriesCursor = page.nextCursor;
    } on ApiException catch (error) {
      entriesError = error.userMessage;
    }
    List<RechargeOrder> orders = const [];
    String? ordersCursor;
    String? ordersError;
    try {
      final page = await repository.fetchOrders();
      orders = page.items;
      ordersCursor = page.nextCursor;
    } on ApiException catch (error) {
      ordersError = error.userMessage;
    }
    List<UsageRecord> usage = const [];
    String? usageCursor;
    String? usageError;
    try {
      final page = await repository.fetchUsage();
      usage = page.items;
      usageCursor = page.nextCursor;
    } on ApiException catch (error) {
      usageError = error.userMessage;
    }
    List<QuestionQuery> queries = const [];
    String? queriesCursor;
    bool queryDetailsAvailable = true;
    try {
      final page = await ref.read(questionRepositoryProvider).fetchQueries();
      queries = page.items;
      queriesCursor = page.nextCursor;
    } on ApiException catch (error) {
      if (error.isNotFound) {
        queryDetailsAvailable = false;
      } else {
        queryDetailsAvailable = false;
      }
    } catch (_) {
      queryDetailsAvailable = false;
    }
    return WalletState(
      wallet: wallet,
      pricing: pricing,
      entries: entries,
      orders: orders,
      usage: usage,
      queries: queries,
      entriesCursor: entriesCursor,
      ordersCursor: ordersCursor,
      usageCursor: usageCursor,
      queriesCursor: queriesCursor,
      queryDetailsAvailable: queryDetailsAvailable,
      pricingError: pricingError,
      entriesError: entriesError,
      ordersError: ordersError,
      usageError: usageError,
    );
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
    state = AsyncData(
      current.copyWith(refreshing: true, clearActionError: true),
    );
    final repository = ref.read(walletRepositoryProvider);
    try {
      final wallet = await repository.fetchWallet();
      PricingInfo? pricing;
      String? pricingError;
      try {
        pricing = await repository.fetchPricing();
      } on ApiException catch (error) {
        pricingError = error.userMessage;
      }
      List<WalletEntry> entries = current.entries;
      String? entriesCursor = current.entriesCursor;
      String? entriesError;
      try {
        final page = await repository.fetchEntries();
        entries = page.items;
        entriesCursor = page.nextCursor;
      } on ApiException catch (error) {
        entriesError = error.userMessage;
      }
      List<RechargeOrder> orders = current.orders;
      String? ordersCursor = current.ordersCursor;
      String? ordersError;
      try {
        final page = await repository.fetchOrders();
        orders = page.items;
        ordersCursor = page.nextCursor;
      } on ApiException catch (error) {
        ordersError = error.userMessage;
      }
      List<UsageRecord> usage = current.usage;
      String? usageCursor = current.usageCursor;
      String? usageError;
      try {
        final page = await repository.fetchUsage();
        usage = page.items;
        usageCursor = page.nextCursor;
      } on ApiException catch (error) {
        usageError = error.userMessage;
      }
      List<QuestionQuery> queries = current.queries;
      String? queriesCursor = current.queriesCursor;
      bool queryDetailsAvailable = current.queryDetailsAvailable;
      try {
        final page = await ref.read(questionRepositoryProvider).fetchQueries();
        queries = page.items;
        queriesCursor = page.nextCursor;
        queryDetailsAvailable = true;
      } catch (_) {
        queryDetailsAvailable = false;
      }
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          wallet: wallet,
          pricing: pricing,
          entries: entries,
          orders: orders,
          usage: usage,
          queries: queries,
          entriesCursor: entriesCursor,
          ordersCursor: ordersCursor,
          usageCursor: usageCursor,
          queriesCursor: queriesCursor,
          queryDetailsAvailable: queryDetailsAvailable,
          refreshing: false,
          pricingError: pricingError,
          entriesError: entriesError,
          ordersError: ordersError,
          usageError: usageError,
          clearPricingError: pricingError == null,
          clearEntriesError: entriesError == null,
          clearOrdersError: ordersError == null,
          clearUsageError: usageError == null,
        ),
      );
    } on ApiException catch (error) {
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          refreshing: false,
          actionError: error.userMessage,
        ),
      );
    } catch (_) {
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
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          creatingOrder: false,
          actionError: error.userMessage,
        ),
      );
      return null;
    } catch (_) {
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
    try {
      final order = await ref.read(walletRepositoryProvider).fetchOrder(id);
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
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(actionError: error.userMessage),
      );
      return null;
    } catch (_) {
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
    if (current == null || current.entriesCursor == null) return;
    try {
      final page = await ref.read(walletRepositoryProvider).fetchEntries(
            cursor: current.entriesCursor,
          );
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          entries: [...latest.entries, ...page.items],
          entriesCursor: page.nextCursor,
        ),
      );
    } catch (_) {
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(entriesError: '更多流水暂时无法加载'),
      );
    }
  }

  Future<void> loadOrders() async {
    final current = state.valueOrNull;
    if (current == null || current.ordersCursor == null) return;
    try {
      final page = await ref.read(walletRepositoryProvider).fetchOrders(
            cursor: current.ordersCursor,
          );
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          orders: [...latest.orders, ...page.items],
          ordersCursor: page.nextCursor,
        ),
      );
    } catch (_) {
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(ordersError: '更多订单暂时无法加载'),
      );
    }
  }

  Future<void> loadUsage() async {
    final current = state.valueOrNull;
    if (current == null || current.usageCursor == null) return;
    try {
      final page = await ref.read(walletRepositoryProvider).fetchUsage(
            cursor: current.usageCursor,
          );
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          usage: [...latest.usage, ...page.items],
          usageCursor: page.nextCursor,
        ),
      );
    } catch (_) {
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(usageError: '更多用量记录暂时无法加载'),
      );
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
