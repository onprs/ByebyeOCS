import '../api/json.dart';

enum ColorSchemeId { graphite, grove, indigo }

enum AppThemeMode { system, light, dark }

extension ColorSchemeIdValue on ColorSchemeId {
  String get value => name;

  String get label => switch (this) {
        ColorSchemeId.graphite => '石墨',
        ColorSchemeId.grove => '青禾',
        ColorSchemeId.indigo => '靛蓝',
      };

  static ColorSchemeId parse(String? value) {
    return switch (value) {
      'grove' => ColorSchemeId.grove,
      'indigo' => ColorSchemeId.indigo,
      _ => ColorSchemeId.graphite,
    };
  }
}

extension AppThemeModeValue on AppThemeMode {
  String get value => name;

  String get label => switch (this) {
        AppThemeMode.system => '跟随系统',
        AppThemeMode.light => '浅色',
        AppThemeMode.dark => '深色',
      };

  static AppThemeMode parse(String? value) {
    return switch (value) {
      'light' => AppThemeMode.light,
      'dark' => AppThemeMode.dark,
      _ => AppThemeMode.system,
    };
  }
}

class UserPreferences {
  const UserPreferences({
    this.colorScheme = ColorSchemeId.graphite,
    this.themeMode = AppThemeMode.system,
    this.taskAttention = true,
    this.walletChanges = true,
    this.version = 0,
  });

  final ColorSchemeId colorScheme;
  final AppThemeMode themeMode;
  final bool taskAttention;
  final bool walletChanges;
  final int version;

  UserPreferences copyWith({
    ColorSchemeId? colorScheme,
    AppThemeMode? themeMode,
    bool? taskAttention,
    bool? walletChanges,
    int? version,
  }) {
    return UserPreferences(
      colorScheme: colorScheme ?? this.colorScheme,
      themeMode: themeMode ?? this.themeMode,
      taskAttention: taskAttention ?? this.taskAttention,
      walletChanges: walletChanges ?? this.walletChanges,
      version: version ?? this.version,
    );
  }

  static UserPreferences parse(Object? raw) {
    final map = asObject(raw);
    final notifications = asObject(map?['notifications']);
    return UserPreferences(
      colorScheme: ColorSchemeIdValue.parse(readString(map, 'color_scheme')),
      themeMode: AppThemeModeValue.parse(readString(map, 'theme_mode')),
      taskAttention: readBool(notifications, 'task_attention') ?? true,
      walletChanges: readBool(notifications, 'wallet_changes') ?? true,
      version: readInt(map, 'version') ?? 0,
    );
  }
}
