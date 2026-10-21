import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:byebyeocs_client/core/models/data_request.dart';
import 'package:byebyeocs_client/core/models/preferences.dart';
import 'package:byebyeocs_client/design/theme.dart';
import 'package:byebyeocs_client/features/settings/deletion_receipt_page.dart';
import 'package:byebyeocs_client/features/settings/privacy_controller.dart';

class _ReceiptController extends DeletionReceiptController {
  @override
  DataRequest? build() => const DataRequest(
        id: 'dr-fixture-001',
        kind: 'deletion',
        status: 'needs_review',
        reviewReason: '资金与必要保留记录待核对',
      );
}

void main() {
  testWidgets('删除回执显示申请编号、最后已知状态与退出说明', (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          deletionReceiptProvider.overrideWith(_ReceiptController.new),
        ],
        child: MaterialApp(
          theme: AppTheme.build(ColorSchemeId.graphite, Brightness.light),
          home: const DeletionReceiptPage(),
        ),
      ),
    );
    await tester.pump();

    expect(find.text('删除申请已提交'), findsOneWidget);
    expect(find.text('dr-fixture-001'), findsOneWidget);
    expect(find.text('待人工核对'), findsOneWidget);
    expect(find.textContaining('本次会话已退出'), findsOneWidget);
    expect(find.textContaining('不会在这里推测'), findsOneWidget);
    expect(find.textContaining('凭该编号和最后已知状态联系管理员'), findsOneWidget);
    expect(find.textContaining('重新登录后可查看'), findsNothing);
    expect(find.text('返回登录'), findsOneWidget);
    expect(find.textContaining('账户已删除'), findsNothing);
  });
}
