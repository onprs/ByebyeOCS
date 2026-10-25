import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/providers.dart';
import '../../app/scroll_controllers.dart';
import '../../app/tab_navigation.dart';
import '../../core/api/api_error.dart';
import '../../core/models/course.dart';
import '../../core/models/plan.dart';
import '../../core/models/task.dart';
import '../../core/models/wallet.dart';
import '../../design/components.dart';
import '../../design/theme.dart';
import '../courses/course_sync_notice.dart';
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
    final scroll = ref.watch(tabScrollControllerProvider('home'));
    final coursesAsync = ref.watch(coursesControllerProvider);
    final plansAsync = ref.watch(plansControllerProvider);
    final walletAsync = ref.watch(walletControllerProvider);
    final tasksAsync = ref.watch(homeTasksProvider);
    final courses =
        coursesAsync.valueOrNull?.courses ?? const <CourseSummary>[];
    final plans = plansAsync.valueOrNull?.plans ?? const <LearningPlan>[];
    final tasks = tasksAsync.valueOrNull?.tasks ?? const <LearningTask>[];
    final wallet = walletAsync.valueOrNull?.wallet;
    if (coursesAsync.hasValue) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (context.mounted) {
          ref.read(coursesControllerProvider.notifier).autoSyncIfNeeded();
        }
      });
    }
    final syncState = coursesAsync.valueOrNull;
    final syncActive = syncState != null &&
        (syncState.syncing ||
            syncState.syncError != null ||
            (syncState.syncResult?.needsAttention ?? false));
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
        controller: scroll,
        primary: false,
        physics: const AlwaysScrollableScrollPhysics(),
        child: ResponsiveContent(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              _Greeting(
                greeting: greeting,
                name: user?.displayLabel ?? '学习通用户',
                school: user?.schoolLabel ?? '—',
                dateText: dateText,
                connected: user?.platformActive ?? false,
              ),
              if (syncActive) ...[
                const SizedBox(height: 16),
                CourseSyncNotice(
                  state: syncState,
                  onRetry: () => ref
                      .read(coursesControllerProvider.notifier)
                      .syncCourses(),
                ),
              ],
              const SizedBox(height: 22),
              LayoutBuilder(builder: (context, constraints) {
                final overview = _LearningOverview(
                  course: courses.isEmpty ? null : courses.first,
                  courseCount: coursesAsync.hasValue ? courses.length : null,
                  pendingPoints: pendingPoints,
                  loading: coursesAsync.isLoading,
                  syncing: syncState?.syncing ?? false,
                  error: coursesAsync.hasError,
                  onOpenCourse: courses.isEmpty
                      ? null
                      : () => context.go('/courses/${courses.first.id}'),
                  onOpenCourses: () => openTab(context, 1, '/courses'),
                  onRetry: () => ref.invalidate(coursesControllerProvider),
                );
                final walletCard = _WalletCard(
                  loading: walletAsync.isLoading,
                  error: walletAsync.hasError,
                  wallet: wallet,
                  onRetry: () => ref.invalidate(walletControllerProvider),
                  onRecharge: () => openTab(context, 3, '/wallet'),
                  onDetails: () => openTab(context, 3, '/wallet'),
                );
                if (constraints.maxWidth < 900) {
                  return Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      overview,
                      const SizedBox(height: 16),
                      walletCard,
                    ],
                  );
                }
                return Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(child: overview),
                    const SizedBox(width: 18),
                    SizedBox(width: 320, child: walletCard),
                  ],
                );
              }),
              const SizedBox(height: 22),
              _StatsStrip(
                courseCount: coursesAsync.hasValue ? courses.length : null,
                pendingPoints: coursesAsync.hasValue ? pendingPoints : null,
                enabledPlans: plansAsync.hasValue ? enabledPlans : null,
                confirmedTasks: tasksAsync.hasValue ? confirmedTasks : null,
                onCourses: () => openTab(context, 1, '/courses'),
                onPlans: () => openTab(context, 2, '/plans'),
              ),
              if (attention.isNotEmpty) ...[
                const SizedBox(height: 16),
                _AttentionBanner(
                  task: attention.first,
                  count: attention.length,
                  onOpen: () => openTab(context, 2, '/plans'),
                ),
              ],
              if (coursesAsync.hasError ||
                  plansAsync.hasError ||
                  tasksAsync.hasError) ...[
                const SizedBox(height: 16),
                InlineNotice(
                  tone: StatusTone.warning,
                  message: _errorSummary(coursesAsync, plansAsync, tasksAsync),
                  action: TextButton(
                    onPressed: () => _refresh(ref),
                    child: const Text('重试'),
                  ),
                ),
              ],
              const SizedBox(height: 30),
              _SectionHeading(
                title: '继续你的学习',
                subtitle: '平台确认的任务点会累计到课程进度。',
                action: TextButton(
                  onPressed: () => openTab(context, 1, '/courses'),
                  child: const Text('全部课程'),
                ),
              ),
              const SizedBox(height: 14),
              if (coursesAsync.isLoading && !coursesAsync.hasValue)
                const LoadingList(rows: 2, height: 180)
              else if (coursesAsync.hasError && courses.isEmpty)
                ErrorPanel(
                  message: coursesAsync.error is ApiException
                      ? (coursesAsync.error! as ApiException).userMessage
                      : '课程暂时无法加载',
                  compact: true,
                  onRetry: () => ref.invalidate(coursesControllerProvider),
                )
              else if (courses.isEmpty)
                EmptyState(
                  title: '还没有同步课程',
                  message: '课程同步完成后，章节和任务点会显示在这里。',
                  compact: true,
                )
              else
                _CourseGrid(
                  courses: courses.take(3).toList(growable: false),
                  onOpen: (course) => context.go('/courses/${course.id}'),
                ),
              const SizedBox(height: 30),
              LayoutBuilder(builder: (context, constraints) {
                final plansCard = _PlansCard(
                  async: plansAsync,
                  onOpen: () => openTab(context, 2, '/plans'),
                  onRetry: () => ref.invalidate(plansControllerProvider),
                );
                final recentCard = _RecentCard(
                  async: tasksAsync,
                  tasks: recent,
                  onOpen: () => openTab(context, 2, '/plans'),
                  onRetry: () => ref.invalidate(homeTasksProvider),
                );
                if (constraints.maxWidth < 900) {
                  return Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      plansCard,
                      const SizedBox(height: 18),
                      recentCard,
                    ],
                  );
                }
                return Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(flex: 11, child: plansCard),
                    const SizedBox(width: 18),
                    Expanded(flex: 9, child: recentCard),
                  ],
                );
              }),
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
            const SizedBox(width: 7),
            Expanded(
              child: Text(
                connected ? '$school · 学习通已连接' : '$school · 需要重新验证',
                style: TextStyle(
                  color: colors.textSecondary,
                  fontSize: 12,
                  letterSpacing: 0.2,
                ),
              ),
            ),
            Text(
              dateText,
              style: TextStyle(color: colors.muted, fontSize: 12),
            ),
          ],
        ),
        const SizedBox(height: 14),
        Text(
          '$greeting，$name。',
          style: TextStyle(
            color: colors.text,
            fontSize: 28,
            fontWeight: FontWeight.w600,
            letterSpacing: -0.9,
            height: 1.22,
          ),
        ),
        const SizedBox(height: 6),
        Text(
          '平台确认的进度，会同步到你的学习空间。',
          style: TextStyle(
            color: colors.textSecondary,
            fontSize: 13.5,
            height: 1.6,
          ),
        ),
      ],
    );
  }
}

