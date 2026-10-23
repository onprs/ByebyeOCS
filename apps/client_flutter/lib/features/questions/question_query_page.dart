import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/api/json.dart';
import '../../core/models/plan.dart';
import '../../core/models/question.dart';
import '../../design/components.dart';
import '../../design/theme.dart';
import 'question_providers.dart';
import 'question_widgets.dart';

const _questionTypes = <String, String>{
  'single': '单选题',
  'multiple': '多选题',
  'judgement': '判断题',
  'completion': '填空题',
  'shortanswer': '简答题',
};

/// 独立题库查询：只查询并展示答案，不提交学习进度，也不代替安排内的自动测验。
class QuestionQueryPage extends ConsumerStatefulWidget {
  const QuestionQueryPage({super.key});

  @override
  ConsumerState<QuestionQueryPage> createState() => _QuestionQueryPageState();
}

class _QuestionQueryPageState extends ConsumerState<QuestionQueryPage> {
  final _titleController = TextEditingController();
  final _optionsController = TextEditingController();

  String _type = 'single';
  AnswerMode _mode = AnswerMode.cacheFirst;
  String? _modelId;
  QuestionQuery? _query;
  bool _submitting = false;
  bool _refreshing = false;
  bool _reporting = false;
  String? _error;
  String? _notice;
  String? _idempotencyKey;
  String? _idempotencySignature;

  @override
  void dispose() {
    _titleController.dispose();
    _optionsController.dispose();
    super.dispose();
  }

  QuestionOffering? _resolveOffering(List<QuestionOffering> offerings) {
    final available = offerings
        .where((offering) => offering.healthy)
        .toList(growable: false);
    if (available.isEmpty) return null;
    for (final offering in available) {
      if (offering.id == _modelId) return offering;
    }
    for (final offering in available) {
      if (offering.isDefault) return offering;
    }
    return available.first;
  }

  Future<void> _submit() async {
    if (_submitting) return;
    final title = _titleController.text.trim();
    if (title.isEmpty) {
      setState(() {
        _error = '请填写题干后再查询';
        _notice = null;
      });
      return;
    }
    final offerings = ref.read(questionOfferingsProvider).valueOrNull;
    if (offerings == null) {
      setState(() {
        _error = '题库模型目录暂时无法加载，请重试';
        _notice = null;
      });
      return;
    }
    final offering = _resolveOffering(offerings);
    if (offering == null) {
      setState(() {
        _error = '管理员尚未发布可用模型，暂时不能查询答案';
        _notice = null;
      });
      return;
    }
    final options = _optionsController.text.trim();
    final mode = offering.cacheAllowed ? _mode : AnswerMode.alwaysLlm;
    // 幂等键绑定当前模型、作答方式与题目内容；网络失败后原样重试复用同一键。
    final signature =
        '${offering.id}\u0000${mode.value}\u0000$_type\u0000$title\u0000$options';
    if (_idempotencySignature != signature || _idempotencyKey == null) {
      _idempotencySignature = signature;
      _idempotencyKey = newIdempotencyKey();
    }
    setState(() {
      _submitting = true;
      _error = null;
      _notice = null;
      _modelId = offering.id;
    });
    try {
      final query = await ref.read(questionRepositoryProvider).createQuery(
            request: QuestionQueryRequest(
              modelOfferingId: offering.id,
              mode: mode,
              prompt: QuestionPrompt(
                title: title,
                options: options,
                type: _type,
              ),
            ),
            idempotencyKey: _idempotencyKey!,
          );
      if (!mounted) return;
      setState(() {
        _query = query;
        _submitting = false;
        _idempotencyKey = null;
        _idempotencySignature = null;
        _notice = query.answered
            ? '答案已返回'
            : query.status == 'needs_review'
                ? '本次查询需要人工核对，可在结果中查看状态'
                : '查询已提交，答案返回后可刷新结果';
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
        _error = '答案查询未完成，请稍后重试';
      });
    }
  }

