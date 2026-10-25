import 'package:flutter/material.dart';

import 'palette.dart';
import 'theme.dart';

enum StatusTone { neutral, accent, success, warning, danger, info }

/// 桌面使用侧栏，手机使用底部弹层，表单内容共用同一份状态。
Future<T?> showAdaptivePanel<T>({
  required BuildContext context,
  required WidgetBuilder builder,
}) {
  if (context.isNarrow) {
    return showModalBottomSheet<T>(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      showDragHandle: false,
      builder: builder,
    );
  }
  return showDialog<T>(
    context: context,
    builder: (context) => Align(
      alignment: Alignment.centerRight,
      child: SizedBox(
        width: AppMetrics.drawerWidth,
        height: double.infinity,
        child: Material(
          color: context.colors.surface,
          shape: Border(left: BorderSide(color: context.colors.border)),
          child: builder(context),
        ),
      ),
    ),
  );
}

class StatusPill extends StatelessWidget {
  const StatusPill({
    super.key,
    required this.label,
    this.tone = StatusTone.neutral,
    this.icon,
    this.compact = false,
  });

  final String label;
  final StatusTone tone;
  final IconData? icon;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final (foreground, background) = switch (tone) {
      StatusTone.success => (colors.success, colors.successBg),
      StatusTone.warning => (colors.warning, colors.warningBg),
      StatusTone.danger => (colors.danger, colors.dangerBg),
      StatusTone.accent => (colors.accent, colors.accentSoft),
      StatusTone.info => (colors.heroText, colors.heroBg),
      StatusTone.neutral => (colors.textSecondary, colors.surfaceSubtle),
    };
    return Container(
      padding: EdgeInsets.symmetric(
        horizontal: compact ? 8 : 10,
        vertical: compact ? 3 : 5,
      ),
      decoration: BoxDecoration(
        color: background,
        borderRadius: BorderRadius.circular(999),
        border: Border.all(color: foreground.withValues(alpha: 0.14)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (icon != null) ...[
            Icon(icon, size: compact ? 12 : 13, color: foreground),
            const SizedBox(width: 5),
          ],
          Text(
            label,
            style: TextStyle(
              color: foreground,
              fontSize: AppTypography.fontCaption,
              fontWeight: FontWeight.w600,
              height: 1.2,
            ),
          ),
        ],
      ),
    );
  }
}

class AppLogo extends StatelessWidget {
  const AppLogo({super.key, this.compact = false, this.onTap});

  final bool compact;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final content = Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 32,
          height: 32,
          decoration: BoxDecoration(
            color: colors.accent,
            borderRadius: BorderRadius.circular(colors.radius - 2),
          ),
          child: CustomPaint(painter: _LogoMarkPainter(colors.accentContrast)),
        ),
        if (!compact) ...[
          const SizedBox(width: 10),
          Text(
            'ByebyeOCS',
            style: TextStyle(
              color: colors.text,
              fontSize: 16,
              fontWeight: FontWeight.w700,
              letterSpacing: -0.3,
            ),
          ),
        ],
      ],
    );
    if (onTap == null) return content;
    return Semantics(
      button: true,
      label: '返回首页',
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(9),
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 2),
          child: content,
        ),
      ),
    );
  }
}

class _LogoMarkPainter extends CustomPainter {
  const _LogoMarkPainter(this.color);

  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    final paint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 2.2
      ..strokeCap = StrokeCap.round;
    final path = Path()
      ..moveTo(size.width * 0.28, size.height * 0.66)
      ..lineTo(size.width * 0.46, size.height * 0.34)
      ..lineTo(size.width * 0.62, size.height * 0.58)
      ..lineTo(size.width * 0.76, size.height * 0.36);
    canvas.drawPath(path, paint);
    canvas.drawCircle(
      Offset(size.width * 0.76, size.height * 0.36),
      2.1,
      Paint()..color = color,
    );
  }

  @override
  bool shouldRepaint(_LogoMarkPainter oldDelegate) =>
      oldDelegate.color != color;
}

