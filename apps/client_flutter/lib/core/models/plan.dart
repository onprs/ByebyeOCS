import '../api/json.dart';
import 'course.dart';

enum AnswerMode { cacheFirst, alwaysLlm }

extension AnswerModeValue on AnswerMode {
  String get value => switch (this) {
        AnswerMode.cacheFirst => 'cache_first',
        AnswerMode.alwaysLlm => 'always_llm',
      };

  String get label => switch (this) {
        AnswerMode.cacheFirst => '优先使用已有答案',
        AnswerMode.alwaysLlm => '每次都使用模型',
      };

  String get shortLabel => switch (this) {
        AnswerMode.cacheFirst => '精确缓存优先',
        AnswerMode.alwaysLlm => '每次调用模型',
      };

  String get description => switch (this) {
        AnswerMode.cacheFirst => '仅复用完全一致的精确缓存；命中时不会调用模型，token 为 0。',
        AnswerMode.alwaysLlm => '忽略已有答案，每次都调用所选模型，按实际 token 记录用量。',
      };

  static AnswerMode parse(String? value) {
    return value == 'always_llm' ? AnswerMode.alwaysLlm : AnswerMode.cacheFirst;
  }
}

/// 安排级题库策略由服务端按 revision 冻结；启用后自动取题、查询、填写并提交。
class QuestionPolicy {
  const QuestionPolicy({
    this.enabled = false,
    this.modelOfferingId,
    this.answerMode = AnswerMode.cacheFirst,
    this.requireManualConfirmation = false,
  });

  final bool enabled;
  final String? modelOfferingId;
  final AnswerMode answerMode;

  /// 兼容旧服务的只读字段；自动执行不需要人工确认。
  final bool requireManualConfirmation;

  QuestionPolicy copyWith({
    bool? enabled,
    Object? modelOfferingId = _unset,
    AnswerMode? answerMode,
  }) {
    return QuestionPolicy(
      enabled: enabled ?? this.enabled,
      modelOfferingId: modelOfferingId == _unset
          ? this.modelOfferingId
          : modelOfferingId as String?,
      answerMode: answerMode ?? this.answerMode,
    );
  }

  Map<String, dynamic> toInputJson() {
    return {
      'enabled': enabled,
      'model_offering_id': enabled ? modelOfferingId : null,
      'answer_mode': answerMode.value,
      'require_manual_confirmation': false,
    };
  }

  static QuestionPolicy parse(Object? raw) {
    final map = asObject(raw);
    if (map == null) return const QuestionPolicy();
    return QuestionPolicy(
      enabled: readBool(map, 'enabled') ?? false,
      modelOfferingId: readString(map, 'model_offering_id'),
      answerMode: AnswerModeValue.parse(readString(map, 'answer_mode')),
      requireManualConfirmation: false,
    );
  }

  static const _unset = Object();
}

class PlanChapterRef {
  const PlanChapterRef({required this.courseId, required this.chapterId});

  final String courseId;
  final String chapterId;

  Map<String, dynamic> toJson() => {
        'course_id': courseId,
        'chapter_id': chapterId,
      };

  static PlanChapterRef? parse(Object? raw) {
    final map = asObject(raw);
    final courseId = readString(map, 'course_id');
    final chapterId = readString(map, 'chapter_id');
    if (courseId == null || chapterId == null) return null;
    return PlanChapterRef(courseId: courseId, chapterId: chapterId);
  }
}

class PlanScope {
  const PlanScope({
    this.kind = 'all',
    this.courseIds = const <String>[],
    this.chapters = const <PlanChapterRef>[],
  });

  final String kind;
  final List<String> courseIds;
  final List<PlanChapterRef> chapters;

  bool get isSelected => kind == 'selected';

  static PlanScope parse(Object? raw) {
    final map = asObject(raw);
    return PlanScope(
      kind: readString(map, 'kind') ?? 'all',
      courseIds: readStringList(map, 'course_ids'),
      chapters: asList(map?['chapters'])
          .map(PlanChapterRef.parse)
          .whereType<PlanChapterRef>()
          .toList(growable: false),
    );
  }
}

class LearningPlan {
  const LearningPlan({
    required this.id,
    required this.name,
    required this.status,
    required this.version,
    required this.priority,
    this.courseIds = const <String>[],
    this.taskTypes = const <String>[],
    this.scope = const PlanScope(),
    this.currentRevisionId,
    this.createdAt,
    this.updatedAt,
    this.questionPolicy = const QuestionPolicy(),
  });

  final String id;
  final String name;
  final String status;
  final int version;
  final int priority;
  final List<String> courseIds;
  final List<String> taskTypes;
  final PlanScope scope;
  final String? currentRevisionId;
  final DateTime? createdAt;
  final DateTime? updatedAt;
  final QuestionPolicy questionPolicy;

  bool get isActive => status == 'active';
  bool get isPaused => status == 'paused';
  bool get isArchived => status == 'archived';