  Future<void> _refreshResult() async {
    final query = _query;
    if (query == null || query.id.isEmpty || _refreshing) return;
    setState(() {
      _refreshing = true;
      _error = null;
    });
    try {
      final latest =
          await ref.read(questionRepositoryProvider).fetchQuery(query.id);
      if (!mounted) return;
      setState(() {
        _query = latest;
        _refreshing = false;
        if (latest.answered) _notice = '答案已刷新';
      });
    } on ApiException catch (error) {
      if (!mounted) return;
      setState(() {
        _refreshing = false;
        _error = error.userMessage;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _refreshing = false;
        _error = '答案刷新未完成';
      });
    }
  }

  Future<void> _report() async {
    final query = _query;
    if (query == null || _reporting) return;
    final reason = await askQuestionReportReason(context);
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

  @override
  Widget build(BuildContext context) {
    final capabilities = ref.watch(authCapabilitiesProvider);
    final offeringsAsync = ref.watch(questionOfferingsProvider);
    return SingleChildScrollView(
      padding: EdgeInsets.only(bottom: context.isNarrow ? 12 : 28),
      child: ResponsiveContent(
        maxWidth: 920,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Align(
              alignment: Alignment.centerLeft,
              child: TextButton.icon(
                onPressed: () => context.go('/settings'),
                icon: const Icon(Icons.arrow_back, size: 16),
                label: const Text('返回设置'),
              ),
            ),
            const PageHeader(
              eyebrow: '题库查询',
              title: '按题查询答案',
              description: '选择已发布模型，输入题目后查询答案；发现答案或缓存有误可以提交复核。',
            ),
            if (capabilities.questionsExplicitlyDisabled)
              const SectionCard(
                child: EmptyState(
                  icon: Icons.lock_outline,
                  title: '题库查询当前未开放',
                  message: '当前服务尚未开放题库查询。已开启题库策略的安排仍按原授权状态执行。',
                ),
              )
            else
              SectionCard(
                title: '查询题目',
                subtitle: '单独查询只返回答案，不写入课程任务；安排内的测验仍由任务自动完成。',
                child: offeringsAsync.when(
                  loading: () => const Padding(
                    padding: EdgeInsets.symmetric(vertical: 8),
                    child: LinearProgressIndicator(minHeight: 2),
                  ),
                  error: (error, _) => ErrorPanel(
                    message: error is ApiException
                        ? error.userMessage
                        : '题库模型目录暂时无法加载',
                    requestId: error is ApiException ? error.requestId : null,
                    onRetry: () => ref.invalidate(questionOfferingsProvider),
                  ),
                  data: (offerings) => _buildForm(offerings),
                ),
              ),
            if (_error != null || _notice != null) ...[
              const SizedBox(height: 14),
              InlineNotice(
                tone: _error == null ? StatusTone.success : StatusTone.warning,
                icon: _error == null
                    ? Icons.check_circle_outline
                    : Icons.info_outline,
                message: _error ?? _notice!,
                action: IconButton(
                  tooltip: '关闭',
                  onPressed: () => setState(() {
                    _error = null;
                    _notice = null;
                  }),
                  icon: const Icon(Icons.close, size: 16),
                ),
              ),
            ],
            if (_query != null) ...[
              const SizedBox(height: 14),
              SectionCard(
                title: '查询结果',
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    QuestionResultCard(
                      query: _query!,
                      reporting: _reporting,
                      onReport: _report,
                      refreshing: _refreshing,
                      onRefresh: _refreshResult,
                    ),
                    if (_query!.status == 'needs_review') ...[
                      const SizedBox(height: 10),
                      InlineNotice(
                        tone: StatusTone.warning,
                        icon: Icons.fact_check_outlined,
                        message:
                            questionReviewReasonLabel(_query!.reviewReason),
                      ),
                    ],
                    if (_query!.status == 'failed') ...[
                      const SizedBox(height: 10),
                      const InlineNotice(
                        tone: StatusTone.danger,
                        icon: Icons.error_outline,
                        message: '答案查询未完成，本次不会自动扣费。可修改题目后重新查询。',
                      ),
                    ],
                  ],
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }

  Widget _buildForm(List<QuestionOffering> offerings) {
    final available =
        offerings.where((offering) => offering.healthy).toList(growable: false);
    if (available.isEmpty) {
      return const EmptyState(
        icon: Icons.smart_toy_outlined,
        title: '管理员尚未发布可用模型',
        message: '题库模型发布后才能查询答案。',
        compact: true,
      );
    }
    final selected = _resolveOffering(available);
    final cacheAllowed = selected?.cacheAllowed ?? true;
    final mode = cacheAllowed ? _mode : AnswerMode.alwaysLlm;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        InputDecorator(
          decoration: const InputDecoration(
            labelText: '查询模型',
            isDense: true,
          ),
          child: DropdownButtonHideUnderline(
            child: DropdownButton<String>(
              value: selected?.id,
              isExpanded: true,
              isDense: true,
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
              onChanged: _submitting
                  ? null
                  : (value) => setState(() {
                        _modelId = value;
                        _error = null;
                      }),
            ),
          ),
        ),
        const SizedBox(height: 12),
        Text(
          '作答方式',
          style: TextStyle(
            color: context.colors.textSecondary,
            fontSize: 12,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 8),
        SegmentedButton<AnswerMode>(
          segments: [
            ButtonSegment(
              value: AnswerMode.cacheFirst,
              label: const Text('缓存优先'),
              enabled: cacheAllowed,
            ),
            const ButtonSegment(
              value: AnswerMode.alwaysLlm,
              label: Text('每次模型'),
            ),
          ],
          selected: {mode},
          showSelectedIcon: false,
          onSelectionChanged: _submitting || !cacheAllowed
              ? null
              : (value) => setState(() => _mode = value.first),
        ),
        const SizedBox(height: 7),
        Text(
          !cacheAllowed
              ? '该模型未启用答案缓存，每次都会调用模型。'
              : mode.description,
          style: TextStyle(
            color: context.colors.textSecondary,
            fontSize: 11.5,
            height: 1.5,
          ),
        ),
        const SizedBox(height: 14),
        InputDecorator(
          decoration: const InputDecoration(
            labelText: '题型',
            isDense: true,
          ),
          child: DropdownButtonHideUnderline(
            child: DropdownButton<String>(
              value: _type,
              isExpanded: true,
              isDense: true,
              items: [
                for (final entry in _questionTypes.entries)
                  DropdownMenuItem(
                    value: entry.key,
                    child: Text(entry.value),
                  ),
              ],
              onChanged: _submitting
                  ? null
                  : (value) {
                      if (value != null) setState(() => _type = value);
                    },
            ),
          ),
        ),
        const SizedBox(height: 14),
        TextField(
          controller: _titleController,
          enabled: !_submitting,
          maxLines: 4,
          maxLength: 20000,
          decoration: const InputDecoration(
            labelText: '题干',
            hintText: '粘贴或输入完整题干',
            alignLabelWithHint: true,
          ),
        ),
        const SizedBox(height: 10),
        TextField(
          controller: _optionsController,
          enabled: !_submitting,
          maxLines: 5,
          maxLength: 50000,
          decoration: const InputDecoration(
            labelText: '选项（可选）',
            hintText: 'A. ...\nB. ...',
            alignLabelWithHint: true,
          ),
        ),
        const SizedBox(height: 6),
        Text(
          '查询会使用所选模型；精确缓存命中时不会调用模型，token 为 0。',
          style: TextStyle(
            color: context.colors.muted,
            fontSize: 11.5,
            height: 1.5,
          ),
        ),
        const SizedBox(height: 14),
        Align(
          alignment: Alignment.centerLeft,
          child: AsyncButton(
            label: _query == null ? '查询答案' : '重新查询答案',
            icon: Icons.search,
            busy: _submitting,
            onPressed: _submitting ? null : _submit,
          ),
        ),
      ],
    );
  }
}
