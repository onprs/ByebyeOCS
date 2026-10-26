import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/api/api_error.dart';
import '../../core/format/formatters.dart';
import '../../core/models/course.dart';
import '../../core/platform/external_link.dart';
import '../../design/components.dart';
import '../../design/theme.dart';
import 'course_cover.dart';
import 'courses_controller.dart';

enum _CourseTab { chapters, resources }

class CourseDetailPage extends ConsumerStatefulWidget {
  const CourseDetailPage({super.key, required this.courseId});

  final String courseId;

  @override
  ConsumerState<CourseDetailPage> createState() => _CourseDetailPageState();
}

class _CourseDetailPageState extends ConsumerState<CourseDetailPage> {
  _CourseTab _tab = _CourseTab.chapters;

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(courseDetailProvider(widget.courseId));
    return SingleChildScrollView(
      padding: EdgeInsets.only(bottom: context.isNarrow ? 12 : 28),
      child: ResponsiveContent(
        child: async.when(
          loading: () => const LoadingList(rows: 3, height: 180),
          error: (error, _) => Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              TextButton.icon(
                onPressed: () => context.go('/courses'),
                icon: const Icon(Icons.arrow_back, size: 16),
                label: const Text('返回课程'),
              ),
              const SizedBox(height: 16),
              ErrorPanel(
                message:
                    error is ApiException ? error.userMessage : '课程详情暂时无法加载',
                requestId: error is ApiException ? error.requestId : null,
                onRetry: () =>
                    ref.invalidate(courseDetailProvider(widget.courseId)),
              ),
            ],
          ),
          data: (detail) => _buildDetail(context, detail),
        ),
      ),
    );
  }

  Widget _buildDetail(BuildContext context, CourseDetail detail) {
    final colors = context.colors;
    final course = detail.summary;
    final progress = course.taskPointCount == 0
        ? 0.0
        : course.confirmedTaskPoints / course.taskPointCount;
    final allResources =
        detail.chapters.expand((chapter) => chapter.resources).toList();
    final body = Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            TextButton.icon(
              onPressed: () => context.go('/courses'),
              icon: const Icon(Icons.arrow_back, size: 16),
              label: const Text('我的课程'),
            ),
            const Spacer(),
            IconButton(
              tooltip: '刷新课程',
              onPressed: () =>
                  ref.invalidate(courseDetailProvider(widget.courseId)),
              icon: const Icon(Icons.refresh, size: 18),
            ),
          ],
        ),
        const SizedBox(height: 4),
        Container(
          decoration: BoxDecoration(
            color: colors.surface,
            borderRadius: colors.cardRadius,
            border: Border.all(color: colors.border),
          ),
          clipBehavior: Clip.antiAlias,
          child: LayoutBuilder(
            builder: (context, constraints) {
              final compact = constraints.maxWidth < 680;
              final cover = CourseCover(
                path: course.coverUrl,
                seed: course.id,
                height: compact ? 108 : 140,
              );
              final summary = Padding(
                padding: EdgeInsets.all(context.isNarrow ? 18 : 24),
                child: LayoutBuilder(
                  builder: (context, constraints) {
                    final narrow = constraints.maxWidth < 650;
                    final titleBlock = Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          [
                            course.term,
                            course.detailsPending
                                ? '章节待同步'
                                : '${course.chapterCount} 章节',
                            if (!course.detailsPending)
                              '${course.taskPointCount} 个任务点',
                          ].whereType<String>().join(' · '),
                          style: TextStyle(
                            color: colors.textSecondary,
                            fontSize: 12,
                          ),
                        ),
                        const SizedBox(height: 8),
                        Text(
                          course.title,
                          style: TextStyle(
                            color: colors.text,
                            fontSize: narrow ? 26 : AppTypography.fontTitle,
                            fontWeight: FontWeight.w600,
                            letterSpacing: -0.7,
                            height: 1.25,
                          ),
                        ),
                        const SizedBox(height: 8),
                        Text(
                          '${course.teacher ?? '教师待同步'} · 课程快照${course.stale ? '待同步' : '已更新'}',
                          style: TextStyle(
                            color: colors.textSecondary,
                            fontSize: 13,
                          ),
                        ),
                      ],
                    );
                    final actions = Wrap(
                      spacing: 10,
                      runSpacing: 10,
                      children: [
                        AsyncButton(
                          label: '安排这门课程',
                          icon: Icons.add_task,
                          onPressed: () =>
                              context.go('/plans?course=${course.id}'),
                        ),
                      ],
                    );
                    if (narrow) {
                      return Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          titleBlock,
                          const SizedBox(height: 16),
                          actions,
                        ],
                      );
                    }
                    return Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Expanded(child: titleBlock),
                        const SizedBox(width: 20),
                        actions,
                      ],
                    );
                  },
                ),
              );
              if (compact) {
                return Column(children: [cover, summary]);
              }
              return Row(
                crossAxisAlignment: CrossAxisAlignment.center,
                children: [
                  Padding(
                    padding: const EdgeInsets.fromLTRB(24, 24, 0, 24),
                    child: ClipRRect(
                      borderRadius: BorderRadius.circular(10),
                      child: SizedBox(width: 224, child: cover),
                    ),
                  ),
                  Expanded(child: summary),
                ],
              );
            },
          ),
        ),
        const SizedBox(height: 16),
        if (context.isNarrow) ...[
          _CourseSummaryCard(course: course, progress: progress),
          const SizedBox(height: 16),
        ],
        LayoutBuilder(
          builder: (context, constraints) {
            final wide = constraints.maxWidth >= 920;
            final chapterCard = SectionCard(
              padding: const EdgeInsets.fromLTRB(16, 14, 16, 16),
              title: '课程内容',
              action: SegmentedButton<_CourseTab>(
                segments: const [
                  ButtonSegment(
                    value: _CourseTab.chapters,
                    label: Text('章节'),
                  ),
                  ButtonSegment(
                    value: _CourseTab.resources,
                    label: Text('学习资料'),
                  ),
                ],
                selected: {_tab},
                showSelectedIcon: false,
                onSelectionChanged: (value) {
                  setState(() => _tab = value.first);
                },
              ),
              child: course.detailsPending
                  ? const InlineNotice(message: '课程内容待同步，同步完成后自动更新。')
                  : _tab == _CourseTab.chapters
                      ? _buildChapters(context, detail.chapters)
                      : _buildResources(context, allResources),
            );
            if (!wide) return chapterCard;
            return Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(child: chapterCard),
                const SizedBox(width: 16),
                SizedBox(
                  width: 310,
                  child: _CourseSummaryCard(course: course, progress: progress),
                ),
              ],
            );
          },
        ),
        const SizedBox(height: 16),
        const InlineNotice(
          icon: Icons.link,
          message: '课程内容以学习通为准。普通附件不计入未完成任务，平台确认数量会在同步后更新。',
        ),
      ],
    );
    return body;
  }

  Widget _buildChapters(
    BuildContext context,
    List<CourseChapter> chapters,
  ) {
    if (chapters.isEmpty) {
      return const EmptyState(
        icon: Icons.menu_book_outlined,
        title: '课程暂无章节',
        message: '同步课程后，章节与附件会显示在这里。',
        compact: true,
      );
    }
    return Column(
      children: [
        for (var index = 0; index < chapters.length; index++)
          _ChapterTile(
            chapter: chapters[index],
            initiallyExpanded: index == 0,
            onOpenResource: _openResource,
            onOpenTaskPoint: _openTaskPoint,
          ),
      ],
    );
  }

  Widget _buildResources(
    BuildContext context,
    List<CourseAttachment> resources,
  ) {
    if (resources.isEmpty) {
      return const EmptyState(
        icon: Icons.folder_open_outlined,
        title: '暂无普通学习资料',
        message: '任务点内容请在章节中查看。',
        compact: true,
      );
    }
    return Column(
      children: [
        for (final resource in resources)
          Material(
            color: Colors.transparent,
            child: ListTile(
              onTap: () => _openResource(resource),
              leading: Icon(
                Icons.description_outlined,
                color: context.colors.textSecondary,
              ),
              title: Text(resource.title),
              subtitle: const Text('学习资料 · 不计入任务点'),
              trailing: const Icon(Icons.open_in_new, size: 16),
            ),
          ),
      ],
    );
  }

  void _openResource(CourseAttachment attachment) {
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      builder: (sheetContext) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(22, 8, 22, 24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SheetHeader(
                title: attachment.title,
              ),
              const SizedBox(height: 18),
              Text(
                '学习资料需要在学习通官方课程中查看，平台不会将其计入未完成任务。',
                style: TextStyle(
                  color: sheetContext.colors.textSecondary,
                  height: 1.65,
                  fontSize: 14,
                ),
              ),
              const SizedBox(height: 20),
              Row(
                children: [
                  Expanded(
                    child: OutlinedButton(
                      onPressed: () => Navigator.of(sheetContext).pop(),
                      child: const Text('关闭'),
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: FilledButton.icon(
                      onPressed: () async {
                        final opened = await openExternalHttps(
                          'https://i.chaoxing.com/',
                        );
                        if (!sheetContext.mounted) return;
                        if (!opened) {
                          showAppMessage(sheetContext, '无法打开学习通');
                        }
                      },
                      icon: const Icon(Icons.open_in_new, size: 16),
                      label: const Text('打开学习通'),
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  void _openTaskPoint(CourseAttachment attachment) {
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      builder: (sheetContext) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(22, 8, 22, 24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SheetHeader(
                title: attachment.title,
                subtitle: '${attachment.kindLabel}任务点',
              ),
              const SizedBox(height: 16),
              _TaskPointFacts(attachment: attachment),
              const SizedBox(height: 16),
              Text(
                attachment.hasPlaybackEvidence
                    ? '已记录 ${formatDurationSeconds(attachment.playedSeconds)} 的播放信息。最终完成仍以平台确认结果为准。'
                    : '当前没有可用的真实播放记录。执行观察不会替代平台确认。',
                style: TextStyle(
                  color: sheetContext.colors.textSecondary,
                  height: 1.65,
                  fontSize: 14,
                ),
              ),
              const SizedBox(height: 20),
              SizedBox(
                width: double.infinity,
                child: OutlinedButton(
                  onPressed: () => Navigator.of(sheetContext).pop(),
                  child: const Text('关闭'),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _ChapterTile extends StatefulWidget {
  const _ChapterTile({
    required this.chapter,
    required this.initiallyExpanded,
    required this.onOpenResource,
    required this.onOpenTaskPoint,
  });

  final CourseChapter chapter;
  final bool initiallyExpanded;
  final ValueChanged<CourseAttachment> onOpenResource;
  final ValueChanged<CourseAttachment> onOpenTaskPoint;

  @override
  State<_ChapterTile> createState() => _ChapterTileState();
}

class _ChapterTileState extends State<_ChapterTile> {
  late bool _expanded = widget.initiallyExpanded;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final chapter = widget.chapter;
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      decoration: BoxDecoration(
        color: colors.surfaceSubtle,
        borderRadius: BorderRadius.circular(colors.radius - 2),
        border: Border.all(color: colors.border),
      ),
      child: Column(
        children: [
          InkWell(
            onTap: () => setState(() => _expanded = !_expanded),
            borderRadius: BorderRadius.circular(colors.radius - 2),
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 13, vertical: 13),
              child: Row(
                children: [
                  Icon(
                    _expanded ? Icons.expand_less : Icons.expand_more,
                    size: 18,
                    color: colors.textSecondary,
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      chapter.title,
                      style: TextStyle(
                        color: colors.text,
                        fontSize: 14,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Text(
                    chapter.taskPoints.isEmpty
                        ? '无任务点'
                        : '${chapter.confirmedCount} / ${chapter.taskPoints.length}',
                    style: TextStyle(
                      color: chapter.taskPoints.isNotEmpty &&
                              chapter.confirmedCount ==
                                  chapter.taskPoints.length
                          ? colors.success
                          : colors.textSecondary,
                      fontSize: 12,
                      fontFeatures: const [FontFeature.tabularFigures()],
                    ),
                  ),
                ],
              ),
            ),
          ),
          if (_expanded) ...[
            Divider(height: 1, color: colors.border),
            if (chapter.attachments.isEmpty)
              Padding(
                padding: const EdgeInsets.all(14),
                child: Align(
                  alignment: Alignment.centerLeft,
                  child: Text(
                    '本章没有内容条目',
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 13,
                    ),
                  ),
                ),
              )
            else
              for (final attachment in chapter.attachments)
                _AttachmentRow(
                  attachment: attachment,
                  onTap: attachment.isTaskPoint
                      ? () => widget.onOpenTaskPoint(attachment)
                      : () => widget.onOpenResource(attachment),
                ),
          ],
        ],
      ),
    );
  }
}

class _AttachmentRow extends StatelessWidget {
  const _AttachmentRow({required this.attachment, required this.onTap});

  final CourseAttachment attachment;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final confirmed = attachment.passedKnown && attachment.platformPassed;
    final status = !attachment.isTaskPoint
        ? const StatusPill(
            label: '学习资料',
            tone: StatusTone.neutral,
            compact: true,
          )
        : confirmed
            ? const StatusPill(
                label: '平台已确认',
                tone: StatusTone.success,
                compact: true,
                icon: Icons.check,
              )
            : attachment.countedIncomplete
                ? const StatusPill(
                    label: '待完成',
                    tone: StatusTone.warning,
                    compact: true,
                  )
                : const StatusPill(
                    label: '待确认',
                    tone: StatusTone.neutral,
                    compact: true,
                  );
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 11),
        child: Row(
          children: [
            Icon(
              switch (attachment.kind) {
                'video' => Icons.play_circle_outline,
                'document' => Icons.description_outlined,
                'audio' => Icons.headphones_outlined,
                'quiz' => Icons.quiz_outlined,
                _ => Icons.attach_file,
              },
              size: 18,
              color: attachment.isTaskPoint ? colors.accent : colors.muted,
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    attachment.title,
                    style: TextStyle(
                      color: colors.text,
                      fontSize: 14,
                      height: 1.4,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    attachment.isTaskPoint
                        ? '${attachment.kindLabel}任务点${attachment.durationSeconds != null && attachment.durationSeconds! > 0 ? ' · ${formatDurationSeconds(attachment.durationSeconds)}' : ''}'
                        : '普通附件 · 不计入任务点',
                    style: TextStyle(
                      color: colors.muted,
                      fontSize: 12,
                    ),
                  ),
                ],
              ),
            ),
            const SizedBox(width: 8),
            status,
          ],
        ),
      ),
    );
  }
}

class _TaskPointFacts extends StatelessWidget {
  const _TaskPointFacts({required this.attachment});

  final CourseAttachment attachment;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final confirmed = attachment.passedKnown && attachment.platformPassed;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: colors.surfaceSubtle,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      child: Column(
        children: [
          _fact(
            context,
            '平台确认',
            confirmed ? '已确认通过' : '尚未确认',
          ),
          const SizedBox(height: 8),
          _fact(
            context,
            '内容类型',
            attachment.kindLabel,
          ),
          const SizedBox(height: 8),
          _fact(
            context,
            '实际播放记录',
            attachment.hasPlaybackEvidence
                ? formatDurationSeconds(attachment.playedSeconds)
                : '无播放记录',
          ),
          if (attachment.progressCheckedAt != null) ...[
            const SizedBox(height: 8),
            _fact(
              context,
              '最近查询',
              formatDateTime(attachment.progressCheckedAt),
            ),
          ],
        ],
      ),
    );
  }

  Widget _fact(BuildContext context, String label, String value) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(
          width: 104,
          child: Text(
            label,
            style: TextStyle(
              color: context.colors.textSecondary,
              fontSize: 12,
            ),
          ),
        ),
        Expanded(
          child: Text(
            value,
            style: TextStyle(
              color: context.colors.text,
              fontSize: 13,
              fontWeight: FontWeight.w500,
            ),
          ),
        ),
      ],
    );
  }
}

