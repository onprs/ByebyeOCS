import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../../core/models/preferences.dart';

class AppearanceState {
  const AppearanceState({
    this.colorScheme = ColorSchemeId.graphite,
    this.themeMode = AppThemeMode.system,
    this.serverVersion,
    this.syncing = false,
    this.error,
    this.notice,
    this.loaded = false,
  });

  final ColorSchemeId colorScheme;
  final AppThemeMode themeMode;
  final int? serverVersion;
  final bool syncing;
  final String? error;
  final String? notice;
  final bool loaded;

  ThemeMode get materialThemeMode => switch (themeMode) {
        AppThemeMode.system => ThemeMode.system,
        AppThemeMode.light => ThemeMode.light,
        AppThemeMode.dark => ThemeMode.dark,
      };

  AppearanceState copyWith({
    ColorSchemeId? colorScheme,
    AppThemeMode? themeMode,
    int? serverVersion,
    bool? syncing,
    String? error,
    String? notice,
    bool? loaded,
    bool clearError = false,
    bool clearNotice = false,
  }) {
    return AppearanceState(
      colorScheme: colorScheme ?? this.colorScheme,
      themeMode: themeMode ?? this.themeMode,
      serverVersion: serverVersion ?? this.serverVersion,
      syncing: syncing ?? this.syncing,
      error: clearError ? null : error ?? this.error,
      notice: clearNotice ? null : notice ?? this.notice,
      loaded: loaded ?? this.loaded,
    );
  }
}

/// 未登录使用本机外观；登录后以服务端偏好为准并保存在本机作为设备默认。
class AppearanceController extends Notifier<AppearanceState> {
  static const _colorKey = 'bbo.appearance.color_scheme';
  static const _modeKey = 'bbo.appearance.theme_mode';

  SharedPreferences? _preferences;
  bool _disposed = false;

  @override
  AppearanceState build() {
    ref.onDispose(() => _disposed = true);
    unawaited(_loadLocal());
    return const AppearanceState();
  }

  Future<void> _loadLocal() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      if (_disposed) return;
      _preferences = prefs;
      final color = ColorSchemeIdValue.parse(prefs.getString(_colorKey));
      final mode = AppThemeModeValue.parse(prefs.getString(_modeKey));
      state = state.copyWith(
        colorScheme: color,
        themeMode: mode,
        loaded: true,
      );
    } catch (_) {
      if (!_disposed) {
        state = state.copyWith(loaded: true);
      }
    }
  }

  Future<void> setColorScheme(ColorSchemeId value) async {
    state = state.copyWith(colorScheme: value, clearError: true);
    await _persistLocal();
  }

  Future<void> setThemeMode(AppThemeMode value) async {
    state = state.copyWith(themeMode: value, clearError: true);
    await _persistLocal();
  }

  Future<void> applyServer(
    UserPreferences preferences, {
    int? version,
  }) async {
    final current = state;
    state = current.copyWith(
      colorScheme: preferences.colorScheme,
      themeMode: preferences.themeMode,
      serverVersion: version ?? preferences.version,
      clearError: true,
    );
    await _persistLocal();
  }

  void markSyncing(bool value) {
    state = state.copyWith(syncing: value, clearError: value);
  }

  void markSaved(int version) {
    state = state.copyWith(
      syncing: false,
      serverVersion: version,
      notice: '偏好已保存',
      clearError: true,
    );
  }

  void markError(String message) {
    state = state.copyWith(
      syncing: false,
      error: message,
      clearNotice: true,
    );
  }

  void clearNotice() {
    state = state.copyWith(clearNotice: true, clearError: true);
  }

  Future<void> _persistLocal() async {
    try {
      final prefs = _preferences ?? await SharedPreferences.getInstance();
      _preferences = prefs;
      await prefs.setString(_colorKey, state.colorScheme.value);
      await prefs.setString(_modeKey, state.themeMode.value);
    } catch (_) {
      // 本机存储失败不阻断当前外观；服务端偏好仍由设置页负责保存。
    }
  }
}

String appearanceLabel(ColorSchemeId id) => id.label;
