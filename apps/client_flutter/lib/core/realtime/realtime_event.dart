// 课程只投影平台核验与关闭事实，执行观察不触发平台完成率更新。
bool isCourseProgressEvent(RealtimeEvent event) =>
    event.type.startsWith('course.') ||
    const {
      'task.confirmed',
      'task.precompleted',
      'task.platform_checked',
      'task.state_changed',
      'task.attempt_finished',
      'task.platform_confirmed_after_cancel',
      'client.poll',
      'client.resumed',
    }.contains(event.type);

class RealtimeEvent {
  const RealtimeEvent({
    required this.type,
    this.resourceId,
    this.version,
    this.data = const <String, dynamic>{},
  });

  final String type;
  final String? resourceId;
  final int? version;
  final Map<String, dynamic> data;

  String get resourceType {
    if (type.startsWith('task.')) return 'task';
    if (type.startsWith('plan.')) return 'plan';
    if (type.startsWith('course.')) return 'course';
    if (type.startsWith('wallet.')) return 'wallet';
    if (type.startsWith('recharge_order.')) return 'recharge_order';
    if (type.startsWith('question.')) return 'question';
    if (type.startsWith('account.')) return 'account';
    return 'unknown';
  }

  String? get key => resourceId == null ? null : '$resourceType:$resourceId';
}
