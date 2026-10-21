import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../app/providers.dart';
import '../../design/components.dart';
import '../../design/theme.dart';

class AppShell extends ConsumerWidget {
  const AppShell({super.key, required this.child});

  final Widget child;

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
  Widget build(BuildContext context, WidgetRef ref) {
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
                child: KeyedSubtree(
                  key: ValueKey(location),
                  child: child,
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
      height: narrow ? 58 : 68,
      decoration: BoxDecoration(
        color: colors.background.withValues(alpha: 0.97),
        border: Border(bottom: BorderSide(color: colors.border)),
      ),
      padding: EdgeInsets.symmetric(horizontal: narrow ? 16 : 28),
      child: Row(
        children: [
          AppLogo(
            compact: narrow,
            onTap: () => onNavigate('/home'),
          ),
          if (!narrow) ...[
            const SizedBox(width: 42),
            Expanded(
              child: Row(
                children: [
                  for (var index = 0; index < destinations.length; index++)
                    _DesktopNavItem(
                      destination: destinations[index],
                      selected: selectedIndex == index,
                      onTap: () => onNavigate(destinations[index].path),
                    ),
                ],
              ),
            ),
          ] else
            const Spacer(),
          if (!narrow) ...[
            const SizedBox(width: 12),
            _AvatarButton(initial: initial, onTap: onSettings),
          ] else
            _AvatarButton(initial: initial, onTap: onSettings),
        ],
      ),
    );
  }
}

class _DesktopNavItem extends StatelessWidget {
  const _DesktopNavItem({
    required this.destination,
    required this.selected,
    required this.onTap,
  });

  final _ShellDestination destination;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Padding(
      padding: const EdgeInsets.only(right: 4),
      child: TextButton(
        onPressed: onTap,
        style: TextButton.styleFrom(
          foregroundColor: selected ? colors.accent : colors.textSecondary,
          backgroundColor: selected ? colors.accentSoft : Colors.transparent,
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
          minimumSize: const Size(0, 44),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(colors.radius - 3),
          ),
        ),
        child: Text(
          destination.label,
          style: TextStyle(
            fontSize: 13.5,
            fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
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
  const _BottomNavigation({
    required this.destinations,
    required this.selectedIndex,
    required this.onNavigate,
  });

  final List<_ShellDestination> destinations;
  final int selectedIndex;
  final ValueChanged<String> onNavigate;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      decoration: BoxDecoration(
        color: colors.surface,
        border: Border(top: BorderSide(color: colors.border)),
      ),
      child: SafeArea(
        top: false,
        child: SizedBox(
          height: 64,
          child: Row(
            children: [
              for (var index = 0; index < destinations.length; index++)
                Expanded(
                  child: _BottomNavItem(
                    destination: destinations[index],
                    selected: selectedIndex == index,
                    onTap: () => onNavigate(destinations[index].path),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class _BottomNavItem extends StatelessWidget {
  const _BottomNavItem({
    required this.destination,
    required this.selected,
    required this.onTap,
  });

  final _ShellDestination destination;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Semantics(
      button: true,
      selected: selected,
      label: destination.label,
      child: InkWell(
        onTap: onTap,
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(
              selected ? destination.activeIcon : destination.icon,
              size: 22,
              color: selected ? colors.accent : colors.textSecondary,
            ),
            const SizedBox(height: 3),
            Text(
              destination.shortLabel,
              style: TextStyle(
                color: selected ? colors.accent : colors.textSecondary,
                fontSize: 11.5,
                fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
              ),
            ),
          ],
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
