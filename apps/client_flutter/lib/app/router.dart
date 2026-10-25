import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../design/components.dart';
import '../design/theme.dart';
import '../features/auth/auth_controller.dart';
import '../features/auth/login_page.dart';
import '../features/courses/course_detail_page.dart';
import '../features/courses/courses_page.dart';
import '../features/home/home_page.dart';
import '../features/plans/plans_page.dart';
import '../features/questions/question_query_page.dart';
import '../features/settings/connect_service_page.dart';
import '../features/settings/deletion_receipt_page.dart';
import '../features/settings/settings_page.dart';
import '../features/shell/app_shell.dart';
import '../features/wallet/wallet_page.dart';

Page<void> _fadePage(GoRouterState state, Widget child) {
  return CustomTransitionPage<void>(
    key: state.pageKey,
    child: child,
    transitionDuration: const Duration(milliseconds: 220),
    reverseTransitionDuration: const Duration(milliseconds: 160),
    transitionsBuilder: (context, animation, secondary, child) {
      if (MediaQuery.disableAnimationsOf(context)) return child;
      final curved = CurvedAnimation(
        parent: animation,
        curve: Curves.easeOutCubic,
        reverseCurve: Curves.easeInCubic,
      );
      return ColoredBox(
        color: context.colors.background,
        child: SlideTransition(
          position: Tween<Offset>(
            begin: const Offset(0.03, 0),
            end: Offset.zero,
          ).animate(curved),
          child: child,
        ),
      );
    },
  );
}

final routerProvider = Provider<GoRouter>((ref) {
  final refresh = ValueNotifier<int>(0);
  ref.listen(authControllerProvider, (previous, next) {
    if (previous?.status != next.status ||
        previous?.user?.id != next.user?.id) {
      refresh.value++;
    }
  });
  ref.onDispose(refresh.dispose);

  final router = GoRouter(
    refreshListenable: refresh,
    redirect: (context, state) {
      final auth = ref.read(authControllerProvider);
      final location = state.matchedLocation;
      final onRoot = location == '/' || location.isEmpty;
      final onLogin = location == '/login';
      final onStartup = location == '/startup';
      final onConnect = location == '/connect';
      final onDeletionReceipt = location == '/deletion-receipt';
      if (onConnect || onDeletionReceipt) return null;
      final requested = _safeReturnLocation(state.uri);
      String startupWithReturn() {
        if (requested == null) return '/startup';
        return '/startup?from=${Uri.encodeComponent(requested)}';
      }

      switch (auth.status) {
        case AuthStatus.loading:
          return onStartup ? null : startupWithReturn();
        case AuthStatus.anonymous:
        case AuthStatus.unavailable:
          return onLogin ? null : '/login';
        case AuthStatus.authenticated:
          if (onRoot) return '/home';
          if (onStartup) {
            return _safeFromQuery(state.uri.queryParameters['from']) ?? '/home';
          }
          return onLogin ? '/home' : null;
      }
    },
    routes: [
      GoRoute(
        path: '/startup',
        builder: (context, state) => const StartupPage(),
      ),
      GoRoute(
        path: '/login',
        builder: (context, state) => const LoginPage(),
      ),
      GoRoute(
        path: '/connect',
        builder: (context, state) => const ConnectServicePage(),
      ),
      GoRoute(
        path: '/deletion-receipt',
        builder: (context, state) => const DeletionReceiptPage(),
      ),
      StatefulShellRoute.indexedStack(
        builder: (context, state, navigationShell) =>
            AppShell(navigationShell: navigationShell),
        branches: [
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/home',
                builder: (context, state) => const HomePage(),
              ),
            ],
          ),
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/courses',
                builder: (context, state) => const CoursesPage(),
                routes: [
                  GoRoute(
                    path: ':id',
                    pageBuilder: (context, state) => _fadePage(
                      state,
                      CourseDetailPage(
                        courseId: state.pathParameters['id'] ?? '',
                      ),
                    ),
                  ),
                ],
              ),
            ],
          ),
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/plans',
                builder: (context, state) => PlansPage(
                  initialCourseId: state.uri.queryParameters['course'],
                  initialPlanId: state.uri.queryParameters['plan'],
                ),
              ),
            ],
          ),
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/wallet',
                builder: (context, state) => WalletPage(
                  orderId: state.uri.queryParameters['order_id'] ??
                      state.uri.queryParameters['order'],
                ),
              ),
            ],
          ),
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/settings',
                builder: (context, state) => const SettingsPage(),
              ),
            ],
          ),
          StatefulShellBranch(
            routes: [
              GoRoute(
                path: '/questions',
                builder: (context, state) => const QuestionQueryPage(),
              ),
            ],
          ),
        ],
      ),
    ],
    errorBuilder: (context, state) =>
        _RouteErrorPage(message: state.error?.toString()),
  );
  ref.onDispose(router.dispose);
  return router;
});

class StartupPage extends ConsumerWidget {
  const StartupPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final colors = context.colors;
    return Scaffold(
      body: Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const AppLogo(),
            const SizedBox(height: 28),
            SizedBox(
              width: 22,
              height: 22,
              child: CircularProgressIndicator(
                strokeWidth: 2.2,
                color: colors.accent,
              ),
            ),
            const SizedBox(height: 14),
            Text(
              '正在确认登录状态',
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 13,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _RouteErrorPage extends StatelessWidget {
  const _RouteErrorPage({this.message});

  final String? message;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 420),
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: EmptyState(
                icon: Icons.explore_off_outlined,
                title: '这个页面不存在',
                message: '链接可能已经失效，返回首页继续查看学习安排。',
                action: FilledButton(
                  onPressed: () => context.go('/home'),
                  child: const Text('返回首页'),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// 登录恢复后只允许回到站内普通页面，避免把外部地址当作跳转目标。
String? _safeFromQuery(String? raw) {
  if (raw == null || raw.isEmpty) return null;
  final uri = Uri.tryParse(raw);
  if (uri == null || uri.hasScheme || uri.hasAuthority) return null;
  final path = uri.path;
  if (!path.startsWith('/') || path.startsWith('//')) return null;
  if (path == '/login' || path == '/startup' || path == '/connect') {
    return null;
  }
  return uri.toString();
}

String? _safeReturnLocation(Uri uri) {
  if (uri.path == '/' || uri.path.isEmpty) return null;
  return _safeFromQuery(uri.toString());
}
