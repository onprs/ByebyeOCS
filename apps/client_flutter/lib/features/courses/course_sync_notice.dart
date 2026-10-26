import 'package:flutter/material.dart';

import '../../design/components.dart';
import '../../design/theme.dart';
import 'course_sync_controller.dart';

/// 课程同步的唯一内联状态展示：运行中显示进度，结束后的提示按 sync id 关闭。
class CourseSyncNotice extends StatelessWidget {
  const CourseSyncNotice({
    super.key,
    required this.state,
    required this.onDismiss,
    required this.onRetry,
  });

  final CourseSyncState state;
  final VoidCallback onDismiss;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    if (!state.visible) return const SizedBox.shrink();
    if (state.busy) return _SyncProgress(state: state);
    final failed = state.phase == CourseSyncPhase.failed;
    final partial = state.phase == CourseSyncPhase.partial;
    return InlineNotice(
      tone: failed
          ? StatusTone.danger
          : partial
              ? StatusTone.warning
              : StatusTone.success,
      icon: failed
          ? Icons.cloud_off_outlined
          : partial
              ? Icons.cloud_queue_outlined
              : Icons.cloud_done_outlined,
      message: state.message,
      action: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (state.canRetry)
            TextButton(
              onPressed: onRetry,
              child: const Text('重试'),
            ),
          IconButton(
            tooltip: '关闭',
            onPressed: onDismiss,
            icon: const Icon(Icons.close, size: 17),
          ),
        ],
      ),
    );
  }
}

class _SyncProgress extends StatelessWidget {
  const _SyncProgress({required this.state});

  final CourseSyncState state;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
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
              state.message,
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
}
