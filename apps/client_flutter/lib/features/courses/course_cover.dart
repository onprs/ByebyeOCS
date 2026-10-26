import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../app/providers.dart';
import '../../core/models/course.dart';
import '../../design/components.dart';

/// 课程封面字节缓存：合并同一地址的并发请求，并按条数与字节数淘汰。
///
/// 缓存只保存单次会话内的封面字节，身份切换时由 [courseCoverStoreProvider]
/// 整体重建，旧会话的在途响应不会写入新缓存。解码与绘制交给 Flutter 图片缓存，
/// 相同字节实例可跨页面复用已解码结果。
class CourseCoverStore {
  CourseCoverStore({
    required Future<Uint8List> Function(String path) fetch,
    this.maxEntries = 64,
    this.maxBytes = 8 * 1024 * 1024,
  }) : _fetch = fetch;

  final Future<Uint8List> Function(String path) _fetch;
  final int maxEntries;
  final int maxBytes;

  /// 插入顺序即最近使用顺序，命中时移到末尾。
  final Map<String, Uint8List> _cache = <String, Uint8List>{};
  final Map<String, Future<Uint8List>> _pending = <String, Future<Uint8List>>{};
  int _cachedBytes = 0;
  bool _disposed = false;

  int get cachedEntries => _cache.length;

  int get cachedBytes => _cachedBytes;

  /// 同一封面只发起一次请求；已有字节时直接返回缓存实例。
  Future<Uint8List> load(String path) {
    final normalized = normalizeCourseCoverPath(path);
    if (normalized == null) {
      return Future<Uint8List>.error(StateError('无效的课程封面地址'));
    }
    final cached = _cache.remove(normalized);
    if (cached != null) {
      _cache[normalized] = cached;
      return Future<Uint8List>.value(cached);
    }
    final pending = _pending[normalized];
    if (pending != null) return pending;
    final future = _download(normalized);
    _pending[normalized] = future;
    return future;
  }

  Future<Uint8List> _download(String path) async {
    try {
      final bytes = await _fetch(path);
      if (!_disposed) _store(path, bytes);
      return bytes;
    } finally {
      _pending.remove(path);
    }
  }

  void _store(String path, Uint8List bytes) {
    final previous = _cache.remove(path);
    if (previous != null) _cachedBytes -= previous.length;
    _cache[path] = bytes;
    _cachedBytes += bytes.length;
    // 至少保留最近一张，避免单张封面因预算不足反复下载。
    while (_cache.length > 1 &&
        (_cache.length > maxEntries || _cachedBytes > maxBytes)) {
      final oldest = _cache.keys.first;
      _cachedBytes -= _cache.remove(oldest)!.length;
    }
  }

  void dispose() {
    _disposed = true;
    _cache.clear();
    _pending.clear();
    _cachedBytes = 0;
  }
}

/// 身份代次变化时重建，退出或换账号后旧缓存与在途请求一并失效。
final courseCoverStoreProvider = Provider<CourseCoverStore>((ref) {
  ref.watch(sessionEpochProvider);
  final store = CourseCoverStore(
    fetch: ref.watch(learningRepositoryProvider).fetchCourseCover,
  );
  ref.onDispose(store.dispose);
  return store;
});

/// 课程封面。加载中、无来源、请求失败或图片解码失败时回落到 [CourseArt]，
/// 不展示额外文字，也不打断页面流程。
class CourseCover extends ConsumerStatefulWidget {
  const CourseCover({
    super.key,
    required this.path,
    required this.seed,
    this.height = 118,
    this.background = true,
    this.fit = BoxFit.cover,
  });

  /// 服务端给出的 API 根目录相对地址；为空表示没有可用封面。
  final String? path;
  final String seed;
  final double height;

  /// 占位图是否绘制底色，与主视觉的渐变背景配合时关闭。
  final bool background;
  final BoxFit fit;

  @override
  ConsumerState<CourseCover> createState() => _CourseCoverState();
}

class _CourseCoverState extends ConsumerState<CourseCover> {
  Uint8List? _bytes;
  CourseCoverStore? _store;
  String? _loadedPath;
  bool _failed = false;
  int _requestSeq = 0;

  void _start(CourseCoverStore store, String path) {
    final seq = ++_requestSeq;
    store.load(path).then<void>(
      (bytes) {
        if (!mounted || seq != _requestSeq) return;
        setState(() => _bytes = bytes);
      },
      onError: (Object _) {
        if (!mounted || seq != _requestSeq) return;
        setState(() => _failed = true);
      },
    );
  }

  /// 渲染前对齐当前会话与地址；旧会话响应已由请求序号丢弃。
  void _sync(CourseCoverStore store, String? path) {
    if (!identical(store, _store)) {
      _store = store;
      _loadedPath = null;
      _bytes = null;
      _failed = false;
    }
    if (path == null || path == _loadedPath) return;
    _loadedPath = path;
    _bytes = null;
    _failed = false;
    _start(store, path);
  }

  @override
  Widget build(BuildContext context) {
    final store = ref.watch(courseCoverStoreProvider);
    final path = normalizeCourseCoverPath(widget.path);
    _sync(store, path);
    final bytes = _bytes;
    return SizedBox(
      height: widget.height,
      width: double.infinity,
      child: LayoutBuilder(
        builder: (context, constraints) {
          final content = path != null && bytes != null && !_failed
              ? Image.memory(
                  bytes,
                  key: ValueKey<String>('cover-$path'),
                  height: widget.height,
                  width: double.infinity,
                  fit: widget.fit,
                  cacheWidth: _decodeWidth(context, constraints.maxWidth),
                  gaplessPlayback: true,
                  errorBuilder: (context, error, stackTrace) => _placeholder(),
                )
              : _placeholder();
          return ClipRect(
            child: AnimatedSwitcher(
              duration: const Duration(milliseconds: 220),
              child: content,
            ),
          );
        },
      ),
    );
  }

  Widget _placeholder() {
    return CourseArt(
      key: const ValueKey<String>('cover-placeholder'),
      seed: widget.seed,
      height: widget.height,
      background: widget.background,
    );
  }

  /// 解码宽度跟随展示宽度且不超过服务端缩图上限，避免原图占满内存。
  int _decodeWidth(BuildContext context, double width) {
    final dpr = MediaQuery.devicePixelRatioOf(context);
    final target = width.isFinite && width > 0 ? width * dpr : 768.0;
    return target.clamp(96.0, 768.0).round();
  }
}
