import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../app/providers.dart';
import '../app/router.dart';
import '../design/theme.dart';
import '../core/realtime/lifecycle_scope.dart';

class ByebyeOcsApp extends ConsumerWidget {
  const ByebyeOcsApp({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final router = ref.watch(routerProvider);
    final appearance = ref.watch(appearanceControllerProvider);
    return MaterialApp.router(
      title: 'ByebyeOCS',
      debugShowCheckedModeBanner: false,
      routerConfig: router,
      theme: AppTheme.build(appearance.colorScheme, Brightness.light),
      darkTheme: AppTheme.build(appearance.colorScheme, Brightness.dark),
      themeMode: appearance.materialThemeMode,
      locale: const Locale('zh', 'CN'),
      supportedLocales: const [Locale('zh', 'CN'), Locale('en')],
      localizationsDelegates: const [
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      builder: (context, child) {
        return LifecycleScope(child: child ?? const SizedBox.shrink());
      },
    );
  }
}
