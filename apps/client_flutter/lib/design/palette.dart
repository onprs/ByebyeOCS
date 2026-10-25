import 'package:flutter/material.dart';

import '../core/models/preferences.dart';

part 'palette_data.dart';

class AppPalette {
  const AppPalette({
    required this.background,
    required this.surface,
    required this.surfaceSubtle,
    required this.text,
    required this.textSecondary,
    required this.muted,
    required this.border,
    required this.accent,
    required this.accentContrast,
    required this.accentSoft,
    required this.sidebar,
    required this.sidebarText,
    required this.sidebarActive,
    required this.success,
    required this.successBg,
    required this.warning,
    required this.warningBg,
    required this.danger,
    required this.dangerBg,
    required this.heroBg,
    required this.heroText,
    required this.chartSecondary,
    required this.radius,
  });

  final Color background;
  final Color surface;
  final Color surfaceSubtle;
  final Color text;
  final Color textSecondary;
  final Color muted;
  final Color border;
  final Color accent;
  final Color accentContrast;
  final Color accentSoft;
  final Color sidebar;
  final Color sidebarText;
  final Color sidebarActive;
  final Color success;
  final Color successBg;
  final Color warning;
  final Color warningBg;
  final Color danger;
  final Color dangerBg;
  final Color heroBg;
  final Color heroText;
  final Color chartSecondary;
  final double radius;

  static const graphiteLight = _graphiteLight;
  static const graphiteDark = _graphiteDark;
  static const groveLight = _groveLight;
  static const groveDark = _groveDark;
  static const indigoLight = _indigoLight;
  static const indigoDark = _indigoDark;

  static AppPalette resolve(ColorSchemeId id, Brightness brightness) {
    final dark = brightness == Brightness.dark;
    return switch (id) {
      ColorSchemeId.grove => dark ? groveDark : groveLight,
      ColorSchemeId.indigo => dark ? indigoDark : indigoLight,
      ColorSchemeId.graphite => dark ? graphiteDark : graphiteLight,
    };
  }
}

@immutable
class AppColors extends ThemeExtension<AppColors> {
  const AppColors({
    required this.palette,
    required this.background,
    required this.surface,
    required this.surfaceSubtle,
    required this.text,
    required this.textSecondary,
    required this.muted,
    required this.border,
    required this.accent,
    required this.accentContrast,
    required this.accentSoft,
    required this.sidebar,
    required this.sidebarText,
    required this.sidebarActive,
    required this.success,
    required this.successBg,
    required this.warning,
    required this.warningBg,
    required this.danger,
    required this.dangerBg,
    required this.heroBg,
    required this.heroText,
    required this.chartSecondary,
    required this.radius,
    required this.isDark,
  });

  final AppPalette palette;
  final Color background;
  final Color surface;
  final Color surfaceSubtle;
  final Color text;
  final Color textSecondary;
  final Color muted;
  final Color border;
  final Color accent;
  final Color accentContrast;
  final Color accentSoft;
  final Color sidebar;
  final Color sidebarText;
  final Color sidebarActive;
  final Color success;
  final Color successBg;
  final Color warning;
  final Color warningBg;
  final Color danger;
  final Color dangerBg;
  final Color heroBg;
  final Color heroText;
  final Color chartSecondary;
  final double radius;
  final bool isDark;

  BorderRadius get cardRadius => BorderRadius.circular(radius);
  BorderRadius get buttonRadius => BorderRadius.circular(AppMetrics.radiusSm);

  static AppColors of(BuildContext context) {
    return Theme.of(context).extension<AppColors>() ??
        AppColors.fromPalette(AppPalette.graphiteLight, false);
  }

  @override
  AppColors copyWith({AppPalette? palette, bool? isDark}) {
    final next = palette ?? this.palette;
    final dark = isDark ?? this.isDark;
    return AppColors.fromPalette(next, dark);
  }

  factory AppColors.fromPalette(AppPalette palette, bool isDark) {
    return AppColors(
      palette: palette,
      background: palette.background,
      surface: palette.surface,
      surfaceSubtle: palette.surfaceSubtle,
      text: palette.text,
      textSecondary: palette.textSecondary,
      muted: palette.muted,
      border: palette.border,
      accent: palette.accent,
      accentContrast: palette.accentContrast,
      accentSoft: palette.accentSoft,
      sidebar: palette.sidebar,
      sidebarText: palette.sidebarText,
      sidebarActive: palette.sidebarActive,
      success: palette.success,
      successBg: palette.successBg,
      warning: palette.warning,
      warningBg: palette.warningBg,
      danger: palette.danger,
      dangerBg: palette.dangerBg,
      heroBg: palette.heroBg,
      heroText: palette.heroText,
      chartSecondary: palette.chartSecondary,
      radius: palette.radius,
      isDark: isDark,
    );
  }

  @override
  AppColors lerp(covariant AppColors? other, double t) {
    if (other == null) return this;
    Color mix(Color a, Color b) => Color.lerp(a, b, t) ?? b;
    return AppColors(
      palette: t < 0.5 ? palette : other.palette,
      background: mix(background, other.background),
      surface: mix(surface, other.surface),
      surfaceSubtle: mix(surfaceSubtle, other.surfaceSubtle),
      text: mix(text, other.text),
      textSecondary: mix(textSecondary, other.textSecondary),
      muted: mix(muted, other.muted),
      border: mix(border, other.border),
      accent: mix(accent, other.accent),
      accentContrast: mix(accentContrast, other.accentContrast),
      accentSoft: mix(accentSoft, other.accentSoft),
      sidebar: mix(sidebar, other.sidebar),
      sidebarText: mix(sidebarText, other.sidebarText),
      sidebarActive: mix(sidebarActive, other.sidebarActive),
      success: mix(success, other.success),
      successBg: mix(successBg, other.successBg),
      warning: mix(warning, other.warning),
      warningBg: mix(warningBg, other.warningBg),
      danger: mix(danger, other.danger),
      dangerBg: mix(dangerBg, other.dangerBg),
      heroBg: mix(heroBg, other.heroBg),
      heroText: mix(heroText, other.heroText),
      chartSecondary: mix(chartSecondary, other.chartSecondary),
      radius: radius + (other.radius - radius) * t,
      isDark: t < 0.5 ? isDark : other.isDark,
    );
  }
}
