-- 仅建立内部身份与偏好结构；不创建演示用户、管理员或外部平台绑定。
CREATE TABLE public.users (
    id uuid PRIMARY KEY,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'restricted', 'deletion_requested')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE public.admin_users (
    id uuid PRIMARY KEY,
    username text NOT NULL UNIQUE CHECK (length(btrim(username)) BETWEEN 1 AND 128),
    password_hash text NOT NULL CHECK (length(password_hash) > 0),
    disabled boolean NOT NULL DEFAULT false,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE public.user_preferences (
    user_id uuid PRIMARY KEY REFERENCES public.users(id) ON DELETE RESTRICT,
    color_scheme text NOT NULL DEFAULT 'graphite' CHECK (color_scheme IN ('graphite', 'grove', 'indigo')),
    theme_mode text NOT NULL DEFAULT 'system' CHECK (theme_mode IN ('system', 'light', 'dark')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE public.admin_preferences (
    admin_id uuid PRIMARY KEY REFERENCES public.admin_users(id) ON DELETE RESTRICT,
    color_scheme text NOT NULL DEFAULT 'graphite' CHECK (color_scheme IN ('graphite', 'grove', 'indigo')),
    theme_mode text NOT NULL DEFAULT 'system' CHECK (theme_mode IN ('system', 'light', 'dark')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);
