-- 账本、钱包、价格、用量、充值订单、ZPay 支付事实、对账与异常退款结构。
-- 所有资金使用人民币整数分，题库 token 价格使用整数微元；不使用浮点金额。
-- 账本为不可变 journal + 平衡 postings，钱包可用/预占字段只是事务内维护的缓存。
-- 本迁移不创建真实用户、价格、订单或支付凭据，也不开启任何真实收费。

-- ---------------------------------------------------------------------------
-- 钱包与账本账户
-- ---------------------------------------------------------------------------

CREATE TABLE public.wallets (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL UNIQUE REFERENCES public.users(id) ON DELETE RESTRICT,
    currency text NOT NULL DEFAULT 'CNY' CHECK (currency = 'CNY'),
    available_minor bigint NOT NULL DEFAULT 0 CHECK (available_minor >= 0),
    reserved_minor bigint NOT NULL DEFAULT 0 CHECK (reserved_minor >= 0),
    -- 题库未达结算门槛的微元尾差；只能由题库结算事务按价格规则维护。
    tiku_accrued_micros bigint NOT NULL DEFAULT 0 CHECK (tiku_accrued_micros >= 0),
    frozen boolean NOT NULL DEFAULT false,
    frozen_reason text CHECK (frozen_reason IS NULL OR length(btrim(frozen_reason)) BETWEEN 1 AND 200),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT wallets_frozen_state_check CHECK (
        (frozen = false AND frozen_reason IS NULL) OR (frozen = true AND frozen_reason IS NOT NULL)
    )
);

CREATE INDEX wallets_frozen_idx ON public.wallets (frozen) WHERE frozen = true;

-- 账户编码在写入时由服务端确定；用户账户必须挂在钱包上，系统账户全局唯一。
CREATE TABLE public.ledger_accounts (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE CHECK (length(btrim(code)) BETWEEN 1 AND 160),
    kind text NOT NULL CHECK (kind IN (
        'user_available', 'user_reserved', 'channel_clearing',
        'service_revenue', 'refund_payable', 'platform_adjustment'
    )),
    wallet_id uuid REFERENCES public.wallets(id) ON DELETE RESTRICT,
    user_id uuid REFERENCES public.users(id) ON DELETE RESTRICT,
    currency text NOT NULL DEFAULT 'CNY' CHECK (currency = 'CNY'),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ledger_accounts_owner_check CHECK (
        (kind IN ('user_available', 'user_reserved') AND wallet_id IS NOT NULL AND user_id IS NOT NULL)
        OR
        (kind IN ('channel_clearing', 'service_revenue', 'refund_payable', 'platform_adjustment')
            AND wallet_id IS NULL AND user_id IS NULL)
    )
);

CREATE UNIQUE INDEX ledger_accounts_wallet_kind_idx
    ON public.ledger_accounts (wallet_id, kind) WHERE wallet_id IS NOT NULL;
CREATE UNIQUE INDEX ledger_accounts_system_kind_idx
    ON public.ledger_accounts (kind, currency) WHERE wallet_id IS NULL;

-- 钱包创建后指向自己的两个用户账户。系统账户使用固定 UUID，不依赖运行时插入顺序。
ALTER TABLE public.wallets
    ADD COLUMN available_account_id uuid REFERENCES public.ledger_accounts(id) ON DELETE RESTRICT,
    ADD COLUMN reserved_account_id uuid REFERENCES public.ledger_accounts(id) ON DELETE RESTRICT,
    ADD CONSTRAINT wallets_ledger_accounts_check CHECK (
        (available_account_id IS NULL AND reserved_account_id IS NULL)
        OR (available_account_id IS NOT NULL AND reserved_account_id IS NOT NULL)
    );

