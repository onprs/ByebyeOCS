import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:byebyeocs_client/app/providers.dart';
import 'package:byebyeocs_client/core/api/api_client.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/models/page.dart';
import 'package:byebyeocs_client/core/models/question.dart';
import 'package:byebyeocs_client/core/models/wallet.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/core/realtime/realtime_service.dart';
import 'package:byebyeocs_client/core/repositories/question_repository.dart';
import 'package:byebyeocs_client/core/repositories/wallet_repository.dart';
import 'package:byebyeocs_client/core/session/session_manager.dart';
import 'package:byebyeocs_client/features/wallet/wallet_controller.dart';

import '../helpers.dart';

ApiClient _unusedApiClient() {
  return ApiClient(
    config: const AppConfig(apiBaseUri: null, channel: ClientChannel.web),
    session: SessionManager(
      channel: ClientChannel.web,
      store: InMemoryTokenStore(),
    ),
    httpClient: MockClient((request) async => http.Response('{}', 200)),
  );
}

WalletSnapshot _wallet(String available) {
  return WalletSnapshot.parse(<String, dynamic>{
    ...walletFixture,
    'available_minor': available,
  });
}

class _ControlledWalletRepository extends WalletRepository {
  _ControlledWalletRepository() : super(api: _unusedApiClient());

  final Completer<WalletSnapshot> oldRefresh = Completer<WalletSnapshot>();
  int walletCalls = 0;

  @override
  Future<WalletSnapshot> fetchWallet() {
    walletCalls++;
    if (walletCalls == 2) return oldRefresh.future;
    return Future<WalletSnapshot>.value(
      _wallet(walletCalls >= 3 ? '2000' : '1000'),
    );
  }

  @override
  Future<PricingInfo> fetchPricing() async =>
      PricingInfo.parse(Map<String, dynamic>.from(pricingFixture));

  @override
  Future<Page<WalletEntry>> fetchEntries({
    String? cursor,
    int limit = 30,
  }) async =>
      Page<WalletEntry>(items: const <WalletEntry>[]);

  @override
  Future<Page<RechargeOrder>> fetchOrders({
    String? cursor,
    int limit = 30,
  }) async =>
      Page<RechargeOrder>(items: const <RechargeOrder>[]);

  @override
  Future<Page<UsageRecord>> fetchUsage({
    String? cursor,
    int limit = 30,
  }) async =>
      Page<UsageRecord>(items: const <UsageRecord>[]);
}

class _EmptyQuestionRepository extends QuestionRepository {
  _EmptyQuestionRepository() : super(api: _unusedApiClient());

  @override
  Future<Page<QuestionQuery>> fetchQueries({
    String? cursor,
    int limit = 30,
  }) async =>
      Page<QuestionQuery>(items: const <QuestionQuery>[]);
}

class _PagedWalletRepository extends WalletRepository {
  _PagedWalletRepository() : super(api: _unusedApiClient());

  final Completer<Page<WalletEntry>> nextPage = Completer<Page<WalletEntry>>();
  int entryCalls = 0;

  @override
  Future<WalletSnapshot> fetchWallet() async => _wallet('1000');

  @override
  Future<PricingInfo> fetchPricing() async =>
      PricingInfo.parse(Map<String, dynamic>.from(pricingFixture));

  @override
  Future<Page<WalletEntry>> fetchEntries({
    String? cursor,
    int limit = 30,
  }) {
    entryCalls++;
    if (cursor != null && cursor.isNotEmpty) return nextPage.future;
    return Future<Page<WalletEntry>>.value(
      Page<WalletEntry>(
        items: <WalletEntry>[
          WalletEntry.parse(Map<String, dynamic>.from(entryFixtures[0])),
        ],
        nextCursor: 'cursor-1',
      ),
    );
  }

  @override
  Future<Page<RechargeOrder>> fetchOrders({
    String? cursor,
    int limit = 30,
  }) async =>
      Page<RechargeOrder>(items: const <RechargeOrder>[]);

