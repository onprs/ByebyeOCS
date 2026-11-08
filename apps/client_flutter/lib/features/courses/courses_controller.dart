import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/models/course.dart';
import '../../core/models/page.dart';
import '../../core/realtime/realtime_event.dart';

class CourseListState {
  const CourseListState({
    this.courses = const <CourseSummary>[],
    this.nextCursor,
    this.search = '',
    this.refreshing = false,
    this.refreshError,
  });

  final List<CourseSummary> courses;
  final String? nextCursor;
  final String search;
  final bool refreshing;
  final String? refreshError;

  bool get isEmpty => courses.isEmpty;

  CourseListState copyWith({
    List<CourseSummary>? courses,
    String? nextCursor,
    String? search,
    bool? refreshing,
    String? refreshError,
    bool clearRefreshError = false,
    bool clearNextCursor = false,
  }) {
    return CourseListState(
      courses: courses ?? this.courses,
      nextCursor: clearNextCursor ? null : nextCursor ?? this.nextCursor,
      search: search ?? this.search,
      refreshing: refreshing ?? this.refreshing,
      refreshError:
          clearRefreshError ? null : refreshError ?? this.refreshError,
    );
  }
}

class CoursesController extends AsyncNotifier<CourseListState> {
  Page<CourseSummary>? _page;
  Timer? _refreshTimer;
  bool _refreshQueued = false;
  bool _disposed = false;
  int _epoch = 0;

  /// 搜索、刷新与翻页共享同一请求序号；只允许最新一次结果写入界面。
  int _requestSeq = 0;

  @override
  Future<CourseListState> build() async {
    final epoch = ++_epoch;
    _disposed = false;
    _requestSeq++;
    _refreshTimer?.cancel();
    _refreshTimer = null;
    _refreshQueued = false;
    ref.watch(sessionEpochProvider);
    final user = ref.watch(currentUserProvider);
    final events = ref.watch(realtimeServiceProvider).events.listen(_onEvent);
    ref.onDispose(() {
      events.cancel();
      if (epoch != _epoch) return;
      _disposed = true;
      _requestSeq++;
      _refreshTimer?.cancel();
      _refreshTimer = null;
      _refreshQueued = false;
    });
    if (user == null) {
      _page = null;
      return const CourseListState();
    }
    final page = await ref.watch(learningRepositoryProvider).fetchCourses();
    if (epoch != _epoch || _disposed) return const CourseListState();
    _page = page;
    if (_refreshQueued) _armRefresh();
    return CourseListState(
      courses: page.items,
      nextCursor: page.nextCursor,
    );
  }

  void _onEvent(RealtimeEvent event) {
    if (!isCourseProgressEvent(event) || _disposed) return;
    _refreshQueued = true;
    final current = state.valueOrNull;
    if (current != null && !current.refreshing) _armRefresh();
  }

  // 合并任务事件；在途请求结束后仍保留一次刷新，避免漏掉请求期间的新核验。
  void _armRefresh() {
    if (_disposed || !_refreshQueued || _refreshTimer != null) return;
    _refreshTimer = Timer(const Duration(milliseconds: 800), () {
      _refreshTimer = null;
      if (_disposed || state.valueOrNull?.refreshing == true) return;
      _refreshQueued = false;
      unawaited(refresh(silent: true));
    });
  }

