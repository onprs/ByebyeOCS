import 'package:flutter/material.dart';

import '../core/models/preferences.dart';
import 'palette.dart';

const _sansFallback = <String>[
  'BboCjk',
  'Noto Sans CJK SC',
  'Source Han Sans SC',
  'PingFang SC',
  'Microsoft YaHei',
  'WenQuanYi Micro Hei',
];

const _serifFallback = <String>[
  'BboCjk',
  'Noto Serif CJK SC',
  'Source Han Serif SC',
  'Songti SC',
  'SimSun',
  'serif',
];

class AppTheme {
  const AppTheme._();

  static ThemeData build(ColorSchemeId scheme, Brightness brightness) {
    final palette = AppPalette.resolve(scheme, brightness);
    final colors = AppColors.fromPalette(
      palette,
      brightness == Brightness.dark,
    );
    final base = ThemeData(
      useMaterial3: true,
      brightness: brightness,
      visualDensity: VisualDensity.standard,
      materialTapTargetSize: MaterialTapTargetSize.padded,
    );
    final colorScheme = base.colorScheme.copyWith(
      primary: colors.accent,
      onPrimary: colors.accentContrast,
      secondary: colors.heroText,
      onSecondary: colors.surface,
      surface: colors.surface,
      onSurface: colors.text,
      error: colors.danger,
      onError: colors.surface,
      outline: colors.border,
      outlineVariant: colors.border,
      surfaceContainerLowest: colors.background,
      surfaceContainerLow: colors.surfaceSubtle,
      surfaceContainer: colors.surface,
      surfaceContainerHigh: colors.surfaceSubtle,
      surfaceContainerHighest: colors.accentSoft,
      onSurfaceVariant: colors.textSecondary,
      shadow: Colors.black26,
      scrim: Colors.black54,
    );
    final textTheme = _textTheme(base.textTheme, colors, brightness, scheme);
    final headingFallback =
        scheme == ColorSchemeId.grove ? _serifFallback : _sansFallback;
    final headings = textTheme.copyWith(
      displayLarge: textTheme.displayLarge?.copyWith(
        fontFamilyFallback: headingFallback,
        fontWeight: FontWeight.w600,
        letterSpacing: -1.2,
      ),
      displayMedium: textTheme.displayMedium?.copyWith(
        fontFamilyFallback: headingFallback,
        fontWeight: FontWeight.w600,
        letterSpacing: -0.8,
      ),
      displaySmall: textTheme.displaySmall?.copyWith(
        fontFamilyFallback: headingFallback,
        fontWeight: FontWeight.w600,
        letterSpacing: -0.4,
      ),
      headlineMedium: textTheme.headlineMedium?.copyWith(
        fontFamilyFallback: headingFallback,
        fontWeight: FontWeight.w600,
      ),
      headlineSmall: textTheme.headlineSmall?.copyWith(
        fontFamilyFallback: headingFallback,
        fontWeight: FontWeight.w600,
      ),
      titleLarge: textTheme.titleLarge?.copyWith(
        fontFamilyFallback: headingFallback,
        fontWeight: FontWeight.w600,
      ),
    );

    final buttonShape =
        RoundedRectangleBorder(borderRadius: colors.buttonRadius);
    final buttonStyle = ButtonStyle(
      minimumSize: const WidgetStatePropertyAll(Size(0, 48)),
      padding: const WidgetStatePropertyAll(
        EdgeInsets.symmetric(horizontal: 20, vertical: 12),
      ),
      shape: WidgetStatePropertyAll(buttonShape),
      textStyle: WidgetStatePropertyAll(
        TextStyle(
          fontSize: 14,
          fontWeight: FontWeight.w600,
          fontFamilyFallback: _sansFallback,
        ),
      ),
    );

    final border = OutlineInputBorder(
      borderRadius: BorderRadius.circular(palette.radius - 1),
      borderSide: BorderSide(color: colors.border),
    );

    return base.copyWith(
      colorScheme: colorScheme,
      scaffoldBackgroundColor: colors.background,
      canvasColor: colors.surface,
      cardColor: colors.surface,
      dividerColor: colors.border,
      textTheme: headings,
      primaryTextTheme: headings,
      extensions: <ThemeExtension<dynamic>>[colors],
      splashFactory: InkSparkle.splashFactory,
      appBarTheme: AppBarTheme(
        backgroundColor: colors.background,
        foregroundColor: colors.text,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        scrolledUnderElevation: 0,
        centerTitle: false,
        titleTextStyle: headings.titleLarge,
      ),
      cardTheme: CardThemeData(
        color: colors.surface,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        margin: EdgeInsets.zero,
        shape: RoundedRectangleBorder(
          borderRadius: colors.cardRadius,
          side: BorderSide(color: colors.border),
        ),
      ),
      dividerTheme: DividerThemeData(
        color: colors.border,
        thickness: 1,
        space: 1,
      ),
      inputDecorationTheme: InputDecorationThemeData(
        filled: true,
        fillColor: colors.surfaceSubtle,
        contentPadding: const EdgeInsets.symmetric(
          horizontal: 14,
          vertical: 15,
        ),
        hintStyle: TextStyle(
          color: colors.muted,
          fontFamilyFallback: _sansFallback,
        ),
        labelStyle: TextStyle(color: colors.textSecondary),
        floatingLabelStyle: TextStyle(color: colors.accent),
        border: border,
        enabledBorder: border,
        focusedBorder: border.copyWith(
          borderSide: BorderSide(color: colors.accent, width: 1.6),
        ),
        errorBorder: border.copyWith(
          borderSide: BorderSide(color: colors.danger),
        ),
        focusedErrorBorder: border.copyWith(
          borderSide: BorderSide(color: colors.danger, width: 1.6),
        ),
        errorStyle: TextStyle(color: colors.danger, fontSize: 12.5),
        helperStyle: TextStyle(color: colors.textSecondary, fontSize: 12.5),
      ),
      filledButtonTheme: FilledButtonThemeData(style: buttonStyle),
      elevatedButtonTheme: ElevatedButtonThemeData(
        style: buttonStyle.copyWith(
          backgroundColor: WidgetStatePropertyAll(colors.accent),
          foregroundColor: WidgetStatePropertyAll(colors.accentContrast),
          elevation: const WidgetStatePropertyAll(0),
          shape: WidgetStatePropertyAll(buttonShape),
        ),
      ),
      outlinedButtonTheme: OutlinedButtonThemeData(
        style: buttonStyle.copyWith(
          foregroundColor: WidgetStatePropertyAll(colors.text),
          side: WidgetStatePropertyAll(BorderSide(color: colors.border)),
          backgroundColor: WidgetStatePropertyAll(colors.surface),
        ),
      ),
      textButtonTheme: TextButtonThemeData(
        style: ButtonStyle(
          foregroundColor: WidgetStatePropertyAll(colors.accent),
          minimumSize: const WidgetStatePropertyAll(Size(0, 44)),
          padding: const WidgetStatePropertyAll(
            EdgeInsets.symmetric(horizontal: 10, vertical: 10),
          ),
          textStyle: const WidgetStatePropertyAll(
            TextStyle(fontWeight: FontWeight.w600),
          ),
        ),
      ),
      iconButtonTheme: IconButtonThemeData(
        style: ButtonStyle(
          minimumSize: const WidgetStatePropertyAll(Size(44, 44)),
          iconSize: const WidgetStatePropertyAll(20),
          foregroundColor: WidgetStatePropertyAll(colors.textSecondary),
          shape: WidgetStatePropertyAll(
            RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(8),
            ),
          ),
        ),
      ),
      chipTheme: ChipThemeData(
        backgroundColor: colors.surfaceSubtle,
        selectedColor: colors.accentSoft,
        secondarySelectedColor: colors.accentSoft,
        labelStyle: TextStyle(
          color: colors.text,
          fontSize: 13,
          fontFamilyFallback: _sansFallback,
        ),
        secondaryLabelStyle: TextStyle(
          color: colors.accent,
          fontWeight: FontWeight.w600,
        ),
        side: BorderSide(color: colors.border),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(palette.radius - 2),
        ),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
        showCheckmark: false,
      ),
      checkboxTheme: CheckboxThemeData(
        fillColor: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.selected)) return colors.accent;
          return Colors.transparent;
        }),
        checkColor: WidgetStatePropertyAll(colors.accentContrast),
        side: BorderSide(color: colors.muted),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(4)),
      ),
      switchTheme: SwitchThemeData(
        thumbColor: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.selected)) {
            return colors.accentContrast;
          }
          return colors.surface;
        }),
        trackColor: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.selected)) return colors.accent;
          return colors.border;
        }),
        trackOutlineColor: const WidgetStatePropertyAll(Colors.transparent),
      ),
      radioTheme: RadioThemeData(
        fillColor: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.selected)) return colors.accent;
          return colors.muted;
        }),
      ),
      progressIndicatorTheme: ProgressIndicatorThemeData(
        color: colors.accent,
        linearTrackColor: colors.border,
        circularTrackColor: colors.border,
      ),
      navigationBarTheme: NavigationBarThemeData(
        backgroundColor: colors.surface,
        surfaceTintColor: Colors.transparent,
        indicatorColor: colors.accentSoft,
        height: 68,
        labelBehavior: NavigationDestinationLabelBehavior.alwaysShow,
        labelTextStyle: WidgetStateProperty.resolveWith((states) {
          return TextStyle(
            fontSize: 12,
            fontWeight: states.contains(WidgetState.selected)
                ? FontWeight.w600
                : FontWeight.w500,
            color: states.contains(WidgetState.selected)
                ? colors.accent
                : colors.textSecondary,
            fontFamilyFallback: _sansFallback,
          );
        }),
        iconTheme: WidgetStateProperty.resolveWith((states) {
          return IconThemeData(
            size: 23,
            color: states.contains(WidgetState.selected)
                ? colors.accent
                : colors.textSecondary,
          );
        }),
      ),
      bottomSheetTheme: BottomSheetThemeData(
        backgroundColor: colors.surface,
        modalBackgroundColor: colors.surface,
        surfaceTintColor: Colors.transparent,
        showDragHandle: true,
        dragHandleColor: colors.border,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.vertical(
            top: Radius.circular(palette.radius + 4),
          ),
        ),
      ),
      dialogTheme: DialogThemeData(
        backgroundColor: colors.surface,
        surfaceTintColor: Colors.transparent,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(palette.radius + 2),
          side: BorderSide(color: colors.border),
        ),
        titleTextStyle: headings.titleLarge,
        contentTextStyle: headings.bodyMedium,
      ),
      snackBarTheme: SnackBarThemeData(
        backgroundColor: colors.text,
        contentTextStyle: TextStyle(
          color: colors.background,
          fontFamilyFallback: _sansFallback,
        ),
        behavior: SnackBarBehavior.floating,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(palette.radius - 2),
        ),
        insetPadding: const EdgeInsets.all(16),
      ),
      tooltipTheme: TooltipThemeData(
        decoration: BoxDecoration(
          color: colors.text,
          borderRadius: BorderRadius.circular(6),
        ),
        textStyle: TextStyle(color: colors.background, fontSize: 12),
        waitDuration: const Duration(milliseconds: 500),
      ),
      scrollbarTheme: ScrollbarThemeData(
        thumbColor:
            WidgetStatePropertyAll(colors.muted.withValues(alpha: 0.55)),
        radius: const Radius.circular(8),
        thickness: const WidgetStatePropertyAll(6),
      ),
      tabBarTheme: TabBarThemeData(
        labelColor: colors.text,
        unselectedLabelColor: colors.textSecondary,
        indicatorColor: colors.accent,
        indicatorSize: TabBarIndicatorSize.label,
        dividerColor: colors.border,
        labelStyle: TextStyle(
          fontWeight: FontWeight.w600,
          fontSize: 14,
          fontFamilyFallback: _sansFallback,
        ),
        unselectedLabelStyle: TextStyle(
          fontWeight: FontWeight.w500,
          fontSize: 14,
          fontFamilyFallback: _sansFallback,
        ),
      ),
      segmentedButtonTheme: SegmentedButtonThemeData(
        style: ButtonStyle(
          minimumSize: const WidgetStatePropertyAll(Size(0, 44)),
          textStyle: const WidgetStatePropertyAll(
            TextStyle(fontSize: 13.5, fontWeight: FontWeight.w600),
          ),
          foregroundColor: WidgetStateProperty.resolveWith((states) {
            if (states.contains(WidgetState.selected)) return colors.accent;
            return colors.textSecondary;
          }),
          backgroundColor: WidgetStateProperty.resolveWith((states) {
            if (states.contains(WidgetState.selected)) return colors.accentSoft;
            return colors.surface;
          }),
          side: WidgetStatePropertyAll(
            BorderSide(color: colors.border),
          ),
          shape: WidgetStatePropertyAll(
            RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(palette.radius - 2),
            ),
          ),
        ),
      ),
      listTileTheme: ListTileThemeData(
        iconColor: colors.textSecondary,
        textColor: colors.text,
        contentPadding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
        minVerticalPadding: 12,
        shape: RoundedRectangleBorder(borderRadius: colors.cardRadius),
      ),
      textSelectionTheme: TextSelectionThemeData(
        cursorColor: colors.accent,
        selectionColor: colors.accent.withValues(alpha: 0.22),
        selectionHandleColor: colors.accent,
      ),
      focusColor: colors.accentSoft,
      hoverColor: colors.accentSoft.withValues(alpha: 0.6),
      highlightColor: colors.accentSoft.withValues(alpha: 0.5),
      splashColor: colors.accent.withValues(alpha: 0.08),
    );
  }

  static TextTheme _textTheme(
    TextTheme base,
    AppColors colors,
    Brightness brightness,
    ColorSchemeId scheme,
  ) {
    final text = base
        .apply(
          bodyColor: colors.text,
          displayColor: colors.text,
          fontFamilyFallback: _sansFallback,
        )
        .copyWith(
          bodyLarge: base.bodyLarge?.copyWith(fontSize: 15.5, height: 1.65),
          bodyMedium: base.bodyMedium?.copyWith(fontSize: 14.5, height: 1.6),
          bodySmall: base.bodySmall?.copyWith(fontSize: 12.5, height: 1.5),
          titleMedium: base.titleMedium?.copyWith(
            fontSize: 15.5,
            fontWeight: FontWeight.w600,
          ),
          titleSmall: base.titleSmall?.copyWith(
            fontSize: 13.5,
            fontWeight: FontWeight.w600,
          ),
          labelLarge: base.labelLarge?.copyWith(
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
          labelMedium: base.labelMedium?.copyWith(fontSize: 12.5),
          labelSmall: base.labelSmall?.copyWith(fontSize: 11.5),
        );
    return text.copyWith(
      bodyLarge: text.bodyLarge?.copyWith(
        fontFamilyFallback:
            scheme == ColorSchemeId.grove ? _serifFallback : null,
      ),
      bodyMedium: text.bodyMedium?.copyWith(
        fontFamilyFallback:
            scheme == ColorSchemeId.grove ? _serifFallback : null,
      ),
    );
  }
}

extension AppThemeContext on BuildContext {
  AppColors get colors => AppColors.of(this);
  bool get isNarrow => MediaQuery.sizeOf(this).width < 760;
  bool get isDesktop => MediaQuery.sizeOf(this).width >= 1000;
}
