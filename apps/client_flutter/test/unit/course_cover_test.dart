import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:byebyeocs_client/app/providers.dart';
import 'package:byebyeocs_client/core/api/api_client.dart';
import 'package:byebyeocs_client/core/api/api_error.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/models/course.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/core/repositories/learning_repository.dart';
import 'package:byebyeocs_client/core/session/session_manager.dart';
import 'package:byebyeocs_client/features/courses/course_cover.dart';

const _courseId = '22222222-2222-4222-8222-222222222222';
const _digest =
    '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef';
const _coverPath = 'courses/$_courseId/cover?v=$_digest';
final _coverBytes = Uint8List.fromList(
  base64Decode(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFAAH/q842iQAAAABJRU5ErkJggg==',
  ),
);

String _coverPathFor(String courseId) =>
    'courses/$courseId/cover?v=$_digest';

String _uuidFor(int index) =>
    '33333333-3333-4333-8333-${index.toString().padLeft(12, '0')}';

LearningRepository _repository(
  http.Client client, {
  ClientChannel channel = ClientChannel.web,
  SessionManager? session,
}) {
  return LearningRepository(
    api: ApiClient(
      config: AppConfig(
        apiBaseUri: Uri.parse('https://api.test/api/v1/'),
        channel: channel,
      ),
      session: session ??
          SessionManager(channel: channel, store: InMemoryTokenStore()),
      httpClient: client,
    ),
  );
}

