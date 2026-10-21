import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'app/app.dart';
import 'core/platform/platform_setup.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  configurePlatform();
  runApp(const ProviderScope(child: ByebyeOcsApp()));
}
