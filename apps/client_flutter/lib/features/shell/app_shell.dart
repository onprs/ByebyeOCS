import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/providers.dart';
import '../../design/components.dart';
import '../../design/theme.dart';

class AppShell extends ConsumerStatefulWidget {
  const AppShell({super.key, required this.child});
  final Widget child;
  @override
  ConsumerState<AppShell> createState() => _AppShellState();
}

class _AppShellState extends ConsumerState<AppShell> {
  final _pageStorage = PageStorageBucket();

  static const _destinations = <_ShellDestination>[
    _ShellDestination(
      path: '/home',
      label: '首页',
      shortLabel: '首页',
      icon: Icons.home_outlined,
      activeIcon: Icons.home_rounded,
    ),
    _ShellDestination(
      path: '/courses',
      label: '我的课程',
      shortLabel: '课程',
      icon: Icons.menu_book_outlined,
      activeIcon: Icons.menu_book_rounded,
    ),
    _ShellDestination(
      path: '/plans',
      label: '任务安排',
      shortLabel: '安排',
      icon: Icons.calendar_month_outlined,
      activeIcon: Icons.calendar_month_rounded,
    ),
    _ShellDestination(
      path: '/wallet',
      label: '我的钱包',
      shortLabel: '钱包',
      icon: Icons.account_balance_wallet_outlined,
      activeIcon: Icons.account_balance_wallet_rounded,
    ),
  ];

  @override
  Widget build(BuildContext context) {
    final location = GoRouterState.of(context).matchedLocation;
    final colors = context.colors;
    final narrow = MediaQuery.sizeOf(context).width < 760;
    final selected = _selectedIndex(location);
    final user = ref.watch(currentUserProvider);
    final initial = user?.displayLabel.characters.first ?? '我';
    return Scaffold(
      backgroundColor: colors.background,
      body: SafeArea(
        bottom: false,
        child: Column(
          children: [
            _TopBar(
              narrow: narrow,
              selectedIndex: selected,
              destinations: _destinations,
              initial: initial,
              onNavigate: (path) => context.go(path),
              onSettings: () => context.go('/settings'),
            ),
            Expanded(
              child: ClipRect(
                child: PageStorage(
                  bucket: _pageStorage,
                  child: AnimatedSwitcher(
                    duration: Duration(
                        milliseconds:
                            MediaQuery.disableAnimationsOf(context) ? 0 : 160),
                    transitionBuilder: (child, animation) => FadeTransition(
                      opacity: animation,
                      child: AnimatedBuilder(
                        animation: animation,
                        builder: (context, child) => Transform.translate(
                            offset: Offset(0, 4 * (1 - animation.value)),
                            child: child),
                        child: child,
                      ),
                    ),
                    child: KeyedSubtree(
                        key: ValueKey(location), child: widget.child),
                  ),
                ),
              ),
            ),
            if (narrow)
              _BottomNavigation(
                destinations: _destinations,
                selectedIndex: selected,
                onNavigate: (path) => context.go(path),
              ),
          ],
        ),
      ),
    );
  }

  int _selectedIndex(String location) {
    for (var index = 0; index < _destinations.length; index++) {
      final path = _destinations[index].path;
      if (location == path || location.startsWith('$path/')) return index;
    }
    return -1;
  }
}

class _TopBar extends StatelessWidget {
  const _TopBar({
    required this.narrow,
    required this.selectedIndex,
    required this.destinations,
    required this.initial,
    required this.onNavigate,
    required this.onSettings,
  });

  final bool narrow;
  final int selectedIndex;
  final List<_ShellDestination> destinations;
  final String initial;
  final ValueChanged<String> onNavigate;
  final VoidCallback onSettings;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      height: narrow ? 56 : AppMetrics.topbarHeight,
      decoration: BoxDecoration(
        color: colors.surface,
        border: Border(bottom: BorderSide(color: colors.border)),
      ),
      child: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: AppMetrics.contentWidth),
          child: Padding(
            padding: EdgeInsets.symmetric(
                horizontal: narrow ? 16 : AppMetrics.pagePadding),
            child: Row(
              children: [
                AppLogo(compact: narrow, onTap: () => onNavigate('/home')),
                if (!narrow) ...[
                  const SizedBox(width: 32),
                  Expanded(
                    child: Align(
                      alignment: Alignment.centerLeft,
                      child: ConstrainedBox(
                        constraints: const BoxConstraints(maxWidth: 520),
                        child: SlidingSelectionBar(
                          labels:
                              destinations.map((item) => item.label).toList(),
                          selectedIndex: selectedIndex,
                          onSelected: (index) =>
                              onNavigate(destinations[index].path),
                        ),
                      ),
                    ),
                  ),
                ] else
                  const Spacer(),
                _AvatarButton(initial: initial, onTap: onSettings),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _AvatarButton extends StatelessWidget {
  const _AvatarButton({required this.initial, required this.onTap});

  final String initial;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Semantics(
      button: true,
      label: '个人设置',
      child: Tooltip(
        message: '个人设置',
        child: InkWell(
          onTap: onTap,
          customBorder: const CircleBorder(),
          child: Container(
            width: 44,
            height: 44,
            alignment: Alignment.center,
            child: Container(
              width: 34,
              height: 34,
              alignment: Alignment.center,
              decoration: BoxDecoration(
                color: colors.accentSoft,
                shape: BoxShape.circle,
                border: Border.all(color: colors.accent.withValues(alpha: 0.2)),
              ),
              child: Text(
                initial,
                style: TextStyle(
                  color: colors.accent,
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _BottomNavigation extends StatelessWidget {
  const _BottomNavigation(
      {required this.destinations,
      required this.selectedIndex,
      required this.onNavigate});
  final List<_ShellDestination> destinations;
  final int selectedIndex;
  final ValueChanged<String> onNavigate;
  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return DecoratedBox(
      decoration: BoxDecoration(
          color: colors.surface,
          border: Border(top: BorderSide(color: colors.border))),
      child: SafeArea(
        top: false,
        child: SlidingSelectionBar(
          labels: destinations.map((item) => item.shortLabel).toList(),
          icons: [
            for (var index = 0; index < destinations.length; index++)
              selectedIndex == index
                  ? destinations[index].activeIcon
                  : destinations[index].icon
          ],
          vertical: true,
          selectedIndex: selectedIndex,
          onSelected: (index) => onNavigate(destinations[index].path),
        ),
      ),
    );
  }
}

class _ShellDestination {
  const _ShellDestination({
    required this.path,
    required this.label,
    required this.shortLabel,
    required this.icon,
    required this.activeIcon,
  });

  final String path;
  final String label;
  final String shortLabel;
  final IconData icon;
  final IconData activeIcon;
}
