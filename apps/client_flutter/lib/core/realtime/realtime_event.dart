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
