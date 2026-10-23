import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:byebyeocs_client/app/providers.dart';
import 'package:byebyeocs_client/core/api/api_client.dart';
import 'package:byebyeocs_client/core/api/api_error.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/models/preferences.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/core/repositories/auth_repository.dart';
import 'package:byebyeocs_client/core/session/session_manager.dart';
import 'package:byebyeocs_client/features/settings/preferences_controller.dart';

typedef _PatchCall = ({
  int version,
  String? colorScheme,
  String? themeMode,
  bool? taskAttention,
  bool? walletChanges,
});

PreferencesSnapshot _snapshot(
  ColorSchemeId color,
  AppThemeMode mode,
  int version, {
  bool taskAttention = true,
  bool walletChanges = true,
}) {
  return PreferencesSnapshot(
    preferences: UserPreferences(
      colorScheme: color,
      themeMode: mode,
      taskAttention: taskAttention,
      walletChanges: walletChanges,
      version: version,
    ),
    version: version,
  );
}

class _PreferencesRepository extends AuthRepository {
  _PreferencesRepository()
      : super(
          api: ApiClient(
            config: const AppConfig(
              apiBaseUri: null,
              channel: ClientChannel.web,
            ),
            session: SessionManager(
              channel: ClientChannel.web,
              store: InMemoryTokenStore(),
            ),
            httpClient: MockClient((request) async => http.Response('{}', 200)),
          ),
          session: SessionManager(
            channel: ClientChannel.web,
            store: InMemoryTokenStore(),
          ),
        );

  PreferencesSnapshot server = _snapshot(
    ColorSchemeId.graphite,
    AppThemeMode.system,
    1,
  );
  final List<_PatchCall> patches = <_PatchCall>[];
  final List<Completer<PreferencesSnapshot>> pending =
      <Completer<PreferencesSnapshot>>[];
  int fetchCount = 0;

  @override
  Future<PreferencesSnapshot> fetchPreferences() async {
    fetchCount++;
    return server;
  }

  @override
  Future<PreferencesSnapshot> patchPreferences({
    required int version,
    String? colorScheme,
    String? themeMode,
    bool? taskAttention,
    bool? walletChanges,
  }) {
    patches.add((
      version: version,
      colorScheme: colorScheme,
      themeMode: themeMode,
      taskAttention: taskAttention,
      walletChanges: walletChanges,
    ));
    final completer = Completer<PreferencesSnapshot>();
    pending.add(completer);
    return completer.future;
  }
}

