import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:byebyeocs_client/app/providers.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/models/preferences.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/design/components.dart';
import 'package:byebyeocs_client/design/theme.dart';
import 'package:byebyeocs_client/features/auth/auth_controller.dart';
import 'package:byebyeocs_client/features/courses/course_cover.dart';
import 'package:byebyeocs_client/features/courses/course_detail_page.dart';
import 'package:byebyeocs_client/features/courses/courses_page.dart';
import 'package:byebyeocs_client/features/home/home_page.dart';

import '../helpers.dart';

const _courseId = '22222222-2222-4222-8222-222222222222';
const _digest =
    '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef';
const _coverUrl = 'courses/$_courseId/cover?v=$_digest';
const _coverPath = '/api/v1/courses/$_courseId/cover';

final _coverBytes = Uint8List.fromList(
  base64Decode(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGO4o6HxHwAFPAIsDsQvxQAAAABJRU5ErkJggg==',
  ),
);
final _otherCoverBytes = Uint8List.fromList(
  base64Decode(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGPQCLjzHwAETAJU7P26YgAAAABJRU5ErkJggg==',
  ),
);
final _brokenBytes = Uint8List.fromList([0, 1, 2, 3, 4]);

bool _isCoverRequest(http.Request request) =>
    request.url.path.endsWith('/cover');

http.Response _imageResponse(List<int> bytes) {
  return http.Response.bytes(
    bytes,
    200,
    headers: {'content-type': 'image/jpeg'},
  );
}

http.Response _coursesResponse({String? coverUrl}) {
  return jsonResponse({
    'items': [
      for (final course in coursesFixture)
        {...course, if (coverUrl != null) 'cover_url': coverUrl},
    ],
    'next_cursor': '',
  });
}

http.Response _courseDetailResponse({String? coverUrl}) {
  return jsonResponse({
    ...courseDetailFixture,
    if (coverUrl != null) 'cover_url': coverUrl,
  });
}

/// 从已渲染的封面图片中取回字节，用于确认显示的是哪一次响应。
Uint8List? _displayedCoverBytes(WidgetTester tester) {
  for (final image in tester.widgetList<Image>(find.byType(Image))) {
    final provider = image.image;
    if (provider is ResizeImage) {
      final inner = provider.imageProvider;
      if (inner is MemoryImage) return inner.bytes;
    } else if (provider is MemoryImage) {
      return provider.bytes;
    }
  }
  return null;
}

Future<void> _pumpHarness(
  WidgetTester tester, {
  required Widget child,
  required RequestHandler handler,
}) async {
  SharedPreferences.setMockInitialValues({});
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        appConfigProvider.overrideWithValue(
          const AppConfig(
            apiBaseUri: null,
            channel: ClientChannel.web,
          ).copyWithBase(Uri.parse('https://api.test/api/v1/')),
        ),
        tokenStoreProvider.overrideWithValue(InMemoryTokenStore()),
        httpClientProvider.overrideWithValue(MockClient(handler)),
        authControllerProvider.overrideWith(
          () => FakeAuthController(authenticatedState()),
        ),
      ],
      child: MaterialApp(
        theme: AppTheme.build(ColorSchemeId.graphite, Brightness.light),
        home: Scaffold(body: SafeArea(child: child)),
      ),
    ),
  );
  await settle(tester);
}

extension on AppConfig {
  AppConfig copyWithBase(Uri base) =>
      AppConfig(apiBaseUri: base, channel: channel);
}

