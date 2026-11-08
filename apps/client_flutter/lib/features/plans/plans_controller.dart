import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/models/page.dart';
import '../../core/models/plan.dart';
import '../../core/models/task.dart';
import '../../core/repositories/learning_repository.dart';
import '../../core/realtime/realtime_event.dart';

const planTasksPageSize = 10;

class PlansState {
  const PlansState({
    this.plans = const <LearningPlan>[],
    this.tasks = const <LearningTask>[],
    this.plansCursor,
    this.tasksCursor,
    this.selectedPlanId,
    this.taskCategory = 'current',
    this.taskPage = 0,
    this.taskCursors = const <String?>[null],
    this.taskProgress = const PlanProgress(),
    this.taskTotal = 0,
    this.refreshing = false,
    this.tasksRefreshing = false,
    this.plansLoadingMore = false,
    this.tasksLoadingMore = false,
    this.controlPendingPlanId,
    this.notice,
    this.error,
  });
  final List<LearningPlan> plans;
  final List<LearningTask> tasks;
  final String? plansCursor, tasksCursor, selectedPlanId;
  final String taskCategory;
  final int taskPage, taskTotal;
  final List<String?> taskCursors;
  final PlanProgress taskProgress;
  final bool refreshing, tasksRefreshing, plansLoadingMore, tasksLoadingMore;
  final String? controlPendingPlanId, notice, error;
  bool get refreshingTasks => tasksRefreshing;
  bool get loadingMoreTasks => tasksLoadingMore;
  bool get loadingMorePlans => plansLoadingMore;
  bool get plansHasMore => hasMorePlans;
  bool get tasksHasMore => hasMoreTasks;
  List<LearningTask> get activeTasks =>
      tasks.where((task) => task.category == 'current').toList(growable: false);
  List<LearningTask> get attentionTasks => tasks
      .where((task) => task.category == 'attention')
      .toList(growable: false);
  List<LearningTask> get closedTasks =>
      tasks.where((task) => task.category == 'closed').toList(growable: false);
  List<LearningTask> get historyTasks =>
      tasks.where((task) => task.category == 'history').toList(growable: false);
  bool get hasMorePlans => plansCursor?.isNotEmpty ?? false;
  bool get hasMoreTasks => tasksCursor?.isNotEmpty ?? false;
  bool get hasPreviousTasks => taskPage > 0;
  static bool isClosedTask(LearningTask task) => task.isClosed;
  static bool isAttentionTask(LearningTask task) =>
      !task.isClosed &&
      (task.state == LearningTaskState.needsAttention ||
          task.state == LearningTaskState.failed);
  int get attentionCount => taskProgress.attention;
  int get closedCount => taskProgress.closed;
  LearningPlan? get selectedPlan {
    for (final plan in plans) {
      if (plan.id == selectedPlanId) return plan;
    }
    return null;
  }

  PlansState copyWith({
    List<LearningPlan>? plans,
    List<LearningTask>? tasks,
    String? plansCursor,
    String? tasksCursor,
    bool clearPlansCursor = false,
    bool clearTasksCursor = false,
    String? selectedPlanId,
    bool clearSelected = false,
    String? taskCategory,
    int? taskPage,
    int? taskTotal,
    List<String?>? taskCursors,
    PlanProgress? taskProgress,
    bool? refreshing,
    bool? tasksRefreshing,
    bool? plansLoadingMore,
    bool? tasksLoadingMore,
    String? controlPendingPlanId,
    bool clearControlPending = false,
    String? notice,
    String? error,
    bool clearNotice = false,
    bool clearError = false,
  }) =>
      PlansState(
        plans: plans ?? this.plans,
        tasks: tasks ?? this.tasks,
        plansCursor: clearPlansCursor ? null : plansCursor ?? this.plansCursor,
        tasksCursor: clearTasksCursor ? null : tasksCursor ?? this.tasksCursor,
        selectedPlanId:
            clearSelected ? null : selectedPlanId ?? this.selectedPlanId,
        taskCategory: taskCategory ?? this.taskCategory,
        taskPage: taskPage ?? this.taskPage,
        taskCursors: taskCursors ?? this.taskCursors,
        taskProgress: taskProgress ?? this.taskProgress,
        taskTotal: taskTotal ?? this.taskTotal,
        refreshing: refreshing ?? this.refreshing,
        tasksRefreshing: tasksRefreshing ?? this.tasksRefreshing,
        plansLoadingMore: plansLoadingMore ?? this.plansLoadingMore,
        tasksLoadingMore: tasksLoadingMore ?? this.tasksLoadingMore,
        controlPendingPlanId: clearControlPending
            ? null
            : controlPendingPlanId ?? this.controlPendingPlanId,
        notice: clearNotice ? null : notice ?? this.notice,
        error: clearError ? null : error ?? this.error,
      );
}

