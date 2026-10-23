import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:byebyeocs_client/app/providers.dart';
import 'package:byebyeocs_client/core/api/api_client.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/models/course.dart';
import 'package:byebyeocs_client/core/models/page.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/core/realtime/realtime_service.dart';
import 'package:byebyeocs_client/core/repositories/learning_repository.dart';
import 'package:byebyeocs_client/core/session/session_manager.dart';
import 'package:byebyeocs_client/features/courses/courses_controller.dart';

import '../helpers.dart';

CourseSummary _course(String id, String title) {
  return CourseSummary.parse(<String, dynamic>{
    ...coursesFixture.first,
    'id': id,
    'title': title,
  });
}

class _ControlledCourseRepository extends LearningRepository {
  _ControlledCourseRepository()
      : super(
          api: ApiClient(
            config: const AppConfig(
              apiBaseUri: null,
              channel: ClientChannel.web,
            ),
            session: SessionManager(
              channel: ClientChannel.web,
              store: InMemoryTokenStore(),
            ),
            httpClient: MockClient((request) async => http.Response('{}', 200)),
          ),
        );

  final Completer<Page<CourseSummary>> slowSearch =
      Completer<Page<CourseSummary>>();
  final Completer<Page<CourseSummary>> slowRefresh =
      Completer<Page<CourseSummary>>();
  final List<String> searches = <String>[];
  int calls = 0;

  @override
  Future<Page<CourseSummary>> fetchCourses({
    String? cursor,
    String search = '',
    int limit = 100,
  }) {
    calls++;
    searches.add(search);
    if (search == '设计') return slowSearch.future;
    if (search == '结构') {
      return Future<Page<CourseSummary>>.value(
        Page<CourseSummary>(
          items: <CourseSummary>[_course('c2', '数据结构与算法')],
        ),
      );
    }
    if (calls == 2) return slowRefresh.future;
    if (calls >= 3) {
      return Future<Page<CourseSummary>>.value(
        Page<CourseSummary>(
          items: <CourseSummary>[_course('c-new', '新账户课程')],
        ),
      );
    }
    return Future<Page<CourseSummary>>.value(
      Page<CourseSummary>(
        items: <CourseSummary>[_course('c1', '设计思维与创新方法')],
      ),
    );
  }
}

ProviderContainer _container(_ControlledCourseRepository repository) {
  final config = const AppConfig(apiBaseUri: null, channel: ClientChannel.web);
  final session = SessionManager(
    channel: ClientChannel.web,
    store: InMemoryTokenStore(),
  );
  return ProviderContainer(
    overrides: [
      currentUserProvider.overrideWithValue(testUser),
      learningRepositoryProvider.overrideWithValue(repository),
      realtimeServiceProvider.overrideWithValue(
        RealtimeService(config: config, session: session),
      ),
    ],
  );
}

void main() {
  test('较慢的旧搜索响应不得覆盖最后输入的结果', () async {
    final repository = _ControlledCourseRepository();
    final container = _container(repository);
    addTearDown(container.dispose);
    container.listen(coursesControllerProvider, (_, __) {});
    await container.read(coursesControllerProvider.future);
    final notifier = container.read(coursesControllerProvider.notifier);

    final slow = notifier.setSearch('设计');
    await Future<void>.delayed(Duration.zero);
    await notifier.setSearch('结构');
    expect(
      container.read(coursesControllerProvider).valueOrNull?.courses.single.id,
      'c2',
    );

    repository.slowSearch.complete(
      Page<CourseSummary>(
        items: <CourseSummary>[_course('c-stale', '过期搜索结果')],
      ),
    );
    await slow;
    await Future<void>.delayed(Duration.zero);

    final state = container.read(coursesControllerProvider).valueOrNull;
    expect(state?.search, '结构');
    expect(state?.courses.single.id, 'c2');
  });

  test('切换账户重建后旧账户在途课程响应不得写入新状态', () async {
    final repository = _ControlledCourseRepository();
    final container = _container(repository);
    addTearDown(container.dispose);
    container.listen(coursesControllerProvider, (_, __) {});
    await container.read(coursesControllerProvider.future);
    final notifier = container.read(coursesControllerProvider.notifier);

    final oldRefresh = notifier.refresh(silent: true);
    await Future<void>.delayed(Duration.zero);
    expect(repository.searches.length, 2);

    container.read(sessionEpochProvider.notifier).bump();
    for (var index = 0; index < 20; index++) {
      await Future<void>.delayed(Duration.zero);
      if (container.read(coursesControllerProvider).valueOrNull?.courses.any(
                (course) => course.id == 'c-new',
              ) ??
          false) {
        break;
      }
    }
    expect(
      container.read(coursesControllerProvider).valueOrNull?.courses.single.id,
      'c-new',
    );

    repository.slowRefresh.complete(
      Page<CourseSummary>(
        items: <CourseSummary>[_course('c-old', '旧账户课程')],
      ),
    );
    await oldRefresh;
    await Future<void>.delayed(Duration.zero);

    expect(
      container.read(coursesControllerProvider).valueOrNull?.courses.single.id,
      'c-new',
    );
  });
}
