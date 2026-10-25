import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/models/course.dart';
import '../../app/scroll_controllers.dart';
import '../../design/components.dart';
import '../../design/theme.dart';
import 'course_sync_notice.dart';
import 'courses_controller.dart';

enum _CourseFilter { all, pending, confirmed }

class CoursesPage extends ConsumerStatefulWidget {
  const CoursesPage({super.key});

  @override
  ConsumerState<CoursesPage> createState() => _CoursesPageState();
}

class _CoursesPageState extends ConsumerState<CoursesPage> {
  final _searchController = TextEditingController();
  Timer? _debounce;
  _CourseFilter _filter = _CourseFilter.all;

  @override
  void dispose() {
    _debounce?.cancel();
    _searchController.dispose();
    super.dispose();
  }

  void _onSearchChanged(String value) {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 380), () {
      ref.read(coursesControllerProvider.notifier).setSearch(value);
    });
  }

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(coursesControllerProvider);
    final user = ref.watch(currentUserProvider);
    final capabilities = ref.watch(authCapabilitiesProvider);
    final scroll = ref.watch(tabScrollControllerProvider('courses'));
    final syncState = async.valueOrNull;
    final syncActive = syncState != null &&
        (syncState.syncing ||
            syncState.syncError != null ||
            (syncState.syncResult?.needsAttention ?? false));
    if (async.hasValue) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (context.mounted) {
          ref.read(coursesControllerProvider.notifier).autoSyncIfNeeded();
        }
      });
    }
    return SingleChildScrollView(
      key: const PageStorageKey('courses-scroll'),
      controller: scroll,
      primary: false,
      padding: EdgeInsets.only(bottom: context.isNarrow ? 12 : 28),
      child: ResponsiveContent(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            PageHeader(
              title: '课程与任务点',
            ),
            if (syncActive) ...[
              CourseSyncNotice(
                state: syncState,
                onRetry: () =>
                    ref.read(coursesControllerProvider.notifier).syncCourses(),
              ),
              const SizedBox(height: 16),
            ],
            if (!capabilities.courseSyncAvailable)
              const Padding(
                padding: EdgeInsets.only(bottom: 16),
                child: InlineNotice(
                  icon: Icons.info_outline,
                  message: '课程同步当前未开放，已有课程仍可查看。',
                ),
              ),
            if (user != null)
              Padding(
                padding: const EdgeInsets.only(bottom: 14),
                child: Row(
                  children: [
                    Icon(
                      Icons.link,
                      size: 15,
                      color: context.colors.muted,
                    ),
                    const SizedBox(width: 6),
                    Expanded(
                      child: Text(
                        '学习通身份 · ${user.displayLabel} · ${user.schoolLabel}',
                        style: TextStyle(
                          color: context.colors.textSecondary,
                          fontSize: 13,
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            async.when(
              loading: () => const LoadingList(rows: 3, height: 260),
              error: (error, _) => ErrorPanel(
                message: error is ApiException ? error.userMessage : '课程暂时无法加载',
                requestId: error is ApiException ? error.requestId : null,
                onRetry: () => ref.invalidate(coursesControllerProvider),
              ),
              data: (data) => _buildData(context, data),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildData(BuildContext context, CourseListState data) {
    final filtered = data.courses.where((course) {
      return switch (_filter) {
        _CourseFilter.all => true,
        _CourseFilter.pending => course.incompleteTaskPointCount > 0,
        _CourseFilter.confirmed => course.allConfirmed,
      };
    }).toList(growable: false);
    final pending = data.courses
        .where((course) => course.incompleteTaskPointCount > 0)
        .length;
    final confirmed =
        data.courses.where((course) => course.allConfirmed).length;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (data.syncResult != null || data.syncError != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 16),
            child: _SyncBanner(
              result: data.syncResult,
              error: data.syncError,
              onClose: () =>
                  ref.read(coursesControllerProvider.notifier).clearBanner(),
            ),
          ),
        if (data.refreshError != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 16),
            child: InlineNotice(
              tone: StatusTone.warning,
              icon: Icons.cloud_off_outlined,
              message: data.refreshError!,
              action: TextButton(
                onPressed: () => ref
                    .read(coursesControllerProvider.notifier)
                    .refresh(silent: true),
                child: const Text('重试'),
              ),
            ),
          ),
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
          decoration: BoxDecoration(
            color: context.colors.surface,
            borderRadius: context.colors.cardRadius,
            border: Border.all(color: context.colors.border),
          ),
          child: LayoutBuilder(
            builder: (context, constraints) {
              final narrow = constraints.maxWidth < 720;
              final search = TextField(
                controller: _searchController,
                onChanged: _onSearchChanged,
                textInputAction: TextInputAction.search,
                decoration: InputDecoration(
                  hintText: '搜索课程或教师',
                  prefixIcon: const Icon(Icons.search, size: 19),
                  isDense: true,
                  filled: false,
                  border: InputBorder.none,
                  enabledBorder: InputBorder.none,
                  focusedBorder: InputBorder.none,
                  contentPadding:
                      const EdgeInsets.symmetric(vertical: 12, horizontal: 4),
                  suffixIcon: _searchController.text.isEmpty
                      ? null
                      : IconButton(
                          tooltip: '清除搜索',
                          onPressed: () {
                            _searchController.clear();
                            ref
                                .read(coursesControllerProvider.notifier)
                                .setSearch('');
                            setState(() {});
                          },
                          icon: const Icon(Icons.close, size: 17),
                        ),
                ),
              );
              final filters = Wrap(
                spacing: 6,
                runSpacing: 6,
                children: [
                  _FilterChip(
                    label: '全部 ${data.courses.length}',
                    selected: _filter == _CourseFilter.all,
                    onTap: () => setState(() => _filter = _CourseFilter.all),
                  ),
                  _FilterChip(
                    label: '待完成 $pending',
                    selected: _filter == _CourseFilter.pending,
                    onTap: () =>
                        setState(() => _filter = _CourseFilter.pending),
                  ),
                  _FilterChip(
                    label: '已确认 $confirmed',
                    selected: _filter == _CourseFilter.confirmed,
                    onTap: () =>
                        setState(() => _filter = _CourseFilter.confirmed),
                  ),
                ],
              );
              if (narrow) {
                return Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    search,
                    const SizedBox(height: 6),
                    filters,
                  ],
                );
              }
              return Row(
                children: [
                  Expanded(flex: 3, child: search),
                  const SizedBox(width: 14),
                  filters,
                ],
              );
            },
          ),
        ),
        const SizedBox(height: 18),
        if (filtered.isEmpty)
          SectionCard(
            child: EmptyState(
              icon: data.courses.isEmpty
                  ? Icons.sync_outlined
                  : Icons.search_off_outlined,
              title: data.courses.isEmpty ? '还没有同步课程' : '没有找到符合条件的课程',
              message: data.courses.isEmpty
                  ? '课程同步完成后，章节与任务点会显示在这里。'
                  : '调整搜索词或筛选条件后再试。',
              action: data.courses.isEmpty
                  ? AsyncButton(
                      label: '同步课程',
                      icon: Icons.sync,
                      busy: data.syncing,
                      onPressed: () => ref
                          .read(coursesControllerProvider.notifier)
                          .syncCourses(),
                    )
                  : OutlinedButton(
                      onPressed: () {
                        _searchController.clear();
                        setState(() => _filter = _CourseFilter.all);
                        ref
                            .read(coursesControllerProvider.notifier)
                            .setSearch('');
                      },
                      child: const Text('查看全部课程'),
                    ),
            ),
          )
        else
          LayoutBuilder(
            builder: (context, constraints) {
              final columns = constraints.maxWidth >= 1060
                  ? 3
                  : constraints.maxWidth >= 700
                      ? 2
                      : 1;
              final spacing = 14.0;
              final width =
                  (constraints.maxWidth - spacing * (columns - 1)) / columns;
              return Wrap(
                spacing: spacing,
                runSpacing: spacing,
                children: [
                  for (final course in filtered)
                    SizedBox(
                      width: width,
                      child: _CourseCard(
                        course: course,
                        onTap: () => context.go('/courses/${course.id}'),
                      ),
                    ),
                ],
              );
            },
          ),
        if (data.nextCursor != null)
          Padding(
            padding: const EdgeInsets.only(top: 16),
            child: Center(
              child: TextButton(
                onPressed: data.refreshing
                    ? null
                    : () =>
                        ref.read(coursesControllerProvider.notifier).loadMore(),
                child: Text(data.refreshing ? '正在加载' : '加载更多课程'),
              ),
            ),
          ),
        const SizedBox(height: 18),
        const InlineNotice(
          icon: Icons.fact_check_outlined,
          message: '完成数量来自学习通平台返回。普通附件标注为学习资料，不计入任务点。',
        ),
      ],
    );
  }
}

class _FilterChip extends StatelessWidget {
  const _FilterChip({
    required this.label,
    required this.selected,
    required this.onTap,
  });

  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(999),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 9),
        decoration: BoxDecoration(
          color: selected ? colors.accentSoft : Colors.transparent,
          borderRadius: BorderRadius.circular(999),
          border: Border.all(
            color:
                selected ? colors.accent.withValues(alpha: 0.2) : colors.border,
          ),
        ),
        child: Text(
          label,
          style: TextStyle(
            color: selected ? colors.accent : colors.textSecondary,
            fontSize: 13,
            fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
          ),
        ),
      ),
    );
  }
}

