import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/api/api_error.dart';
import '../../core/models/data_request.dart';
import '../../core/platform/export_download.dart' as platform;

class PrivacyState {
  const PrivacyState({
    this.exports = const <DataRequest>[],
    this.deletions = const <DataRequest>[],
    this.exportsCursor,
    this.deletionsCursor,
    this.refreshing = false,
    this.requestingExport = false,
    this.requestingDeletion = false,
    this.downloadingId,
    this.notice,
    this.error,
    this.errorCode,
  });

  final List<DataRequest> exports;
  final List<DataRequest> deletions;
  final String? exportsCursor;
  final String? deletionsCursor;
  final bool refreshing;
  final bool requestingExport;
  final bool requestingDeletion;
  final String? downloadingId;
  final String? notice;
  final String? error;
  final String? errorCode;

  PrivacyState copyWith({
    List<DataRequest>? exports,
    List<DataRequest>? deletions,
    String? exportsCursor,
    String? deletionsCursor,
    bool? refreshing,
    bool? requestingExport,
    bool? requestingDeletion,
    String? downloadingId,
    String? notice,
    String? error,
    String? errorCode,
    bool clearDownloading = false,
    bool clearNotice = false,
    bool clearError = false,
    bool clearExportsCursor = false,
    bool clearDeletionsCursor = false,
  }) {
    return PrivacyState(
      exports: exports ?? this.exports,
      deletions: deletions ?? this.deletions,
      exportsCursor:
          clearExportsCursor ? null : exportsCursor ?? this.exportsCursor,
      deletionsCursor: clearDeletionsCursor
          ? null
          : deletionsCursor ?? this.deletionsCursor,
      refreshing: refreshing ?? this.refreshing,
      requestingExport: requestingExport ?? this.requestingExport,
      requestingDeletion: requestingDeletion ?? this.requestingDeletion,
      downloadingId:
          clearDownloading ? null : downloadingId ?? this.downloadingId,
      notice: clearNotice ? null : notice ?? this.notice,
      error: clearError ? null : error ?? this.error,
      errorCode: clearError ? null : errorCode ?? this.errorCode,
    );
  }
}

class PrivacyController extends AsyncNotifier<PrivacyState> {
  /// 账户代次：重建后旧账户的在途响应不得写入新用户状态。
  int _accountEpoch = 0;

  /// 列表代次与翻页锁：刷新后旧游标结果必须丢弃，重复点击只加载一次。
  int _listEpoch = 0;
  bool _loadingExports = false;
  bool _loadingDeletions = false;

  @override
  Future<PrivacyState> build() async {
    ++_accountEpoch;
    ++_listEpoch;
    _loadingExports = false;
    _loadingDeletions = false;
    ref.watch(sessionEpochProvider);
    final user = ref.watch(currentUserProvider);
    if (user == null) return const PrivacyState();
    final repository = ref.watch(privacyRepositoryProvider);
    final results = await Future.wait([
      repository.fetchExports(),
      repository.fetchDeletions(),
    ]);
    final exports = results[0];
    final deletions = results[1];
    return PrivacyState(
      exports: exports.items,
      deletions: deletions.items,
      exportsCursor: exports.nextCursor,
      deletionsCursor: deletions.nextCursor,
    );
  }

