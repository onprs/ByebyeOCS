// 由 scripts/generate-design-tokens.py 生成；请修改 design/tokens.json。
part of 'palette.dart';

abstract final class AppMetrics {
  static const double spaceUnit = 8;
  static const double radius = 10;
  static const double radiusSm = 6;
  static const double radiusLg = 14;
  static const double controlHeight = 36;
  static const double touchHeight = 44;
  static const double sidebarWidth = 224;
  static const double topbarHeight = 64;
  static const double contentWidth = 1280;
  static const double tableWidth = 1440;
  static const double drawerWidth = 600;
  static const double pagePadding = 32;
}

abstract final class AppTypography {
  static const double fontTitle = 26;
  static const double fontSection = 16;
  static const double fontBody = 14;
  static const double fontSecondary = 13;
  static const double fontCaption = 12;
  static const double fontDisplay = 34;
}

const _graphiteLight = AppPalette(
  background: Color(0xFFF7F8FA),
  surface: Color(0xFFFFFFFF),
  surfaceSubtle: Color(0xFFF4F6F9),
  text: Color(0xFF182230),
  textSecondary: Color(0xFF5D6B7A),
  muted: Color(0xFF68717E),
  border: Color(0xFFE6EAF0),
  accent: Color(0xFF4969CE),
  accentContrast: Color(0xFFFFFFFF),
  accentSoft: Color(0xFFF1F4FD),
  sidebar: Color(0xFF1D2026),
  sidebarText: Color(0xFFA6AAB4),
  sidebarActive: Color(0xFF303641),
  success: Color(0xFF257B60),
  successBg: Color(0xFFECF6F0),
  warning: Color(0xFF8D6828),
  warningBg: Color(0xFFFBF5E8),
  danger: Color(0xFFB04E49),
  dangerBg: Color(0xFFFCEFEE),
  heroBg: Color(0xFFF4F6F9),
  heroText: Color(0xFF182230),
  chartSecondary: Color(0xFF9EACBF),
  radius: AppMetrics.radius,
);

const _graphiteDark = AppPalette(
  background: Color(0xFF131518),
  surface: Color(0xFF1C1F24),
  surfaceSubtle: Color(0xFF181B20),
  text: Color(0xFFE3E6ED),
  textSecondary: Color(0xFFA2A9B5),
  muted: Color(0xFF98A2B2),
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
  heroBg: Color(0xFF181B20),
  heroText: Color(0xFFE3E6ED),
  chartSecondary: Color(0xFF647387),
  radius: AppMetrics.radius,
);

const _groveLight = AppPalette(
  background: Color(0xFFF6F7F2),
  surface: Color(0xFFFFFFFF),
  surfaceSubtle: Color(0xFFF8F9F4),
  text: Color(0xFF2C3932),
  textSecondary: Color(0xFF647166),
  muted: Color(0xFF657365),
  border: Color(0xFFE3E7DE),
  accent: Color(0xFF2D7159),
  accentContrast: Color(0xFFFFFFFF),
  accentSoft: Color(0xFFEAF1E7),
  sidebar: Color(0xFFEFF2EA),
  sidebarText: Color(0xFF616F62),
  sidebarActive: Color(0xFFE2EADB),
  success: Color(0xFF2D7159),
  successBg: Color(0xFFEAF3E9),
  warning: Color(0xFF86662A),
  warningBg: Color(0xFFF8F2E3),
  danger: Color(0xFFA9504A),
  dangerBg: Color(0xFFF8EEEA),
  heroBg: Color(0xFFF8F9F4),
  heroText: Color(0xFF2C3932),
  chartSecondary: Color(0xFFB7C4A2),
  radius: AppMetrics.radius,
);

const _groveDark = AppPalette(
  background: Color(0xFF151C18),
  surface: Color(0xFF1E2821),
  surfaceSubtle: Color(0xFF1A231D),
  text: Color(0xFFE0E9DF),
  textSecondary: Color(0xFFA2B09F),
  muted: Color(0xFF98A2B2),
  border: Color(0xFF324033),
  accent: Color(0xFF9EC8AA),
  accentContrast: Color(0xFF1A3021),
  accentSoft: Color(0xFF304735),
  sidebar: Color(0xFF18211B),
  sidebarText: Color(0xFF8FA08B),
  sidebarActive: Color(0xFF2D3E2C),
  success: Color(0xFF9EC8AA),
  successBg: Color(0xFF2B4233),
  warning: Color(0xFFD5B97E),
  warningBg: Color(0xFF3A3527),
  danger: Color(0xFFDDA49B),
  dangerBg: Color(0xFF3C2E2B),
  heroBg: Color(0xFF1A231D),
  heroText: Color(0xFFE0E9DF),
  chartSecondary: Color(0xFF75836C),
  radius: AppMetrics.radius,
);

const _indigoLight = AppPalette(
  background: Color(0xFFF5F6FB),
  surface: Color(0xFFFFFFFF),
  surfaceSubtle: Color(0xFFF9F9FD),
  text: Color(0xFF232843),
  textSecondary: Color(0xFF62687F),
  muted: Color(0xFF68718A),
  border: Color(0xFFE6E8F1),
  accent: Color(0xFF6655CA),
  accentContrast: Color(0xFFFFFFFF),
  accentSoft: Color(0xFFEFEDFB),
  sidebar: Color(0xFF1A2341),
  sidebarText: Color(0xFFA4AECB),
  sidebarActive: Color(0xFF33365F),
  success: Color(0xFF2E755F),
  successBg: Color(0xFFEAF4EF),
  warning: Color(0xFF89672B),
  warningBg: Color(0xFFF9F3E5),
  danger: Color(0xFFA6514C),
  dangerBg: Color(0xFFFAEEED),
  heroBg: Color(0xFFF9F9FD),
  heroText: Color(0xFF232843),
  chartSecondary: Color(0xFFACA2DB),
  radius: AppMetrics.radius,
);

const _indigoDark = AppPalette(
  background: Color(0xFF121727),
  surface: Color(0xFF1C2339),
  surfaceSubtle: Color(0xFF181F33),
  text: Color(0xFFE5E7F3),
  textSecondary: Color(0xFFA0A9C5),
  muted: Color(0xFF98A2B2),
  border: Color(0xFF303A56),
  accent: Color(0xFFAFA6F1),
  accentContrast: Color(0xFF272044),
  accentSoft: Color(0xFF36365C),
  sidebar: Color(0xFF12192D),
  sidebarText: Color(0xFF95A2C5),
  sidebarActive: Color(0xFF303B5D),
  success: Color(0xFF90C8AB),
  successBg: Color(0xFF263D37),
  warning: Color(0xFFD6B879),
  warningBg: Color(0xFF393329),
  danger: Color(0xFFE0A09A),
  dangerBg: Color(0xFF3D2D35),
  heroBg: Color(0xFF181F33),
  heroText: Color(0xFFE5E7F3),
  chartSecondary: Color(0xFF706A98),
  radius: AppMetrics.radius,
);
