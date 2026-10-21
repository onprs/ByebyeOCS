import 'package:flutter/material.dart';

import '../../core/models/task.dart';
import '../../design/components.dart';

StatusTone taskStateTone(LearningTaskState state) {
  return switch (state) {
    LearningTaskState.confirmed => StatusTone.success,
    LearningTaskState.running ||
    LearningTaskState.preparing =>
      StatusTone.accent,
    LearningTaskState.waitingVerification => StatusTone.info,
    LearningTaskState.needsAttention ||
    LearningTaskState.failed =>
      StatusTone.danger,
    LearningTaskState.paused => StatusTone.warning,
    LearningTaskState.cancelled => StatusTone.neutral,
    _ => StatusTone.neutral,
  };
}

IconData taskStateIcon(LearningTaskState state) {
  return switch (state) {
    LearningTaskState.confirmed => Icons.verified_outlined,
    LearningTaskState.running => Icons.play_circle_outline,
    LearningTaskState.preparing => Icons.pending_outlined,
    LearningTaskState.waitingVerification => Icons.fact_check_outlined,
    LearningTaskState.paused => Icons.pause_circle_outline,
    LearningTaskState.needsAttention => Icons.report_problem_outlined,
    LearningTaskState.failed => Icons.error_outline,
    LearningTaskState.cancelled => Icons.block_outlined,
    _ => Icons.schedule_outlined,
  };
}

String taskFailureLabel(LearningTask task) {
  final code = task.lastErrorCode;
  if (code == null) return '任务需要处理';
  return switch (code) {
    'verification_required' || 'identity_invalid' => '学习通身份需要重新验证',
    'forbidden' => '课程权限不足，任务已停止',
    'rate_limited' => '平台请求受限，任务已停止',
    'capability_missing' => '当前内容暂不支持自动处理',
    'invalid_task' => '任务内容已变化，需要重新调度',
    'account_blocked' => '学习通账号当前受限',
    _ => '任务需要处理',
  };
}
