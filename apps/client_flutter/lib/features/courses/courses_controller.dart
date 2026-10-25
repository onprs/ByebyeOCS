import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/models/course.dart';
import '../../core/models/page.dart';
import '../../core/realtime/realtime_event.dart';
import '../auth/auth_controller.dart';

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
  bool _autoSyncTried = false;
  bool _polling = false;
  bool _disposed = false;

  /// 搜索、刷新与翻页共享同一请求序号；只允许最新一次结果写入界面。
  int _requestSeq = 0;

  @override
  Future<CourseListState> build() async {
    _disposed = false;
    _autoSyncTried = false;
    _polling = false;
    _requestSeq++;
    ref.watch(sessionEpochProvider);
    final user = ref.watch(currentUserProvider);
    _events = ref.watch(realtimeServiceProvider).events.listen(_onEvent);
    ref.onDispose(() {
      _disposed = true;
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
    if (current == null) return;
    if (current.search == trimmed) return;
    final seq = ++_requestSeq;
    // 搜索期间保留工具栏与搜索框，避免整页加载态移除输入焦点。
    state = AsyncData(
      current.copyWith(
        search: trimmed,
        refreshing: true,
        clearRefreshError: true,
        clearSyncError: true,
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
    }
  }

  Future<void> refresh({bool silent = false}) async {
    final current = state.valueOrNull;
    if (current == null) {
      ref.invalidateSelf();
      return;
    }
    if (current.refreshing) return;
    final seq = ++_requestSeq;
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
      if (seq != _requestSeq) return;
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
    }
  }

  /// 页面进入时自动接续运行中的同步；没有同步且课程为空时自动开始。
  Future<void> autoSyncIfNeeded() async {
    if (_autoSyncTried) return;
    final current = state.valueOrNull;
    if (current == null || current.syncing) return;
    _autoSyncTried = true;
    if (!ref.read(authControllerProvider).capabilities.courseSyncAvailable) {
      return;
    }
    try {
      final latest =
          await ref.read(learningRepositoryProvider).fetchLatestCourseSync();
      if (_disposed) return;
      if (latest != null && latest.running) {
        state = AsyncData(
          current.copyWith(
            syncing: true,
            syncResult: latest,
            clearSyncError: true,
          ),
        );
        await _pollCourseSync(latest.id);
        return;
      }
      if (latest != null && latest.needsAttention) {
        state = AsyncData(
          current.copyWith(
            syncing: false,
            syncResult: latest,
            syncError: '课程同步未完成，请重试',
          ),
        );
        return;
      }
    } catch (_) {
      // 最新同步状态不可读取时仍允许在课程为空时重新开始。
    }
    if (current.courses.isEmpty) {
      await syncCourses(automatic: true);
    }
  }

  Future<void> syncCourses({bool automatic = false}) async {
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
      if (_disposed) return;
      state = AsyncData(
        (state.valueOrNull ?? current).copyWith(
          syncing: result.running,
          syncResult: result,
          syncError: result.needsAttention ? '课程同步未完成，请重试' : null,
          clearSyncError: !result.needsAttention,
        ),
      );
      if (result.running && result.id.isNotEmpty) {
        await _pollCourseSync(result.id);
      } else if (result.succeeded) {
        await refresh(silent: true);
      }
    } on ApiException catch (error) {
      if (_disposed) return;
      state = AsyncData(
        (state.valueOrNull ?? current).copyWith(
          syncing: false,
          syncError: error.userMessage,
        ),
      );
    } catch (_) {
      if (_disposed) return;
      state = AsyncData(
        (state.valueOrNull ?? current).copyWith(
          syncing: false,
          syncError: '课程同步请求未完成，请稍后重试',
        ),
      );
    }
  }

  Future<void> _pollCourseSync(String id) async {
    if (_polling) return;
    _polling = true;
    try {
      for (var attempt = 0; attempt < 2400; attempt++) {
        await Future<void>.delayed(const Duration(milliseconds: 3000));
        if (_disposed) {
          return;
        }
        final result =
            await ref.read(learningRepositoryProvider).fetchCourseSync(id);
        if (_disposed) {
          return;
        }
        final current = state.valueOrNull;
        if (current == null) return;
        final finished = !result.running;
        state = AsyncData(
          current.copyWith(
            syncing: !finished,
            syncResult: result,
            syncError: result.needsAttention ? '课程同步未完成，请重试' : null,
            clearSyncError: !result.needsAttention,
          ),
        );
        if (finished) {
          await refresh(silent: true);
          return;
        }
        if (attempt % 10 == 9) {
          await refresh(silent: true);
        }
      }
      if (_disposed) return;
      final current = state.valueOrNull;
      if (current != null) {
        state = AsyncData(
          current.copyWith(
            syncing: false,
            syncError: '课程同步时间较长，请稍后重试',
          ),
        );
      }
    } catch (_) {
      if (_disposed) return;
      final current = state.valueOrNull;
      if (current != null) {
        state = AsyncData(
          current.copyWith(
            syncing: false,
            syncError: '课程同步状态读取未完成，请重试',
          ),
        );
      }
    } finally {
      _polling = false;
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
