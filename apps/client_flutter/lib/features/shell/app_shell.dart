import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/providers.dart';
import '../../app/tab_navigation.dart';
import '../../design/components.dart';
import '../../design/theme.dart';

class AppShell extends ConsumerStatefulWidget {
  const AppShell({super.key, required this.navigationShell});
  final StatefulNavigationShell navigationShell;
  @override
  ConsumerState<AppShell> createState() => _AppShellState();
}

class _AppShellState extends ConsumerState<AppShell>
    with SingleTickerProviderStateMixin {
  final _pageStorage = PageStorageBucket();
  late final AnimationController _fade = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 160),
    reverseDuration: const Duration(milliseconds: 90),
    value: 1,
  );
  bool _switching = false;

  @override
  void dispose() {
    _fade.dispose();
    super.dispose();
  }

  Future<void> _navigate(int index) async {
    final current = widget.navigationShell.currentIndex;
    if (index == current) {
      widget.navigationShell.goBranch(index, initialLocation: true);
      return;
    }
    if (_switching) return;
    if (MediaQuery.disableAnimationsOf(context)) {
      widget.navigationShell.goBranch(index);
      return;
    }
    _switching = true;
    await _fade.animateTo(0, curve: Curves.easeIn);
    if (!mounted) return;
    widget.navigationShell.goBranch(index);
    await _fade.animateTo(1, curve: Curves.easeOut);
    _switching = false;
  }

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
    final colors = context.colors;
    final narrow = MediaQuery.sizeOf(context).width < 760;
    final selected = widget.navigationShell.currentIndex < _destinations.length
        ? widget.navigationShell.currentIndex
        : -1;
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
              onNavigate: _navigate,
              onSettings: () => _navigate(4),
            ),
            Expanded(
              child: ClipRect(
                child: PageStorage(
                  bucket: _pageStorage,
                  child: FadeTransition(
                    opacity: _fade,
                    child: TabNavigation(
                      go: _navigate,
                      child: widget.navigationShell,
                    ),
                  ),
                ),
              ),
            ),
            SiteFooter(compact: narrow),
            if (narrow)
              _BottomNavigation(
                destinations: _destinations,
                selectedIndex: selected,
                onNavigate: _navigate,
              ),
          ],
        ),
      ),
    );
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
  final ValueChanged<int> onNavigate;
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
                AppLogo(compact: narrow, onTap: () => onNavigate(0)),
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
                          onSelected: onNavigate,
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
  final ValueChanged<int> onNavigate;
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
          onSelected: onNavigate,
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
