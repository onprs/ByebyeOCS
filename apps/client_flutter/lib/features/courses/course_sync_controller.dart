import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/models/course.dart';
import 'courses_controller.dart';

/// 状态轮询的最长尝试次数，避免卡住的任务无限占用后续操作。
const _maxPollAttempts = 2400;

/// 课程数据超过该时间没有成功同步时，页面进入会补一次同步。
const courseSyncFreshWindow = Duration(minutes: 30);

/// 自动检查的节流间隔；页面重建不会反复请求“最近同步”接口。
const _autoCheckInterval = Duration(minutes: 5);

/// 状态轮询间隔；测试可覆盖以缩短等待。
final courseSyncPollIntervalProvider = Provider<Duration>(
  (ref) => const Duration(seconds: 3),
);

/// 可替换的时钟，保证过期判断和自动检查节流在测试中可控。
final courseSyncClockProvider = Provider<DateTime Function()>(
  (ref) => DateTime.now,
);

enum CourseSyncPhase { idle, queued, running, succeeded, partial, failed }

class CourseSyncState {
  const CourseSyncState({
    this.phase = CourseSyncPhase.idle,
    this.result,
    this.error,
    this.dismissedKey,
  });

  final CourseSyncPhase phase;
  final CourseSyncResult? result;
  final String? error;
  final String? dismissedKey;

  bool get busy =>
      phase == CourseSyncPhase.queued || phase == CourseSyncPhase.running;

  bool get hasNotice => phase != CourseSyncPhase.idle;

  /// 关闭标记按 sync id 保存；同一任务的轮询更新不会让提示重新出现。
  String? get noticeKey {
    final id = result?.id;
    if (id != null && id.isNotEmpty) return id;
    if (error != null) return 'request-error';
    if (busy) return 'requesting';
    return null;
  }

  bool get visible =>
      hasNotice && noticeKey != null && noticeKey != dismissedKey;

  /// 只对可以再次请求的情况展示重试；身份、结构适配和批量上限等问题需要其他处理。
  bool get canRetry {
    if (busy) return false;
    if (error != null) return true;
    final value = result;
    if (value == null) return false;
    if (value.status == 'failed') return true;
    if (value.status != 'needs_attention') return false;
    return !_blockedRetryCodes.contains(value.errorCode);
  }

  String get message {
    if (busy) {
      final value = result;
      if (value == null) return '正在提交课程同步…';
      final count = value.courseCount;
      if (phase == CourseSyncPhase.queued) {
        return '课程同步已排队，正在等待开始…';
      }
      return count > 0 ? '正在同步课程… 已读取 $count 门' : '正在同步课程…';
    }
    if (error != null) return error!;
    final value = result;
    if (value == null) return '';
    final count = value.courseCount;
    switch (value.errorCode) {
      case 'course_structure_partial':
        return '部分课程结构待适配，已同步可用课程。';
      case 'external_disabled':
        return '课程同步当前未开放，已有课程仍可查看。';
      case 'sync_batch_exceeded':
        return '课程数量超过单次同步上限，已保留原有课程。';
      case 'sync_attempts_exhausted':
        return '课程同步多次未完成，请稍后重试。';
      case 'identity_invalid':
      case 'verification_required':
      case 'identity_not_linked':
      case 'forbidden':
        return '学习通身份需要重新验证，课程同步已暂停。';
      case 'rate_limited':
        return '学习通平台限流，课程同步稍后重试。';
      case 'transient':
        return '课程同步暂时中断，请重试。';
    }
    if (value.succeeded) {
      return count > 0 ? '课程同步已完成，共 $count 门课程。' : '课程同步已完成，未发现可同步课程。';
    }
    if (value.status == 'needs_attention') {
      return '课程同步需要处理，请稍后重试。';
    }
    return '课程同步未完成，请重试';
  }

  static const _blockedRetryCodes = <String>{
    'course_structure_partial',
    'external_disabled',
    'sync_batch_exceeded',
    'sync_attempts_exhausted',
    'identity_invalid',
    'verification_required',
    'identity_not_linked',
    'forbidden',
  };
}

/// 课程同步的单一状态源。页面只读取这里，不再把同步状态混进课程列表。
class CourseSyncController extends Notifier<CourseSyncState> {
  Timer? _pollTimer;
  String? _pollingId;
  int _pollGeneration = 0;
  int _pollAttempt = 0;
  int _pollReadFailures = 0;
  bool _disposed = false;
  bool _checkInFlight = false;
  bool _autoRequestFailed = false;
  DateTime? _lastCheckAt;

