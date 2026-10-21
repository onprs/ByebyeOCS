import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/api/json.dart';
import '../../core/format/formatters.dart';
import '../../core/models/plan.dart';
import '../../core/models/question.dart';
import '../../core/models/task.dart';
import '../../design/components.dart';
import '../../design/theme.dart';
import '../questions/question_providers.dart';
import 'plans_controller.dart';
import 'task_visuals.dart';

Future<void> showTaskDetailSheet(
  BuildContext context,
  String taskId,
) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    useSafeArea: true,
    builder: (context) => TaskDetailSheet(taskId: taskId),
  );
}

class TaskDetailSheet extends ConsumerWidget {
  const TaskDetailSheet({super.key, required this.taskId});

  final String taskId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(taskDetailProvider(taskId));
    final media = MediaQuery.of(context);
    final height = media.size.height;
    final wide = media.size.width >= 760;
    return Container(
      constraints: BoxConstraints(
        maxWidth: wide ? 860 : double.infinity,
        maxHeight: height * 0.94,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(22, 4, 14, 12),
            child: Row(
              children: [
                Expanded(
                  child: async.when(
                    loading: () => const SheetHeader(title: '任务进度'),
                    error: (_, __) => const SheetHeader(title: '任务进度'),
                    data: (data) => SheetHeader(
                      title: data.detail.task.title,
                      subtitle:
                          '${data.detail.task.courseTitle ?? '课程'} · ${data.detail.task.kindLabel}',
                    ),
                  ),
                ),
                IconButton(
                  tooltip: '刷新任务',
                  onPressed: () => ref
                      .read(taskDetailProvider(taskId).notifier)
                      .refresh(silent: true),
                  icon: const Icon(Icons.refresh, size: 19),
                ),
                IconButton(
                  tooltip: '关闭',
                  onPressed: () => Navigator.pop(context),
                  icon: const Icon(Icons.close, size: 20),
                ),
              ],
            ),
          ),
          Divider(height: 1, color: context.colors.border),
          Flexible(
            child: async.when(
              loading: () => const Padding(
                padding: EdgeInsets.all(24),
                child: LoadingList(rows: 3, height: 120),
              ),
              error: (error, _) => Padding(
                padding: const EdgeInsets.all(22),
                child: ErrorPanel(
                  message:
                      error is ApiException ? error.userMessage : '任务详情暂时无法加载',
                  requestId: error is ApiException ? error.requestId : null,
                  onRetry: () => ref.invalidate(taskDetailProvider(taskId)),
                ),
              ),
              data: (data) => _TaskDetailBody(
                taskId: taskId,
                state: data,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _TaskDetailBody extends ConsumerStatefulWidget {
  const _TaskDetailBody({required this.taskId, required this.state});

  final String taskId;
  final TaskDetailState state;

  @override
  ConsumerState<_TaskDetailBody> createState() => _TaskDetailBodyState();
}

class _TaskDetailBodyState extends ConsumerState<_TaskDetailBody> {
  bool _sending = false;

  @override
  Widget build(BuildContext context) {
    final task = widget.state.detail.task;
    final observation = widget.state.detail.latestObservation;
    final platform = widget.state.detail.latestPlatform;
    final media = widget.state.detail.latestMedia;
    final colors = context.colors;
    return Column(
      children: [
        Flexible(
          child: SingleChildScrollView(
            padding: const EdgeInsets.fromLTRB(22, 18, 22, 24),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(
                      child: Wrap(
                        spacing: 8,
                        runSpacing: 8,
                        crossAxisAlignment: WrapCrossAlignment.center,
                        children: [
                          StatusPill(
                            label: task.state.label,
                            tone: taskStateTone(task.state),
                            icon: taskStateIcon(task.state),
                          ),
                          if (task.estimate.hasValue)
                            const StatusPill(
                              label: '预计时间为估算',
                              tone: StatusTone.neutral,
                              compact: true,
                            ),
                          if (task.media.available)
                            const StatusPill(
                              label: '有媒体记录',
                              tone: StatusTone.info,
                              compact: true,
                            )
                          else
                            const StatusPill(
                              label: '无播放记录',
                              tone: StatusTone.neutral,
                              compact: true,
                            ),
                        ],
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 10),
                Text(
                  [
                    task.courseTitle,
                    task.chapterTitle,
                    task.taskKind == null ? null : task.kindLabel,
                  ].whereType<String>().join(' · '),
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 12.5,
                    height: 1.5,
                  ),
                ),
                if (task.state == LearningTaskState.needsAttention ||
                    task.state == LearningTaskState.failed) ...[
                  const SizedBox(height: 14),
                  InlineNotice(
                    tone: StatusTone.danger,
                    icon: Icons.report_problem_outlined,
                    message: '${taskFailureLabel(task)}。相关操作已停止，不会自动重试。',
                  ),
                ],
                if (widget.state.error != null) ...[
                  const SizedBox(height: 12),
                  ErrorPanel(message: widget.state.error!, compact: true),
                ],
                if (widget.state.notice != null) ...[
                  const SizedBox(height: 12),
                  InlineNotice(
                    tone: StatusTone.success,
                    icon: Icons.info_outline,
                    message: widget.state.notice!,
                    action: IconButton(
                      tooltip: '关闭',
                      onPressed: () => ref
                          .read(taskDetailProvider(widget.taskId).notifier)
                          .clearNotice(),
                      icon: const Icon(Icons.close, size: 16),
                    ),
                  ),
                ],
                const SizedBox(height: 18),
                LayoutBuilder(
                  builder: (context, constraints) {
                    final wide = constraints.maxWidth >= 650;
                    final cards = [
                      _EvidenceCard(
                        icon: Icons.monitor_heart_outlined,
                        title: '执行观察',
                        value: observation == null
                            ? '尚无执行观察'
                            : observation.effectiveFraction == null
                                ? '已记录观察'
                                : '已观察 ${formatPercent(observation.effectiveFraction)}',
                        detail: observation?.observedAt == null
                            ? '暂时还没有新的进度观察。'
                            : '更新于 ${formatDateTime(observation!.observedAt)}',
                        note: observation?.note,
                        tone: StatusTone.accent,
                        progress: observation?.effectiveFraction,
                      ),
                      _EvidenceCard(
                        icon: platform.isConfirmed
                            ? Icons.verified_outlined
                            : Icons.fact_check_outlined,
                        title: '平台确认',
                        value: platform.label,
                        detail: platform.checkedAt == null
                            ? '执行结束后会再次查询学习通。'
                            : '查询于 ${formatDateTime(platform.checkedAt)}',
                        note: platform.note,
                        tone: platform.isConfirmed
                            ? StatusTone.success
                            : StatusTone.warning,
                      ),
                      _EvidenceCard(
                        icon: Icons.videocam_outlined,
                        title: '真实媒体记录',
                        value: media.available ? '已记录媒体证据' : '无播放记录',
                        detail: media.available
                            ? '已收到本次任务的媒体记录。'
                            : '本任务没有可用的真实播放证据。',
                        note: media.observedAt == null
                            ? null
                            : '记录于 ${formatDateTime(media.observedAt)}',
                        tone: media.available
                            ? StatusTone.info
                            : StatusTone.neutral,
                      ),
                    ];
                    if (!wide) {
                      return Column(
                        children: [
                          for (var index = 0;
                              index < cards.length;
                              index++) ...[
                            cards[index],
                            if (index != cards.length - 1)
                              const SizedBox(height: 10),
                          ],
                        ],
                      );
                    }
                    return Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        for (var index = 0; index < cards.length; index++) ...[
                          Expanded(child: cards[index]),
                          if (index != cards.length - 1)
                            const SizedBox(width: 10),
                        ],
                      ],
                    );
                  },
                ),
                const SizedBox(height: 16),
                _FactsGrid(task: task),
                const SizedBox(height: 16),
                _BillingCard(task: task),
                if (task.taskKind == 'quiz' ||
                    task.questionPrompt != null ||
                    task.questionQueryId != null) ...[
                  const SizedBox(height: 16),
                  _QuestionPanel(task: task),
                ],
                const SizedBox(height: 16),
                _HistorySection(detail: widget.state.detail),
              ],
            ),
          ),
        ),
        Container(
          padding: const EdgeInsets.fromLTRB(22, 12, 22, 16),
          decoration: BoxDecoration(
            color: colors.surface,
            border: Border(top: BorderSide(color: colors.border)),
          ),
          child: SafeArea(
            top: false,
            child: _Actions(
              task: task,
              busy: widget.state.commandPending || _sending,
              onCommand: _command,
            ),
          ),
        ),
      ],
    );
  }

  Future<void> _command(String action) async {
    if (_sending) return;
    if (action == 'cancel' || action == 'redispatch') {
      final confirmed = await showConfirmDialog(
        context,
        title: action == 'cancel' ? '取消这个任务？' : '重新调度这个任务？',
        message: action == 'cancel'
            ? '取消后任务会进入已取消状态；尚未消费的预占费用会自动释放。'
            : '重新调度会让任务重新进入队列，不会重复创建任务或直接产生新扣款。',
        confirmLabel: action == 'cancel' ? '取消任务' : '重新调度',
        danger: action == 'cancel',
      );
      if (!confirmed || !mounted) return;
    }
    setState(() => _sending = true);
    await ref.read(taskDetailProvider(widget.taskId).notifier).command(action);
    if (!mounted) return;
    setState(() => _sending = false);
    await ref.read(plansControllerProvider.notifier).refresh(silent: true);
  }
}

class _EvidenceCard extends StatelessWidget {
  const _EvidenceCard({
    required this.icon,
    required this.title,
    required this.value,
    required this.detail,
    this.note,
    this.tone = StatusTone.neutral,
    this.progress,
  });

