import '../api/json.dart';
import 'question.dart';

enum LearningTaskState {
  pending,
  preparing,
  running,
  waitingVerification,
  confirmed,
  paused,
  needsAttention,
  failed,
  cancelled,
  unknown,
}

extension LearningTaskStateValue on LearningTaskState {
  String get value => switch (this) {
        LearningTaskState.pending => 'pending',
        LearningTaskState.preparing => 'preparing',
        LearningTaskState.running => 'running',
        LearningTaskState.waitingVerification => 'waiting_verification',
        LearningTaskState.confirmed => 'confirmed',
        LearningTaskState.paused => 'paused',
        LearningTaskState.needsAttention => 'needs_attention',
        LearningTaskState.failed => 'failed',
        LearningTaskState.cancelled => 'cancelled',
        LearningTaskState.unknown => 'unknown',
      };

  String get label => switch (this) {
        LearningTaskState.pending => '待执行',
        LearningTaskState.preparing => '准备中',
        LearningTaskState.running => '执行中',
        LearningTaskState.waitingVerification => '等待验证',
        LearningTaskState.confirmed => '平台已确认',
        LearningTaskState.paused => '已暂停',
        LearningTaskState.needsAttention => '需要处理',
        LearningTaskState.failed => '失败',
        LearningTaskState.cancelled => '已取消',
        LearningTaskState.unknown => '状态待确认',
      };

  bool get isTerminal =>
      this == LearningTaskState.confirmed ||
      this == LearningTaskState.cancelled;

  bool get isActive =>
      this == LearningTaskState.preparing || this == LearningTaskState.running;

  bool get canCancel => !isTerminal;

  bool get canPause =>
      this == LearningTaskState.pending ||
      this == LearningTaskState.preparing ||
      this == LearningTaskState.running ||
      this == LearningTaskState.waitingVerification;

  bool get canResume => this == LearningTaskState.paused;

  bool get canRedispatch =>
      this == LearningTaskState.failed ||
      this == LearningTaskState.needsAttention ||
      this == LearningTaskState.waitingVerification ||
      this == LearningTaskState.cancelled ||
      this == LearningTaskState.paused;

  static LearningTaskState parse(String? value) {
    return switch (value) {
      'pending' => LearningTaskState.pending,
      'preparing' => LearningTaskState.preparing,
      'running' => LearningTaskState.running,
      'waiting_verification' => LearningTaskState.waitingVerification,
      'confirmed' => LearningTaskState.confirmed,
      'paused' => LearningTaskState.paused,
      'needs_attention' => LearningTaskState.needsAttention,
      'failed' => LearningTaskState.failed,
      'cancelled' => LearningTaskState.cancelled,
      _ => LearningTaskState.unknown,
    };
  }
}

class TaskPlanRef {
  const TaskPlanRef({
    required this.planId,
    this.revisionId,
    this.version,
  });

  final String planId;
  final String? revisionId;
  final int? version;

  static TaskPlanRef parse(Map<String, dynamic> map) {
    return TaskPlanRef(
      planId: readString(map, 'plan_id') ?? '',
      revisionId: readString(map, 'revision_id'),
      version: readInt(map, 'version'),
    );
  }
}

class TaskObservation {
  const TaskObservation({
    this.fraction,
    this.positionSeconds,
    this.durationSeconds,
    this.source,
    this.observedAt,
    this.note,
  });

  final double? fraction;
  final double? positionSeconds;
  final double? durationSeconds;
  final String? source;
  final DateTime? observedAt;
  final String? note;

  double? get effectiveFraction {
    final direct = fraction;
    if (direct != null) return direct.clamp(0, 1).toDouble();
    final position = positionSeconds;
    final duration = durationSeconds;
    if (position != null && duration != null && duration > 0) {
      return (position / duration).clamp(0, 1).toDouble();
    }
    return null;
  }

  static TaskObservation? parse(Object? raw) {
    final map = asObject(raw);
    if (map == null) return null;
    return TaskObservation(
      fraction: readDouble(map, 'fraction'),
      positionSeconds: readDouble(map, 'position_seconds'),
      durationSeconds: readDouble(map, 'duration_seconds'),
      source: readString(map, 'source'),
      observedAt: readDateTime(map, 'observed_at'),
      note: readString(map, 'note'),
    );
  }
}

