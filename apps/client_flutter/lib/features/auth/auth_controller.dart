import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/models/auth.dart';
import '../../core/models/capabilities.dart';
import '../../core/config/server_config.dart';
import '../../core/models/preferences.dart';
import '../../core/repositories/auth_repository.dart';

enum AuthStatus { loading, anonymous, authenticated, unavailable }

class AuthState {
  const AuthState({
    required this.status,
    this.user,
    this.preferences,
    this.capabilities = const Capabilities(),
    this.error,
    this.notice,
    this.busy = false,
  });

  final AuthStatus status;
  final UserProfile? user;
  final UserPreferences? preferences;
  final Capabilities capabilities;
  final ApiException? error;
  final String? notice;
  final bool busy;

  bool get isAuthenticated => status == AuthStatus.authenticated;

  AuthState copyWith({
    AuthStatus? status,
    UserProfile? user,
    UserPreferences? preferences,
    Capabilities? capabilities,
    ApiException? error,
    String? notice,
    bool? busy,
    bool clearError = false,
    bool clearNotice = false,
  }) {
    return AuthState(
      status: status ?? this.status,
      user: user ?? this.user,
      preferences: preferences ?? this.preferences,
      capabilities: capabilities ?? this.capabilities,
      error: clearError ? null : error ?? this.error,
      notice: clearNotice ? null : notice ?? this.notice,
      busy: busy ?? this.busy,
    );
  }
}

class AuthController extends Notifier<AuthState> {
  bool _disposed = false;
  bool _bootstrapping = false;

  /// 会话代次：切换服务、退出或登录后，早先的身份请求不得改写当前状态。
  int _authEpoch = 0;

  @override
  AuthState build() {
    _authEpoch++;
    ref.onDispose(() => _disposed = true);
    final server = ref.watch(serverConfigProvider);
    if (!server.loaded) {
      return const AuthState(status: AuthStatus.loading);
    }
    final session = ref.watch(sessionManagerProvider);
    session.onExpired = () {
      if (_disposed) return;
      unawaited(_handleExpired());
    };
    unawaited(Future<void>.microtask(bootstrap));
    return const AuthState(status: AuthStatus.loading);
  }

  Future<void> bootstrap() async {
    if (_bootstrapping || _disposed) return;
    _bootstrapping = true;
    final epoch = ++_authEpoch;
    if (state.status != AuthStatus.authenticated) {
      state = state.copyWith(
        status: AuthStatus.loading,
        busy: true,
        clearError: true,
      );
    }
    final repository = ref.read(authRepositoryProvider);
    final session = ref.read(sessionManagerProvider);
    try {
      final sessionStatus = await repository.fetchSessionStatus();
      if (_disposed || epoch != _authEpoch) return;
      var capabilities = Capabilities.parse(sessionStatus.capabilities);
      try {
        final transport = await repository.fetchCapabilities();
        if (_disposed || epoch != _authEpoch) return;
        capabilities = capabilities.merge(transport);
      } catch (_) {
        // 管理能力接口暂不可用不影响已验证的身份会话。
      }
      session.resetExpiredNotification();
      if (!sessionStatus.authenticated) {
        if (_disposed || epoch != _authEpoch) return;
        state = AuthState(
          status: AuthStatus.anonymous,
          capabilities: capabilities,
        );
        return;
      }
      final me = await repository.fetchMe();
      if (_disposed || epoch != _authEpoch) return;
      PreferencesSnapshot? preferences;
      try {
        preferences = await repository.fetchPreferences();
      } on ApiException catch (error) {
        if (error.isUnauthenticated) rethrow;
      }
      if (_disposed || epoch != _authEpoch) return;
      final bumpEpoch = state.user?.id != me.id;
      state = AuthState(
        status: AuthStatus.authenticated,
        user: me,
        preferences: preferences?.preferences,
        capabilities: capabilities,
      );
      if (bumpEpoch) {
        ref.read(sessionEpochProvider.notifier).bump();
      }
      if (preferences != null) {
        await ref.read(appearanceControllerProvider.notifier).applyServer(
            preferences.preferences,
            version: preferences.effectiveVersion);
      }
    } on ApiException catch (error) {
      if (_disposed || epoch != _authEpoch) return;
      if (error.isUnauthenticated) {
        await session.clear();
        if (_disposed || epoch != _authEpoch) return;
        state = AuthState(
          status: AuthStatus.anonymous,
          capabilities: state.capabilities,
          notice: '登录已失效，请重新验证身份',
        );
      } else {
        state = state.copyWith(
          status: AuthStatus.unavailable,
          error: error,
          busy: false,
        );
      }
    } catch (_) {
      if (_disposed || epoch != _authEpoch) return;
      state = state.copyWith(
        status: AuthStatus.unavailable,
        error: const ApiException(
          statusCode: 0,
          code: 'client_unavailable',
          message: '无法连接服务，请稍后重试',
          retryable: true,
        ),
        busy: false,
      );
    } finally {
      _bootstrapping = false;
      if (!_disposed && epoch == _authEpoch && state.busy) {
        state = state.copyWith(busy: false);
      }
    }
  }

