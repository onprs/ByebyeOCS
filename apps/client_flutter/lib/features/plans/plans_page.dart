import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/api/api_error.dart';
import '../../core/format/formatters.dart';
import '../../core/models/course.dart';
import '../../core/models/plan.dart';
import '../../core/models/task.dart';
import '../../app/scroll_controllers.dart';
import '../../design/components.dart';
import '../../design/theme.dart';
import '../courses/courses_controller.dart';
import 'plan_editor.dart';
import 'plans_controller.dart';
import 'task_detail_sheet.dart';
import 'task_visuals.dart';

enum _PlanTaskFilter { current, attention, history }

class PlansPage extends ConsumerStatefulWidget {
  const PlansPage({
    super.key,
    this.initialCourseId,
    this.initialPlanId,
  });

  final String? initialCourseId;
  final String? initialPlanId;

  @override
  ConsumerState<PlansPage> createState() => _PlansPageState();
}

class _PlansPageState extends ConsumerState<PlansPage> {
  _PlanTaskFilter _filter = _PlanTaskFilter.current;
  bool _handledInitial = false;
  bool _planConfigExpanded = false;

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(plansControllerProvider);
    final coursesAsync = ref.watch(coursesControllerProvider);
    final courses =
        coursesAsync.valueOrNull?.courses ?? const <CourseSummary>[];
    final scroll = ref.watch(tabScrollControllerProvider('plans'));
    return SingleChildScrollView(
      key: const PageStorageKey('plans-scroll'),
      controller: scroll,
      primary: false,
      padding: EdgeInsets.only(bottom: context.isNarrow ? 12 : 28),
      child: ResponsiveContent(
        child: async.when(
          loading: () => const LoadingList(rows: 3, height: 150),
          error: (error, _) => ErrorPanel(
            message: error is ApiException ? error.userMessage : '任务安排暂时无法加载',
            requestId: error is ApiException ? error.requestId : null,
            onRetry: () => ref.invalidate(plansControllerProvider),
          ),
          data: (data) {
            _handleInitial(data);
            return _buildData(context, data, courses);
          },
        ),
      ),
    );
  }

  void _handleInitial(PlansState data) {
    if (_handledInitial || !mounted) return;
    if (widget.initialCourseId != null) {
      _handledInitial = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _openEditor(createWithCourseId: widget.initialCourseId);
      });
      return;
    }
    if (widget.initialPlanId != null && widget.initialPlanId!.isNotEmpty) {
      _handledInitial = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) {
          ref
              .read(plansControllerProvider.notifier)
              .ensureSelection(widget.initialPlanId);
        }
      });
    }
  }

  Widget _buildData(
    BuildContext context,
    PlansState data,
    List<CourseSummary> courses,
  ) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        PageHeader(
          title: '安排与实时进度',
          actions: [
            AsyncButton(
              label: '新建安排',
              icon: Icons.add,
              onPressed: () => _openEditor(),
            ),
          ],
        ),
        if (data.notice != null || data.error != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 16),
            child: InlineNotice(
              tone:
                  data.error == null ? StatusTone.success : StatusTone.warning,
              icon: data.error == null
                  ? Icons.check_circle_outline
                  : Icons.info_outline,
              message: data.error ?? data.notice!,
              action: IconButton(
                tooltip: '关闭',
                onPressed: () =>
                    ref.read(plansControllerProvider.notifier).clearBanner(),
                icon: const Icon(Icons.close, size: 17),
              ),
            ),
          ),
        if (data.plans.isEmpty)
          SectionCard(
            child: EmptyState(
              icon: Icons.calendar_month_outlined,
              title: '还没有任务安排',
              message: '选择课程与章节范围，建立第一条学习安排。',
            ),
          )
        else
          LayoutBuilder(
            builder: (context, constraints) {
              final wide = constraints.maxWidth >= 920;
              if (!wide) {
                return Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    _PlanSelector(
                      plans: data.plans,
                      selectedId: data.selectedPlan?.id,
                      onChanged: (id) => ref
                          .read(plansControllerProvider.notifier)
                          .selectPlan(id),
                    ),
                    const SizedBox(height: 14),
                    _buildWorkspace(context, data, courses, wide: false),
                  ],
                );
              }
              return Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  SizedBox(
                    width: 292,
                    child: _PlanList(
                      plans: data.plans,
                      selectedId: data.selectedPlan?.id,
                      courses: courses,
                      onSelect: (id) => ref
                          .read(plansControllerProvider.notifier)
                          .selectPlan(id),
                      onCreate: () => _openEditor(),
                    ),
                  ),
                  const SizedBox(width: 18),
                  Expanded(
                    child: _buildWorkspace(context, data, courses, wide: true),
                  ),
                ],
              );
            },
          ),
      ],
    );
  }

  Widget _buildWorkspace(
    BuildContext context,
    PlansState data,
    List<CourseSummary> courses, {
    required bool wide,
  }) {
    final plan = data.selectedPlan;
    if (plan == null) return const SizedBox.shrink();
    final activeCount = data.activeTasks.length;
    final attentionCount = data.attentionTasks.length;
    final hasRunning = data.activeTasks.any((task) => task.state.isActive);
    final courseNames = plan.courseIds
        .map((id) => courses.where((course) => course.id == id))
        .expand((matches) => matches)
        .map((course) => course.title)
        .toList(growable: false);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Container(
          padding: const EdgeInsets.all(18),
          decoration: BoxDecoration(
            color: context.colors.surface,
            borderRadius: context.colors.cardRadius,
            border: Border.all(color: context.colors.border),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Wrap(
                          spacing: 8,
                          runSpacing: 6,
                          crossAxisAlignment: WrapCrossAlignment.center,
                          children: [
                            Text(
                              plan.name,
                              style: TextStyle(
                                color: context.colors.text,
                                fontSize: AppTypography.fontSection,
                                fontWeight: FontWeight.w600,
                                letterSpacing: -0.3,
                              ),
                            ),
                            if (plan.questionPolicy.enabled)
                              const StatusPill(
                                label: '题库策略已开启',
                                tone: StatusTone.info,
                                compact: true,
                              ),
                            StatusPill(
                              label: plan.isActive
                                  ? '已开启'
                                  : plan.isPaused
                                      ? '已暂停'
                                      : plan.isArchived
                                          ? '已归档'
                                          : '状态待确认',
                              tone: plan.isActive
                                  ? StatusTone.success
                                  : plan.isPaused
                                      ? StatusTone.warning
                                      : StatusTone.neutral,
                              compact: true,
                            ),
                          ],
                        ),
                        const SizedBox(height: 7),
                        Text(
                          '${courseNames.isEmpty ? '${plan.courseIds.length} 门课程' : courseNames.join('、')} · ${plan.scopeLabel} · ${plan.priorityLabel}',
                          style: TextStyle(
                            color: context.colors.textSecondary,
                            fontSize: 13,
                            height: 1.5,
                          ),
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(width: 10),
                  IconButton(
                    tooltip: '编辑安排',
                    onPressed: plan.isArchived
                        ? null
                        : () =>
                            _openEditor(plan: plan, hasActive: activeCount > 0),
                    icon: const Icon(Icons.edit_outlined, size: 18),
                  ),
                ],
              ),
              if (wide || _planConfigExpanded) ...[
                const SizedBox(height: 14),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    _MetaChip(
                      label: '任务类型',
                      value: plan.taskTypes.map(_taskTypeLabel).join('、'),
                    ),
                    _MetaChip(
                      label: '优先级',
                      value: plan.priorityLabel,
                    ),
                    _MetaChip(
                      label: '当前任务',
                      value: '${activeCount + attentionCount} 项',
                    ),
                    _MetaChip(
                      label: '题库策略',
                      value: plan.questionPolicy.enabled
                          ? plan.questionPolicy.answerMode.shortLabel
                          : '关闭',
                    ),
                  ],
                ),
                if (plan.questionPolicy.enabled) ...[
                  const SizedBox(height: 12),
                  const InlineNotice(
                    icon: Icons.quiz_outlined,
                    message: '题库策略随安排版本冻结；开启后自动查询并提交测验答案。',
                  ),
                ],
                if (hasRunning) ...[
                  const SizedBox(height: 12),
                  const InlineNotice(
                    icon: Icons.play_circle_outline,
                    message: '正在执行的任务保持本轮设置；编辑安排只影响后续任务。',
                  ),
                ],
                const SizedBox(height: 14),
                Wrap(
                  spacing: 10,
                  runSpacing: 10,
                  children: [
                    AsyncButton(
                      label: plan.isActive ? '暂停后续任务' : '恢复安排',
                      icon: plan.isActive
                          ? Icons.pause_outlined
                          : Icons.play_arrow_outlined,
                      busy: data.controlPendingPlanId == plan.id,
                      variant: AsyncButtonVariant.outline,
                      onPressed:
                          plan.isArchived || data.controlPendingPlanId != null
                              ? null
                              : () => _controlPlan(plan),
                    ),
                    TextButton.icon(
                      onPressed: () => ref
                          .read(plansControllerProvider.notifier)
                          .refresh(silent: true),
                      icon: const Icon(Icons.refresh, size: 16),
                      label: const Text('刷新状态'),
                    ),
                  ],
                ),
              ] else
                Align(
                  alignment: Alignment.centerLeft,
                  child: TextButton.icon(
                    onPressed: () => setState(() => _planConfigExpanded = true),
                    icon: const Icon(Icons.tune, size: 16),
                    label: const Text('安排设置'),
                  ),
                ),
              if (!wide && _planConfigExpanded)
                Align(
                  alignment: Alignment.centerLeft,
                  child: TextButton.icon(
                    onPressed: () =>
                        setState(() => _planConfigExpanded = false),
                    icon: const Icon(Icons.expand_less, size: 16),
                    label: const Text('收起设置'),
                  ),
                ),
            ],
          ),
        ),
        const SizedBox(height: 16),
        SectionCard(
          padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
          title: '任务进度',
          action: Wrap(
            spacing: 6,
            children: [
              _TaskFilterChip(
                label: '当前 $activeCount',
                selected: _filter == _PlanTaskFilter.current,
                onTap: () => setState(() => _filter = _PlanTaskFilter.current),
              ),
              _TaskFilterChip(
                label: '需要处理 $attentionCount',
                selected: _filter == _PlanTaskFilter.attention,
                danger: attentionCount > 0,
                onTap: () =>
                    setState(() => _filter = _PlanTaskFilter.attention),
              ),
              _TaskFilterChip(
                label: '历史',
                selected: _filter == _PlanTaskFilter.history,
                onTap: () => setState(() => _filter = _PlanTaskFilter.history),
              ),
            ],
          ),
          child: _buildTaskList(context, data),
        ),
        const SizedBox(height: 16),
        const InlineNotice(
          icon: Icons.schedule_outlined,
          message: '预计剩余时间为估算值。执行结束后仍需查询学习通，平台确认前不会计为完成。',
        ),
      ],
    );
  }

  Widget _buildTaskList(BuildContext context, PlansState data) {
    final tasks = switch (_filter) {
      _PlanTaskFilter.current => data.activeTasks,
      _PlanTaskFilter.attention => data.attentionTasks,
      _PlanTaskFilter.history => data.historyTasks,
    };
    if (data.refreshingTasks && tasks.isEmpty) {
      return const LoadingList(rows: 2, height: 92);
    }
    if (tasks.isEmpty) {
      final (title, message) = switch (_filter) {
        _PlanTaskFilter.current => ('当前没有进行中的任务', '安排保存后，任务会在下一轮调度中生成。'),
        _PlanTaskFilter.attention => ('没有需要人工处理的任务', '身份过期、平台限制或资源不可用时会在这里提示。'),
        _PlanTaskFilter.history => ('暂无历史任务', '平台确认或取消后的任务会保留在这里。'),
      };
      return _withTaskPager(
        data,
        EmptyState(
          icon: _filter == _PlanTaskFilter.attention
              ? Icons.check_circle_outline
              : Icons.inbox_outlined,
          title: title,
          message: message,
          compact: true,
        ),
      );
    }
    if (_filter == _PlanTaskFilter.current) {
      final groups = <_TaskGroup>[
        _TaskGroup(
          title: '执行中',
          tasks: tasks
              .where((task) =>
                  task.state == LearningTaskState.running ||
                  task.state == LearningTaskState.preparing)
              .toList(growable: false),
        ),
        _TaskGroup(
          title: '等待平台确认',
          tasks: tasks
              .where(
                  (task) => task.state == LearningTaskState.waitingVerification)
              .toList(growable: false),
        ),
        _TaskGroup(
          title: '接下来',
          tasks: tasks
              .where((task) => task.state == LearningTaskState.pending)
              .toList(growable: false),
        ),
        _TaskGroup(
          title: '已暂停',
          tasks: tasks
              .where((task) => task.state == LearningTaskState.paused)
              .toList(growable: false),
        ),
      ];
      return _withTaskPager(
        data,
        Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (final group in groups)
              if (group.tasks.isNotEmpty) ...[
                Padding(
                  padding: const EdgeInsets.only(top: 6, bottom: 7),
                  child: Text(
                    '${group.title} · ${group.tasks.length}',
                    style: TextStyle(
                      color: context.colors.textSecondary,
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                      letterSpacing: 0.4,
                    ),
                  ),
                ),
                for (final task in group.tasks)
                  _TaskRow(task: task, onTap: () => _openTask(task)),
              ],
          ],
        ),
      );
    }
    return _withTaskPager(
      data,
      Column(
        children: [
          for (final task in tasks)
            _TaskRow(task: task, onTap: () => _openTask(task)),
        ],
      ),
    );
  }

  Widget _withTaskPager(PlansState data, Widget content) {
    if (data.refreshingTasks ||
        (!data.tasksHasMore && !data.loadingMoreTasks)) {
      return content;
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        content,
        Padding(
          padding: const EdgeInsets.only(top: 6),
          child: Center(
            child: TextButton(
              onPressed: data.loadingMoreTasks
                  ? null
                  : () => ref
                      .read(plansControllerProvider.notifier)
                      .loadMoreTasks(),
              child: Text(data.loadingMoreTasks ? '加载中…' : '加载更多'),
            ),
          ),
        ),
      ],
    );
  }

  Future<void> _openTask(LearningTask task) async {
    await showTaskDetailSheet(context, task.id);
    if (!mounted) return;
    await ref.read(plansControllerProvider.notifier).refresh(silent: true);
  }

  Future<void> _openEditor({
    LearningPlan? plan,
    bool hasActive = false,
    String? createWithCourseId,
  }) async {
    final result = await showPlanEditor(
      context,
      plan: plan,
      initialCourseId: createWithCourseId,
      hasActiveTasks: hasActive,
    );
    if (!mounted || result == null) return;
    final controller = ref.read(plansControllerProvider.notifier);
    await controller.afterPlanSaved(result.id);
    await controller.ensureSelection(result.id);
  }

  Future<void> _controlPlan(LearningPlan plan) async {
    final pausing = plan.isActive;
    if (pausing) {
      final confirmed = await showConfirmDialog(
        context,
        title: '暂停这条安排？',
        message: '暂停只停止后续任务，不会中断当前正在执行的任务。',
        confirmLabel: '暂停后续任务',
      );
      if (!confirmed || !mounted) return;
    }
    await ref.read(plansControllerProvider.notifier).controlPlan(
          planId: plan.id,
          action: pausing ? 'pause' : 'resume',
        );
  }

  String _taskTypeLabel(String type) {
    return switch (type) {
      'video' => '视频',
      'document' => '文档',
      'audio' => '音频',
      'quiz' => '测验',
      'other' => '其他',
      _ => type,
    };
  }
}

