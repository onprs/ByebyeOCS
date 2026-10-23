import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:byebyeocs_client/app/providers.dart';
import 'package:byebyeocs_client/core/api/api_client.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/models/page.dart';
import 'package:byebyeocs_client/core/models/plan.dart';
import 'package:byebyeocs_client/core/models/task.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/core/realtime/realtime_service.dart';
import 'package:byebyeocs_client/core/repositories/learning_repository.dart';
import 'package:byebyeocs_client/core/session/session_manager.dart';
import 'package:byebyeocs_client/features/plans/plans_controller.dart';

import '../helpers.dart';

LearningTask _task(String id) {
  return LearningTask.parse(<String, dynamic>{
    ...taskFixtures[0],
    'id': id,
  });
}

LearningPlan _plan(String id, String name) {
  return LearningPlan.parse(<String, dynamic>{
    ...planFixture,
    'id': id,
    'name': name,
  });
}

class _PagedPlansRepository extends LearningRepository {
  _PagedPlansRepository()
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

  final Completer<Page<LearningTask>> stalePage =
      Completer<Page<LearningTask>>();
  final List<(String?, String?)> taskRequests = <(String?, String?)>[];

  @override
  Future<Page<LearningPlan>> fetchPlans(
      {String? cursor, int limit = 50}) async {
    return Page<LearningPlan>(items: <LearningPlan>[
      _plan('p1', '第一安排'),
      _plan('p2', '第二安排'),
    ]);
  }

  @override
  Future<Page<LearningTask>> fetchTasks({
    String? cursor,
    String? planId,
    String? courseId,
    List<String> states = const <String>[],
    int limit = 50,
  }) {
    taskRequests.add((planId, cursor));
    if (planId == 'p1' && cursor == 'p1-next') {
      return stalePage.future;
    }
    if (planId == 'p2') {
      return Future<Page<LearningTask>>.value(
        Page<LearningTask>(items: <LearningTask>[_task('task-p2')]),
      );
    }
    return Future<Page<LearningTask>>.value(
      Page<LearningTask>(
        items: <LearningTask>[_task('task-p1')],
        nextCursor: 'p1-next',
      ),
    );
  }
}

