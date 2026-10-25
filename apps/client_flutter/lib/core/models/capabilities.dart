import '../api/json.dart';

class AuthCapability {
  const AuthCapability({
    this.provider,
    this.methods = const <String>[],
    this.qrScan = false,
    this.verification,
    this.enabled,
    this.configured,
  });

  final String? provider;
  final List<String> methods;
  final bool qrScan;
  final String? verification;
  final bool? enabled;
  final bool? configured;

  bool get passwordAvailable =>
      methods.contains('password') &&
      verification != 'disabled' &&
      enabled != false;

  bool get explicitlyUnavailable =>
      enabled == false ||
      configured == false ||
      verification == 'disabled' ||
      (enabled == true && methods.isEmpty) ||
      (methods.isNotEmpty && !methods.contains('password'));

  bool get qrUnsupported => true;
}

class AppearanceCapability {
  const AppearanceCapability({
    this.defaultColorScheme = 'graphite',
    this.defaultThemeMode = 'system',
    this.colorSchemes = const ['graphite', 'grove', 'indigo'],
    this.themeModes = const ['system', 'light', 'dark'],
  });

  final String defaultColorScheme;
  final String defaultThemeMode;
  final List<String> colorSchemes;
  final List<String> themeModes;
}

class PricingCapability {
  const PricingCapability({
    this.serviceConfigured,
    this.tikuConfigured,
    this.automaticCharging,
  });

  final bool? serviceConfigured;
  final bool? tikuConfigured;
  final bool? automaticCharging;

  String get serviceLabel {
    if (serviceConfigured == true && automaticCharging == true) {
      return '按已发布单价结算';
    }
    return '单价待公布，暂未自动扣费';
  }
}

class PaymentChannel {
  const PaymentChannel({
    required this.id,
    required this.name,
    this.available = true,
    this.unavailableReason,
  });

  final String id;
  final String name;
  final bool available;
  final String? unavailableReason;
}

class TaskTypeCapability {
  const TaskTypeCapability({
    required this.id,
    required this.name,
    this.available = true,
  });

  final String id;
  final String name;
  final bool available;
}

/// 合并认证能力与服务能力端点的结果；未知能力保持未知，不默认成功。
class Capabilities {
  const Capabilities({
    this.apiVersion,
    this.audience,
    this.minimumClientVersion,
    this.auth = const AuthCapability(),
    this.features = const <String, bool>{},
    this.appearance = const AppearanceCapability(),
    this.pricing = const PricingCapability(),
    this.paymentChannels = const <PaymentChannel>[],
    this.taskTypes = const <TaskTypeCapability>[],
  });

  final String? apiVersion;
  final String? audience;
  final String? minimumClientVersion;
  final AuthCapability auth;
  final Map<String, bool> features;
  final AppearanceCapability appearance;
  final PricingCapability pricing;
  final List<PaymentChannel> paymentChannels;
  final List<TaskTypeCapability> taskTypes;

  bool feature(String key) => features[key] == true;

  bool get courseSyncAvailable => features['course_sync'] != false;
  bool get schedulingAvailable => features['scheduling'] != false;
  bool get walletAvailable => features['wallet'] != false;
  bool get paymentsAvailable =>
      features['payments'] == true && paymentChannels.isNotEmpty;
  bool get questionsAvailable => features['tiku'] == true;
  bool get questionsExplicitlyDisabled => features['tiku'] == false;
  bool get realtimeAvailable => features['events'] != false;

