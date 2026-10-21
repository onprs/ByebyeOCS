import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

import '../config/app_config.dart';
import '../session/session_manager.dart';
import 'api_error.dart';
import 'api_response.dart';
import 'json.dart';

enum _RefreshResult { success, invalid, unavailable }

/// 统一 HTTP 出口：Cookie/Bearer 差异、CSRF、刷新轮换、错误封套与 ETag。
class ApiClient {
  ApiClient({
    required AppConfig config,
    required SessionManager session,
    required http.Client httpClient,
    this.timeout = const Duration(seconds: 25),
  })  : _config = config,
        _session = session,
        _http = httpClient;

  final AppConfig _config;
  final SessionManager _session;
  final http.Client _http;
  final Duration timeout;

  Future<void> _refreshing = Future<void>.value();
  bool _refreshInFlight = false;

  Uri _uri(String path, Map<String, String>? query) {
    try {
      return _config.resolve(path, query);
    } on StateError {
      throw const ApiException(
        statusCode: 0,
        code: 'client_not_configured',
        message: '当前客户端尚未连接服务，请稍后重试',
      );
    }
  }

  Future<ApiResponse> get(
    String path, {
    Map<String, String>? query,
    bool authenticated = true,
    Map<String, String>? headers,
    bool parseJson = true,
  }) {
    return _send(
      'GET',
      path,
      query: query,
      authenticated: authenticated,
      extraHeaders: headers,
      parseJson: parseJson,
    );
  }

  Future<ApiResponse> post(
    String path, {
    Object? body,
    bool authenticated = true,
    String? idempotencyKey,
    Map<String, String>? headers,
  }) {
    return _send(
      'POST',
      path,
      body: body,
      authenticated: authenticated,
      idempotencyKey: idempotencyKey,
      extraHeaders: headers,
    );
  }

  Future<ApiResponse> patch(
    String path, {
    Object? body,
    bool authenticated = true,
    String? ifMatch,
    String? idempotencyKey,
  }) {
    return _send(
      'PATCH',
      path,
      body: body,
      authenticated: authenticated,
      ifMatch: ifMatch,
      idempotencyKey: idempotencyKey,
    );
  }

  Future<ApiResponse> _send(
    String method,
    String path, {
    Map<String, String>? query,
    Object? body,
    bool authenticated = true,
    String? ifMatch,
    String? idempotencyKey,
    Map<String, String>? extraHeaders,
    bool allowRefresh = true,
    bool allowCsrfRetry = true,
    bool parseJson = true,
  }) async {
    await _session.ensureLoaded();
    final uri = _uri(path, query);
    final request = http.Request(method, uri);
    request.headers['Accept'] = 'application/json';
    if (body != null) {
      request.headers['Content-Type'] = 'application/json; charset=utf-8';
      request.bodyBytes = utf8.encode(jsonEncode(body));
    }
    if (ifMatch != null && ifMatch.isNotEmpty) {
      request.headers['If-Match'] = ifMatch;
    }
    if (idempotencyKey != null && idempotencyKey.isNotEmpty) {
      request.headers['Idempotency-Key'] = idempotencyKey;
    }
    if (!_session.isWeb && authenticated) {
      final token = _session.accessToken;
      if (token != null && token.isNotEmpty) {
        request.headers['Authorization'] = 'Bearer $token';
      }
    }
    final csrf = _session.csrfToken;
    if (csrf != null && csrf.isNotEmpty) {
      request.headers['X-CSRF-Token'] = csrf;
    }
    if (extraHeaders != null) {
      request.headers.addAll(extraHeaders);
    }

    final response = await _perform(request, parseJson: parseJson);
    if (response.statusCode >= 200 && response.statusCode < 300) {
      return response;
    }

    final error = _errorFrom(response);

    if (error.isUnauthenticated && authenticated && allowRefresh) {
      final result = await _refresh();
      if (result == _RefreshResult.success) {
        return _send(
          method,
          path,
          query: query,
          body: body,
          authenticated: authenticated,
          ifMatch: ifMatch,
          idempotencyKey: idempotencyKey,
          extraHeaders: extraHeaders,
          allowRefresh: false,
          allowCsrfRetry: false,
          parseJson: parseJson,
        );
      }
      if (result == _RefreshResult.unavailable) {
        throw ApiException(
          statusCode: 503,
          code: 'service_unavailable',
          message: '登录状态暂时无法确认，请稍后重试',
          requestId: error.requestId,
          retryable: true,
        );
      }
      _session.notifyExpired();
      throw error;
    }

    if (error.isCsrfInvalid &&
        allowCsrfRetry &&
        _session.isWeb &&
        _session.csrfToken != null) {
      final refreshed = await _reloadCsrf();
      if (refreshed) {
        return _send(
          method,
          path,
          query: query,
          body: body,
          authenticated: authenticated,
          ifMatch: ifMatch,
          idempotencyKey: idempotencyKey,
          extraHeaders: extraHeaders,
          allowRefresh: false,
          allowCsrfRetry: false,
        );
      }
    }

    if (error.isUnauthenticated && authenticated) {
      _session.notifyExpired();
    }
    throw error;
  }

