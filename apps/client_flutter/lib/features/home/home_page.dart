import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/models/course.dart';
import '../../core/models/plan.dart';
import '../../core/models/task.dart';
import '../../core/models/wallet.dart';
import '../../design/components.dart';
import '../../design/theme.dart';
import '../courses/courses_controller.dart';
import '../plans/plans_controller.dart';
import '../plans/task_visuals.dart';
import '../wallet/wallet_controller.dart';
import 'home_controller.dart';

class HomePage extends ConsumerWidget {
  const HomePage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final user = ref.watch(currentUserProvider);
    final coursesAsync = ref.watch(coursesControllerProvider);
    final plansAsync = ref.watch(plansControllerProvider);
    final walletAsync = ref.watch(walletControllerProvider);
    final tasksAsync = ref.watch(homeTasksProvider);
    final courses =
        coursesAsync.valueOrNull?.courses ?? const <CourseSummary>[];
    final plans = plansAsync.valueOrNull?.plans ?? const <LearningPlan>[];
    final tasks = tasksAsync.valueOrNull?.tasks ?? const <LearningTask>[];
    final wallet = walletAsync.valueOrNull?.wallet;
    final pendingPoints = courses.fold<int>(
      0,
      (sum, course) => sum + course.incompleteTaskPointCount,
    );
    final enabledPlans = plans.where((plan) => plan.isActive).length;
    final confirmedTasks =
        tasks.where((task) => task.state == LearningTaskState.confirmed).length;
    final attention = tasks
        .where((task) =>
            task.state == LearningTaskState.needsAttention ||
            task.state == LearningTaskState.failed)
        .toList(growable: false);
    final recent = tasks
        .where((task) => task.state == LearningTaskState.confirmed)
        .take(3)
        .toList(growable: false);
    final now = DateTime.now();
    final greeting = now.hour < 6
        ? '夜深了'
        : now.hour < 12
            ? '上午好'
            : now.hour < 18
                ? '下午好'
                : '晚上好';
    final dateText = '${now.month} 月 ${now.day} 日 · ${_weekday(now.weekday)}';

