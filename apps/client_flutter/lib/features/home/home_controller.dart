import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/models/task.dart';
import '../../core/realtime/realtime_event.dart';
import '../../core/repositories/learning_repository.dart';

class HomeTasksState {
  const HomeTasksState({
    this.tasks = const <LearningTask>[],
    this.attentionCount = 0,
    this.refreshing = false,
    this.error,
  });

  final List<LearningTask> tasks;
  final int attentionCount;
  final bool refreshing;
  final String? error;

  HomeTasksState copyWith({
    List<LearningTask>? tasks,
    int? attentionCount,
    bool? refreshing,
    String? error,
  }) =>
      HomeTasksState(
        tasks: tasks ?? this.tasks,
        attentionCount: attentionCount ?? this.attentionCount,
        refreshing: refreshing ?? this.refreshing,
        error: error,
      );
}

class HomeTasksController extends AsyncNotifier<HomeTasksState> {
  StreamSubscription<RealtimeEvent>? _events;
  Timer? _refreshTimer;
  bool _refreshQueued = false;

  /// 账户代次：重建后旧账户的在途刷新不得写入新用户状态。
  int _accountEpoch = 0;

  @override
  Future<HomeTasksState> build() async {
    ++_accountEpoch;
    ref.watch(sessionEpochProvider);
    final user = ref.watch(currentUserProvider);
    final repository = ref.watch(learningRepositoryProvider);
    _events = ref.watch(realtimeServiceProvider).events.listen(_onEvent);
    ref.onDispose(() {
      ++_accountEpoch;
      _refreshTimer?.cancel();
      _events?.cancel();
      _events = null;
      _refreshQueued = false;
    });
    if (user == null) return const HomeTasksState();
    return _fetch(repository);
  }

  Future<HomeTasksState> _fetch(LearningRepository repository) async {
    final pages = await Future.wait([
      repository.fetchTasks(actionable: true, limit: 1),
      repository.fetchTasks(states: const ['confirmed'], limit: 10),
    ]);
    final attention = pages.first.items.where((task) =>
        (task.state == LearningTaskState.needsAttention ||
            task.state == LearningTaskState.failed) &&
        !task.isClosed);
    final byId = <String, LearningTask>{
      for (final task in [
        ...attention,
        ...pages.last.items
            .where((task) => task.state == LearningTaskState.confirmed),
      ])
        task.id: task,
    };
    return HomeTasksState(
      tasks: byId.values.toList(growable: false),
      attentionCount: pages.first.totalCount ?? attention.length,
    );
  }

  void _onEvent(RealtimeEvent event) {
    if (event.type == 'task.observation') return;
    if (event.type == 'client.resumed' ||
        event.type == 'client.poll' ||
        event.type.startsWith('task.') ||
        event.type.startsWith('plan.') ||
        event.type.startsWith('course.')) {
      _refreshTimer ??= Timer(const Duration(milliseconds: 400), () {
        _refreshTimer = null;
        unawaited(refresh(silent: true));
      });
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
    final epoch = _accountEpoch;
    state = AsyncData(current.copyWith(refreshing: true, error: null));
    try {
      final loaded = await _fetch(ref.read(learningRepositoryProvider));
      if (epoch != _accountEpoch) return;
      state = AsyncData(loaded);
    } on ApiException catch (error) {
      if (epoch != _accountEpoch) return;
      state = AsyncData(
          current.copyWith(refreshing: false, error: error.userMessage));
    } catch (_) {
      if (epoch != _accountEpoch) return;
      state =
          AsyncData(current.copyWith(refreshing: false, error: '任务摘要暂时无法刷新'));
    } finally {
      if (epoch == _accountEpoch && _refreshQueued) {
        _refreshQueued = false;
        unawaited(refresh(silent: true));
      }
    }
  }
}

final homeTasksProvider =
    AsyncNotifierProvider<HomeTasksController, HomeTasksState>(
  HomeTasksController.new,
);