class _PlanSelector extends StatelessWidget {
  const _PlanSelector({
    required this.plans,
    required this.selectedId,
    required this.onChanged,
  });

  final List<LearningPlan> plans;
  final String? selectedId;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 4),
      decoration: BoxDecoration(
        color: colors.surface,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      child: DropdownButtonHideUnderline(
        child: DropdownButton<String>(
          isExpanded: true,
          value: selectedId,
          icon: const Icon(Icons.expand_more, size: 18),
          items: [
            for (final plan in plans)
              DropdownMenuItem(
                value: plan.id,
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        plan.name,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontSize: 14),
                      ),
                    ),
                    const SizedBox(width: 8),
                    StatusPill(
                      label: plan.isActive ? '已开启' : '已暂停',
                      tone: plan.isActive
                          ? StatusTone.success
                          : StatusTone.warning,
                      compact: true,
                    ),
                  ],
                ),
              ),
          ],
          onChanged: (value) {
            if (value != null) onChanged(value);
          },
        ),
      ),
    );
  }
}

class _PlanList extends StatelessWidget {
  const _PlanList({
    required this.plans,
    required this.selectedId,
    required this.courses,
    required this.onSelect,
    required this.onCreate,
  });

  final List<LearningPlan> plans;
  final String? selectedId;
  final List<CourseSummary> courses;
  final ValueChanged<String> onSelect;
  final VoidCallback onCreate;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return SectionCard(
      padding: const EdgeInsets.fromLTRB(10, 12, 10, 12),
      title: '我的安排',
      action: IconButton(
        tooltip: '新建安排',
        onPressed: onCreate,
        icon: const Icon(Icons.add, size: 19),
      ),
      child: Column(
        children: [
          for (final plan in plans)
            Padding(
              padding: const EdgeInsets.only(bottom: 6),
              child: Semantics(
                button: true,
                selected: plan.id == selectedId,
                label: plan.name,
                child: InkWell(
                  onTap: () => onSelect(plan.id),
                  borderRadius: BorderRadius.circular(colors.radius - 2),
                  child: Container(
                    padding: const EdgeInsets.all(12),
                    decoration: BoxDecoration(
                      color: plan.id == selectedId
                          ? colors.accentSoft
                          : colors.surfaceSubtle,
                      borderRadius: BorderRadius.circular(colors.radius - 2),
                      border: Border.all(
                        color: plan.id == selectedId
                            ? colors.accent.withValues(alpha: 0.3)
                            : colors.border,
                      ),
                    ),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Row(
                          children: [
                            Expanded(
                              child: Text(
                                plan.name,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: TextStyle(
                                  color: colors.text,
                                  fontSize: 14,
                                  fontWeight: FontWeight.w600,
                                ),
                              ),
                            ),
                            const SizedBox(width: 6),
                            StatusPill(
                              label: plan.isActive ? '已开启' : '已暂停',
                              tone: plan.isActive
                                  ? StatusTone.success
                                  : StatusTone.warning,
                              compact: true,
                            ),
                          ],
                        ),
                        const SizedBox(height: 5),
                        Text(
                          '${plan.courseIds.length} 门课程 · ${plan.taskTypes.map((type) => switch (type) {
                                'video' => '视频',
                                'document' => '文档',
                                'audio' => '音频',
                                'quiz' => '测验',
                                'other' => '其他',
                                _ => type,
                              }).join('、')}',
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: colors.textSecondary,
                            fontSize: 12,
                          ),
                        ),
                        const SizedBox(height: 3),
                        Text(
                          '${plan.scopeLabel} · ${plan.priorityLabel}${plan.questionPolicy.enabled ? ' · 题库策略已开启' : ''}',
                          style: TextStyle(
                            color: colors.muted,
                            fontSize: 12,
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class _TaskFilterChip extends StatelessWidget {
  const _TaskFilterChip({
    required this.label,
    required this.selected,
    required this.onTap,
    this.danger = false,
  });

  final String label;
  final bool selected;
  final VoidCallback onTap;
  final bool danger;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final accent = danger ? colors.danger : colors.accent;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(999),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
        decoration: BoxDecoration(
          color: selected ? accent.withValues(alpha: 0.1) : Colors.transparent,
          borderRadius: BorderRadius.circular(999),
          border: Border.all(
            color: selected ? accent.withValues(alpha: 0.25) : colors.border,
          ),
        ),
        child: Text(
          label,
          style: TextStyle(
            color: selected ? accent : colors.textSecondary,
            fontSize: 12,
            fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
          ),
        ),
      ),
    );
  }
}

class _MetaChip extends StatelessWidget {
  const _MetaChip({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
      decoration: BoxDecoration(
        color: colors.surfaceSubtle,
        borderRadius: BorderRadius.circular(7),
        border: Border.all(color: colors.border),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            '$label ',
            style: TextStyle(color: colors.muted, fontSize: 12),
          ),
          Text(
            value,
            style: TextStyle(
              color: colors.textSecondary,
              fontSize: 12,
              fontWeight: FontWeight.w500,
            ),
          ),
        ],
      ),
    );
  }
}

class _TaskRow extends StatelessWidget {
  const _TaskRow({required this.task, required this.onTap});

