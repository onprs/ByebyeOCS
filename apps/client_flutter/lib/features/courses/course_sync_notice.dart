import 'package:flutter/material.dart';

import '../../design/components.dart';
import '../../design/theme.dart';
import 'courses_controller.dart';

/// 自动课程同步的实时状态；正常完成后自动消失，仅失败时提供重试。
class CourseSyncNotice extends StatelessWidget {
  const CourseSyncNotice({
    super.key,
    required this.state,
    required this.onRetry,
  });

  final CourseListState? state;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    final current = state;
    if (current == null) return const SizedBox.shrink();
    final result = current.syncResult;
    final failed =
        current.syncError != null || (result?.needsAttention ?? false);
    if (current.syncing) {
      final colors = context.colors;
      final count = result?.courseCount ?? 0;
      return Container(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
        decoration: BoxDecoration(
          color: colors.accentSoft,
          borderRadius: colors.cardRadius,
          border: Border.all(color: colors.border),
        ),
        child: Row(
          children: [
            SizedBox(
              width: 16,
              height: 16,
              child: CircularProgressIndicator(
                strokeWidth: 2,
                color: colors.accent,
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Text(
                count > 0 ? '正在同步课程… 已读取 $count 门' : '正在同步课程…',
                style: TextStyle(
                  color: colors.text,
                  fontSize: 13,
                  height: 1.5,
                ),
              ),
            ),
          ],
        ),
      );
    }
    if (!failed) return const SizedBox.shrink();
    final partial = result?.errorCode == 'course_structure_partial';
    return InlineNotice(
      tone: StatusTone.warning,
      icon: Icons.cloud_off_outlined,
      message: current.syncError ?? '课程同步未完成，请重试',
      action: partial
          ? null
          : TextButton(onPressed: onRetry, child: const Text('重试')),
    );
  }
}