class PlatformFact {
  const PlatformFact({
    this.status = 'unknown',
    this.passed = false,
    this.checkedAt,
    this.source,
    this.playTimeSeconds,
    this.note,
  });

  final String status;
  final bool passed;
  final DateTime? checkedAt;
  final String? source;
  final double? playTimeSeconds;
  final String? note;

  bool get isConfirmed => status == 'confirmed' && passed;

  String get label {
    if (isConfirmed) return '平台已确认通过';
    return switch (status) {
      'unconfirmed' => '平台尚未确认',
      'unknown' => '平台结果待查询',
      _ => '平台结果待确认',
    };
  }

  static PlatformFact parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return PlatformFact(
      status: readString(map, 'status') ?? 'unknown',
      passed: readBool(map, 'passed') ?? false,
      checkedAt: readDateTime(map, 'checked_at'),
      source: readString(map, 'source'),
      playTimeSeconds: readDouble(map, 'play_time_seconds'),
      note: readString(map, 'note'),
    );
  }
}

class MediaEvidence {
  const MediaEvidence({
    this.available = false,
    this.source,
    this.detail,
    this.observedAt,
  });

  final bool available;
  final String? source;
  final String? detail;
  final DateTime? observedAt;

  bool get hasRecord => available;

  static MediaEvidence parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return MediaEvidence(
      available: readBool(map, 'available') ?? false,
      source: readString(map, 'source'),
      detail: readString(map, 'detail'),
      observedAt: readDateTime(map, 'observed_at'),
    );
  }
}

class TaskBilling {
  const TaskBilling({
    this.state = 'disabled',
    this.configured = false,
    this.amountKind,
    this.amountMinor,
    this.currency,
    this.priceVersion,
    this.errorCode,
  });

  final String state;
  final bool configured;
  final String? amountKind;
  final int? amountMinor;
  final String? currency;
  final String? priceVersion;
  final String? errorCode;

  bool get hasPublishedPrice =>
      configured && amountMinor != null && amountMinor! > 0;

  static TaskBilling parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return TaskBilling(
      state: readString(map, 'state') ?? 'disabled',
      configured: readBool(map, 'configured') ?? false,
      amountKind: readString(map, 'amount_kind'),
      amountMinor:
          readInt(map, 'amount_minor') ?? readInt(map, 'quoted_amount_minor'),
      currency: readString(map, 'currency'),
      priceVersion: readString(map, 'price_version'),
      errorCode: readString(map, 'error_code'),
    );
  }
}

class TaskEstimate {
  const TaskEstimate({
    this.remainingSecondsMin,
    this.remainingSecondsMax,
    this.estimated = false,
  });

  final int? remainingSecondsMin;
  final int? remainingSecondsMax;
  final bool estimated;

  bool get hasValue =>
      estimated && remainingSecondsMin != null && remainingSecondsMax != null;

  static TaskEstimate parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return TaskEstimate(
      remainingSecondsMin: readInt(map, 'remaining_seconds_min'),
      remainingSecondsMax: readInt(map, 'remaining_seconds_max'),
      estimated: readBool(map, 'estimated') ?? false,
    );
  }
}

class LearningTask {
  const LearningTask({
    required this.id,
    required this.state,
    required this.title,
    this.desiredState = '',
    this.version = 0,
    this.priority = 50,
    this.taskKind,
    this.taskPointId,
    this.courseId,
    this.courseTitle,
    this.chapterId,
    this.chapterTitle,
    this.planRefs = const <TaskPlanRef>[],
    this.attemptCount = 0,
    this.failureCount = 0,
    this.verificationAttempts = 0,
    this.lastErrorCode,
    this.nextAttemptAt,
    this.confirmedAt,
    this.platform = const PlatformFact(),
    this.observation,
    this.media = const MediaEvidence(),
    this.billing = const TaskBilling(),
    this.estimate = const TaskEstimate(),
    this.questionPrompt,
    this.questionQueryId,
    this.createdAt,
    this.updatedAt,
  });

