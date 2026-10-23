import '../api/json.dart';
import '../format/formatters.dart';

class WalletSnapshot {
  const WalletSnapshot({
    required this.id,
    this.currency = 'CNY',
    this.availableMinor = 0,
    this.reservedMinor = 0,
    this.tikuAccruedMicros = 0,
    this.frozen = false,
    this.frozenReason,
    this.version = 0,
    this.updatedAt,
  });

  final String id;
  final String currency;
  final int availableMinor;
  final int reservedMinor;
  final int tikuAccruedMicros;
  final bool frozen;
  final String? frozenReason;
  final int version;
  final DateTime? updatedAt;

  Money get available => Money(availableMinor, currency: currency);
  Money get reserved => Money(reservedMinor, currency: currency);

  static WalletSnapshot parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return WalletSnapshot(
      id: readString(map, 'id') ?? '',
      currency: readString(map, 'currency') ?? 'CNY',
      availableMinor: Money.parseMinor(map['available_minor']) ?? 0,
      reservedMinor: Money.parseMinor(map['reserved_minor']) ?? 0,
      tikuAccruedMicros: Money.parseMinor(map['tiku_accrued_micros']) ?? 0,
      frozen: readBool(map, 'frozen') ?? false,
      frozenReason: readString(map, 'frozen_reason'),
      version: readInt(map, 'version') ?? 0,
      updatedAt: readDateTime(map, 'updated_at'),
    );
  }
}

enum WalletEntryKind {
  recharge,
  capture,
  hold,
  release,
  manualCredit,
  manualDebit,
  other
}

class WalletEntry {
  const WalletEntry({
    required this.id,
    required this.kind,
    this.reason,
    this.availableDeltaMinor = 0,
    this.reservedDeltaMinor = 0,
    this.orderId,
    this.usageId,
    this.createdAt,
  });

  final String id;
  final String kind;
  final String? reason;
  final int availableDeltaMinor;
  final int reservedDeltaMinor;
  final String? orderId;
  final String? usageId;
  final DateTime? createdAt;

  WalletEntryKind get kindValue {
    return switch (kind) {
      'recharge' => WalletEntryKind.recharge,
      'capture' => WalletEntryKind.capture,
      'hold' => WalletEntryKind.hold,
      'release' => WalletEntryKind.release,
      'manual_credit' => WalletEntryKind.manualCredit,
      'manual_debit' => WalletEntryKind.manualDebit,
      _ => WalletEntryKind.other,
    };
  }

  String get label {
    return switch (kindValue) {
      WalletEntryKind.recharge => '余额充值',
      WalletEntryKind.capture => '使用结算',
      WalletEntryKind.hold => '费用预占',
      WalletEntryKind.release => '预占释放',
      WalletEntryKind.manualCredit => '余额调整',
      WalletEntryKind.manualDebit => '余额调整',
      WalletEntryKind.other => '资金变动',
    };
  }

  Money get availableDelta => Money(availableDeltaMinor);

  static WalletEntry parse(Map<String, dynamic> map) {
    return WalletEntry(
      id: readString(map, 'journal_id') ?? readString(map, 'id') ?? '',
      kind: readString(map, 'kind') ?? 'other',
      reason: readString(map, 'reason'),
      availableDeltaMinor: Money.parseMinor(map['available_delta_minor']) ?? 0,
      reservedDeltaMinor: Money.parseMinor(map['reserved_delta_minor']) ?? 0,
      orderId: readString(map, 'order_id'),
      usageId: readString(map, 'usage_id'),
      createdAt: readDateTime(map, 'created_at'),
    );
  }
}

enum RechargeStage {
  pendingPayment,
  paidWaitingCredit,
  credited,
  closed,
  needsReview,
  refunded,
  unknown,
}

class RechargeOrder {
  const RechargeOrder({
    required this.id,
    required this.amountMinor,
    this.currency = 'CNY',
    this.channel,
    this.status,
    this.paymentStatus,
    this.creditStatus,
    this.provider,
    this.merchantOrderNo,
    this.latePayment = false,
    this.createdAt,
    this.updatedAt,
    this.expiresAt,
    this.paidAt,
    this.creditedAt,
    this.closedAt,
    this.version = 0,
    this.checkoutUrl,
    this.checkoutState,
  });

  final String id;
  final int amountMinor;
  final String currency;
  final String? channel;
  final String? status;
  final String? paymentStatus;
  final String? creditStatus;
  final String? provider;
  final String? merchantOrderNo;
  final bool latePayment;
  final DateTime? createdAt;
  final DateTime? updatedAt;
  final DateTime? expiresAt;
  final DateTime? paidAt;
  final DateTime? creditedAt;
  final DateTime? closedAt;
  final int version;
  final String? checkoutUrl;
  final String? checkoutState;

