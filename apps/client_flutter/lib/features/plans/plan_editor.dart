import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/api/api_response.dart';
import '../../core/models/plan.dart';
import '../../core/models/question.dart';
import '../../design/components.dart';
import '../../design/theme.dart';
import '../courses/courses_controller.dart';
import '../questions/question_providers.dart';

Future<LearningPlan?> showPlanEditor(
  BuildContext context, {
  LearningPlan? plan,
  String? initialCourseId,
  bool hasActiveTasks = false,
}) {
  return showAdaptivePanel<LearningPlan>(
    context: context,
    builder: (context) => PlanEditorSheet(
      plan: plan,
      initialCourseId: initialCourseId,
      hasActiveTasks: hasActiveTasks,
    ),
  );
}

class PlanEditorSheet extends ConsumerStatefulWidget {
  const PlanEditorSheet({
    super.key,
    this.plan,
    this.initialCourseId,
    this.hasActiveTasks = false,
  });

  final LearningPlan? plan;
  final String? initialCourseId;
  final bool hasActiveTasks;

  @override
  ConsumerState<PlanEditorSheet> createState() => _PlanEditorSheetState();
}

class _PlanEditorSheetState extends ConsumerState<PlanEditorSheet> {
  final _nameController = TextEditingController();
  final _courseSearchController = TextEditingController();
  final _scrollController = ScrollController();

  Set<String> _courseIds = <String>{};
  String _courseSearch = '';
  Set<String> _taskTypes = <String>{'video'};
  int _priority = 50;
  bool _questionEnabled = false;
  String? _questionModelId;
  AnswerMode _answerMode = AnswerMode.cacheFirst;
  bool _hadChapterScope = false;

  PlanSnapshot? _snapshot;
  bool _loadingSnapshot = false;
  bool _saving = false;
  bool _conflict = false;
  bool _dirty = false;
  String? _saveError;
  String? _snapshotError;

  bool get _isEditing => widget.plan != null;

  @override
  void initState() {
    super.initState();
    final plan = widget.plan;
    if (plan != null) {
      _applyPlan(plan);
      _loadingSnapshot = true;
      WidgetsBinding.instance.addPostFrameCallback((_) => _loadSnapshot());
    } else if (widget.initialCourseId != null &&
        widget.initialCourseId!.isNotEmpty) {
      _courseIds = {widget.initialCourseId!};
    }
  }

  @override
  void dispose() {
    _nameController.dispose();
    _courseSearchController.dispose();
    _scrollController.dispose();
    super.dispose();
  }

  void _applyPlan(LearningPlan plan) {
    _nameController.text = plan.name;
    _courseIds = plan.courseIds.toSet();
    _hadChapterScope = plan.scope.isSelected;
    _taskTypes =
        plan.taskTypes.isEmpty ? <String>{'video'} : plan.taskTypes.toSet();
    _priority = plan.priority >= 67
        ? 80
        : plan.priority <= 33
            ? 20
            : 50;
    _questionEnabled = plan.questionPolicy.enabled;
    _questionModelId = plan.questionPolicy.modelOfferingId;
    _answerMode = plan.questionPolicy.answerMode;
  }

  Future<void> _loadSnapshot({bool replaceDraft = false}) async {
    final id = widget.plan?.id;
    if (id == null) return;
    setState(() {
      _loadingSnapshot = true;
      _snapshotError = null;
      _conflict = false;
    });
    try {
      final snapshot = await ref.read(learningRepositoryProvider).fetchPlan(id);
      if (!mounted) return;
      setState(() {
        _snapshot = snapshot;
        _loadingSnapshot = false;
        if (replaceDraft) {
          _applyPlan(snapshot.plan);
          _dirty = false;
        }
      });
    } on ApiException catch (error) {
      if (!mounted) return;
      setState(() {
        _loadingSnapshot = false;
        _snapshotError = error.userMessage;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _loadingSnapshot = false;
        _snapshotError = '安排最新版本暂时无法加载';
      });
    }
  }