class PageHeader extends StatelessWidget {
  const PageHeader(
      {super.key,
      required this.title,
      this.actions,
      this.padding = const EdgeInsets.only(bottom: 24)});
  final String title;
  final List<Widget>? actions;
  final EdgeInsets padding;
  @override
  Widget build(BuildContext context) => Padding(
        padding: padding,
        child: Wrap(
            alignment: WrapAlignment.spaceBetween,
            crossAxisAlignment: WrapCrossAlignment.center,
            spacing: 16,
            runSpacing: 16,
            children: [
              Semantics(
                  header: true,
                  child: Text(title,
                      style: TextStyle(
                          color: context.colors.text,
                          fontSize: AppTypography.fontTitle,
                          fontWeight: FontWeight.w600,
                          height: 1.2))),
              if (actions != null && actions!.isNotEmpty)
                Wrap(spacing: 8, runSpacing: 8, children: actions!),
            ]),
      );
}

class _SectionFraming extends InheritedWidget {
  const _SectionFraming({required super.child});
  @override
  bool updateShouldNotify(_SectionFraming oldWidget) => false;
}

/// 一个内容表面中的分区共享边界，内部分组使用分隔线。
class SectionGroup extends StatelessWidget {
  const SectionGroup({super.key, required this.child});
  final Widget child;
  @override
  Widget build(BuildContext context) => _SectionFraming(
        child: Material(
          color: context.colors.surface,
          shape: RoundedRectangleBorder(
              borderRadius: context.colors.cardRadius,
              side: BorderSide(color: context.colors.border)),
          child: child,
        ),
      );
}

class SectionCard extends StatelessWidget {
  const SectionCard(
      {super.key,
      this.title,
      this.subtitle,
      this.action,
      required this.child,
      this.padding = const EdgeInsets.all(20),
      this.headerPadding,
      this.color,
      this.framed = true});
  final String? title;
  final String? subtitle;
  final Widget? action;
  final Widget child;
  final EdgeInsets padding;
  final EdgeInsets? headerPadding;
  final Color? color;
  final bool framed;
  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final hasFrame = framed &&
        context.dependOnInheritedWidgetOfExactType<_SectionFraming>() == null;
    return _SectionFraming(
        child: Container(
      decoration: hasFrame
          ? BoxDecoration(
              color: color ?? colors.surface,
              borderRadius: colors.cardRadius,
              border: Border.all(color: colors.border))
          : null,
      child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
        if (title != null)
          SectionCardHeader(
              title: title!,
              subtitle: subtitle,
              action: action,
              padding:
                  headerPadding ?? const EdgeInsets.fromLTRB(20, 18, 20, 0)),
        Padding(
            padding: title == null
                ? padding
                : EdgeInsets.fromLTRB(
                    padding.left, 16, padding.right, padding.bottom),
            child: child),
      ]),
    ));
  }
}

/// 独立的分区标题，供普通卡片与 sliver 卡片复用同一排版。
class SectionCardHeader extends StatelessWidget {
  const SectionCardHeader({
    super.key,
    required this.title,
    this.subtitle,
    this.action,
    this.padding = const EdgeInsets.fromLTRB(20, 18, 18, 0),
  });

  final String title;
  final String? subtitle;
  final Widget? action;
  final EdgeInsets padding;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Padding(
      padding: padding,
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Semantics(
                  header: true,
                  child: Text(
                    title,
                    style: TextStyle(
                      color: colors.text,
                      fontSize: 16,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
                if (subtitle != null) ...[
                  const SizedBox(height: 4),
                  Text(
                    subtitle!,
                    style: TextStyle(
                      color: colors.textSecondary,
                      fontSize: 13,
                      height: 1.5,
                    ),
                  ),
                ],
              ],
            ),
          ),
          if (action != null) ...[
            const SizedBox(width: 12),
            action!,
          ],
        ],
      ),
    );
  }
}

class EmptyState extends StatelessWidget {
  const EmptyState({
    super.key,
    required this.title,
    this.message,
    this.icon = Icons.inbox_outlined,
    this.action,
    this.compact = false,
  });

  final String title;
  final String? message;
  final IconData icon;
  final Widget? action;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Padding(
      padding: EdgeInsets.symmetric(
        vertical: compact ? 22 : 42,
        horizontal: 16,
      ),
      child: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 420),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Container(
                width: 48,
                height: 48,
                decoration: BoxDecoration(
                  color: colors.surfaceSubtle,
                  shape: BoxShape.circle,
                  border: Border.all(color: colors.border),
                ),
                child: Icon(icon, color: colors.muted, size: 23),
              ),
              const SizedBox(height: 14),
              Text(
                title,
                textAlign: TextAlign.center,
                style: TextStyle(
                  color: colors.text,
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
              if (message != null) ...[
                const SizedBox(height: 6),
                Text(
                  message!,
                  textAlign: TextAlign.center,
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 13,
                    height: 1.6,
                  ),
                ),
              ],
              if (action != null) ...[
                const SizedBox(height: 16),
                action!,
              ],
            ],
          ),
        ),
      ),
    );
  }
}

