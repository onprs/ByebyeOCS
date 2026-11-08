import 'dart:async';

import 'package:byebyeocs_client/core/api/api_client.dart';
import 'package:byebyeocs_client/core/session/session_manager.dart';
import 'package:byebyeocs_client/core/config/app_config.dart';
import 'package:byebyeocs_client/core/models/page.dart';
import 'package:byebyeocs_client/core/models/course.dart';
import 'package:byebyeocs_client/app/providers.dart';
import 'package:byebyeocs_client/core/realtime/realtime_event.dart';
import 'package:byebyeocs_client/core/platform/token_store.dart';
import 'package:byebyeocs_client/core/realtime/realtime_service.dart';
import 'package:byebyeocs_client/core/repositories/learning_repository.dart';
import 'package:byebyeocs_client/features/courses/courses_controller.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import '../helpers.dart';

class _CourseEvents extends RealtimeService {
  _CourseEvents()
      : super(
            config:
                const AppConfig(apiBaseUri: null, channel: ClientChannel.web),
            session: SessionManager(
                channel: ClientChannel.web, store: InMemoryTokenStore()));
  final controller = StreamController<RealtimeEvent>.broadcast(sync: true);
  @override
  Stream<RealtimeEvent> get events => controller.stream;
  void emit(String type) => controller.add(RealtimeEvent(type: type));
  @override
  void dispose() {
    controller.close();
    super.dispose();
  }
}

class _ProgressRepository extends LearningRepository {
  _ProgressRepository()
      : super(
            api: ApiClient(
                config: const AppConfig(
                    apiBaseUri: null, channel: ClientChannel.web),
                session: SessionManager(
                    channel: ClientChannel.web, store: InMemoryTokenStore()),
                httpClient: MockClient((_) async => http.Response('{}', 200))));
  int listCalls = 0;
  int detailCalls = 0;
  bool confirmed = false;
  Completer<Page<CourseSummary>>? delayed;
  CourseSummary summary(String id) => CourseSummary.parse({
        ...coursesFixture.first,
        'id': id,
        'title': id,
        'incomplete_task_point_count': confirmed ? 0 : 1
      });
  @override
  Future<Page<CourseSummary>> fetchCourses(
      {String? cursor, int limit = 100, String? search}) {
    listCalls++;
    if (delayed != null) {
      final pending = delayed!;
      delayed = null;
      return pending.future;
    }
    return Future.value(Page(
        items: [summary(cursor == null ? 'c1' : 'c2')],
        nextCursor: cursor == null ? 'next' : null));
  }

  @override
  Future<CourseDetail> fetchCourse(String id) async {
    detailCalls++;
    return CourseDetail.parse({
      ...courseDetailFixture,
      'incomplete_task_point_count': confirmed ? 0 : 1
    });
  }
}

ProviderContainer _container(
        _ProgressRepository repository, _CourseEvents events) =>
    ProviderContainer(overrides: [
      currentUserProvider.overrideWithValue(testUser),
      learningRepositoryProvider.overrideWithValue(repository),
      realtimeServiceProvider.overrideWithValue(events),
    ]);

