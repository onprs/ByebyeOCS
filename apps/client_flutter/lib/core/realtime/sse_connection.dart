export 'sse_connection_base.dart';
export 'sse_connection_native.dart'
    if (dart.library.js_interop) 'sse_connection_web.dart';
