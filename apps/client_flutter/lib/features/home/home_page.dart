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
        .where((task) =>
            task.state == LearningTaskState.confirmed ||
            task.state == LearningTaskState.waitingVerification)
        .take(4)
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
        physics: const AlwaysScrollableScrollPhysics(),
        padding: EdgeInsets.only(bottom: context.isNarrow ? 12 : 28),
        child: ResponsiveContent(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              _Greeting(
                greeting: greeting,
                name: user?.displayLabel ?? '学习通用户',
                school: user?.schoolLabel ?? '学校信息待同步',
                dateText: dateText,
                connected: user?.platformActive ?? false,
              ),
              const SizedBox(height: 18),
              LayoutBuilder(
                builder: (context, constraints) {
                  final wide = constraints.maxWidth >= 900;
                  final hero = _HeroCard(
                    enabledPlans: plansAsync.hasValue ? enabledPlans : null,
                    pendingPoints: coursesAsync.hasValue ? pendingPoints : null,
                    onPlans: () => context.go('/plans'),
                    onCourses: () => context.go('/courses'),
                  );
                  final walletCard = _HomeWalletCard(
                    loading: walletAsync.isLoading,
                    error: walletAsync.hasError,
                    wallet: wallet,
                    pricing: walletAsync.valueOrNull?.pricing,
                    onRetry: () => ref.invalidate(walletControllerProvider),
                    onOpen: () => context.go('/wallet'),
                  );
                  if (!wide) {
                    return Column(
                      children: [
                        hero,
                        const SizedBox(height: 12),
                        walletCard,
                      ],
                    );
                  }
                  return Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Expanded(flex: 7, child: hero),
                      const SizedBox(width: 14),
                      Expanded(flex: 4, child: walletCard),
                    ],
                  );
                },
              ),
              const SizedBox(height: 14),
              _StatsStrip(
                courseCount: coursesAsync.hasValue ? courses.length : null,
                pendingPoints: coursesAsync.hasValue ? pendingPoints : null,
                enabledPlans: plansAsync.hasValue ? enabledPlans : null,
                confirmedTasks: tasksAsync.hasValue ? confirmedTasks : null,
                onCourses: () => context.go('/courses'),
                onPlans: () => context.go('/plans'),
              ),
              if (attention.isNotEmpty) ...[
                const SizedBox(height: 14),
                _AttentionBanner(
                  task: attention.first,
                  count: attention.length,
                  onOpen: () => context.go('/plans'),
                ),
              ],
              if (coursesAsync.hasError ||
                  plansAsync.hasError ||
                  tasksAsync.hasError) ...[
                const SizedBox(height: 14),
                InlineNotice(
                  tone: StatusTone.warning,
                  icon: Icons.cloud_off_outlined,
                  message: _errorSummary(coursesAsync, plansAsync, tasksAsync),
                  action: TextButton(
                    onPressed: () => _refresh(ref),
                    child: const Text('重试'),
                  ),
                ),
              ],
              const SizedBox(height: 24),
              _SectionHeading(
                title: '继续你的学习',
                subtitle: '课程完成数来自学习通平台返回。',
                action: TextButton(
                  onPressed: () => context.go('/courses'),
                  child: const Text('全部课程'),
                ),
              ),
              const SizedBox(height: 12),
              if (coursesAsync.isLoading)
                const LoadingList(rows: 1, height: 150)
              else if (coursesAsync.hasError)
                ErrorPanel(
                  message: coursesAsync.error is ApiException
                      ? (coursesAsync.error! as ApiException).userMessage
                      : '课程暂时无法加载',
                  compact: true,
                  onRetry: () => ref.invalidate(coursesControllerProvider),
                )
              else if (courses.isEmpty)
                SectionCard(
                  child: EmptyState(
                    icon: Icons.menu_book_outlined,
                    title: '还没有同步课程',
                    message: '同步学习通课程后，任务点与进度会显示在这里。',
                    compact: true,
                    action: OutlinedButton(
                      onPressed: () => context.go('/courses'),
                      child: const Text('去同步课程'),
                    ),
                  ),
                )
              else
                LayoutBuilder(
                  builder: (context, constraints) {
                    final columns = constraints.maxWidth >= 980
                        ? 3
                        : constraints.maxWidth >= 640
                            ? 2
                            : 1;
                    final spacing = 12.0;
                    final width =
                        (constraints.maxWidth - spacing * (columns - 1)) /
                            columns;
                    return Wrap(
                      spacing: spacing,
                      runSpacing: spacing,
                      children: [
                        for (final course in courses.take(3))
                          SizedBox(
                            width: width,
                            child: _MiniCourseCard(
                              course: course,
                              onTap: () => context.go('/courses/${course.id}'),
                            ),
                          ),
                      ],
                    );
                  },
                ),
              const SizedBox(height: 24),
              LayoutBuilder(
                builder: (context, constraints) {
                  final wide = constraints.maxWidth >= 900;
                  final plansSection = _PlansSection(
                    async: plansAsync,
                    onOpen: () => context.go('/plans'),
                    onRetry: () => ref.invalidate(plansControllerProvider),
                  );
                  final recentSection = _RecentSection(
                    async: tasksAsync,
                    tasks: recent,
                    onOpen: () => context.go('/plans'),
                    onRetry: () => ref.invalidate(homeTasksProvider),
                  );
                  if (!wide) {
                    return Column(
                      children: [
                        plansSection,
                        const SizedBox(height: 14),
                        recentSection,
                      ],
                    );
                  }
                  return Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Expanded(child: plansSection),
                      const SizedBox(width: 14),
                      Expanded(child: recentSection),
                    ],
                  );
                },
              ),
            ],
          ),
        ),
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
              style: TextStyle(color: colors.muted, fontSize: 11.5),
            ),
          ],
        ),
        const SizedBox(height: 12),
        Text(
          '$greeting，$name。',
          style: TextStyle(
            color: colors.text,
            fontSize: 27,
            fontWeight: FontWeight.w600,
            letterSpacing: -0.8,
            height: 1.25,
          ),
        ),
        const SizedBox(height: 5),
        Text(
          '学习的每一步，都值得被看见。',
          style: TextStyle(
            color: colors.textSecondary,
            fontSize: 13.5,
          ),
        ),
      ],
    );
  }
}

