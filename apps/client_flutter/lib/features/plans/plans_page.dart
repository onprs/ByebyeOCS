import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/api/api_error.dart';
import '../../core/format/formatters.dart';
import '../../core/models/course.dart';
import '../../core/models/plan.dart';
import '../../core/models/task.dart';
import '../../app/providers.dart';
import '../../app/scroll_controllers.dart';
import '../../design/components.dart';
import '../../design/page_layout.dart';
import '../../design/theme.dart';
import '../courses/courses_controller.dart';
import 'plan_editor.dart';
import 'plans_controller.dart';
import 'task_detail_sheet.dart';
import 'task_visuals.dart';

enum _PlanTaskFilter { current, attention, closed, history }

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
  bool _handledInitial = false;
  bool _planConfigExpanded = false;

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(plansControllerProvider);
    final coursesAsync = ref.watch(coursesControllerProvider);
    final courses =
        coursesAsync.valueOrNull?.courses ?? const <CourseSummary>[];
    final scroll = ref.watch(tabScrollControllerProvider('plans'));
    return PageScrollView(
      key: const PageStorageKey('plans-scroll'),
      controller: scroll,
      primary: false,
      child: ResponsiveContent(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            async.when(
              loading: () => const LoadingList(rows: 3, height: 150),
              error: (error, _) => ErrorPanel(
                message:
                    error is ApiException ? error.userMessage : '任务安排暂时无法加载',
                requestId: error is ApiException ? error.requestId : null,
                onRetry: () => ref.invalidate(plansControllerProvider),
              ),
              data: (data) {
                _handleInitial(data);
                return _buildData(context, data, courses);
              },
            ),
          ],
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
              message: '选择课程与任务类型，建立第一条学习安排。',
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
    final progress = data.taskProgress;
    final activeCount = progress.active;
    final attentionCount = progress.attention;
    final historyCount = progress.history;
    final closedCount = progress.closed;
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
                                label: '自动答题已开启',
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
              const SizedBox(height: 14),
              _PlanProgressBar(progress: progress),
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
                      label: '自动答题',
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
                    message: '自动答题随安排版本冻结；开启后自动查询并提交测验答案。',
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
                    TextButton.icon(
                      style: TextButton.styleFrom(
                        foregroundColor: context.colors.danger,
                      ),
                      onPressed:
                          plan.isArchived || data.controlPendingPlanId != null
                              ? null
                              : () => _deletePlan(plan),
                      icon: const Icon(Icons.delete_outline, size: 16),
                      label: const Text('删除安排'),
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
                selected: data.taskCategory == 'current',
                onTap: () => ref
                    .read(plansControllerProvider.notifier)
                    .selectTaskCategory('current'),
              ),
              _TaskFilterChip(
                label: '需要处理 $attentionCount',
                selected: data.taskCategory == 'attention',
                danger: attentionCount > 0,
                onTap: () => ref
                    .read(plansControllerProvider.notifier)
                    .selectTaskCategory('attention'),
              ),
              _TaskFilterChip(
                label: '已关闭 $closedCount',
                selected: data.taskCategory == 'closed',
                onTap: () => ref
                    .read(plansControllerProvider.notifier)
                    .selectTaskCategory('closed'),
              ),
              _TaskFilterChip(
                label: '历史 $historyCount',
                selected: data.taskCategory == 'history',
                onTap: () => ref
                    .read(plansControllerProvider.notifier)
                    .selectTaskCategory('history'),
              ),
            ],
          ),
          child: _buildTaskList(context, data),
        ),
      ],
    );
  }

  Widget _buildTaskList(BuildContext context, PlansState data) {
    final filter = _PlanTaskFilter.values.byName(data.taskCategory);
    if (data.tasksRefreshing && data.tasks.isEmpty) {
      return const LoadingList(rows: 2, height: 92);
    }
    if (data.tasks.isEmpty) {
      final (title, message) = switch (filter) {
        _PlanTaskFilter.current => ('当前没有进行中的任务', '安排保存后，任务会在下一轮调度中生成。'),
        _PlanTaskFilter.attention => ('没有需要处理的任务', '暂时无法自动恢复的任务会在这里提示。'),
        _PlanTaskFilter.closed => ('没有已关闭的任务', '平台关闭的任务会显示在这里。'),
        _PlanTaskFilter.history => ('暂无历史任务', '平台确认或取消后的任务会保留在这里。'),
      };
      return EmptyState(
          icon: filter == _PlanTaskFilter.attention
              ? Icons.check_circle_outline
              : Icons.inbox_outlined,
          title: title,
          message: message,
          compact: true);
    }
    final list = _TaskListPage(
        tasks: data.tasks,
        onTap: _openTask,
        onRedispatch: _redispatchTask,
        grouped: filter == _PlanTaskFilter.current);
    final totalPages = (data.taskTotal / planTasksPageSize).ceil();
    if (totalPages <= 1) return list;
    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      list,
      Padding(
          padding: const EdgeInsets.only(top: 2),
          child: Row(children: [
            Expanded(
                child: Text('共 ${data.taskTotal} 条',
                    style:
                        TextStyle(color: context.colors.muted, fontSize: 12))),
            _TaskPagerControls(
                page: data.taskPage + 1,
                totalPages: totalPages,
                busy: data.tasksRefreshing,
                hasPrevious: data.hasPreviousTasks,
                hasNext: data.hasMoreTasks,
                onPrevious: data.hasPreviousTasks
                    ? () => ref
                        .read(plansControllerProvider.notifier)
                        .previousTasksPage()
                    : null,
                onNext: () =>
                    ref.read(plansControllerProvider.notifier).loadMoreTasks()),
          ])),
    ]);
  }

  Future<void> _redispatchTask(LearningTask task) async {
    final closed = taskIsClosed(task);
    final confirmed = await showConfirmDialog(
      context,
      title: closed ? '重新检查测验可用性？' : '重新调度这个任务？',
      message: closed
          ? '会重新读取平台页面；课程或测验仍然关闭时保持当前状态，不会提交或扣费。'
          : '任务会重新进入队列，不会重复创建任务或直接产生新扣款。',
      confirmLabel: closed ? '重新检查' : '重新调度',
    );
    if (!confirmed || !mounted) return;
    try {
      await ref.read(learningRepositoryProvider).controlTask(
            id: task.id,
            action: 'redispatch',
          );
      if (!mounted) return;
      await ref.read(plansControllerProvider.notifier).refresh(silent: true);
    } on ApiException catch (error) {
      if (!mounted) return;
      showAppMessage(context, error.userMessage);
    } catch (_) {
      if (!mounted) return;
      showAppMessage(context, '重新调度未完成，请稍后重试');
    }
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

  Future<void> _deletePlan(LearningPlan plan) async {
    final confirmed = await showConfirmDialog(
      context,
      title: '删除这条安排？',
      message: '安排会从列表移除，后续不再派发新任务。已产生的任务、进度和记录都会保留。',
      confirmLabel: '删除安排',
      danger: true,
    );
    if (!confirmed || !mounted) return;
    await ref.read(plansControllerProvider.notifier).deletePlan(
          planId: plan.id,
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
              child: SizedBox(
                height: 100,
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
                            '${plan.scopeLabel} · ${plan.priorityLabel}${plan.questionPolicy.enabled ? ' · 自动答题已开启' : ''}',
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
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
  const _TaskRow({required this.task, required this.onTap, this.onRedispatch});

  final LearningTask task;
  final VoidCallback onTap;
  final VoidCallback? onRedispatch;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final observation = task.observation?.effectiveFraction;
    final closed = taskIsClosed(task);
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Semantics(
        button: true,
        label:
            '${task.title}，${closed ? taskClosedLabel(task) : task.state.label}',
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
                        closed
                            ? Icons.event_busy_outlined
                            : taskStateIcon(task.state),
                        size: 17,
                        color: closed
                            ? colors.muted
                            : switch (taskStateTone(task.state)) {
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
                    if (onRedispatch != null &&
                        (closed ||
                            task.state == LearningTaskState.needsAttention ||
                            task.state == LearningTaskState.failed)) ...[
                      const SizedBox(width: 2),
                      IconButton(
                        tooltip: '重新调度任务',
                        onPressed: onRedispatch,
                        visualDensity: VisualDensity.compact,
                        icon: const Icon(Icons.refresh, size: 18),
                      ),
                    ],
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
                const SizedBox(height: 9),
                _TaskProgressLine(task: task),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _PlanProgressBar extends StatelessWidget {
  const _PlanProgressBar({required this.progress});

  final PlanProgress progress;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final completed = progress.hasTasks && progress.percent >= 100;
    final parts = <String>[
      if (progress.active > 0) '待执行 ${progress.active} 项',
      if (progress.attention > 0) '需要处理 ${progress.attention} 项',
      if (progress.closed > 0) '已关闭 ${progress.closed} 项（不计入完成率）',
      if (progress.cancelled > 0) '已取消 ${progress.cancelled} 项',
    ];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Text(
              '总进度',
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
            const Spacer(),
            Text(
              progress.hasTasks
                  ? '已完成 ${progress.completed} / ${progress.executableTotal} · ${progress.percent}%'
                  : '暂无任务',
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 12,
                fontWeight: FontWeight.w500,
              ),
            ),
          ],
        ),
        const SizedBox(height: 6),
        TweenAnimationBuilder<double>(
          tween: Tween<double>(begin: 0, end: progress.value),
          duration: const Duration(milliseconds: 450),
          curve: Curves.easeOutCubic,
          builder: (context, value, _) => ClipRRect(
            borderRadius: BorderRadius.circular(999),
            child: LinearProgressIndicator(
              value: progress.hasTasks ? value : null,
              minHeight: 7,
              backgroundColor: colors.border,
              valueColor: AlwaysStoppedAnimation(
                completed ? colors.success : colors.accent,
              ),
            ),
          ),
        ),
        if (progress.hasTasks && parts.isNotEmpty) ...[
          const SizedBox(height: 6),
          Text(
            parts.join(' · '),
            style: TextStyle(
              color: progress.attention > 0 ? colors.danger : colors.muted,
              fontSize: 12,
              height: 1.5,
            ),
          ),
        ] else if (!progress.hasTasks) ...[
          const SizedBox(height: 6),
          Text(
            '课程同步后生成的任务会出现在这里。',
            style: TextStyle(color: colors.muted, fontSize: 12, height: 1.5),
          ),
        ],
      ],
    );
  }
}

class _TaskProgressLine extends StatelessWidget {
  const _TaskProgressLine({required this.task});

  final LearningTask task;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final observed = task.observation?.effectiveFraction;
    final closed = taskIsClosed(task);
    final (double? value, Color color, String label) = closed
        ? (0.0, colors.muted, taskClosedDetail(task))
        : switch (task.state) {
            LearningTaskState.confirmed => (1.0, colors.success, '平台确认完成'),
            LearningTaskState.cancelled => (
                observed ?? 1.0,
                colors.muted,
                '已取消',
              ),
            LearningTaskState.preparing || LearningTaskState.running => (
                observed,
                colors.accent,
                observed == null
                    ? '执行中，等待观察数据'
                    : '执行观察 ${formatPercent(observed)}',
              ),
            LearningTaskState.waitingVerification => (
                1.0,
                colors.warning,
                '等待平台确认',
              ),
            LearningTaskState.needsAttention || LearningTaskState.failed => (
                observed ?? 0.0,
                colors.danger,
                '需要处理',
              ),
            LearningTaskState.paused => (
                observed ?? 0.0,
                colors.warning,
                '已暂停',
              ),
            _ => (
                observed ?? 0.0,
                colors.accent,
                observed == null ? '等待执行' : '执行观察 ${formatPercent(observed)}',
              ),
          };
    final percent = value == null ? '进行中' : '${(value * 100).round()}%';
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Expanded(
              child: Text(
                label,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(color: colors.textSecondary, fontSize: 12),
              ),
            ),
            const SizedBox(width: 8),
            Text(
              percent,
              style: TextStyle(color: colors.muted, fontSize: 12),
            ),
          ],
        ),
        const SizedBox(height: 5),
        ClipRRect(
          borderRadius: BorderRadius.circular(999),
          child: LinearProgressIndicator(
            value: value,
            minHeight: 5,
            backgroundColor: colors.border,
            valueColor: AlwaysStoppedAnimation(color),
          ),
        ),
      ],
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