  Future<void> refresh() async {
    final current = state.valueOrNull;
    if (current == null) {
      ref.invalidateSelf();
      return;
    }
    if (current.refreshing) return;
    final epoch = _accountEpoch;
    ++_listEpoch;
    state = AsyncData(
      current.copyWith(refreshing: true, clearError: true, clearNotice: true),
    );
    final repository = ref.read(privacyRepositoryProvider);
    try {
      final exports = await repository.fetchExports();
      final deletions = await repository.fetchDeletions();
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          exports: exports.items,
          deletions: deletions.items,
          exportsCursor: exports.nextCursor,
          deletionsCursor: deletions.nextCursor,
          refreshing: false,
          clearError: true,
        ),
      );
    } on ApiException catch (error) {
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          refreshing: false,
          error: error.userMessage,
          errorCode: error.code,
        ),
      );
    } catch (_) {
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(refreshing: false, error: '数据请求状态暂时无法刷新'),
      );
    }
  }

  Future<void> loadMoreExports() async {
    final current = state.valueOrNull;
    if (current == null || current.exportsCursor == null || _loadingExports) {
      return;
    }
    final epoch = _accountEpoch;
    final listEpoch = _listEpoch;
    _loadingExports = true;
    try {
      final page = await ref.read(privacyRepositoryProvider).fetchExports(
            cursor: current.exportsCursor,
          );
      if (epoch != _accountEpoch || listEpoch != _listEpoch) return;
      final latest = state.valueOrNull ?? current;
      final known = latest.exports.map((item) => item.id).toSet();
      final appended =
          page.items.where((item) => known.add(item.id)).toList(growable: false);
      state = AsyncData(
        latest.copyWith(
          exports: [...latest.exports, ...appended],
          exportsCursor: page.nextCursor,
          clearExportsCursor: page.nextCursor == null,
        ),
      );
    } catch (_) {
      if (epoch != _accountEpoch || listEpoch != _listEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(latest.copyWith(error: '更多导出记录暂时无法加载'));
    } finally {
      _loadingExports = false;
    }
  }

  Future<void> loadMoreDeletions() async {
    final current = state.valueOrNull;
    if (current == null ||
        current.deletionsCursor == null ||
        _loadingDeletions) {
      return;
    }
    final epoch = _accountEpoch;
    final listEpoch = _listEpoch;
    _loadingDeletions = true;
    try {
      final page = await ref.read(privacyRepositoryProvider).fetchDeletions(
            cursor: current.deletionsCursor,
          );
      if (epoch != _accountEpoch || listEpoch != _listEpoch) return;
      final latest = state.valueOrNull ?? current;
      final known = latest.deletions.map((item) => item.id).toSet();
      final appended =
          page.items.where((item) => known.add(item.id)).toList(growable: false);
      state = AsyncData(
        latest.copyWith(
          deletions: [...latest.deletions, ...appended],
          deletionsCursor: page.nextCursor,
          clearDeletionsCursor: page.nextCursor == null,
        ),
      );
    } catch (_) {
      if (epoch != _accountEpoch || listEpoch != _listEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(latest.copyWith(error: '更多删除申请暂时无法加载'));
    } finally {
      _loadingDeletions = false;
    }
  }

  Future<void> requestExport() async {
    final current = state.valueOrNull;
    if (current == null || current.requestingExport) return;
    final epoch = _accountEpoch;
    state = AsyncData(
      current.copyWith(
        requestingExport: true,
        clearError: true,
        clearNotice: true,
      ),
    );
    try {
      final created = await ref.read(privacyRepositoryProvider).requestExport();
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          requestingExport: false,
          exports: _upsert(latest.exports, created),
          notice:
              created.status == 'pending' ? '导出申请已提交，等待文件准备完成' : '导出申请状态已更新',
        ),
      );
    } on ApiException catch (error) {
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          requestingExport: false,
          error: error.userMessage,
          errorCode: error.code,
        ),
      );
    } catch (_) {
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(requestingExport: false, error: '导出申请未完成，请稍后重试'),
      );
    }
  }

  Future<DataRequest?> requestDeletion() async {
    final current = state.valueOrNull;
    if (current == null || current.requestingDeletion) return null;
    final epoch = _accountEpoch;
    state = AsyncData(
      current.copyWith(
        requestingDeletion: true,
        clearError: true,
        clearNotice: true,
      ),
    );
    try {
      final created =
          await ref.read(privacyRepositoryProvider).requestDeletion();
      if (epoch != _accountEpoch) return created;
      ref.read(deletionReceiptProvider.notifier).set(created);
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          requestingDeletion: false,
          deletions: _upsert(latest.deletions, created),
          notice: created.message ??
              (created.status == 'needs_review'
                  ? '删除申请已提交，资金与必要保留记录将单独核对'
                  : '删除申请已提交'),
        ),
      );
      return created;
    } on ApiException catch (error) {
      if (epoch != _accountEpoch) return null;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          requestingDeletion: false,
          error: error.userMessage,
          errorCode: error.code,
        ),
      );
      return null;
    } catch (_) {
      if (epoch != _accountEpoch) return null;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(requestingDeletion: false, error: '删除申请未完成，请稍后重试'),
      );
      return null;
    }
  }

  Future<void> download(DataRequest request) async {
    final current = state.valueOrNull;
    if (current == null || current.downloadingId != null || !request.isReady) {
      return;
    }
    final epoch = _accountEpoch;
    state = AsyncData(
      current.copyWith(
        downloadingId: request.id,
        clearError: true,
        clearNotice: true,
      ),
    );
    try {
      final download =
          await ref.read(privacyRepositoryProvider).downloadExport(request.id);
      final saved = await platform.saveExportDownload(
        filename: download.filename,
        bytes: download.bytes,
      );
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          clearDownloading: true,
          notice: saved.message,
        ),
      );
    } on ApiException catch (error) {
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          clearDownloading: true,
          error: error.userMessage,
          errorCode: error.code,
        ),
      );
    } catch (_) {
      if (epoch != _accountEpoch) return;
      final latest = state.valueOrNull ?? current;
      state = AsyncData(
        latest.copyWith(
          clearDownloading: true,
          error: '导出文件未能保存，请稍后重试',
        ),
      );
    }
  }

  void clearBanner() {
    final current = state.valueOrNull;
    if (current == null) return;
    state = AsyncData(current.copyWith(clearError: true, clearNotice: true));
  }

  List<DataRequest> _upsert(
    List<DataRequest> source,
    DataRequest value,
  ) {
    final replaced = source.where((item) => item.id != value.id).toList();
    return [value, ...replaced];
  }
}

final privacyControllerProvider =
    AsyncNotifierProvider<PrivacyController, PrivacyState>(
  PrivacyController.new,
);

class DeletionReceiptController extends Notifier<DataRequest?> {
  @override
  DataRequest? build() => null;

  void set(DataRequest request) => state = request;

  void clear() => state = null;
}

final deletionReceiptProvider =
    NotifierProvider<DeletionReceiptController, DataRequest?>(
  DeletionReceiptController.new,
);
