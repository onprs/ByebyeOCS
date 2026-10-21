import '../api/json.dart';

class Page<T> {
  const Page({required this.items, this.nextCursor});

  final List<T> items;
  final String? nextCursor;

  bool get hasMore => nextCursor != null && nextCursor!.isNotEmpty;
}

Page<T> parsePage<T>(
  Object? raw,
  T Function(Map<String, dynamic>) itemParser,
) {
  final map = asObject(raw);
  if (map == null) {
    // 兼容直接返回数组的只读列表接口。
    final items = asList(raw)
        .map(asObject)
        .whereType<Map<String, dynamic>>()
        .map(itemParser)
        .toList(growable: false);
    return Page<T>(items: items);
  }
  final items = asList(map['items'])
      .map(asObject)
      .whereType<Map<String, dynamic>>()
      .map(itemParser)
      .toList(growable: false);
  return Page<T>(
    items: items,
    nextCursor: readString(map, 'next_cursor'),
  );
}
