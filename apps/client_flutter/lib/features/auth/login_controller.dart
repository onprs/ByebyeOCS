import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../auth/auth_controller.dart';

class LoginState {
  const LoginState({
    this.submitting = false,
    this.challengeId,
    this.expiresAt,
    this.attemptsRemaining,
    this.expired = false,
    this.locked = false,
    this.message,
    this.error,
    this.completed = false,
  });

  final bool submitting;
  final String? challengeId;
  final DateTime? expiresAt;
  final int? attemptsRemaining;
  final bool expired;
  final bool locked;
  final String? message;
  final ApiException? error;
  final bool completed;

  bool get hasChallenge => challengeId != null && !expired && !locked;

  LoginState copyWith({
    bool? submitting,
    String? challengeId,
    DateTime? expiresAt,
    int? attemptsRemaining,
    bool? expired,
    bool? locked,
    String? message,
    ApiException? error,
    bool? completed,
    bool clearChallenge = false,
    bool clearMessage = false,
    bool clearError = false,
  }) {
    return LoginState(
      submitting: submitting ?? this.submitting,
      challengeId: clearChallenge ? null : challengeId ?? this.challengeId,
      expiresAt: clearChallenge ? null : expiresAt ?? this.expiresAt,
      attemptsRemaining:
          clearChallenge ? null : attemptsRemaining ?? this.attemptsRemaining,
      expired: expired ?? this.expired,
      locked: locked ?? this.locked,
      message: clearMessage ? null : message ?? this.message,
      error: clearError ? null : error ?? this.error,
      completed: completed ?? this.completed,
    );
  }
}

class LoginController extends Notifier<LoginState> {
  bool _disposed = false;

  @override
  LoginState build() {
    ref.onDispose(() => _disposed = true);
    return const LoginState();
  }

  void reset() {
    state = const LoginState();
  }

  Future<void> submit({
    required String account,
    required String password,
    required bool consent,
  }) async {
    if (state.submitting) return;
    if (account.trim().isEmpty || password.isEmpty) {
      state = state.copyWith(
        message: '请填写学习通账号和密码',
        clearError: true,
      );
      return;
    }
    if (!consent) {
      state = state.copyWith(
        message: '请先确认授权说明',
        clearError: true,
      );
      return;
    }
    if (state.locked) {
      state = state.copyWith(
        message: '验证次数已用尽，请重新发起验证',
        expired: true,
        clearError: true,
      );
      return;
    }
    state = state.copyWith(
      submitting: true,
      clearMessage: true,
      clearError: true,
    );
    final repository = ref.read(authRepositoryProvider);
    try {
      var challengeId = state.challengeId;
      if (challengeId == null || state.expired) {
        final challenge = await repository.createChallenge();
        if (_disposed) return;
        challengeId = challenge.id;
        state = state.copyWith(
          challengeId: challenge.id,
          expiresAt: challenge.expiresAt,
          attemptsRemaining: challenge.attemptsRemaining,
          expired: challenge.isExpired,
          locked: challenge.attemptsRemaining == 0,
        );
      }
      final result = await repository.verifyChallenge(
        id: challengeId,
        account: account,
        password: password,
      );
      if (_disposed) return;
      state = state.copyWith(submitting: false, completed: true);
      await ref.read(authControllerProvider.notifier).completeLogin(result);
    } on ApiException catch (error) {
      if (_disposed) return;
      await _applyError(error, challengeId: state.challengeId);
    } catch (_) {
      if (_disposed) return;
      state = state.copyWith(
        submitting: false,
        error: const ApiException(
          statusCode: 0,
          code: 'client_error',
          message: '登录请求未完成，请稍后重试',
          retryable: true,
        ),
      );
    }
  }

  Future<void> _applyError(
    ApiException error, {
    String? challengeId,
  }) async {
    var expired = false;
    var locked = false;
    var attempts = state.attemptsRemaining;
    if (error.isUnauthenticated && attempts != null && attempts <= 0) {
      locked = true;
    }
    if (error.statusCode == 404 ||
        error.code == 'not_found' ||
        error.isConflict) {
      expired = true;
    }
    if (error.code == 'external_disabled' ||
        error.code == 'not_configured' ||
        error.statusCode == 503) {
      expired = true;
    }
    if (challengeId != null && !expired) {
      try {
        final challenge =
            await ref.read(authRepositoryProvider).fetchChallenge(challengeId);
        if (_disposed) return;
        attempts = challenge.attemptsRemaining;
        if (challenge.isExpired) expired = true;
        if (challenge.status == 'failed') locked = true;
        if (challenge.status == 'consumed') expired = true;
      } catch (_) {
        // 挑战状态读取失败不覆盖原始登录错误。
      }
    }
    if (_disposed) return;
    state = state.copyWith(
      submitting: false,
      error: error,
      message: _friendlyMessage(error),
      attemptsRemaining: attempts,
      expired: expired,
      locked: locked,
    );
  }

  String _friendlyMessage(ApiException error) {
    if (error.code == 'external_disabled' || error.code == 'not_configured') {
      return '学习通身份验证尚未开放，请联系服务提供方';
    }
    if (error.isRateLimited) {
      final seconds = error.retryAfter?.inSeconds;
      return seconds == null
          ? '验证尝试过于频繁，请稍后再试'
          : '验证尝试过于频繁，请 ${seconds.clamp(1, 3600)} 秒后再试';
    }
    if (error.isUnauthenticated) {
      return '学习通账号或密码未通过验证';
    }
    if (error.statusCode == 404 || error.isConflict) {
      return '本次登录验证已过期，请重新发起';
    }
    return error.userMessage;
  }

  void restart() {
    state = const LoginState(
      message: '请重新填写身份信息并发起验证',
    );
  }
}

final loginControllerProvider =
    NotifierProvider<LoginController, LoginState>(LoginController.new);
