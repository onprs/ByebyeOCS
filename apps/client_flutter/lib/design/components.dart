import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'palette.dart';
import 'theme.dart';

enum StatusTone { neutral, accent, success, warning, danger, info }

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
              fontSize: compact ? 11.5 : 12,
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
              fontSize: 16.5,
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
  const PageHeader({
    super.key,
    required this.title,
    this.description,
    this.eyebrow,
    this.actions,
    this.padding = const EdgeInsets.only(bottom: 22, top: 6),
  });

  final String title;
  final String? description;
  final String? eyebrow;
  final List<Widget>? actions;
  final EdgeInsets padding;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    final narrow = context.isNarrow;
    return Padding(
      padding: padding,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (eyebrow != null) ...[
            Text(
              eyebrow!,
              style: TextStyle(
                color: colors.muted,
                fontSize: 11,
                letterSpacing: 1.4,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 8),
          ],
          Wrap(
            alignment: WrapAlignment.spaceBetween,
            crossAxisAlignment: WrapCrossAlignment.center,
            spacing: 16,
            runSpacing: 14,
            children: [
              Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Semantics(
                    header: true,
                    child: Text(
                      title,
                      style: TextStyle(
                        color: colors.text,
                        fontSize: narrow ? 24 : 29,
                        fontWeight: FontWeight.w600,
                        letterSpacing: -0.7,
                        height: 1.2,
                      ),
                    ),
                  ),
                  if (description != null) ...[
                    const SizedBox(height: 7),
                    ConstrainedBox(
                      constraints: const BoxConstraints(maxWidth: 620),
                      child: Text(
                        description!,
                        style: TextStyle(
                          color: colors.textSecondary,
                          fontSize: 14,
                          height: 1.6,
                        ),
                      ),
                    ),
                  ],
                ],
              ),
              if (actions != null && actions!.isNotEmpty)
                Wrap(spacing: 10, runSpacing: 10, children: actions!),
            ],
          ),
        ],
      ),
    );
  }
}

class SectionCard extends StatelessWidget {
  const SectionCard({
    super.key,
    this.title,
    this.subtitle,
    this.action,
    required this.child,
    this.padding = const EdgeInsets.all(20),
    this.headerPadding,
    this.color,
  });

  final String? title;
  final String? subtitle;
  final Widget? action;
  final Widget child;
  final EdgeInsets padding;
  final EdgeInsets? headerPadding;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      decoration: BoxDecoration(
        color: color ?? colors.surface,
        borderRadius: colors.cardRadius,
        border: Border.all(color: colors.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (title != null)
            SectionCardHeader(
              title: title!,
              subtitle: subtitle,
              action: action,
              padding:
                  headerPadding ?? const EdgeInsets.fromLTRB(20, 18, 18, 0),
            ),
          Padding(
            padding: title == null
                ? padding
                : EdgeInsets.fromLTRB(
                    padding.left,
                    16,
                    padding.right,
                    padding.bottom,
                  ),
            child: child,
          ),
        ],
      ),
    );
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
                      fontSize: 12.5,
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
                  fontSize: 15,
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
                    fontSize: 13.5,
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
                fontSize: 11.5,
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

class SkeletonBlock extends StatelessWidget {
  const SkeletonBlock({
    super.key,
    this.height = 18,
    this.width,
    this.radius = 7,
  });

  final double height;
  final double? width;
  final double radius;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return Container(
      height: height,
      width: width,
      decoration: BoxDecoration(
        color: colors.surfaceSubtle,
        borderRadius: BorderRadius.circular(radius),
        border: Border.all(color: colors.border.withValues(alpha: 0.55)),
      ),
    );
  }
}

class AsyncButton extends StatelessWidget {
  const AsyncButton({
    super.key,
    required this.label,
    required this.onPressed,
    this.busy = false,
    this.icon,
    this.variant = AsyncButtonVariant.primary,
    this.expand = false,
  });

  final String label;
  final VoidCallback? onPressed;
  final bool busy;
  final IconData? icon;
  final AsyncButtonVariant variant;
  final bool expand;

