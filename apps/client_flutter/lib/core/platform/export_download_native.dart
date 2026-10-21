import 'dart:io';
import 'dart:typed_data';

import 'package:path_provider/path_provider.dart';

import '../../core/models/data_request.dart';

/// 原生端写入应用私有目录；不把令牌拼接到任何 URL，也不写日志。
Future<ExportDownloadResult> saveExportDownload({
  required String filename,
  required List<int> bytes,
}) async {
  final safe = filename.replaceAll(RegExp(r'[^A-Za-z0-9._-]'), '_');
  final name = safe.isEmpty ? 'byebyeocs-data-export.json' : safe;
  final directory = await getApplicationSupportDirectory();
  final exports = Directory('${directory.path}/exports');
  if (!await exports.exists()) {
    await exports.create(recursive: true);
  }
  final file = File('${exports.path}/$name');
  await file.writeAsBytes(Uint8List.fromList(bytes), flush: true);
  return ExportDownloadResult(
    message: '导出文件已保存到应用私有目录',
    path: file.path,
  );
}