  Future<ApiResponse> _perform(
    http.Request request, {
    bool parseJson = true,
  }) async {
    try {
      final streamed = await _http.send(request).timeout(timeout);
      final bytes = await streamed.stream.toBytes().timeout(timeout);
      final text = utf8.decode(bytes, allowMalformed: true);
      Object? data;
      final shouldParse = parseJson || streamed.statusCode >= 400;
      if (shouldParse && text.trim().isNotEmpty) {
        try {
          data = jsonDecode(text);
        } on FormatException {
          data = null;
        }
      }
      final headers = <String, String>{};
      streamed.headers.forEach((key, value) {
        headers[key.toLowerCase()] = value;
      });
      return ApiResponse(
        statusCode: streamed.statusCode,
        data: data,
        headers: headers,
        bytes: bytes,
      );
    } on TimeoutException {
      throw const ApiException(
        statusCode: 0,
        code: 'network_timeout',
        message: '网络响应超时，请稍后重试',
        retryable: true,
      );
    } on http.ClientException {
      throw const ApiException(
        statusCode: 0,
        code: 'network_unavailable',
        message: '无法连接服务，请检查网络后重试',
        retryable: true,
      );
    }
  }

  ApiException _errorFrom(ApiResponse response) {
    final envelope = asObject(asObject(response.data)?['error']);
    final code =
        readString(envelope, 'code') ?? _fallbackCode(response.statusCode);
    final message = readString(envelope, 'message') ??
        _fallbackMessage(response.statusCode);
    final requestId = readString(envelope, 'request_id') ?? response.requestId;
    final retryAfterRaw = response.headers['retry-after'];
    final retryAfter = retryAfterRaw == null
        ? null
        : Duration(seconds: int.tryParse(retryAfterRaw) ?? 0);
    return ApiException(
      statusCode: response.statusCode,
      code: code,
      message: message,
      requestId: requestId,
      retryable: readBool(envelope, 'retryable') ??
          (response.statusCode == 429 || response.statusCode >= 500),
      scope: readString(envelope, 'scope'),
      retryAfter: retryAfter,
    );
  }

  String _fallbackCode(int status) {
    return switch (status) {
      401 => 'unauthenticated',
      403 => 'forbidden',
      404 => 'not_found',
      409 => 'conflict',
      412 => 'version_conflict',
      422 => 'validation_failed',
      428 => 'precondition_required',
      429 => 'rate_limited',
      503 => 'service_unavailable',
      _ => 'request_failed',
    };
  }

  String _fallbackMessage(int status) {
    return switch (status) {
      401 => '登录已失效，请重新验证身份',
      403 => '当前操作未获授权',
      404 => '请求的内容不存在',
      409 => '操作与当前状态冲突，请刷新后重试',
      412 => '内容已在其他位置更新，请重新加载',
      422 => '提交内容需要核对',
      428 => '需要先加载最新内容',
      429 => '操作过于频繁，请稍后再试',
      503 => '服务暂不可用，请稍后重试',
      _ => '操作未完成，请稍后重试',
    };
  }

  Future<_RefreshResult> _refresh() async {
    if (_refreshInFlight) {
      await _refreshing;
      return _lastRefreshResult;
    }
    _refreshInFlight = true;
    final completer = Completer<void>();
    _refreshing = completer.future;
    try {
      final result = _session.isWeb ? await _refreshWeb() : await _refreshApp();
      _lastRefreshResult = result;
      return result;
    } finally {
      _refreshInFlight = false;
      completer.complete();
    }
  }

  _RefreshResult _lastRefreshResult = _RefreshResult.invalid;

  Future<_RefreshResult> _refreshApp() async {
    await _session.ensureLoaded();
    final refresh = _session.refreshToken;
    if (refresh == null || refresh.isEmpty) return _RefreshResult.invalid;
    try {
      final response = await _send(
        'POST',
        'auth/refresh',
        body: {'refresh_token': refresh},
        authenticated: false,
        allowRefresh: false,
        allowCsrfRetry: false,
      );
      final data = response.object;
      final access = readString(data, 'access_token');
      final rotated = readString(data, 'refresh_token');
      if (access == null || rotated == null) {
        return _RefreshResult.invalid;
      }
      await _session.acceptRotatedTokens(
        accessToken: access,
        refreshToken: rotated,
        accessExpiresAt: readDateTime(data, 'expires_at'),
      );
      return _RefreshResult.success;
    } on ApiException catch (error) {
      if (error.isUnauthenticated) {
        await _session.clear();
        return _RefreshResult.invalid;
      }
      return _RefreshResult.unavailable;
    }
  }

  Future<_RefreshResult> _refreshWeb() async {
    try {
      final status = await _send(
        'GET',
        'auth/session',
        authenticated: false,
        allowRefresh: false,
        allowCsrfRetry: false,
      );
      final statusData = status.object;
      final authenticated = readBool(statusData, 'authenticated') ?? false;
      await _session.acceptCsrf(readString(statusData, 'csrf_token'));
      if (!authenticated) return _RefreshResult.invalid;
      final response = await _send(
        'POST',
        'auth/refresh',
        authenticated: false,
        allowRefresh: false,
        allowCsrfRetry: false,
      );
      await _session.acceptCsrf(readString(response.object, 'csrf_token'));
      return _RefreshResult.success;
    } on ApiException catch (error) {
      if (error.isUnauthenticated || error.isCsrfInvalid) {
        await _session.clear();
        return _RefreshResult.invalid;
      }
      return _RefreshResult.unavailable;
    }
  }

  Future<bool> _reloadCsrf() async {
    try {
      final response = await _send(
        'GET',
        'auth/session',
        authenticated: false,
        allowRefresh: false,
        allowCsrfRetry: false,
      );
      final data = response.object;
      if (readBool(data, 'authenticated') == false) return false;
      await _session.acceptCsrf(readString(data, 'csrf_token'));
      return _session.csrfToken != null;
    } on ApiException {
      return false;
    }
  }
}
