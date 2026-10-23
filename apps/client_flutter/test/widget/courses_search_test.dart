import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:byebyeocs_client/features/courses/courses_page.dart';

import '../helpers.dart';

void main() {
  testWidgets('搜索进行中保留搜索框，不被整页加载态移除', (tester) async {
    final pendingSearch = Completer<http.Response>();
    final server = TestServer();
    var searchRequests = 0;

    Future<http.Response> handler(http.Request request) {
      if (request.url.path == '/api/v1/courses' &&
          request.url.queryParameters.containsKey('search')) {
        searchRequests++;
        return pendingSearch.future;
      }
      return server.call(request);
    }

    await pumpTestPage(tester, const CoursesPage(), handler: handler);
    expect(find.byType(TextField), findsOneWidget);

    await tester.enterText(find.byType(TextField), '设计');
    await tester.pump(const Duration(milliseconds: 400));
    await tester.pump();

    expect(searchRequests, 1);
    // 搜索请求尚未完成时仍能看到并继续编辑搜索词。
    expect(find.byType(TextField), findsOneWidget);

    pendingSearch.complete(
      jsonResponse({'items': coursesFixture, 'next_cursor': ''}),
    );
    await settle(tester);
    expect(find.text('设计思维与创新方法'), findsWidgets);
  });
}
