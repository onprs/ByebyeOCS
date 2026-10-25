import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/format/formatters.dart';
import '../../core/models/data_request.dart';
import '../../design/components.dart';
import '../../design/theme.dart';
import '../settings/privacy_controller.dart';

class DeletionReceiptPage extends ConsumerWidget {
  const DeletionReceiptPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final receipt = ref.watch(deletionReceiptProvider);
    final colors = context.colors;
    return Scaffold(
      backgroundColor: colors.background,
      body: SafeArea(
        child: Center(
          child: SingleChildScrollView(
            padding: const EdgeInsets.all(22),
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 560),
              child: receipt == null
                  ? SectionCard(
                      child: EmptyState(
                        icon: Icons.receipt_long_outlined,
                        title: '没有可显示的删除申请回执',
                        message: '当前会话已退出。若已提交申请，请重新登录后查看申请状态。',
                        action: OutlinedButton(
                          onPressed: () => context.go('/login'),
                          child: const Text('返回登录'),
                        ),
                      ),
                    )
                  : _ReceiptCard(
                      receipt: receipt,
                      onLogin: () {
                        ref.read(deletionReceiptProvider.notifier).clear();
                        context.go('/login');
                      },
                    ),
            ),
          ),
        ),
      ),
    );
  }
}

class _ReceiptCard extends StatelessWidget {
  const _ReceiptCard({required this.receipt, required this.onLogin});

  final DataRequest receipt;
  final VoidCallback onLogin;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return SectionCard(
      padding: const EdgeInsets.all(22),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Container(
            width: 48,
            height: 48,
            decoration: BoxDecoration(
              color: colors.warningBg,
              shape: BoxShape.circle,
            ),
            child: Icon(
              Icons.fact_check_outlined,
              color: colors.warning,
              size: 23,
            ),
          ),
          const SizedBox(height: 16),
          Text(
            '删除申请已提交',
            style: TextStyle(
              color: colors.text,
              fontSize: 26,
              fontWeight: FontWeight.w600,
              letterSpacing: -0.4,
            ),
          ),
          const SizedBox(height: 8),
          Text(
            '本次会话已退出。下面仅显示提交时收到的最后已知状态；后续处理无法通过旧会话查询，也不会在这里推测。',
            style: TextStyle(
              color: colors.textSecondary,
              fontSize: 13,
              height: 1.65,
            ),
          ),
          const SizedBox(height: 18),
          Container(
            padding: const EdgeInsets.all(14),
            decoration: BoxDecoration(
              color: colors.surfaceSubtle,
              borderRadius: colors.cardRadius,
              border: Border.all(color: colors.border),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                _fact(context, '申请编号', receipt.id, selectable: true),
                const SizedBox(height: 10),
                Row(
                  children: [
                    SizedBox(
                      width: 84,
                      child: Text(
                        '最后已知状态',
                        style: TextStyle(
                          color: colors.textSecondary,
                          fontSize: 12,
                        ),
                      ),
                    ),
                    StatusPill(
                      label: receipt.statusLabel,
                      tone: receipt.status == 'needs_review'
                          ? StatusTone.warning
                          : StatusTone.neutral,
                      compact: true,
                    ),
                  ],
                ),
                if (receipt.detailLabel != null) ...[
                  const SizedBox(height: 10),
                  _fact(context, '处理说明', receipt.detailLabel!),
                ],
                if (receipt.createdAt != null) ...[
                  const SizedBox(height: 10),
                  _fact(context, '提交时间', formatDateTime(receipt.createdAt)),
                ],
              ],
            ),
          ),
          const SizedBox(height: 14),
          InlineNotice(
            icon: Icons.info_outline,
            message: _notice,
          ),
          const SizedBox(height: 18),
          Row(
            children: [
              Expanded(
                child: OutlinedButton.icon(
                  onPressed: () async {
                    await Clipboard.setData(ClipboardData(text: receipt.id));
                    if (!context.mounted) return;
                    showAppMessage(context, '申请编号已复制');
                  },
                  icon: const Icon(Icons.copy, size: 15),
                  label: const Text('复制编号'),
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: FilledButton(
                  onPressed: onLogin,
                  child: const Text('返回登录'),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  String get _notice {
    return switch (receipt.status) {
      'completed' => '账号已关闭，无法再登录查询。请凭申请编号联系管理员。',
      'rejected' => '申请已驳回。可以重新验证学习通身份进入；旧会话不会恢复，任务保持暂停。',
      'deferred' => '管理员已记录延后处理，删除尚未完成。请凭申请编号和最后已知状态联系管理员。',
      'needs_review' =>
        '资金、未结算用量与必要保留记录仍需核对。请保存申请编号，凭该编号和最后已知状态联系管理员；当前无法通过旧会话查询后续结果。',
      _ => '请保存申请编号；当前无法通过旧会话查询后续处理结果。',
    };
  }

  Widget _fact(
    BuildContext context,
    String label,
    String value, {
    bool selectable = false,
  }) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(
          width: 84,
          child: Text(
            label,
            style: TextStyle(
              color: context.colors.textSecondary,
              fontSize: 12,
            ),
          ),
        ),
        Expanded(
          child: selectable
              ? SelectableText(
                  value,
                  style: TextStyle(
                    color: context.colors.text,
                    fontSize: 12,
                    fontFamily: 'monospace',
                    height: 1.5,
                  ),
                )
              : Text(
                  value,
                  style: TextStyle(
                    color: context.colors.text,
                    fontSize: 12,
                    height: 1.5,
                  ),
                ),
        ),
      ],
    );
  }
}
