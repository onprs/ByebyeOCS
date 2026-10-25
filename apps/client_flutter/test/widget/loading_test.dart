import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:byebyeocs_client/design/components.dart';

void main() {
  for (final disabled in [false, true]) {
    testWidgets(disabled ? '减少动态效果时骨架停止扫光' : '加载骨架持续扫光', (tester) async {
      await tester.pumpWidget(MaterialApp(
          home: Builder(
              builder: (context) => MediaQuery(
                    data: MediaQuery.of(context)
                        .copyWith(disableAnimations: disabled),
                    child: const Scaffold(body: SkeletonBlock(width: 160)),
                  ))));
      await tester.pump(const Duration(milliseconds: 200));
      expect(tester.hasRunningAnimations, !disabled);
      await tester.pumpWidget(const SizedBox.shrink());
    });
  }
}