    return RefreshIndicator(
      onRefresh: () => _refresh(ref),
      child: SingleChildScrollView(
        key: const PageStorageKey('home-scroll'),
        physics: const AlwaysScrollableScrollPhysics(),
        child: ResponsiveContent(
            child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            LayoutBuilder(builder: (context, constraints) {
              final greetingWidget = _Greeting(
                  greeting: greeting,
                  name: user?.displayLabel ?? '学习通用户',
                  school: user?.schoolLabel ?? '—',
                  dateText: dateText,
                  connected: user?.platformActive ?? false);
              final balance = _HomeBalance(
                  loading: walletAsync.isLoading,
                  error: walletAsync.hasError,
                  wallet: wallet,
                  onRetry: () => ref.invalidate(walletControllerProvider),
                  onOpen: () => context.go('/wallet'));
              return constraints.maxWidth >= 900
                  ? Row(children: [
                      Expanded(child: greetingWidget),
                      const SizedBox(width: 24),
                      Flexible(child: balance)
                    ])
                  : Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                          greetingWidget,
                          const SizedBox(height: 12),
                          balance
                        ]);
            }),
            const SizedBox(height: 20),
            _StatsStrip(
                courseCount: coursesAsync.hasValue ? courses.length : null,
                pendingPoints: coursesAsync.hasValue ? pendingPoints : null,
                enabledPlans: plansAsync.hasValue ? enabledPlans : null,
                confirmedTasks: tasksAsync.hasValue ? confirmedTasks : null,
                onCourses: () => context.go('/courses'),
                onPlans: () => context.go('/plans')),
            if (attention.isNotEmpty) ...[
              const SizedBox(height: 16),
              _AttentionBanner(
                  task: attention.first,
                  count: attention.length,
                  onOpen: () => context.go('/plans')),
            ],
            if (coursesAsync.hasError ||
                plansAsync.hasError ||
                tasksAsync.hasError) ...[
              const SizedBox(height: 16),
              InlineNotice(
                  tone: StatusTone.warning,
                  message: _errorSummary(coursesAsync, plansAsync, tasksAsync),
                  action: TextButton(
                      onPressed: () => _refresh(ref), child: const Text('重试'))),
            ],
            const SizedBox(height: 24),
            _SectionHeading(
                title: '我的课程',
                action: TextButton(
                    onPressed: () => context.go('/courses'),
                    child: const Text('全部课程'))),
            const SizedBox(height: 8),
            if (coursesAsync.isLoading)
              const LoadingList(rows: 3, height: 64)
            else if (coursesAsync.hasError)
              ErrorPanel(
                  message: coursesAsync.error is ApiException
                      ? (coursesAsync.error! as ApiException).userMessage
                      : '课程暂时无法加载',
                  compact: true,
                  onRetry: () => ref.invalidate(coursesControllerProvider))
            else if (courses.isEmpty)
              EmptyState(
                  title: '还没有同步课程',
                  compact: true,
                  action: OutlinedButton(
                      onPressed: () => context.go('/courses'),
                      child: const Text('同步课程')))
            else
              Column(children: [
                for (final course in courses.take(3))
                  _HomeCourseRow(
                      course: course,
                      onTap: () => context.go('/courses/${course.id}'))
              ]),
            const SizedBox(height: 24),
            _PlansSection(
              async: plansAsync,
              onOpen: () => context.go('/plans'),
              onRetry: () => ref.invalidate(plansControllerProvider),
              recent: _RecentSection(
                  async: tasksAsync,
                  tasks: recent,
                  onOpen: () => context.go('/plans'),
                  onRetry: () => ref.invalidate(homeTasksProvider)),
            ),
          ],
        )),
      ),
    );
  }

  Future<void> _refresh(WidgetRef ref) async {
    final futures = <Future<void>>[];
    if (ref.read(coursesControllerProvider).hasValue) {
      futures.add(
        ref.read(coursesControllerProvider.notifier).refresh(silent: true),
      );
    } else {
      ref.invalidate(coursesControllerProvider);
    }
    if (ref.read(plansControllerProvider).hasValue) {
      futures.add(
        ref.read(plansControllerProvider.notifier).refresh(silent: true),
      );
    } else {
      ref.invalidate(plansControllerProvider);
    }
    if (ref.read(walletControllerProvider).hasValue) {
      futures.add(
        ref.read(walletControllerProvider.notifier).refresh(silent: true),
      );
    } else {
      ref.invalidate(walletControllerProvider);
    }
    if (ref.read(homeTasksProvider).hasValue) {
      futures.add(
        ref.read(homeTasksProvider.notifier).refresh(silent: true),
      );
    } else {
      ref.invalidate(homeTasksProvider);
    }
    await Future.wait(futures);
  }

  String _errorSummary(
    AsyncValue<CourseListState> courses,
    AsyncValue<PlansState> plans,
    AsyncValue<HomeTasksState> tasks,
  ) {
    final messages = <String>[];
    if (courses.hasError) messages.add('课程');
    if (plans.hasError) messages.add('安排');
    if (tasks.hasError) messages.add('任务');
    return '${messages.join('、')}摘要暂时无法加载，可继续使用其他功能。';
  }

  String _weekday(int value) {
    return const ['星期一', '星期二', '星期三', '星期四', '星期五', '星期六', '星期日'][value - 1];
  }
}

class _Greeting extends StatelessWidget {
  const _Greeting({
    required this.greeting,
    required this.name,
    required this.school,
    required this.dateText,
    required this.connected,
  });

  final String greeting;
  final String name;
  final String school;
  final String dateText;
  final bool connected;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Container(
              width: 7,
              height: 7,
              decoration: BoxDecoration(
                color: connected ? colors.success : colors.warning,
                shape: BoxShape.circle,
              ),
            ),
            const SizedBox(width: 6),
            Expanded(
              child: Text(
                connected ? '$school · 学习通已连接' : '$school · 需要重新验证',
                style: TextStyle(
                  color: colors.textSecondary,
                  fontSize: 12,
                ),
              ),
            ),
            Text(
              dateText,
              style: TextStyle(color: colors.muted, fontSize: 12),
            ),
          ],
        ),
        const SizedBox(height: 12),
        Text(
          '$greeting，$name。',
          style: TextStyle(
            color: colors.text,
            fontSize: 26,
            fontWeight: FontWeight.w600,
            letterSpacing: -0.8,
            height: 1.25,
          ),
        ),
      ],
    );
  }
}