class PlansController extends AsyncNotifier<PlansState> {
  StreamSubscription<RealtimeEvent>? _events;
  Timer? _refreshTimer;
  bool _disposed = false, _refreshQueued = false;
  int _tasksRequest = 0, _accountEpoch = 0;

  @override
  Future<PlansState> build() async {
    ++_accountEpoch;
    ref.watch(sessionEpochProvider);
    _disposed = false;
    final user = ref.watch(currentUserProvider);
    _events = ref.watch(realtimeServiceProvider).events.listen(_onEvent);
    ref.onDispose(() {
      ++_accountEpoch;
      ++_tasksRequest;
      _disposed = true;
      _refreshTimer?.cancel();
      _events?.cancel();
      _events = null;
    });
    if (user == null) return const PlansState();
    final repository = ref.watch(learningRepositoryProvider);
    final plans = await repository.fetchPlans();
    final selected = _pickSelectedPlan(plans.items, null);
    final tasks = await _fetchTaskPage(repository, selected, 'current', null);
    return _pageState(
        PlansState(
            plans: plans.items,
            plansCursor: plans.nextCursor,
            selectedPlanId: selected),
        tasks,
        pageIndex: 0,
        cursors: const [null]);
  }

  void _onEvent(RealtimeEvent event) {
    final current = state.valueOrNull;
    if (current == null) return;
    if (event.type == 'client.resumed' ||
        event.type == 'client.poll' ||
        event.type.startsWith('plan.') ||
        event.type.startsWith('task.') ||
        event.type.startsWith('course.')) {
      _refreshTimer ??= Timer(const Duration(milliseconds: 750), () {
        _refreshTimer = null;
        unawaited(refreshPlans(silent: true));
      });
    }
  }

  Future<Page<LearningTask>> _fetchTaskPage(LearningRepository repository,
      String? planId, String category, String? cursor) {
    if (planId == null) {
      return Future.value(const Page(
          items: <LearningTask>[],
          totalCount: 0,
          taskMeta: {'category': 'current', 'progress': <String, dynamic>{}}));
    }
    return repository.fetchTasks(
        planId: planId,
        category: category,
        cursor: cursor,
        limit: planTasksPageSize);
  }

  PlansState _pageState(PlansState current, Page<LearningTask> page,
      {required int pageIndex, required List<String?> cursors}) {
    if (current.selectedPlanId != null &&
        (page.taskMeta == null ||
            page.totalCount == null ||
            page.taskMeta!['category'] != current.taskCategory)) {
      throw const FormatException('任务分页响应缺少一致的分类与统计');
    }
    if (page.items.any((task) => task.category != current.taskCategory)) {
      throw const FormatException('任务分类与列表不一致');
    }
    return current.copyWith(
        tasks: page.items,
        tasksCursor: page.nextCursor,
        clearTasksCursor: !page.hasMore,
        taskPage: pageIndex,
        taskCursors: cursors,
        taskTotal: page.totalCount ?? 0,
        taskProgress: PlanProgress.parse(page.taskMeta?['progress']),
        tasksRefreshing: false,
        tasksLoadingMore: false,
        clearError: true);
  }

  String? _pickSelectedPlan(List<LearningPlan> plans, String? wanted) {
    if (wanted != null && plans.any((plan) => plan.id == wanted)) return wanted;
    return plans.isEmpty ? null : plans.first.id;
  }

  Future<void> refresh({bool silent = false}) => refreshPlans(silent: silent);
  Future<void> ensureSelection(String? planId) async {
    if (planId != null && state.valueOrNull?.selectedPlanId != planId) {
      await selectPlan(planId);
    }
  }

  Future<void> selectPlan(String? planId) =>
      _refreshTasks(planId, selectPlan: true);
  Future<void> selectTaskCategory(String category) async {
    final current = state.valueOrNull;
    if (current == null ||
        current.taskCategory == category ||
        !const ['current', 'attention', 'closed', 'history']
            .contains(category)) {
      return;
    }
    state = AsyncData(current.copyWith(
        taskCategory: category,
        tasks: const [],
        taskPage: 0,
        taskCursors: const [null],
        clearTasksCursor: true));
    await _goTaskPage(0, null, const [null], reset: true);
  }

