-- 认证、会话、登录挑战与安全辅助结构。
-- 迁移只建立结构，不写入默认账号、演示身份或任何密钥。
-- 全部表使用 uuid 主键，时间列为 UTC timestamptz。

-- 管理员第二因素：只保存 AES-GCM 密文、密钥版本与重放计数，不保存明文密钥。
ALTER TABLE public.admin_users
    ADD COLUMN display_name text,
    ADD COLUMN password_updated_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN totp_secret_ciphertext bytea,
    ADD COLUMN totp_secret_key_version integer,
    ADD COLUMN totp_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN totp_confirmed_at timestamptz,
    ADD COLUMN totp_last_counter bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT admin_users_display_name_check CHECK (
        display_name IS NULL OR length(btrim(display_name)) BETWEEN 1 AND 128
    ),
    ADD CONSTRAINT admin_users_totp_last_counter_check CHECK (totp_last_counter >= 0),
    -- 两种稳定状态：未启用第二因素，或密文/版本/启用标记/确认时间同时具备。
    ADD CONSTRAINT admin_users_totp_material_check CHECK (
        (
            totp_secret_ciphertext IS NULL AND
            totp_secret_key_version IS NULL AND
            totp_enabled = false AND
            totp_confirmed_at IS NULL
        ) OR (
            totp_secret_ciphertext IS NOT NULL AND
            coalesce(octet_length(totp_secret_ciphertext), 0) >= 29 AND
            coalesce(totp_secret_key_version, 0) > 0 AND
            totp_enabled = true AND
            totp_confirmed_at IS NOT NULL
        )
    );

-- 通知偏好与外观偏好同表，保持双端读取模型一致；默认值与原型一致。
ALTER TABLE public.user_preferences
    ADD COLUMN notify_task_attention boolean NOT NULL DEFAULT true,
    ADD COLUMN notify_wallet_changes boolean NOT NULL DEFAULT true;

ALTER TABLE public.admin_preferences
    ADD COLUMN notify_task_attention boolean NOT NULL DEFAULT true,
    ADD COLUMN notify_wallet_changes boolean NOT NULL DEFAULT true;

-- 外部身份关联：稳定主体由平台适配器确认后写入，不把用户名当主体。
-- subject_index 为主密钥派生的 HMAC 查询索引，subject_ciphertext 为 AES-GCM 密文。
CREATE TABLE public.external_identities (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    provider text NOT NULL CHECK (provider ~ '^[a-z][a-z0-9_]{1,31}$'),
    namespace text NOT NULL CHECK (length(btrim(namespace)) BETWEEN 1 AND 190),
    subject_index text NOT NULL CHECK (subject_index ~ '^[0-9a-f]{64}$'),
    subject_ciphertext bytea NOT NULL CHECK (octet_length(subject_ciphertext) >= 29),
    subject_key_version integer NOT NULL CHECK (subject_key_version > 0),
    display_name text,
    school text,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    verified_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    CONSTRAINT external_identities_display_name_check CHECK (
        display_name IS NULL OR length(btrim(display_name)) BETWEEN 1 AND 128
    ),
    CONSTRAINT external_identities_school_check CHECK (
        school IS NULL OR length(btrim(school)) BETWEEN 1 AND 190
    ),
    CONSTRAINT external_identities_revoked_check CHECK (
        (status = 'active' AND revoked_at IS NULL) OR (status = 'revoked' AND revoked_at IS NOT NULL)
    ),
    -- 一个已确认主体只归属一个内部用户；并发首次登录靠该唯一约束收敛。
    CONSTRAINT external_identities_subject_key UNIQUE (provider, namespace, subject_index)
);

CREATE INDEX external_identities_user_idx ON public.external_identities (user_id, status);
-- MVP 一个内部用户只保留一个有效外部身份主体。
CREATE UNIQUE INDEX external_identities_active_owner_idx
    ON public.external_identities (user_id, provider)
    WHERE status = 'active';

-- 平台会话：只保存加密凭据材料，仅供服务间解密，不通过 HTTP 返回。
CREATE TABLE public.platform_sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    external_identity_id uuid NOT NULL REFERENCES public.external_identities(id) ON DELETE RESTRICT,
    key_version integer NOT NULL CHECK (key_version > 0),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) >= 29),
    expires_at timestamptz NOT NULL,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked', 'expired')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at timestamptz,
    revoke_reason text CHECK (revoke_reason IS NULL OR length(btrim(revoke_reason)) BETWEEN 1 AND 64),
    CONSTRAINT platform_sessions_expiry_check CHECK (expires_at > created_at)
);

CREATE INDEX platform_sessions_active_idx
    ON public.platform_sessions (user_id, expires_at DESC)
    WHERE status = 'active';
CREATE INDEX platform_sessions_identity_idx ON public.platform_sessions (external_identity_id);

