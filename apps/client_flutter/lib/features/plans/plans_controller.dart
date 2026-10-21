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
    this.refreshingPlans = false,
    this.refreshingTasks = false,
    this.controlPendingPlanId,
    this.notice,
    this.error,
  });

  final List<LearningPlan> plans;
  final String? selectedPlanId;
  final List<LearningTask> tasks;
  final bool refreshingPlans;
  final bool refreshingTasks;
  final String? controlPendingPlanId;
  final String? notice;
  final String? error;

  LearningPlan? get selectedPlan {
    for (final plan in plans) {
      if (plan.id == selectedPlanId) return plan;
    }
    return plans.isEmpty ? null : plans.first;
  }

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
    bool? refreshingPlans,
    bool? refreshingTasks,
    String? controlPendingPlanId,
    String? notice,
    String? error,
    bool clearControlPending = false,
    bool clearNotice = false,
    bool clearError = false,
    bool clearSelected = false,
  }) {
    return PlansState(
      plans: plans ?? this.plans,
      selectedPlanId:
          clearSelected ? null : selectedPlanId ?? this.selectedPlanId,
      tasks: tasks ?? this.tasks,
      refreshingPlans: refreshingPlans ?? this.refreshingPlans,
      refreshingTasks: refreshingTasks ?? this.refreshingTasks,
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
  bool _loadingTasks = false;

  @override
  Future<PlansState> build() async {
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
        ? (await repository.fetchTasks()).items
        : (await repository.fetchTasks(planId: selected)).items;
    return PlansState(
      plans: plansPage.items,
      selectedPlanId: selected,
      tasks: tasks,
    );
  }

  void _onEvent(RealtimeEvent event) {
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
    state = AsyncData(
      current.copyWith(refreshingPlans: true, clearError: true),
    );
    try {
      final page = await ref.read(learningRepositoryProvider).fetchPlans();
      final latest = state.valueOrNull ?? current;
      var selected = latest.selectedPlanId;
      if (selected == null || page.items.every((plan) => plan.id != selected)) {
        selected = page.items.isEmpty ? null : page.items.first.id;
      }
      state = AsyncData(
        latest.copyWith(
          plans: page.items,
          selectedPlanId: selected,
          refreshingPlans: false,
          clearSelected: selected == null,
        ),
      );
      if (selected != null && selected != current.selectedPlanId) {
        await _refreshTasks(silent: true, targetPlanId: selected);
      }
    } on ApiException catch (error) {
      state = AsyncData(
        (state.valueOrNull ?? current).copyWith(
          refreshingPlans: false,
          error: error.userMessage,
        ),
      );
    } catch (_) {
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
    if (_loadingTasks) return;
    final current = state.valueOrNull;
    if (current == null) return;
    final planId = targetPlanId ?? current.selectedPlanId;
    _loadingTasks = true;
    state = AsyncData(
      (state.valueOrNull ?? current).copyWith(refreshingTasks: true),
    );
    try {
      final page = await ref.read(learningRepositoryProvider).fetchTasks(
            planId: planId,
          );
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(tasks: page.items, refreshingTasks: false),
      );
    } on ApiException catch (error) {
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          refreshingTasks: false,
          error: error.userMessage,
        ),
      );
    } catch (_) {
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          refreshingTasks: false,
          error: '任务列表暂时无法刷新',
        ),
      );
    } finally {
      _loadingTasks = false;
    }
  }

  Future<PlanControlResult?> controlPlan({
    required String planId,
    required String action,
  }) async {
    final current = state.valueOrNull;
    if (current == null || current.controlPendingPlanId != null) return null;
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
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          clearControlPending: true,
          error: error.userMessage,
        ),
      );
      return null;
    } catch (_) {
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
    await refreshPlans(silent: true);
    final current = state.valueOrNull;
    if (current != null && current.selectedPlanId != planId) {
      await selectPlan(planId);
    }
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

  @override
  Future<TaskDetailState> build(String taskId) async {
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
    state = AsyncData(current.copyWith(refreshing: true, clearError: true));
    try {
      final detail = await ref.read(learningRepositoryProvider).fetchTask(arg);
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(detail: detail, refreshing: false, clearError: true),
      );
    } on ApiException catch (error) {
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(refreshing: false, error: error.userMessage),
      );
    } catch (_) {
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(refreshing: false, error: '任务详情暂时无法刷新'),
      );
    }
  }

  Future<void> command(String action, {String? reason}) async {
    final current = state.valueOrNull;
    if (current == null || current.commandPending) return;
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
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          commandPending: false,
          notice: result.message,
        ),
      );
      await refresh(silent: true);
    } on ApiException catch (error) {
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          commandPending: false,
          error: error.userMessage,
        ),
      );
    } catch (_) {
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
