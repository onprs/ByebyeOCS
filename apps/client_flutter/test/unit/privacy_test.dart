import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:byebyeocs_client/core/api/api_client.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/core/models/data_request.dart';
import 'package:byebyeocs_client/core/repositories/privacy_repository.dart';
import 'package:byebyeocs_client/core/session/session_manager.dart';

void main() {
  test('导出列表解析状态，下载使用受保护 GET 并读取文件名', () async {
    late http.Request downloadRequest;
    final api = ApiClient(
      config: AppConfig(
        apiBaseUri: Uri.parse('https://api.test/api/v1/'),
        channel: ClientChannel.web,
      ),
      session: SessionManager(
        channel: ClientChannel.web,
        store: InMemoryTokenStore(),
      ),
      httpClient: MockClient((request) async {
        if (request.url.path.endsWith('/download')) {
          downloadRequest = request;
          return http.Response.bytes(
            Uint8List.fromList(utf8.encode('{"export":true}')),
            200,
            headers: {
              'content-type': 'application/json',
              'content-disposition': 'attachment; filename="account-data.json"',
            },
          );
        }
        final isDeletion = request.url.path.endsWith('/deletion-requests');
        return http.Response(
          jsonEncode({
            'items': [
              isDeletion
                  ? {
                      'id': 'dr1',
                      'kind': 'deletion',
                      'status': 'needs_review',
                      'created_at': '2026-10-21T06:00:00Z',
                      'review_reason': '资金待核对',
                    }
                  : {
                      'id': 'de1',
                      'kind': 'export',
                      'status': 'ready',
                      'created_at': '2026-10-21T06:00:00Z',
                      'updated_at': '2026-10-21T06:05:00Z',
                    },
            ],
            'next_cursor': '',
          }),
          200,
          headers: {'content-type': 'application/json'},
        );
      }),
    );
    final repository = PrivacyRepository(api: api);

    final exports = await repository.fetchExports();
    expect(exports.items.single.status, 'ready');
    expect(exports.items.single.id, 'de1');

    final download = await repository.downloadExport('de1');
    expect(download.filename, 'account-data.json');
    expect(utf8.decode(download.bytes), '{"export":true}');
    expect(downloadRequest.method, 'GET');
    expect(downloadRequest.url.query, isEmpty);
    expect(downloadRequest.headers.containsKey('Authorization'), isFalse);
    expect(
      downloadRequest.url.toString(),
      'https://api.test/api/v1/me/data-exports/de1/download',
    );
  });

  test('删除申请状态与核对原因来自服务端', () async {
    final api = ApiClient(
      config: AppConfig(
        apiBaseUri: Uri.parse('https://api.test/api/v1/'),
        channel: ClientChannel.web,
      ),
      session: SessionManager(
        channel: ClientChannel.web,
        store: InMemoryTokenStore(),
      ),
      httpClient: MockClient((request) async {
        return http.Response(
          jsonEncode({
            'items': [
              {
                'id': 'dr1',
                'kind': 'deletion',
                'status': 'needs_review',
                'review_reason': '资金与保留记录待核对',
              },
            ],
            'next_cursor': '',
          }),
          200,
          headers: {'content-type': 'application/json'},
        );
      }),
    );
    final page = await PrivacyRepository(api: api).fetchDeletions();
    expect(page.items.single.status, 'needs_review');
    expect(page.items.single.statusLabel, '待人工核对');
    expect(page.items.single.detailLabel, '资金与保留记录待核对');
    expect(
      DataRequest.parse({
        'id': 'd',
        'kind': 'deletion',
        'status': 'deferred',
      }).statusLabel,
      '已延后，删除尚未完成',
    );
    expect(
      DataRequest.parse({
        'id': 'r',
        'kind': 'deletion',
        'status': 'rejected',
      }).statusLabel,
      '已驳回，可重新验证',
    );
    expect(
      DataRequest.parse({
        'id': 'c',
        'kind': 'deletion',
        'status': 'completed',
      }).statusLabel,
      '账号已关闭',
    );
  });
}
