import 'dart:js_interop';
import 'dart:typed_data';

import 'package:web/web.dart' as web;

import '../../core/models/data_request.dart';

/// Web 先以同源 Cookie 请求受保护下载，再用 Blob 交给浏览器保存。
/// URL 只包含临时 blob，不包含 access/refresh token。
Future<ExportDownloadResult> saveExportDownload({
  required String filename,
  required List<int> bytes,
}) async {
  final safe = filename.replaceAll(RegExp(r'[^A-Za-z0-9._-]'), '_');
  final name = safe.isEmpty ? 'byebyeocs-data-export.json' : safe;
  final blob = web.Blob(
    [Uint8List.fromList(bytes).toJS].toJS,
    web.BlobPropertyBag(type: 'application/json'),
  );
  final url = web.URL.createObjectURL(blob);
  final anchor = web.HTMLAnchorElement()
    ..href = url
    ..download = name
    ..style.display = 'none';
  web.document.body?.append(anchor);
  anchor.click();
  anchor.remove();
  web.URL.revokeObjectURL(url);
  return const ExportDownloadResult(message: '下载已开始');
}
