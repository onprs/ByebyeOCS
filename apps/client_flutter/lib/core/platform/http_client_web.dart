import 'package:http/browser_client.dart';
import 'package:http/http.dart' as http;

http.Client createPlatformHttpClient() {
  final client = BrowserClient();
  // 同源部署使用 HttpOnly Cookie；跨源调试同样需要凭据。
  client.withCredentials = true;
  return client;
}