class _CourseSummaryCard extends StatelessWidget {
  const _CourseSummaryCard({required this.course, required this.progress});

  final CourseSummary course;
  final double progress;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return SectionCard(
      title: '课程进度',
      child: Column(
        children: [
          SizedBox(
            width: 118,
            height: 118,
            child: Stack(
              alignment: Alignment.center,
              children: [
                SizedBox(
                  width: 118,
                  height: 118,
                  child: CircularProgressIndicator(
                    value: progress,
                    strokeWidth: 7,
                    backgroundColor: colors.border,
                    valueColor: AlwaysStoppedAnimation(colors.accent),
                  ),
                ),
                Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(
                      course.detailsPending || course.taskPointCount == 0
                          ? '—'
                          : '${(progress * 100).round()}%',
                      style: TextStyle(
                        color: colors.text,
                        fontSize: 26,
                        fontWeight: FontWeight.w600,
                        fontFeatures: const [FontFeature.tabularFigures()],
                      ),
                    ),
                    Text(
                      '平台已确认',
                      style: TextStyle(
                        color: colors.textSecondary,
                        fontSize: 12,
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ),
          const SizedBox(height: 16),
          Row(
            children: [
              _stat(
                  context,
                  '已确认',
                  course.detailsPending
                      ? '—'
                      : '${course.confirmedTaskPoints}'),
              _stat(
                  context,
                  '待完成',
                  course.detailsPending
                      ? '—'
                      : '${course.incompleteTaskPointCount}'),
              _stat(context, '任务点',
                  course.detailsPending ? '—' : '${course.taskPointCount}'),
            ],
          ),
          const SizedBox(height: 12),
          Align(
            alignment: Alignment.centerLeft,
            child: Text(
              '最近同步 ${formatDateTime(course.syncedAt)}',
              style: TextStyle(
                color: colors.muted,
                fontSize: 12,
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _stat(BuildContext context, String label, String value) {
    return Expanded(
      child: Column(
        children: [
          Text(
            value,
            style: TextStyle(
              color: context.colors.text,
              fontSize: 16,
              fontWeight: FontWeight.w600,
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
          const SizedBox(height: 2),
          Text(
            label,
            style: TextStyle(
              color: context.colors.textSecondary,
              fontSize: 12,
            ),
          ),
        ],
      ),
    );
  }
}
