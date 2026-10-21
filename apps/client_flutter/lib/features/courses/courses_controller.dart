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
    this.syncing = false,
    this.syncResult,
    this.syncError,
    this.refreshError,
  });

  final List<CourseSummary> courses;
  final String? nextCursor;
  final String search;
  final bool refreshing;
  final bool syncing;
  final CourseSyncResult? syncResult;
  final String? syncError;
  final String? refreshError;

  bool get isEmpty => courses.isEmpty;

  CourseListState copyWith({
    List<CourseSummary>? courses,
    String? nextCursor,
    String? search,
    bool? refreshing,
    bool? syncing,
    CourseSyncResult? syncResult,
    String? syncError,
    String? refreshError,
    bool clearSyncError = false,
    bool clearRefreshError = false,
    bool clearSyncResult = false,
  }) {
    return CourseListState(
      courses: courses ?? this.courses,
      nextCursor: nextCursor ?? this.nextCursor,
      search: search ?? this.search,
      refreshing: refreshing ?? this.refreshing,
      syncing: syncing ?? this.syncing,
      syncResult: clearSyncResult ? null : syncResult ?? this.syncResult,
      syncError: clearSyncError ? null : syncError ?? this.syncError,
      refreshError:
          clearRefreshError ? null : refreshError ?? this.refreshError,
    );
  }
}

class CoursesController extends AsyncNotifier<CourseListState> {
  Page<CourseSummary>? _page;
  StreamSubscription<RealtimeEvent>? _events;

  @override
  Future<CourseListState> build() async {
    ref.watch(sessionEpochProvider);
    final user = ref.watch(currentUserProvider);
    _events = ref.watch(realtimeServiceProvider).events.listen(_onEvent);
    ref.onDispose(() {
      _events?.cancel();
      _events = null;
    });
    if (user == null) {
      return const CourseListState();
    }
    final page = await ref.watch(learningRepositoryProvider).fetchCourses();
    _page = page;
    return CourseListState(
      courses: page.items,
      nextCursor: page.nextCursor,
    );
  }

  void _onEvent(RealtimeEvent event) {
    if (event.type.startsWith('course.')) {
      unawaited(refresh(silent: true));
    }
  }

  Future<void> setSearch(String value) async {
    final trimmed = value.trim();
    final current = state.valueOrNull;
    if (current != null && current.search == trimmed) return;
    state = const AsyncLoading();
    try {
      final page = await ref
          .read(learningRepositoryProvider)
          .fetchCourses(search: trimmed);
      _page = page;
      state = AsyncData(
        CourseListState(
          courses: page.items,
          nextCursor: page.nextCursor,
          search: trimmed,
        ),
      );
    } catch (error, stack) {
      state = AsyncError(error, stack);
    }
  }

  Future<void> refresh({bool silent = false}) async {
    final current = state.valueOrNull;
    if (current == null) {
      ref.invalidateSelf();
      return;
    }
    if (current.refreshing) return;
    state = AsyncData(
      current.copyWith(
        refreshing: true,
        clearRefreshError: true,
        clearSyncError: true,
      ),
    );
    try {
      final page = await ref
          .read(learningRepositoryProvider)
          .fetchCourses(search: current.search);
      _page = page;
      state = AsyncData(
        current.copyWith(
          courses: page.items,
          nextCursor: page.nextCursor,
          refreshing: false,
          clearRefreshError: true,
        ),
      );
    } on ApiException catch (error) {
      state = AsyncData(
        current.copyWith(
          refreshing: false,
          refreshError: error.userMessage,
        ),
      );
    } catch (_) {
      state = AsyncData(
        current.copyWith(
          refreshing: false,
          refreshError: '课程刷新未完成，请稍后重试',
        ),
      );
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
    state = AsyncData(current.copyWith(refreshing: true));
    try {
      final next = await ref.read(learningRepositoryProvider).fetchCourses(
            cursor: page.nextCursor,
            search: current.search,
          );
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
          refreshing: false,
        ),
      );
    } catch (error) {
      state = AsyncData(
        current.copyWith(
          refreshing: false,
          refreshError: error is ApiException ? error.userMessage : '加载更多课程未完成',
        ),
      );
    }
  }

  Future<void> syncCourses() async {
    final current = state.valueOrNull;
    if (current == null || current.syncing) return;
    state = AsyncData(
      current.copyWith(
        syncing: true,
        clearSyncError: true,
        clearSyncResult: true,
      ),
    );
    try {
      final result =
          await ref.read(learningRepositoryProvider).requestCourseSync();
      state = AsyncData(
        (state.valueOrNull ?? current).copyWith(
          syncing: false,
          syncResult: result,
        ),
      );
      if (!result.accepted) {
        await refresh(silent: true);
      }
    } on ApiException catch (error) {
      state = AsyncData(
        (state.valueOrNull ?? current).copyWith(
          syncing: false,
          syncError: error.userMessage,
        ),
      );
    } catch (_) {
      state = AsyncData(
        (state.valueOrNull ?? current).copyWith(
          syncing: false,
          syncError: '课程同步请求未完成，请稍后重试',
        ),
      );
    }
  }

  void clearBanner() {
    final current = state.valueOrNull;
    if (current == null) return;
    state = AsyncData(
      current.copyWith(clearSyncResult: true, clearSyncError: true),
    );
  }
}

final coursesControllerProvider =
    AsyncNotifierProvider<CoursesController, CourseListState>(
  CoursesController.new,
);

final courseDetailProvider =
    FutureProvider.autoDispose.family<CourseDetail, String>((ref, id) async {
  ref.watch(sessionEpochProvider);
  if (id.isEmpty) throw StateError('课程编号为空');
  return ref.watch(learningRepositoryProvider).fetchCourse(id);
});
