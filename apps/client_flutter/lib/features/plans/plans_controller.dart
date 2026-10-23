import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/models/plan.dart';
import '../../core/models/task.dart';
import '../../core/realtime/realtime_event.dart';

class PlansState {
  const PlansState({
    this.plans = const <LearningPlan>[],
    this.selectedPlanId,
    this.tasks = const <LearningTask>[],
    this.tasksCursor,
    this.refreshingPlans = false,
    this.refreshingTasks = false,
    this.loadingMoreTasks = false,
    this.controlPendingPlanId,
    this.notice,
    this.error,
  });

  final List<LearningPlan> plans;
  final String? selectedPlanId;
  final List<LearningTask> tasks;

  /// 当前安排任务列表的下一页游标；刷新或切换安排后必须重置。
  final String? tasksCursor;
  final bool refreshingPlans;
  final bool refreshingTasks;
  final bool loadingMoreTasks;
  final String? controlPendingPlanId;
  final String? notice;
  final String? error;

  LearningPlan? get selectedPlan {
    for (final plan in plans) {
      if (plan.id == selectedPlanId) return plan;
    }
    return plans.isEmpty ? null : plans.first;
  }

  bool get tasksHasMore => tasksCursor != null && tasksCursor!.isNotEmpty;

  List<LearningTask> get activeTasks => tasks
      .where((task) =>
          task.state == LearningTaskState.pending ||
          task.state == LearningTaskState.preparing ||
          task.state == LearningTaskState.running ||
          task.state == LearningTaskState.waitingVerification ||
          task.state == LearningTaskState.paused)
      .toList(growable: false);

  List<LearningTask> get attentionTasks => tasks
      .where((task) =>
          task.state == LearningTaskState.needsAttention ||
          task.state == LearningTaskState.failed)
      .toList(growable: false);

  List<LearningTask> get historyTasks => tasks
      .where((task) =>
          task.state == LearningTaskState.confirmed ||
          task.state == LearningTaskState.cancelled)
      .toList(growable: false);

  PlansState copyWith({
    List<LearningPlan>? plans,
    String? selectedPlanId,
    List<LearningTask>? tasks,
    String? tasksCursor,
    bool? refreshingPlans,
    bool? refreshingTasks,
    bool? loadingMoreTasks,
    String? controlPendingPlanId,
    String? notice,
    String? error,
    bool clearControlPending = false,
    bool clearTasksCursor = false,
    bool clearNotice = false,
    bool clearError = false,
    bool clearSelected = false,
  }) {
    return PlansState(
      plans: plans ?? this.plans,
      selectedPlanId:
          clearSelected ? null : selectedPlanId ?? this.selectedPlanId,
      tasks: tasks ?? this.tasks,
      tasksCursor: clearTasksCursor ? null : tasksCursor ?? this.tasksCursor,
      refreshingPlans: refreshingPlans ?? this.refreshingPlans,
      refreshingTasks: refreshingTasks ?? this.refreshingTasks,
      loadingMoreTasks: loadingMoreTasks ?? this.loadingMoreTasks,
      controlPendingPlanId: clearControlPending
          ? null
          : controlPendingPlanId ?? this.controlPendingPlanId,
      notice: clearNotice ? null : notice ?? this.notice,
      error: clearError ? null : error ?? this.error,
    );
  }
}

class PlansController extends AsyncNotifier<PlansState> {
  StreamSubscription<RealtimeEvent>? _events;
  String? _preferredPlanId;

  /// 任务列表请求序号与在途计数：切安排时旧响应不得覆盖新选择。
  int _taskRequestSeq = 0;
  final Map<String, int> _tasksInFlight = <String, int>{};

  /// 最近一次任务请求所属安排；用于识别切回旧安排时在途请求已过期。
  String? _latestTaskRequestKey;

  /// 账户代次：重建后旧账户的在途安排请求不得写入新用户状态。
  int _accountEpoch = 0;
  bool _disposed = false;

  @override
  Future<PlansState> build() async {
    final epoch = ++_accountEpoch;
    _disposed = false;
    ref.onDispose(() {
      if (epoch == _accountEpoch) _disposed = true;
    });
    _taskRequestSeq++;
    _tasksInFlight.clear();
    _latestTaskRequestKey = null;
    ref.watch(sessionEpochProvider);
    final user = ref.watch(currentUserProvider);
    _events = ref.watch(realtimeServiceProvider).events.listen(_onEvent);
    ref.onDispose(() {
      _events?.cancel();
      _events = null;
    });
    if (user == null) return const PlansState();
    final repository = ref.watch(learningRepositoryProvider);
    final plansPage = await repository.fetchPlans();
    var selected = _preferredPlanId;
    if (selected == null ||
        plansPage.items.every((plan) => plan.id != selected)) {
      selected = plansPage.items.isEmpty ? null : plansPage.items.first.id;
    }
    final tasks = selected == null
        ? await repository.fetchTasks()
        : await repository.fetchTasks(planId: selected);
    return PlansState(
      plans: plansPage.items,
      selectedPlanId: selected,
      tasks: tasks.items,
      tasksCursor: tasks.nextCursor,
    );
  }

