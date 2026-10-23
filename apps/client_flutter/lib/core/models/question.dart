import '../api/json.dart';
import 'plan.dart';

class QuestionOffering {
  const QuestionOffering({
    required this.id,
    required this.displayName,
    this.description,
    this.capabilities = const <String>[],
    this.isDefault = false,
    this.sortOrder = 0,
    this.priceVersion,
    this.cacheAllowed = false,
    this.health,
  });

  final String id;
  final String displayName;
  final String? description;
  final List<String> capabilities;
  final bool isDefault;
  final int sortOrder;
  final String? priceVersion;
  final bool cacheAllowed;
  final String? health;

  bool get supportsImages =>
      capabilities.contains('image') ||
      capabilities.contains('vision') ||
      capabilities.contains('图片');

  bool get healthy => switch (health) {
        'unavailable' || 'disabled' || 'failed' => false,
        _ => true,
      };

  String get capabilityLabel {
    final labels = <String>[];
    if (capabilities.contains('text') || capabilities.isEmpty) labels.add('文本');
    if (supportsImages) labels.add('图片');
    return labels.isEmpty ? '文本' : labels.join(' · ');
  }

  String get priceLabel =>
      priceVersion == null || priceVersion!.isEmpty ? '单价待公布' : '按已发布单价';

  static QuestionOffering parse(Map<String, dynamic> map) {
    final cache = asObject(map['cache_policy']);
    return QuestionOffering(
      id: readString(map, 'id') ?? '',
      displayName: readString(map, 'display_name') ?? '未命名模型',
      description: readString(map, 'description'),
      capabilities: readStringList(map, 'capabilities'),
      isDefault: readBool(map, 'default') ?? false,
      sortOrder: readInt(map, 'sort_order') ?? 0,
      priceVersion: readString(map, 'price_version'),
      cacheAllowed: readBool(cache, 'allow') ?? false,
      health: readString(map, 'health'),
    );
  }
}

class QuestionPrompt {
  const QuestionPrompt({
    this.title = '',
    this.options = '',
    this.type = 'single',
    this.images = const <String>[],
    this.imageCount = 0,
  });

  final String title;
  final String options;
  final String type;
  final List<String> images;
  final int imageCount;

  bool get hasContent => title.trim().isNotEmpty;

  String get typeLabel => switch (type) {
        'multiple' => '多选题',
        'judgement' => '判断题',
        'completion' => '填空题',
        'shortanswer' => '简答题',
        _ => '单选题',
      };

  static QuestionPrompt parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return QuestionPrompt(
      title: readString(map, 'title') ?? '',
      options: readString(map, 'options') ?? '',
      type: readString(map, 'type') ?? 'single',
      images: readStringList(map, 'images'),
      imageCount: readInt(map, 'image_count') ?? 0,
    );
  }
}

class QuestionUsageBreakdown {
  const QuestionUsageBreakdown({
    this.inputTokens = 0,
    this.outputTokens = 0,
    this.totalTokens = 0,
    this.complete = false,
    this.calls = const <QuestionCall>[],
  });

  final int inputTokens;
  final int outputTokens;
  final int totalTokens;
  final bool complete;
  final List<QuestionCall> calls;

  static QuestionUsageBreakdown parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return QuestionUsageBreakdown(
      inputTokens: readInt(map, 'input_tokens') ?? 0,
      outputTokens: readInt(map, 'output_tokens') ?? 0,
      totalTokens: readInt(map, 'total_tokens') ?? 0,
      complete: readBool(map, 'complete') ?? false,
      calls: asList(map['calls'])
          .map(asObject)
          .whereType<Map<String, dynamic>>()
          .map(QuestionCall.parse)
          .toList(growable: false),
    );
  }
}

class QuestionCall {
  const QuestionCall({
    this.slot,
    this.inputTokens = 0,
    this.outputTokens = 0,
    this.totalTokens = 0,
    this.status,
  });

  final String? slot;
  final int inputTokens;
  final int outputTokens;
  final int totalTokens;
  final String? status;

  static QuestionCall parse(Map<String, dynamic> map) {
    return QuestionCall(
      slot: readString(map, 'slot'),
      inputTokens: readInt(map, 'input_tokens') ?? 0,
      outputTokens: readInt(map, 'output_tokens') ?? 0,
      totalTokens: readInt(map, 'total_tokens') ?? 0,
      status: readString(map, 'status'),
    );
  }
}

class QuestionPricing {
  const QuestionPricing({
    this.state = 'unpriced',
    this.priceVersion,
    this.costMicros,
    this.amountMinor,
  });

  final String state;
  final String? priceVersion;
  final int? costMicros;
  final int? amountMinor;

  bool get unpriced => state != 'quoted';

