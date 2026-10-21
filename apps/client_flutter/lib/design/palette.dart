import 'package:flutter/material.dart';

import '../core/models/preferences.dart';

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

  static const graphiteLight = AppPalette(
    background: Color(0xFFF6F7F9),
    surface: Color(0xFFFFFFFF),
    surfaceSubtle: Color(0xFFFAFBFC),
    text: Color(0xFF252831),
    textSecondary: Color(0xFF656B77),
    muted: Color(0xFF8B919C),
    border: Color(0xFFE7E9EE),
    accent: Color(0xFF4969CE),
    accentContrast: Color(0xFFFFFFFF),
    accentSoft: Color(0xFFEEF1FB),
    sidebar: Color(0xFF1D2026),
    sidebarText: Color(0xFFA6AAB4),
    sidebarActive: Color(0xFF303641),
    success: Color(0xFF278165),
    successBg: Color(0xFFECF6F0),
    warning: Color(0xFFA07830),
    warningBg: Color(0xFFFBF5E8),
    danger: Color(0xFFBA5752),
    dangerBg: Color(0xFFFCEEEE),
    heroBg: Color(0xFFECF0F9),
    heroText: Color(0xFF344B7C),
    chartSecondary: Color(0xFF9EACBF),
    radius: 9,
  );

  static const graphiteDark = AppPalette(
    background: Color(0xFF131518),
    surface: Color(0xFF1C1F24),
    surfaceSubtle: Color(0xFF181B20),
    text: Color(0xFFE3E6ED),
    textSecondary: Color(0xFFA2A9B5),
    muted: Color(0xFF7C8594),
    border: Color(0xFF2D323B),
    accent: Color(0xFF91A7ED),
    accentContrast: Color(0xFF17233C),
    accentSoft: Color(0xFF29334D),
    sidebar: Color(0xFF14161A),
    sidebarText: Color(0xFF929AA9),
    sidebarActive: Color(0xFF29313E),
    success: Color(0xFF85C3A6),
    successBg: Color(0xFF243C31),
    warning: Color(0xFFD0AD6C),
    warningBg: Color(0xFF393224),
    danger: Color(0xFFE29B94),
    dangerBg: Color(0xFF3C292B),
    heroBg: Color(0xFF273248),
    heroText: Color(0xFFD3DDED),
    chartSecondary: Color(0xFF647387),
    radius: 9,
  );

  static const groveLight = AppPalette(
    background: Color(0xFFF6F7F2),
    surface: Color(0xFFFFFFFC),
    surfaceSubtle: Color(0xFFF8F9F4),
    text: Color(0xFF2C3932),
    textSecondary: Color(0xFF738074),
    muted: Color(0xFF8E988F),
    border: Color(0xFFE3E7DE),
    accent: Color(0xFF2D7159),
    accentContrast: Color(0xFFFFFFFF),
    accentSoft: Color(0xFFEAF1E7),
    sidebar: Color(0xFFEFF2EA),
    sidebarText: Color(0xFF758173),
    sidebarActive: Color(0xFFE2EADB),
    success: Color(0xFF278165),
    successBg: Color(0xFFECF6F0),
    warning: Color(0xFFA07830),
    warningBg: Color(0xFFFBF5E8),
    danger: Color(0xFFBA5752),
    dangerBg: Color(0xFFFCEEEE),
    heroBg: Color(0xFFE8EEDF),
    heroText: Color(0xFF32543E),
    chartSecondary: Color(0xFFB7C4A2),
    radius: 13,
  );

  static const groveDark = AppPalette(
    background: Color(0xFF151C18),
    surface: Color(0xFF1E2821),
    surfaceSubtle: Color(0xFF1A231D),
    text: Color(0xFFE0E9DF),
    textSecondary: Color(0xFFA2B09F),
    muted: Color(0xFF7D8D7E),
    border: Color(0xFF324033),
    accent: Color(0xFF9EC8AA),
    accentContrast: Color(0xFF1A3021),
    accentSoft: Color(0xFF304735),
    sidebar: Color(0xFF18211B),
    sidebarText: Color(0xFF8FA08B),
    sidebarActive: Color(0xFF2D3E2C),
    success: Color(0xFF85C3A6),
    successBg: Color(0xFF243C31),
    warning: Color(0xFFD0AD6C),
    warningBg: Color(0xFF393224),
    danger: Color(0xFFE29B94),
    dangerBg: Color(0xFF3C292B),
    heroBg: Color(0xFF2A3D2B),
    heroText: Color(0xFFD4E8CD),
    chartSecondary: Color(0xFF75836C),
    radius: 13,
  );

  static const indigoLight = AppPalette(
    background: Color(0xFFF5F6FB),
    surface: Color(0xFFFFFFFF),
    surfaceSubtle: Color(0xFFF9F9FD),
    text: Color(0xFF232843),
    textSecondary: Color(0xFF72778D),
    muted: Color(0xFF9296AB),
    border: Color(0xFFE6E8F1),
    accent: Color(0xFF6655CA),
    accentContrast: Color(0xFFFFFFFF),
    accentSoft: Color(0xFFEFEDFB),
    sidebar: Color(0xFF1A2341),
    sidebarText: Color(0xFFA4AECB),
    sidebarActive: Color(0xFF33365F),
    success: Color(0xFF278165),
    successBg: Color(0xFFECF6F0),
    warning: Color(0xFFA07830),
    warningBg: Color(0xFFFBF5E8),
    danger: Color(0xFFBA5752),
    dangerBg: Color(0xFFFCEEEE),
    heroBg: Color(0xFFE9E7F7),
    heroText: Color(0xFF3A3564),
    chartSecondary: Color(0xFFACA2DB),
    radius: 11,
  );

  static const indigoDark = AppPalette(
    background: Color(0xFF121727),
    surface: Color(0xFF1C2339),
    surfaceSubtle: Color(0xFF181F33),
    text: Color(0xFFE5E7F3),
    textSecondary: Color(0xFFA0A9C5),
    muted: Color(0xFF7B86A6),
    border: Color(0xFF303A56),
    accent: Color(0xFFAFA6F1),
    accentContrast: Color(0xFF272044),
    accentSoft: Color(0xFF36365C),
    sidebar: Color(0xFF12192D),
    sidebarText: Color(0xFF95A2C5),
    sidebarActive: Color(0xFF303B5D),
    success: Color(0xFF85C3A6),
    successBg: Color(0xFF243C31),
    warning: Color(0xFFD0AD6C),
    warningBg: Color(0xFF393224),
    danger: Color(0xFFE29B94),
    dangerBg: Color(0xFF3C292B),
    heroBg: Color(0xFF333651),
    heroText: Color(0xFFE0DEF7),
    chartSecondary: Color(0xFF706A98),
    radius: 11,
  );

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
  BorderRadius get buttonRadius => BorderRadius.circular(radius - 2);

  static AppColors of(BuildContext context) {
    return Theme.of(context).extension<AppColors>() ??
        const AppColors(
          palette: AppPalette.graphiteLight,
          background: Color(0xFFF6F7F9),
          surface: Colors.white,
          surfaceSubtle: Color(0xFFFAFBFC),
          text: Color(0xFF252831),
          textSecondary: Color(0xFF656B77),
          muted: Color(0xFF8B919C),
          border: Color(0xFFE7E9EE),
          accent: Color(0xFF4969CE),
          accentContrast: Colors.white,
          accentSoft: Color(0xFFEEF1FB),
          sidebar: Color(0xFF1D2026),
          sidebarText: Color(0xFFA6AAB4),
          sidebarActive: Color(0xFF303641),
          success: Color(0xFF278165),
          successBg: Color(0xFFECF6F0),
          warning: Color(0xFFA07830),
          warningBg: Color(0xFFFBF5E8),
          danger: Color(0xFFBA5752),
          dangerBg: Color(0xFFFCEEEE),
          heroBg: Color(0xFFECF0F9),
          heroText: Color(0xFF344B7C),
          chartSecondary: Color(0xFF9EACBF),
          radius: 9,
          isDark: false,
        );
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
