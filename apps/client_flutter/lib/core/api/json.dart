import 'dart:math';

/// 集中解析服务端 JSON，未知或类型不符的字段返回 null，由展示层明确处理。
Map<String, dynamic>? asObject(Object? value) {
  if (value is Map<String, dynamic>) return value;
  if (value is Map) {
    return value.map((key, item) => MapEntry(key.toString(), item));
  }
  return null;
}

List<dynamic> asList(Object? value) {
  if (value is List) return value;
  return const <dynamic>[];
}

Map<String, dynamic> objectOrEmpty(Object? value) =>
    asObject(value) ?? const <String, dynamic>{};

String? readString(Map<String, dynamic>? source, String key) {
  final value = source?[key];
  if (value is String) {
    final trimmed = value.trim();
    return trimmed.isEmpty ? null : trimmed;
  }
  return null;
}

String readStringOr(
        Map<String, dynamic>? source, String key, String fallback) =>
    readString(source, key) ?? fallback;

String? readStringAlias(Map<String, dynamic>? source, List<String> keys) {
  for (final key in keys) {
    final value = readString(source, key);
    if (value != null) return value;
  }
  return null;
}

int? readInt(Map<String, dynamic>? source, String key) {
  final value = source?[key];
  if (value is int) return value;
  if (value is num) return value.toInt();
  if (value is String) return int.tryParse(value);
  return null;
}

int? readIntAlias(Map<String, dynamic>? source, List<String> keys) {
  for (final key in keys) {
    final value = readInt(source, key);
    if (value != null) return value;
  }
  return null;
}

double? readDouble(Map<String, dynamic>? source, String key) {
  final value = source?[key];
  if (value is double) return value;
  if (value is num) return value.toDouble();
  if (value is String) return double.tryParse(value);
  return null;
}

bool? readBool(Map<String, dynamic>? source, String key) {
  final value = source?[key];
  if (value is bool) return value;
  if (value is num) return value != 0;
  if (value is String) {
    switch (value.toLowerCase()) {
      case 'true':
      case '1':
      case 'yes':
        return true;
      case 'false':
      case '0':
      case 'no':
        return false;
    }
  }
  return null;
}

DateTime? readDateTime(Map<String, dynamic>? source, String key) {
  final value = readString(source, key);
  if (value == null) return null;
  final parsed = DateTime.tryParse(value)?.toUtc();
  if (parsed == null || parsed.year <= 1) return null;
  return parsed;
}

DateTime? readDateTimeAlias(Map<String, dynamic>? source, List<String> keys) {
  for (final key in keys) {
    final value = readDateTime(source, key);
    if (value != null) return value;
  }
  return null;
}

List<String> readStringList(Map<String, dynamic>? source, String key) {
  return asList(source?[key]).whereType<String>().toList(growable: false);
}

List<String> readStringListAlias(
  Map<String, dynamic>? source,
  List<String> keys,
) {
  for (final key in keys) {
    final values = readStringList(source, key);
    if (values.isNotEmpty) return values;
  }
  return const <String>[];
}

/// 生成幂等键。只包含安全随机十六进制字符，长度满足服务端约束。
String newIdempotencyKey([Random? random]) {
  final source = random ?? Random.secure();
  final buffer = StringBuffer('idem_');
  for (var index = 0; index < 24; index++) {
    buffer.write(source.nextInt(16).toRadixString(16));
  }
  return buffer.toString();
}
