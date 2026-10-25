import 'package:flutter/widgets.dart';
import 'package:go_router/go_router.dart';

/// 让页面内的“去课程/去安排”等入口也走同一套切换动画。
class TabNavigation extends InheritedWidget {
  const TabNavigation({super.key, required this.go, required super.child});

  final ValueChanged<int> go;

  static TabNavigation? maybeOf(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<TabNavigation>();

  @override
  bool updateShouldNotify(TabNavigation oldWidget) => oldWidget.go != go;
}

void openTab(BuildContext context, int index, String fallbackPath) {
  final navigation = TabNavigation.maybeOf(context);
  if (navigation != null) {
    navigation.go(index);
    return;
  }
  GoRouter.of(context).go(fallbackPath);
}
