import '../api/json.dart';

class CourseSummary {
  const CourseSummary({
    required this.id,
    required this.title,
    this.teacher,
    this.term,
    this.chapterCount = 0,
    this.taskPointCount = 0,
    this.incompleteTaskPointCount = 0,
    this.resourceCount = 0,
    this.snapshotVersion,
    this.syncedAt,
    this.stale = false,
    this.coverUrl,
  });

  final String id;
  final String title;
  final String? teacher;
  final String? term;
  final int chapterCount;
  final int taskPointCount;
  final int incompleteTaskPointCount;
  final int resourceCount;
  final int? snapshotVersion;
  final DateTime? syncedAt;
  final bool stale;

  /// API 根目录相对的封面地址；缺失或为空表示没有可用来源。
  final String? coverUrl;

  int get confirmedTaskPoints =>
      (taskPointCount - incompleteTaskPointCount).clamp(0, taskPointCount);

  /// 课程概要已发布，但章节和个人进度尚未完整读取。
  bool get detailsPending => snapshotVersion == 0;

  bool get allConfirmed => taskPointCount > 0 && incompleteTaskPointCount == 0;

  static CourseSummary parse(Map<String, dynamic> map) {
    return CourseSummary(
      id: readString(map, 'id') ?? '',
      title: readString(map, 'title') ?? '未命名课程',
      teacher: readString(map, 'teacher'),
      term: readString(map, 'term'),
      chapterCount: readInt(map, 'chapter_count') ?? 0,
      taskPointCount: readInt(map, 'task_point_count') ?? 0,
      incompleteTaskPointCount:
          readInt(map, 'incomplete_task_point_count') ?? 0,
      resourceCount: readInt(map, 'resource_count') ?? 0,
      snapshotVersion: readInt(map, 'snapshot_version'),
      syncedAt: readDateTime(map, 'synced_at'),
      stale: readBool(map, 'stale') ?? false,
      coverUrl: readString(map, 'cover_url'),
    );
  }
}

final _courseCoverUuid = RegExp(
  r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$',
);

final _courseCoverDigest = RegExp(r'^[0-9a-f]{64}$');

/// 课程封面地址只接受 API 根目录相对的 `courses/<UUID>/cover?v=<摘要>`。
///
/// 绝对网址、根路径、路径穿越与其他接口都会让认证头或请求落到非预期位置，
/// 因此这里重建规范化地址；无法确认时返回 null，由调用方使用占位图。
String? normalizeCourseCoverPath(String? raw) {
  final value = raw?.trim();
  if (value == null || value.isEmpty || value.length > 256) return null;
  final uri = Uri.tryParse(value);
  if (uri == null) return null;
  if (uri.hasScheme || uri.hasAuthority || uri.hasFragment) return null;
  if (value.startsWith('/') || value.startsWith('\\')) return null;
  final segments = uri.pathSegments;
  if (segments.length != 3) return null;
  if (segments[0] != 'courses' || segments[2] != 'cover') return null;
  final courseId = segments[1];
  if (!_courseCoverUuid.hasMatch(courseId)) return null;
  final query = uri.queryParametersAll;
  if (query.keys.any((key) => key != 'v')) return null;
  final digests = query['v'] ?? const <String>[];
  if (digests.length > 1) return null;
  final digest = digests.isEmpty ? null : digests.first;
  if (digest != null && !_courseCoverDigest.hasMatch(digest)) return null;
  return 'courses/$courseId/cover${digest == null ? '' : '?v=$digest'}';
}

class CourseAttachment {
  const CourseAttachment({
    required this.id,
    required this.title,
    this.objectId,
    this.taskPointId,
    this.kind,
    this.durationSeconds,
    this.playedSeconds,
    this.platformPassed = false,
    this.passedKnown = false,
    this.isTaskPoint = false,
    this.countedIncomplete = false,
    this.progressCheckedAt,
  });

  final String id;
  final String? objectId;
  final String? taskPointId;
  final String title;
  final String? kind;
  final double? durationSeconds;
  final double? playedSeconds;
  final bool platformPassed;
  final bool passedKnown;
  final bool isTaskPoint;
  final bool countedIncomplete;
  final DateTime? progressCheckedAt;

