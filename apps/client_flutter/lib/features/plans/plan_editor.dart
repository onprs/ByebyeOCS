import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/api/api_response.dart';
import '../../core/models/course.dart';
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
  return showModalBottomSheet<LearningPlan>(
    context: context,
    isScrollControlled: true,
    useSafeArea: true,
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
  final _scrollController = ScrollController();

  Set<String> _courseIds = <String>{};
  String _scopeKind = 'all';
  Set<String> _chapterKeys = <String>{};
  Set<String> _taskTypes = <String>{'video'};
  int _priority = 50;
  bool _questionEnabled = false;
  String? _questionModelId;
  AnswerMode _answerMode = AnswerMode.cacheFirst;

  final Map<String, CourseDetail> _details = <String, CourseDetail>{};
  final Set<String> _loadingDetails = <String>{};
  String? _detailError;

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
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _ensureDetails(_courseIds);
    });
  }

  @override
  void dispose() {
    _nameController.dispose();
    _scrollController.dispose();
    super.dispose();
  }

  void _applyPlan(LearningPlan plan) {
    _nameController.text = plan.name;
    _courseIds = plan.courseIds.toSet();
    _scopeKind = plan.scope.isSelected ? 'selected' : 'all';
    _chapterKeys = plan.scope.chapters
        .map((chapter) => '${chapter.courseId}::${chapter.chapterId}')
        .toSet();
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
      _ensureDetails(_courseIds);
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

  Future<void> _ensureDetails(Set<String> ids) async {
    final repository = ref.read(learningRepositoryProvider);
    for (final id in ids) {
      if (_details.containsKey(id) || _loadingDetails.contains(id)) continue;
      setState(() {
        _loadingDetails.add(id);
        _detailError = null;
      });
      try {
        final detail = await repository.fetchCourse(id);
        if (!mounted) return;
        setState(() {
          _details[id] = detail;
          _loadingDetails.remove(id);
        });
      } on ApiException catch (error) {
        if (!mounted) return;
        setState(() {
          _loadingDetails.remove(id);
          _detailError = error.userMessage;
        });
      } catch (_) {
        if (!mounted) return;
        setState(() {
          _loadingDetails.remove(id);
          _detailError = '课程章节暂时无法加载';
        });
      }
    }
  }

  Future<void> _save() async {
    if (_saving || _loadingSnapshot || _conflict) return;
    final draft = PlanDraft(
      name: _nameController.text,
      courseIds: _courseIds.toList(growable: false),
      scopeKind: _scopeKind,
      chapters: _scopeKind == 'selected'
          ? _chapterKeys.map((key) {
              final parts = key.split('::');
              return PlanChapterRef(courseId: parts[0], chapterId: parts[1]);
            }).toList(growable: false)
          : const <PlanChapterRef>[],
      taskTypes: _taskTypes.toList(growable: false),
      priority: _priority,
      questionPolicy: QuestionPolicy(
        enabled: _questionEnabled,
        modelOfferingId: _questionEnabled ? _questionModelId : null,
        answerMode: _answerMode,
        requireManualConfirmation: true,
      ),
    );
    final errors = draft.validate();
    if (_questionEnabled) {
      final offerings = ref.read(questionOfferingsProvider).valueOrNull;
      if (offerings != null && offerings.isEmpty) {
        errors.add('管理员尚未发布可用模型，请关闭题库策略');
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
      constraints: BoxConstraints(maxWidth: narrow ? double.infinity : 780),
      height: narrow ? height * 0.94 : height * 0.9,
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
                          _isEditing ? '修改会保存为新的安排版本。' : '选择课程、章节范围和任务类型。',
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
                    const SizedBox(height: 20),
                    _FieldLabel(text: '章节范围'),
                    const SizedBox(height: 8),
                    SegmentedButton<String>(
                      segments: const [
                        ButtonSegment(value: 'all', label: Text('全部章节')),
                        ButtonSegment(value: 'selected', label: Text('选择章节')),
                      ],
                      selected: {_scopeKind},
                      showSelectedIcon: false,
                      onSelectionChanged: _saving
                          ? null
                          : (value) {
                              setState(() {
                                _scopeKind = value.first;
                                _dirty = true;
                                _saveError = null;
                              });
                              if (_scopeKind == 'selected') {
                                _ensureDetails(_courseIds);
                              }
                            },
                    ),
                    if (_scopeKind == 'selected') ...[
                      const SizedBox(height: 10),
                      _buildChapters(context),
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
        return Container(
          constraints: const BoxConstraints(maxHeight: 230),
          decoration: BoxDecoration(
            color: context.colors.surfaceSubtle,
            borderRadius: context.colors.cardRadius,
            border: Border.all(color: context.colors.border),
          ),
          child: Scrollbar(
            child: ListView.builder(
              shrinkWrap: true,
              itemCount: state.courses.length,
              itemBuilder: (context, index) {
                final course = state.courses[index];
                final selected = _courseIds.contains(course.id);
                return Material(
                  color: Colors.transparent,
                  child: CheckboxListTile(
                    value: selected,
                    dense: true,
                    controlAffinity: ListTileControlAffinity.leading,
                    title: Text(
                      course.title,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(fontSize: 13.5),
                    ),
                    subtitle: Text(
                      '${course.teacher ?? '教师待同步'} · ${course.chapterCount} 章节',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(fontSize: 11.5),
                    ),
                    onChanged: _saving
                        ? null
                        : (value) {
                            setState(() {
                              if (value == true) {
                                _courseIds.add(course.id);
                              } else {
                                _courseIds.remove(course.id);
                                _chapterKeys.removeWhere(
                                  (key) => key.startsWith('${course.id}::'),
                                );
                              }
                              _dirty = true;
                              _saveError = null;
                            });
                            _ensureDetails(_courseIds);
                          },
                  ),
                );
              },
            ),
          ),
        );
      },
    );
  }

  Widget _buildChapters(BuildContext context) {
    final colors = context.colors;
    if (_courseIds.isEmpty) {
      return const InlineNotice(
        icon: Icons.touch_app_outlined,
        message: '先选择课程，再勾选章节。',
      );
    }
    if (_loadingDetails.isNotEmpty && _details.isEmpty) {
      return const LoadingList(rows: 1, height: 56);
    }
    final children = <Widget>[];
    for (final courseId in _courseIds) {
      final detail = _details[courseId];
      if (detail == null) {
        continue;
      }
      children.add(
        Padding(
          padding: const EdgeInsets.only(top: 6, bottom: 4),
          child: Text(
            detail.summary.title,
            style: TextStyle(
              color: colors.textSecondary,
              fontSize: 12,
              fontWeight: FontWeight.w600,
            ),
          ),
        ),
      );
      for (final chapter in detail.chapters) {
        final key = '$courseId::${chapter.id}';
        children.add(
          Material(
            color: Colors.transparent,
            child: CheckboxListTile(
              value: _chapterKeys.contains(key),
              dense: true,
              contentPadding: EdgeInsets.zero,
              controlAffinity: ListTileControlAffinity.leading,
              title: Text(
                chapter.title,
                style: const TextStyle(fontSize: 13),
              ),
              subtitle: Text(
                chapter.taskPoints.isEmpty
                    ? '本章暂无任务点'
                    : '${chapter.taskPoints.length} 个任务点',
                style: const TextStyle(fontSize: 11.5),
              ),
              onChanged: _saving
                  ? null
                  : (value) {
                      setState(() {
                        if (value == true) {
                          _chapterKeys.add(key);
                        } else {
                          _chapterKeys.remove(key);
                        }
                        _dirty = true;
                        _saveError = null;
                      });
                    },
            ),
          ),
        );
      }
    }
    if (_loadingDetails.isNotEmpty) {
      children.add(
        const Padding(
          padding: EdgeInsets.symmetric(vertical: 10),
          child: LinearProgressIndicator(minHeight: 2),
        ),
      );
    }
    if (_detailError != null) {
      children.add(
        Padding(
          padding: const EdgeInsets.only(top: 8),
          child: ErrorPanel(
            message: _detailError!,
            compact: true,
            onRetry: () {
              _details.clear();
              _ensureDetails(_courseIds);
            },
          ),
        ),
      );
    }
    if (children.isEmpty) {
      return const InlineNotice(
        icon: Icons.info_outline,
        message: '课程章节尚未加载，请稍后重试。',
      );
    }
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: colors.surfaceSubtle,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: children,
      ),
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
                      '题库策略',
                      style: TextStyle(
                        color: colors.text,
                        fontSize: 13.5,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    const SizedBox(height: 3),
                    Text(
                      '策略随安排版本冻结；修改只影响后续领取的任务。',
                      style: TextStyle(
                        color: colors.textSecondary,
                        fontSize: 11.5,
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
              '关闭时不会指定模型或作答方式，也不会自动把测验交给题库。',
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 11.5,
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
                message: '题库模型目录暂时无法加载；可保留现有策略，暂不改动。',
                action: TextButton(
                  onPressed: () => ref.invalidate(questionOfferingsProvider),
                  child: const Text('重试'),
                ),
              )
            else if (available.isEmpty)
              InlineNotice(
                tone: StatusTone.warning,
                icon: Icons.lock_outline,
                message: '管理员尚未发布可用模型，不能开启安排级题库策略。',
                action: TextButton(
                  onPressed: () => setState(() {
                    _questionEnabled = false;
                    _questionModelId = null;
                    _saveError = null;
                  }),
                  child: const Text('关闭策略'),
                ),
              )
            else ...[
              DropdownButtonFormField<String>(
                initialValue: selected?.id,
                decoration: const InputDecoration(
                  labelText: '公开模型',
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
                  message: '安排中的模型已不可用，请重新选择，或关闭题库策略。',
                ),
              ],
            ],
            const SizedBox(height: 12),
            SegmentedButton<AnswerMode>(
              segments: const [
                ButtonSegment(
                  value: AnswerMode.cacheFirst,
                  label: Text('cache_first'),
                ),
                ButtonSegment(
                  value: AnswerMode.alwaysLlm,
                  label: Text('always_llm'),
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
                fontSize: 11.5,
                height: 1.5,
              ),
            ),
            const SizedBox(height: 10),
            Text(
              '提交前人工确认：固定开启。当前版本不会自动答题，也不会自动提交。',
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 11,
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
            fontSize: 13.5,
            fontWeight: FontWeight.w600,
          ),
        ),
        if (trailing != null) ...[
          const Spacer(),
          Text(
            trailing!,
            style: TextStyle(color: colors.muted, fontSize: 11.5),
          ),
        ],
      ],
    );
  }
}
