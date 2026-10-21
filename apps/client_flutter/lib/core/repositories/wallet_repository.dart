import '../api/api_client.dart';
import '../models/page.dart';
import '../models/wallet.dart';

class WalletRepository {
  WalletRepository({required ApiClient api}) : _api = api;

  final ApiClient _api;

  Future<WalletSnapshot> fetchWallet() async {
    final response = await _api.get('wallet');
    return WalletSnapshot.parse(response.data);
  }

  Future<Page<WalletEntry>> fetchEntries({
    String? cursor,
    int limit = 30,
  }) async {
    final response = await _api.get('wallet/entries', query: {
      'limit': '$limit',
      if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
    });
    return parsePage<WalletEntry>(response.data, WalletEntry.parse);
  }

  Future<Page<RechargeOrder>> fetchOrders({
    String? cursor,
    int limit = 30,
  }) async {
    final response = await _api.get('recharge-orders', query: {
      'limit': '$limit',
      if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
    });
    return parsePage<RechargeOrder>(response.data, RechargeOrder.parse);
  }

  Future<RechargeOrder> fetchOrder(String id) async {
    final response = await _api.get('recharge-orders/$id');
    return RechargeOrder.parse(response.object);
  }

  Future<RechargeOrder> createOrder({
    required int amountMinor,
    required String channel,
    required String idempotencyKey,
  }) async {
    final response = await _api.post(
      'recharge-orders',
      body: {
        'amount_minor': amountMinor.toString(),
        'channel': channel,
      },
      idempotencyKey: idempotencyKey,
    );
    return RechargeOrder.parse(response.object);
  }

  Future<PricingInfo> fetchPricing() async {
    final response = await _api.get('pricing');
    return PricingInfo.parse(response.data);
  }

  Future<Page<UsageRecord>> fetchUsage({
    String? cursor,
    int limit = 30,
  }) async {
    final response = await _api.get('usage', query: {
      'limit': '$limit',
      if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
    });
    return parsePage<UsageRecord>(response.data, UsageRecord.parse);
  }
}