void main() {
  test('加载更多追加下一页，并在切换安排后丢弃旧游标结果', () async {
    final repository = _PagedPlansRepository();
    final config =
        const AppConfig(apiBaseUri: null, channel: ClientChannel.web);
    final container = ProviderContainer(
      overrides: [
        currentUserProvider.overrideWithValue(testUser),
        learningRepositoryProvider.overrideWithValue(repository),
        realtimeServiceProvider.overrideWithValue(
          RealtimeService(
            config: config,
            session: SessionManager(
              channel: ClientChannel.web,
              store: InMemoryTokenStore(),
            ),
          ),
        ),
      ],
    );
    addTearDown(container.dispose);
    container.listen(plansControllerProvider, (_, __) {});

    await container.read(plansControllerProvider.future);
    final notifier = container.read(plansControllerProvider.notifier);
    expect(container.read(plansControllerProvider).valueOrNull?.tasksHasMore,
        isTrue);

    // 第一页之后开始翻页；响应尚未返回时切换安排。
    final loading = notifier.loadMoreTasks();
    await Future<void>.delayed(Duration.zero);
    expect(repository.taskRequests, contains(('p1', 'p1-next')));

    await notifier.selectPlan('p2');
    expect(container.read(plansControllerProvider).valueOrNull?.selectedPlanId,
        'p2');
    expect(container.read(plansControllerProvider).valueOrNull?.tasksHasMore,
        isFalse);

    repository.stalePage.complete(
      Page<LearningTask>(
        items: <LearningTask>[_task('task-p1-stale')],
        nextCursor: 'p1-stale-next',
      ),
    );
    await loading;

    final state = container.read(plansControllerProvider).valueOrNull;
    expect(state?.selectedPlanId, 'p2');
    expect(state?.tasks.map((task) => task.id), <String>['task-p2']);
    expect(state?.tasksHasMore, isFalse);
    expect(state?.loadingMoreTasks, isFalse);
  });

  test('刷新期间到达的旧翻页结果不得追加到新列表', () async {
    final repository = _PagedPlansRepository();
    final config =
        const AppConfig(apiBaseUri: null, channel: ClientChannel.web);
    final container = ProviderContainer(
      overrides: [
        currentUserProvider.overrideWithValue(testUser),
        learningRepositoryProvider.overrideWithValue(repository),
        realtimeServiceProvider.overrideWithValue(
          RealtimeService(
            config: config,
            session: SessionManager(
              channel: ClientChannel.web,
              store: InMemoryTokenStore(),
            ),
          ),
        ),
      ],
    );
    addTearDown(container.dispose);
    container.listen(plansControllerProvider, (_, __) {});

    await container.read(plansControllerProvider.future);
    final notifier = container.read(plansControllerProvider.notifier);
    final loading = notifier.loadMoreTasks();
    await Future<void>.delayed(Duration.zero);

    await notifier.refresh(silent: true);
    repository.stalePage.complete(
      Page<LearningTask>(
        items: <LearningTask>[_task('task-p1-stale')],
        nextCursor: 'p1-stale-next',
      ),
    );
    await loading;

    final state = container.read(plansControllerProvider).valueOrNull;
    expect(state?.tasks.map((task) => task.id), <String>['task-p1']);
    expect(state?.tasksCursor, 'p1-next');
    expect(state?.loadingMoreTasks, isFalse);
  });

  test('Provider 销毁后迟到的翻页结果不再写状态', () async {
    final repository = _PagedPlansRepository();
    final config =
        const AppConfig(apiBaseUri: null, channel: ClientChannel.web);
    final container = ProviderContainer(
      overrides: [
        currentUserProvider.overrideWithValue(testUser),
        learningRepositoryProvider.overrideWithValue(repository),
        realtimeServiceProvider.overrideWithValue(
          RealtimeService(
            config: config,
            session: SessionManager(
              channel: ClientChannel.web,
              store: InMemoryTokenStore(),
            ),
          ),
        ),
      ],
    );
    container.listen(plansControllerProvider, (_, __) {});
    await container.read(plansControllerProvider.future);

    final loading =
        container.read(plansControllerProvider.notifier).loadMoreTasks();
    await Future<void>.delayed(Duration.zero);
    expect(repository.taskRequests, contains(('p1', 'p1-next')));

    container.dispose();
    repository.stalePage.complete(
      Page<LearningTask>(
        items: <LearningTask>[_task('task-p1-stale')],
        nextCursor: 'p1-stale-next',
      ),
    );
    await loading;
  });

  test('加载更多成功后按游标追加并保留分组所需的任务事实', () async {
    final repository = _PagedPlansRepository();
    final config =
        const AppConfig(apiBaseUri: null, channel: ClientChannel.web);
    final container = ProviderContainer(
      overrides: [
        currentUserProvider.overrideWithValue(testUser),
        learningRepositoryProvider.overrideWithValue(repository),
        realtimeServiceProvider.overrideWithValue(
          RealtimeService(
            config: config,
            session: SessionManager(
              channel: ClientChannel.web,
              store: InMemoryTokenStore(),
            ),
          ),
        ),
      ],
    );
    addTearDown(container.dispose);
    container.listen(plansControllerProvider, (_, __) {});

    await container.read(plansControllerProvider.future);
    // 让翻页立即返回，验证正常追加路径。
    repository.stalePage.complete(
      Page<LearningTask>(
        items: <LearningTask>[_task('task-p1-older')],
        nextCursor: null,
      ),
    );
    await container.read(plansControllerProvider.notifier).loadMoreTasks();

    final state = container.read(plansControllerProvider).valueOrNull;
    expect(
      state?.tasks.map((task) => task.id),
      <String>['task-p1', 'task-p1-older'],
    );
    expect(state?.tasksHasMore, isFalse);
    expect(state?.loadingMoreTasks, isFalse);
    expect(state?.refreshingTasks, isFalse);
  });
}