class _SyncBanner extends StatelessWidget {
  const _SyncBanner({
    this.result,
    this.error,
    required this.onClose,
  });

  final CourseSyncResult? result;
  final String? error;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    if (error != null) {
      return InlineNotice(
        tone: StatusTone.danger,
        message: error!,
        action: IconButton(
          tooltip: '关闭',
          onPressed: onClose,
          icon: const Icon(Icons.close, size: 17),
        ),
      );
    }
    final value = result!;
    if (value.failed) {
      return InlineNotice(
        tone: StatusTone.warning,
        message: '课程同步未完成，请稍后重试。',
        action: TextButton(onPressed: onClose, child: const Text('关闭')),
      );
    }
    final message = value.accepted
        ? '同步请求已提交，课程更新完成后会在这里显示。'
        : '课程同步已处理${value.courseCount > 0 ? '，共 ${value.courseCount} 门课程' : ''}。';
    return InlineNotice(
      tone: StatusTone.success,
      icon: Icons.cloud_done_outlined,
      message: message,
      action: TextButton(onPressed: onClose, child: const Text('知道了')),
    );
  }
}

class _CourseCard extends StatelessWidget {
  const _CourseCard({required this.course, required this.onTap});

  final CourseSummary course;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final progress = course.taskPointCount == 0
        ? 0.0
        : course.confirmedTaskPoints / course.taskPointCount;
    return Semantics(
      button: true,
      label:
          '${course.title}，平台已确认 ${course.confirmedTaskPoints} 个，待完成 ${course.incompleteTaskPointCount} 个',
      child: InkWell(
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
              CourseArt(seed: course.id),
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
                              course.term,
                              '${course.chapterCount} 章节',
                            ].whereType<String>().join(' · '),
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              color: colors.textSecondary,
                              fontSize: 12,
                            ),
                          ),
                        ),
                        if (course.stale)
                          const StatusPill(
                            label: '待同步',
                            tone: StatusTone.warning,
                            compact: true,
                          )
                        else if (course.allConfirmed)
                          const StatusPill(
                            label: '全部确认',
                            tone: StatusTone.success,
                            compact: true,
                            icon: Icons.check,
                          ),
                      ],
                    ),
                    const SizedBox(height: 8),
                    Text(
                      course.title,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: colors.text,
                        fontSize: 16,
                        fontWeight: FontWeight.w600,
                        height: 1.35,
                      ),
                    ),
                    const SizedBox(height: 5),
                    Text(
                      course.teacher ?? '教师待同步',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: colors.textSecondary,
                        fontSize: 13,
                      ),
                    ),
                    const SizedBox(height: 14),
                    Row(
                      children: [
                        Text(
                          '平台已确认',
                          style: TextStyle(
                            color: colors.textSecondary,
                            fontSize: 12,
                          ),
                        ),
                        const Spacer(),
                        Text(
                          '${course.confirmedTaskPoints}',
                          style: TextStyle(
                            color: colors.text,
                            fontSize: 16,
                            fontWeight: FontWeight.w600,
                            fontFeatures: const [FontFeature.tabularFigures()],
                          ),
                        ),
                        Text(
                          ' / ${course.taskPointCount}',
                          style: TextStyle(
                            color: colors.muted,
                            fontSize: 13,
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: 7),
                    ProgressBar(value: progress),
                    const SizedBox(height: 11),
                    Row(
                      children: [
                        Expanded(
                          child: Text(
                            course.taskPointCount == 0
                                ? '课程暂无任务点'
                                : course.incompleteTaskPointCount > 0
                                    ? '${course.incompleteTaskPointCount} 个任务点待完成'
                                    : '任务点已全部确认',
                            style: TextStyle(
                              color: course.incompleteTaskPointCount > 0
                                  ? colors.textSecondary
                                  : colors.success,
                              fontSize: 12,
                            ),
                          ),
                        ),
                        Icon(
                          Icons.arrow_outward,
                          size: 16,
                          color: colors.textSecondary,
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
