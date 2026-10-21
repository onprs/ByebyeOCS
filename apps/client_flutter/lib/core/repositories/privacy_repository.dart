import '../api/api_client.dart';
import '../models/data_request.dart';
import '../models/page.dart';

class PrivacyRepository {
  PrivacyRepository({required ApiClient api}) : _api = api;

  final ApiClient _api;

  Future<Page<DataRequest>> fetchExports({
    String? cursor,
    int limit = 20,
  }) async {
    final response = await _api.get('me/data-exports', query: {
      'limit': '$limit',
      if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
    });
    return parsePage(response.data, DataRequest.parse);
  }

  Future<Page<DataRequest>> fetchDeletions({
    String? cursor,
    int limit = 20,
  }) async {
    final response = await _api.get('me/deletion-requests', query: {
      'limit': '$limit',
      if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
    });
    return parsePage(response.data, DataRequest.parse);
  }

  Future<DataRequest> fetchExport(String id) async {
    final response = await _api.get('me/data-exports/$id');
    return DataRequest.parse(response.object);
  }

  Future<DataRequest> requestExport() async {
    final response = await _api.post(
      'me/data-exports',
      body: {'format': 'json'},
    );
    return DataRequest.parse(response.object);
  }

  Future<DataRequest> requestDeletion() async {
    final response = await _api.post(
      'me/deletion-requests',
      body: {'confirm': true},
    );
    return DataRequest.parse(response.object);
  }

  Future<ExportDownload> downloadExport(String id) async {
    final response = await _api.get(
      'me/data-exports/$id/download',
      parseJson: false,
    );
    final bytes = response.bytes;
    if (bytes == null) {
      throw StateError('导出文件内容为空');
    }
    final disposition = response.headers['content-disposition'];
    return ExportDownload(
      bytes: bytes,
      filename: _filename(disposition, id),
    );
  }

  String _filename(String? disposition, String id) {
    if (disposition != null) {
      final match = RegExp(r'filename="?([^";]+)"?').firstMatch(disposition);
      final value = match?.group(1)?.trim();
      if (value != null && value.isNotEmpty) {
        return _safeFilename(value);
      }
    }
    return 'byebyeocs-data-$id.json';
  }

  String _safeFilename(String value) {
    final cleaned = value.replaceAll(RegExp(r'[^A-Za-z0-9._-]'), '_');
    if (cleaned.isEmpty) return 'byebyeocs-data-export.json';
    return cleaned.endsWith('.json') ? cleaned : '$cleaned.json';
  }
}
