import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

class ServerConfigState {
  const ServerConfigState({
    this.baseUri,
    this.loaded = false,
    this.error,
  });

  final Uri? baseUri;
  final bool loaded;
  final String? error;

  bool get configured => baseUri != null;

  ServerConfigState copyWith({
    Uri? baseUri,
    bool? loaded,
    String? error,
    bool clearError = false,
  }) {
    return ServerConfigState(
      baseUri: baseUri ?? this.baseUri,
      loaded: loaded ?? this.loaded,
      error: clearError ? null : error ?? this.error,
    );
  }
}

class ServerConfigResult {
  const ServerConfigResult({this.uri, this.error});

  final Uri? uri;
  final String? error;

  bool get ok => uri != null;
}

/// 地址只接受 HTTPS；调试构建额外允许本机/私有网段 HTTP，正式包不放开明文。
Uri? normalizeServiceBase(String raw, {bool allowDebugHttp = false}) {
  final text = raw.trim();
  if (text.isEmpty) return null;
  final parsed = Uri.tryParse(text);
  if (parsed == null ||
      parsed.host.isEmpty ||
      parsed.hasQuery ||
      parsed.hasFragment) {
    return null;
  }
  if (parsed.userInfo.isNotEmpty) return null;
  if (parsed.scheme == 'https') {
    return _withApiPath(parsed);
  }
  if (parsed.scheme == 'http' && allowDebugHttp && _isLocalHost(parsed.host)) {
    return _withApiPath(parsed);
  }
  return null;
}

Uri _withApiPath(Uri uri) {
  var path = uri.path;
  if (path.isEmpty || path == '/') {
    path = '/api/v1/';
  } else if (path.endsWith('/api/v1')) {
    path = '$path/';
  } else if (!path.endsWith('/')) {
    path = '$path/';
  }
  return uri.replace(
    scheme: uri.scheme,
    path: path,
    query: null,
    fragment: null,
  );
}

bool _isLocalHost(String host) {
  final value = host.toLowerCase();
  if (value == 'localhost' || value == '127.0.0.1' || value == '::1') {
    return true;
  }
  if (value.startsWith('10.') || value.startsWith('192.168.')) return true;
  final parts = value.split('.');
  if (parts.length == 4 && parts[0] == '172') {
    final second = int.tryParse(parts[1]);
    if (second != null && second >= 16 && second <= 31) return true;
  }
  return false;
}

bool get isWebPlatform => kIsWeb;

/// 原生客户端默认没有服务域名。用户保存的地址优先于构建参数；Web 永远同源。
class ServerConfigController extends Notifier<ServerConfigState> {
  static const preferenceKey = 'bbo.server.api_base';

  SharedPreferences? _preferences;

  @override
  ServerConfigState build() {
    if (kIsWeb) {
      final base = Uri.base.resolve('/api/v1/');
      return ServerConfigState(baseUri: base, loaded: true);
    }
    final fromEnvironment = normalizeServiceBase(
      const String.fromEnvironment('API_BASE_URL'),
      allowDebugHttp: kDebugMode,
    );
    unawaited(_load(fromEnvironment));
    return ServerConfigState(baseUri: fromEnvironment);
  }

  Future<void> _load(Uri? fallback) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      _preferences = prefs;
      final stored = prefs.getString(preferenceKey);
      final parsed = stored == null
          ? null
          : normalizeServiceBase(stored, allowDebugHttp: kDebugMode);
      state = ServerConfigState(
        baseUri: parsed ?? fallback,
        loaded: true,
      );
    } catch (_) {
      state = ServerConfigState(baseUri: fallback, loaded: true);
    }
  }

  Future<ServerConfigResult> setBaseUrl(String raw) async {
    if (kIsWeb) {
      return const ServerConfigResult(
        error: 'Web 客户端使用同源服务，不能修改服务地址',
      );
    }
    final normalized = normalizeServiceBase(raw, allowDebugHttp: kDebugMode);
    if (normalized == null) {
      return const ServerConfigResult(
        error: '请填写以 https:// 开头的完整服务地址',
      );
    }
    try {
      final prefs = _preferences ?? await SharedPreferences.getInstance();
      _preferences = prefs;
      await prefs.setString(preferenceKey, normalized.toString());
    } catch (_) {
      return const ServerConfigResult(error: '服务地址未能保存，请稍后重试');
    }
    state = ServerConfigState(baseUri: normalized, loaded: true);
    return ServerConfigResult(uri: normalized);
  }

  Future<void> clearBaseUrl() async {
    if (kIsWeb) return;
    try {
      final prefs = _preferences ?? await SharedPreferences.getInstance();
      _preferences = prefs;
      await prefs.remove(preferenceKey);
    } catch (_) {
      // 清除失败不影响当前会话处理。
    }
    state = const ServerConfigState(loaded: true);
  }
}

final serverConfigProvider =
    NotifierProvider<ServerConfigController, ServerConfigState>(
  ServerConfigController.new,
);
