import 'package:flutter/material.dart';

import '../../core/format/formatters.dart';
import '../../core/models/plan.dart';
import '../../core/models/question.dart';
import '../../design/components.dart';
import '../../design/theme.dart';

/// 题目只读展示；题干、选项与图片数量来自服务端保存的查询快照。
class QuestionPromptView extends StatelessWidget {
  const QuestionPromptView({super.key, required this.prompt});

  final QuestionPrompt prompt;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      padding: const EdgeInsets.all(13),
      decoration: BoxDecoration(
        color: colors.surfaceSubtle,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            prompt.typeLabel,
            style: TextStyle(color: colors.muted, fontSize: 11),
          ),
          const SizedBox(height: 5),
          Text(
            prompt.title,
            style: TextStyle(
              color: colors.text,
              fontSize: 13.5,
              height: 1.55,
              fontWeight: FontWeight.w500,
            ),
          ),
          if (prompt.options.isNotEmpty) ...[
            const SizedBox(height: 8),
            Text(
              prompt.options,
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 12.5,
                height: 1.65,
              ),
            ),
          ],
          if (prompt.images.isNotEmpty || prompt.imageCount > 0) ...[
            const SizedBox(height: 6),
            Text(
              '题目包含 ${prompt.images.isNotEmpty ? prompt.images.length : prompt.imageCount} 张图片',
              style: TextStyle(color: colors.muted, fontSize: 11),
            ),
          ],
        ],
      ),
    );
  }
}

/// 查询结果卡片；待处理查询可提供刷新动作，已返回答案可报告问题。
class QuestionResultCard extends StatelessWidget {
  const QuestionResultCard({
    super.key,
    required this.query,
    required this.reporting,
    this.onReport,
    this.refreshing = false,
    this.onRefresh,
  });

  final QuestionQuery query;
  final bool reporting;
  final VoidCallback? onReport;
  final bool refreshing;
  final Future<void> Function()? onRefresh;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final pending = !query.answered && query.status == 'pending';
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: colors.surfaceSubtle,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Wrap(
            spacing: 8,
            runSpacing: 6,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              StatusPill(
                label: query.cacheHit ? '缓存命中' : '模型作答',
                tone: query.cacheHit ? StatusTone.success : StatusTone.accent,
                icon: query.cacheHit
                    ? Icons.storage_outlined
                    : Icons.smart_toy_outlined,
                compact: true,
              ),
              StatusPill(
                label: query.mode.label,
                tone: StatusTone.neutral,
                compact: true,
              ),
              if (pending)
                const StatusPill(
                  label: '处理中',
                  tone: StatusTone.warning,
                  compact: true,
                ),
            ],
          ),
          const SizedBox(height: 10),
          Text(
            pending ? '答案仍在处理中' : query.answer ?? '答案尚未返回',
            style: TextStyle(
              color: colors.text,
              fontSize: 16,
              fontWeight: FontWeight.w600,
              height: 1.5,
            ),
          ),
          const SizedBox(height: 5),
          Text(
            '${query.modelName ?? '平台模型'} · ${query.cacheLabel}',
            style: TextStyle(color: colors.textSecondary, fontSize: 11.5),
          ),
          const SizedBox(height: 10),
          Container(
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(
              color: colors.surface,
              borderRadius: BorderRadius.circular(7),
              border: Border.all(color: colors.border),
            ),
            child: Row(
              children: [
                _TokenValue(label: '输入 token', value: query.usage.inputTokens),
                _TokenValue(label: '输出 token', value: query.usage.outputTokens),
                _TokenValue(label: '合计', value: query.usage.totalTokens),
              ],
            ),
          ),
          const SizedBox(height: 8),
          Text(
            query.pricing.label,
            style: TextStyle(color: colors.textSecondary, fontSize: 11.5),
          ),
          if (query.cacheHit) ...[
            const SizedBox(height: 4),
            Text(
              query.cacheCreatedAt == null
                  ? '使用精确缓存答案；本次未调用模型，token 为 0。'
                  : '使用 ${formatShortDateTime(query.cacheCreatedAt)} 的精确缓存；本次未调用模型，token 为 0。',
              style: TextStyle(
                color: colors.success,
                fontSize: 11,
                height: 1.5,
              ),
            ),
          ],
          if (query.contentExpired) ...[
            const SizedBox(height: 4),
            Text(
              '题目内容已按保留规则清理，历史答案仍可查看。',
              style: TextStyle(color: colors.textSecondary, fontSize: 11),
            ),
          ],
          const SizedBox(height: 10),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              if (pending && onRefresh != null)
                AsyncButton(
                  label: '刷新结果',
                  icon: Icons.refresh,
                  busy: refreshing,
                  variant: AsyncButtonVariant.outline,
                  onPressed: onRefresh,
                ),
              AsyncButton(
                label: '报告答案问题',
                icon: Icons.flag_outlined,
                busy: reporting,
                variant: AsyncButtonVariant.outline,
                onPressed: query.answer == null ? null : onReport,
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _TokenValue extends StatelessWidget {
  const _TokenValue({required this.label, required this.value});

  final String label;
  final int value;

  @override
  Widget build(BuildContext context) {
    return Expanded(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            label,
            style: TextStyle(color: context.colors.muted, fontSize: 10),
          ),
          const SizedBox(height: 2),
          Text(
            formatCount(value),
            style: TextStyle(
              color: context.colors.text,
              fontSize: 13,
              fontWeight: FontWeight.w600,
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
        ],
      ),
    );
  }
}

/// 报告答案或缓存问题；返回去空格后的原因，取消时返回 null。
Future<String?> askQuestionReportReason(BuildContext context) {
  return showDialog<String>(
    context: context,
    builder: (_) => const _QuestionReportDialog(),
  );
}

class _QuestionReportDialog extends StatefulWidget {
  const _QuestionReportDialog();

  @override
  State<_QuestionReportDialog> createState() => _QuestionReportDialogState();
}

class _QuestionReportDialogState extends State<_QuestionReportDialog> {
  final _controller = TextEditingController();

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('报告答案问题'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text('说明答案或缓存的问题，平台会标记待复核，不会直接删除账务记录。'),
          const SizedBox(height: 12),
          TextField(
            controller: _controller,
            autofocus: true,
            maxLength: 200,
            maxLines: 3,
            decoration: const InputDecoration(hintText: '例如：答案与题目不符'),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: () {
            final value = _controller.text.trim();
            if (value.isEmpty) return;
            Navigator.of(context).pop(value);
          },
          child: const Text('提交'),
        ),
      ],
    );
  }
}

String questionReviewReasonLabel(String? reason) {
  return switch (reason) {
    'usage_incomplete' => '模型用量不完整，本次不自动扣费，等待人工核对。',
    'reserve_failed' || 'billing_not_connected' => '费用预占未完成，查询结果等待核对。',
    'billing_settlement_failed' ||
    'billing_release_failed' =>
      '费用结算出现异常，本次结果等待核对。',
    'timeout' || 'unknown' => '模型响应超时或状态未知，本次结果等待人工核对。',
    _ => '本次查询需要人工核对，未自动扣费。',
  };
}