  final IconData icon;
  final String title;
  final String value;
  final String detail;
  final String? note;
  final StatusTone tone;
  final double? progress;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final (foreground, background) = switch (tone) {
      StatusTone.success => (colors.success, colors.successBg),
      StatusTone.warning => (colors.warning, colors.warningBg),
      StatusTone.danger => (colors.danger, colors.dangerBg),
      StatusTone.accent => (colors.accent, colors.accentSoft),
      StatusTone.info => (colors.heroText, colors.heroBg),
      _ => (colors.textSecondary, colors.surfaceSubtle),
    };
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: background,
        borderRadius: colors.cardRadius,
        border: Border.all(color: foreground.withValues(alpha: 0.14)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(icon, size: 16, color: foreground),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  title,
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 11.5,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            value,
            style: TextStyle(
              color: colors.text,
              fontSize: 14,
              fontWeight: FontWeight.w600,
              height: 1.4,
            ),
          ),
          const SizedBox(height: 4),
          Text(
            detail,
            style: TextStyle(
              color: colors.textSecondary,
              fontSize: 11.5,
              height: 1.5,
            ),
          ),
          if (note != null) ...[
            const SizedBox(height: 4),
            Text(
              note!,
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 11,
                height: 1.5,
              ),
            ),
          ],
          if (progress != null) ...[
            const SizedBox(height: 10),
            ProgressBar(value: progress!, height: 4, color: foreground),
          ],
        ],
      ),
    );
  }
}

