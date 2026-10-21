-- 管理员标准角色、角色管理审计与会话创建限流键。
-- 000003 已交付，本迁移只追加；不写入账号、密钥或演示数据。

-- 固定标准角色；默认 owner 保持既有 CLI 建号行为兼容。
ALTER TABLE public.admin_users
    ADD COLUMN role text NOT NULL DEFAULT 'owner'
    CHECK (role IN ('owner', 'operator', 'support', 'finance', 'observer'));

CREATE INDEX admin_users_role_idx ON public.admin_users (role, created_at) WHERE disabled = false;

-- 登录挑战记录限流用的客户端摘要键；不保存明文 IP 或其他客户端标识。
ALTER TABLE public.login_challenges
    ADD COLUMN client_key_hash text
    CHECK (client_key_hash IS NULL OR client_key_hash ~ '^[0-9a-f]{64}$');

CREATE INDEX login_challenges_client_idx ON public.login_challenges (client_key_hash, created_at DESC);
CREATE INDEX login_challenges_expiry_idx ON public.login_challenges (expires_at);