-- 用户端会话：Web Cookie 与 App 访问令牌共用表，channel 区分，只存令牌摘要。
CREATE TABLE public.client_sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    platform_session_id uuid REFERENCES public.platform_sessions(id) ON DELETE SET NULL,
    channel text NOT NULL CHECK (channel IN ('web', 'app')),
    token_hash text NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    csrf_hash text NOT NULL CHECK (csrf_hash ~ '^[0-9a-f]{64}$'),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked', 'expired')),
    authenticated_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    rotated_from uuid REFERENCES public.client_sessions(id) ON DELETE SET NULL,
    revoked_at timestamptz,
    revoke_reason text CHECK (revoke_reason IS NULL OR length(btrim(revoke_reason)) BETWEEN 1 AND 64),
    client_label text CHECK (client_label IS NULL OR length(btrim(client_label)) BETWEEN 1 AND 64),
    CONSTRAINT client_sessions_expiry_check CHECK (
        expires_at > created_at AND absolute_expires_at >= expires_at
    ),
    CONSTRAINT client_sessions_revoked_check CHECK (
        (status = 'active' AND revoked_at IS NULL) OR (status <> 'active' AND revoked_at IS NOT NULL)
    )
);

CREATE INDEX client_sessions_user_idx ON public.client_sessions (user_id, status);
CREATE INDEX client_sessions_expiry_idx ON public.client_sessions (status, expires_at);
CREATE INDEX client_sessions_platform_idx ON public.client_sessions (platform_session_id);

-- 管理员会话：与用户会话分表，避免任一端的 Cookie 被另一端接受。
CREATE TABLE public.admin_sessions (
    id uuid PRIMARY KEY,
    admin_id uuid NOT NULL REFERENCES public.admin_users(id) ON DELETE RESTRICT,
    token_hash text NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    csrf_hash text NOT NULL CHECK (csrf_hash ~ '^[0-9a-f]{64}$'),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked', 'expired')),
    authenticated_at timestamptz NOT NULL,
    step_up_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    rotated_from uuid REFERENCES public.admin_sessions(id) ON DELETE SET NULL,
    revoked_at timestamptz,
    revoke_reason text CHECK (revoke_reason IS NULL OR length(btrim(revoke_reason)) BETWEEN 1 AND 64),
    CONSTRAINT admin_sessions_expiry_check CHECK (
        expires_at > created_at AND absolute_expires_at >= expires_at
    ),
    CONSTRAINT admin_sessions_revoked_check CHECK (
        (status = 'active' AND revoked_at IS NULL) OR (status <> 'active' AND revoked_at IS NOT NULL)
    )
);

CREATE INDEX admin_sessions_admin_idx ON public.admin_sessions (admin_id, status);
CREATE INDEX admin_sessions_expiry_idx ON public.admin_sessions (status, expires_at);

-- App 刷新令牌链：只存摘要，轮换后旧记录保留用于复用检测。
CREATE TABLE public.refresh_tokens (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES public.client_sessions(id) ON DELETE CASCADE,
    family_id uuid NOT NULL,
    token_hash text NOT NULL UNIQUE CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    generation integer NOT NULL CHECK (generation > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    revoked_at timestamptz,
    revoke_reason text CHECK (revoke_reason IS NULL OR length(btrim(revoke_reason)) BETWEEN 1 AND 64),
    CONSTRAINT refresh_tokens_expiry_check CHECK (expires_at > created_at)
);

CREATE INDEX refresh_tokens_family_idx ON public.refresh_tokens (family_id, generation);
CREATE INDEX refresh_tokens_session_idx ON public.refresh_tokens (session_id);

-- 登录挑战：绑定本次客户端、有到期时间与尝试上限，单次使用。
CREATE TABLE public.login_challenges (
    id uuid PRIMARY KEY,
    method text NOT NULL CHECK (method IN ('password')),
    client_channel text NOT NULL CHECK (client_channel IN ('web', 'app')),
    binding_hash text NOT NULL CHECK (binding_hash ~ '^[0-9a-f]{64}$'),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'verified', 'consumed', 'failed', 'expired')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 10),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    verified_at timestamptz,
    consumed_at timestamptz,
    user_id uuid REFERENCES public.users(id) ON DELETE RESTRICT,
    external_identity_id uuid REFERENCES public.external_identities(id) ON DELETE RESTRICT,
    CONSTRAINT login_challenges_expiry_check CHECK (expires_at > created_at),
    CONSTRAINT login_challenges_attempt_limit_check CHECK (attempts <= max_attempts),
    CONSTRAINT login_challenges_binding_owner_check CHECK (
        (user_id IS NULL AND external_identity_id IS NULL) OR (user_id IS NOT NULL AND external_identity_id IS NOT NULL)
    )
);

CREATE INDEX login_challenges_pending_idx ON public.login_challenges (status, expires_at);

-- 持久化登录限流：按范围与主体摘要累计失败，窗口与封禁时间由服务端策略决定。
CREATE TABLE public.auth_rate_limits (
    id uuid PRIMARY KEY,
    scope text NOT NULL CHECK (scope IN ('admin_login', 'user_login')),
    subject_hash text NOT NULL CHECK (subject_hash ~ '^[0-9a-f]{64}$'),
    window_started_at timestamptz NOT NULL,
    failures integer NOT NULL DEFAULT 0 CHECK (failures >= 0),
    blocked_until timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT auth_rate_limits_subject_key UNIQUE (scope, subject_hash)
);

CREATE INDEX auth_rate_limits_updated_idx ON public.auth_rate_limits (updated_at);