class _FactsGrid extends StatelessWidget {
  const _FactsGrid({required this.task});

  final LearningTask task;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final facts = <(String, String)>[
      ('任务类型', task.kindLabel),
      ('执行尝试', '${task.attemptCount} 次'),
      (
        '优先级',
        task.priority >= 67
            ? '优先处理'
            : task.priority <= 33
                ? '稍后处理'
                : '普通'
      ),
      ('最近更新', formatDateTime(task.updatedAt)),
      if (task.estimate.hasValue)
        (
          '预计剩余',
          '${formatDurationSeconds(task.estimate.remainingSecondsMin)} – ${formatDurationSeconds(task.estimate.remainingSecondsMax)}（估算）'
        )
      else
        ('预计剩余', '暂无可靠估算'),
    ];
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: colors.surface,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      child: Wrap(
        spacing: 18,
        runSpacing: 12,
        children: [
          for (final fact in facts)
            SizedBox(
              width: 190,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    fact.$1,
                    style: TextStyle(color: colors.muted, fontSize: 10.5),
                  ),
                  const SizedBox(height: 3),
                  Text(
                    fact.$2,
                    style: TextStyle(
                      color: colors.text,
                      fontSize: 12,
                      height: 1.5,
                    ),
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }
}

class _BillingCard extends StatelessWidget {
  const _BillingCard({required this.task});

  final LearningTask task;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final billing = task.billing;
    final text = billing.hasPublishedPrice
        ? Money(billing.amountMinor!, currency: billing.currency ?? 'CNY').text
        : '单价待公布 · 当前不自动扣费';
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: colors.surfaceSubtle,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(Icons.receipt_long_outlined,
              size: 17, color: colors.textSecondary),
          const SizedBox(width: 9),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  '费用记录',
                  style: TextStyle(
                    color: colors.text,
                    fontSize: 12.5,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 3),
                Text(
                  text,
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 11.5,
                    height: 1.5,
                  ),
                ),
                if (billing.priceVersion != null && billing.hasPublishedPrice)
                  Text(
                    '价格版本 ${billing.priceVersion}',
                    style: TextStyle(color: colors.muted, fontSize: 10.5),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _HistorySection extends StatelessWidget {
  const _HistorySection({required this.detail});

  final TaskDetail detail;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final hasContent = detail.attempts.isNotEmpty || detail.commands.isNotEmpty;
    return SectionCard(
      padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
      title: '执行历史',
      subtitle: '任务尝试和你的暂停、取消操作会分别保留；是否完成以平台确认为准。',
      child: !hasContent
          ? Text(
              '还没有执行记录。',
              style: TextStyle(color: colors.textSecondary, fontSize: 12.5),
            )
          : Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                for (final attempt in detail.attempts.take(6))
                  Padding(
                    padding: const EdgeInsets.only(bottom: 8),
                    child: _HistoryRow(
                      title: '第 ${attempt.attemptNo} 次尝试',
                      status: _attemptStateLabel(attempt.state),
                      detail: [
                        attempt.result,
                        attempt.errorCode == null ? null : '执行未完成',
                      ].whereType<String>().join(' · '),
                      time: attempt.startedAt,
                    ),
                  ),
                for (final command in detail.commands.take(6))
                  Padding(
                    padding: const EdgeInsets.only(bottom: 8),
                    child: _HistoryRow(
                      title: command.label,
                      status: command.appliedAt == null ? '已提交，等待生效' : '已生效',
                      detail: command.reason ?? '',
                      time: command.createdAt,
                    ),
                  ),
              ],
            ),
    );
  }

