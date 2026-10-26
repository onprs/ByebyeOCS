import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:byebyeocs_client/app/providers.dart';
import 'package:byebyeocs_client/core/api/api_client.dart';
import 'package:byebyeocs_client/core/api/api_error.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/models/course.dart';
import 'package:byebyeocs_client/core/models/page.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/core/realtime/realtime_service.dart';
import 'package:byebyeocs_client/core/repositories/learning_repository.dart';
import 'package:byebyeocs_client/core/session/session_manager.dart';
import 'package:byebyeocs_client/features/auth/auth_controller.dart';
import 'package:byebyeocs_client/features/courses/course_sync_controller.dart';
import 'package:byebyeocs_client/features/courses/courses_controller.dart';

import '../helpers.dart';

CourseSummary _course({DateTime? syncedAt}) {
  return CourseSummary(
    id: 'c1',
    title: '设计思维与创新方法',
    chapterCount: 2,
    taskPointCount: 2,
    incompleteTaskPointCount: 0,
    syncedAt: syncedAt,
  );
}

CourseSyncResult _sync(
  String id,
  String status, {
  int courseCount = 0,
  String? errorCode,
  DateTime? finishedAt,
}) {
  return CourseSyncResult(
    id: id,
    status: status,
    courseCount: courseCount,
    errorCode: errorCode,
    requestedAt: finishedAt,
    finishedAt: finishedAt,
  );
}