  Future<void> _refreshTasks(String? planId, {bool selectPlan = false}) async {
    final current = state.valueOrNull;
    if (current == null) return;
    if (selectPlan) {
      state = AsyncData(current.copyWith(
          selectedPlanId: planId,
          clearSelected: planId == null,
          tasks: const [],
          taskPage: 0,
          taskCursors: const [null],
          clearTasksCursor: true));
    }
    final latest = state.valueOrNull!;
    await _goTaskPage(latest.taskPage, latest.taskCursors[latest.taskPage],
        latest.taskCursors,
        reset: selectPlan);
  }

  Future<void> loadMoreTasks() async {
    final current = state.valueOrNull;
    if (current == null ||
        current.tasksRefreshing ||
        current.tasksLoadingMore ||
        !current.hasMoreTasks) {
      return;
    }
    if (current.taskCursors.contains(current.tasksCursor)) {
      state = AsyncData(
          current.copyWith(error: '任务游标重复，请刷新列表', clearTasksCursor: true));
      return;
    }
    await _goTaskPage(current.taskPage + 1, current.tasksCursor, [
      ...current.taskCursors.take(current.taskPage + 1),
      current.tasksCursor
    ]);
  }

  Future<void> previousTasksPage() async {
    final current = state.valueOrNull;
    if (current == null ||
        current.tasksRefreshing ||
        current.tasksLoadingMore ||
        !current.hasPreviousTasks) {
      return;
    }
    await _goTaskPage(current.taskPage - 1,
        current.taskCursors[current.taskPage - 1], current.taskCursors);
  }

  Future<void> _goTaskPage(int pageIndex, String? cursor, List<String?> cursors,
      {bool reset = false}) async {
    final current = state.valueOrNull;
    if (current == null) return;
    final request = ++_tasksRequest, epoch = _accountEpoch;
    final planId = current.selectedPlanId, category = current.taskCategory;
    state =
        AsyncData(current.copyWith(tasksRefreshing: true, clearError: true));
    try {
      final repository = ref.read(learningRepositoryProvider);
      Page<LearningTask> result;
      try {
        result = await _fetchTaskPage(repository, planId, category, cursor);
      } on ApiException catch (error) {
        if (error.code != 'task_list_changed' || cursor == null) rethrow;
        result = await _fetchTaskPage(repository, planId, category, null);
        pageIndex = 0;
        cursors = const [null];
      }
      if (!_currentTaskRequest(request, epoch, planId, category)) return;
      if (pageIndex > 0 &&
          (result.items.isEmpty ||
              pageIndex * planTasksPageSize >= (result.totalCount ?? 0))) {
        result = await _fetchTaskPage(repository, planId, category, null);
        pageIndex = 0;
        cursors = const [null];
      }
      if (!_currentTaskRequest(request, epoch, planId, category)) return;
      if (cursor != null && result.nextCursor == cursor) {
        throw const FormatException('任务游标没有推进');
      }
      if (_refreshQueued) {
        state = AsyncData(state.valueOrNull!
            .copyWith(tasksRefreshing: false, tasksLoadingMore: false));
        return;
      }
      state = AsyncData(_pageState(state.valueOrNull!, result,
          pageIndex: pageIndex, cursors: cursors));
    } on ApiException catch (error) {
      if (!_currentTaskRequest(request, epoch, planId, category)) return;
      state = AsyncData(state.valueOrNull!.copyWith(
          tasksRefreshing: false,
          tasksLoadingMore: false,
          error: error.userMessage));
    } catch (_) {
      if (!_currentTaskRequest(request, epoch, planId, category)) return;
      state = AsyncData(state.valueOrNull!.copyWith(
          tasksRefreshing: false,
          tasksLoadingMore: false,
          error: '任务列表暂时无法刷新，请重试'));
    } finally {
      if (_currentTaskRequest(request, epoch, planId, category) &&
          _refreshQueued) {
        _refreshQueued = false;
        unawaited(refreshPlans(silent: true));
      }
    }
  }

  bool _currentTaskRequest(
          int request, int epoch, String? planId, String category) =>
      !_disposed &&
      epoch == _accountEpoch &&
      request == _tasksRequest &&
      state.valueOrNull?.selectedPlanId == planId &&
      state.valueOrNull?.taskCategory == category;