  Future<void> setSearch(String value) async {
    final trimmed = value.trim();
    final current = state.valueOrNull;
    if (current == null) return;
    if (current.search == trimmed) return;
    final seq = ++_requestSeq;
    // 搜索期间保留工具栏与搜索框，避免整页加载态移除输入焦点。
    state = AsyncData(
      current.copyWith(
        search: trimmed,
        refreshing: true,
        clearRefreshError: true,
      ),
    );
    try {
      final page = await ref
          .read(learningRepositoryProvider)
          .fetchCourses(search: trimmed);
      if (seq != _requestSeq) return;
      _page = page;
      state = AsyncData(
        CourseListState(
          courses: page.items,
          nextCursor: page.nextCursor,
          search: trimmed,
        ),
      );
    } on ApiException catch (error) {
      if (seq != _requestSeq) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          refreshing: false,
          refreshError: error.userMessage,
        ),
      );
    } catch (_) {
      if (seq != _requestSeq) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          refreshing: false,
          refreshError: '课程搜索未完成，请稍后重试',
        ),
      );
    } finally {
      if (seq == _requestSeq && _refreshQueued) _armRefresh();
    }
  }

  Future<void> refresh({bool silent = false}) async {
    final current = state.valueOrNull;
    if (current == null) {
      ref.invalidateSelf();
      return;
    }
    if (current.refreshing) {
      _refreshQueued = true;
      return;
    }
    final seq = ++_requestSeq;
    state = AsyncData(
      current.copyWith(
        refreshing: true,
        clearRefreshError: true,
      ),
    );
    try {
      final repository = ref.read(learningRepositoryProvider);
      var page = await repository.fetchCourses(search: current.search);
      final items = [...page.items];
      final cursors = <String>{};
      // 静默刷新覆盖已经加载的范围，保留搜索与分页位置。
      while (silent && items.length < current.courses.length && page.hasMore) {
        if (seq != _requestSeq) return;
        if (!cursors.add(page.nextCursor!)) throw StateError('课程分页游标重复');
        page = await repository.fetchCourses(
            cursor: page.nextCursor, search: current.search);
        for (final item in page.items) {
          if (items.every((existing) => existing.id != item.id)) {
            items.add(item);
          }
        }
      }
      if (seq != _requestSeq) return;
      page = Page<CourseSummary>(items: items, nextCursor: page.nextCursor);
      _page = page;
      state = AsyncData(
        current.copyWith(
          courses: page.items,
          nextCursor: page.nextCursor,
          clearNextCursor: page.nextCursor == null,
          refreshing: false,
          clearRefreshError: true,
        ),
      );
    } on ApiException catch (error) {
      if (seq != _requestSeq) return;
      state = AsyncData(
        current.copyWith(
          refreshing: false,
          refreshError: error.userMessage,
        ),
      );
    } catch (_) {
      if (seq != _requestSeq) return;
      state = AsyncData(
        current.copyWith(
          refreshing: false,
          refreshError: '课程刷新未完成，请稍后重试',
        ),
      );
    } finally {
      if (seq == _requestSeq && _refreshQueued) _armRefresh();
    }
  }

  Future<void> loadMore() async {
    final current = state.valueOrNull;
    final page = _page;
    if (current == null ||
        page == null ||
        !page.hasMore ||
        current.refreshing) {
      return;
    }
    // 翻页只是当前查询的继续；搜索开始后旧翻页结果必须丢弃。
    final seq = _requestSeq;
    state = AsyncData(current.copyWith(refreshing: true));
    try {
      final next = await ref.read(learningRepositoryProvider).fetchCourses(
            cursor: page.nextCursor,
            search: current.search,
          );
      if (seq != _requestSeq) return;
      final merged = <CourseSummary>[
        ...current.courses,
        ...next.items.where(
          (item) => current.courses.every((course) => course.id != item.id),
        ),
      ];
      final combined = Page<CourseSummary>(
        items: merged,
        nextCursor: next.nextCursor,
      );
      _page = combined;
      state = AsyncData(
        current.copyWith(
          courses: merged,
          nextCursor: next.nextCursor,
          clearNextCursor: next.nextCursor == null,
          refreshing: false,
        ),
      );
    } catch (error) {
      if (seq != _requestSeq) return;
      state = AsyncData(
        current.copyWith(
          refreshing: false,
          refreshError: error is ApiException ? error.userMessage : '加载更多课程未完成',
        ),
      );
    } finally {
      if (seq == _requestSeq && _refreshQueued) _armRefresh();
    }
  }
}

final coursesControllerProvider =
    AsyncNotifierProvider<CoursesController, CourseListState>(
  CoursesController.new,
);

final courseDetailProvider =
    FutureProvider.autoDispose.family<CourseDetail, String>((ref, id) async {
  ref.watch(sessionEpochProvider);
  ref.watch(coursesControllerProvider.select((value) {
    for (final course
        in value.valueOrNull?.courses ?? const <CourseSummary>[]) {
      if (course.id == id) return course.snapshotVersion;
    }
    return null;
  }));
  Timer? refreshTimer;
  final events = ref.watch(realtimeServiceProvider).events.listen((event) {
    if (!isCourseProgressEvent(event) || refreshTimer != null) return;
    refreshTimer = Timer(const Duration(milliseconds: 800), ref.invalidateSelf);
  });
  ref.onDispose(() {
    events.cancel();
    refreshTimer?.cancel();
  });
  if (id.isEmpty) throw StateError('课程编号为空');
  return ref.watch(learningRepositoryProvider).fetchCourse(id);
});
