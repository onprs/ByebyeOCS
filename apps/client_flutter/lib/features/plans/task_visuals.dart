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
  if (task.isClosed) return taskClosedDetail(task);
  final code = task.lastErrorCode;
  if (code == null) return '任务需要处理';
  return switch (code) {
    'verification_required' => '任务需要本人验证（人脸或交互）',
    'identity_invalid' => '学习通身份需要重新验证',
    'forbidden' => '课程权限不足，任务已停止',
    'rate_limited' => '平台请求受限，任务已停止',
    'capability_missing' => '当前内容暂不支持自动处理',
    'quiz_closed' => '测验已关闭或当前不可作答',
    'course_closed' => '课程已结束，任务已停止',
    'submission_unconfirmed' => '已提交，平台尚未确认；自动核验已到上限',
    'verification_inconclusive' => '平台核验结果不明确；自动核验已到上限',
    'answer_unavailable' => '题库未返回可用答案，可重新查询或更换模型',
    'submission_unknown' => '提交结果未知，等待平台核验',
    'invalid_task' => '任务内容已变化，需要重新调度',
    'account_blocked' => '学习通账号当前受限',
    _ => '任务需要处理',
  };
}

/// 平台关闭导致不可派发的任务：测验关闭或整门课程结束。
bool taskIsClosed(LearningTask task) => task.isClosed;

String taskClosedLabel(LearningTask task) =>
    task.lastErrorCode == 'course_closed' ? '已结束' : '已关闭';

String taskClosedDetail(LearningTask task) => switch (task.lastErrorCode) {
      'course_closed' => '课程已结束，任务已停止',
      'chapter_closed' => '章节已关闭，任务已停止',
      _ => task.availability == 'closed' ? '章节已关闭，任务已停止' : '测验已关闭或当前不可作答',
    };