  String _attemptStateLabel(String? state) {
    return switch (state) {
      'running' || 'leased' => '执行中',
      'succeeded' || 'completed' => '已完成',
      'failed' => '未完成',
      'cancelled' => '已停止',
      'released' => '已释放',
      null => '状态待确认',
      _ => '状态待确认',
    };
  }
}

class _HistoryRow extends StatelessWidget {
  const _HistoryRow({
    required this.title,
    required this.status,
    required this.detail,
    this.time,
  });

  final String title;
  final String status;
  final String detail;
  final DateTime? time;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      padding: const EdgeInsets.all(11),
      decoration: BoxDecoration(
        color: colors.surfaceSubtle,
        borderRadius: BorderRadius.circular(colors.radius - 3),
        border: Border.all(color: colors.border),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        title,
                        style: TextStyle(
                          color: colors.text,
                          fontSize: 12.5,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                    Text(
                      status,
                      style: TextStyle(
                        color: colors.textSecondary,
                        fontSize: 11,
                      ),
                    ),
                  ],
                ),
                if (detail.isNotEmpty) ...[
                  const SizedBox(height: 3),
                  Text(
                    detail,
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 11.5,
                      height: 1.5,
                    ),
                  ),
                ],
              ],
            ),
          ),
          if (time != null) ...[
            const SizedBox(width: 10),
            Text(
              formatShortDateTime(time),
              style: TextStyle(color: colors.muted, fontSize: 10.5),
            ),
          ],
        ],
      ),
    );
  }
}

class _Actions extends StatelessWidget {
  const _Actions({
    required this.task,
    required this.busy,
    required this.onCommand,
  });

  final LearningTask task;
  final bool busy;
  final ValueChanged<String> onCommand;

