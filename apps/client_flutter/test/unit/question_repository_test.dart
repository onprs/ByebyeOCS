import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:byebyeocs_client/core/api/api_client.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/core/repositories/question_repository.dart';
import 'package:byebyeocs_client/core/session/session_manager.dart';

Map<String, dynamic> _model(String id, String name, int sortOrder) => {
      'id': id,
      'display_name': name,
      'capabilities': ['text'],
      'default': false,
      'sort_order': sortOrder,
      'cache_policy': {'allow': true, 'scope': 'user', 'ttl_days': 7},
      'health': 'healthy',
    };

void main() {
  test('模型目录跟随游标读取全部已发布条目', () async {
    final cursors = <String?>[];
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
        cursors.add(request.url.queryParameters['cursor']);
        expect(request.url.queryParameters['limit'], '100');
        final firstPage = request.url.queryParameters['cursor'] == null;
        return http.Response(
          jsonEncode(
            firstPage
                ? {
                    'items': [_model('qm_1', '第一页模型', 2)],
                    'next_cursor': 'cursor_2',
                  }
                : {
                    'items': [_model('qm_2', '第二页模型', 1)],
                    'next_cursor': null,
                  },
          ),
          200,
          headers: {'content-type': 'application/json'},
        );
      }),
    );

    final offerings = await QuestionRepository(api: api).fetchOfferings();

    expect(cursors, [null, 'cursor_2']);
    expect(offerings.map((offering) => offering.id), ['qm_2', 'qm_1']);
  });
}
