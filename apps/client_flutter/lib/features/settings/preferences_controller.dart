import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/models/preferences.dart';
import '../../core/repositories/auth_repository.dart';

/// 一次用户保存意图；字段为空表示本次不修改该偏好。
class _PreferencePatch {
  _PreferencePatch({
    this.colorScheme,
    this.themeMode,
    this.taskAttention,
    this.walletChanges,
  });

  final ColorSchemeId? colorScheme;
  final AppThemeMode? themeMode;
  final bool? taskAttention;
  final bool? walletChanges;
  final Completer<void> done = Completer<void>();

  bool get isEmpty =>
      colorScheme == null &&
      themeMode == null &&
      taskAttention == null &&
      walletChanges == null;
}

class PreferencesController extends AsyncNotifier<PreferencesSnapshot> {
  /// 账户代次与保存队列代次：重建后旧响应不得写入新账户，也不得覆盖新选择。
  int _accountEpoch = 0;
  int _drainToken = 0;
  bool _draining = false;
  bool _disposed = false;
  final List<_PreferencePatch> _queue = <_PreferencePatch>[];

  @override
  Future<PreferencesSnapshot> build() async {
    final epoch = ++_accountEpoch;
    _disposed = false;
    ref.onDispose(() {
      if (epoch != _accountEpoch) return;
      _disposed = true;
      _drainToken++;
      _discardQueue();
    });
    // 重建时结束旧保存队列，旧请求返回后只完成等待者，不再写状态或外观。
    _drainToken++;
    _draining = false;
    _discardQueue();
    ref.watch(sessionEpochProvider);
    final appearance = ref.read(appearanceControllerProvider.notifier);
    try {
      final snapshot =
          await ref.watch(authRepositoryProvider).fetchPreferences();
      if (_disposed || epoch != _accountEpoch) return snapshot;
      await appearance.applyServer(
        snapshot.preferences,
        version: snapshot.effectiveVersion,
      );
      return snapshot;
    } finally {
      if (!_disposed && epoch == _accountEpoch && !_draining) {
        appearance.markSyncing(false);
      }
    }
  }

  Future<void> save({
    ColorSchemeId? colorScheme,
    AppThemeMode? themeMode,
    bool? taskAttention,
    bool? walletChanges,
  }) {
    final patch = _PreferencePatch(
      colorScheme: colorScheme,
      themeMode: themeMode,
      taskAttention: taskAttention,
      walletChanges: walletChanges,
    );
    if (state.valueOrNull == null || patch.isEmpty) {
      patch.done.complete();
      return patch.done.future;
    }
    if (_disposed) {
      patch.done.complete();
      return patch.done.future;
    }
    _queue.add(patch);
    _kickDrain();
    return patch.done.future;
  }

  void _kickDrain() {
    if (_disposed || _draining || _queue.isEmpty) return;
    _draining = true;
    final token = ++_drainToken;
    unawaited(_drain(token));
  }

  Future<void> _drain(int token) async {
    final epoch = _accountEpoch;
    final appearance = ref.read(appearanceControllerProvider.notifier);
    try {
      while (token == _drainToken && _queue.isNotEmpty) {
        final patch = _queue.removeAt(0);
        final current = state.valueOrNull;
        if (current == null) {
          patch.done.complete();
          continue;
        }
        appearance.markSyncing(true);
        try {
          final snapshot =
              await ref.read(authRepositoryProvider).patchPreferences(
                    version: current.effectiveVersion,
                    colorScheme: patch.colorScheme?.value,
                    themeMode: patch.themeMode?.value,
                    taskAttention: patch.taskAttention,
                    walletChanges: patch.walletChanges,
                  );
          if (_disposed || token != _drainToken || epoch != _accountEpoch) {
            patch.done.complete();
            break;
          }
          state = AsyncData(snapshot);
          if (_queue.isEmpty) {
            // 只有队列耗尽后的响应才能回写外观，避免中间响应覆盖最后选择。
            await appearance.applyServer(
              snapshot.preferences,
              version: snapshot.effectiveVersion,
            );
            if (_disposed || token != _drainToken || epoch != _accountEpoch) {
              patch.done.complete();
              break;
            }
            appearance.markSaved(snapshot.effectiveVersion);
          }
          patch.done.complete();
        } on ApiException catch (error) {
          if (_disposed || token != _drainToken || epoch != _accountEpoch) {
            patch.done.complete();
            break;
          }
          // 真实冲突时丢弃排队中的旧选择；外部修改以服务端最新值为准。
          _discardQueue();
          await _handleSaveError(error, epoch, token);
          patch.done.complete();
          break;
        } catch (_) {
          if (_disposed || token != _drainToken || epoch != _accountEpoch) {
            patch.done.complete();
            break;
          }
          _discardQueue();
          appearance.markError('偏好未保存，请稍后重试');
          patch.done.complete();
          break;
        }
      }
    } finally {
      if (token == _drainToken) {
        _draining = false;
        _kickDrain();
      }
    }
  }

  Future<void> _handleSaveError(
    ApiException error,
    int epoch,
    int token,
  ) async {
    final appearance = ref.read(appearanceControllerProvider.notifier);
    if (_disposed) return;
    if (!error.isVersionConflict && !error.isPreconditionRequired) {
      appearance.markError(error.userMessage);
      return;
    }
    try {
      final latest = await ref.read(authRepositoryProvider).fetchPreferences();
      if (_disposed || token != _drainToken || epoch != _accountEpoch) return;
      state = AsyncData(latest);
      await appearance.applyServer(
        latest.preferences,
        version: latest.effectiveVersion,
      );
      if (_disposed || token != _drainToken || epoch != _accountEpoch) return;
    } catch (_) {
      // 拉取最新偏好失败时保留当前界面，但仍明确提示需要重新选择。
    }
    appearance.markError('偏好已在其他位置更新，请重新选择');
  }

  void _discardQueue() {
    for (final patch in _queue) {
      if (!patch.done.isCompleted) patch.done.complete();
    }
    _queue.clear();
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