  @override
  Widget build(BuildContext context) {
    final actions = <Widget>[];
    if (task.state.canPause) {
      actions.add(
        AsyncButton(
          label: '暂停任务',
          icon: Icons.pause_outlined,
          busy: busy,
          variant: AsyncButtonVariant.outline,
          onPressed: () => onCommand('pause'),
        ),
      );
    }
    if (task.state.canResume) {
      actions.add(
        AsyncButton(
          label: '继续任务',
          icon: Icons.play_arrow_outlined,
          busy: busy,
          onPressed: () => onCommand('resume'),
        ),
      );
    }
    if (task.state.canRedispatch &&
        (task.state == LearningTaskState.failed ||
            task.state == LearningTaskState.needsAttention ||
            task.state == LearningTaskState.cancelled)) {
      actions.add(
        AsyncButton(
          label: '重新调度',
          icon: Icons.refresh,
          busy: busy,
          variant: AsyncButtonVariant.outline,
          onPressed: () => onCommand('redispatch'),
        ),
      );
    }
    if (task.state.canCancel) {
      actions.add(
        AsyncButton(
          label: '取消任务',
          icon: Icons.block_outlined,
          busy: busy,
          variant: AsyncButtonVariant.danger,
          onPressed: () => onCommand('cancel'),
        ),
      );
    }
    if (actions.isEmpty) {
      return Row(
        children: [
          Icon(
            task.state == LearningTaskState.confirmed
                ? Icons.verified_outlined
                : Icons.info_outline,
            size: 17,
            color: context.colors.textSecondary,
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              task.state == LearningTaskState.confirmed
                  ? '平台结果已保存，此任务不再需要操作。'
                  : '当前状态暂无可执行的操作。',
              style: TextStyle(
                color: context.colors.textSecondary,
                fontSize: 12.5,
              ),
            ),
          ),
        ],
      );
    }
    return Wrap(spacing: 10, runSpacing: 10, children: actions);
  }
}

class _QuestionPanel extends ConsumerStatefulWidget {
  const _QuestionPanel({required this.task});

  final LearningTask task;

  @override
  ConsumerState<_QuestionPanel> createState() => _QuestionPanelState();
}

class _QuestionPanelState extends ConsumerState<_QuestionPanel> {
  QuestionQuery? _query;
  QuestionPrompt? _manualPrompt;
  bool _loadingExisting = false;
  bool _querying = false;
  bool _submitting = false;
  bool _reporting = false;
  bool _confirmedAnswer = false;
  bool _submittedThisSession = false;
  String? _selectedModelId;
  AnswerMode _mode = AnswerMode.cacheFirst;
  String? _error;
  String? _notice;