  Future<void> _save() async {
    if (_saving || _loadingSnapshot || _conflict) return;
    final draft = PlanDraft(
      name: _nameController.text,
      courseIds: _courseIds.toList(growable: false),
      taskTypes: _taskTypes.toList(growable: false),
      priority: _priority,
      questionPolicy: QuestionPolicy(
        enabled: _questionEnabled,
        modelOfferingId: _questionEnabled ? _questionModelId : null,
        answerMode: _answerMode,
      ),
    );
    final errors = draft.validate();
    if (_questionEnabled) {
      final offerings = ref.read(questionOfferingsProvider).valueOrNull;
      if (offerings != null && offerings.isEmpty) {
        errors.add('管理员尚未发布可用模型，请关闭题库配置');
      } else if (offerings != null &&
          _questionModelId != null &&
          offerings.every((model) => model.id != _questionModelId)) {
        errors.add('所选题库模型已不可用，请重新选择');
      }
    }
    if (errors.isNotEmpty) {
      setState(() => _saveError = errors.first);
      return;
    }
    setState(() {
      _saving = true;
      _saveError = null;
    });
    try {
      final repository = ref.read(learningRepositoryProvider);
      final LearningPlan result;
      if (_isEditing) {
        final etag = _snapshot?.etag ??
            (_snapshot?.effectiveVersion != null
                ? formatVersionEtag(_snapshot!.effectiveVersion!)
                : null);
        if (etag == null) {
          if (!mounted) return;
          setState(() {
            _saving = false;
            _saveError = '需要先加载安排最新版本再保存';
          });
          return;
        }
        final updated = await repository.updatePlan(
          id: widget.plan!.id,
          draft: draft,
          etag: etag,
        );
        result = updated.plan;
        _snapshot = updated;
      } else {
        final created = await repository.createPlan(draft);
        result = created.plan;
        _snapshot = created;
      }
      if (!mounted) return;
      Navigator.of(context).pop(result);
    } on ApiException catch (error) {
      if (!mounted) return;
      if (error.isVersionConflict) {
        setState(() {
          _saving = false;
          _conflict = true;
          _saveError = null;
        });
      } else if (error.isPreconditionRequired) {
        setState(() {
          _saving = false;
          _saveError = '需要先加载安排最新版本再保存';
        });
        await _loadSnapshot();
      } else {
        setState(() {
          _saving = false;
          _saveError = error.userMessage;
        });
      }
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _saving = false;
        _saveError = '安排未保存，请稍后重试';
      });
    }
  }

  void _markDirty() {
    if (!_dirty) setState(() => _dirty = true);
    if (_saveError != null) setState(() => _saveError = null);
  }

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final media = MediaQuery.of(context);
    final height = media.size.height;
    final narrow = media.size.width < 760;
    return Container(
      width: double.infinity,
      constraints: const BoxConstraints(maxWidth: AppMetrics.drawerWidth),
      height: narrow ? height * 0.9 : height,
      child: Padding(
        padding: EdgeInsets.only(bottom: media.viewInsets.bottom),
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(22, 4, 14, 12),
              child: Row(
                children: [
                  Expanded(
                    child: SheetHeader(
                      title: _isEditing ? '编辑任务安排' : '新建任务安排',
                      subtitle:
                          _isEditing ? '修改会保存为新的安排版本。' : '选择课程和任务类型。',
                    ),
                  ),
                  IconButton(
                    tooltip: '关闭',
                    onPressed: _saving ? null : () => Navigator.pop(context),
                    icon: const Icon(Icons.close, size: 20),
                  ),
                ],
              ),
            ),
            Divider(height: 1, color: colors.border),
            Expanded(
              child: SingleChildScrollView(
                controller: _scrollController,
                padding: const EdgeInsets.fromLTRB(22, 18, 22, 24),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    if (_loadingSnapshot)
                      const Padding(
                        padding: EdgeInsets.only(bottom: 14),
                        child: LinearProgressIndicator(minHeight: 2),
                      ),
                    if (_snapshotError != null)
                      Padding(
                        padding: const EdgeInsets.only(bottom: 14),
                        child: ErrorPanel(
                          message: _snapshotError!,
                          onRetry: () => _loadSnapshot(replaceDraft: true),
                          compact: true,
                        ),
                      ),
                    if (_conflict)
                      Padding(
                        padding: const EdgeInsets.only(bottom: 14),
                        child: InlineNotice(
                          tone: StatusTone.warning,
                          icon: Icons.merge_type,
                          message: '安排已在其他位置更新，当前草稿没有覆盖最新版本。载入最新版本后可继续编辑。',
                          action: TextButton(
                            onPressed: () => _loadSnapshot(replaceDraft: true),
                            child: const Text('载入最新版本'),
                          ),
                        ),
                      ),
                    if (widget.hasActiveTasks)
                      const Padding(
                        padding: EdgeInsets.only(bottom: 14),
                        child: InlineNotice(
                          icon: Icons.play_circle_outline,
                          message: '当前任务会保持本轮设置；修改从后续任务生效。',
                        ),
                      ),
                    _FieldLabel(text: '安排名称'),
                    const SizedBox(height: 8),
                    TextField(
                      controller: _nameController,
                      enabled: !_saving && !_loadingSnapshot,
                      maxLength: 80,
                      textInputAction: TextInputAction.next,
                      onChanged: (_) => _markDirty(),
                      decoration: const InputDecoration(
                        hintText: '例如：本周课程学习',
                        counterText: '',
                      ),
                    ),
                    const SizedBox(height: 20),
                    _FieldLabel(
                      text: '课程范围',
                      trailing: '已选 ${_courseIds.length} 门',
                    ),
                    const SizedBox(height: 8),
                    _buildCourses(context),
                    if (_hadChapterScope) ...[
                      const SizedBox(height: 10),
                      const InlineNotice(
                        icon: Icons.info_outline,
                        message: '这条安排原按选定章节执行；保存后统一为全部章节。',
                      ),
                    ],
                    const SizedBox(height: 20),
                    _FieldLabel(text: '任务类型'),
                    const SizedBox(height: 8),
                    _buildTaskTypes(context),
                    const SizedBox(height: 20),
                    _FieldLabel(text: '优先级'),
                    const SizedBox(height: 8),
                    SegmentedButton<int>(
                      segments: const [
                        ButtonSegment(value: 20, label: Text('稍后处理')),
                        ButtonSegment(value: 50, label: Text('普通')),
                        ButtonSegment(value: 80, label: Text('优先处理')),
                      ],
                      selected: {_priority},
                      showSelectedIcon: false,
                      onSelectionChanged: _saving
                          ? null
                          : (value) {
                              setState(() {
                                _priority = value.first;
                                _dirty = true;
                                _saveError = null;
                              });
                            },
                    ),
                    const SizedBox(height: 20),
                    _buildQuestionPolicy(context),
                    const SizedBox(height: 22),
                    if (_saveError != null)
                      ErrorPanel(
                        message: _saveError!,
                        compact: true,
                      ),
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
                child: Row(
                  children: [
                    Expanded(
                      child: OutlinedButton(
                        onPressed:
                            _saving ? null : () => Navigator.pop(context),
                        child: const Text('取消'),
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      flex: 2,
                      child: AsyncButton(
                        label: _isEditing ? '保存安排' : '创建安排',
                        busy: _saving,
                        icon: Icons.check,
                        expand: true,
                        onPressed: _loadingSnapshot || _conflict ? null : _save,
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildCourses(BuildContext context) {
    final async = ref.watch(coursesControllerProvider);
    return async.when(
      loading: () => const LoadingList(rows: 1, height: 58),
      error: (error, _) => ErrorPanel(
        message: error is ApiException ? error.userMessage : '课程暂时无法加载',
        onRetry: () => ref.invalidate(coursesControllerProvider),
        compact: true,
      ),
      data: (state) {
        if (state.courses.isEmpty) {
          return const InlineNotice(
            icon: Icons.menu_book_outlined,
            message: '还没有已同步课程，请先在课程页同步。',
          );
        }
        final keyword = _courseSearch.trim().toLowerCase();
        final matches = keyword.isEmpty
            ? state.courses
            : state.courses.where((course) {
                final title = course.title.toLowerCase();
                final teacher = (course.teacher ?? '').toLowerCase();
                return title.contains(keyword) || teacher.contains(keyword);
              }).toList(growable: false);
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            TextField(
              key: const Key('plan-course-search'),
              controller: _courseSearchController,
              enabled: !_saving,
              textInputAction: TextInputAction.search,
              onChanged: (value) => setState(() => _courseSearch = value),
              decoration: InputDecoration(
                isDense: true,
                hintText: '搜索课程名称或教师',
                prefixIcon: const Icon(Icons.search, size: 18),
                suffixIcon: _courseSearch.trim().isEmpty
                    ? null
                    : IconButton(
                        tooltip: '清除搜索',
                        onPressed: () {
                          _courseSearchController.clear();
                          setState(() => _courseSearch = '');
                        },
                        icon: const Icon(Icons.close, size: 16),
                      ),
              ),
            ),
            const SizedBox(height: 8),
            Container(
              constraints: const BoxConstraints(maxHeight: 230),
              decoration: BoxDecoration(
                color: context.colors.surfaceSubtle,
                borderRadius: context.colors.cardRadius,
                border: Border.all(color: context.colors.border),
              ),
              child: matches.isEmpty
                  ? const Padding(
                      padding: EdgeInsets.symmetric(vertical: 18),
                      child: Center(
                        child: Text(
                          '没有匹配的课程',
                          style: TextStyle(fontSize: 13),
                        ),
                      ),
                    )
                  : Scrollbar(
                      child: ListView.builder(
                        shrinkWrap: true,
                        itemCount: matches.length,
                        itemBuilder: (context, index) {
                          final course = matches[index];
                          final selected = _courseIds.contains(course.id);
                          return Material(
                            color: Colors.transparent,
                            child: CheckboxListTile(
                              value: selected,
                              dense: true,
                              enabled: !course.isClosed,
                              controlAffinity: ListTileControlAffinity.leading,
                              title: Text(
                                course.title,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: const TextStyle(fontSize: 14),
                              ),
                              subtitle: Text(
                                course.isClosed
                                    ? '课程已结束，不再派发任务'
                                    : '${course.teacher ?? '教师待同步'} · ${course.chapterCount} 章节'
                                        '${course.closedTaskPointCount > 0 ? ' · ${course.closedTaskPointCount} 个已关闭任务点' : ''}',
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: const TextStyle(fontSize: 12),
                              ),
                              onChanged: _saving || course.isClosed
                                  ? null
                                  : (value) {
                                      setState(() {
                                        if (value == true) {
                                          _courseIds.add(course.id);
                                        } else {
                                          _courseIds.remove(course.id);
                                        }
                                        _dirty = true;
                                        _saveError = null;
                                      });
                                    },
                            ),
                          );
                        },
                      ),
                    ),
            ),
            if (_courseIds.isNotEmpty)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Text(
                  '已选 ${_courseIds.length} 门；搜索不会改变已选课程。',
                  style: TextStyle(
                    color: context.colors.muted,
                    fontSize: 12,
                  ),
                ),
              ),
          ],
        );
      },
    );
  }

  Widget _buildQuestionPolicy(BuildContext context) {
    final colors = context.colors;
    final offeringsAsync = ref.watch(questionOfferingsProvider);
    final offerings = offeringsAsync.valueOrNull ?? const <QuestionOffering>[];
    final available =
        offerings.where((offering) => offering.healthy).toList(growable: false);
    QuestionOffering? selected;
    for (final offering in available) {
      if (offering.id == _questionModelId) selected = offering;
    }
    final modelMissing = _questionModelId != null && selected == null;
    final loadingModels = offeringsAsync.isLoading;
    final modelsUnavailable = offeringsAsync.hasError;
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
          Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      '题库配置',
                      style: TextStyle(
                        color: colors.text,
                        fontSize: 14,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const SizedBox(height: 3),
                    Text(
                      '开启后自动查询测验答案并提交，结果以平台复核为准。',
                      style: TextStyle(
                        color: colors.textSecondary,
                        fontSize: 12,
                        height: 1.5,
                      ),
                    ),
                  ],
                ),
              ),
              Switch(
                value: _questionEnabled,
                onChanged: _saving || loadingModels
                    ? null
                    : (value) {
                        if (value && (modelsUnavailable || available.isEmpty)) {
                          return;
                        }
                        setState(() {
                          _questionEnabled = value;
                          _dirty = true;
                          _saveError = null;
                        });
                      },
              ),
            ],
          ),
          if (!_questionEnabled) ...[
            const SizedBox(height: 8),
            Text(
              '关闭时不查询也不提交测验答案。',
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 12,
                height: 1.5,
              ),
            ),
          ] else ...[
            const SizedBox(height: 12),
            if (loadingModels)
              const LinearProgressIndicator(minHeight: 2)
            else if (modelsUnavailable)
              InlineNotice(
                tone: StatusTone.warning,
                icon: Icons.cloud_off_outlined,
                message: '模型列表暂时无法加载；可保留现有配置，暂不改动。',
                action: TextButton(
                  onPressed: () => ref.invalidate(questionOfferingsProvider),
                  child: const Text('重试'),
                ),
              )
            else if (available.isEmpty)
              InlineNotice(
                tone: StatusTone.warning,
                icon: Icons.lock_outline,
                message: '管理员尚未发布可用模型，暂时不能开启题库配置。',
                action: TextButton(
                  onPressed: () => setState(() {
                    _questionEnabled = false;
                    _questionModelId = null;
                    _saveError = null;
                  }),
                  child: const Text('关闭题库配置'),
                ),
              )
            else ...[
              DropdownButtonFormField<String>(
                initialValue: selected?.id,
                style: TextStyle(fontSize: 14, color: colors.text),
                decoration: const InputDecoration(
                  labelText: '模型',
                  isDense: true,
                ),
                items: [
                  for (final offering in available)
                    DropdownMenuItem(
                      value: offering.id,
                      child: Text(
                        offering.supportsImages
                            ? '${offering.displayName} · 支持图片题'
                            : offering.displayName,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(fontSize: 14, color: colors.text),
                      ),
                    ),
                ],
                onChanged: _saving
                    ? null
                    : (value) {
                        setState(() {
                          _questionModelId = value;
                          _dirty = true;
                          _saveError = null;
                        });
                      },
              ),
              if (modelMissing) ...[
                const SizedBox(height: 8),
                const InlineNotice(
                  tone: StatusTone.warning,
                  message: '安排中的模型已不可用，请重新选择，或关闭题库配置。',
                ),
              ],
            ],
            const SizedBox(height: 12),
            SegmentedButton<AnswerMode>(
              segments: const [
                ButtonSegment(
                  value: AnswerMode.cacheFirst,
                  label: Text('优先复用答案'),
                ),
                ButtonSegment(
                  value: AnswerMode.alwaysLlm,
                  label: Text('每次调用模型'),
                ),
              ],
              selected: {_answerMode},
              showSelectedIcon: false,
              onSelectionChanged: _saving
                  ? null
                  : (value) {
                      setState(() {
                        _answerMode = value.first;
                        _dirty = true;
                      });
                    },
            ),
            const SizedBox(height: 7),
            Text(
              _answerMode.description,
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 12,
                height: 1.5,
              ),
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildTaskTypes(BuildContext context) {
    final capabilities = ref.watch(authCapabilitiesProvider);
    final options = capabilities.taskTypes.isNotEmpty
        ? capabilities.taskTypes
            .map((type) =>
                (id: type.id, name: type.name, enabled: type.available))
            .toList(growable: false)
        : const [
            (id: 'video', name: '视频', enabled: true),
            (id: 'document', name: '文档', enabled: true),
            (id: 'audio', name: '音频', enabled: true),
            (id: 'quiz', name: '测验', enabled: true),
          ];
    return Wrap(
      spacing: 8,
      runSpacing: 8,
      children: [
        for (final option in options)
          FilterChip(
            label: Text(option.name),
            selected: _taskTypes.contains(option.id),
            onSelected: _saving || !option.enabled
                ? null
                : (selected) {
                    setState(() {
                      if (selected) {
                        _taskTypes.add(option.id);
                      } else {
                        _taskTypes.remove(option.id);
                      }
                      _dirty = true;
                      _saveError = null;
                    });
                  },
          ),
      ],
    );
  }
}

class _FieldLabel extends StatelessWidget {
  const _FieldLabel({required this.text, this.trailing});

  final String text;
  final String? trailing;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Row(
      children: [
        Text(
          text,
          style: TextStyle(
            color: colors.text,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
        if (trailing != null) ...[
          const Spacer(),
          Text(
            trailing!,
            style: TextStyle(color: colors.muted, fontSize: 12),
          ),
        ],
      ],
    );
  }
}