  Money get amount => Money(amountMinor, currency: currency);

  RechargeStage get stage {
    // 渠道退款是订单最终的支付事实；即使此前已到账，也应优先展示已退款。
    if (paymentStatus == 'refunded') return RechargeStage.refunded;
    if (creditStatus == 'credited' || status == 'completed') {
      return RechargeStage.credited;
    }
    if (status == 'needs_review' ||
        creditStatus == 'needs_review' ||
        paymentStatus == 'unknown' ||
        paymentStatus == 'failed' ||
        latePayment) {
      return RechargeStage.needsReview;
    }
    if (status == 'closed') return RechargeStage.closed;
    if (paymentStatus == 'paid') return RechargeStage.paidWaitingCredit;
    if (status == 'pending_payment' || paymentStatus == 'unpaid') {
      return RechargeStage.pendingPayment;
    }
    return RechargeStage.unknown;
  }

  String get stageLabel {
    return switch (stage) {
      RechargeStage.pendingPayment => '待支付',
      RechargeStage.paidWaitingCredit => '支付成功 · 入账处理中',
      RechargeStage.credited => '已到账',
      RechargeStage.closed => '已关闭',
      RechargeStage.needsReview => '待核对',
      RechargeStage.refunded => '已退款',
      RechargeStage.unknown => '状态待核对',
    };
  }

  String get channelLabel {
    return switch (channel) {
      'alipay' => '支付宝',
      'wxpay' => '微信支付',
      null => '支付方式待确认',
      _ => channel!,
    };
  }

  bool get hasCheckout =>
      checkoutState == 'available' &&
      checkoutUrl != null &&
      checkoutUrl!.isNotEmpty;

  static RechargeOrder parse(Map<String, dynamic> map) {
    return RechargeOrder(
      id: readString(map, 'id') ?? '',
      amountMinor: Money.parseMinor(map['amount_minor']) ?? 0,
      currency: readString(map, 'currency') ?? 'CNY',
      channel: readString(map, 'channel'),
      status: readString(map, 'status'),
      paymentStatus: readString(map, 'payment_status'),
      creditStatus: readString(map, 'credit_status'),
      provider: readString(map, 'provider'),
      merchantOrderNo: readString(map, 'merchant_order_no'),
      latePayment: readBool(map, 'late_payment') ?? false,
      createdAt: readDateTime(map, 'created_at'),
      updatedAt: readDateTime(map, 'updated_at'),
      expiresAt: readDateTime(map, 'expires_at'),
      paidAt: readDateTime(map, 'paid_at'),
      creditedAt: readDateTime(map, 'credited_at'),
      closedAt: readDateTime(map, 'closed_at'),
      version: readInt(map, 'version') ?? 0,
      checkoutUrl: readString(map, 'checkout_url'),
      checkoutState: readString(map, 'checkout_state'),
    );
  }
}

class RechargePolicy {
  const RechargePolicy({
    this.provider,
    this.enabled = false,
    this.state,
    this.channels = const <String>[],
    this.defaultChannel,
    this.minMinor,
    this.maxMinor,
  });

  final String? provider;
  final bool enabled;
  final String? state;
  final List<String> channels;
  final String? defaultChannel;
  final int? minMinor;
  final int? maxMinor;

  static RechargePolicy parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return RechargePolicy(
      provider: readString(map, 'provider'),
      enabled: readBool(map, 'enabled') ?? false,
      state: readString(map, 'state'),
      channels: readStringList(map, 'channels'),
      defaultChannel: readString(map, 'default_channel'),
      minMinor: Money.parseMinor(map['min_minor']),
      maxMinor: Money.parseMinor(map['max_minor']),
    );
  }
}

class PublishedPrice {
  const PublishedPrice({
    this.scope,
    this.configured = false,
    this.state,
    this.unitLabel,
    this.unitPrecision,
    this.priceMicros,
    this.inputMicrosPerMillion,
    this.outputMicrosPerMillion,
    this.settlementRule,
    this.rounding,
    this.minChargeMinor,
    this.publishedAt,
  });

  final String? scope;
  final bool configured;
  final String? state;
  final String? unitLabel;
  final int? unitPrecision;
  final int? priceMicros;
  final int? inputMicrosPerMillion;
  final int? outputMicrosPerMillion;
  final String? settlementRule;
  final String? rounding;
  final int? minChargeMinor;
  final DateTime? publishedAt;

  bool get active => configured && state == 'active';

  String get stateLabel {
    if (active) return '价格已发布';
    if (state == 'disabled') return '价格已暂停使用';
    return '单价待公布';
  }

