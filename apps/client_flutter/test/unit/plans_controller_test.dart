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

class _ControlledLearningRepository extends LearningRepository {
  _ControlledLearningRepository()
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

  /// 首次读取 p1 任务用于初始化；后续 p1 刷新由测试控制完成时机。
  final Completer<Page<LearningTask>> p1Refresh =
      Completer<Page<LearningTask>>();
  final List<String?> requestedPlanIds = <String?>[];

  LearningPlan get p1 =>
      LearningPlan.parse(Map<String, dynamic>.from(planFixture));
  LearningPlan get p2 => LearningPlan.parse(
        <String, dynamic>{
          ...planFixture,
          'id': 'p2',
          'name': '第二安排',
        },
      );

  @override
  Future<Page<LearningPlan>> fetchPlans(
      {String? cursor, int limit = 50}) async {
    return Page<LearningPlan>(items: <LearningPlan>[p1, p2]);
  }

  @override
  Future<Page<LearningTask>> fetchTasks({
    String? cursor,
    String? planId,
    String? courseId,
    List<String> states = const <String>[],
    int limit = 50,
  }) {
    requestedPlanIds.add(planId);
    if (planId == 'p1' &&
        requestedPlanIds.where((String? id) => id == 'p1').length > 1) {
      return p1Refresh.future;
    }
    final fixture = planId == 'p2' ? taskFixtures[1] : taskFixtures[0];
    final task = LearningTask.parse(<String, dynamic>{
      ...fixture,
      'id': planId == 'p2' ? 'task-p2' : 'task-p1',
    });
    return Future<Page<LearningTask>>.value(
      Page<LearningTask>(items: <LearningTask>[task]),
    );
  }
}

void main() {
  test('切换安排时旧安排在途任务不得覆盖新选择', () async {
    final repository = _ControlledLearningRepository();
    final config =
        const AppConfig(apiBaseUri: null, channel: ClientChannel.web);
    final session = SessionManager(
      channel: ClientChannel.web,
      store: InMemoryTokenStore(),
    );
    final container = ProviderContainer(
      overrides: [
        currentUserProvider.overrideWithValue(testUser),
        learningRepositoryProvider.overrideWithValue(repository),
        realtimeServiceProvider.overrideWithValue(
          RealtimeService(config: config, session: session),
        ),
      ],
    );
    addTearDown(container.dispose);
    container.listen(plansControllerProvider, (_, __) {});

    await container.read(plansControllerProvider.future);
    final notifier = container.read(plansControllerProvider.notifier);
    expect(container.read(plansControllerProvider).valueOrNull?.selectedPlanId,
        'p1');
    expect(
      container
          .read(plansControllerProvider)
          .valueOrNull
          ?.tasks
          .map((task) => task.id),
      <String>['task-p1'],
    );

    // 让 p1 的刷新停在在途状态，再切换到 p2。
    final refreshing = notifier.refresh(silent: true);
    await Future<void>.delayed(Duration.zero);
    expect(repository.requestedPlanIds.where((id) => id == 'p1').length, 2);

    await notifier.selectPlan('p2');
    expect(container.read(plansControllerProvider).valueOrNull?.selectedPlanId,
        'p2');
    expect(repository.requestedPlanIds, contains('p2'));

    // p1 的旧响应此时才返回，不能写入 p2 的界面。
    repository.p1Refresh.complete(
      Page<LearningTask>(
        items: <LearningTask>[
          LearningTask.parse(<String, dynamic>{
            ...taskFixtures[0],
            'id': 'task-p1-stale',
          }),
        ],
      ),
    );
    await refreshing;
    await Future<void>.delayed(Duration.zero);

    final state = container.read(plansControllerProvider).valueOrNull;
    expect(state?.selectedPlanId, 'p2');
    expect(state?.tasks.map((task) => task.id), <String>['task-p2']);
    expect(state?.refreshingTasks, isFalse);
  });

  test('切回仍有旧请求在途的安排时必须重新请求，不能停在空列表', () async {
    final repository = _ControlledLearningRepository();
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

    // p1 刷新停在在途状态，切到 p2 后立即切回 p1。
    final refreshing = notifier.refresh(silent: true);
    await Future<void>.delayed(Duration.zero);
    await notifier.selectPlan('p2');
    final reselect = notifier.selectPlan('p1');
    await Future<void>.delayed(Duration.zero);

    // 旧 p1 请求已过期，切回 p1 必须发出新的 p1 请求。
    expect(repository.requestedPlanIds.where((id) => id == 'p1').length, 3);

    repository.p1Refresh.complete(
      Page<LearningTask>(
        items: <LearningTask>[
          LearningTask.parse(<String, dynamic>{
            ...taskFixtures[0],
            'id': 'task-p1-reloaded',
          }),
        ],
      ),
    );
    await Future.wait<void>(<Future<void>>[refreshing, reselect]);
    await Future<void>.delayed(Duration.zero);

    final state = container.read(plansControllerProvider).valueOrNull;
    expect(state?.selectedPlanId, 'p1');
    expect(state?.tasks.map((task) => task.id), <String>['task-p1-reloaded']);
    expect(state?.refreshingTasks, isFalse);
  });
}
