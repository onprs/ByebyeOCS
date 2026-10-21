import '../api/api_client.dart';
import '../api/api_response.dart';
import '../models/auth.dart';
import '../models/capabilities.dart';
import '../models/preferences.dart';
import '../session/session_manager.dart';

class PreferencesSnapshot {
  const PreferencesSnapshot({
    required this.preferences,
    this.etag,
    this.version,
  });

  final UserPreferences preferences;
  final String? etag;
  final int? version;

  int get effectiveVersion => version ?? preferences.version;
}

class AuthRepository {
  AuthRepository({required ApiClient api, required SessionManager session})
      : _api = api,
        _session = session;

  final ApiClient _api;
  final SessionManager _session;

  Future<AuthSessionStatus> fetchSessionStatus() async {
    final response = await _api.get('auth/session', authenticated: true);
    final status = AuthSessionStatus.parse(response.data);
    await _session.acceptCsrf(status.csrfToken);
    return status;
  }

  Future<UserProfile> fetchMe() async {
    final response = await _api.get('me');
    return UserProfile.parse(response.data);
  }

  Future<Capabilities> fetchCapabilities() async {
    final response = await _api.get('capabilities', authenticated: false);
    return Capabilities.parse(response.data);
  }

  Future<PreferencesSnapshot> fetchPreferences() async {
    final response = await _api.get('me/preferences');
    return PreferencesSnapshot(
      preferences: UserPreferences.parse(response.data),
      etag: response.etag,
      version: response.headerVersion ?? response.bodyVersion,
    );
  }

  Future<PreferencesSnapshot> patchPreferences({
    required int version,
    String? colorScheme,
    String? themeMode,
    bool? taskAttention,
    bool? walletChanges,
  }) async {
    final body = <String, dynamic>{
      if (colorScheme != null) 'color_scheme': colorScheme,
      if (themeMode != null) 'theme_mode': themeMode,
      if (taskAttention != null || walletChanges != null)
        'notifications': {
          if (taskAttention != null) 'task_attention': taskAttention,
          if (walletChanges != null) 'wallet_changes': walletChanges,
        },
    };
    final response = await _api.patch(
      'me/preferences',
      body: body,
      ifMatch: formatVersionEtag(version),
    );
    return PreferencesSnapshot(
      preferences: UserPreferences.parse(response.data),
      etag: response.etag,
      version: response.headerVersion ?? response.bodyVersion,
    );
  }

  Future<AuthChallenge> createChallenge() async {
    final response = await _api.post(
      'auth/chaoxing/challenges',
      body: {
        'method': 'password',
        'client': _session.isWeb ? 'web' : 'app',
      },
      authenticated: false,
    );
    final challenge = AuthChallenge.parse(response.data);
    _session.setChallengeBinding(challenge.bindingToken);
    return challenge;
  }

  Future<AuthChallenge> fetchChallenge(String id) async {
    final headers = <String, String>{};
    if (!_session.isWeb && _session.challengeBinding != null) {
      headers['X-Challenge-Binding'] = _session.challengeBinding!;
    }
    final response = await _api.get(
      'auth/chaoxing/challenges/$id',
      authenticated: false,
      headers: headers.isEmpty ? null : headers,
    );
    return AuthChallenge.parse(response.data);
  }

  Future<LoginResult> verifyChallenge({
    required String id,
    required String school,
    required String account,
    required String password,
    String? verification,
  }) async {
    final headers = <String, String>{};
    if (!_session.isWeb && _session.challengeBinding != null) {
      headers['X-Challenge-Binding'] = _session.challengeBinding!;
    }
    final response = await _api.post(
      'auth/chaoxing/challenges/$id/verify',
      body: {
        'school': school.trim(),
        'account': account.trim(),
        'password': password,
        if (verification != null && verification.trim().isNotEmpty)
          'verification': verification.trim(),
      },
      authenticated: false,
      headers: headers.isEmpty ? null : headers,
    );
    final result = LoginResult.parse(response.data);
    await _session.acceptLogin(
      accessToken: result.accessToken,
      refreshToken: result.refreshToken,
      accessExpiresAt: result.expiresAt,
      csrf: result.csrfToken,
    );
    return result;
  }

  Future<void> logout() async {
    try {
      await _api.post(
        'auth/logout',
        body: {'platform': false},
      );
    } finally {
      await _session.clear();
    }
  }
}