void main() {
  testWidgets('任务确认事件合并刷新已加载页，搜索和分页内容保留', (tester) async {
    final repository = _ProgressRepository();
    final events = _CourseEvents();
    final container = _container(repository, events);
    addTearDown(() {
      container.dispose();
      events.dispose();
    });
    container.listen(coursesControllerProvider, (_, __) {});
    await container.read(coursesControllerProvider.future);
    await container.read(coursesControllerProvider.notifier).setSearch('课程');
    await container.read(coursesControllerProvider.notifier).loadMore();
    final previous = repository.listCalls;
    repository.confirmed = true;
    events.emit('task.confirmed');
    events.emit('task.attempt_finished');
    events.emit('task.state_changed');
    await tester.pump(const Duration(milliseconds: 900));
    await tester.pump();
    final value = container.read(coursesControllerProvider).valueOrNull!;
    expect(value.search, '课程');
    expect(value.courses.map((item) => item.id), ['c1', 'c2']);
    expect(value.courses.every((item) => item.incompleteTaskPointCount == 0),
        isTrue);
    expect(repository.listCalls, previous + 2);
  });

  testWidgets('刷新期间收到的核验事件会在请求结束后再取最新状态', (tester) async {
    final repository = _ProgressRepository();
    final events = _CourseEvents();
    final container = _container(repository, events);
    addTearDown(() {
      container.dispose();
      events.dispose();
    });
    container.listen(coursesControllerProvider, (_, __) {});
    await container.read(coursesControllerProvider.future);
    final pending = Completer<Page<CourseSummary>>();
    repository.delayed = pending;
    final refresh = container
        .read(coursesControllerProvider.notifier)
        .refresh(silent: true);
    events.emit('task.confirmed');
    final old = repository.summary('c1');
    repository.confirmed = true;
    pending.complete(Page(items: [old], nextCursor: 'next'));
    await refresh;
    await tester.pump(const Duration(milliseconds: 900));
    await tester.pump();
    expect(repository.listCalls, 3);
    expect(
        container
            .read(coursesControllerProvider)
            .valueOrNull!
            .courses
            .first
            .incompleteTaskPointCount,
        0);
  });

  testWidgets('详情在快照版本不变时也随任务更新，观察事件不冒充平台完成', (tester) async {
    final repository = _ProgressRepository();
    final events = _CourseEvents();
    final container = _container(repository, events);
    addTearDown(() {
      container.dispose();
      events.dispose();
    });
    container.listen(coursesControllerProvider, (_, __) {});
    await container.read(coursesControllerProvider.future);
    container.listen(courseDetailProvider('c1'), (_, __) {});
    await container.read(courseDetailProvider('c1').future);
    final version = container
        .read(coursesControllerProvider)
        .valueOrNull!
        .courses
        .first
        .snapshotVersion;
    final calls = repository.detailCalls;
    events.emit('task.observation');
    await tester.pump(const Duration(milliseconds: 900));
    expect(repository.detailCalls, calls);
    repository.confirmed = true;
    events.emit('task.confirmed');
    await tester.pump(const Duration(milliseconds: 900));
    await tester.pump();
    expect(repository.detailCalls, greaterThan(calls));
    expect(
        container
            .read(coursesControllerProvider)
            .valueOrNull!
            .courses
            .first
            .snapshotVersion,
        version);
    expect(
        container
            .read(courseDetailProvider('c1'))
            .valueOrNull!
            .summary
            .incompleteTaskPointCount,
        0);
  });

  testWidgets('轮询回退恢复课程进度', (tester) async {
    final repository = _ProgressRepository();
    final events = _CourseEvents();
    final container = _container(repository, events);
    addTearDown(() {
      container.dispose();
      events.dispose();
    });
    container.listen(coursesControllerProvider, (_, __) {});
    await container.read(coursesControllerProvider.future);
    repository.confirmed = true;
    events.emit('client.poll');
    await tester.pump(const Duration(milliseconds: 900));
    await tester.pump();
    expect(
        container
            .read(coursesControllerProvider)
            .valueOrNull!
            .courses
            .first
            .incompleteTaskPointCount,
        0);
  });

  test('章节关闭任务不进入可执行分母，已核实完成不被关闭标签覆盖', () {
    const chapter = CourseChapter(id: 'chapter', title: '章节', attachments: [
      CourseAttachment(
          id: 'passed',
          title: '已完成',
          isTaskPoint: true,
          passedKnown: true,
          platformPassed: true,
          availability: 'closed'),
      CourseAttachment(
          id: 'closed',
          title: '已关闭',
          isTaskPoint: true,
          countedIncomplete: true,
          availability: 'closed'),
      CourseAttachment(id: 'resource', title: '资料'),
    ]);
    final node = CourseChapterNode(chapter: chapter, depth: 0, children: []);
    expect(node.taskPointCount, 2);
    expect(node.closedCount, 1);
    expect(node.executableTaskPointCount, 1);
    expect(node.confirmedCount, 1);
    expect(chapter.incompleteCount, 0);
  });
}