  final String id;
  final String desiredState;
  final LearningTaskState state;
  final int version;
  final int priority;
  final String? taskKind;
  final String title;
  final String? taskPointId;
  final String? courseId;
  final String? courseTitle;
  final String? chapterId;
  final String? chapterTitle;
  final List<TaskPlanRef> planRefs;
  final int attemptCount;
  final int failureCount;
  final int verificationAttempts;
  final String? lastErrorCode;
  final DateTime? nextAttemptAt;
  final DateTime? confirmedAt;
  final PlatformFact platform;
  final TaskObservation? observation;
  final MediaEvidence media;
  final TaskBilling billing;
  final TaskEstimate estimate;
  final QuestionPrompt? questionPrompt;
  final String? questionQueryId;
  final DateTime? createdAt;
  final DateTime? updatedAt;

  String get kindLabel {
    return switch (taskKind) {
      'video' => '视频',
      'document' => '文档',
      'audio' => '音频',
      'quiz' => '测验',
      'other' => '学习内容',
      _ => '学习内容',
    };
  }

  String? get primaryPlanId => planRefs.isEmpty ? null : planRefs.first.planId;

  static LearningTask parse(Map<String, dynamic> map) {
    final questionMap = asObject(map['question_query']) ??
        asObject(map['question_prompt']) ??
        asObject(map['quiz_question']);
    final nestedQuestion = asObject(questionMap?['question']);
    final rawQuestion = asObject(map['question']);
    final promptSource =
        rawQuestion != null && readString(rawQuestion, 'title') != null
            ? rawQuestion
            : nestedQuestion ?? asObject(questionMap?['prompt']);
    final prompt =
        promptSource == null ? null : QuestionPrompt.parse(promptSource);
    final queryId = readString(map, 'question_query_id') ??
        readString(questionMap, 'id') ??
        readString(rawQuestion, 'id');
    return LearningTask(
      id: readString(map, 'id') ?? '',
      desiredState: readString(map, 'desired_state') ?? '',
      state: LearningTaskStateValue.parse(readString(map, 'state')),
      version: readInt(map, 'version') ?? 0,
      priority: readInt(map, 'priority') ?? 50,
      taskKind: readString(map, 'task_kind'),
      title: readString(map, 'title') ?? '未命名任务',
      taskPointId: readString(map, 'task_point_id'),
      courseId: readString(map, 'course_id'),
      courseTitle: readString(map, 'course_title'),
      chapterId: readString(map, 'chapter_id'),
      chapterTitle: readString(map, 'chapter_title'),
      planRefs: asList(map['plan_refs'])
          .map(asObject)
          .whereType<Map<String, dynamic>>()
          .map(TaskPlanRef.parse)
          .toList(growable: false),
      attemptCount: readInt(map, 'attempt_count') ?? 0,
      failureCount: readInt(map, 'failure_count') ?? 0,
      verificationAttempts: readInt(map, 'verification_attempts') ?? 0,
      lastErrorCode: readString(map, 'last_error_code'),
      nextAttemptAt: readDateTime(map, 'next_attempt_at'),
      confirmedAt: readDateTime(map, 'confirmed_at'),
      platform: PlatformFact.parse(map['platform']),
      observation: TaskObservation.parse(map['observation']),
      media: MediaEvidence.parse(map['media_evidence']),
      billing: TaskBilling.parse(map['billing']),
      estimate: TaskEstimate.parse(map['estimate']),
      questionPrompt: prompt != null && prompt.hasContent ? prompt : null,
      questionQueryId: queryId,
      createdAt: readDateTime(map, 'created_at'),
      updatedAt: readDateTime(map, 'updated_at'),
    );
  }
}

class TaskAttempt {
  const TaskAttempt({
    required this.id,
    required this.attemptNo,
    this.state,
    this.startedAt,
    this.endedAt,
    this.result,
    this.errorCode,
    this.observation,
    this.platform = const PlatformFact(),
    this.media = const MediaEvidence(),
  });

  final String id;
  final int attemptNo;
  final String? state;
  final DateTime? startedAt;
  final DateTime? endedAt;
  final String? result;
  final String? errorCode;
  final TaskObservation? observation;
  final PlatformFact platform;
  final MediaEvidence media;

  static TaskAttempt parse(Map<String, dynamic> map) {
    return TaskAttempt(
      id: readString(map, 'id') ?? '',
      attemptNo: readInt(map, 'attempt_no') ?? 0,
      state: readString(map, 'state'),
      startedAt: readDateTime(map, 'started_at'),
      endedAt: readDateTime(map, 'ended_at'),
      result: readString(map, 'result'),
      errorCode: readString(map, 'error_code'),
      observation: TaskObservation.parse(map['observation']),
      platform: PlatformFact.parse(map['platform']),
      media: MediaEvidence.parse(map['media_evidence']),
    );
  }
}

