import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:http/http.dart' as http;

import '../core/api/api_client.dart';
import '../core/config/app_config.dart';
import '../core/config/server_config.dart';
import '../core/models/auth.dart';
import '../core/models/capabilities.dart';
import '../core/models/preferences.dart';
import '../core/platform/http_client.dart';
import '../core/platform/token_store.dart';
import '../core/repositories/auth_repository.dart';
import '../core/repositories/learning_repository.dart';
import '../core/repositories/privacy_repository.dart';
import '../core/repositories/question_repository.dart';
import '../core/repositories/wallet_repository.dart';
import '../core/realtime/realtime_service.dart';
import '../core/session/session_manager.dart';
import '../features/auth/auth_controller.dart';
import '../features/settings/appearance_controller.dart';

final appConfigProvider = Provider<AppConfig>((ref) {
  final server = ref.watch(serverConfigProvider);
  return AppConfig(
    apiBaseUri: server.baseUri,
    channel: kIsWeb ? ClientChannel.web : ClientChannel.app,
  );
});

final httpClientProvider = Provider<http.Client>((ref) {
  final client = createPlatformHttpClient();
  ref.onDispose(client.close);
  return client;
});

final tokenStoreProvider = Provider<TokenStore>((ref) {
  return createTokenStore();
});

final sessionManagerProvider = Provider<SessionManager>((ref) {
  final config = ref.watch(appConfigProvider);
  return SessionManager(
    channel: config.channel,
    store: ref.watch(tokenStoreProvider),
  );
});

final apiClientProvider = Provider<ApiClient>((ref) {
  return ApiClient(
    config: ref.watch(appConfigProvider),
    session: ref.watch(sessionManagerProvider),
    httpClient: ref.watch(httpClientProvider),
  );
});

final realtimeServiceProvider = Provider<RealtimeService>((ref) {
  final service = RealtimeService(
    config: ref.watch(appConfigProvider),
    session: ref.watch(sessionManagerProvider),
  );
  ref.onDispose(service.dispose);
  return service;
});

final authRepositoryProvider = Provider<AuthRepository>((ref) {
  return AuthRepository(
    api: ref.watch(apiClientProvider),
    session: ref.watch(sessionManagerProvider),
  );
});

final learningRepositoryProvider = Provider<LearningRepository>((ref) {
  return LearningRepository(api: ref.watch(apiClientProvider));
});

final walletRepositoryProvider = Provider<WalletRepository>((ref) {
  return WalletRepository(api: ref.watch(apiClientProvider));
});

final privacyRepositoryProvider = Provider<PrivacyRepository>((ref) {
  return PrivacyRepository(api: ref.watch(apiClientProvider));
});

final questionRepositoryProvider = Provider<QuestionRepository>((ref) {
  return QuestionRepository(api: ref.watch(apiClientProvider));
});

final appearanceControllerProvider =
    NotifierProvider<AppearanceController, AppearanceState>(
  AppearanceController.new,
);

/// 每次身份切换递增；用户级数据控制器监听它，避免上一用户缓存残留。
final sessionEpochProvider =
    NotifierProvider<SessionEpoch, int>(SessionEpoch.new);

class SessionEpoch extends Notifier<int> {
  @override
  int build() => 0;

  void bump() => state = state + 1;
}

final currentUserProvider = Provider<UserProfile?>((ref) {
  return ref.watch(authControllerProvider.select((value) => value.user));
});

final userPreferencesProvider = Provider<UserPreferences?>((ref) {
  return ref.watch(authControllerProvider.select((value) => value.preferences));
});

final authCapabilitiesProvider = Provider<Capabilities>((ref) {
  return ref
      .watch(authControllerProvider.select((value) => value.capabilities));
});

final authStatusProvider = Provider<AuthStatus>((ref) {
  return ref.watch(authControllerProvider.select((value) => value.status));
});
