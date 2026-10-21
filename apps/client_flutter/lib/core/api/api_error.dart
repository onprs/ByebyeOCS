class ApiException implements Exception {
  const ApiException({
    required this.statusCode,
    required this.code,
    required this.message,
    this.requestId,
    this.retryable = false,
    this.scope,
    this.retryAfter,
  });

  final int statusCode;
  final String code;
  final String message;
  final String? requestId;
  final bool retryable;
  final String? scope;
  final Duration? retryAfter;

  bool get isUnauthenticated => statusCode == 401 || code == 'unauthenticated';
  bool get isVersionConflict => statusCode == 412 || code == 'version_conflict';
  bool get isPreconditionRequired =>
      statusCode == 428 || code == 'precondition_required';
  bool get isCsrfInvalid => code == 'csrf_invalid';
  bool get isNotConfigured =>
      code == 'not_configured' || code == 'external_disabled';
  bool get isNotFound => statusCode == 404 || code == 'not_found';
  bool get isRateLimited => statusCode == 429 || code == 'rate_limited';
  bool get isValidation => statusCode == 422 || code == 'validation_failed';
  bool get isConflict => statusCode == 409 || code == 'conflict';

  String get userMessage => message.isEmpty ? '操作未完成，请稍后重试' : message;

  @override
  String toString() => '$code($statusCode): $message';
}
