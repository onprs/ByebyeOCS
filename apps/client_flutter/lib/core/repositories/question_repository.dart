import '../api/api_client.dart';
import '../api/json.dart';
import '../models/page.dart';
import '../models/question.dart';
import '../models/task.dart';

class QuestionRepository {
  QuestionRepository({required ApiClient api}) : _api = api;

  final ApiClient _api;

  Future<List<QuestionOffering>> fetchOfferings() async {
    final response = await _api.get('question-models');
    final map = response.object;
    final raw = map['items'] ?? response.data;
    return asList(raw)
        .map(asObject)
        .whereType<Map<String, dynamic>>()
        .map(QuestionOffering.parse)
        .toList(growable: false)
      ..sort((a, b) => a.sortOrder.compareTo(b.sortOrder));
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

  Future<TaskControlResult> submit({
    required QuestionQuery query,
    required String idempotencyKey,
  }) async {
    final answer = query.answer ?? '';
    final response = await _api.post(
      'question-submissions',
      body: QuestionSubmissionRequest(
        queryId: query.id,
        answerSha256: answerSha256(answer),
        confirmed: true,
      ).toJson(),
      idempotencyKey: idempotencyKey,
    );
    final map = response.object;
    return TaskControlResult(
      taskId: readString(map, 'query_id') ?? query.id,
      state: readString(map, 'status') ?? 'submitted',
      applied: readBool(map, 'submitted') ?? true,
    );
  }
}
