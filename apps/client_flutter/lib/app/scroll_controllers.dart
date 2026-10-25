import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// 标签页共用同一控制器，切换页面后仍能回到离开时的位置。
final tabScrollControllerProvider =
    Provider.family<ScrollController, String>((ref, key) {
  final controller = ScrollController();
  ref.onDispose(controller.dispose);
  return controller;
});