class _LearningOverview extends StatelessWidget {
  const _LearningOverview({
    required this.course,
    required this.courseCount,
    required this.pendingPoints,
    required this.loading,
    required this.syncing,
    required this.error,
    required this.onOpenCourse,
    required this.onOpenCourses,
    required this.onRetry,
  });

  final CourseSummary? course;
  final int? courseCount;
  final int pendingPoints;
  final bool loading;
  final bool syncing;
  final bool error;
  final VoidCallback? onOpenCourse;
  final VoidCallback onOpenCourses;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [colors.accentSoft, colors.surface],
        ),
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      clipBehavior: Clip.antiAlias,
      child: LayoutBuilder(builder: (context, constraints) {
        final wide = constraints.maxWidth >= 640;
        final content = Padding(
          padding: EdgeInsets.all(wide ? 26 : 20),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisAlignment: MainAxisAlignment.start,
            children: [
              Row(
                children: [
                  Icon(Icons.play_circle_outline,
                      size: 16, color: colors.accent),
                  const SizedBox(width: 7),
                  Text(
                    '继续学习',
                    style: TextStyle(
                      color: colors.accent,
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                      letterSpacing: 1.6,
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 16),
              if (loading && course == null)
                const SkeletonBlock(width: 240, height: 32)
              else if (error && course == null)
                Text('课程摘要暂时无法读取',
                    style: TextStyle(color: colors.textSecondary, fontSize: 14))
              else if (course == null)
                Text(
                  syncing ? '正在同步课程' : '从同步课程开始',
                  style: TextStyle(
                    color: colors.text,
                    fontSize: wide ? 26 : 22,
                    fontWeight: FontWeight.w600,
                    letterSpacing: -0.6,
                  ),
                )
              else
                Text(
                  course!.title,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    color: colors.text,
                    fontSize: wide ? 26 : 22,
                    fontWeight: FontWeight.w600,
                    letterSpacing: -0.6,
                    height: 1.3,
                  ),
                ),
              const SizedBox(height: 10),
              Text(
                course == null
                    ? '同步学习通课程，按章节查看任务点与平台确认进度。'
                    : [
                        if (course!.term != null) course!.term!,
                        if (course!.teacher != null) course!.teacher!,
                        '${course!.chapterCount} 章节',
                      ].join(' · '),
                style: TextStyle(
                  color: colors.textSecondary,
                  fontSize: 13,
                  height: 1.6,
                ),
              ),
              if (course != null) ...[
                const SizedBox(height: 20),
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        '平台已确认 ${course!.confirmedTaskPoints} / ${course!.taskPointCount} 个任务点',
                        style: TextStyle(
                          color: colors.text,
                          fontSize: 13,
                          fontWeight: FontWeight.w500,
                        ),
                      ),
                    ),
                    Text(
                      course!.taskPointCount == 0
                          ? '—'
                          : '${((course!.confirmedTaskPoints / course!.taskPointCount) * 100).round()}%',
                      style: TextStyle(
                        color: colors.textSecondary,
                        fontSize: 12,
                        fontFeatures: const [FontFeature.tabularFigures()],
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                ClipRRect(
                  borderRadius: BorderRadius.circular(4),
                  child: LinearProgressIndicator(
                    value: course!.taskPointCount == 0
                        ? 0
                        : course!.confirmedTaskPoints / course!.taskPointCount,
                    minHeight: 6,
                    backgroundColor: colors.border,
                    valueColor: AlwaysStoppedAnimation<Color>(
                      course!.allConfirmed ? colors.success : colors.accent,
                    ),
                  ),
                ),
              ],
              const SizedBox(height: 22),
              Wrap(
                spacing: 10,
                runSpacing: 10,
                children: [
                  if (onOpenCourse != null)
                    FilledButton(
                      onPressed: onOpenCourse,
                      child: const Text('查看课程'),
                    ),
                  OutlinedButton(
                    onPressed: error ? onRetry : onOpenCourses,
                    child: Text(error ? '重新加载' : '课程列表'),
                  ),
                  if (courseCount != null && courseCount! > 1)
                    Text(
                      '已同步 $courseCount 门课程',
                      style: TextStyle(
                        color: colors.textSecondary,
                        fontSize: 12,
                      ),
                    ),
                ],
              ),
            ],
          ),
        );
        if (!wide) return content;
        return Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(child: content),
            SizedBox(
              width: 230,
              child: CourseArt(
                seed: course?.id ?? 'empty',
                height: 280,
                background: false,
              ),
            ),
          ],
        );
      }),
    );
  }
}