class ErrorPanel extends StatelessWidget {
  const ErrorPanel({
    super.key,
    required this.message,
    this.requestId,
    this.onRetry,
    this.retryLabel = '重新加载',
    this.compact = false,
  });

  final String message;
  final String? requestId;
  final VoidCallback? onRetry;
  final String retryLabel;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      padding: EdgeInsets.all(compact ? 14 : 18),
      decoration: BoxDecoration(
        color: colors.dangerBg,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.danger.withValues(alpha: 0.2)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Icon(
                Icons.error_outline,
                color: colors.danger,
                size: 19,
              ),
              const SizedBox(width: 9),
              Expanded(
                child: Text(
                  message,
                  style: TextStyle(
                    color: colors.text,
                    fontSize: 14,
                    height: 1.55,
                  ),
                ),
              ),
            ],
          ),
          if (requestId != null && requestId!.isNotEmpty) ...[
            const SizedBox(height: 7),
            Text(
              '问题编号 $requestId',
              style: TextStyle(
                color: colors.textSecondary,
                fontSize: 12,
                fontFamily: 'monospace',
              ),
            ),
          ],
          if (onRetry != null) ...[
            const SizedBox(height: 10),
            TextButton(
              onPressed: onRetry,
              child: Text(retryLabel),
            ),
          ],
        ],
      ),
    );
  }
}

class InlineNotice extends StatelessWidget {
  const InlineNotice({
    super.key,
    required this.message,
    this.tone = StatusTone.neutral,
    this.icon,
    this.action,
  });

  final String message;
  final StatusTone tone;
  final IconData? icon;
  final Widget? action;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final (foreground, background) = switch (tone) {
      StatusTone.success => (colors.success, colors.successBg),
      StatusTone.warning => (colors.warning, colors.warningBg),
      StatusTone.danger => (colors.danger, colors.dangerBg),
      StatusTone.accent => (colors.accent, colors.accentSoft),
      StatusTone.info => (colors.heroText, colors.heroBg),
      StatusTone.neutral => (colors.textSecondary, colors.surfaceSubtle),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 13, vertical: 11),
      decoration: BoxDecoration(
        color: background,
        borderRadius: BorderRadius.circular(colors.radius - 2),
        border: Border.all(color: foreground.withValues(alpha: 0.16)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon ?? Icons.info_outline, size: 17, color: foreground),
          const SizedBox(width: 9),
          Expanded(
            child: Text(
              message,
              style: TextStyle(
                color: colors.text,
                fontSize: 13,
                height: 1.55,
              ),
            ),
          ),
          if (action != null) ...[
            const SizedBox(width: 8),
            action!,
          ],
        ],
      ),
    );
  }
}

class LoadingList extends StatelessWidget {
  const LoadingList({super.key, this.rows = 3, this.height = 92});

  final int rows;
  final double height;

  @override
  Widget build(BuildContext context) {
    return Column(
      children: List.generate(rows, (index) {
        return Padding(
          padding: const EdgeInsets.only(bottom: 12),
          child: SkeletonBlock(height: height),
        );
      }),
    );
  }
}

/// 顶栏、底栏和页签共用等宽选项与连续移动的选中背景。
class SlidingSelectionBar extends StatefulWidget {
  const SlidingSelectionBar({
    super.key,
    required this.labels,
    required this.selectedIndex,
    required this.onSelected,
    this.icons,
    this.vertical = false,
  });
  final List<String> labels;
  final List<IconData>? icons;
  final int selectedIndex;
  final ValueChanged<int> onSelected;
  final bool vertical;

  @override
  State<SlidingSelectionBar> createState() => _SlidingSelectionBarState();
}