  Future<void> refreshPlans({bool silent = false}) async {
    final current = state.valueOrNull;
    if (current == null) {
      ref.invalidateSelf();
      return;
    }
    if (current.refreshing || current.tasksRefreshing) {
      _refreshQueued = true;
      return;
    }
    final epoch = _accountEpoch;
    state = AsyncData(current.copyWith(refreshing: true, clearError: true));
    try {
      final repository = ref.read(learningRepositoryProvider);
      final first = await repository.fetchPlans();
      final plans = [...first.items];
      var cursor = first.nextCursor;
      final seen = <String>{};
      while (plans.length < current.plans.length &&
          cursor != null &&
          cursor.isNotEmpty) {
        if (!seen.add(cursor)) throw const FormatException('安排游标重复');
        final next = await repository.fetchPlans(cursor: cursor);
        plans.addAll(next.items);
        cursor = next.nextCursor;
      }
      if (_disposed || epoch != _accountEpoch) return;
      final latest = state.valueOrNull!,
          selected = _pickSelectedPlan(plans, latest.selectedPlanId);
      final changed = selected != latest.selectedPlanId;
      state = AsyncData(latest.copyWith(
          plans: plans,
          plansCursor: cursor,
          clearPlansCursor: cursor == null || cursor.isEmpty,
          selectedPlanId: selected,
          clearSelected: selected == null,
          refreshing: false,
          tasks: changed ? const [] : latest.tasks,
          taskPage: changed ? 0 : latest.taskPage,
          taskCursors: changed ? const [null] : latest.taskCursors));
      await _refreshTasks(selected);
    } on ApiException catch (error) {
      if (!_disposed && epoch == _accountEpoch) {
        state = AsyncData(state.valueOrNull!
            .copyWith(refreshing: false, error: error.userMessage));
      }
    } catch (_) {
      if (!_disposed && epoch == _accountEpoch) {
        state = AsyncData(state.valueOrNull!
            .copyWith(refreshing: false, error: '安排暂时无法刷新，请稍后重试'));
      }
    }
  }

  Future<void> loadMorePlans() async {
    final current = state.valueOrNull;
    if (current == null || current.plansLoadingMore || !current.hasMorePlans) {
      return;
    }
    final epoch = _accountEpoch;
    state =
        AsyncData(current.copyWith(plansLoadingMore: true, clearError: true));
    try {
      final page = await ref
          .read(learningRepositoryProvider)
          .fetchPlans(cursor: current.plansCursor);
      if (_disposed || epoch != _accountEpoch) return;
      if (page.nextCursor == current.plansCursor) {
        throw const FormatException('安排游标没有推进');
      }
      final latest = state.valueOrNull!;
      final byId = {
        for (final plan in [...latest.plans, ...page.items]) plan.id: plan
      };
      state = AsyncData(latest.copyWith(
          plans: byId.values.toList(),
          plansCursor: page.nextCursor,
          clearPlansCursor: !page.hasMore,
          plansLoadingMore: false));
    } catch (_) {
      if (!_disposed && epoch == _accountEpoch) {
        state = AsyncData(state.valueOrNull!
            .copyWith(plansLoadingMore: false, error: '更多安排暂时无法加载'));
      }
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

  Future<bool> deletePlan({required String planId}) async {
    final current = state.valueOrNull;
    if (current == null || current.controlPendingPlanId != null) return false;
    final epoch = _accountEpoch;
    state = AsyncData(
      current.copyWith(
        controlPendingPlanId: planId,
        clearNotice: true,
        clearError: true,
      ),
    );
    try {
      await ref.read(learningRepositoryProvider).deletePlan(id: planId);
      if (_disposed || epoch != _accountEpoch) return false;
      final latest = state.valueOrNull ?? current;
      final removedSelected = latest.selectedPlanId == planId;
      state = AsyncData(
        latest.copyWith(
          plans: latest.plans
              .where((plan) => plan.id != planId)
              .toList(growable: false),
          clearSelected: removedSelected,
          tasks: removedSelected ? const <LearningTask>[] : latest.tasks,
          clearTasksCursor: removedSelected,
          clearControlPending: true,
          notice: '安排已删除，已有任务和历史记录保留',
        ),
      );
      await refreshPlans(silent: true);
      return true;
    } on ApiException catch (error) {
      if (_disposed || epoch != _accountEpoch) return false;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          clearControlPending: true,
          error: error.userMessage,
        ),
      );
      return false;
    } catch (_) {
      if (_disposed || epoch != _accountEpoch) return false;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          clearControlPending: true,
          error: '安排未删除，请稍后重试',
        ),
      );
      return false;
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