  @override
  void initState() {
    super.initState();
    if (widget.task.questionQueryId != null) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _loadExisting());
    }
  }

  Future<void> _editManualPrompt() async {
    final prompt = await _askQuestionPrompt(context);
    if (prompt == null || !mounted) return;
    setState(() {
      _manualPrompt = prompt;
      _error = null;
      _notice = '题目已填写，请选择模型后查询';
    });
  }

  Future<void> _loadExisting() async {
    final id = widget.task.questionQueryId;
    if (id == null || _loadingExisting) return;
    setState(() => _loadingExisting = true);
    try {
      final query = await ref.read(questionRepositoryProvider).fetchQuery(id);
      if (!mounted) return;
      setState(() {
        _query = query;
        _loadingExisting = false;
        _confirmedAnswer = false;
      });
    } on ApiException catch (error) {
      if (!mounted) return;
      setState(() {
        _loadingExisting = false;
        _error = error.userMessage;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _loadingExisting = false;
        _error = '答案记录暂时无法加载';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final offeringsAsync = ref.watch(questionOfferingsProvider);
    final capabilities = ref.watch(authCapabilitiesProvider);
    final prompt = _query?.prompt.hasContent == true
        ? _query!.prompt
        : _manualPrompt ?? widget.task.questionPrompt;
    return SectionCard(
      padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
      title: '题库答案',
      subtitle: '查询、用户确认与提交相互独立；提交前需要人工核对。',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (capabilities.questionsExplicitlyDisabled)
            const InlineNotice(
              icon: Icons.lock_outline,
              message: '题库查询当前未开放。',
            )
          else ...[
            if (_query?.contentExpired == true)
              const InlineNotice(
                tone: StatusTone.warning,
                icon: Icons.hourglass_disabled_outlined,
                message: '题目内容已按保留规则清理，历史答案仍可查看。',
              )
            else if (prompt == null)
              InlineNotice(
                icon: Icons.help_outline,
                message: '平台尚未返回题目内容，可手动填写题干后再查询答案。',
                action: TextButton(
                  onPressed: () => _editManualPrompt(),
                  child: const Text('填写题目'),
                ),
              )
            else
              _PromptView(prompt: prompt),
            const SizedBox(height: 12),
            offeringsAsync.when(
              loading: () => const LinearProgressIndicator(minHeight: 2),
              error: (error, _) => InlineNotice(
                tone: StatusTone.warning,
                message:
                    error is ApiException ? error.userMessage : '题库模型目录暂时无法加载',
              ),
              data: (offerings) => _buildModelAndMode(
                context,
                offerings,
                prompt,
              ),
            ),
            if (_loadingExisting) ...[
              const SizedBox(height: 12),
              const LinearProgressIndicator(minHeight: 2),
            ],
            if (_query != null) ...[
              const SizedBox(height: 14),
              _QueryResult(
                query: _query!,
                confirmed: _confirmedAnswer,
                submitting: _submitting,
                reporting: _reporting,
                onConfirmChanged: (value) {
                  setState(() {
                    _confirmedAnswer = value;
                    _error = null;
                  });
                },
                onSubmit:
                    _query!.submitted || _submittedThisSession ? null : _submit,
                onReport: _query!.submitted ? null : _report,
              ),
            ],
            if (_query?.status == 'needs_review') ...[
              const SizedBox(height: 10),
              InlineNotice(
                tone: StatusTone.warning,
                icon: Icons.fact_check_outlined,
                message: _reviewReasonLabel(_query!.reviewReason),
              ),
            ],
            if (_query?.status == 'failed') ...[
              const SizedBox(height: 10),
              const InlineNotice(
                tone: StatusTone.danger,
                icon: Icons.error_outline,
                message: '答案查询未完成，本次不会自动扣费。可稍后重新查询。',
              ),
            ],
            if (_error != null) ...[
              const SizedBox(height: 10),
              ErrorPanel(message: _error!, compact: true),
            ],
            if (_notice != null) ...[
              const SizedBox(height: 10),
              InlineNotice(
                tone: StatusTone.success,
                message: _notice!,
              ),
            ],
          ],
        ],
      ),
    );
  }

  Widget _buildModelAndMode(
    BuildContext context,
    List<QuestionOffering> offerings,
    QuestionPrompt? prompt,
  ) {
    final colors = context.colors;
    final available =
        offerings.where((offering) => offering.healthy).toList(growable: false);
    QuestionOffering? selected;
    for (final offering in available) {
      if (offering.id == _selectedModelId) selected = offering;
    }
    final missingSelected = _selectedModelId != null && selected == null;
    final canQuery =
        prompt != null && selected != null && !_querying && !_loadingExisting;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.only(bottom: 8),
          child: Text(
            '安排策略在任务领取时冻结；此处的模型与模式只用于本次手工查询，不会自动执行或提交。',
            style: TextStyle(color: colors.muted, fontSize: 11, height: 1.5),
          ),
        ),
        DropdownButtonFormField<String>(
          initialValue: selected?.id,
          decoration: const InputDecoration(
            labelText: '查询模型',
            isDense: true,
          ),
          items: [
            for (final offering in available)
              DropdownMenuItem(
                value: offering.id,
                child: Text(
                  '${offering.displayName} · ${offering.capabilityLabel} · ${offering.priceLabel}',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
          ],
          onChanged: _querying
              ? null
              : (value) {
                  setState(() {
                    _selectedModelId = value;
                    _error = null;
                  });
                },
        ),
        if (missingSelected) ...[
          const SizedBox(height: 8),
          const InlineNotice(
            tone: StatusTone.warning,
            message: '安排中的模型已不可用，请重新选择后再查询。',
          ),
        ],
        if (available.isEmpty) ...[
          const SizedBox(height: 8),
          const InlineNotice(
            icon: Icons.info_outline,
            message: '管理员尚未发布可用模型，暂时不能查询答案。',
          ),
        ],
        const SizedBox(height: 12),
        SegmentedButton<AnswerMode>(
          segments: const [
            ButtonSegment(
              value: AnswerMode.cacheFirst,
              label: Text('优先使用已有答案'),
            ),
            ButtonSegment(
              value: AnswerMode.alwaysLlm,
              label: Text('每次都使用模型'),
            ),
          ],
          selected: {_mode},
          showSelectedIcon: false,
          onSelectionChanged:
              _querying ? null : (value) => setState(() => _mode = value.first),
        ),
        const SizedBox(height: 7),
        Text(
          _mode.description,
          style: TextStyle(
            color: colors.textSecondary,
            fontSize: 11.5,
            height: 1.5,
          ),
        ),
        const SizedBox(height: 12),
        Row(
          children: [
            Expanded(
              child: AsyncButton(
                label: _query == null ? '查询答案' : '重新查询答案',
                icon: Icons.search,
                busy: _querying,
                onPressed: canQuery
                    ? () => _queryAnswer(
                          prompt: prompt,
                          offering: selected!,
                        )
                    : null,
              ),
            ),
            if (_query != null) ...[
              const SizedBox(width: 8),
              IconButton(
                tooltip: '刷新答案',
                onPressed: (_query?.id.isEmpty ?? true) || _querying
                    ? null
                    : () => _refreshQuery(_query?.id ?? ''),
                icon: const Icon(Icons.refresh, size: 18),
              ),
            ],
          ],
        ),
      ],
    );
  }

  Future<void> _queryAnswer({
    required QuestionPrompt prompt,
    required QuestionOffering offering,
  }) async {
    setState(() {
      _querying = true;
      _error = null;
      _notice = null;
      _confirmedAnswer = false;
    });
    try {
      final query = await ref.read(questionRepositoryProvider).createQuery(
            request: QuestionQueryRequest(
              modelOfferingId: offering.id,
              mode: _mode,
              prompt: prompt,
            ),
            idempotencyKey: newIdempotencyKey(),
          );
      if (!mounted) return;
      setState(() {
        _query = query;
        _querying = false;
        _notice = query.answered ? '答案已返回，请核对后再提交' : '查询已提交，答案返回后可继续核对';
      });
      if (!query.answered && query.id.isNotEmpty) {
        await _refreshQuery(query.id);
      }
    } on ApiException catch (error) {
      if (!mounted) return;
      setState(() {
        _querying = false;
        _error = error.userMessage;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _querying = false;
        _error = '答案查询未完成，请稍后重试';
      });
    }
  }

  Future<void> _refreshQuery(String id) async {
    setState(() => _querying = true);
    try {
      final query = await ref.read(questionRepositoryProvider).fetchQuery(id);
      if (!mounted) return;
      setState(() {
        _query = query;
        _querying = false;
        _error = null;
        if (query.answered) _notice = '答案已刷新，请核对后再提交';
      });
    } on ApiException catch (error) {
      if (!mounted) return;
      setState(() {
        _querying = false;
        _error = error.userMessage;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _querying = false;
        _error = '答案刷新未完成';
      });
    }
  }

  Future<void> _submit() async {
    final query = _query;
    if (query == null || !_confirmedAnswer || _submitting) return;
    final confirmed = await showConfirmDialog(
      context,
      title: '提交这道题的答案？',
      message: '提交后由学习通处理。请确认题目、答案与来源均已核对。',
      confirmLabel: '确认提交',
    );
    if (!confirmed || !mounted) return;
    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      await ref.read(questionRepositoryProvider).submit(
            query: query,
            idempotencyKey: newIdempotencyKey(),
          );
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _submittedThisSession = true;
        _notice = '答案已提交，等待学习通返回结果';
      });
    } on ApiException catch (error) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = error.userMessage;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _submitting = false;
        _error = '答案提交未完成，请稍后重试';
      });
    }
  }

  Future<void> _report() async {
    final query = _query;
    if (query == null || _reporting) return;
    final reason = await _askReason(
      context,
      title: '报告答案问题',
      message: '说明答案或缓存的问题，平台会标记待复核，不会直接删除账务记录。',
      hint: '例如：答案与题目不符',
    );
    if (reason == null || !mounted) return;
    setState(() {
      _reporting = true;
      _error = null;
    });
    try {
      await ref.read(questionRepositoryProvider).reportCache(
            queryId: query.id,
            reason: reason,
          );
      if (!mounted) return;
      setState(() {
        _reporting = false;
        _notice = '问题已提交复核';
      });
    } on ApiException catch (error) {
      if (!mounted) return;
      setState(() {
        _reporting = false;
        _error = error.userMessage;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _reporting = false;
        _error = '问题报告未完成';
      });
    }
  }

  String _reviewReasonLabel(String? reason) {
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
}

