import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/design/components.dart';
import 'package:byebyeocs_client/design/page_layout.dart';
import 'package:byebyeocs_client/features/courses/course_detail_page.dart';
import 'package:byebyeocs_client/features/courses/courses_page.dart';
import 'package:byebyeocs_client/features/home/home_page.dart';
import 'package:byebyeocs_client/features/plans/plans_page.dart';
import 'package:byebyeocs_client/features/questions/question_query_page.dart';
import 'package:byebyeocs_client/features/settings/settings_page.dart';
import 'package:byebyeocs_client/features/wallet/wallet_page.dart';

import '../helpers.dart';

void main() {
  for (final width in [390.0, 1440.0]) {
    testWidgets('${width.toInt()}px 短内容页脚贴齐视口底部', (tester) async {
      await pumpTestPage(
        tester,
        const PageScrollView(child: SizedBox(height: 120)),
        size: Size(width, 900),
      );
      final viewport = tester.getRect(find.byType(PageScrollView));
      final footer = tester.getRect(find.byType(SiteFooter));
      expect(footer.bottom, moreOrLessEquals(viewport.bottom, epsilon: 1));
      expect(footer.left, viewport.left);
      expect(footer.right, viewport.right);
      expect(footer.height, lessThan(80));
      expect(tester.takeException(), isNull);
    });

    testWidgets('${width.toInt()}px 长内容页脚随正文滚动且末尾无额外留白', (tester) async {
      final controller = ScrollController();
      addTearDown(controller.dispose);
      await pumpTestPage(
        tester,
        PageScrollView(
          controller: controller,
          child: const SizedBox(height: 2600),
        ),
        size: Size(width, 900),
      );
      expect(
          find.textContaining('Created by onprs').hitTestable(), findsNothing);
      controller.jumpTo(400);
      await settle(tester);
      expect(
          find.textContaining('Created by onprs').hitTestable(), findsNothing);
      controller.jumpTo(controller.position.maxScrollExtent);
      await settle(tester);
      expect(find.textContaining('Created by onprs').hitTestable(),
          findsOneWidget);
      final footer = tester.getRect(find.byType(SiteFooter));
      final viewport = tester.getRect(find.byType(PageScrollView));
      expect(footer.bottom, moreOrLessEquals(viewport.bottom, epsilon: 1));
      expect(tester.takeException(), isNull);
    });

    testWidgets('${width.toInt()}px 各主页面页脚位置一致且课程常驻说明已移除', (tester) async {
      const pages = <Widget>[
        HomePage(),
        CoursesPage(),
        CourseDetailPage(courseId: 'c1'),
        PlansPage(),
        WalletPage(),
        SettingsPage(),
        QuestionQueryPage(),
      ];
      for (final page in pages) {
        await pumpTestPage(tester, page, size: Size(width, 1200));
        final scrollable = tester.state<ScrollableState>(find
            .descendant(
              of: find.byType(PageScrollView),
              matching: find.byType(Scrollable),
            )
            .first);
        scrollable.position.jumpTo(scrollable.position.maxScrollExtent);
        await settle(tester);
        expect(find.byType(SiteFooter), findsOneWidget,
            reason: '${page.runtimeType} 应只有一个页脚');
        final viewport = tester.getRect(find.byType(PageScrollView));
        final footer = tester.getRect(find.byType(SiteFooter));
        expect(footer.bottom, moreOrLessEquals(viewport.bottom, epsilon: 1),
            reason: '${page.runtimeType} 页脚底部不应留白');
        expect(footer.left, viewport.left);
        expect(footer.right, viewport.right);
        expect(find.text('完成数量来自学习通平台返回。普通附件标注为学习资料，不计入任务点。'), findsNothing);
        expect(
            find.text('课程内容以学习通为准。普通附件不计入未完成任务，平台确认数量会在同步后更新。'), findsNothing);
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox.shrink());
        await tester.pump();
      }
    });
  }

  testWidgets('320px 大字号页脚自然换行且不压缩文字', (tester) async {
    await pumpTestPage(
      tester,
      Builder(builder: (context) {
        return MediaQuery(
          data:
              MediaQuery.of(context).copyWith(textScaler: TextScaler.linear(2)),
          child: const PageScrollView(child: SizedBox(height: 80)),
        );
      }),
      size: const Size(320, 600),
    );
    final footer = find.byType(SiteFooter);
    expect(tester.getSize(footer).height, greaterThan(36));
    expect(find.descendant(of: footer, matching: find.byType(FittedBox)),
        findsNothing);
    expect(tester.getRect(footer).bottom, moreOrLessEquals(600, epsilon: 1));
    expect(tester.takeException(), isNull);
  });
}