class _HeroCard extends StatelessWidget {
  const _HeroCard({
    required this.enabledPlans,
    required this.pendingPoints,
    required this.onPlans,
    required this.onCourses,
  });

  final int? enabledPlans;
  final int? pendingPoints;
  final VoidCallback onPlans;
  final VoidCallback onCourses;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final narrow = context.isNarrow;
    return Container(
      padding: EdgeInsets.all(narrow ? 20 : 26),
      decoration: BoxDecoration(
        color: colors.heroBg,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      child: Stack(
        children: [
          Positioned(
            right: -24,
            top: -30,
            child: Container(
              width: 150,
              height: 150,
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                border: Border.all(
                  color: colors.heroText.withValues(alpha: 0.12),
                ),
              ),
            ),
          ),
          Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                '你的学习空间',
                style: TextStyle(
                  color: colors.heroText.withValues(alpha: 0.7),
                  fontSize: 11,
                  letterSpacing: 1.1,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 10),
              Text(
                '让计划向前，\n让时间留给你。',
                style: TextStyle(
                  color: colors.heroText,
                  fontSize: narrow ? 25 : 32,
                  height: 1.3,
                  fontWeight: FontWeight.w600,
                  letterSpacing: -0.7,
                ),
              ),
              const SizedBox(height: 10),
              Text(
                enabledPlans == null || pendingPoints == null
                    ? '正在读取你的课程与安排摘要。'
                    : pendingPoints == 0
                        ? '当前 $enabledPlans 条安排已开启，没有待确认的任务点。'
                        : '当前 $enabledPlans 条安排已开启，$pendingPoints 个任务点仍待平台确认。',
                style: TextStyle(
                  color: colors.heroText.withValues(alpha: 0.8),
                  fontSize: 13,
                  height: 1.65,
                ),
              ),
              const SizedBox(height: 18),
              Wrap(
                spacing: 10,
                runSpacing: 10,
                children: [
                  FilledButton.icon(
                    onPressed: onPlans,
                    style: FilledButton.styleFrom(
                      backgroundColor: colors.surface,
                      foregroundColor: colors.heroText,
                    ),
                    icon: const Icon(Icons.calendar_month_outlined, size: 16),
                    label: const Text('查看我的安排'),
                  ),
                  OutlinedButton.icon(
                    onPressed: onCourses,
                    style: OutlinedButton.styleFrom(
                      foregroundColor: colors.heroText,
                      side: BorderSide(
                        color: colors.heroText.withValues(alpha: 0.25),
                      ),
                    ),
                    icon: const Icon(Icons.sync_outlined, size: 16),
                    label: const Text('课程与同步'),
                  ),
                ],
              ),
              const SizedBox(height: 16),
              Wrap(
                spacing: 14,
                runSpacing: 6,
                children: [
                  _HeroFact(
                    icon: Icons.verified_outlined,
                    label: '平台确认再计完成',
                  ),
                  _HeroFact(
                    icon: Icons.timer_outlined,
                    label: '预计时间标注为估算',
                  ),
                ],
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _HeroFact extends StatelessWidget {
  const _HeroFact({required this.icon, required this.label});

  final IconData icon;
  final String label;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 13, color: colors.heroText.withValues(alpha: 0.6)),
        const SizedBox(width: 5),
        Text(
          label,
          style: TextStyle(
            color: colors.heroText.withValues(alpha: 0.65),
            fontSize: 10.5,
          ),
        ),
      ],
    );
  }
}

class _HomeWalletCard extends StatelessWidget {
  const _HomeWalletCard({
    required this.loading,
    required this.error,
    required this.wallet,
    required this.pricing,
    required this.onRetry,
    required this.onOpen,
  });