INSERT INTO public.ledger_accounts (id, code, kind, currency) VALUES
    ('00000000-0000-4000-8000-000005000001', 'system:cny:channel_clearing', 'channel_clearing', 'CNY'),
    ('00000000-0000-4000-8000-000005000002', 'system:cny:service_revenue', 'service_revenue', 'CNY'),
    ('00000000-0000-4000-8000-000005000003', 'system:cny:refund_payable', 'refund_payable', 'CNY'),
    ('00000000-0000-4000-8000-000005000004', 'system:cny:platform_adjustment', 'platform_adjustment', 'CNY')
ON CONFLICT (id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 价格版本与计费设置
-- ---------------------------------------------------------------------------

CREATE TABLE public.price_versions (
    id uuid PRIMARY KEY,
    scope text NOT NULL CHECK (scope IN ('service_task_point', 'tiku_tokens')),
    version bigint NOT NULL CHECK (version > 0),
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'retired')),
    currency text NOT NULL DEFAULT 'CNY' CHECK (currency = 'CNY'),
    unit_label text NOT NULL CHECK (length(btrim(unit_label)) BETWEEN 1 AND 80),
    -- 服务用量以 10^-unit_precision 个任务点为单位；题库固定为 0。
    unit_precision smallint NOT NULL DEFAULT 0 CHECK (unit_precision BETWEEN 0 AND 6),
    price_micros bigint CHECK (price_micros IS NULL OR price_micros >= 0),
    input_micros_per_million bigint CHECK (
        input_micros_per_million IS NULL OR input_micros_per_million >= 0
    ),
    output_micros_per_million bigint CHECK (
        output_micros_per_million IS NULL OR output_micros_per_million >= 0
    ),
    settlement_rule text NOT NULL CHECK (settlement_rule IN (
        'per_event', 'per_event_min_charge', 'accumulate_threshold'
    )),
    rounding text NOT NULL DEFAULT 'floor' CHECK (rounding IN ('floor', 'half_up')),
    min_charge_minor bigint NOT NULL DEFAULT 0 CHECK (min_charge_minor >= 0),
    -- 题库只有累计到该微元数才产生 1 分钱账单；1 分 = 10000 微元。
    settlement_threshold_micros bigint NOT NULL DEFAULT 10000
        CHECK (settlement_threshold_micros >= 10000),
    published_at timestamptz,
    published_by uuid REFERENCES public.admin_users(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT price_versions_scope_version_uq UNIQUE (scope, version),
    CONSTRAINT price_versions_shape_check CHECK (
        (scope = 'service_task_point'
            AND price_micros IS NOT NULL
            AND input_micros_per_million IS NULL
            AND output_micros_per_million IS NULL
            AND settlement_rule IN ('per_event', 'per_event_min_charge'))
        OR
        (scope = 'tiku_tokens'
            AND price_micros IS NULL
            AND input_micros_per_million IS NOT NULL
            AND output_micros_per_million IS NOT NULL
            AND settlement_rule = 'accumulate_threshold'
            AND unit_precision = 0
            AND min_charge_minor = 0)
    ),
    CONSTRAINT price_versions_published_check CHECK (
        (status = 'draft' AND published_at IS NULL AND published_by IS NULL)
        OR (status IN ('published', 'retired') AND published_at IS NOT NULL AND published_by IS NOT NULL)
    )
);

CREATE UNIQUE INDEX price_versions_one_published_idx
    ON public.price_versions (scope) WHERE status = 'published';
CREATE INDEX price_versions_scope_version_idx
    ON public.price_versions (scope, version DESC);

-- 同一个全局设置行保存收费开关与充值限制；价格本身仍保存在不可变价格版本中。
CREATE TABLE public.billing_settings (
    id boolean PRIMARY KEY DEFAULT true CHECK (id = true),
    service_charge_enabled boolean NOT NULL DEFAULT false,
    tiku_charge_enabled boolean NOT NULL DEFAULT false,
    zpay_checkout_enabled boolean NOT NULL DEFAULT false,
    zpay_refund_enabled boolean NOT NULL DEFAULT false,
    default_channel text NOT NULL DEFAULT 'alipay' CHECK (default_channel IN ('alipay', 'wxpay')),
    min_recharge_minor bigint NOT NULL DEFAULT 100 CHECK (min_recharge_minor > 0),
    max_recharge_minor bigint NOT NULL DEFAULT 100000 CHECK (max_recharge_minor >= 100),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES public.admin_users(id) ON DELETE RESTRICT,
    CONSTRAINT billing_settings_recharge_range_check CHECK (min_recharge_minor <= max_recharge_minor)
);