class _SlidingSelectionBarState extends State<SlidingSelectionBar> {
  bool _animate = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) setState(() => _animate = true);
    });
  }

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final labels = widget.labels;
    final icons = widget.icons;
    final selectedIndex = widget.selectedIndex;
    final vertical = widget.vertical;
    final duration = Duration(
        milliseconds:
            !_animate || MediaQuery.disableAnimationsOf(context) ? 0 : 220);
    return LayoutBuilder(builder: (context, constraints) {
      final itemWidth = constraints.maxWidth / labels.length;
      return SizedBox(
        height: vertical ? 64 : AppMetrics.touchHeight,
        child: Stack(children: [
          AnimatedPositioned(
            duration: duration,
            curve: Curves.easeOutCubic,
            left: itemWidth * selectedIndex.clamp(0, labels.length - 1) + 4,
            width: itemWidth - 8,
            top: 4,
            bottom: 4,
            child: AnimatedOpacity(
              opacity: selectedIndex >= 0 ? 1 : 0,
              duration: duration,
              child: DecoratedBox(
                  decoration: BoxDecoration(
                      color: colors.accentSoft,
                      borderRadius: colors.buttonRadius)),
            ),
          ),
          Row(children: [
            for (var index = 0; index < labels.length; index++)
              Expanded(
                  child: Semantics(
                selected: index == selectedIndex,
                child: TextButton(
                  onPressed: () => widget.onSelected(index),
                  style: TextButton.styleFrom(
                    minimumSize: Size.zero,
                    padding: const EdgeInsets.symmetric(horizontal: 8),
                    foregroundColor: index == selectedIndex
                        ? colors.accent
                        : colors.textSecondary,
                  ),
                  child: SizedBox(
                    height: vertical ? 64 : AppMetrics.touchHeight,
                    child: vertical
                        ? Column(
                            mainAxisAlignment: MainAxisAlignment.center,
                            children: [
                                if (icons != null) Icon(icons[index], size: 20),
                                const SizedBox(height: 4),
                                Text(labels[index],
                                    style: const TextStyle(fontSize: 12)),
                              ])
                        : Center(
                            child: Text(labels[index],
                                style: const TextStyle(fontSize: 14))),
                  ),
                ),
              )),
          ]),
        ]),
      );
    });
  }
}

class SkeletonBlock extends StatefulWidget {
  const SkeletonBlock(
      {super.key, this.width, this.height = 14, this.radius = 4});
  final double? width;
  final double height;
  final double radius;
  @override
  State<SkeletonBlock> createState() => _SkeletonBlockState();
}

class _SkeletonBlockState extends State<SkeletonBlock>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller = AnimationController(
      vsync: this, duration: const Duration(milliseconds: 1200));
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (MediaQuery.disableAnimationsOf(context)) {
      _controller.stop();
    } else {
      _controller.repeat();
    }
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return AnimatedBuilder(
      animation: _controller,
      child: Container(
        width: widget.width,
        height: widget.height,
        decoration: BoxDecoration(
            color: colors.surfaceSubtle,
            borderRadius: BorderRadius.circular(widget.radius)),
      ),
      builder: (context, child) => ShaderMask(
        blendMode: BlendMode.srcATop,
        shaderCallback: (bounds) => LinearGradient(
          begin: Alignment(-3 + _controller.value * 6, 0),
          end: Alignment(-1 + _controller.value * 6, 0),
          colors: [
            colors.surfaceSubtle,
            colors.border.withValues(alpha: .5),
            colors.surfaceSubtle
          ],
          stops: const [0, .5, 1],
        ).createShader(bounds),
        child: child,
      ),
    );
  }
}

class AsyncButton extends StatefulWidget {
  const AsyncButton(
      {super.key,
      required this.label,
      required this.onPressed,
      this.busy = false,
      this.icon,
      this.variant = AsyncButtonVariant.primary,
      this.expand = false});
  final String label;
  final VoidCallback? onPressed;
  final bool busy;
  final IconData? icon;
  final AsyncButtonVariant variant;
  final bool expand;
  @override
  State<AsyncButton> createState() => _AsyncButtonState();
}

class _AsyncButtonState extends State<AsyncButton> {
  late String _idleLabel = widget.label;
  @override
  void didUpdateWidget(AsyncButton oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!widget.busy) _idleLabel = widget.label;
  }

  @override
  Widget build(BuildContext context) {
    final spinner = SizedBox(
        width: 16,
        height: 16,
        child: CircularProgressIndicator(
            strokeWidth: 2, color: context.colors.accent));
    final text = Row(
        mainAxisSize: MainAxisSize.min,
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          if (widget.icon != null) ...[
            widget.busy ? spinner : Icon(widget.icon, size: 16),
            const SizedBox(width: 8)
          ],
          Text(widget.busy ? _idleLabel : widget.label),
        ]);
    final child = widget.icon == null
        ? Stack(alignment: Alignment.center, children: [
            Opacity(opacity: widget.busy ? 0 : 1, child: text),
            if (widget.busy) spinner
          ])
        : text;
    final effective = widget.busy ? null : widget.onPressed;
    final button = switch (widget.variant) {
      AsyncButtonVariant.primary =>
        FilledButton(onPressed: effective, child: child),
      AsyncButtonVariant.outline =>
        OutlinedButton(onPressed: effective, child: child),
      AsyncButtonVariant.text => TextButton(onPressed: effective, child: child),
      AsyncButtonVariant.danger => FilledButton(
          onPressed: effective,
          style: FilledButton.styleFrom(
              backgroundColor: context.colors.danger,
              foregroundColor: context.colors.surface),
          child: child),
    };
    return widget.expand
        ? SizedBox(width: double.infinity, child: button)
        : button;
  }
}