  void _onEvent(RealtimeEvent event) {
    if (_disposed) return;
    final current = state.valueOrNull;
    if (current == null) return;
    if (event.type == 'client.resume') {
      unawaited(refresh(silent: true));
      return;
    }
    if (event.type == 'client.poll') {
      // SSE 降级期间的计划启停变化也要通过有界轮询合并到界面。
      unawaited(refreshPlans(silent: true));
      if (current.tasks.any((task) =>
          task.state.isActive ||
          task.state == LearningTaskState.waitingVerification)) {
        unawaited(_refreshTasks(silent: true));
      }
      return;
    }
    if (event.type.startsWith('plan.')) {
      unawaited(refreshPlans(silent: true));
    }
    if (event.type.startsWith('task.')) {
      unawaited(_refreshTasks(silent: true));
    }
  }

  Future<void> ensureSelection(String? planId) async {
    if (planId == null || planId.isEmpty) return;
    _preferredPlanId = planId;
    final current = state.valueOrNull;
    if (current == null) return;
    if (current.selectedPlanId == planId ||
        current.plans.every((plan) => plan.id != planId)) {
      return;
    }
    await selectPlan(planId);
  }

  Future<void> selectPlan(String planId) async {
    final current = state.valueOrNull;
    if (current == null || current.selectedPlanId == planId) return;
    state = AsyncData(
      current.copyWith(
        selectedPlanId: planId,
        tasks: const <LearningTask>[],
        refreshingTasks: true,
        loadingMoreTasks: false,
        clearTasksCursor: true,
        clearError: true,
      ),
    );
    await _refreshTasks(silent: true, targetPlanId: planId);
  }

  Future<void> refresh({bool silent = false}) async {
    final current = state.valueOrNull;
    if (current == null) {
      ref.invalidateSelf();
      return;
    }
    await refreshPlans(silent: silent);
    await _refreshTasks(silent: silent);
  }