  static PublishedPrice parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return PublishedPrice(
      scope: readString(map, 'scope'),
      configured: readBool(map, 'configured') ?? false,
      state: readString(map, 'state'),
      unitLabel: readString(map, 'unit_label'),
      unitPrecision: readInt(map, 'unit_precision'),
      priceMicros: Money.parseMinor(map['price_micros']),
      inputMicrosPerMillion: Money.parseMinor(map['input_micros_per_million']),
      outputMicrosPerMillion:
          Money.parseMinor(map['output_micros_per_million']),
      settlementRule: readString(map, 'settlement_rule'),
      rounding: readString(map, 'rounding'),
      minChargeMinor: Money.parseMinor(map['min_charge_minor']),
      publishedAt: readDateTime(map, 'published_at'),
    );
  }
}

class PricingInfo {
  const PricingInfo({
    this.currency = 'CNY',
    this.chargingAllowed = false,
    this.automaticCharging = false,
    this.service = const PublishedPrice(),
    this.tiku = const PublishedPrice(),
    this.recharge = const RechargePolicy(),
  });

  final String currency;
  final bool chargingAllowed;
  final bool automaticCharging;
  final PublishedPrice service;
  final PublishedPrice tiku;
  final RechargePolicy recharge;

  static PricingInfo parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return PricingInfo(
      currency: readString(map, 'currency') ?? 'CNY',
      chargingAllowed: readBool(map, 'charging_allowed') ?? false,
      automaticCharging: readBool(map, 'automatic_charging') ?? false,
      service: PublishedPrice.parse(map['service']),
      tiku: PublishedPrice.parse(map['tiku']),
      recharge: RechargePolicy.parse(map['recharge']),
    );
  }
}

class UsageRecord {
  const UsageRecord({
    required this.id,
    this.scope,
    this.status,
    this.inputTokens = 0,
    this.outputTokens = 0,
    this.totalTokens = 0,
    this.usageComplete = false,
    this.costMicros = 0,
    this.chargedMinor = 0,
    this.quoteMinor = 0,
    this.priceVersion,
    this.createdAt,
    this.settledAt,
  });

  final String id;
  final String? scope;
  final String? status;
  final int inputTokens;
  final int outputTokens;
  final int totalTokens;
  final bool usageComplete;
  final int costMicros;
  final int chargedMinor;
  final int quoteMinor;
  final int? priceVersion;
  final DateTime? createdAt;
  final DateTime? settledAt;

  String get scopeLabel => switch (scope) {
        'service_task_point' => '任务点用量',
        'tiku_tokens' => '题库用量',
        _ => '用量记录',
      };

  String get statusLabel {
    if (status == 'settled') return '已结算';
    if (status == 'unpriced') return '用量已记录 · 单价待公布';
    if (status == 'disabled') return '收费未启用';
    if (status == 'quoted') return '已报价';
    if (status == 'reserved') return '费用预占中';
    if (status == 'released') return '预占已释放';
    if (status == 'usage_incomplete') return '用量不完整，未自动扣费';
    return '状态待核对';
  }

  String get chargeLabel {
    if (chargedMinor > 0) return Money(chargedMinor).text;
    return '未扣费';
  }

  static UsageRecord parse(Map<String, dynamic> map) {
    return UsageRecord(
      id: readString(map, 'id') ?? '',
      scope: readString(map, 'scope'),
      status: readString(map, 'status'),
      inputTokens: readInt(map, 'input_tokens') ?? 0,
      outputTokens: readInt(map, 'output_tokens') ?? 0,
      totalTokens: readInt(map, 'total_tokens') ?? 0,
      usageComplete: readBool(map, 'usage_complete') ?? false,
      costMicros: Money.parseMinor(map['cost_micros']) ?? 0,
      chargedMinor: Money.parseMinor(map['charged_minor']) ?? 0,
      quoteMinor: Money.parseMinor(map['quote_minor']) ?? 0,
      priceVersion: readInt(map, 'price_version'),
      createdAt: readDateTime(map, 'created_at'),
      settledAt: readDateTime(map, 'settled_at'),
    );
  }
}

String formatMicrosYuan(int micros) {
  final negative = micros < 0;
  final absolute = micros.abs();
  final yuan = absolute ~/ 1000000;
  final fraction = (absolute % 1000000).toString().padLeft(6, '0');
  var trimmed = fraction;
  while (trimmed.length > 2 && trimmed.endsWith('0')) {
    trimmed = trimmed.substring(0, trimmed.length - 1);
  }
  return '${negative ? '-' : ''}¥$yuan.$trimmed';
}