  @override
  Future<Page<UsageRecord>> fetchUsage({
    String? cursor,
    int limit = 30,
  }) async =>
      Page<UsageRecord>(items: const <UsageRecord>[]);
}

void main() {
  test('切换账户后旧账户钱包刷新不得覆盖新账户余额', () async {
    final repository = _ControlledWalletRepository();
    final config =
        const AppConfig(apiBaseUri: null, channel: ClientChannel.web);
    final session = SessionManager(
      channel: ClientChannel.web,
      store: InMemoryTokenStore(),
    );
    final container = ProviderContainer(
      overrides: [
        currentUserProvider.overrideWithValue(testUser),
        walletRepositoryProvider.overrideWithValue(repository),
        questionRepositoryProvider
            .overrideWithValue(_EmptyQuestionRepository()),
        realtimeServiceProvider.overrideWithValue(
          RealtimeService(config: config, session: session),
        ),
      ],
    );
    addTearDown(container.dispose);
    container.listen(walletControllerProvider, (_, __) {});

    await container.read(walletControllerProvider.future);
    expect(
      container
          .read(walletControllerProvider)
          .valueOrNull
          ?.wallet
          .availableMinor,
      1000,
    );

    final refreshing =
        container.read(walletControllerProvider.notifier).refresh(silent: true);
    await Future<void>.delayed(Duration.zero);
    expect(repository.walletCalls, 2);

    container.read(sessionEpochProvider.notifier).bump();
    for (var index = 0; index < 20; index++) {
      await Future<void>.delayed(Duration.zero);
      if (container
              .read(walletControllerProvider)
              .valueOrNull
              ?.wallet
              .availableMinor ==
          2000) {
        break;
      }
    }
    expect(
      container
          .read(walletControllerProvider)
          .valueOrNull
          ?.wallet
          .availableMinor,
      2000,
    );

    repository.oldRefresh.complete(_wallet('1000'));
    await refreshing;
    await Future<void>.delayed(Duration.zero);

    expect(
      container
          .read(walletControllerProvider)
          .valueOrNull
          ?.wallet
          .availableMinor,
      2000,
    );
  });

  test('连续点击加载更多只追加一页，不重复展示同一页流水', () async {
    final repository = _PagedWalletRepository();
    final config =
        const AppConfig(apiBaseUri: null, channel: ClientChannel.web);
    final session = SessionManager(
      channel: ClientChannel.web,
      store: InMemoryTokenStore(),
    );
    final container = ProviderContainer(
      overrides: [
        currentUserProvider.overrideWithValue(testUser),
        walletRepositoryProvider.overrideWithValue(repository),
        questionRepositoryProvider
            .overrideWithValue(_EmptyQuestionRepository()),
        realtimeServiceProvider.overrideWithValue(
          RealtimeService(config: config, session: session),
        ),
      ],
    );
    addTearDown(container.dispose);
    container.listen(walletControllerProvider, (_, __) {});
    await container.read(walletControllerProvider.future);
    final notifier = container.read(walletControllerProvider.notifier);
    expect(
      container.read(walletControllerProvider).valueOrNull?.entries.length,
      1,
    );

    final first = notifier.loadEntries();
    final second = notifier.loadEntries();
    repository.nextPage.complete(
      Page<WalletEntry>(
        items: <WalletEntry>[
          WalletEntry.parse(Map<String, dynamic>.from(entryFixtures[1])),
        ],
      ),
    );
    await first;
    await second;

    final entries =
        container.read(walletControllerProvider).valueOrNull?.entries ??
            const <WalletEntry>[];
    expect(entries.map((entry) => entry.id).toList(), <String>['j1', 'j2']);
    expect(repository.entryCalls, 2);
    expect(
      container.read(walletControllerProvider).valueOrNull?.entriesCursor,
      isNull,
    );

    await notifier.loadEntries();
    expect(repository.entryCalls, 2);
  });
}