  static Capabilities parse(Object? raw) {
    final map = asObject(raw);
    if (map == null) return const Capabilities();
    final identity = asObject(map['identity']);
    final authentication = asObject(map['authentication']);
    final auth = AuthCapability(
      provider: readString(identity, 'provider'),
      methods: _stringSet(
        identity?['methods'] ?? authentication?['login_methods'],
      ),
      qrScan: readBool(identity, 'qr_scan') ?? false,
      verification: readString(identity, 'verification'),
      enabled: readBool(authentication, 'enabled'),
      configured: readBool(authentication, 'configured'),
    );
    final featureMap = asObject(map['features']) ?? const <String, dynamic>{};
    final features = <String, bool>{};
    featureMap.forEach((key, value) {
      if (value is bool) features[key] = value;
    });
    for (final key in const [
      'course_sync',
      'scheduling',
      'task_execution',
      'wallet',
      'payments',
      'tiku',
      'preferences',
      'events',
    ]) {
      final value = readBool(map, key);
      if (value != null) features[key] = value;
    }
    final appearanceMap = asObject(map['appearance']);
    final appearance = AppearanceCapability(
      defaultColorScheme:
          readString(appearanceMap, 'default_color_scheme') ?? 'graphite',
      defaultThemeMode:
          readString(appearanceMap, 'default_theme_mode') ?? 'system',
      colorSchemes: _stringSet(appearanceMap?['color_schemes']),
      themeModes: _stringSet(appearanceMap?['theme_modes']),
    );
    final pricingMap = asObject(map['pricing']);
    final pricing = PricingCapability(
      serviceConfigured: readBool(pricingMap, 'service_configured'),
      tikuConfigured: readBool(pricingMap, 'tiku_configured'),
      automaticCharging: readBool(pricingMap, 'automatic_charging'),
    );
    return Capabilities(
      apiVersion: readString(map, 'api_version'),
      audience: readString(map, 'audience'),
      minimumClientVersion: readString(map, 'minimum_client_version'),
      auth: auth,
      features: features,
      appearance: appearance,
      pricing: pricing,
      paymentChannels: _parseChannels(
        map['payment_channels'] ?? map['channels'] ?? map['payments'],
      ),
      taskTypes: _parseTaskTypes(map['task_types']),
    );
  }

  Capabilities merge(Capabilities other) {
    final mergedFeatures = <String, bool>{...features, ...other.features};
    final mergedAuth = other.auth.methods.isNotEmpty ||
            other.auth.verification != null ||
            other.auth.enabled != null
        ? other.auth
        : auth;
    return Capabilities(
      apiVersion: other.apiVersion ?? apiVersion,
      audience: audience ?? other.audience,
      minimumClientVersion: other.minimumClientVersion ?? minimumClientVersion,
      auth: mergedAuth,
      features: mergedFeatures,
      appearance: other.appearance.colorSchemes.isNotEmpty
          ? other.appearance
          : appearance,
      pricing: other.pricing,
      paymentChannels: other.paymentChannels.isNotEmpty
          ? other.paymentChannels
          : paymentChannels,
      taskTypes: other.taskTypes.isNotEmpty ? other.taskTypes : taskTypes,
    );
  }

  static List<String> _stringSet(Object? value) {
    if (value is List) {
      return value.whereType<String>().toList(growable: false);
    }
    return const <String>[];
  }

  static List<PaymentChannel> _parseChannels(Object? value) {
    if (value is! List) return const <PaymentChannel>[];
    final channels = <PaymentChannel>[];
    for (final item in value) {
      if (item is String) {
        channels.add(PaymentChannel(id: item, name: _channelName(item)));
        continue;
      }
      final map = asObject(item);
      if (map == null) continue;
      final id = readString(map, 'id') ?? readString(map, 'channel');
      if (id == null) continue;
      channels.add(
        PaymentChannel(
          id: id,
          name: readString(map, 'name') ?? _channelName(id),
          available:
              readBool(map, 'available') ?? readBool(map, 'enabled') ?? true,
          unavailableReason: readString(map, 'unavailable_reason'),
        ),
      );
    }
    return channels;
  }

  static List<TaskTypeCapability> _parseTaskTypes(Object? value) {
    if (value is! List) return const <TaskTypeCapability>[];
    final types = <TaskTypeCapability>[];
    for (final item in value) {
      if (item is String) {
        types.add(TaskTypeCapability(id: item, name: _taskTypeName(item)));
        continue;
      }
      final map = asObject(item);
      if (map == null) continue;
      final id = readString(map, 'id') ?? readString(map, 'kind');
      if (id == null) continue;
      types.add(
        TaskTypeCapability(
          id: id,
          name: readString(map, 'name') ?? _taskTypeName(id),
          available: readBool(map, 'available') ?? true,
        ),
      );
    }
    return types;
  }

  static String _channelName(String id) {
    return switch (id) {
      'alipay' => '支付宝',
      'wxpay' || 'wechat' => '微信支付',
      _ => id,
    };
  }

  static String _taskTypeName(String id) {
    return switch (id) {
      'video' => '视频',
      'document' => '文档',
      'audio' => '音频',
      'quiz' => '测验',
      'other' => '其他',
      _ => id,
    };
  }
}