/// 当前页的任务列表；“当前”筛选按执行状态分组，其余筛选平铺。
class _TaskListPage extends StatelessWidget {
  const _TaskListPage({
    required this.tasks,
    required this.onTap,
    required this.onRedispatch,
    required this.grouped,
  });

  final List<LearningTask> tasks;
  final ValueChanged<LearningTask> onTap;
  final ValueChanged<LearningTask> onRedispatch;
  final bool grouped;

  @override
  Widget build(BuildContext context) {
    if (!grouped) {
      return Column(
        children: [
          for (final task in tasks)
            _TaskRow(
              task: task,
              onTap: () => onTap(task),
              onRedispatch: () => onRedispatch(task),
            ),
        ],
      );
    }
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
    return Column(
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
              _TaskRow(
                task: task,
                onTap: () => onTap(task),
                onRedispatch: () => onRedispatch(task),
              ),
          ],
      ],
    );
  }
}

/// 右下角页码切换控件。
class _TaskPagerControls extends StatelessWidget {
  const _TaskPagerControls({
    required this.page,
    required this.totalPages,
    required this.busy,
    required this.hasPrevious,
    required this.hasNext,
    required this.onPrevious,
    required this.onNext,
  });

  final int page;
  final int totalPages;
  final bool busy;
  final bool hasPrevious;
  final bool hasNext;
  final VoidCallback? onPrevious;
  final VoidCallback? onNext;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          totalPages > 0 ? '$page / $totalPages' : '$page',
          style: TextStyle(
            color: colors.textSecondary,
            fontSize: 12,
            fontWeight: FontWeight.w500,
          ),
        ),
        const SizedBox(width: 4),
        _PagerIcon(
          icon: Icons.chevron_left,
          tooltip: '上一页',
          busy: busy,
          enabled: hasPrevious,
          onTap: onPrevious,
        ),
        _PagerIcon(
          icon: Icons.chevron_right,
          tooltip: '下一页',
          busy: busy,
          enabled: hasNext,
          onTap: onNext,
        ),
      ],
    );
  }
}

class _PagerIcon extends StatelessWidget {
  const _PagerIcon({
    required this.icon,
    required this.tooltip,
    required this.busy,
    required this.enabled,
    required this.onTap,
  });

  final IconData icon;
  final String tooltip;
  final bool busy;
  final bool enabled;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final active = enabled && !busy;
    return Tooltip(
      message: tooltip,
      child: InkWell(
        onTap: active ? onTap : null,
        borderRadius: BorderRadius.circular(7),
        child: Container(
          width: 28,
          height: 28,
          decoration: BoxDecoration(
            color: active ? colors.surfaceSubtle : Colors.transparent,
            borderRadius: BorderRadius.circular(7),
            border:
                Border.all(color: active ? colors.border : Colors.transparent),
          ),
          child: busy
              ? Padding(
                  padding: const EdgeInsets.all(7),
                  child: CircularProgressIndicator(
                    strokeWidth: 2,
                    valueColor: AlwaysStoppedAnimation(colors.muted),
                  ),
                )
              : Icon(
                  icon,
                  size: 18,
                  color: active ? colors.textSecondary : colors.muted,
                ),
        ),
      ),
    );
  }
}