class _SyncCourseRepository extends LearningRepository {
  _SyncCourseRepository({
    required this.courses,
    this.latest,
    this.requestResult,
    this.pollResults = const <CourseSyncResult>[],
  }) : super(
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

  List<CourseSummary> courses;
  CourseSyncResult? latest;
  CourseSyncResult? requestResult;
  ApiException? requestError;
  List<CourseSyncResult> pollResults;
  Completer<CourseSyncResult?>? latestGate;
  Completer<CourseSyncResult>? pollGate;
  Completer<CourseSyncResult>? requestGate;
  int detailCalls = 0;

  int latestCalls = 0;
  int requestCalls = 0;
  int pollCalls = 0;
  int courseCalls = 0;

  @override
  Future<Page<CourseSummary>> fetchCourses({
    String? cursor,
    String search = '',
    int limit = 100,
  }) async {
    courseCalls++;
    return Page<CourseSummary>(items: courses);
  }

  @override
  Future<CourseSyncResult?> fetchLatestCourseSync() {
    latestCalls++;
    final gate = latestGate;
    if (gate != null) return gate.future;
    return Future<CourseSyncResult?>.value(latest);
  }

  @override
  Future<CourseSyncResult> requestCourseSync() {
    requestCalls++;
    if (requestGate != null) return requestGate!.future;
    final error = requestError;
    if (error != null) {
      return Future<CourseSyncResult>.error(error);
    }
    return Future<CourseSyncResult>.value(
      requestResult ?? _sync('sync-request', 'queued'),
    );
  }

  @override
  Future<CourseDetail> fetchCourse(String id) async {
    detailCalls++;
    return CourseDetail(
        summary: courses.firstWhere((course) => course.id == id));
  }

  @override
  Future<CourseSyncResult> fetchCourseSync(String id) {
    pollCalls++;
    final gate = pollGate;
    if (gate != null) return gate.future;
    if (pollResults.isEmpty) {
      return Future<CourseSyncResult>.value(
        _sync(id, 'succeeded', courseCount: 1),
      );
    }
    return Future<CourseSyncResult>.value(pollResults.removeAt(0));
  }
}

ProviderContainer _container(
  _SyncCourseRepository repository, {
  required DateTime Function() clock,
  Duration pollInterval = const Duration(milliseconds: 5),
}) {
  final config = const AppConfig(apiBaseUri: null, channel: ClientChannel.web);
  final session = SessionManager(
    channel: ClientChannel.web,
    store: InMemoryTokenStore(),
  );
  return ProviderContainer(
    overrides: [
      currentUserProvider.overrideWithValue(testUser),
      authControllerProvider.overrideWith(
        () => FakeAuthController(authenticatedState()),
      ),
      learningRepositoryProvider.overrideWithValue(repository),
      realtimeServiceProvider.overrideWithValue(
        RealtimeService(config: config, session: session),
      ),
      courseSyncClockProvider.overrideWithValue(clock),
      courseSyncPollIntervalProvider.overrideWithValue(pollInterval),
    ],
  );
}

Future<void> _warm(ProviderContainer container) async {
  container.listen(coursesControllerProvider, (_, __) {});
  container.listen(courseSyncControllerProvider, (_, __) {});
  await container.read(coursesControllerProvider.future);
}

Future<CourseSyncState> _waitFor(
  ProviderContainer container,
  bool Function(CourseSyncState state) predicate, {
  String reason = '课程同步状态',
}) async {
  final deadline = DateTime.now().add(const Duration(seconds: 2));
  while (true) {
    final state = container.read(courseSyncControllerProvider);
    if (predicate(state)) return state;
    if (DateTime.now().isAfter(deadline)) {
      fail('等待$reason超时：${state.phase} ${state.error}');
    }
    await Future<void>.delayed(const Duration(milliseconds: 5));
  }
}

void main() {
  test('解析同步请求与完成时间', () {
    final result = CourseSyncResult.parse(<String, dynamic>{
      'id': 'sync-1',
      'status': 'succeeded',
      'course_count': 2,
      'requested_at': '2026-10-26T02:00:00Z',
      'finished_at': '2026-10-26T02:05:00Z',
    });

    expect(result.requestedAt, DateTime.utc(2026, 10, 26, 2));
    expect(result.finishedAt, DateTime.utc(2026, 10, 26, 2, 5));
    expect(result.completedAt, DateTime.utc(2026, 10, 26, 2, 5));
  });

  test('课程数据超过 30 分钟时自动同步并接续到完成状态', () async {
    final now = DateTime.utc(2026, 10, 26, 4);
    final repository = _SyncCourseRepository(
      courses: [
        _course(syncedAt: now.subtract(const Duration(minutes: 31))),
      ],
      requestResult: _sync('sync-1', 'queued'),
      pollResults: [
        _sync('sync-1', 'succeeded', courseCount: 1, finishedAt: now),
      ],
    );
    final container = _container(repository, clock: () => now);
    addTearDown(container.dispose);
    await _warm(container);

    await container
        .read(courseSyncControllerProvider.notifier)
        .autoSyncIfNeeded();

    expect(repository.requestCalls, 1);
    final state = await _waitFor(
      container,
      (value) => value.phase == CourseSyncPhase.succeeded,
      reason: '自动同步完成',
    );
    expect(state.visible, isTrue);
    expect(state.message, '课程同步已完成，共 1 门课程。');
    expect(repository.courseCalls, greaterThanOrEqualTo(2));
  });

  test('最近一次成功同步未超过 30 分钟时不重复同步', () async {
    final now = DateTime.utc(2026, 10, 26, 4);
    final repository = _SyncCourseRepository(
      courses: [
        _course(syncedAt: now.subtract(const Duration(hours: 2))),
      ],
      latest: _sync(
        'sync-latest',
        'succeeded',
        courseCount: 1,
        finishedAt: now.subtract(const Duration(minutes: 1)),
      ),
    );
    final container = _container(repository, clock: () => now);
    addTearDown(container.dispose);
    await _warm(container);

    await container
        .read(courseSyncControllerProvider.notifier)
        .autoSyncIfNeeded();

    expect(repository.latestCalls, 1);
    expect(repository.requestCalls, 0);
    expect(
      container.read(courseSyncControllerProvider).phase,
      CourseSyncPhase.idle,
    );
  });

  test('没有同步记录时按课程同步时间判断，未过期不自动同步', () async {
    final now = DateTime.utc(2026, 10, 26, 4);
    final repository = _SyncCourseRepository(
      courses: [
        _course(syncedAt: now.subtract(const Duration(minutes: 5))),
      ],
    );
    final container = _container(repository, clock: () => now);
    addTearDown(container.dispose);
    await _warm(container);

    await container
        .read(courseSyncControllerProvider.notifier)
        .autoSyncIfNeeded();

    expect(repository.latestCalls, 1);
    expect(repository.requestCalls, 0);
    expect(
      container.read(courseSyncControllerProvider).phase,
      CourseSyncPhase.idle,
    );
  });

  test('最近一次成功同步超过 30 分钟时重新同步', () async {
    final now = DateTime.utc(2026, 10, 26, 4);
    final repository = _SyncCourseRepository(
      courses: [
        _course(syncedAt: now.subtract(const Duration(hours: 1))),
      ],
      latest: _sync(
        'sync-old',
        'succeeded',
        courseCount: 1,
        finishedAt: now.subtract(const Duration(minutes: 31)),
      ),
      requestResult: _sync('sync-new', 'queued'),
      pollResults: [
        _sync('sync-new', 'succeeded', courseCount: 1, finishedAt: now),
      ],
    );
    final container = _container(repository, clock: () => now);
    addTearDown(container.dispose);
    await _warm(container);

    await container
        .read(courseSyncControllerProvider.notifier)
        .autoSyncIfNeeded();

    expect(repository.requestCalls, 1);
    await _waitFor(
      container,
      (value) => value.phase == CourseSyncPhase.succeeded,
      reason: '过期数据重新同步',
    );
  });

  test('运行中的同步只接续一个轮询，并用同一状态展示进度', () async {
    final now = DateTime.utc(2026, 10, 26, 4);
    final latestGate = Completer<CourseSyncResult?>();
    final pollGate = Completer<CourseSyncResult>();
    final repository = _SyncCourseRepository(
      courses: [
        _course(syncedAt: now.subtract(const Duration(minutes: 31))),
      ],
    )
      ..latestGate = latestGate
      ..pollGate = pollGate;
    final container = _container(repository, clock: () => now);
    addTearDown(container.dispose);
    await _warm(container);
    final notifier = container.read(courseSyncControllerProvider.notifier);

    final first = notifier.autoSyncIfNeeded();
    final second = notifier.autoSyncIfNeeded();
    expect(repository.latestCalls, 1);

    latestGate.complete(
      _sync('sync-running', 'running', courseCount: 4),
    );
    await Future.wait([first, second]);

    final running = container.read(courseSyncControllerProvider);
    expect(running.phase, CourseSyncPhase.running);
    expect(running.message, '正在同步课程… 已读取 4 门');
    expect(running.visible, isTrue);

    await notifier.autoSyncIfNeeded();
    expect(repository.latestCalls, 1);
    final pollDeadline = DateTime.now().add(const Duration(seconds: 2));
    while (repository.pollCalls == 0) {
      if (DateTime.now().isAfter(pollDeadline)) fail('等待状态轮询启动超时');
      await Future<void>.delayed(const Duration(milliseconds: 5));
    }
    expect(repository.pollCalls, 1);

    await container.read(coursesControllerProvider.notifier).refresh(
          silent: true,
        );
    expect(
      container.read(courseSyncControllerProvider).phase,
      CourseSyncPhase.running,
    );

    pollGate.complete(
      _sync('sync-running', 'succeeded', courseCount: 4, finishedAt: now),
    );
    final finished = await _waitFor(
      container,
      (value) => value.phase == CourseSyncPhase.succeeded,
      reason: '接续同步完成',
    );
    expect(finished.result?.id, 'sync-running');
    expect(repository.pollCalls, 1);
  });

  test('关闭同一 sync id 的失败提示后，自动检查不会重新弹出', () async {
    var now = DateTime.utc(2026, 10, 26, 4);
    final repository = _SyncCourseRepository(
      courses: [_course(syncedAt: now)],
      latest: _sync('sync-failed', 'failed'),
    );
    final container = _container(repository, clock: () => now);
    addTearDown(container.dispose);
    await _warm(container);
    final notifier = container.read(courseSyncControllerProvider.notifier);

    await notifier.autoSyncIfNeeded();
    final failed = container.read(courseSyncControllerProvider);
    expect(failed.phase, CourseSyncPhase.failed);
    expect(failed.visible, isTrue);

    notifier.dismiss();
    expect(container.read(courseSyncControllerProvider).visible, isFalse);

    now = now.add(const Duration(minutes: 6));
    await notifier.autoSyncIfNeeded();
    final dismissed = container.read(courseSyncControllerProvider);
    expect(dismissed.visible, isFalse);
    expect(dismissed.result?.id, 'sync-failed');
    expect(repository.requestCalls, 0);

    repository.latest = _sync('sync-failed-new', 'failed');
    now = now.add(const Duration(minutes: 6));
    await notifier.autoSyncIfNeeded();
    final next = container.read(courseSyncControllerProvider);
    expect(next.visible, isTrue);
    expect(next.result?.id, 'sync-failed-new');
  });

  test('自动请求失败后不循环重试，手动重试仍可发起同步', () async {
    var now = DateTime.utc(2026, 10, 26, 4);
    final repository = _SyncCourseRepository(
      courses: const <CourseSummary>[],
    )..requestError = const ApiException(
        statusCode: 503,
        code: 'service_unavailable',
        message: '课程服务暂不可用',
      );
    final container = _container(repository, clock: () => now);
    addTearDown(container.dispose);
    await _warm(container);
    final notifier = container.read(courseSyncControllerProvider.notifier);

    await notifier.autoSyncIfNeeded();
    final failed = container.read(courseSyncControllerProvider);
    expect(failed.phase, CourseSyncPhase.failed);
    expect(failed.message, '课程服务暂不可用');
    expect(failed.canRetry, isTrue);
    expect(repository.requestCalls, 1);

    now = now.add(const Duration(minutes: 6));
    await notifier.autoSyncIfNeeded();
    expect(repository.requestCalls, 1);

    repository.requestError = null;
    repository.requestResult = _sync('sync-manual', 'queued');
    repository.pollResults = [
      _sync('sync-manual', 'succeeded', courseCount: 1, finishedAt: now),
    ];
    await notifier.retry();
    expect(repository.requestCalls, 2);
    await _waitFor(
      container,
      (value) => value.phase == CourseSyncPhase.succeeded,
      reason: '手动重试完成',
    );
  });

  test('新的后台同步成功后替换旧失败提示', () async {
    var now = DateTime.utc(2026, 10, 26);
    final repository = _SyncCourseRepository(
      courses: [_course(syncedAt: now)],
      latest: _sync('old-failure', 'needs_attention', errorCode: 'transient'),
    );
    final container = _container(repository, clock: () => now);
    addTearDown(container.dispose);
    await _warm(container);
    final notifier = container.read(courseSyncControllerProvider.notifier);
    await notifier.autoSyncIfNeeded();
    expect(container.read(courseSyncControllerProvider).phase,
        CourseSyncPhase.failed);
    now = now.add(const Duration(minutes: 6));
    repository.latest =
        _sync('new-success', 'succeeded', courseCount: 1, finishedAt: now);
    await notifier.autoSyncIfNeeded();
    final updated = container.read(courseSyncControllerProvider);
    expect(updated.phase, CourseSyncPhase.succeeded);
    expect(updated.result?.id, 'new-success');
    expect(updated.error, isNull);
    expect(repository.requestCalls, 0);
  });

  test('账号切换后丢弃旧的最近同步响应', () async {
    final now = DateTime.utc(2026, 10, 26);
    final gate = Completer<CourseSyncResult?>();
    final repository = _SyncCourseRepository(courses: [_course(syncedAt: now)])
      ..latestGate = gate;
    final container = _container(repository, clock: () => now);
    addTearDown(container.dispose);
    await _warm(container);
    final pending = container
        .read(courseSyncControllerProvider.notifier)
        .autoSyncIfNeeded();
    expect(repository.latestCalls, 1);
    container.read(sessionEpochProvider.notifier).bump();
    expect(container.read(courseSyncControllerProvider).phase,
        CourseSyncPhase.idle);
    gate.complete(
        _sync('old-user-sync', 'needs_attention', errorCode: 'forbidden'));
    await pending;
    expect(container.read(courseSyncControllerProvider).phase,
        CourseSyncPhase.idle);
    expect(container.read(courseSyncControllerProvider).result, isNull);
  });

  test('账号切换后丢弃旧的同步提交响应', () async {
    final now = DateTime.utc(2026, 10, 26);
    final gate = Completer<CourseSyncResult>();
    final repository = _SyncCourseRepository(courses: [_course(syncedAt: now)])
      ..requestGate = gate;
    final container = _container(repository, clock: () => now);
    addTearDown(container.dispose);
    await _warm(container);
    final pending =
        container.read(courseSyncControllerProvider.notifier).syncCourses();
    expect(repository.requestCalls, 1);
    container.read(sessionEpochProvider.notifier).bump();
    expect(container.read(courseSyncControllerProvider).phase,
        CourseSyncPhase.idle);
    gate.complete(_sync('old-user-sync', 'queued'));
    await pending;
    await Future<void>.delayed(const Duration(milliseconds: 15));
    expect(container.read(courseSyncControllerProvider).phase,
        CourseSyncPhase.idle);
    expect(repository.pollCalls, 0);
  });

  test('课程完整快照更新后自动刷新当前详情', () async {
    final now = DateTime.utc(2026, 10, 26);
    final repository = _SyncCourseRepository(courses: const [
      CourseSummary(id: 'c1', title: '待加载课程', snapshotVersion: 0),
    ]);
    final container = _container(repository, clock: () => now);
    addTearDown(container.dispose);
    await _warm(container);
    container.listen(courseDetailProvider('c1'), (_, __) {});
    final initial = await container.read(courseDetailProvider('c1').future);
    expect(initial.summary.detailsPending, isTrue);
    repository.courses = [
      CourseSummary(
          id: 'c1',
          title: '已加载课程',
          snapshotVersion: 1,
          syncedAt: now,
          chapterCount: 2),
    ];
    await container
        .read(coursesControllerProvider.notifier)
        .refresh(silent: true);
    final updated = await container.read(courseDetailProvider('c1').future);
    expect(updated.summary.detailsPending, isFalse);
    expect(updated.summary.chapterCount, 2);
    expect(repository.detailCalls, 2);
  });
}