void main() {
  group('课程封面地址解析', () {
    test('读取可选封面地址，缺失或为空表示没有来源', () {
      final summary = CourseSummary.parse({
        'id': 'c1',
        'title': '设计思维与创新方法',
        'cover_url': _coverPath,
      });
      expect(summary.coverUrl, _coverPath);
      expect(
        CourseSummary.parse({'id': 'c1', 'title': '课程'}).coverUrl,
        isNull,
      );
      expect(
        CourseSummary.parse({
          'id': 'c1',
          'title': '课程',
          'cover_url': '   ',
        }).coverUrl,
        isNull,
      );
    });

    test('接受限定形式的根目录相对地址并重建规范化结果', () {
      expect(normalizeCourseCoverPath(_coverPath), _coverPath);
      expect(
        normalizeCourseCoverPath('  courses/$_courseId/cover  '),
        'courses/$_courseId/cover',
      );
      expect(
        normalizeCourseCoverPath(
          'courses/${_courseId.toUpperCase()}/cover?v=$_digest',
        ),
        'courses/${_courseId.toUpperCase()}/cover?v=$_digest',
      );
    });

    test('拒绝绝对网址、路径穿越和其他接口', () {
      final rejected = <String?>[
        null,
        '',
        'https://evil.test/api/v1/courses/$_courseId/cover',
        '//evil.test/api/v1/courses/$_courseId/cover',
        '/courses/$_courseId/cover',
        'courses/../admin/users',
        'courses/$_courseId/cover/../../users',
        'courses/$_courseId/cover/extra',
        'courses/not-a-uuid/cover',
        'courses/$_courseId/snapshot',
        'courses/$_courseId/cover#fragment',
        'courses/$_courseId/cover?token=abc',
        'courses/$_courseId/cover?v=$_digest&redirect=1',
        'courses/$_courseId/cover?v=abc',
        'courses/$_courseId/cover?v=${_digest.toUpperCase()}',
        'courses/$_courseId/cover?v=$_digest&v=$_digest',
        'courses/%2e%2e/cover',
        'api/v1/courses/$_courseId/cover',
      ];
      for (final value in rejected) {
        expect(
          normalizeCourseCoverPath(value),
          isNull,
          reason: '应拒绝 $value',
        );
      }
    });
  });

  group('课程封面请求', () {
    test('按 API 根目录相对地址请求，不重复拼接 /api/v1', () async {
      http.Request? captured;
      final repository = _repository(MockClient((request) async {
        captured = request;
        return http.Response.bytes(
          _coverBytes,
          200,
          headers: {'content-type': 'image/jpeg'},
        );
      }));

      final bytes = await repository.fetchCourseCover(_coverPath);

      expect(captured, isNotNull);
      expect(captured!.method, 'GET');
      expect(captured!.url.path, '/api/v1/courses/$_courseId/cover');
      expect(captured!.url.queryParameters, {'v': _digest});
      expect(bytes, _coverBytes);
    });

    test('复用现有 Bearer 鉴权，不把令牌写进地址', () async {
      final store = InMemoryTokenStore();
      final session = SessionManager(
        channel: ClientChannel.app,
        store: store,
      );
      await session.acceptLogin(
        accessToken: 'access-cover',
        refreshToken: 'refresh-cover',
      );
      http.Request? captured;
      final repository = _repository(
        MockClient((request) async {
          captured = request;
          return http.Response.bytes(
            _coverBytes,
            200,
            headers: {'content-type': 'image/jpeg'},
          );
        }),
        channel: ClientChannel.app,
        session: session,
      );

      await repository.fetchCourseCover(_coverPath);

      expect(captured!.headers['Authorization'], 'Bearer access-cover');
      expect(captured!.url.toString(), isNot(contains('access-cover')));
      expect(captured!.url.toString(), isNot(contains('token')));
    });

    test('非法地址不发起请求，响应不是图片时不返回数据', () async {
      var requests = 0;
      final repository = _repository(MockClient((request) async {
        requests++;
        return http.Response(
          '<html>login</html>',
          200,
          headers: {'content-type': 'text/html'},
        );
      }));

      await expectLater(
        repository.fetchCourseCover('https://evil.test/steal'),
        throwsA(isA<ApiException>()),
      );
      expect(requests, 0);

      await expectLater(
        repository.fetchCourseCover(_coverPath),
        throwsA(isA<ApiException>()),
      );
      expect(requests, 1);
    });
  });

  group('课程封面缓存', () {
    test('同一封面的并发请求合并为一次并缓存字节', () async {
      var fetches = 0;
      final completer = Completer<Uint8List>();
      final store = CourseCoverStore(fetch: (path) {
        fetches++;
        return completer.future;
      });

      final first = store.load(_coverPath);
      final second = store.load(_coverPath);
      completer.complete(_coverBytes);
      final a = await first;
      final b = await second;

      expect(fetches, 1);
      expect(identical(a, b), isTrue);
      expect(store.cachedEntries, 1);
      expect(store.cachedBytes, _coverBytes.length);

      await store.load(_coverPath);
      expect(fetches, 1);
    });

    test('条数超限时淘汰最久未使用的封面', () async {
      final fetches = <String, int>{};
      final store = CourseCoverStore(
        fetch: (path) async {
          fetches[path] = (fetches[path] ?? 0) + 1;
          return Uint8List.fromList([path.length]);
        },
        maxEntries: 2,
      );
      final urlA = _coverPathFor(_uuidFor(1));
      final urlB = _coverPathFor(_uuidFor(2));
      final urlC = _coverPathFor(_uuidFor(3));

      await store.load(urlA);
      await store.load(urlB);
      await store.load(urlA);
      await store.load(urlC);
      expect(store.cachedEntries, 2);

      await store.load(urlB);
      expect(fetches[urlB], 2);
      expect(store.cachedEntries, 2);
    });

    test('字节总量超限时淘汰，且保留最近一张避免反复下载', () async {
      final store = CourseCoverStore(
        fetch: (path) async => Uint8List(4),
        maxBytes: 6,
      );
      final urlA = _coverPathFor(_uuidFor(1));
      final urlB = _coverPathFor(_uuidFor(2));

      await store.load(urlA);
      await store.load(urlB);
      expect(store.cachedEntries, 1);
      expect(store.cachedBytes, 4);

      final oversized = CourseCoverStore(
        fetch: (path) async => Uint8List(10),
        maxBytes: 6,
      );
      await oversized.load(urlA);
      expect(oversized.cachedEntries, 1);
      expect(oversized.cachedBytes, 10);
    });

    test('释放后丢弃在途结果，不再写入缓存', () async {
      final completer = Completer<Uint8List>();
      final store = CourseCoverStore(fetch: (path) => completer.future);
      final pending = store.load(_coverPath);

      store.dispose();
      completer.complete(_coverBytes);

      expect(await pending, _coverBytes);
      expect(store.cachedEntries, 0);
      expect(store.cachedBytes, 0);
      // 已释放的缓存不再保存新字节。
      expect(await store.load(_coverPath), _coverBytes);
      expect(store.cachedEntries, 0);
    });

    test('非法地址不进入缓存也不触发下载', () async {
      var fetches = 0;
      final store = CourseCoverStore(fetch: (path) async {
        fetches++;
        return _coverBytes;
      });

      await expectLater(
        store.load('https://evil.test/cover'),
        throwsA(isA<StateError>()),
      );
      expect(fetches, 0);
      expect(store.cachedEntries, 0);
    });
  });

  test('切换会话后代次缓存重建，旧字节不会跨账号复用', () async {
    var requests = 0;
    final container = ProviderContainer(overrides: [
      appConfigProvider.overrideWithValue(
        const AppConfig(
          apiBaseUri: null,
          channel: ClientChannel.web,
        ).copyWithBase(Uri.parse('https://api.test/api/v1/')),
      ),
      tokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
      httpClientProvider.overrideWithValue(MockClient((request) async {
        requests++;
        return http.Response.bytes(
          _coverBytes,
          200,
          headers: {'content-type': 'image/jpeg'},
        );
      })),
    ]);
    addTearDown(container.dispose);

    final first = container.read(courseCoverStoreProvider);
    await first.load(_coverPath);
    await first.load(_coverPath);
    expect(requests, 1);

    container.read(sessionEpochProvider.notifier).bump();
    final second = container.read(courseCoverStoreProvider);

    expect(identical(first, second), isFalse);
    expect(second.cachedEntries, 0);
    await second.load(_coverPath);
    expect(requests, 2);
  });
}

extension on AppConfig {
  AppConfig copyWithBase(Uri base) =>
      AppConfig(apiBaseUri: base, channel: channel);
}
