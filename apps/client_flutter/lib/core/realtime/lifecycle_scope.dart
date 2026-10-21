import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../features/auth/auth_controller.dart';

class LifecycleScope extends ConsumerStatefulWidget {
  const LifecycleScope({super.key, required this.child});

  final Widget child;

  @override
  ConsumerState<LifecycleScope> createState() => _LifecycleScopeState();
}

class _LifecycleScopeState extends ConsumerState<LifecycleScope>
    with WidgetsBindingObserver {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    WidgetsBinding.instance.addPostFrameCallback((_) => _syncRealtime());
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    final realtime = ref.read(realtimeServiceProvider);
    switch (state) {
      case AppLifecycleState.resumed:
        final auth = ref.read(authControllerProvider);
        if (auth.isAuthenticated) {
          unawaited(
            ref.read(authControllerProvider.notifier).refreshAccount(),
          );
          realtime.resume();
        }
      case AppLifecycleState.inactive:
      case AppLifecycleState.hidden:
      case AppLifecycleState.paused:
        realtime.pause();
      case AppLifecycleState.detached:
        realtime.stop();
    }
  }

  void _syncRealtime() {
    if (!mounted) return;
    final auth = ref.read(authControllerProvider);
    final realtime = ref.read(realtimeServiceProvider);
    if (auth.isAuthenticated) {
      realtime.start();
    } else {
      realtime.stop();
    }
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(authControllerProvider, (previous, next) {
      if (previous?.status != next.status ||
          previous?.user?.id != next.user?.id) {
        _syncRealtime();
      }
    });
    return widget.child;
  }
}