class _WalletCard extends StatelessWidget {
  const _WalletCard({
    required this.loading,
    required this.error,
    required this.wallet,
    required this.onRetry,
    required this.onRecharge,
    required this.onDetails,
  });

  final bool loading;
  final bool error;
  final WalletSnapshot? wallet;
  final VoidCallback onRetry;
  final VoidCallback onRecharge;
  final VoidCallback onDetails;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      padding: const EdgeInsets.all(22),
      decoration: BoxDecoration(
        color: colors.surface,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(
                width: 30,
                height: 30,
                decoration: BoxDecoration(
                  color: colors.accentSoft,
                  borderRadius: BorderRadius.circular(8),
                ),
                child: Icon(Icons.account_balance_wallet_outlined,
                    size: 16, color: colors.accent),
              ),
              const SizedBox(width: 9),
              Text(
                '我的钱包',
                style: TextStyle(
                  color: colors.text,
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const Spacer(),
              TextButton(
                onPressed: onDetails,
                style: TextButton.styleFrom(
                  padding: const EdgeInsets.symmetric(horizontal: 6),
                  minimumSize: const Size(0, 32),
                ),
                child: const Text('明细'),
              ),
            ],
          ),
          const SizedBox(height: 18),
          if (loading && wallet == null)
            const SkeletonBlock(width: 140, height: 34)
          else if (error)
            TextButton(
              onPressed: onRetry,
              child: const Text('余额读取失败，重试'),
            )
          else
            Text(
              wallet?.available.text ?? '—',
              style: TextStyle(
                color: colors.text,
                fontSize: 32,
                fontWeight: FontWeight.w600,
                letterSpacing: -1,
                fontFeatures: const [FontFeature.tabularFigures()],
              ),
            ),
          const SizedBox(height: 4),
          Text(
            '预占 ${wallet?.reserved.text ?? '—'} · 余额以账户流水为准',
            style: TextStyle(
              color: colors.textSecondary,
              fontSize: 12,
              height: 1.5,
            ),
          ),
          const SizedBox(height: 20),
          Row(
            children: [
              Expanded(
                child: FilledButton(
                  onPressed: onRecharge,
                  child: const Text('充值'),
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: OutlinedButton(
                  onPressed: onDetails,
                  child: const Text('查看明细'),
                ),
              ),
            ],
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

  final int? courseCount, pendingPoints, enabledPlans, confirmedTasks;
  final VoidCallback onCourses, onPlans;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final items = [
      (Icons.menu_book_outlined, courseCount, '已同步课程', onCourses),
      (Icons.pending_actions_outlined, pendingPoints, '待确认', onCourses),
      (Icons.calendar_month_outlined, enabledPlans, '开启安排', onPlans),
      (Icons.verified_outlined, confirmedTasks, '近期确认', onPlans),
    ];
    return LayoutBuilder(builder: (context, constraints) {
      final narrow = constraints.maxWidth < 640;
      Widget item((IconData, int?, String, VoidCallback) data,
          {required bool last}) {
        return Expanded(
          child: InkWell(
            onTap: data.$4,
            borderRadius: BorderRadius.circular(8),
            child: Padding(
              padding: EdgeInsets.symmetric(
                vertical: narrow ? 12 : 16,
                horizontal: narrow ? 10 : 12,
              ),
              child: Row(
                children: [
                  Container(
                    width: 32,
                    height: 32,
                    decoration: BoxDecoration(
                      color: colors.surfaceSubtle,
                      borderRadius: BorderRadius.circular(8),
                      border: Border.all(color: colors.border),
                    ),
                    child: Icon(data.$1, size: 16, color: colors.textSecondary),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          data.$2?.toString() ?? '—',
                          style: TextStyle(
                            color: colors.text,
                            fontSize: 22,
                            fontWeight: FontWeight.w600,
                            height: 1.2,
                            fontFeatures: const [FontFeature.tabularFigures()],
                          ),
                        ),
                        const SizedBox(height: 2),
                        Text(
                          data.$3,
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
                  if (!narrow)
                    Icon(Icons.chevron_right, size: 16, color: colors.muted),
                ],
              ),
            ),
          ),
        );
      }

      if (narrow) {
        return Column(
          children: [
            Row(children: [
              item(items[0], last: false),
              VerticalDivider(width: 1, color: colors.border),
              item(items[1], last: true),
            ]),
            Divider(height: 1, color: colors.border),
            Row(children: [
              item(items[2], last: false),
              VerticalDivider(width: 1, color: colors.border),
              item(items[3], last: true),
            ]),
          ],
        );
      }
      return Container(
        decoration: BoxDecoration(
          border: Border(
            top: BorderSide(color: colors.border),
            bottom: BorderSide(color: colors.border),
          ),
        ),
        child: Row(
          children: [
            for (var index = 0; index < items.length; index++) ...[
              if (index > 0) VerticalDivider(width: 1, color: colors.border),
              item(items[index], last: index == items.length - 1),
            ],
          ],
        ),
      );
    });
  }
}

class _SectionHeading extends StatelessWidget {
  const _SectionHeading({
    required this.title,
    this.subtitle,
    required this.action,
  });

  final String title;
  final String? subtitle;
  final Widget action;

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
                  letterSpacing: -0.3,
                ),
              ),
              if (subtitle != null) ...[
                const SizedBox(height: 4),
                Text(
                  subtitle!,
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 12.5,
                  ),
                ),
              ],
            ],
          ),
        ),
        action,
      ],
    );
  }
}