  String get kindLabel {
    return switch (kind) {
      'video' => '视频',
      'document' => '文档',
      'audio' => '音频',
      'quiz' => '测验',
      'other' => '学习内容',
      null => '学习内容',
      _ => '学习内容',
    };
  }

  bool get hasPlaybackEvidence {
    final played = playedSeconds;
    return isTaskPoint && played != null && played > 0;
  }

  static CourseAttachment parse(Map<String, dynamic> map) {
    return CourseAttachment(
      id: readString(map, 'id') ?? '',
      objectId: readString(map, 'object_id'),
      taskPointId: readString(map, 'task_point_id'),
      title: readString(map, 'title') ?? '未命名内容',
      kind: readString(map, 'kind'),
      durationSeconds: readDouble(map, 'duration_seconds'),
      playedSeconds: readDouble(map, 'played_seconds'),
      platformPassed: readBool(map, 'platform_passed') ?? false,
      passedKnown: readBool(map, 'passed_known') ?? false,
      isTaskPoint: readBool(map, 'is_task_point') ?? false,
      countedIncomplete: readBool(map, 'counted_incomplete') ?? false,
      progressCheckedAt: readDateTime(map, 'progress_checked_at'),
    );
  }
}

class CourseChapter {
  const CourseChapter({
    required this.id,
    required this.title,
    this.externalId,
    this.sortOrder = 0,
    this.attachments = const <CourseAttachment>[],
  });

  final String id;
  final String? externalId;
  final String title;
  final int sortOrder;
  final List<CourseAttachment> attachments;

  List<CourseAttachment> get taskPoints =>
      attachments.where((item) => item.isTaskPoint).toList(growable: false);

  List<CourseAttachment> get resources =>
      attachments.where((item) => !item.isTaskPoint).toList(growable: false);

  int get confirmedCount => taskPoints
      .where((item) => item.passedKnown && item.platformPassed)
      .length;

  int get incompleteCount =>
      taskPoints.where((item) => item.countedIncomplete).length;

  static CourseChapter parse(Map<String, dynamic> map) {
    return CourseChapter(
      id: readString(map, 'id') ?? '',
      externalId: readString(map, 'external_id'),
      title: readString(map, 'title') ?? '未命名章节',
      sortOrder: readInt(map, 'sort_order') ?? 0,
      attachments: asList(map['attachments'])
          .map(asObject)
          .whereType<Map<String, dynamic>>()
          .map(CourseAttachment.parse)
          .toList(growable: false),
    );
  }
}

class CourseDetail {
  const CourseDetail({
    required this.summary,
    this.chapters = const <CourseChapter>[],
  });

  final CourseSummary summary;
  final List<CourseChapter> chapters;

  static CourseDetail parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return CourseDetail(
      summary: CourseSummary.parse(map),
      chapters: asList(map['chapters'])
          .map(asObject)
          .whereType<Map<String, dynamic>>()
          .map(CourseChapter.parse)
          .toList(growable: false),
    );
  }
}

class CourseSyncResult {
  const CourseSyncResult({
    required this.id,
    required this.status,
    this.courseCount = 0,
    this.created = false,
    this.errorCode,
    this.requestedAt,
    this.finishedAt,
  });

  final String id;
  final String status;
  final int courseCount;
  final bool created;
  final String? errorCode;
  final DateTime? requestedAt;
  final DateTime? finishedAt;

  bool get accepted => status == 'queued' || status == 'running';
  bool get running => accepted;
  bool get succeeded => status == 'succeeded';
  bool get failed => status == 'failed';
  bool get needsAttention => status == 'needs_attention' || status == 'failed';

  /// 服务端完成时间；旧记录缺失时退回请求时间，供数据过期判断使用。
  DateTime? get completedAt => finishedAt ?? requestedAt;

  static CourseSyncResult parse(Object? raw, {bool created = false}) {
    final root = asObject(raw) ?? const <String, dynamic>{};
    final sync = asObject(root['sync']) ?? root;
    return CourseSyncResult(
      id: readString(sync, 'id') ?? '',
      status: readString(sync, 'status') ?? 'queued',
      courseCount: readInt(sync, 'course_count') ?? 0,
      created: readBool(root, 'created') ?? created,
      errorCode: readString(sync, 'error_code'),
      requestedAt: readDateTime(sync, 'requested_at'),
      finishedAt: readDateTime(sync, 'finished_at'),
    );
  }
}