  final bool loading;
  final bool error;
  final WalletSnapshot? wallet;
  final PricingInfo? pricing;
  final VoidCallback onRetry;
  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return SectionCard(
      title: '我的钱包',
      action: IconButton(
        tooltip: '查看钱包',
        onPressed: onOpen,
        icon: const Icon(Icons.arrow_outward, size: 17),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (loading)
            const SkeletonBlock(height: 42, width: 150)
          else if (error)
            Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  '余额暂不可用',
                  style: TextStyle(color: colors.text, fontSize: 14),
                ),
                const SizedBox(height: 6),
                TextButton(
                  onPressed: onRetry,
                  child: const Text('重新加载'),
                ),
              ],
            )
          else ...[
            Text(
              '可用余额',
              style: TextStyle(color: colors.textSecondary, fontSize: 11.5),
            ),
            const SizedBox(height: 4),
            Text(
              wallet?.available.text ?? '—',
              style: TextStyle(
                color: colors.text,
                fontSize: 28,
                fontWeight: FontWeight.w600,
                letterSpacing: -0.7,
                fontFeatures: const [FontFeature.tabularFigures()],
              ),
            ),
            const SizedBox(height: 4),
            Text(
              '预占 ${wallet?.reserved.text ?? '—'}',
              style: TextStyle(color: colors.muted, fontSize: 11),
            ),
          ],
          const SizedBox(height: 12),
          Text(
            pricing == null
                ? '计费信息暂不可用'
                : pricing!.automaticCharging
                    ? '按平台确认的任务点计量'
                    : '单价待公布，暂未自动扣费',
            style: TextStyle(
              color: colors.textSecondary,
              fontSize: 11,
              height: 1.5,
            ),
          ),
          const SizedBox(height: 12),
          SizedBox(
            width: double.infinity,
            child: OutlinedButton.icon(
              onPressed: onOpen,
              icon: const Icon(Icons.add_card_outlined, size: 16),
              label: const Text('充值或查看明细'),
            ),
          ),
        ],
      ),
    );
  }
}

class _StatsStrip extends StatelessWidget {
  const _StatsStrip({
    required this.courseCount,
    required this.pendingPoints,
    required this.enabledPlans,
    required this.confirmedTasks,
    required this.onCourses,
    required this.onPlans,
  });

  final int? courseCount;
  final int? pendingPoints;
  final int? enabledPlans;
  final int? confirmedTasks;
  final VoidCallback onCourses;
  final VoidCallback onPlans;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final columns = constraints.maxWidth >= 860
            ? 4
            : constraints.maxWidth >= 520
                ? 2
                : 2;
        final spacing = 10.0;
        final width =
            (constraints.maxWidth - spacing * (columns - 1)) / columns;
        return Wrap(
          spacing: spacing,
          runSpacing: spacing,
          children: [
            SizedBox(
              width: width,
              child: MetricTile(
                label: '门课程已同步',
                value: courseCount?.toString() ?? '—',
                icon: Icons.menu_book_outlined,
                onTap: onCourses,
              ),
            ),
            SizedBox(
              width: width,
              child: MetricTile(
                label: '个任务点待确认',
                value: pendingPoints?.toString() ?? '—',
                icon: Icons.pending_actions_outlined,
                onTap: onCourses,
              ),
            ),
            SizedBox(
              width: width,
              child: MetricTile(
                label: '条安排已开启',
                value: enabledPlans?.toString() ?? '—',
                icon: Icons.calendar_month_outlined,
                onTap: onPlans,
              ),
            ),
            SizedBox(
              width: width,
              child: MetricTile(
                label: '项近期平台确认',
                value: confirmedTasks?.toString() ?? '—',
                icon: Icons.verified_outlined,
                onTap: onPlans,
              ),
            ),
          ],
        );
      },
    );
  }
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
                    fontSize: 11.5,
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