  @override
  CourseSyncState build() {
    _pollTimer?.cancel();
    _pollTimer = null;
    _pollingId = null;
    _pollGeneration++;
    _pollAttempt = 0;
    _pollReadFailures = 0;
    _disposed = false;
    _checkInFlight = false;
    _autoRequestFailed = false;
    _lastCheckAt = null;
    ref.watch(sessionEpochProvider);
    ref.watch(currentUserProvider);
    ref.watch(authCapabilitiesProvider);
    ref.onDispose(() {
      _disposed = true;
      _pollGeneration++;
      _pollTimer?.cancel();
      _pollTimer = null;
    });
    return const CourseSyncState();
  }

  DateTime _now() => ref.read(courseSyncClockProvider)().toUtc();

  bool _expired(DateTime time) =>
      _now().difference(time.toUtc()) > courseSyncFreshWindow;

  bool _active(int generation) => !_disposed && generation == _pollGeneration;

  /// 页面进入时接续运行中的同步；没有同步记录且课程为空或过期时自动开始。
  /// 自动流程失败后不会周期重试，失败提示保留手动重试入口。
  Future<void> autoSyncIfNeeded() async {
    if (_disposed || _checkInFlight || state.busy) return;
    final coursesState = ref.read(coursesControllerProvider).valueOrNull;
    if (coursesState == null) return;
    if (ref.read(currentUserProvider) == null) return;
    if (!ref.read(authCapabilitiesProvider).courseSyncAvailable) return;

    // 本会话已经成功同步且数据未过期时，不重复访问状态接口。
    final known = state.result;
    if (known != null &&
        known.succeeded &&
        known.completedAt != null &&
        !_expired(known.completedAt!)) {
      return;
    }

    final now = _now();
    final lastCheck = _lastCheckAt;
    if (lastCheck != null && now.difference(lastCheck) < _autoCheckInterval) {
      return;
    }
    // 上一次自动请求没有提交成功时保持失败提示，由用户决定是否重试。
    if (_autoRequestFailed) return;

    _checkInFlight = true;
    _lastCheckAt = now;
    try {
      final latest =
          await ref.read(learningRepositoryProvider).fetchLatestCourseSync();
      if (_disposed) return;
      if (latest != null && latest.running) {
        if (latest.id.isEmpty) {
          _failWith('课程同步状态缺少编号，请重试', result: latest);
          return;
        }
        _showResult(latest);
        _startPolling(latest.id);
        return;
      }
      if (latest != null && latest.needsAttention) {
        _showResult(latest);
        return;
      }
      if (latest != null && latest.succeeded) {
        // 同一个任务在状态读取中断后已完成时，补上真实完成状态。
        final currentId = state.result?.id;
        if (currentId != null &&
            currentId == latest.id &&
            state.phase != CourseSyncPhase.succeeded) {
          _showResult(latest);
        }
        if (!_needsSync(coursesState.courses, latest)) return;
        await syncCourses(automatic: true);
        return;
      }
      if (!_needsSync(coursesState.courses, latest)) return;
      await syncCourses(automatic: true);
    } catch (_) {
      if (_disposed) return;
      // 状态接口不可用时不要无限补请求；只在本次会话还没有结果时尝试一次。
      if (state.phase != CourseSyncPhase.idle) return;
      if (!_needsSync(coursesState.courses, null)) return;
      await syncCourses(automatic: true);
    } finally {
      _checkInFlight = false;
    }
  }

  /// 手动重试。状态读取中断但任务仍在运行时继续轮询，避免重复创建同步。
  Future<void> retry() async {
    if (_disposed || state.busy) return;
    _autoRequestFailed = false;
    final result = state.result;
    if (result != null && result.running && result.id.isNotEmpty) {
      state = CourseSyncState(
        phase: _phaseOf(result),
        result: result,
      );
      _startPolling(result.id);
      return;
    }
    await syncCourses();
  }

  Future<void> syncCourses({bool automatic = false}) async {
    if (_disposed || state.busy) return;
    final now = _now();
    _lastCheckAt = now;
    if (!automatic) {
      _autoRequestFailed = false;
    }
    state = const CourseSyncState(phase: CourseSyncPhase.queued);
    try {
      final result =
          await ref.read(learningRepositoryProvider).requestCourseSync();
      if (_disposed) return;
      if (result.running && result.id.isEmpty) {
        _failWith('课程同步状态缺少编号，请重试', result: result);
        return;
      }
      _showResult(result);
      if (result.running) {
        _startPolling(result.id);
      } else {
        await _refreshCourses();
      }
    } on ApiException catch (error) {
      if (_disposed) return;
      _autoRequestFailed = true;
      _failWith(error.userMessage);
    } catch (_) {
      if (_disposed) return;
      _autoRequestFailed = true;
      _failWith('课程同步请求未完成，请稍后重试');
    }
  }