  Future<void> refreshPlans({bool silent = false}) async {
    final current = state.valueOrNull;
    if (current == null) return;
    if (current.refreshingPlans) return;
    final epoch = _accountEpoch;
    state = AsyncData(
      current.copyWith(refreshingPlans: true, clearError: true),
    );
    try {
      final page = await ref.read(learningRepositoryProvider).fetchPlans();
      if (_disposed || epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      var selected = latest.selectedPlanId;
      if (selected == null || page.items.every((plan) => plan.id != selected)) {
        selected = page.items.isEmpty ? null : page.items.first.id;
      }
      final selectionChanged = selected != latest.selectedPlanId;
      state = AsyncData(
        latest.copyWith(
          plans: page.items,
          selectedPlanId: selected,
          tasks: selectionChanged ? const <LearningTask>[] : latest.tasks,
          clearTasksCursor: selectionChanged,
          refreshingTasks: selected == null ? false : latest.refreshingTasks,
          loadingMoreTasks: selectionChanged ? false : latest.loadingMoreTasks,
          refreshingPlans: false,
          clearSelected: selected == null,
        ),
      );
      if (selected != null && selectionChanged) {
        await _refreshTasks(silent: true, targetPlanId: selected);
      }
    } on ApiException catch (error) {
      if (_disposed || epoch != _accountEpoch) return;
      state = AsyncData(
        (state.valueOrNull ?? current).copyWith(
          refreshingPlans: false,
          error: error.userMessage,
        ),
      );
    } catch (_) {
      if (_disposed || epoch != _accountEpoch) return;
      state = AsyncData(
        (state.valueOrNull ?? current).copyWith(
          refreshingPlans: false,
          error: '安排暂时无法刷新',
        ),
      );
    }
  }

  Future<void> _refreshTasks({
    bool silent = false,
    String? targetPlanId,
  }) async {
    final current = state.valueOrNull;
    if (current == null) return;
    final planId = targetPlanId ?? current.selectedPlanId;
    final key = planId ?? '';
    // 同一安排的重复刷新合并为一次请求；不同安排必须并行发出，
    // 否则切换安排会被在途请求挡住并显示上一安排的任务。
    // 若最近一次请求已属于其他安排，当前安排在途请求已过期，必须重新发出。
    if ((_tasksInFlight[key] ?? 0) > 0 && _latestTaskRequestKey == key) {
      return;
    }
    _tasksInFlight[key] = (_tasksInFlight[key] ?? 0) + 1;
    _latestTaskRequestKey = key;
    final seq = ++_taskRequestSeq;
    state = AsyncData(
      (state.valueOrNull ?? current).copyWith(refreshingTasks: true),
    );
    try {
      final page = await ref.read(learningRepositoryProvider).fetchTasks(
            planId: planId,
          );
      if (_disposed || seq != _taskRequestSeq) return;
      final latest = state.valueOrNull;
      if (latest == null || latest.selectedPlanId != planId) return;
      state = AsyncData(
        latest.copyWith(
          tasks: page.items,
          tasksCursor: page.nextCursor,
          clearTasksCursor: page.nextCursor == null,
          refreshingTasks: false,
          loadingMoreTasks: false,
        ),
      );
    } on ApiException catch (error) {
      if (_disposed || seq != _taskRequestSeq) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          refreshingTasks: false,
          loadingMoreTasks: false,
          error: error.userMessage,
        ),
      );
    } catch (_) {
      if (_disposed || seq != _taskRequestSeq) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          refreshingTasks: false,
          loadingMoreTasks: false,
          error: '任务列表暂时无法刷新',
        ),
      );
    } finally {
      final remaining = (_tasksInFlight[key] ?? 1) - 1;
      if (remaining <= 0) {
        _tasksInFlight.remove(key);
      } else {
        _tasksInFlight[key] = remaining;
      }
      if (seq == _taskRequestSeq) {
        _latestTaskRequestKey = null;
      }
    }
  }

  Future<void> loadMoreTasks() async {
    final current = state.valueOrNull;
    if (current == null ||
        !current.tasksHasMore ||
        current.refreshingTasks ||
        current.loadingMoreTasks) {
      return;
    }
    final epoch = _accountEpoch;
    final seq = _taskRequestSeq;
    final planId = current.selectedPlanId;
    final cursor = current.tasksCursor!;
    state = AsyncData(
      current.copyWith(loadingMoreTasks: true, clearError: true),
    );
    try {
      final page = await ref.read(learningRepositoryProvider).fetchTasks(
            planId: planId,
            cursor: cursor,
          );
      if (_disposed || epoch != _accountEpoch || seq != _taskRequestSeq) return;
      final latest = state.valueOrNull;
      if (latest == null || latest.selectedPlanId != planId) return;
      final known = latest.tasks.map((task) => task.id).toSet();
      final appended = page.items
          .where((task) => known.add(task.id))
          .toList(growable: false);
      // 服务端正常推进游标；若返回同一游标则停止继续加载，避免重复请求。
      final nextCursor = page.nextCursor == cursor ? null : page.nextCursor;
      state = AsyncData(
        latest.copyWith(
          tasks: [...latest.tasks, ...appended],
          tasksCursor: nextCursor,
          clearTasksCursor: nextCursor == null,
          loadingMoreTasks: false,
        ),
      );
    } on ApiException catch (error) {
      if (_disposed || epoch != _accountEpoch || seq != _taskRequestSeq) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          loadingMoreTasks: false,
          error: error.userMessage,
        ),
      );
    } catch (_) {
      if (_disposed || epoch != _accountEpoch || seq != _taskRequestSeq) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          loadingMoreTasks: false,
          error: '更多任务暂时无法加载',
        ),
      );
    }
  }

  Future<PlanControlResult?> controlPlan({
    required String planId,
    required String action,
  }) async {
    final current = state.valueOrNull;
    if (current == null || current.controlPendingPlanId != null) return null;
    final epoch = _accountEpoch;
    state = AsyncData(
      current.copyWith(
        controlPendingPlanId: planId,
        clearNotice: true,
        clearError: true,
      ),
    );
    try {
      final result = await ref.read(learningRepositoryProvider).controlPlan(
            id: planId,
            action: action,
          );
      if (_disposed || epoch != _accountEpoch) return result;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          clearControlPending: true,
          notice: result.applied
              ? (action == 'pause' ? '安排已暂停后续任务' : '安排已恢复')
              : '操作已提交，正在等待状态更新',
        ),
      );
      await refreshPlans(silent: true);
      return result;
    } on ApiException catch (error) {
      if (_disposed || epoch != _accountEpoch) return null;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          clearControlPending: true,
          error: error.userMessage,
        ),
      );
      return null;
    } catch (_) {
      if (_disposed || epoch != _accountEpoch) return null;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          clearControlPending: true,
          error: '安排启停未完成，请稍后重试',
        ),
      );
      return null;
    }
  }

  Future<void> afterPlanSaved(String planId) async {
    final epoch = _accountEpoch;
    await refreshPlans(silent: true);
    if (_disposed || epoch != _accountEpoch) return;
    final current = state.valueOrNull;
    if (current != null && current.selectedPlanId != planId) {
      await selectPlan(planId);
    }
    if (_disposed || epoch != _accountEpoch) return;
    state = AsyncData(
      (state.valueOrNull ?? const PlansState()).copyWith(
        notice: '安排已保存，修改用于后续任务',
      ),
    );
  }

  void clearBanner() {
    final current = state.valueOrNull;
    if (current == null) return;
    state = AsyncData(
      current.copyWith(clearNotice: true, clearError: true),
    );
  }
}