  String get label {
    if (state == 'cache_hit') return '缓存命中 · 无模型调用';
    if (state == 'free') return '免费';
    if (state == 'quoted') {
      final amount = amountMinor;
      if (amount != null && amount > 0) {
        return '按报价结算';
      }
      return '已报价';
    }
    if (state == 'usage_incomplete') return '用量不完整 · 未自动扣费';
    return '单价待公布 · 本次不自动扣费';
  }

  static QuestionPricing parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return QuestionPricing(
      state: readString(map, 'state') ?? 'unpriced',
      priceVersion: readString(map, 'price_version'),
      costMicros: readInt(map, 'cost_micros'),
      amountMinor: readInt(map, 'amount_minor'),
    );
  }
}

class QuestionQuery {
  const QuestionQuery({
    required this.id,
    this.status,
    this.prompt = const QuestionPrompt(),
    this.answer,
    this.modelId,
    this.modelName,
    this.mode = AnswerMode.cacheFirst,
    this.cacheHit = false,
    this.cacheCreatedAt,
    this.usage = const QuestionUsageBreakdown(),
    this.pricing = const QuestionPricing(),
    this.confirmationToken,
    this.confirmedAt,
    this.submissionStatus,
    this.reviewReason,
    this.contentExpired = false,
    this.createdAt,
  });

  final String id;
  final String? status;
  final QuestionPrompt prompt;
  final String? answer;
  final String? modelId;
  final String? modelName;
  final AnswerMode mode;
  final bool cacheHit;
  final DateTime? cacheCreatedAt;
  final QuestionUsageBreakdown usage;
  final QuestionPricing pricing;
  final String? confirmationToken;
  final DateTime? confirmedAt;
  final String? submissionStatus;
  final String? reviewReason;
  final bool contentExpired;
  final DateTime? createdAt;

  bool get answered =>
      status == 'answered' || (answer != null && answer!.isNotEmpty);

  bool get submitted =>
      submissionStatus == 'submitted' || submissionStatus == 'confirmed';

  String get cacheLabel {
    if (cacheHit) return '精确缓存命中 · 0 model token';
    if (usage.complete) return '模型作答';
    return '用量待核对';
  }

  static QuestionQuery parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    final model = asObject(map['model']) ?? const <String, dynamic>{};
    final cache = asObject(map['cache']) ?? const <String, dynamic>{};
    final usage = asObject(map['usage']) ?? asObject(map['usage_breakdown']);
    final pricing = asObject(map['pricing']);
    final submission = asObject(map['submission']);
    final confirmation = asObject(map['confirmation']);
    final queryStatus = readString(map, 'status');
    return QuestionQuery(
      id: readString(map, 'id') ?? '',
      status: queryStatus,
      prompt: QuestionPrompt.parse(map['question']),
      answer: readString(map, 'answer'),
      modelId: readStringAlias(
        model,
        ['offering_id', 'id', 'model_offering_id'],
      ),
      modelName: readStringAlias(
        model,
        ['display_name', 'name'],
      ),
      mode: AnswerModeValue.parse(readString(map, 'mode')),
      cacheHit: readBool(cache, 'hit') ?? queryStatus == 'cache_hit',
      cacheCreatedAt: readDateTime(cache, 'created_at'),
      usage: QuestionUsageBreakdown.parse(usage),
      pricing: QuestionPricing.parse(pricing),
      confirmationToken: readString(confirmation, 'token'),
      confirmedAt: readDateTime(confirmation, 'confirmed_at'),
      submissionStatus: readString(submission, 'status'),
      reviewReason: readString(map, 'review_reason'),
      contentExpired: readBool(map, 'content_expired') ?? false,
      createdAt: readDateTime(map, 'created_at'),
    );
  }
}

class QuestionQueryRequest {
  const QuestionQueryRequest({
    required this.modelOfferingId,
    required this.mode,
    required this.prompt,
    this.cacheScope = 'user',
    this.maxOutputTokens = 1024,
  });

  final String modelOfferingId;
  final AnswerMode mode;
  final QuestionPrompt prompt;
  final String cacheScope;
  final int maxOutputTokens;

  Map<String, dynamic> toJson() => {
        'model_offering_id': modelOfferingId,
        'mode': mode.value,
        'cache_scope': cacheScope,
        'question': {
          'title': prompt.title,
          'options': prompt.options,
          'type': prompt.type,
          'images': prompt.images,
        },
        'budget': {'max_output_tokens': maxOutputTokens},
      };
}

class QuestionCacheReportRequest {
  const QuestionCacheReportRequest({
    required this.queryId,
    required this.reason,
  });

  final String queryId;
  final String reason;

  Map<String, dynamic> toJson() => {
        'query_id': queryId,
        'reason': reason,
      };
}