INSERT INTO public.billing_settings (id) VALUES (true);

-- 价格版本一经写入，价格、单位和结算规则不可修改；只允许发布和停用状态转换。
CREATE FUNCTION public.protect_price_version() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.scope IS DISTINCT FROM OLD.scope
        OR NEW.version IS DISTINCT FROM OLD.version
        OR NEW.currency IS DISTINCT FROM OLD.currency
        OR NEW.unit_label IS DISTINCT FROM OLD.unit_label
        OR NEW.unit_precision IS DISTINCT FROM OLD.unit_precision
        OR NEW.price_micros IS DISTINCT FROM OLD.price_micros
        OR NEW.input_micros_per_million IS DISTINCT FROM OLD.input_micros_per_million
        OR NEW.output_micros_per_million IS DISTINCT FROM OLD.output_micros_per_million
        OR NEW.settlement_rule IS DISTINCT FROM OLD.settlement_rule
        OR NEW.rounding IS DISTINCT FROM OLD.rounding
        OR NEW.min_charge_minor IS DISTINCT FROM OLD.min_charge_minor
        OR NEW.settlement_threshold_micros IS DISTINCT FROM OLD.settlement_threshold_micros
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION 'price version is immutable';
    END IF;
    -- 只允许首次由 draft 发布时填充 published_at/by，之后不可再改。
    IF NEW.published_at IS DISTINCT FROM OLD.published_at
        OR NEW.published_by IS DISTINCT FROM OLD.published_by
    THEN
        IF NOT (
            OLD.status = 'draft' AND NEW.status = 'published'
            AND OLD.published_at IS NULL AND OLD.published_by IS NULL
            AND NEW.published_at IS NOT NULL AND NEW.published_by IS NOT NULL
        ) THEN
            RAISE EXCEPTION 'price version publication fields are immutable';
        END IF;
    END IF;
    IF OLD.status = 'retired' AND NEW.status <> 'retired' THEN
        RAISE EXCEPTION 'retired price version cannot be reactivated';
    END IF;
    IF OLD.status = 'draft' AND NEW.status NOT IN ('draft', 'published') THEN
        RAISE EXCEPTION 'invalid price version transition';
    END IF;
    IF OLD.status = 'published' AND NEW.status NOT IN ('published', 'retired') THEN
        RAISE EXCEPTION 'invalid price version transition';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER price_versions_immutable
BEFORE UPDATE ON public.price_versions
FOR EACH ROW EXECUTE FUNCTION public.protect_price_version();

CREATE FUNCTION public.reject_price_version_delete() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'price versions are append only';
END;
$$;

CREATE TRIGGER price_versions_no_delete
BEFORE DELETE ON public.price_versions
FOR EACH ROW EXECUTE FUNCTION public.reject_price_version_delete();
CREATE TRIGGER price_versions_no_truncate
BEFORE TRUNCATE ON public.price_versions
FOR EACH STATEMENT EXECUTE FUNCTION public.reject_price_version_delete();

-- ---------------------------------------------------------------------------
-- 充值订单与支付尝试
-- ---------------------------------------------------------------------------

