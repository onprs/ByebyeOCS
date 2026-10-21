import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/models/preferences.dart';
import '../../core/repositories/auth_repository.dart';

class PreferencesController extends AsyncNotifier<PreferencesSnapshot> {
  @override
  Future<PreferencesSnapshot> build() async {
    ref.watch(sessionEpochProvider);
    final snapshot = await ref.watch(authRepositoryProvider).fetchPreferences();
    await ref
        .read(appearanceControllerProvider.notifier)
        .applyServer(snapshot.preferences, version: snapshot.effectiveVersion);
    return snapshot;
  }

  Future<void> save({
    ColorSchemeId? colorScheme,
    AppThemeMode? themeMode,
    bool? taskAttention,
    bool? walletChanges,
  }) async {
    final current = state.valueOrNull;
    if (current == null) return;
    ref.read(appearanceControllerProvider.notifier).markSyncing(true);
    try {
      final snapshot = await ref.read(authRepositoryProvider).patchPreferences(
            version: current.effectiveVersion,
            colorScheme: colorScheme?.value,
            themeMode: themeMode?.value,
            taskAttention: taskAttention,
            walletChanges: walletChanges,
          );
      state = AsyncData(snapshot);
      await ref.read(appearanceControllerProvider.notifier).applyServer(
          snapshot.preferences,
          version: snapshot.effectiveVersion);
      ref
          .read(appearanceControllerProvider.notifier)
          .markSaved(snapshot.effectiveVersion);
    } on ApiException catch (error) {
      if (error.isVersionConflict || error.isPreconditionRequired) {
        try {
          final latest =
              await ref.read(authRepositoryProvider).fetchPreferences();
          state = AsyncData(latest);
          await ref.read(appearanceControllerProvider.notifier).applyServer(
                latest.preferences,
                version: latest.effectiveVersion,
              );
        } catch (_) {}
        ref
            .read(appearanceControllerProvider.notifier)
            .markError('偏好已在其他位置更新，请重新选择');
      } else {
        ref
            .read(appearanceControllerProvider.notifier)
            .markError(error.userMessage);
      }
    } catch (_) {
      ref.read(appearanceControllerProvider.notifier).markError('偏好未保存，请稍后重试');
    }
  }

  Future<void> reload() async {
    ref.invalidateSelf();
    await future;
  }
}

final preferencesControllerProvider =
    AsyncNotifierProvider<PreferencesController, PreferencesSnapshot>(
  PreferencesController.new,
);
