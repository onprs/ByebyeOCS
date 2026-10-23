import '../api/api_client.dart';
import '../api/json.dart';
import '../models/page.dart';
import '../models/question.dart';
import '../models/task.dart';

class QuestionRepository {
  QuestionRepository({required ApiClient api}) : _api = api;

  final ApiClient _api;

  Future<List<QuestionOffering>> fetchOfferings() async {
    const pageSize = 100;
    // 模型目录按游标分页；用户只能在已加载的条目中选择，必须读完当前目录。
    final result = <QuestionOffering>[];
    final seen = <String>{};
    String? cursor;
    for (var page = 0; page < 5; page++) {
      final response = await _api.get('question-models', query: {
        'limit': '$pageSize',
        if (cursor != null) 'cursor': cursor,
      });
      final map = response.object;
      final raw = map['items'] ?? response.data;
      final items = asList(raw)
          .map(asObject)
          .whereType<Map<String, dynamic>>()
          .map(QuestionOffering.parse)
          .where((offering) => seen.add(offering.id))
          .toList(growable: false);
      result.addAll(items);
      final next = readString(map, 'next_cursor');
      if (next == null || next.isEmpty || next == cursor) break;
      cursor = next;
    }
    result.sort((a, b) => a.sortOrder.compareTo(b.sortOrder));
    return result;
  }

  Future<QuestionQuery> createQuery({
    required QuestionQueryRequest request,
    required String idempotencyKey,
  }) async {
    final response = await _api.post(
      'question-queries',
      body: request.toJson(),
      idempotencyKey: idempotencyKey,
    );
    return QuestionQuery.parse(response.object);
  }

  Future<QuestionQuery> fetchQuery(String id) async {
    final response = await _api.get('question-queries/$id');
    return QuestionQuery.parse(response.object);
  }

  Future<Page<QuestionQuery>> fetchQueries({
    String? cursor,
    int limit = 20,
  }) async {
    final response = await _api.get('question-queries', query: {
      'limit': '$limit',
      if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
    });
    return parsePage<QuestionQuery>(response.data, QuestionQuery.parse);
  }

  Future<TaskControlResult> reportCache({
    required String queryId,
    required String reason,
  }) async {
    final response = await _api.post(
      'question-cache-reports',
      body:
          QuestionCacheReportRequest(queryId: queryId, reason: reason).toJson(),
      idempotencyKey: newIdempotencyKey(),
    );
    final map = response.object;
    return TaskControlResult(
      taskId: readString(map, 'query_id') ?? queryId,
      state: readString(map, 'status') ?? 'submitted',
      applied: true,
    );
  }
}