  Future<void> completeLogin(LoginResult result) async {
    final epoch = ++_authEpoch;
    final repository = ref.read(authRepositoryProvider);
    var capabilities = state.capabilities;
    try {
      final sessionStatus = await repository.fetchSessionStatus();
      if (_disposed || epoch != _authEpoch) return;
      capabilities = Capabilities.parse(sessionStatus.capabilities);
      try {
        final transport = await repository.fetchCapabilities();
        if (_disposed || epoch != _authEpoch) return;
        capabilities = capabilities.merge(transport);
      } catch (_) {}
    } catch (_) {}
    UserProfile? user = result.user;
    PreferencesSnapshot? preferences;
    try {
      user = await repository.fetchMe();
      if (_disposed || epoch != _authEpoch) return;
      preferences = await repository.fetchPreferences();
    } catch (_) {
      // 登录已成功时仍以会话事实进入；各页面会独立展示数据加载错误。
    }
    if (_disposed || epoch != _authEpoch) return;
    ref.read(sessionEpochProvider.notifier).bump();
    state = AuthState(
      status: AuthStatus.authenticated,
      user: user,
      preferences: preferences?.preferences,
      capabilities: capabilities,
    );
    if (preferences != null) {
      await ref.read(appearanceControllerProvider.notifier).applyServer(
          preferences.preferences,
          version: preferences.effectiveVersion);
    }
  }

  Future<void> refreshAccount() async {
    final epoch = _authEpoch;
    final repository = ref.read(authRepositoryProvider);
    try {
      final me = await repository.fetchMe();
      if (_disposed || epoch != _authEpoch) return;
      state = state.copyWith(user: me, clearError: true);
    } on ApiException catch (error) {
      if (_disposed || epoch != _authEpoch) return;
      if (error.isUnauthenticated) {
        await _handleExpired();
      } else {
        state = state.copyWith(error: error);
      }
    }
  }

  /// 切换服务前必须调用：清除旧令牌、会话与用户级缓存，再允许写入新地址。
  Future<void> clearForServerChange() async {
    ++_authEpoch;
    await ref.read(sessionManagerProvider).clear();
    if (_disposed) return;
    ref.read(sessionEpochProvider.notifier).bump();
    state = AuthState(
      status: AuthStatus.anonymous,
      capabilities: state.capabilities,
      notice: '服务地址已切换，请重新验证学习通身份',
    );
  }

  /// 删除申请提交后服务端已撤销会话；本机立即退出，由申请回执页展示最后已知状态。
  Future<void> endSessionAfterDeletion() async {
    ++_authEpoch;
    await ref.read(sessionManagerProvider).clear();
    if (_disposed) return;
    ref.read(sessionEpochProvider.notifier).bump();
    state = AuthState(
      status: AuthStatus.anonymous,
      capabilities: state.capabilities,
    );
  }

  Future<void> logout() async {
    if (state.busy) return;
    ++_authEpoch;
    state = state.copyWith(busy: true, clearError: true);
    String? notice;
    try {
      await ref.read(authRepositoryProvider).logout();
    } catch (_) {
      notice = '本机登录信息已清除，远端退出可能稍后完成';
    } finally {
      await ref.read(sessionManagerProvider).clear();
      if (!_disposed) {
        ref.read(sessionEpochProvider.notifier).bump();
        state = AuthState(
          status: AuthStatus.anonymous,
          capabilities: state.capabilities,
          notice: notice ?? '已退出登录',
        );
      }
    }
  }

  Future<void> _handleExpired() async {
    if (_disposed) return;
    ++_authEpoch;
    await ref.read(sessionManagerProvider).clear();
    if (_disposed) return;
    ref.read(sessionEpochProvider.notifier).bump();
    state = AuthState(
      status: AuthStatus.anonymous,
      capabilities: state.capabilities,
      notice: '登录已失效，请重新验证身份',
    );
  }

  void clearNotice() {
    state = state.copyWith(clearNotice: true);
  }
}

final authControllerProvider = NotifierProvider<AuthController, AuthState>(
  AuthController.new,
);