  final LearningTask task;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final observation = task.observation?.effectiveFraction;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Semantics(
        button: true,
        label: '${task.title}，${task.state.label}',
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(colors.radius - 2),
          child: Container(
            padding: const EdgeInsets.all(13),
            decoration: BoxDecoration(
              color: colors.surfaceSubtle,
              borderRadius: BorderRadius.circular(colors.radius - 2),
              border: Border.all(color: colors.border),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Container(
                      width: 32,
                      height: 32,
                      decoration: BoxDecoration(
                        color: switch (taskStateTone(task.state)) {
                          StatusTone.success => colors.successBg,
                          StatusTone.danger => colors.dangerBg,
                          StatusTone.warning => colors.warningBg,
                          StatusTone.accent => colors.accentSoft,
                          _ => colors.surface,
                        },
                        borderRadius: BorderRadius.circular(8),
                      ),
                      child: Icon(
                        taskStateIcon(task.state),
                        size: 17,
                        color: switch (taskStateTone(task.state)) {
                          StatusTone.success => colors.success,
                          StatusTone.danger => colors.danger,
                          StatusTone.warning => colors.warning,
                          StatusTone.accent => colors.accent,
                          _ => colors.textSecondary,
                        },
                      ),
                    ),
                    const SizedBox(width: 10),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            task.title,
                            maxLines: 2,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              color: colors.text,
                              fontSize: 14,
                              fontWeight: FontWeight.w600,
                              height: 1.4,
                            ),
                          ),
                          const SizedBox(height: 3),
                          Text(
                            [
                              task.courseTitle,
                              task.chapterTitle,
                              task.kindLabel,
                            ].whereType<String>().join(' · '),
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              color: colors.textSecondary,
                              fontSize: 12,
                            ),
                          ),
                        ],
                      ),
                    ),
                    const SizedBox(width: 8),
                    StatusPill(
                      label: task.state.label,
                      tone: taskStateTone(task.state),
                      compact: true,
                    ),
                  ],
                ),
                const SizedBox(height: 10),
                Wrap(
                  spacing: 10,
                  runSpacing: 6,
                  crossAxisAlignment: WrapCrossAlignment.center,
                  children: [
                    _TaskFact(
                      icon: Icons.monitor_heart_outlined,
                      text: observation == null
                          ? '尚无执行观察'
                          : '执行观察 ${formatPercent(observation)}',
                    ),
                    _TaskFact(
                      icon: task.platform.isConfirmed
                          ? Icons.verified_outlined
                          : Icons.fact_check_outlined,
                      text: task.platform.label,
                      tone: task.platform.isConfirmed
                          ? StatusTone.success
                          : StatusTone.neutral,
                    ),
                    _TaskFact(
                      icon: Icons.videocam_outlined,
                      text: task.media.available ? '有媒体记录' : '无播放记录',
                    ),
                    if (task.estimate.hasValue)
                      _TaskFact(
                        icon: Icons.timer_outlined,
                        text:
                            '预计 ${formatDurationSeconds(task.estimate.remainingSecondsMax)}（估算）',
                      ),
                  ],
                ),
                if (task.state == LearningTaskState.needsAttention ||
                    task.state == LearningTaskState.failed) ...[
                  const SizedBox(height: 8),
                  Text(
                    taskFailureLabel(task),
                    style: TextStyle(
                      color: colors.danger,
                      fontSize: 12,
                    ),
                  ),
                ],
                if (observation != null) ...[
                  const SizedBox(height: 8),
                  ProgressBar(
                    value: observation,
                    height: 4,
                    color: task.platform.isConfirmed
                        ? colors.success
                        : colors.accent,
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _TaskFact extends StatelessWidget {
  const _TaskFact({
    required this.icon,
    required this.text,
    this.tone = StatusTone.neutral,
  });

  final IconData icon;
  final String text;
  final StatusTone tone;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final color = switch (tone) {
      StatusTone.success => colors.success,
      StatusTone.warning => colors.warning,
      StatusTone.danger => colors.danger,
      StatusTone.accent => colors.accent,
      _ => colors.textSecondary,
    };
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 13, color: color),
        const SizedBox(width: 4),
        Text(
          text,
          style: TextStyle(color: color, fontSize: 12),
        ),
      ],
    );
  }
}

class _TaskGroup {
  const _TaskGroup({required this.title, required this.tasks});

  final String title;
  final List<LearningTask> tasks;
}
