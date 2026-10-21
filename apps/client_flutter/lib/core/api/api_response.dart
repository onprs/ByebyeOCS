import 'dart:typed_data';

import 'json.dart';

class ApiResponse {
  ApiResponse({
    required this.statusCode,
    required this.data,
    required this.headers,
    this.bytes,
  });

  final int statusCode;
  final Object? data;
  final Map<String, String> headers;
  final Uint8List? bytes;

  Map<String, dynamic> get object => objectOrEmpty(data);

  List<dynamic> get list => asList(data);

  String? get etag {
    final raw = headers['etag'];
    if (raw == null || raw.trim().isEmpty) return null;
    return raw.trim();
  }

  int? get headerVersion => parseVersion(etag);

  int? get bodyVersion {
    final map = asObject(data);
    return readInt(map, 'version');
  }

  int? get version => headerVersion ?? bodyVersion;

  String? get requestId => headers['x-request-id'];
}

int? parseVersion(String? etag) {
  if (etag == null) return null;
  final trimmed = etag.trim().replaceAll('"', '');
  if (trimmed.startsWith('v')) {
    return int.tryParse(trimmed.substring(1));
  }
  return int.tryParse(trimmed);
}

String formatVersionEtag(int version) => '"v$version"';