class TaskCommand {
  const TaskCommand({
    required this.id,
    required this.kind,
    this.status,
    this.resultCode,
    this.reason,
    this.createdAt,
    this.appliedAt,
  });

  final String id;
  final String kind;
  final String? status;
  final String? resultCode;
  final String? reason;
  final DateTime? createdAt;
  final DateTime? appliedAt;

  String get label {
    return switch (kind) {
      'pause' => '暂停',
      'resume' => '继续',
      'cancel' => '取消',
      'redispatch' => '重新调度',
      _ => '操作记录',
    };
  }

  static TaskCommand parse(Map<String, dynamic> map) {
    return TaskCommand(
      id: readString(map, 'id') ?? '',
      kind: readString(map, 'kind') ?? '',
      status: readString(map, 'status'),
      resultCode: readString(map, 'result_code'),
      reason: readString(map, 'reason'),
      createdAt: readDateTime(map, 'created_at'),
      appliedAt: readDateTime(map, 'applied_at'),
    );
  }
}

class TaskDetail {
  const TaskDetail({
    required this.task,
    this.attempts = const <TaskAttempt>[],
    this.commands = const <TaskCommand>[],
  });

  final LearningTask task;
  final List<TaskAttempt> attempts;
  final List<TaskCommand> commands;

  TaskObservation? get latestObservation {
    final direct = task.observation;
    if (direct != null) return direct;
    for (final attempt in attempts) {
      if (attempt.observation != null) return attempt.observation;
    }
    return null;
  }

  PlatformFact get latestPlatform {
    if (task.platform.checkedAt != null || task.platform.status != 'unknown') {
      return task.platform;
    }
    for (final attempt in attempts) {
      if (attempt.platform.checkedAt != null ||
          attempt.platform.status != 'unknown') {
        return attempt.platform;
      }
    }
    return task.platform;
  }

  MediaEvidence get latestMedia {
    if (task.media.available) return task.media;
    for (final attempt in attempts) {
      if (attempt.media.available) return attempt.media;
    }
    return task.media;
  }

  static TaskDetail parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return TaskDetail(
      task: LearningTask.parse(map),
      attempts: asList(map['attempts'])
          .map(asObject)
          .whereType<Map<String, dynamic>>()
          .map(TaskAttempt.parse)
          .toList(growable: false),
      commands: asList(map['commands'])
          .map(asObject)
          .whereType<Map<String, dynamic>>()
          .map(TaskCommand.parse)
          .toList(growable: false),
    );
  }
}

class TaskControlResult {
  const TaskControlResult({
    required this.taskId,
    required this.state,
    this.desiredState,
    this.applied = false,
    this.stopRequested = false,
    this.resultCode,
    this.version,
  });

  final String taskId;
  final String state;
  final String? desiredState;
  final bool applied;
  final bool stopRequested;
  final String? resultCode;
  final int? version;

  String get message {
    if (stopRequested || !applied) {
      return '已提交停止请求，正在等待任务安全停下';
    }
    return '操作已生效，正在刷新任务状态';
  }

  static TaskControlResult parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return TaskControlResult(
      taskId: readString(map, 'task_id') ?? '',
      state: readString(map, 'state') ?? 'unknown',
      desiredState: readString(map, 'desired_state'),
      applied: readBool(map, 'applied') ?? false,
      stopRequested: readBool(map, 'stop_requested') ?? false,
      resultCode: readString(map, 'result_code'),
      version: readInt(map, 'version'),
    );
  }
}

class TaskCommandRequest {
  const TaskCommandRequest({
    required this.action,
    this.reason,
  });

  final String action;
  final String? reason;

  Map<String, dynamic> toJson() => {
        'action': action,
        if (reason != null && reason!.trim().isNotEmpty)
          'reason': reason!.trim(),
      };
}

class PlanControlResult {
  const PlanControlResult({
    required this.planId,
    required this.status,
    this.applied = false,
  });

  final String planId;
  final String status;
  final bool applied;

  static PlanControlResult parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return PlanControlResult(
      planId: readString(map, 'plan_id') ?? '',
      status: readString(map, 'status') ?? 'unknown',
      applied: readBool(map, 'applied') ?? false,
    );
  }
}