enum AsyncButtonVariant { primary, outline, text, danger }

class MetricTile extends StatelessWidget {
  const MetricTile({
    super.key,
    required this.label,
    required this.value,
    this.icon,
    this.onTap,
    this.tone = StatusTone.neutral,
  });

  final String label;
  final String value;
  final IconData? icon;
  final VoidCallback? onTap;
  final StatusTone tone;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final content = Padding(
      padding: const EdgeInsets.all(15),
      child: Row(
        children: [
          if (icon != null) ...[
            Container(
              width: 34,
              height: 34,
              decoration: BoxDecoration(
                color: tone == StatusTone.accent
                    ? colors.accentSoft
                    : colors.surfaceSubtle,
                borderRadius: BorderRadius.circular(8),
              ),
              child: Icon(
                icon,
                size: 16,
                color: tone == StatusTone.accent
                    ? colors.accent
                    : colors.textSecondary,
              ),
            ),
            const SizedBox(width: 12),
          ],
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  value,
                  style: TextStyle(
                    color: colors.text,
                    fontSize: 16,
                    fontWeight: FontWeight.w600,
                    fontFeatures: const [FontFeature.tabularFigures()],
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  label,
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 13,
                  ),
                ),
              ],
            ),
          ),
          if (onTap != null)
            Icon(Icons.chevron_right, size: 16, color: colors.muted),
        ],
      ),
    );
    final decorated = Container(
      decoration: BoxDecoration(
        color: colors.surface,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      child: content,
    );
    if (onTap == null) return decorated;
    return Semantics(
      button: true,
      label: '$label $value',
      child: InkWell(
        onTap: onTap,
        borderRadius: colors.cardRadius,
        child: decorated,
      ),
    );
  }
}

class ProgressBar extends StatelessWidget {
  const ProgressBar({
    super.key,
    required this.value,
    this.height = 6,
    this.color,
  });

  final double value;
  final double height;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return ClipRRect(
      borderRadius: BorderRadius.circular(999),
      child: LinearProgressIndicator(
        value: value.clamp(0, 1),
        minHeight: height,
        backgroundColor: colors.border,
        valueColor: AlwaysStoppedAnimation(color ?? colors.accent),
      ),
    );
  }
}

class CourseArt extends StatelessWidget {
  const CourseArt(
      {super.key,
      required this.seed,
      this.height = 118,
      this.background = true});

  final String seed;
  final double height;
  final bool background;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return SizedBox(
      height: height,
      width: double.infinity,
      child: CustomPaint(
        painter: _CourseArtPainter(
          seed: seed,
          colors: colors,
          background: background,
        ),
        child: const SizedBox.expand(),
      ),
    );
  }
}

class _CourseArtPainter extends CustomPainter {
  _CourseArtPainter(
      {required this.seed, required this.colors, this.background = true});

  final String seed;
  final AppColors colors;
  final bool background;

  @override
  void paint(Canvas canvas, Size size) {
    final hash = seed.hashCode.abs();
    final shift = ((hash % 5) - 2) * 0.07;
    if (background) {
      final fill = Paint()
        ..shader = LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [
            Color.alphaBlend(
              colors.accent.withValues(alpha: 0.12),
              colors.surfaceSubtle,
            ),
            colors.surface,
          ],
        ).createShader(Offset.zero & size);
      canvas.drawRect(Offset.zero & size, fill);
    }

    final accent = colors.accent;
    canvas.drawCircle(
      Offset(size.width * 0.84, size.height * 0.16),
      size.height * 0.62,
      Paint()..color = accent.withValues(alpha: 0.10),
    );
    final ring = Paint()
      ..color = accent.withValues(alpha: 0.26)
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.1;
    canvas.drawCircle(
      Offset(size.width * 0.76, size.height * 0.28),
      size.height * 0.36,
      ring,
    );
    canvas.drawCircle(
      Offset(size.width * 0.76, size.height * 0.28),
      size.height * 0.20,
      Paint()
        ..color = accent.withValues(alpha: 0.14)
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1.1,
    );