CREATE TABLE public.recharge_orders (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    wallet_id uuid NOT NULL REFERENCES public.wallets(id) ON DELETE RESTRICT,
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    currency text NOT NULL DEFAULT 'CNY' CHECK (currency = 'CNY'),
    channel text NOT NULL CHECK (channel IN ('alipay', 'wxpay')),
    -- 本地生命周期与渠道支付、账务到账分开保存，不能用 return_url 直接改写。
    status text NOT NULL DEFAULT 'pending_payment'
        CHECK (status IN ('pending_payment', 'completed', 'closed', 'needs_review')),
    payment_status text NOT NULL DEFAULT 'unpaid'
        CHECK (payment_status IN ('unpaid', 'paid', 'failed', 'unknown', 'refunded')),
    credit_status text NOT NULL DEFAULT 'not_credited'
        CHECK (credit_status IN ('not_credited', 'credited', 'needs_review')),
    provider text NOT NULL DEFAULT 'zpay' CHECK (provider = 'zpay'),
    merchant_order_no text NOT NULL UNIQUE CHECK (merchant_order_no ~ '^[0-9]{1,32}$'),
    provider_trade_no text CHECK (
        provider_trade_no IS NULL OR (length(provider_trade_no) BETWEEN 1 AND 64
            AND provider_trade_no ~ '^[A-Za-z0-9_.:-]+$')
    ),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    late_payment boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    paid_at timestamptz,
    credited_at timestamptz,
    closed_at timestamptz,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    CONSTRAINT recharge_orders_idempotency_uq UNIQUE (user_id, idempotency_key),
    CONSTRAINT recharge_orders_expiry_check CHECK (expires_at > created_at),
    CONSTRAINT recharge_orders_paid_at_check CHECK (
        (payment_status IN ('unpaid', 'failed', 'unknown') AND paid_at IS NULL)
        OR (payment_status IN ('paid', 'refunded') AND paid_at IS NOT NULL)
    ),
    CONSTRAINT recharge_orders_credited_at_check CHECK (
        (credit_status = 'credited' AND credited_at IS NOT NULL)
        OR (credit_status <> 'credited' AND credited_at IS NULL)
    )
);

CREATE UNIQUE INDEX recharge_orders_provider_trade_idx
    ON public.recharge_orders (provider, provider_trade_no) WHERE provider_trade_no IS NOT NULL;
CREATE INDEX recharge_orders_user_created_idx
    ON public.recharge_orders (user_id, created_at DESC, id DESC);
CREATE INDEX recharge_orders_status_idx
    ON public.recharge_orders (status, created_at DESC, id DESC);
CREATE INDEX recharge_orders_credit_pending_idx
    ON public.recharge_orders (payment_status, credit_status, created_at)
    WHERE payment_status = 'paid' AND credit_status <> 'credited';

CREATE TABLE public.payment_attempts (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL UNIQUE REFERENCES public.recharge_orders(id) ON DELETE RESTRICT,
    provider text NOT NULL DEFAULT 'zpay' CHECK (provider = 'zpay'),
    provider_order_id text CHECK (
        provider_order_id IS NULL OR length(provider_order_id) BETWEEN 1 AND 64
    ),
    provider_trade_no text CHECK (
        provider_trade_no IS NULL OR length(provider_trade_no) BETWEEN 1 AND 64
    ),
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    channel text NOT NULL CHECK (channel IN ('alipay', 'wxpay')),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'paid', 'failed', 'unknown', 'expired')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX payment_attempts_provider_trade_idx
    ON public.payment_attempts (provider, provider_trade_no) WHERE provider_trade_no IS NOT NULL;