  @override
  Widget build(BuildContext context) {
    final child = Row(
      mainAxisSize: MainAxisSize.min,
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        if (busy)
          const SizedBox(
            width: 16,
            height: 16,
            child: CircularProgressIndicator(strokeWidth: 2),
          )
        else if (icon != null)
          Icon(icon, size: 17),
        if (busy || icon != null) const SizedBox(width: 8),
        Text(label),
      ],
    );
    final effective = busy ? null : onPressed;
    final button = switch (variant) {
      AsyncButtonVariant.primary => FilledButton(
          onPressed: effective,
          child: child,
        ),
      AsyncButtonVariant.outline => OutlinedButton(
          onPressed: effective,
          child: child,
        ),
      AsyncButtonVariant.text => TextButton(
          onPressed: effective,
          child: child,
        ),
      AsyncButtonVariant.danger => FilledButton(
          onPressed: effective,
          style: FilledButton.styleFrom(
            backgroundColor: context.colors.danger,
            foregroundColor: context.colors.surface,
          ),
          child: child,
        ),
    };
    return expand ? SizedBox(width: double.infinity, child: button) : button;
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
                size: 17,
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
                    fontSize: 20,
                    fontWeight: FontWeight.w600,
                    fontFeatures: const [FontFeature.tabularFigures()],
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  label,
                  style: TextStyle(
                    color: colors.textSecondary,
                    fontSize: 12.5,
                  ),
                ),
              ],
            ),
          ),
          if (onTap != null)
            Icon(Icons.chevron_right, size: 18, color: colors.muted),
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
  const CourseArt({
    super.key,
    required this.seed,
    this.height = 112,
  });

  final String seed;
  final double height;

  @override
  Widget build(BuildContext context) {
    final colors = context.colors;
    return ClipRRect(
      borderRadius: BorderRadius.vertical(top: colors.cardRadius.topLeft),
      child: SizedBox(
        height: height,
        width: double.infinity,
        child: CustomPaint(
          painter: _CourseArtPainter(
            seed: seed.hashCode,
            palette: colors,
          ),
        ),
      ),
    );
  }
}

class _CourseArtPainter extends CustomPainter {
  _CourseArtPainter({required this.seed, required this.palette});

  final int seed;
  final AppColors palette;

  @override
  void paint(Canvas canvas, Size size) {
    final random = math.Random(seed);
    final background = Paint()..color = palette.heroBg;
    canvas.drawRect(Offset.zero & size, background);
    final ink = Paint()
      ..color = palette.heroText.withValues(alpha: 0.32)
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.2;
    final accent = Paint()
      ..color = palette.accent.withValues(alpha: 0.45)
      ..style = PaintingStyle.fill;
    for (var index = 0; index < 3; index++) {
      final radius = size.shortestSide * (0.22 + random.nextDouble() * 0.25);
      final center = Offset(
        size.width * (0.15 + random.nextDouble() * 0.7),
        size.height * (0.2 + random.nextDouble() * 0.6),
      );
      canvas.drawCircle(center, radius, ink);
    }
    final path = Path()
      ..moveTo(0, size.height * (0.68 + random.nextDouble() * 0.15))
      ..quadraticBezierTo(
        size.width * 0.35,
        size.height * (0.35 + random.nextDouble() * 0.2),
        size.width,
        size.height * (0.55 + random.nextDouble() * 0.2),
      );
    canvas.drawPath(
      path,
      Paint()
        ..color = palette.heroText.withValues(alpha: 0.22)
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1.4,
    );
    for (var index = 0; index < 4; index++) {
      canvas.drawCircle(
        Offset(
          size.width * (0.08 + index * 0.26),
          size.height * (0.18 + random.nextDouble() * 0.16),
        ),
        2.4,
        accent,
      );
    }
  }

  @override
  bool shouldRepaint(_CourseArtPainter oldDelegate) =>
      oldDelegate.seed != seed || oldDelegate.palette != palette;
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
                  fontSize: 20,
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
    this.maxWidth = 1180,
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
                horizontal: context.isNarrow ? 18 : 28,
                vertical: context.isNarrow ? 16 : 22,
              ),
          child: child,
        ),
      ),
    );
  }
}
