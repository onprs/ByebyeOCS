/// 资金只以整数分参与计算与校验；格式化不改变精度。
class Money {
  const Money(this.minor, {this.currency = 'CNY'});

  final int minor;
  final String currency;

  static Money? tryParse(Object? value, {String? currency}) {
    final minor = parseMinor(value);
    if (minor == null) return null;
    return Money(minor,
        currency: (currency == null || currency.isEmpty) ? 'CNY' : currency);
  }

  static int? parseMinor(Object? value) {
    if (value is int) return value;
    if (value is String) {
      final text = value.trim();
      if (text.isEmpty) return null;
      return int.tryParse(text);
    }
    if (value is num) {
      if (value is double && value != value.truncateToDouble()) return null;
      return value.toInt();
    }
    return null;
  }

  bool get isNegative => minor < 0;

  String get amountText => formatMinor(minor);

  String get text {
    final prefix = switch (currency) {
      'CNY' => '¥',
      'USD' => r'$',
      'EUR' => '€',
      _ => '$currency ',
    };
    final sign = minor < 0 ? '-' : '';
    return '$sign$prefix${formatMinor(minor.abs())}';
  }

  String get signedText {
    if (minor > 0) return '+$text';
    return text;
  }

  Money operator +(Money other) {
    _assertSameCurrency(other);
    return Money(minor + other.minor, currency: currency);
  }

  Money operator -(Money other) {
    _assertSameCurrency(other);
    return Money(minor - other.minor, currency: currency);
  }

  void _assertSameCurrency(Money other) {
    if (other.currency != currency) {
      throw ArgumentError('不同币种不能直接相加');
    }
  }

  @override
  bool operator ==(Object other) =>
      other is Money && other.minor == minor && other.currency == currency;

  @override
  int get hashCode => Object.hash(minor, currency);

  @override
  String toString() => text;
}

String formatMinor(int minor) {
  final negative = minor < 0;
  final absolute = minor.abs();
  final yuan = (absolute ~/ 100).toString();
  final cents = (absolute % 100).toString().padLeft(2, '0');
  final grouped = StringBuffer();
  for (var index = 0; index < yuan.length; index++) {
    if (index > 0 && (yuan.length - index) % 3 == 0) grouped.write(',');
    grouped.write(yuan[index]);
  }
  return '${negative ? '-' : ''}$grouped.$cents';
}

/// 输入金额转换为整数分；超过两位小数或非法字符返回 null。
int? parseYuanInput(String raw) {
  final text = raw.trim().replaceAll(',', '');
  if (text.isEmpty) return null;
  if (!RegExp(r'^\d+(\.\d{0,2})?$').hasMatch(text)) return null;
  final parts = text.split('.');
  final yuan = int.tryParse(parts.first);
  if (yuan == null) return null;
  final cents = parts.length == 1
      ? 0
      : int.parse(parts[1].padRight(2, '0').substring(0, 2));
  return yuan * 100 + cents;
}

String formatDateTime(DateTime? value, {bool withSeconds = false}) {
  if (value == null || value.year <= 1) return '—';
  final local = value.toLocal();
  final date = '${local.year}-${_two(local.month)}-${_two(local.day)}';
  final time = '${_two(local.hour)}:${_two(local.minute)}'
      '${withSeconds ? ':${_two(local.second)}' : ''}';
  return '$date $time';
}

String formatShortDateTime(DateTime? value) {
  if (value == null || value.year <= 1) return '—';
  final local = value.toLocal();
  return '${_two(local.month)}-${_two(local.day)} ${_two(local.hour)}:${_two(local.minute)}';
}

String formatDurationSeconds(num? seconds) {
  if (seconds == null || seconds <= 0) return '0 分钟';
  final minutes = seconds / 60;
  if (minutes < 1) return '不足 1 分钟';
  if (minutes < 60) return '${minutes.floor()} 分钟';
  final hours = minutes / 60;
  if (hours < 10) {
    return '${hours.floor()} 小时 ${(minutes % 60).round()} 分钟';
  }
  return '${hours.round()} 小时';
}

String formatPercent(double? fraction) {
  if (fraction == null) return '—';
  final clamped = fraction.clamp(0, 1).toDouble();
  return '${(clamped * 100).round()}%';
}

String _two(int value) => value.toString().padLeft(2, '0');

String formatCount(int value) {
  final text = value.abs().toString();
  final buffer = StringBuffer();
  for (var index = 0; index < text.length; index++) {
    if (index > 0 && (text.length - index) % 3 == 0) buffer.write(',');
    buffer.write(text[index]);
  }
  return '${value < 0 ? '-' : ''}$buffer';
}