Future<void> _flush() => Future<void>.delayed(Duration.zero);

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues(<String, Object>{});
  });

  test('连续保存按队列串行使用最新 ETag，并以最后一次选择为准', () async {
    final repository = _PreferencesRepository();
    final container = ProviderContainer(
      overrides: [authRepositoryProvider.overrideWithValue(repository)],
    );
    addTearDown(container.dispose);
    container.listen(preferencesControllerProvider, (_, __) {});
    await container.read(preferencesControllerProvider.future);

    final notifier = container.read(preferencesControllerProvider.notifier);
    final first = notifier.save(
      colorScheme: ColorSchemeId.grove,
      themeMode: AppThemeMode.dark,
    );
    await _flush();
    expect(repository.patches, hasLength(1));
    expect(repository.patches.single.version, 1);

    // 第一次请求在途时用户再次选择；本地外观先显示最后选择。
    await container
        .read(appearanceControllerProvider.notifier)
        .setColorScheme(ColorSchemeId.indigo);
    final second = notifier.save(colorScheme: ColorSchemeId.indigo);
    await _flush();
    expect(repository.patches, hasLength(1));

    repository.pending[0].complete(
      _snapshot(ColorSchemeId.grove, AppThemeMode.dark, 2),
    );
    await _flush();
    // 第二次保存读取第一次响应后的新版本，且不把中间响应回写外观。
    expect(repository.patches, hasLength(2));
    expect(repository.patches[1].version, 2);
    expect(repository.patches[1].colorScheme, 'indigo');
    expect(
      container.read(appearanceControllerProvider).colorScheme,
      ColorSchemeId.indigo,
    );

    repository.pending[1].complete(
      _snapshot(ColorSchemeId.indigo, AppThemeMode.dark, 3),
    );
    await Future.wait<void>(<Future<void>>[first, second]);

    final snapshot = container.read(preferencesControllerProvider).valueOrNull;
    final appearance = container.read(appearanceControllerProvider);
    expect(snapshot?.effectiveVersion, 3);
    expect(snapshot?.preferences.colorScheme, ColorSchemeId.indigo);
    expect(snapshot?.preferences.themeMode, AppThemeMode.dark);
    expect(appearance.colorScheme, ColorSchemeId.indigo);
    expect(appearance.themeMode, AppThemeMode.dark);
    expect(appearance.syncing, isFalse);
    expect(appearance.notice, '偏好已保存');
  });

  test('412 时采用服务端最新值，丢弃旧排队选择且不静默覆盖外部修改', () async {
    final repository = _PreferencesRepository();
    final container = ProviderContainer(
      overrides: [authRepositoryProvider.overrideWithValue(repository)],
    );
    addTearDown(container.dispose);
    container.listen(preferencesControllerProvider, (_, __) {});
    await container.read(preferencesControllerProvider.future);

    final notifier = container.read(preferencesControllerProvider.notifier);
    final first = notifier.save(colorScheme: ColorSchemeId.grove);
    final second = notifier.save(taskAttention: false);
    await _flush();
    expect(repository.patches, hasLength(1));

    // 外部设备已改为靛蓝/浅色，版本推进到 5；本次保存携带旧 ETag 被拒绝。
    repository.server = _snapshot(
      ColorSchemeId.indigo,
      AppThemeMode.light,
      5,
    );
    repository.pending[0].completeError(
      const ApiException(
        statusCode: 412,
        code: 'version_conflict',
        message: '偏好版本已变化',
      ),
    );
    await Future.wait<void>(<Future<void>>[first, second]);

    expect(repository.patches, hasLength(1));
    expect(repository.fetchCount, 2);
    final snapshot = container.read(preferencesControllerProvider).valueOrNull;
    final appearance = container.read(appearanceControllerProvider);
    expect(snapshot?.effectiveVersion, 5);
    expect(snapshot?.preferences.colorScheme, ColorSchemeId.indigo);
    expect(snapshot?.preferences.themeMode, AppThemeMode.light);
    expect(snapshot?.preferences.taskAttention, isTrue);
    expect(appearance.colorScheme, ColorSchemeId.indigo);
    expect(appearance.themeMode, AppThemeMode.light);
    expect(appearance.error, '偏好已在其他位置更新，请重新选择');
    expect(appearance.syncing, isFalse);

    // 恢复后新的保存必须基于服务端最新版本。
    final third = notifier.save(walletChanges: false);
    await _flush();
    expect(repository.patches, hasLength(2));
    expect(repository.patches[1].version, 5);
    repository.pending[1].complete(
      _snapshot(
        ColorSchemeId.indigo,
        AppThemeMode.light,
        6,
        walletChanges: false,
      ),
    );
    await third;
    expect(
      container
          .read(preferencesControllerProvider)
          .valueOrNull
          ?.effectiveVersion,
      6,
    );
  });

  test('Provider 销毁时结束排队保存，迟到响应不再写状态', () async {
    final repository = _PreferencesRepository();
    final container = ProviderContainer(
      overrides: [authRepositoryProvider.overrideWithValue(repository)],
    );
    container.listen(preferencesControllerProvider, (_, __) {});
    await container.read(preferencesControllerProvider.future);

    final notifier = container.read(preferencesControllerProvider.notifier);
    final first = notifier.save(colorScheme: ColorSchemeId.grove);
    final second = notifier.save(taskAttention: false);
    await _flush();
    expect(repository.patches, hasLength(1));

    container.dispose();
    repository.pending[0].complete(
      _snapshot(ColorSchemeId.grove, AppThemeMode.system, 2),
    );
    await Future.wait<void>(<Future<void>>[first, second]);
  });

  test('会话重建后旧保存响应不得回写新账户偏好', () async {
    final repository = _PreferencesRepository();
    final container = ProviderContainer(
      overrides: [authRepositoryProvider.overrideWithValue(repository)],
    );
    addTearDown(container.dispose);
    container.listen(preferencesControllerProvider, (_, __) {});
    await container.read(preferencesControllerProvider.future);

    final notifier = container.read(preferencesControllerProvider.notifier);
    final stale = notifier.save(colorScheme: ColorSchemeId.grove);
    await _flush();
    expect(repository.patches, hasLength(1));

    repository.server = _snapshot(
      ColorSchemeId.indigo,
      AppThemeMode.dark,
      10,
    );
    container.read(sessionEpochProvider.notifier).bump();
    await container.read(preferencesControllerProvider.future);

    repository.pending[0].complete(
      _snapshot(ColorSchemeId.grove, AppThemeMode.system, 2),
    );
    await stale;
    await _flush();

    final snapshot = container.read(preferencesControllerProvider).valueOrNull;
    final appearance = container.read(appearanceControllerProvider);
    expect(snapshot?.effectiveVersion, 10);
    expect(snapshot?.preferences.colorScheme, ColorSchemeId.indigo);
    expect(appearance.colorScheme, ColorSchemeId.indigo);
    expect(appearance.themeMode, AppThemeMode.dark);
  });
}
