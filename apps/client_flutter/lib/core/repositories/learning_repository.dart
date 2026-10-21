import '../api/api_client.dart';
import '../api/api_response.dart';
import '../api/json.dart';
import '../models/course.dart';
import '../models/page.dart';
import '../models/plan.dart';
import '../models/task.dart';

class LearningRepository {
  LearningRepository({required ApiClient api}) : _api = api;

  final ApiClient _api;

  Future<Page<CourseSummary>> fetchCourses({
    String? cursor,
    String search = '',
    int limit = 100,
  }) async {
    final response = await _api.get('courses', query: {
      'limit': '$limit',
      if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
      if (search.trim().isNotEmpty) 'search': search.trim(),
    });
    return parsePage<CourseSummary>(response.data, CourseSummary.parse);
  }

  Future<CourseDetail> fetchCourse(String id) async {
    final response = await _api.get('courses/$id');
    return CourseDetail.parse(response.data);
  }

  Future<CourseSyncResult> requestCourseSync() async {
    final response = await _api.post(
      'course-syncs',
      idempotencyKey: newIdempotencyKey(),
    );
    return CourseSyncResult.parse(response.data,
        created: response.statusCode == 202);
  }

  Future<Page<LearningPlan>> fetchPlans({
    String? cursor,
    int limit = 50,
  }) async {
    final response = await _api.get('plans', query: {
      'limit': '$limit',
      if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
    });
    return parsePage<LearningPlan>(response.data, LearningPlan.parse);
  }

  Future<PlanSnapshot> fetchPlan(String id) async {
    final response = await _api.get('plans/$id');
    return _snapshot(response);
  }

  Future<PlanSnapshot> createPlan(PlanDraft draft) async {
    final response = await _api.post('plans', body: draft.toJson());
    return _snapshot(response);
  }

  Future<PlanSnapshot> updatePlan({
    required String id,
    required PlanDraft draft,
    required String etag,
  }) async {
    final response = await _api.patch(
      'plans/$id',
      body: draft.toJson(),
      ifMatch: etag,
    );
    return _snapshot(response);
  }

  Future<PlanControlResult> controlPlan({
    required String id,
    required String action,
    String? reason,
  }) async {
    final response = await _api.post(
      'plans/$id/controls',
      body: {
        'action': action,
        if (reason != null && reason.trim().isNotEmpty) 'reason': reason.trim(),
      },
      idempotencyKey: newIdempotencyKey(),
    );
    return PlanControlResult.parse(response.data);
  }

  Future<Page<LearningTask>> fetchTasks({
    String? cursor,
    String? planId,
    String? courseId,
    List<String> states = const <String>[],
    int limit = 50,
  }) async {
    final response = await _api.get('tasks', query: {
      'limit': '$limit',
      if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
      if (planId != null && planId.isNotEmpty) 'plan_id': planId,
      if (courseId != null && courseId.isNotEmpty) 'course_id': courseId,
      if (states.isNotEmpty) 'state': states.join(','),
    });
    return parsePage<LearningTask>(response.data, LearningTask.parse);
  }

  Future<TaskDetail> fetchTask(String id) async {
    final response = await _api.get('tasks/$id');
    return TaskDetail.parse(response.data);
  }

  Future<TaskControlResult> controlTask({
    required String id,
    required String action,
    String? reason,
  }) async {
    final response = await _api.post(
      'tasks/$id/controls',
      body: TaskCommandRequest(action: action, reason: reason).toJson(),
      idempotencyKey: newIdempotencyKey(),
    );
    return TaskControlResult.parse(response.data);
  }

  PlanSnapshot _snapshot(ApiResponse response) {
    final plan = LearningPlan.parse(response.object);
    final version =
        response.headerVersion ?? response.bodyVersion ?? plan.version;
    final etag =
        response.etag ?? (version > 0 ? formatVersionEtag(version) : null);
    return PlanSnapshot(plan: plan, etag: etag, version: version);
  }
}