-- 支付通知先持久化最小事实，绝不保存原始 payload、签名或商户密钥。
CREATE TABLE public.payment_inbox (
    id uuid PRIMARY KEY,
    provider text NOT NULL CHECK (provider = 'zpay'),
    source text NOT NULL DEFAULT 'callback' CHECK (source IN ('callback', 'query')),
    dedupe_key text NOT NULL UNIQUE CHECK (dedupe_key ~ '^[0-9a-f]{64}$'),
    payload_digest text NOT NULL CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
    order_id uuid REFERENCES public.recharge_orders(id) ON DELETE RESTRICT,
    merchant_order_no text NOT NULL CHECK (length(merchant_order_no) BETWEEN 1 AND 32),
    provider_trade_no text NOT NULL CHECK (length(provider_trade_no) BETWEEN 1 AND 64),
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    channel text NOT NULL CHECK (length(channel) BETWEEN 1 AND 32),
    provider_status text NOT NULL CHECK (provider_status ~ '^[A-Z_]{1,32}$'),
    status text NOT NULL DEFAULT 'received'
        CHECK (status IN ('received', 'processed', 'ignored', 'needs_review')),
    verified boolean NOT NULL DEFAULT true,
    reason_code text CHECK (reason_code IS NULL OR length(reason_code) BETWEEN 1 AND 64),
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz
);

CREATE INDEX payment_inbox_order_idx ON public.payment_inbox (order_id, received_at DESC);
CREATE INDEX payment_inbox_status_idx ON public.payment_inbox (status, received_at);
CREATE INDEX payment_inbox_trade_idx ON public.payment_inbox (provider, provider_trade_no);

CREATE FUNCTION public.reject_payment_inbox_delete() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'payment inbox is append only';
END;
$$;

CREATE TRIGGER payment_inbox_no_delete
BEFORE DELETE ON public.payment_inbox
FOR EACH ROW EXECUTE FUNCTION public.reject_payment_inbox_delete();
CREATE TRIGGER payment_inbox_no_truncate
BEFORE TRUNCATE ON public.payment_inbox
FOR EACH STATEMENT EXECUTE FUNCTION public.reject_payment_inbox_delete();

-- ---------------------------------------------------------------------------
-- 预占、不可变分录与余额缓存
-- ---------------------------------------------------------------------------

CREATE TABLE public.holds (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    wallet_id uuid NOT NULL REFERENCES public.wallets(id) ON DELETE RESTRICT,
    business_key text NOT NULL UNIQUE CHECK (length(btrim(business_key)) BETWEEN 1 AND 200),
    request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    settled_minor bigint NOT NULL DEFAULT 0 CHECK (settled_minor >= 0),
    released_minor bigint NOT NULL DEFAULT 0 CHECK (released_minor >= 0),
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'settled', 'released')),
    source_type text NOT NULL DEFAULT 'other'
        CHECK (source_type IN ('service_task_point', 'tiku_query', 'other')),
    source_id text CHECK (source_id IS NULL OR length(source_id) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    CONSTRAINT holds_amount_check CHECK (settled_minor + released_minor <= amount_minor),
    CONSTRAINT holds_state_check CHECK (
        (status = 'open' AND settled_minor + released_minor < amount_minor)
        OR (status = 'settled' AND settled_minor = amount_minor AND released_minor = 0)
        OR (status = 'released' AND settled_minor + released_minor = amount_minor AND released_minor > 0)
    )
);

CREATE INDEX holds_user_status_idx ON public.holds (user_id, status, created_at DESC);
CREATE INDEX holds_wallet_idx ON public.holds (wallet_id, status);

CREATE TABLE public.journals (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN (
        'recharge', 'hold', 'capture', 'release',
        'manual_credit', 'manual_debit',
        'refund_reserve', 'refund_payout', 'refund_return'
    )),
    business_key text NOT NULL UNIQUE CHECK (length(btrim(business_key)) BETWEEN 1 AND 200),
    request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    user_id uuid REFERENCES public.users(id) ON DELETE RESTRICT,
    wallet_id uuid REFERENCES public.wallets(id) ON DELETE RESTRICT,
    currency text NOT NULL DEFAULT 'CNY' CHECK (currency = 'CNY'),
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 500),
    actor_kind text NOT NULL DEFAULT 'system' CHECK (actor_kind IN ('user', 'admin', 'system')),
    actor_id uuid,
    reference_type text CHECK (reference_type IS NULL OR length(reference_type) BETWEEN 1 AND 64),
    reference_id text CHECK (reference_id IS NULL OR length(reference_id) BETWEEN 1 AND 200),
    order_id uuid REFERENCES public.recharge_orders(id) ON DELETE RESTRICT,
    hold_id uuid REFERENCES public.holds(id) ON DELETE RESTRICT,
    price_version_id uuid REFERENCES public.price_versions(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT journals_actor_check CHECK (
        (actor_kind = 'admin' AND actor_id IS NOT NULL)
        OR (actor_kind = 'user' AND actor_id IS NOT NULL)
        OR (actor_kind = 'system')
    )
);