class _SectionHeading extends StatelessWidget {
  const _SectionHeading({
    required this.title,
    this.subtitle,
    this.action,
  });

  final String title;
  final String? subtitle;
  final Widget? action;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Row(
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                title,
                style: TextStyle(
                  color: colors.text,
                  fontSize: 17,
                  fontWeight: FontWeight.w600,
                ),
              ),
              if (subtitle != null) ...[
                const SizedBox(height: 3),
                Text(
                  subtitle!,
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 12,
                  ),
                ),
              ],
            ],
          ),
        ),
        if (action != null) action!,
      ],
    );
  }
}

class _MiniCourseCard extends StatelessWidget {
  const _MiniCourseCard({required this.course, required this.onTap});

  final CourseSummary course;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final progress = course.taskPointCount == 0
        ? 0.0
        : course.confirmedTaskPoints / course.taskPointCount;
    return InkWell(
      onTap: onTap,
      borderRadius: colors.cardRadius,
      child: Container(
        decoration: BoxDecoration(
          color: colors.surface,
          borderRadius: colors.cardRadius,
          border: Border.all(color: colors.border),
        ),
        clipBehavior: Clip.antiAlias,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            CourseArt(seed: course.id, height: 74),
            Padding(
              padding: const EdgeInsets.fromLTRB(13, 11, 13, 13),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    course.title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: colors.text,
                      fontSize: 13.5,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    '平台已确认 ${course.confirmedTaskPoints} / ${course.taskPointCount}',
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 11,
                    ),
                  ),
                  const SizedBox(height: 7),
                  ProgressBar(value: progress, height: 4),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _PlansSection extends StatelessWidget {
  const _PlansSection({
    required this.async,
    required this.onOpen,
    required this.onRetry,
  });

  final AsyncValue<PlansState> async;
  final VoidCallback onOpen;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return SectionCard(
      title: '你的学习安排',
      action: TextButton(onPressed: onOpen, child: const Text('管理安排')),
      child: async.when(
        loading: () => const LoadingList(rows: 2, height: 58),
        error: (error, _) => ErrorPanel(
          message: error is ApiException ? error.userMessage : '安排暂时无法加载',
          compact: true,
          onRetry: onRetry,
        ),
        data: (data) {
          if (data.plans.isEmpty) {
            return EmptyState(
              icon: Icons.calendar_month_outlined,
              title: '还没有任务安排',
              message: '选择课程和章节，建立第一条学习安排。',
              compact: true,
              action: OutlinedButton(
                onPressed: onOpen,
                child: const Text('新建安排'),
              ),
            );
          }
          return Column(
            children: [
              for (final plan in data.plans.take(2))
                _HomePlanRow(plan: plan, onTap: onOpen),
            ],
          );
        },
      ),
    );
  }
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
                      fontSize: 11,
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
      title: '最近进展',
      action: const StatusPill(label: '平台返回', tone: StatusTone.neutral),
      child: async.when(
        loading: () => const LoadingList(rows: 2, height: 52),
        error: (error, _) => ErrorPanel(
          message: error is ApiException ? error.userMessage : '任务摘要暂时无法加载',
          compact: true,
          onRetry: onRetry,
        ),
        data: (_) {
          if (tasks.isEmpty) {
            return const EmptyState(
              icon: Icons.fact_check_outlined,
              title: '还没有平台返回的进展',
              message: '任务执行结束后会再次查询平台，确认结果会显示在这里。',
              compact: true,
            );
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
                                  fontSize: 12.5,
                                  fontWeight: FontWeight.w500,
                                ),
                              ),
                              const SizedBox(height: 2),
                              Text(
                                task.courseTitle ?? '课程',
                                style: TextStyle(
                                  color: context.colors.textSecondary,
                                  fontSize: 10.5,
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