class _PromptView extends StatelessWidget {
  const _PromptView({required this.prompt});

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

class _QueryResult extends StatelessWidget {
  const _QueryResult({
    required this.query,
    required this.confirmed,
    required this.submitting,
    required this.reporting,
    required this.onConfirmChanged,
    required this.onSubmit,
    required this.onReport,
  });

  final QuestionQuery query;
  final bool confirmed;
  final bool submitting;
  final bool reporting;
  final ValueChanged<bool> onConfirmChanged;
  final VoidCallback? onSubmit;
  final VoidCallback? onReport;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
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
              if (query.submitted)
                const StatusPill(
                  label: '已提交',
                  tone: StatusTone.info,
                  compact: true,
                ),
            ],
          ),
          const SizedBox(height: 10),
          Text(
            query.answer ?? '答案尚未返回',
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
                _token(context, '输入 token', query.usage.inputTokens),
                _token(context, '输出 token', query.usage.outputTokens),
                _token(context, '合计', query.usage.totalTokens),
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
          const SizedBox(height: 10),
          Material(
            color: Colors.transparent,
            child: CheckboxListTile(
              value: confirmed,
              onChanged: query.submitted || submitting
                  ? null
                  : (value) => onConfirmChanged(value ?? false),
              controlAffinity: ListTileControlAffinity.leading,
              contentPadding: EdgeInsets.zero,
              visualDensity: VisualDensity.compact,
              title: Text(
                '我已核对题目、答案与来源，确认提交这道题',
                style: TextStyle(
                  color: colors.textSecondary,
                  fontSize: 12,
                  height: 1.5,
                ),
              ),
            ),
          ),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              AsyncButton(
                label: '提交到学习通',
                icon: Icons.send_outlined,
                busy: submitting,
                onPressed: confirmed && query.answered ? onSubmit : null,
              ),
              AsyncButton(
                label: '报告答案问题',
                icon: Icons.flag_outlined,
                busy: reporting,
                variant: AsyncButtonVariant.outline,
                onPressed: onReport,
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _token(BuildContext context, String label, int value) {
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

Future<QuestionPrompt?> _askQuestionPrompt(BuildContext context) async {
  final titleController = TextEditingController();
  final optionsController = TextEditingController();
  var type = 'single';
  final result = await showDialog<QuestionPrompt>(
    context: context,
    builder: (dialogContext) {
      return StatefulBuilder(
        builder: (context, setDialogState) {
          return AlertDialog(
            title: const Text('填写题目'),
            content: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  TextField(
                    controller: titleController,
                    autofocus: true,
                    maxLines: 4,
                    maxLength: 20000,
                    decoration: const InputDecoration(labelText: '题干'),
                  ),
                  const SizedBox(height: 10),
                  TextField(
                    controller: optionsController,
                    maxLines: 5,
                    maxLength: 50000,
                    decoration: const InputDecoration(
                      labelText: '选项（可选）',
                      hintText: 'A. ...\nB. ...',
                    ),
                  ),
                  const SizedBox(height: 10),
                  DropdownButtonFormField<String>(
                    initialValue: type,
                    decoration: const InputDecoration(labelText: '题型'),
                    items: const [
                      DropdownMenuItem(value: 'single', child: Text('单选题')),
                      DropdownMenuItem(value: 'multiple', child: Text('多选题')),
                      DropdownMenuItem(value: 'judgement', child: Text('判断题')),
                      DropdownMenuItem(value: 'completion', child: Text('填空题')),
                    ],
                    onChanged: (value) {
                      if (value != null) {
                        setDialogState(() => type = value);
                      }
                    },
                  ),
                ],
              ),
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.of(dialogContext).pop(),
                child: const Text('取消'),
              ),
              FilledButton(
                onPressed: () {
                  final title = titleController.text.trim();
                  if (title.isEmpty) return;
                  Navigator.of(dialogContext).pop(
                    QuestionPrompt(
                      title: title,
                      options: optionsController.text.trim(),
                      type: type,
                    ),
                  );
                },
                child: const Text('使用此题目'),
              ),
            ],
          );
        },
      );
    },
  );
  titleController.dispose();
  optionsController.dispose();
  return result;
}

Future<String?> _askReason(
  BuildContext context, {
  required String title,
  required String message,
  required String hint,
}) async {
  final controller = TextEditingController();
  final result = await showDialog<String>(
    context: context,
    builder: (dialogContext) {
      return AlertDialog(
        title: Text(title),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(message),
            const SizedBox(height: 12),
            TextField(
              controller: controller,
              autofocus: true,
              maxLength: 200,
              maxLines: 3,
              decoration: InputDecoration(hintText: hint),
            ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () {
              final value = controller.text.trim();
              if (value.length < 2) return;
              Navigator.of(dialogContext).pop(value);
            },
            child: const Text('提交'),
          ),
        ],
      );
    },
  );
  controller.dispose();
  return result;
}