CREATE INDEX journals_user_created_idx ON public.journals (user_id, created_at DESC, id DESC);
CREATE INDEX journals_wallet_created_idx ON public.journals (wallet_id, created_at DESC, id DESC);
CREATE INDEX journals_order_idx ON public.journals (order_id) WHERE order_id IS NOT NULL;
CREATE INDEX journals_hold_idx ON public.journals (hold_id) WHERE hold_id IS NOT NULL;

CREATE TABLE public.postings (
    id uuid PRIMARY KEY,
    journal_id uuid NOT NULL REFERENCES public.journals(id) ON DELETE RESTRICT,
    account_id uuid NOT NULL REFERENCES public.ledger_accounts(id) ON DELETE RESTRICT,
    amount_minor bigint NOT NULL CHECK (amount_minor <> 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT postings_journal_account_uq UNIQUE (journal_id, account_id),
    -- 单项分录绝对值必须能被 bigint 安全累加；真实金额远小于此上限。
    CONSTRAINT postings_amount_bound CHECK (amount_minor BETWEEN -9007199254740992 AND 9007199254740992)
);

CREATE INDEX postings_journal_idx ON public.postings (journal_id);
CREATE INDEX postings_account_created_idx ON public.postings (account_id, created_at);

CREATE FUNCTION public.reject_journal_mutation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'journals and postings are append only';
END;
$$;

CREATE TRIGGER journals_no_update
BEFORE UPDATE ON public.journals
FOR EACH ROW EXECUTE FUNCTION public.reject_journal_mutation();
CREATE TRIGGER journals_no_delete
BEFORE DELETE ON public.journals
FOR EACH ROW EXECUTE FUNCTION public.reject_journal_mutation();
CREATE TRIGGER journals_no_truncate
BEFORE TRUNCATE ON public.journals
FOR EACH STATEMENT EXECUTE FUNCTION public.reject_journal_mutation();
CREATE TRIGGER postings_no_update
BEFORE UPDATE ON public.postings
FOR EACH ROW EXECUTE FUNCTION public.reject_journal_mutation();
CREATE TRIGGER postings_no_delete
BEFORE DELETE ON public.postings
FOR EACH ROW EXECUTE FUNCTION public.reject_journal_mutation();
CREATE TRIGGER postings_no_truncate
BEFORE TRUNCATE ON public.postings
FOR EACH STATEMENT EXECUTE FUNCTION public.reject_journal_mutation();

-- 每个 journal 至少两条分录，正数合计等于 journal.amount_minor，总和为零。
CREATE FUNCTION public.check_journal_balance() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE
    total numeric;
    positive numeric;
    counted integer;
    expected bigint;
BEGIN
    SELECT coalesce(sum(amount_minor), 0),
           coalesce(sum(amount_minor) FILTER (WHERE amount_minor > 0), 0),
           count(*)
      INTO total, positive, counted
      FROM public.postings
     WHERE journal_id = NEW.journal_id;
    SELECT amount_minor INTO expected FROM public.journals WHERE id = NEW.journal_id;
    IF counted < 2 OR total <> 0 OR positive <> expected THEN
        RAISE EXCEPTION 'journal % is not balanced', NEW.journal_id;
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER postings_balanced
AFTER INSERT ON public.postings
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_journal_balance();

CREATE FUNCTION public.check_journal_has_postings() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE
    counted integer;
BEGIN
    SELECT count(*) INTO counted FROM public.postings WHERE journal_id = NEW.id;
    IF counted < 2 THEN
        RAISE EXCEPTION 'journal % has no balanced postings', NEW.id;
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER journals_posted
AFTER INSERT ON public.journals
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.check_journal_has_postings();

-- 零金额结算/释放不产生 journal，仍需保存业务键摘要以拒绝同键不同负载。
CREATE TABLE public.ledger_request_keys (
    business_key text PRIMARY KEY CHECK (length(btrim(business_key)) BETWEEN 1 AND 200),
    request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    kind text NOT NULL CHECK (length(btrim(kind)) BETWEEN 1 AND 64),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- 用量事实（主服务任务点与题库 token 分开）
-- ---------------------------------------------------------------------------

CREATE TABLE public.usage_records (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    scope text NOT NULL CHECK (scope IN ('service_task_point', 'tiku_tokens')),
    stable_key text NOT NULL CHECK (length(btrim(stable_key)) BETWEEN 1 AND 200),
    source_type text CHECK (source_type IS NULL OR length(source_type) BETWEEN 1 AND 64),
    source_id text CHECK (source_id IS NULL OR length(source_id) BETWEEN 1 AND 200),
    status text NOT NULL DEFAULT 'quoted' CHECK (status IN (
        'quoted', 'reserved', 'settled', 'released',
        'unpriced', 'disabled', 'needs_review', 'failed'
    )),
    request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    quantity_scaled bigint NOT NULL DEFAULT 0 CHECK (quantity_scaled >= 0),
    input_tokens bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    total_tokens bigint NOT NULL DEFAULT 0 CHECK (total_tokens >= 0),
    usage_complete boolean NOT NULL DEFAULT false,
    platform_confirmed_at timestamptz,
    platform_evidence_ref text CHECK (
        platform_evidence_ref IS NULL OR length(platform_evidence_ref) BETWEEN 1 AND 200
    ),
    price_version_id uuid REFERENCES public.price_versions(id) ON DELETE RESTRICT,
    quote_micros bigint CHECK (quote_micros IS NULL OR quote_micros >= 0),
    quote_minor bigint CHECK (quote_minor IS NULL OR quote_minor >= 0),
    cost_micros bigint CHECK (cost_micros IS NULL OR cost_micros >= 0),
    charged_minor bigint CHECK (charged_minor IS NULL OR charged_minor >= 0),
    settlement_threshold_micros bigint CHECK (
        settlement_threshold_micros IS NULL OR settlement_threshold_micros >= 10000
    ),
    hold_id uuid REFERENCES public.holds(id) ON DELETE RESTRICT,
    settle_journal_id uuid REFERENCES public.journals(id) ON DELETE RESTRICT,
    release_journal_id uuid REFERENCES public.journals(id) ON DELETE RESTRICT,
    last_error_code text CHECK (last_error_code IS NULL OR length(last_error_code) BETWEEN 1 AND 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    settled_at timestamptz,
    CONSTRAINT usage_records_stable_uq UNIQUE (scope, user_id, stable_key),
    CONSTRAINT usage_records_tokens_check CHECK (total_tokens = input_tokens + output_tokens),
    CONSTRAINT usage_records_shape_check CHECK (
        (scope = 'service_task_point' AND input_tokens = 0 AND output_tokens = 0 AND total_tokens = 0
            AND quantity_scaled > 0)
        OR
        (scope = 'tiku_tokens' AND quantity_scaled = 0)
    )
);

CREATE INDEX usage_records_user_created_idx
    ON public.usage_records (user_id, created_at DESC, id DESC);
CREATE INDEX usage_records_status_idx ON public.usage_records (status, created_at);
CREATE INDEX usage_records_price_idx
    ON public.usage_records (price_version_id) WHERE price_version_id IS NOT NULL;

ALTER TABLE public.journals
    ADD COLUMN usage_id uuid REFERENCES public.usage_records(id) ON DELETE RESTRICT;
CREATE INDEX journals_usage_idx ON public.journals (usage_id) WHERE usage_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 对账与异常退款
-- ---------------------------------------------------------------------------

CREATE TABLE public.reconciliations (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN (
        'wallet_rebuild', 'payment_orders', 'payment_inbox', 'wallet_unfreeze'
    )),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN (
        'pending', 'running', 'matched', 'mismatch', 'incomplete', 'failed'
    )),
    target_user_id uuid REFERENCES public.users(id) ON DELETE RESTRICT,
    target_wallet_id uuid REFERENCES public.wallets(id) ON DELETE RESTRICT,
    target_order_id uuid REFERENCES public.recharge_orders(id) ON DELETE RESTRICT,
    findings jsonb NOT NULL DEFAULT '{}'::jsonb,
    expected_minor bigint,
    actual_minor bigint,
    difference_minor bigint,
    error_code text CHECK (error_code IS NULL OR length(error_code) BETWEEN 1 AND 64),
    requested_by uuid NOT NULL REFERENCES public.admin_users(id) ON DELETE RESTRICT,
    request_id text NOT NULL CHECK (request_id ~ '^req_[0-9a-f]{32}$'),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz,
    CONSTRAINT reconciliations_idempotency_uq UNIQUE (requested_by, idempotency_key),
    CONSTRAINT reconciliations_findings_object CHECK (jsonb_typeof(findings) = 'object')
);

CREATE INDEX reconciliations_created_idx ON public.reconciliations (created_at DESC, id DESC);
CREATE INDEX reconciliations_status_idx ON public.reconciliations (status, created_at);

CREATE TABLE public.refunds (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES public.recharge_orders(id) ON DELETE RESTRICT,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    wallet_id uuid NOT NULL REFERENCES public.wallets(id) ON DELETE RESTRICT,
    kind text NOT NULL CHECK (kind IN (
        'abnormal_payment', 'duplicate_payment', 'chargeback', 'manual_correction'
    )),
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    currency text NOT NULL DEFAULT 'CNY' CHECK (currency = 'CNY'),
    status text NOT NULL DEFAULT 'requested' CHECK (status IN (
        'requested', 'reserved', 'submitted', 'succeeded', 'failed', 'unknown', 'cancelled'
    )),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 500),
    provider text NOT NULL DEFAULT 'zpay' CHECK (provider = 'zpay'),
    provider_refund_no text NOT NULL UNIQUE CHECK (
        length(provider_refund_no) BETWEEN 1 AND 64
        AND provider_refund_no ~ '^[A-Za-z0-9_.:-]+$'
    ),
    provider_trade_no text CHECK (
        provider_trade_no IS NULL OR length(provider_trade_no) BETWEEN 1 AND 64
    ),
    requested_by uuid NOT NULL REFERENCES public.admin_users(id) ON DELETE RESTRICT,
    approved_by uuid REFERENCES public.admin_users(id) ON DELETE RESTRICT,
    reservation_journal_id uuid REFERENCES public.journals(id) ON DELETE RESTRICT,
    payout_journal_id uuid REFERENCES public.journals(id) ON DELETE RESTRICT,
    return_journal_id uuid REFERENCES public.journals(id) ON DELETE RESTRICT,
    last_error_code text CHECK (last_error_code IS NULL OR length(last_error_code) BETWEEN 1 AND 64),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CONSTRAINT refunds_idempotency_uq UNIQUE (requested_by, idempotency_key),
    CONSTRAINT refunds_status_time_check CHECK (
        (status IN ('succeeded', 'failed', 'cancelled') AND completed_at IS NOT NULL)
        OR (status IN ('requested', 'reserved', 'submitted', 'unknown') AND completed_at IS NULL)
    )
);

CREATE INDEX refunds_order_idx ON public.refunds (order_id, created_at DESC);
CREATE INDEX refunds_status_idx ON public.refunds (status, created_at);