  String get priorityLabel {
    if (priority >= 67) return '优先处理';
    if (priority <= 33) return '稍后处理';
    return '普通';
  }

  String get scopeLabel {
    if (!scope.isSelected) return '全部章节';
    return '选定 ${scope.chapters.length} 个章节';
  }

  static LearningPlan parse(Map<String, dynamic> map) {
    final scope = PlanScope.parse(map['scope']);
    return LearningPlan(
      id: readString(map, 'id') ?? '',
      name: readString(map, 'name') ?? readString(map, 'title') ?? '未命名安排',
      status: readString(map, 'status') ?? 'unknown',
      version: readInt(map, 'version') ?? 0,
      priority: readInt(map, 'priority') ?? 50,
      courseIds: scope.courseIds.isNotEmpty
          ? scope.courseIds
          : readStringList(map, 'course_ids'),
      taskTypes: readStringList(map, 'task_types'),
      scope: scope,
      currentRevisionId: readString(map, 'current_revision_id'),
      createdAt: readDateTime(map, 'created_at'),
      updatedAt: readDateTime(map, 'updated_at'),
      questionPolicy: QuestionPolicy.parse(map['question_policy']),
    );
  }
}

class PlanSnapshot {
  const PlanSnapshot({required this.plan, this.etag, this.version});

  final LearningPlan plan;
  final String? etag;
  final int? version;

  int? get effectiveVersion => version ?? (etag == null ? null : _parse(etag!));

  static int? _parse(String etag) {
    final normalized = etag.replaceAll('"', '');
    final value =
        normalized.startsWith('v') ? normalized.substring(1) : normalized;
    return int.tryParse(value);
  }
}

class PlanDraft {
  const PlanDraft({
    this.name = '',
    this.courseIds = const <String>[],
    this.scopeKind = 'all',
    this.chapters = const <PlanChapterRef>[],
    this.taskTypes = const <String>['video'],
    this.priority = 50,
    this.questionPolicy = const QuestionPolicy(),
  });

  final String name;
  final List<String> courseIds;
  final String scopeKind;
  final List<PlanChapterRef> chapters;
  final List<String> taskTypes;
  final int priority;
  final QuestionPolicy questionPolicy;

  PlanDraft copyWith({
    String? name,
    List<String>? courseIds,
    String? scopeKind,
    List<PlanChapterRef>? chapters,
    List<String>? taskTypes,
    int? priority,
    QuestionPolicy? questionPolicy,
  }) {
    return PlanDraft(
      name: name ?? this.name,
      courseIds: courseIds ?? this.courseIds,
      scopeKind: scopeKind ?? this.scopeKind,
      chapters: chapters ?? this.chapters,
      taskTypes: taskTypes ?? this.taskTypes,
      priority: priority ?? this.priority,
      questionPolicy: questionPolicy ?? this.questionPolicy,
    );
  }

  factory PlanDraft.fromPlan(LearningPlan plan) {
    return PlanDraft(
      name: plan.name,
      courseIds: plan.courseIds,
      scopeKind: plan.scope.kind,
      chapters: plan.scope.chapters,
      taskTypes: plan.taskTypes,
      priority: plan.priority,
      questionPolicy: plan.questionPolicy,
    );
  }

  List<String> validate() {
    final errors = <String>[];
    final trimmed = name.trim();
    if (trimmed.isEmpty) errors.add('请填写安排名称');
    if (trimmed.length > 80) errors.add('安排名称不能超过 80 个字符');
    if (courseIds.isEmpty) errors.add('请至少选择一门课程');
    if (courseIds.length > 20) errors.add('一次最多选择 20 门课程');
    if (taskTypes.isEmpty) errors.add('请至少选择一种任务类型');
    if (scopeKind == 'selected' && chapters.isEmpty) {
      errors.add('选择章节范围时，请至少勾选一个章节');
    }
    if (scopeKind == 'selected' && chapters.length > 200) {
      errors.add('选定范围最多包含 200 个章节，可缩小范围或选择全部章节');
    }
    if (priority < 0 || priority > 100) errors.add('优先级设置无效');
    if (questionPolicy.enabled && questionPolicy.modelOfferingId == null) {
      errors.add('开启题库策略时必须选择一个已发布模型');
    }
    return errors;
  }

  Map<String, dynamic> toJson() {
    return {
      'name': name.trim(),
      'course_ids': courseIds,
      'scope': {
        'kind': scopeKind,
        'chapters': scopeKind == 'selected'
            ? chapters.map((item) => item.toJson()).toList(growable: false)
            : const <Map<String, dynamic>>[],
      },
      'task_types': taskTypes,
      'priority': priority,
      'question_policy': questionPolicy.toInputJson(),
    };
  }
}

class PlanCourseOption {
  const PlanCourseOption({required this.course, required this.chapters});

  final CourseSummary course;
  final List<CourseChapter> chapters;
}