class _StatsStrip extends StatelessWidget {
  const _StatsStrip(
      {required this.courseCount,
      required this.pendingPoints,
      required this.enabledPlans,
      required this.confirmedTasks,
      required this.onCourses,
      required this.onPlans});
  final int? courseCount, pendingPoints, enabledPlans, confirmedTasks;
  final VoidCallback onCourses, onPlans;
  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final values = [courseCount, pendingPoints, enabledPlans, confirmedTasks];
    const labels = ['已同步课程', '待确认', '开启安排', '近期确认'];
    return Container(
      decoration: BoxDecoration(
          border: Border(
              top: BorderSide(color: colors.border),
              bottom: BorderSide(color: colors.border))),
      child: IntrinsicHeight(
          child: Row(children: [
        for (var index = 0; index < labels.length; index++) ...[
          if (index > 0)
            VerticalDivider(
                width: 1, color: colors.border, indent: 16, endIndent: 16),
          Expanded(
              child: InkWell(
            onTap: index == 0 ? onCourses : onPlans,
            child: Padding(
                padding:
                    const EdgeInsets.symmetric(vertical: 16, horizontal: 8),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(values[index]?.toString() ?? '—',
                        style: TextStyle(
                            color: colors.text,
                            fontSize: 26,
                            fontWeight: FontWeight.w600,
                            fontFeatures: const [
                              FontFeature.tabularFigures()
                            ])),
                    const SizedBox(height: 4),
                    Text(labels[index],
                        style: TextStyle(
                            color: colors.textSecondary, fontSize: 12)),
                  ],
                )),
          )),
        ],
      ])),
    );
  }
}

class _HomeBalance extends StatelessWidget {
  const _HomeBalance(
      {required this.loading,
      required this.error,
      required this.wallet,
      required this.onRetry,
      required this.onOpen});
  final bool loading, error;
  final WalletSnapshot? wallet;
  final VoidCallback onRetry, onOpen;
  @override
  Widget build(BuildContext context) => Wrap(
        crossAxisAlignment: WrapCrossAlignment.center,
        spacing: 12,
        runSpacing: 8,
        children: [
          Text('我的钱包',
              style:
                  TextStyle(color: context.colors.textSecondary, fontSize: 13)),
          if (loading)
            const SkeletonBlock(width: 100, height: 26)
          else if (error)
            TextButton(onPressed: onRetry, child: const Text('余额读取失败，重试'))
          else
            Text(wallet?.available.text ?? '—',
                style: const TextStyle(
                    fontSize: 26,
                    fontWeight: FontWeight.w600,
                    fontFeatures: [FontFeature.tabularFigures()])),
          if (!loading && !error)
            Text('预占 ${wallet?.reserved.text ?? '—'}',
                style: TextStyle(
                    color: context.colors.textSecondary, fontSize: 12)),
          OutlinedButton(onPressed: onOpen, child: const Text('充值')),
        ],
      );
}

class _AttentionBanner extends StatelessWidget {
  const _AttentionBanner({
    required this.task,
    required this.count,
    required this.onOpen,
  });

  final LearningTask task;
  final int count;
  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: colors.warningBg,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.warning.withValues(alpha: 0.2)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(Icons.report_problem_outlined, size: 18, color: colors.warning),
          const SizedBox(width: 9),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  count > 1 ? '$count 项任务需要处理' : '有一项任务需要处理',
                  style: TextStyle(
                    color: colors.text,
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 3),
                Text(
                  '${task.title} · ${taskFailureLabel(task)}',
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 12,
                    height: 1.5,
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(width: 8),
          TextButton(
            onPressed: onOpen,
            child: const Text('去处理'),
          ),
        ],
      ),
    );
  }
}

class _InlineEmpty extends StatelessWidget {
  const _InlineEmpty({required this.message, this.action});
  final String message;
  final Widget? action;
  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 20),
        child: Row(children: [
          Expanded(
              child: Text(message,
                  style: TextStyle(
                      color: context.colors.textSecondary, fontSize: 14))),
          if (action != null) action!,
        ]),
      );
}

class _SectionHeading extends StatelessWidget {
  const _SectionHeading({required this.title, required this.action});
  final String title;
  final Widget action;
  @override
  Widget build(BuildContext context) => Row(children: [
        Expanded(
            child: Text(title,
                style: const TextStyle(
                    fontSize: 16, fontWeight: FontWeight.w600))),
        action
      ]);
}

class _HomeCourseRow extends StatelessWidget {
  const _HomeCourseRow({required this.course, required this.onTap});
  final CourseSummary course;
  final VoidCallback onTap;
  @override
  Widget build(BuildContext context) => InkWell(
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.symmetric(vertical: 16),
          decoration: BoxDecoration(
              border: Border(bottom: BorderSide(color: context.colors.border))),
          child: Row(children: [
            Expanded(
                child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                  Text(course.title,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                          fontSize: 14, fontWeight: FontWeight.w600)),
                  const SizedBox(height: 4),
                  Text(
                      '平台已确认 ${course.confirmedTaskPoints} / ${course.taskPointCount}',
                      style: TextStyle(
                          color: context.colors.textSecondary, fontSize: 13)),
                ])),
            const SizedBox(width: 16),
            const Icon(Icons.chevron_right, size: 16),
          ]),
        ),
      );
}

