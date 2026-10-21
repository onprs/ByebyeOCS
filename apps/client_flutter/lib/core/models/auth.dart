import '../api/json.dart';

class SessionInfo {
  const SessionInfo({
    this.channel,
    this.expiresAt,
    this.authenticatedAt,
    this.fresh = false,
  });

  final String? channel;
  final DateTime? expiresAt;
  final DateTime? authenticatedAt;
  final bool fresh;

  bool get isAppChannel => channel == 'app';

  static SessionInfo parse(Object? raw) {
    final map = asObject(raw);
    return SessionInfo(
      channel: readString(map, 'channel'),
      expiresAt: readDateTime(map, 'expires_at'),
      authenticatedAt: readDateTime(map, 'authenticated_at'),
      fresh: readBool(map, 'fresh') ?? false,
    );
  }
}

class PlatformSessionInfo {
  const PlatformSessionInfo({
    this.state,
    this.verifiedAt,
    this.expiresAt,
  });

  final String? state;
  final DateTime? verifiedAt;
  final DateTime? expiresAt;

  bool get isActive => state == 'active';

  String get label {
    return switch (state) {
      'active' => '学习通会话有效',
      'reauthorize_required' || 'expired' => '需要重新验证学习通身份',
      null => '身份状态待确认',
      _ => '身份状态待确认',
    };
  }

  static PlatformSessionInfo parse(Object? raw) {
    final map = asObject(raw);
    return PlatformSessionInfo(
      state: readString(map, 'state'),
      verifiedAt: readDateTime(map, 'verified_at'),
      expiresAt: readDateTime(map, 'expires_at'),
    );
  }
}

class UserProfile {
  const UserProfile({
    required this.id,
    this.status = 'active',
    this.displayName,
    this.school,
    this.provider,
    this.identityVerifiedAt,
    this.session = const SessionInfo(),
    this.platform = const PlatformSessionInfo(),
    this.permissions = const <String>[],
  });

  final String id;
  final String status;
  final String? displayName;
  final String? school;
  final String? provider;
  final DateTime? identityVerifiedAt;
  final SessionInfo session;
  final PlatformSessionInfo platform;
  final List<String> permissions;

  String get displayLabel =>
      displayName == null || displayName!.isEmpty ? '学习通用户' : displayName!;

  String get schoolLabel =>
      school == null || school!.isEmpty ? '学校信息待同步' : school!;

  bool get platformActive => platform.isActive;

  static UserProfile parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return UserProfile(
      id: readString(map, 'id') ?? 'unknown',
      status: readString(map, 'status') ?? 'unknown',
      displayName: readString(map, 'display_name'),
      school: readString(map, 'school'),
      provider: readString(map, 'identity_provider'),
      identityVerifiedAt: readDateTime(map, 'identity_verified_at'),
      session: SessionInfo.parse(map['session']),
      platform: PlatformSessionInfo.parse(map['platform']),
      permissions: readStringList(map, 'permissions'),
    );
  }
}

class AuthSessionStatus {
  const AuthSessionStatus({
    required this.authenticated,
    this.kind,
    this.csrfToken,
    this.channel,
    this.expiresAt,
    this.authenticatedAt,
    this.fresh = false,
    this.permissions = const <String>[],
    this.capabilities,
  });

  final bool authenticated;
  final String? kind;
  final String? csrfToken;
  final String? channel;
  final DateTime? expiresAt;
  final DateTime? authenticatedAt;
  final bool fresh;
  final List<String> permissions;
  final Object? capabilities;

  static AuthSessionStatus parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return AuthSessionStatus(
      authenticated: readBool(map, 'authenticated') ?? false,
      kind: readString(map, 'kind'),
      csrfToken: readString(map, 'csrf_token'),
      channel: readString(map, 'channel'),
      expiresAt: readDateTime(map, 'expires_at'),
      authenticatedAt: readDateTime(map, 'authenticated_at'),
      fresh: readBool(map, 'fresh') ?? false,
      permissions: readStringList(map, 'permissions'),
      capabilities: map['capabilities'],
    );
  }
}

class AuthChallenge {
  const AuthChallenge({
    required this.id,
    this.method = 'password',
    this.status = 'pending',
    this.expiresAt,
    this.attemptsRemaining,
    this.bindingToken,
    this.capabilities,
  });

  final String id;
  final String method;
  final String status;
  final DateTime? expiresAt;
  final int? attemptsRemaining;
  final String? bindingToken;
  final Object? capabilities;

  bool get isExpired => status == 'expired';

  static AuthChallenge parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    return AuthChallenge(
      id: readString(map, 'id') ?? '',
      method: readString(map, 'method') ?? 'password',
      status: readString(map, 'status') ?? 'pending',
      expiresAt: readDateTime(map, 'expires_at'),
      attemptsRemaining: readInt(map, 'attempts_remaining'),
      bindingToken: readString(map, 'binding_token'),
      capabilities: map['capabilities'],
    );
  }
}

class LoginResult {
  const LoginResult({
    this.mode,
    this.csrfToken,
    this.accessToken,
    this.refreshToken,
    this.expiresAt,
    this.user,
    this.permissions = const <String>[],
  });

  final String? mode;
  final String? csrfToken;
  final String? accessToken;
  final String? refreshToken;
  final DateTime? expiresAt;
  final UserProfile? user;
  final List<String> permissions;

  static LoginResult parse(Object? raw) {
    final map = asObject(raw) ?? const <String, dynamic>{};
    final userMap = asObject(map['user']);
    return LoginResult(
      mode: readString(map, 'mode'),
      csrfToken: readString(map, 'csrf_token'),
      accessToken: readString(map, 'access_token'),
      refreshToken: readString(map, 'refresh_token'),
      expiresAt: readDateTime(map, 'expires_at'),
      user: userMap == null ? null : UserProfile.parse(userMap),
      permissions: readStringList(map, 'permissions'),
    );
  }
}
