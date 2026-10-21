import '../api/json.dart';

class DataRequest {
  const DataRequest({
    required this.id,
    required this.kind,
    required this.status,
    this.createdAt,
    this.updatedAt,
    this.expiresAt,
    this.errorCode,
    this.reviewReason,
    this.sessionsRevoked = false,
    this.message,
  });

  final String id;
  final String kind;
  final String status;
  final DateTime? createdAt;
  final DateTime? updatedAt;
  final DateTime? expiresAt;
  final String? errorCode;
  final String? reviewReason;
  final bool sessionsRevoked;
  final String? message;

  bool get isExport => kind == 'export';
  bool get isDeletion => kind == 'deletion';
  bool get isReady => status == 'ready';
  bool get isActive => status == 'pending' || status == 'processing';
  bool get isExpired => status == 'expired';

  String get statusLabel {
    return switch (status) {
      'pending' => '已提交，等待处理',
      'processing' => '正在准备',
      'ready' => '可下载',
      'needs_review' => '待人工核对',
      'deferred' => '已延后，删除尚未完成',
      'failed' => '处理未完成',
      'expired' => '已过期',
      'completed' => '账号已关闭',
      'rejected' => '已驳回，可重新验证',
      _ => '状态待确认',
    };
  }

  String? get detailLabel {
    final reason = reviewReason?.trim();
    if (reason != null && reason.isNotEmpty) return reason;
    return switch (errorCode) {
      'export_too_large' => '导出数据量较大，需要人工核对后提供',
      'export_failed' => '导出未完成，可稍后重新申请',
      'encryption_failed' => '导出文件生成失败，可稍后重新申请',
      'step_up_required' => '需要重新验证身份后再操作',
      'closure_blocked' => '存在未结资金或运行中的活动，删除尚未完成',
      null => null,
      _ => '处理结果待核对',
    };
  }

  static DataRequest parse(Map<String, dynamic> map) {
    return DataRequest(
      id: readString(map, 'id') ?? '',
      kind: readString(map, 'kind') ?? '',
      status: readString(map, 'status') ?? 'unknown',
      createdAt: readDateTime(map, 'created_at'),
      updatedAt: readDateTime(map, 'updated_at'),
      expiresAt: readDateTime(map, 'expires_at'),
      errorCode: readString(map, 'error_code'),
      reviewReason: readString(map, 'review_reason'),
      sessionsRevoked: readBool(map, 'sessions_revoked') ?? false,
      message: readString(map, 'message'),
    );
  }
}

class ExportDownload {
  const ExportDownload({
    required this.bytes,
    required this.filename,
  });

  final List<int> bytes;
  final String filename;
}

class ExportDownloadResult {
  const ExportDownloadResult({required this.message, this.path});

  final String message;
  final String? path;
}