class _PlansSection extends StatelessWidget {
  const _PlansSection(
      {required this.async,
      required this.onOpen,
      required this.onRetry,
      required this.recent});
  final AsyncValue<PlansState> async;
  final VoidCallback onOpen, onRetry;
  final Widget recent;
  @override
  Widget build(BuildContext context) => SectionCard(
        title: '你的学习安排',
        framed: false,
        padding: EdgeInsets.zero,
        headerPadding: EdgeInsets.zero,
        action: TextButton(onPressed: onOpen, child: const Text('管理安排')),
        child:
            Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
          async.when(
            loading: () => const LoadingList(rows: 2, height: 64),
            error: (error, _) => ErrorPanel(
                message: error is ApiException ? error.userMessage : '安排暂时无法加载',
                compact: true,
                onRetry: onRetry),
            data: (data) => data.plans.isEmpty
                ? _InlineEmpty(
                    message: '还没有任务安排',
                    action: TextButton(
                        onPressed: onOpen, child: const Text('新建安排')))
                : Column(children: [
                    for (final plan in data.plans.take(3))
                      _HomePlanRow(plan: plan, onTap: onOpen)
                  ]),
          ),
          const SizedBox(height: 16),
          Divider(color: context.colors.border),
          recent,
        ]),
      );
}

class _HomePlanRow extends StatelessWidget {
  const _HomePlanRow({required this.plan, required this.onTap});

  final LearningPlan plan;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(colors.radius - 3),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 9, horizontal: 2),
        child: Row(
          children: [
            Container(
              width: 34,
              height: 34,
              decoration: BoxDecoration(
                color: colors.surfaceSubtle,
                borderRadius: BorderRadius.circular(8),
                border: Border.all(color: colors.border),
              ),
              child: Icon(
                Icons.calendar_today_outlined,
                size: 16,
                color: colors.textSecondary,
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    plan.name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: colors.text,
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    '${plan.courseIds.length} 门课程 · ${plan.taskTypes.length} 种任务',
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
              label: plan.isActive ? '已开启' : '已暂停',
              tone: plan.isActive ? StatusTone.success : StatusTone.warning,
              compact: true,
            ),
          ],
        ),
      ),
    );
  }
}

class _RecentSection extends StatelessWidget {
  const _RecentSection({
    required this.async,
    required this.tasks,
    required this.onOpen,
    required this.onRetry,
  });

  final AsyncValue<HomeTasksState> async;
  final List<LearningTask> tasks;
  final VoidCallback onOpen;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return SectionCard(
      title: '最近确认',
      framed: false,
      padding: EdgeInsets.zero,
      headerPadding: EdgeInsets.zero,
      child: async.when(
        loading: () => const LoadingList(rows: 2, height: 52),
        error: (error, _) => ErrorPanel(
          message: error is ApiException ? error.userMessage : '任务摘要暂时无法加载',
          compact: true,
          onRetry: onRetry,
        ),
        data: (_) {
          if (tasks.isEmpty) {
            return const _InlineEmpty(message: '暂无平台确认记录');
          }
          return Column(
            children: [
              for (final task in tasks)
                InkWell(
                  onTap: onOpen,
                  borderRadius: BorderRadius.circular(colorsFor(context)),
                  child: Padding(
                    padding:
                        const EdgeInsets.symmetric(vertical: 9, horizontal: 2),
                    child: Row(
                      children: [
                        Icon(
                          task.platform.isConfirmed
                              ? Icons.verified_outlined
                              : Icons.fact_check_outlined,
                          size: 17,
                          color: task.platform.isConfirmed
                              ? context.colors.success
                              : context.colors.warning,
                        ),
                        const SizedBox(width: 9),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                task.title,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: TextStyle(
                                  color: context.colors.text,
                                  fontSize: 13,
                                  fontWeight: FontWeight.w500,
                                ),
                              ),
                              const SizedBox(height: 2),
                              Text(
                                task.courseTitle ?? '课程',
                                style: TextStyle(
                                  color: context.colors.textSecondary,
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
                  ),
                ),
            ],
          );
        },
      ),
    );
  }
}

double colorsFor(BuildContext context) => context.colors.radius - 3;
