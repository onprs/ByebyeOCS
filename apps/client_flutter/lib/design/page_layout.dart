import 'package:flutter/material.dart';

import 'components.dart';

/// 主页面统一的滚动区域：短内容将页脚推至底部，长内容在末尾显示页脚。
/// 钱包等长列表直接使用 slivers，保持按视口懒构建。
class PageScrollView extends StatelessWidget {
  const PageScrollView({
    super.key,
    required Widget child,
    this.controller,
    this.physics,
    this.primary = false,
  })  : _child = child,
        _slivers = null;

  const PageScrollView.slivers({
    super.key,
    required List<Widget> slivers,
    this.controller,
    this.physics,
    this.primary = false,
  })  : _child = null,
        _slivers = slivers;

  final Widget? _child;
  final List<Widget>? _slivers;
  final ScrollController? controller;
  final ScrollPhysics? physics;
  final bool primary;

  @override
  Widget build(BuildContext context) {
    return CustomScrollView(
      controller: controller,
      physics: physics,
      primary: primary,
      slivers: [
        if (_child != null) SliverToBoxAdapter(child: _child) else ..._slivers!,
        const SliverFillRemaining(
          hasScrollBody: false,
          child: Align(
            alignment: Alignment.bottomCenter,
            child: SiteFooter(),
          ),
        ),
      ],
    );
  }
}