final plansControllerProvider =
    AsyncNotifierProvider<PlansController, PlansState>(PlansController.new);

class TaskDetailState {
  const TaskDetailState({
    required this.detail,
    this.refreshing = false,
    this.commandPending = false,
    this.notice,
    this.error,
  });

  final TaskDetail detail;
  final bool refreshing;
  final bool commandPending;
  final String? notice;
  final String? error;

  TaskDetailState copyWith({
    TaskDetail? detail,
    bool? refreshing,
    bool? commandPending,
    String? notice,
    String? error,
    bool clearNotice = false,
    bool clearError = false,
  }) {
    return TaskDetailState(
      detail: detail ?? this.detail,
      refreshing: refreshing ?? this.refreshing,
      commandPending: commandPending ?? this.commandPending,
      notice: clearNotice ? null : notice ?? this.notice,
      error: clearError ? null : error ?? this.error,
    );
  }
}

class TaskDetailController
    extends AutoDisposeFamilyAsyncNotifier<TaskDetailState, String> {
  StreamSubscription<RealtimeEvent>? _events;

  /// 账户代次：重建后旧账户的在途响应不得写入新用户状态。
  int _accountEpoch = 0;

  @override
  Future<TaskDetailState> build(String taskId) async {
    ++_accountEpoch;
    ref.watch(sessionEpochProvider);
    _events = ref.watch(realtimeServiceProvider).events.listen((event) {
      final current = state.valueOrNull;
      if (current == null) return;
      if (event.type == 'client.resume') {
        unawaited(refresh(silent: true));
        return;
      }
      if (event.type == 'client.poll') {
        if (!current.detail.task.state.isTerminal) {
          unawaited(refresh(silent: true));
        }
        return;
      }
      if (event.type.startsWith('task.') &&
          (event.resourceId == null || event.resourceId == taskId)) {
        unawaited(refresh(silent: true));
      }
    });
    ref.onDispose(() {
      _events?.cancel();
      _events = null;
    });
    final detail =
        await ref.watch(learningRepositoryProvider).fetchTask(taskId);
    return TaskDetailState(detail: detail);
  }

  Future<void> refresh({bool silent = false}) async {
    final current = state.valueOrNull;
    if (current == null) {
      ref.invalidateSelf();
      return;
    }
    if (current.refreshing) return;
    final epoch = _accountEpoch;
    state = AsyncData(current.copyWith(refreshing: true, clearError: true));
    try {
      final detail = await ref.read(learningRepositoryProvider).fetchTask(arg);
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(detail: detail, refreshing: false, clearError: true),
      );
    } on ApiException catch (error) {
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(refreshing: false, error: error.userMessage),
      );
    } catch (_) {
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(refreshing: false, error: '任务详情暂时无法刷新'),
      );
    }
  }

  Future<void> command(String action, {String? reason}) async {
    final current = state.valueOrNull;
    if (current == null || current.commandPending) return;
    final epoch = _accountEpoch;
    state = AsyncData(
      current.copyWith(
        commandPending: true,
        clearNotice: true,
        clearError: true,
      ),
    );
    try {
      final result = await ref.read(learningRepositoryProvider).controlTask(
            id: arg,
            action: action,
            reason: reason,
          );
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          commandPending: false,
          notice: result.message,
        ),
      );
      await refresh(silent: true);
    } on ApiException catch (error) {
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          commandPending: false,
          error: error.userMessage,
        ),
      );
    } catch (_) {
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          commandPending: false,
          error: '操作未完成，请稍后重试',
        ),
      );
    }
  }

  void clearNotice() {
    final current = state.valueOrNull;
    if (current == null) return;
    state = AsyncData(current.copyWith(clearNotice: true, clearError: true));
  }
}

final taskDetailProvider = AsyncNotifierProvider.autoDispose
    .family<TaskDetailController, TaskDetailState, String>(
  TaskDetailController.new,
);