  void dismiss() {
    if (!state.visible || state.busy) return;
    final key = state.noticeKey;
    if (key == null) return;
    state = CourseSyncState(
      phase: state.phase,
      result: state.result,
      error: state.error,
      dismissedKey: key,
    );
  }

  bool _needsSync(
    List<CourseSummary> courses,
    CourseSyncResult? latest,
  ) {
    final latestCompleted =
        latest?.succeeded == true ? latest!.completedAt : null;
    if (latestCompleted != null) {
      return _expired(latestCompleted);
    }
    if (latest != null) return false;
    if (courses.isEmpty) return true;
    final oldest = _oldestSyncedAt(courses);
    if (oldest == null) return true;
    return _expired(oldest);
  }

  DateTime? _oldestSyncedAt(List<CourseSummary> courses) {
    DateTime? oldest;
    for (final course in courses) {
      final syncedAt = course.syncedAt;
      if (syncedAt == null) return null;
      if (oldest == null || syncedAt.isBefore(oldest)) {
        oldest = syncedAt;
      }
    }
    return oldest;
  }

  CourseSyncPhase _phaseOf(CourseSyncResult result) {
    if (result.running) {
      return result.status == 'queued'
          ? CourseSyncPhase.queued
          : CourseSyncPhase.running;
    }
    if (result.succeeded) return CourseSyncPhase.succeeded;
    if (result.errorCode == 'course_structure_partial') {
      return CourseSyncPhase.partial;
    }
    return CourseSyncPhase.failed;
  }

  void _showResult(CourseSyncResult result) {
    final phase = _phaseOf(result);
    final key = result.id.isEmpty ? null : result.id;
    state = CourseSyncState(
      phase: phase,
      result: result,
      // 同一 sync id 的结束提示保持关闭；运行中的进度必须重新可见。
      dismissedKey: !result.running && key != null && state.dismissedKey == key
          ? state.dismissedKey
          : null,
    );
  }

  void _failWith(
    String message, {
    CourseSyncResult? result,
    bool keepResult = true,
  }) {
    state = CourseSyncState(
      phase: CourseSyncPhase.failed,
      result: keepResult ? result ?? state.result : result,
      error: message,
      dismissedKey: state.dismissedKey,
    );
  }

  void _startPolling(String id) {
    if (_disposed || id.isEmpty) return;
    _pollTimer?.cancel();
    _pollingId = id;
    _pollGeneration++;
    _pollAttempt = 0;
    _pollReadFailures = 0;
    _schedulePoll(_pollGeneration);
  }

  void _schedulePoll(int generation) {
    if (!_active(generation)) return;
    _pollTimer?.cancel();
    _pollTimer = Timer(
      ref.read(courseSyncPollIntervalProvider),
      () => unawaited(_pollOnce(generation)),
    );
  }

  Future<void> _pollOnce(int generation) async {
    if (!_active(generation)) return;
    final id = _pollingId;
    if (id == null) return;
    _pollAttempt++;
    try {
      final result =
          await ref.read(learningRepositoryProvider).fetchCourseSync(id);
      if (!_active(generation)) return;
      _pollReadFailures = 0;
      _showResult(result);
      if (result.running) {
        if (_pollAttempt >= _maxPollAttempts) {
          _pollingId = null;
          _pollTimer = null;
          _failWith('课程同步时间较长，请稍后重试');
          return;
        }
        if (_pollAttempt % 10 == 0) {
          unawaited(ref
              .read(coursesControllerProvider.notifier)
              .refresh(silent: true));
        }
        _schedulePoll(generation);
        return;
      }
      _pollingId = null;
      _pollTimer = null;
      await _refreshCourses();
    } on ApiException catch (error) {
      if (!_active(generation)) return;
      if (error.isNotFound) {
        _pollingId = null;
        _pollTimer = null;
        _failWith('课程同步记录已不存在，请重试', keepResult: false);
        return;
      }
      _retryPollAfterReadFailure(generation, error.userMessage);
    } catch (_) {
      if (!_active(generation)) return;
      _retryPollAfterReadFailure(generation, '课程同步状态暂时无法读取，请稍后重试');
    }
  }

  void _retryPollAfterReadFailure(int generation, String message) {
    _pollReadFailures++;
    if (_pollReadFailures < 3) {
      _schedulePoll(generation);
      return;
    }
    _pollingId = null;
    _pollTimer = null;
    _failWith(message);
  }

  Future<void> _refreshCourses() async {
    await ref.read(coursesControllerProvider.notifier).refresh(silent: true);
  }
}

final courseSyncControllerProvider =
    NotifierProvider<CourseSyncController, CourseSyncState>(
  CourseSyncController.new,
);
