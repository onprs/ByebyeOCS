import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/models/task.dart';
import '../../core/realtime/realtime_event.dart';

class HomeTasksState {
  const HomeTasksState({
    this.tasks = const <LearningTask>[],
    this.refreshing = false,
    this.error,
  });

  final List<LearningTask> tasks;
  final bool refreshing;
  final String? error;

  HomeTasksState copyWith({
    List<LearningTask>? tasks,
    bool? refreshing,
    String? error,
  }) {
    return HomeTasksState(
      tasks: tasks ?? this.tasks,
      refreshing: refreshing ?? this.refreshing,
      error: error,
    );
  }
}

class HomeTasksController extends AsyncNotifier<HomeTasksState> {
  StreamSubscription<RealtimeEvent>? _events;

  /// 账户代次：重建后旧账户的在途刷新不得写入新用户状态。
  int _accountEpoch = 0;

  @override
  Future<HomeTasksState> build() async {
    ++_accountEpoch;
    ref.watch(sessionEpochProvider);
    final user = ref.watch(currentUserProvider);
    _events = ref.watch(realtimeServiceProvider).events.listen(_onEvent);
    ref.onDispose(() {
      _events?.cancel();
      _events = null;
    });
    if (user == null) return const HomeTasksState();
    final page =
        await ref.watch(learningRepositoryProvider).fetchTasks(limit: 30);
    return HomeTasksState(tasks: page.items);
  }

  void _onEvent(RealtimeEvent event) {
    final current = state.valueOrNull;
    if (current == null) return;
    if (event.type == 'client.resume') {
      unawaited(refresh(silent: true));
      return;
    }
    if (event.type == 'client.poll') {
      if (current.tasks.any((task) =>
          task.state.isActive ||
          task.state == LearningTaskState.waitingVerification)) {
        unawaited(refresh(silent: true));
      }
      return;
    }
    if (event.type.startsWith('task.') ||
        event.type.startsWith('plan.') ||
        event.type.startsWith('course.')) {
      unawaited(refresh(silent: true));
    }
  }

  Future<void> refresh({bool silent = false}) async {
    final current = state.valueOrNull;
    if (current == null) {
      ref.invalidateSelf();
      return;
    }
    if (current.refreshing) return;
    final epoch = _accountEpoch;
    state = AsyncData(current.copyWith(refreshing: true, error: null));
    try {
      final page =
          await ref.read(learningRepositoryProvider).fetchTasks(limit: 30);
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(tasks: page.items, refreshing: false, error: null),
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
        latest.copyWith(refreshing: false, error: '任务摘要暂时无法刷新'),
      );
    }
  }
}

final homeTasksProvider =
    AsyncNotifierProvider<HomeTasksController, HomeTasksState>(
  HomeTasksController.new,
);