class _CourseGrid extends StatelessWidget {
  const _CourseGrid({required this.courses, required this.onOpen});

  final List<CourseSummary> courses;
  final ValueChanged<CourseSummary> onOpen;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(builder: (context, constraints) {
      final columns = constraints.maxWidth >= 1000
          ? 3
          : constraints.maxWidth >= 640
              ? 2
              : 1;
      const gap = 16.0;
      final width = (constraints.maxWidth - gap * (columns - 1)) / columns;
      return Wrap(
        spacing: gap,
        runSpacing: gap,
        children: [
          for (final course in courses)
            SizedBox(
              width: width,
              child: _CourseCard(
                course: course,
                onTap: () => onOpen(course),
              ),
            ),
        ],
      );
    });
  }
}

class _CourseCard extends StatelessWidget {
  const _CourseCard({required this.course, required this.onTap});

  final CourseSummary course;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final ratio = course.taskPointCount == 0
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
            CourseArt(seed: course.id, height: 118),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          [
                            if (course.term != null) course.term!,
                            '${course.chapterCount} 章节',
                          ].join(' · '),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: colors.muted,
                            fontSize: 11.5,
                            letterSpacing: 0.4,
                          ),
                        ),
                      ),
                      if (course.stale)
                        const StatusPill(
                          label: '待同步',
                          tone: StatusTone.warning,
                          compact: true,
                        ),
                    ],
                  ),
                  const SizedBox(height: 7),
                  SizedBox(
                    height: 42,
                    child: Text(
                      course.title,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: colors.text,
                        fontSize: 15,
                        fontWeight: FontWeight.w600,
                        height: 1.4,
                      ),
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    course.teacher ?? '教师待同步',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 12,
                    ),
                  ),
                  const SizedBox(height: 14),
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          '平台已确认 ${course.confirmedTaskPoints} / ${course.taskPointCount}',
                          style: TextStyle(
                            color: colors.textSecondary,
                            fontSize: 12,
                          ),
                        ),
                      ),
                      Text(
                        '${(ratio * 100).round()}%',
                        style: TextStyle(
                          color: colors.textSecondary,
                          fontSize: 11.5,
                          fontFeatures: const [FontFeature.tabularFigures()],
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 6),
                  ClipRRect(
                    borderRadius: BorderRadius.circular(3),
                    child: LinearProgressIndicator(
                      value: ratio,
                      minHeight: 5,
                      backgroundColor: colors.border,
                      valueColor: AlwaysStoppedAnimation<Color>(
                        course.allConfirmed ? colors.success : colors.accent,
                      ),
                    ),
                  ),
                  const SizedBox(height: 12),
                  Row(
                    children: [
                      Icon(
                        course.allConfirmed
                            ? Icons.verified_outlined
                            : Icons.schedule_outlined,
                        size: 15,
                        color: course.allConfirmed
                            ? colors.success
                            : colors.textSecondary,
                      ),
                      const SizedBox(width: 6),
                      Expanded(
                        child: Text(
                          course.allConfirmed
                              ? '任务点已全部确认'
                              : '${course.incompleteTaskPointCount} 个任务点待完成',
                          style: TextStyle(
                            color: course.allConfirmed
                                ? colors.success
                                : colors.textSecondary,
                            fontSize: 12,
                          ),
                        ),
                      ),
                      Icon(Icons.arrow_forward, size: 15, color: colors.muted),
                    ],
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _PlansCard extends StatelessWidget {
  const _PlansCard({
    required this.async,
    required this.onOpen,
    required this.onRetry,
  });

  final AsyncValue<PlansState> async;
  final VoidCallback onOpen, onRetry;

  @override
  Widget build(BuildContext context) {
    return SectionCard(
      title: '你的学习安排',
      action: TextButton(onPressed: onOpen, child: const Text('管理安排')),
      child: async.when(
        loading: () => const LoadingList(rows: 2, height: 60),
        error: (error, _) => ErrorPanel(
          message: error is ApiException ? error.userMessage : '安排暂时无法加载',
          compact: true,
          onRetry: onRetry,
        ),
        data: (data) => data.plans.isEmpty
            ? _InlineEmpty(
                message: '还没有任务安排',
                action: TextButton(
                  onPressed: onOpen,
                  child: const Text('新建安排'),
                ),
              )
            : Column(
                children: [
                  for (final plan in data.plans.take(3))
                    _HomePlanRow(plan: plan, onTap: onOpen),
                ],
              ),
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
      borderRadius: BorderRadius.circular(8),
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

class _RecentCard extends StatelessWidget {
  const _RecentCard({
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
      action: TextButton(onPressed: onOpen, child: const Text('查看全部')),
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
                  borderRadius: BorderRadius.circular(8),
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
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
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
        padding: const EdgeInsets.symmetric(vertical: 18),
        child: Row(
          children: [
            Expanded(
              child: Text(
                message,
                style: TextStyle(
                  color: context.colors.textSecondary,
                  fontSize: 13,
                ),
              ),
            ),
            if (action != null) action!,
          ],
        ),
      );
}