    final frameCenter = Offset(size.width * 0.18, size.height * 0.82);
    final frame = RRect.fromRectAndRadius(
      Rect.fromCenter(
        center: frameCenter,
        width: size.height * 0.72,
        height: size.height * 0.72,
      ),
      Radius.circular(size.height * 0.18),
    );
    canvas.save();
    canvas.translate(frameCenter.dx, frameCenter.dy);
    canvas.rotate(-0.22 + shift);
    canvas.translate(-frameCenter.dx, -frameCenter.dy);
    canvas.drawRRect(
      frame,
      Paint()..color = colors.surface.withValues(alpha: 0.55),
    );
    canvas.drawRRect(
      frame,
      Paint()
        ..color = accent.withValues(alpha: 0.22)
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1,
    );
    canvas.restore();

    final line = Paint()
      ..color = accent.withValues(alpha: 0.30)
      ..strokeWidth = 1.2
      ..strokeCap = StrokeCap.round;
    final start = Offset(size.width * 0.08, size.height * 0.20);
    canvas.drawLine(start, start + Offset(size.height * 0.18, 0), line);
    canvas.drawLine(
      start + const Offset(0, 6),
      start + Offset(size.height * 0.11, 6),
      line,
    );

    final dot = Paint()..color = accent.withValues(alpha: 0.24);
    for (var row = 0; row < 2; row++) {
      for (var column = 0; column < 4; column++) {
        canvas.drawCircle(
          Offset(
            size.width * 0.66 + column * 10,
            size.height * 0.72 + row * 10,
          ),
          1.4,
          dot,
        );
      }
    }
  }

  @override
  bool shouldRepaint(_CourseArtPainter oldDelegate) =>
      oldDelegate.seed != seed ||
      oldDelegate.colors != colors ||
      oldDelegate.background != background;
}

Future<bool> showConfirmDialog(
  BuildContext context, {
  required String title,
  required String message,
  String confirmLabel = '确认',
  String cancelLabel = '取消',
  bool danger = false,
}) async {
  final result = await showDialog<bool>(
    context: context,
    builder: (dialogContext) {
      final colors = dialogContext.colors;
      return AlertDialog(
        title: Text(title),
        content: Text(message),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: Text(cancelLabel),
          ),
          FilledButton(
            style: danger
                ? FilledButton.styleFrom(
                    backgroundColor: colors.danger,
                    foregroundColor: colors.surface,
                  )
                : null,
            onPressed: () => Navigator.of(dialogContext).pop(true),
            child: Text(confirmLabel),
          ),
        ],
      );
    },
  );
  return result ?? false;
}

void showAppMessage(BuildContext context, String message) {
  final messenger = ScaffoldMessenger.maybeOf(context);
  messenger?.showSnackBar(SnackBar(content: Text(message)));
}

class SheetHeader extends StatelessWidget {
  const SheetHeader({
    super.key,
    required this.title,
    this.subtitle,
    this.trailing,
  });

  final String title;
  final String? subtitle;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                title,
                style: TextStyle(
                  color: colors.text,
                  fontSize: AppTypography.fontSection,
                  fontWeight: FontWeight.w600,
                  letterSpacing: -0.3,
                ),
              ),
              if (subtitle != null) ...[
                const SizedBox(height: 4),
                Text(
                  subtitle!,
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 13,
                    height: 1.5,
                  ),
                ),
              ],
            ],
          ),
        ),
        if (trailing != null) trailing!,
      ],
    );
  }
}

class ResponsiveContent extends StatelessWidget {
  const ResponsiveContent({
    super.key,
    required this.child,
    this.maxWidth = AppMetrics.contentWidth,
    this.padding,
  });

  final Widget child;
  final double maxWidth;
  final EdgeInsets? padding;

  @override
  Widget build(BuildContext context) {
    return Align(
      alignment: Alignment.topCenter,
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: maxWidth),
        child: Padding(
          padding: padding ??
              EdgeInsets.symmetric(
                horizontal: context.isNarrow ? 16 : AppMetrics.pagePadding,
                vertical: context.isNarrow ? 16 : 24,
              ),
          child: child,
        ),
      ),
    );
  }
}