void main() {
  testWidgets('首页主视觉与课程卡片显示真实封面', (tester) async {
    var coverRequests = 0;
    final server = TestServer();
    Future<http.Response> handler(http.Request request) async {
      if (_isCoverRequest(request)) {
        coverRequests++;
        expect(request.url.path, _coverPath);
        expect(request.url.queryParameters, {'v': _digest});
        return _imageResponse(_coverBytes);
      }
      if (request.url.path == '/api/v1/courses') {
        return _coursesResponse(coverUrl: _coverUrl);
      }
      return server.call(request);
    }

    await pumpTestPage(tester, const HomePage(), handler: handler);

    // 主视觉与两张课程卡片共用同一封面，只下载一次。
    expect(coverRequests, 1);
    expect(find.byType(Image), findsNWidgets(3));
    expect(find.byType(CourseArt), findsNothing);
    expect(_displayedCoverBytes(tester), _coverBytes);
  });

  testWidgets('手机布局的主视觉同样使用真实封面', (tester) async {
    var coverRequests = 0;
    final server = TestServer();
    Future<http.Response> handler(http.Request request) async {
      if (_isCoverRequest(request)) {
        coverRequests++;
        return _imageResponse(_coverBytes);
      }
      if (request.url.path == '/api/v1/courses') {
        return _coursesResponse(coverUrl: _coverUrl);
      }
      return server.call(request);
    }

    await pumpTestPage(
      tester,
      const HomePage(),
      handler: handler,
      size: const Size(390, 844),
    );

    expect(coverRequests, 1);
    expect(find.byType(Image), findsWidgets);
    expect(find.byType(CourseArt), findsNothing);
  });

  testWidgets('课程页卡片与课程详情显示真实封面', (tester) async {
    var coverRequests = 0;
    final server = TestServer();
    Future<http.Response> handler(http.Request request) async {
      if (_isCoverRequest(request)) {
        coverRequests++;
        return _imageResponse(_coverBytes);
      }
      if (request.url.path == '/api/v1/courses') {
        return _coursesResponse(coverUrl: _coverUrl);
      }
      if (request.url.path == '/api/v1/courses/c1') {
        return _courseDetailResponse(coverUrl: _coverUrl);
      }
      return server.call(request);
    }

    await pumpTestPage(tester, const CoursesPage(), handler: handler);
    expect(find.byType(Image), findsNWidgets(2));
    expect(coverRequests, 1);

    await pumpTestPage(
      tester,
      const CourseDetailPage(courseId: 'c1'),
      handler: handler,
    );
    expect(find.byType(Image), findsOneWidget);
    expect(_displayedCoverBytes(tester), _coverBytes);
  });

  testWidgets('封面请求失败时安静回落到占位图', (tester) async {
    var coverRequests = 0;
    final server = TestServer();
    Future<http.Response> handler(http.Request request) async {
      if (_isCoverRequest(request)) {
        coverRequests++;
        return http.Response('unavailable', 500);
      }
      if (request.url.path == '/api/v1/courses') {
        return _coursesResponse(coverUrl: _coverUrl);
      }
      return server.call(request);
    }

    await pumpTestPage(tester, const CoursesPage(), handler: handler);

    expect(coverRequests, 1);
    expect(find.byType(Image), findsNothing);
    expect(find.byType(CourseArt), findsWidgets);
    expect(find.textContaining('封面'), findsNothing);
  });

  testWidgets('解码失败的封面回落到占位图', (tester) async {
    final server = TestServer();
    Future<http.Response> handler(http.Request request) async {
      if (_isCoverRequest(request)) return _imageResponse(_brokenBytes);
      if (request.url.path == '/api/v1/courses') {
        return _coursesResponse(coverUrl: _coverUrl);
      }
      return server.call(request);
    }

    await pumpTestPage(tester, const CoursesPage(), handler: handler);

    // 真实解码在测试事件循环外完成，失败后由图片自身的错误分支回落到占位图。
    await tester.runAsync(() async {
      await Future<void>.delayed(const Duration(milliseconds: 200));
    });
    await tester.pump();

    expect(
      find.descendant(
        of: find.byType(Image),
        matching: find.byType(CourseArt),
      ),
      findsWidgets,
    );
  });

  testWidgets('没有可用封面时不请求图片，直接使用占位图', (tester) async {
    var coverRequests = 0;
    final server = TestServer();
    Future<http.Response> handler(http.Request request) async {
      if (_isCoverRequest(request)) {
        coverRequests++;
        return _imageResponse(_coverBytes);
      }
      if (request.url.path == '/api/v1/courses') {
        return _coursesResponse();
      }
      return server.call(request);
    }

    await pumpTestPage(tester, const CoursesPage(), handler: handler);

    expect(coverRequests, 0);
    expect(find.byType(Image), findsNothing);
    expect(find.byType(CourseArt), findsWidgets);
  });

  testWidgets('会话切换后旧封面响应不会回填', (tester) async {
    final firstResponse = Completer<http.Response>();
    var coverRequests = 0;
    Future<http.Response> handler(http.Request request) async {
      if (_isCoverRequest(request)) {
        coverRequests++;
        if (coverRequests == 1) return firstResponse.future;
        return _imageResponse(_otherCoverBytes);
      }
      return jsonResponse({'error': {'code': 'not_found'}}, status: 404);
    }

    await _pumpHarness(
      tester,
      child: const CourseCover(path: _coverUrl, seed: 'c1'),
      handler: handler,
    );
    expect(coverRequests, 1);
    expect(find.byType(CourseArt), findsOneWidget);

    final container = ProviderScope.containerOf(
      tester.element(find.byType(CourseCover)),
    );
    container.read(sessionEpochProvider.notifier).bump();
    await settle(tester);

    // 新会话重新请求封面，并取得新响应。
    expect(coverRequests, 2);
    expect(_displayedCoverBytes(tester), _otherCoverBytes);

    // 旧会话的在途响应到达后必须丢弃，不能覆盖当前封面。
    firstResponse.complete(_imageResponse(_coverBytes));
    await settle(tester);
    expect(_displayedCoverBytes(tester), _otherCoverBytes);
  });

  testWidgets('路由切换复用封面缓存，不重复下载', (tester) async {
    var coverRequests = 0;
    Future<http.Response> handler(http.Request request) async {
      if (_isCoverRequest(request)) {
        coverRequests++;
        return _imageResponse(_coverBytes);
      }
      return jsonResponse({'error': {'code': 'not_found'}}, status: 404);
    }

    final showCover = ValueNotifier<bool>(true);
    addTearDown(showCover.dispose);

    await _pumpHarness(
      tester,
      child: ValueListenableBuilder<bool>(
        valueListenable: showCover,
        builder: (context, value, child) => value
            ? const CourseCover(path: _coverUrl, seed: 'c1')
            : const SizedBox.shrink(),
      ),
      handler: handler,
    );
    expect(coverRequests, 1);

    // 离开再回到同一封面，缓存应当复用已下载的字节。
    showCover.value = false;
    await tester.pump();
    showCover.value = true;
    await settle(tester);

    expect(coverRequests, 1);
    expect(_displayedCoverBytes(tester), _coverBytes);
  });
}
