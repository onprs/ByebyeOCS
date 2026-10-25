import type {
  AdminAuthSession,
  AdminCapabilities,
  AdminDashboard,
  AdminEvent,
  AdminMe,
  AdminPreferences,
  AuditEvent,
  BillingSettings,
  ColorScheme,
  DashboardFinance,
  DashboardMetric,
  ModelCachePolicy,
  ModelHealth,
  ModelPricing,
  Order,
  OrderStatus,
  Paginated,
  PriceView,
  QuestionModel,
  QuestionQuery,
  QuestionUsageSummary,
  Reconciliation,
  Refund,
  RuntimeResource,
  RuntimeSettings,
  ServiceState,
  TaskDetail,
  TaskMediaEvidence,
  TaskState,
  TaskSummary,
  ThemeMode,
  ThroughputPoint,
  UserAccount,
  UserAccountState,
  UserDetail,
  WalletEntry,
  WalletSnapshot,
} from './types'

export type JsonObject = Record<string, unknown>

export function asObject(value: unknown): JsonObject | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? (value as JsonObject) : null
}

export function unwrapData(value: unknown): unknown {
  const object = asObject(value)
  if (object && 'data' in object && (object.data !== null && typeof object.data === 'object')) {
    return object.data
  }
  return value
}

export function pickString(object: JsonObject | null | undefined, keys: string[]): string | null {
  if (!object) return null
  for (const key of keys) {
    const value = object[key]
    if (typeof value === 'string' && value.trim() !== '') return value
  }
  return null
}

export function pickNumber(object: JsonObject | null | undefined, keys: string[]): number | null {
  if (!object) return null
  for (const key of keys) {
    const value = object[key]
    if (typeof value === 'number' && Number.isFinite(value)) return value
    if (typeof value === 'string' && value.trim() !== '') {
      const parsed = Number(value)
      if (Number.isFinite(parsed)) return parsed
    }
  }
  return null
}

export function pickBool(object: JsonObject | null | undefined, keys: string[]): boolean | null {
  if (!object) return null
  for (const key of keys) {
    const value = object[key]
    if (typeof value === 'boolean') return value
    if (value === 'true') return true
    if (value === 'false') return false
  }
  return null
}

export function pickArray(object: JsonObject | null | undefined, keys: string[]): unknown[] {
  if (!object) return []
  for (const key of keys) {
    const value = object[key]
    if (Array.isArray(value)) return value
  }
  return []
}

export function pickObject(object: JsonObject | null | undefined, keys: string[]): JsonObject | null {
  if (!object) return null
  for (const key of keys) {
    const value = asObject(object[key])
    if (value) return value
  }
  return null
}

function versionFrom(raw: unknown, etag: string | null = null): number {
  const object = asObject(unwrapData(raw))
  const direct = pickNumber(object, ['version', 'preference_version'])
  if (direct !== null) return Math.trunc(direct)
  const record = asObject(object?.preferences)
  const nested = pickNumber(record, ['version'])
  if (nested !== null) return Math.trunc(nested)
  const parsed = etag?.match(/"?v?(\d+)"?/i)
  return parsed ? Number(parsed[1]) : 0
}

export function normalizeList<T>(raw: unknown, map: (record: JsonObject) => T): Paginated<T> {
  const unwrapped = unwrapData(raw)
  const object = asObject(unwrapped)
  const list = Array.isArray(unwrapped) ? unwrapped : pickArray(object, ['items', 'results', 'list'])
  const next = pickString(object, ['next_cursor', 'nextCursor'])
  return {
    items: list.map(item => map(asObject(item) ?? {})),
    next_cursor: next,
  }
}

export function normalizeMe(raw: unknown): AdminMe {
  const object = asObject(unwrapData(raw)) ?? {}
  const admin = pickObject(object, ['admin', 'user', 'me']) ?? object
  const session = pickObject(admin, ['session']) ?? pickObject(object, ['session'])
  const permissionsRaw = pickArray(admin, ['permissions']).length
    ? pickArray(admin, ['permissions'])
    : pickArray(object, ['permissions'])
  const permissions = permissionsRaw.filter((item): item is string => typeof item === 'string')
  const explicitRole = pickString(admin, ['role', 'role_name'])
  return {
    id: pickString(admin, ['id', 'admin_id']) ?? '',
    username: pickString(admin, ['username', 'login_name']) ?? '',
    display_name: pickString(admin, ['display_name', 'name', 'displayName']) ?? '管理员',
    role: explicitRole ?? (permissions.includes('*') ? 'owner' : 'admin'),
    permissions,
    totp_enabled: pickBool(admin, ['totp_enabled', 'mfa_enabled']) ?? false,
    authenticated_at:
      pickString(session, ['authenticated_at']) ?? pickString(object, ['authenticated_at']),
    session_expires_at:
      pickString(session, ['expires_at']) ?? pickString(object, ['expires_at']),
    fresh: pickBool(session, ['fresh']) ?? pickBool(object, ['fresh']),
  }
}

export function normalizeAuthSession(raw: unknown): AdminAuthSession {
  const object = asObject(unwrapData(raw)) ?? {}
  const capabilities = pickObject(object, ['capabilities']) ?? {}
  const admin = pickObject(capabilities, ['admin']) ?? {}
  return {
    authenticated: pickBool(object, ['authenticated']) ?? false,
    csrf_token: pickString(object, ['csrf_token']),
    channel: pickString(object, ['channel']),
    expires_at: pickString(object, ['expires_at']),
    authenticated_at: pickString(object, ['authenticated_at']),
    fresh: pickBool(object, ['fresh']),
    permissions: pickArray(object, ['permissions']).filter((item): item is string => typeof item === 'string'),
    capabilities: {
      adminMethods: pickArray(admin, ['methods']).filter((item): item is string => typeof item === 'string'),
      stepUp: pickBool(admin, ['step_up']) ?? false,
      webCookie: pickBool(capabilities, ['web_cookie']) ?? false,
      totp: pickBool(capabilities, ['totp']) ?? false,
    },
  }
}

export function normalizeCapabilities(raw: unknown): AdminCapabilities {
  const object = asObject(unwrapData(raw)) ?? {}
  const auth = pickObject(object, ['authentication', 'auth']) ?? {}
  const features = pickObject(object, ['features']) ?? {}
  const appearance = pickObject(object, ['appearance']) ?? {}
  const pricing = pickObject(object, ['pricing']) ?? {}
  const schemeSet = pickArray(appearance, ['color_schemes']).filter(
    (item): item is ColorScheme => item === 'graphite' || item === 'grove' || item === 'indigo',
  )
  const modeSet = pickArray(appearance, ['theme_modes']).filter(
    (item): item is ThemeMode => item === 'system' || item === 'light' || item === 'dark',
  )
  const defaultScheme = pickString(appearance, ['default_color_scheme'])
  const defaultMode = pickString(appearance, ['default_theme_mode'])
  return {
    api_version: pickString(object, ['api_version', 'version']) ?? 'unknown',
    audience: pickString(object, ['audience']) ?? 'admin',
    minimum_client_version: pickString(object, ['minimum_client_version']),
    authentication: {
      enabled: pickBool(auth, ['enabled']) ?? false,
      login_methods: pickArray(auth, ['login_methods']).filter((item): item is string => typeof item === 'string'),
    },
    features: Object.fromEntries(Object.entries(features).map(([key, value]) => [key, value === true])),
    appearance: {
      default_color_scheme:
        defaultScheme === 'grove' || defaultScheme === 'indigo' || defaultScheme === 'graphite'
          ? defaultScheme
          : 'graphite',
      color_schemes: schemeSet.length ? schemeSet : ['graphite', 'grove', 'indigo'],
      default_theme_mode:
        defaultMode === 'light' || defaultMode === 'dark' || defaultMode === 'system' ? defaultMode : 'system',
      theme_modes: modeSet.length ? modeSet : ['system', 'light', 'dark'],
    },
    pricing: {
      service_configured: pickBool(pricing, ['service_configured']) ?? false,
      tiku_configured: pickBool(pricing, ['tiku_configured']) ?? false,
      automatic_charging: pickBool(pricing, ['automatic_charging']) ?? false,
    },
    request_id: pickString(object, ['request_id']),
    generated_at: pickString(object, ['generated_at']),
  }
}

export function normalizePreferences(raw: unknown, etag: string | null = null): AdminPreferences {
  const object = asObject(unwrapData(raw)) ?? {}
  const preferences = pickObject(object, ['preferences']) ?? object
  const scheme = pickString(preferences, ['color_scheme'])
  const mode = pickString(preferences, ['theme_mode'])
  const notifications = pickObject(preferences, ['notifications'])
  return {
    color_scheme: scheme === 'grove' || scheme === 'indigo' || scheme === 'graphite' ? scheme : 'graphite',
    theme_mode: mode === 'light' || mode === 'dark' || mode === 'system' ? mode : 'system',
    version: versionFrom(raw, etag),
    notifications: notifications
      ? {
          task_attention: pickBool(notifications, ['task_attention']) ?? undefined,
          wallet_changes: pickBool(notifications, ['wallet_changes']) ?? undefined,
        }
      : undefined,
  }
}

function mapState(value: string | null): TaskState {
  switch (value) {
    case 'pending':
    case 'queued':
    case 'waiting':
      return 'pending'
    case 'preparing':
      return 'preparing'
    case 'running':
    case 'executing':
      return 'running'
    case 'waiting_verification':
    case 'verifying':
      return 'waiting_verification'
    case 'confirmed':
    case 'platform_confirmed':
      return 'confirmed'
    case 'paused':
      return 'paused'
    case 'needs_attention':
    case 'attention':
      return 'needs_attention'
    case 'failed':
      return 'failed'
    case 'cancelled':
    case 'canceled':
      return 'cancelled'
    default:
      return 'unknown'
  }
}

const taskStateLabels: Record<TaskState, string> = {
  pending: '待执行',
  preparing: '准备中',
  running: '执行中',
  waiting_verification: '等待验证',
  confirmed: '平台已确认',
  paused: '已暂停',
  needs_attention: '需要人工处理',
  failed: '失败',
  cancelled: '已取消',
  unknown: '待确认状态',
}

export function taskStateLabel(state: TaskState, label: string | null): string {
  return label ?? taskStateLabels[state]
}

export function normalizeTaskSummary(record: JsonObject): TaskSummary {
  const observation = pickObject(record, ['observation'])
  const platform = pickObject(record, ['platform', 'platform_confirmation'])
  const state = mapState(pickString(record, ['state', 'status', 'task_state']))
  return {
    id: pickString(record, ['id', 'task_id']) ?? '',
    title: pickString(record, ['title', 'task_title', 'name']) ?? '未命名任务',
    task_kind: pickString(record, ['task_kind', 'kind']),
    course_title: pickString(record, ['course_title', 'course_name']),
    chapter_title: pickString(record, ['chapter_title']),
    user_id: pickString(record, ['user_id', 'owner_id']),
    user_display_name: pickString(record, ['user_display_name', 'owner_display_name']),
    state,
    state_label: pickString(record, ['state_label', 'status_label']) ?? taskStateLabels[state],
    progress_fraction:
      pickNumber(record, ['progress_fraction', 'progress', 'observed_fraction']) ??
      pickNumber(observation, ['fraction']),
    platform_status: pickString(record, ['platform_status']) ?? pickString(platform, ['status']),
    updated_at: pickString(record, ['updated_at', 'last_updated_at']),
    last_error_code: pickString(record, ['last_error_code', 'error_code']),
    attempt_count: pickNumber(record, ['attempt_count']),
    failure_count: pickNumber(record, ['failure_count']),
  }
}

function mediaFrom(record: JsonObject | null, platform: JsonObject | null): TaskMediaEvidence | null {
  if (!record) return null
  const available = pickBool(record, ['available']) === true
  const observedAt = pickString(record, ['observed_at'])
  const detail = pickString(record, ['detail', 'note'])
  const source = pickString(record, ['source'])
  const playTime = pickNumber(platform, ['play_time_seconds'])
  return {
    available,
    records:
      available && observedAt
        ? [
            {
              id: null,
              observed_at: observedAt,
              duration_seconds: playTime,
              detail: detail ?? source,
            },
          ]
        : [],
    note: detail,
    source,
  }
}

function timelineFrom(record: JsonObject): TaskDetail['timeline'] {
  const events: TaskDetail['timeline'] = []
  for (const raw of pickArray(record, ['commands'])) {
    const command = asObject(raw) ?? {}
    const status = pickString(command, ['status'])
    const reason = pickString(command, ['reason'])
    events.push({
      at: pickString(command, ['applied_at', 'created_at']),
      title: `控制命令：${pickString(command, ['kind']) ?? '未知动作'}`,
      detail: [status, reason].filter(Boolean).join(' · ') || null,
      tone: status === 'rejected' ? 'red' : 'info',
    })
  }
  for (const raw of pickArray(record, ['attempts'])) {
    const attempt = asObject(raw) ?? {}
    const attemptState = pickString(attempt, ['state'])
    const result = pickString(attempt, ['result'])
    const errorCode = pickString(attempt, ['error_code'])
    events.push({
      at: pickString(attempt, ['ended_at', 'started_at']),
      title: `执行尝试 #${pickNumber(attempt, ['attempt_no']) ?? '—'}`,
      detail: [attemptState, result, errorCode].filter(Boolean).join(' · ') || null,
      tone: errorCode ? 'amber' : null,
    })
  }
  return events.sort((left, right) => {
    const l = left.at ? Date.parse(left.at) : 0
    const r = right.at ? Date.parse(right.at) : 0
    return r - l
  })
}

export function normalizeTaskDetail(record: JsonObject): TaskDetail {
  const summary = normalizeTaskSummary(record)
  const observation = pickObject(record, ['observation', 'executor_observation'])
  const platform = pickObject(record, ['platform', 'platform_confirmation'])
  const media = pickObject(record, ['media_evidence', 'media'])
  const estimate = pickObject(record, ['estimate'])
  const billing = pickObject(record, ['billing'])
  const controls = pickObject(record, ['controls', 'available_controls'])
  const planRefs = pickArray(record, ['plan_refs'])
  const firstPlan = asObject(planRefs[0])
  const platformStatus = pickString(platform, ['status']) ?? summary.platform_status
  return {
    ...summary,
    desired_state: pickString(record, ['desired_state']),
    version: pickNumber(record, ['version']),
    owner_plan_id: pickString(record, ['plan_id', 'owner_plan_id']) ?? pickString(firstPlan, ['plan_id']),
    observation: observation
      ? {
          fraction: pickNumber(observation, ['fraction', 'progress']),
          source: pickString(observation, ['source']),
          observed_at: pickString(observation, ['observed_at', 'at']),
          detail: pickString(observation, ['note', 'detail']),
        }
      : null,
    platform: platform
      ? {
          status: platformStatus ?? 'unknown',
          passed: pickBool(platform, ['passed']),
          checked_at: pickString(platform, ['checked_at', 'at']),
          basis:
            pickString(platform, ['note']) ??
            pickString(platform, ['source']) ??
            pickString(platform, ['basis']),
          source: pickString(platform, ['source']),
          play_time_seconds: pickNumber(platform, ['play_time_seconds']),
        }
      : summary.platform_status
        ? {
            status: summary.platform_status,
            passed: null,
            checked_at: null,
            basis: null,
            source: null,
            play_time_seconds: null,
          }
        : null,
    media_evidence: mediaFrom(media, platform),
    estimate: estimate
      ? {
          remaining_seconds_min: pickNumber(estimate, ['remaining_seconds_min', 'min_seconds']),
          remaining_seconds_max: pickNumber(estimate, ['remaining_seconds_max', 'max_seconds']),
          estimated: pickBool(estimate, ['estimated']) ?? true,
        }
      : null,
    billing: billing
      ? {
          state: pickString(billing, ['state']),
          configured: pickBool(billing, ['configured']),
          amount_kind: pickString(billing, ['amount_kind']),
          quoted_amount_minor:
            pickString(billing, ['quoted_amount_minor', 'amount_minor']),
          currency: pickString(billing, ['currency']),
          price_version: pickString(billing, ['price_version']),
          error_code: pickString(billing, ['error_code']),
        }
      : null,
    timeline: timelineFrom(record),
    can_pause: pickBool(controls, ['pause', 'can_pause']),
    can_resume: pickBool(controls, ['resume', 'can_resume']),
    can_cancel: pickBool(controls, ['cancel', 'can_cancel']),
    can_reschedule: pickBool(controls, ['reschedule', 'can_reschedule']),
  }
}

function mapUserState(value: string | null): UserAccountState {
  switch (value) {
    case 'valid':
    case 'active':
    case 'session_valid':
      return 'valid'
    case 'expired':
    case 'needs_reverification':
    case 'session_expired':
      return 'expired'
    case 'limited':
    case 'restricted':
      return 'limited'
    default:
      return 'unknown'
  }
}

export function normalizeUser(record: JsonObject): UserAccount {
  const wallet = pickObject(record, ['wallet'])
  const blockedCode = pickString(record, ['blocked_code'])
  const blockedUntil = pickString(record, ['blocked_until'])
  const identityBound = pickBool(record, ['identity_bound', 'identityBound'])
  const rawState = pickString(record, ['status', 'session_status', 'state'])
  let state: UserAccountState = mapUserState(rawState)
  if (!rawState) {
    if (blockedCode || (blockedUntil && Date.parse(blockedUntil) > Date.now())) state = 'limited'
    else if (identityBound === false) state = 'expired'
    else if (identityBound === true) state = 'valid'
  }
  const statusLabel =
    pickString(record, ['status_label', 'session_status_label']) ??
    (state === 'limited'
      ? '已限制'
      : state === 'valid'
        ? '身份已绑定'
        : state === 'expired'
          ? '未绑定身份'
          : null)
  return {
    id: pickString(record, ['user_id', 'id']) ?? '',
    display_name: pickString(record, ['display_name', 'name']) ?? '未命名用户',
    school: pickString(record, ['school', 'school_name']),
    status: state,
    status_label: statusLabel,
    status_detail: pickString(record, ['blocked_reason', 'status_detail', 'session_note']),
    identity_bound: identityBound,
    identity_namespace: pickString(record, ['identity_namespace']),
    blocked_code: blockedCode,
    blocked_until: blockedUntil,
    masked_subject: pickString(record, ['masked_subject', 'external_subject_masked', 'subject_summary']),
    course_count: pickNumber(record, ['course_count', 'courses_count']),
    plan_count: pickNumber(record, ['plan_count']),
    task_count: pickNumber(record, ['task_count']),
    incomplete_task_count: pickNumber(record, ['incomplete_task_count']),
    running_task_count: pickNumber(record, ['running_task_count', 'active_task_count']),
    wallet_id: pickString(record, ['wallet_id']) ?? pickString(wallet, ['id', 'wallet_id']),
    balance_minor:
      pickString(record, ['balance_minor', 'available_minor']) ??
      pickString(wallet, ['available_minor', 'balance_minor']),
    cumulative_spend_minor: pickString(record, ['cumulative_spend_minor', 'spent_minor']),
    account_concurrency: pickNumber(record, ['account_concurrency', 'concurrency_limit']),
    last_synced_at: pickString(record, ['last_synced_at']),
    last_active_at: pickString(record, ['last_active_at', 'last_seen_at']) ?? pickString(record, ['last_synced_at']),
    created_at: pickString(record, ['created_at']),
  }
}

export function normalizeUserDetail(raw: unknown): UserDetail {
  const object = asObject(unwrapData(raw)) ?? {}
  const user = pickObject(object, ['user']) ?? object
  const base = normalizeUser(user)
  const taskSource = pickArray(user, ['recent_tasks', 'tasks'])
  const tasks = (taskSource.length ? taskSource : pickArray(object, ['recent_tasks', 'tasks'])).map(item =>
    normalizeTaskSummary(asObject(item) ?? {}),
  )
  const plans = pickArray(user, ['plans'])
  const courses = pickArray(user, ['courses'])
  return {
    ...base,
    credential_state: pickString(user, ['credential_state', 'credential_status']),
    session_expires_at: pickString(user, ['session_expires_at']),
    active_plan_count:
      pickNumber(user, ['active_plan_count']) ??
      (plans.filter(plan => pickString(asObject(plan), ['status']) === 'active').length || base.plan_count),
    recent_tasks: tasks,
    notes: pickString(user, ['notes', 'admin_note']),
    course_count: base.course_count ?? (courses.length || null),
  }
}

function mapOrderStatus(
  backendStatus: string | null,
  payment: string | null,
  credit: string | null,
): OrderStatus {
  if (payment === 'refunded') return 'refunded'
  if (backendStatus === 'needs_review' || credit === 'needs_review') return 'abnormal'
  if (credit === 'credited') return 'credited'
  if (backendStatus === 'closed') return 'closed'
  if (payment === 'paid') return 'credit_pending'
  if (backendStatus === 'pending_payment' || payment === 'unpaid') return 'pending'
  if (payment === 'failed') return 'abnormal'
  if (payment === 'paid' && credit === 'not_credited') return 'paid'
  return 'unknown'
}

const orderStatusLabels: Record<OrderStatus, string> = {
  pending: '待支付',
  paid: '支付成功',
  credited: '已到账',
  credit_pending: '待入账',
  closed: '已关闭',
  refunded: '已退款',
  abnormal: '需要核对',
  unknown: '状态待确认',
}

export function normalizeOrder(record: JsonObject): Order {
  const payment = pickString(record, ['payment_status', 'payment_state'])
  const credit = pickString(record, ['credit_status', 'credit_state', 'posting_status'])
  const backendStatus = pickString(record, ['status', 'order_status'])
  const status = mapOrderStatus(backendStatus, payment, credit)
  return {
    id: pickString(record, ['id', 'order_id']) ?? '',
    user_id: pickString(record, ['user_id']),
    wallet_id: pickString(record, ['wallet_id']),
    user_display_name: pickString(record, ['user_display_name']),
    amount_minor: pickString(record, ['amount_minor', 'amount']) ?? '0',
    currency: pickString(record, ['currency']) ?? 'CNY',
    channel: pickString(record, ['channel', 'payment_channel']),
    provider: pickString(record, ['provider']),
    merchant_order_no: pickString(record, ['merchant_order_no']),
    payment_status: payment ?? 'unknown',
    credit_status: credit ?? 'unknown',
    status,
    status_label: pickString(record, ['status_label']) ?? orderStatusLabels[status],
    late_payment: pickBool(record, ['late_payment']),
    paid_at: pickString(record, ['paid_at', 'payment_at']),
    credited_at: pickString(record, ['credited_at', 'credit_at']),
    closed_at: pickString(record, ['closed_at']),
    expires_at: pickString(record, ['expires_at']),
    created_at: pickString(record, ['created_at']),
    updated_at: pickString(record, ['updated_at']),
    version: pickNumber(record, ['version']),
    checkout_state: pickString(record, ['checkout_state']),
    reconciliation_state: pickString(record, ['reconciliation_state']),
    note: pickString(record, ['note', 'memo']),
  }
}

export function normalizeWalletSnapshot(raw: unknown): WalletSnapshot {
  const object = asObject(unwrapData(raw)) ?? {}
  const wallet = pickObject(object, ['wallet']) ?? object
  return {
    wallet_id: pickString(wallet, ['id', 'wallet_id']) ?? '',
    currency: pickString(wallet, ['currency']) ?? 'CNY',
    available_minor: pickString(wallet, ['available_minor', 'balance_minor']),
    reserved_minor: pickString(wallet, ['reserved_minor', 'held_minor']),
    frozen: pickBool(wallet, ['frozen']),
    frozen_reason: pickString(wallet, ['frozen_reason']),
    tiku_accrued_micros: pickString(wallet, ['tiku_accrued_micros']),
    version: pickNumber(wallet, ['version']),
    updated_at: pickString(wallet, ['updated_at']),
  }
}

const walletKindLabels: Record<string, string> = {
  recharge: '充值到账',
  hold: '费用预占',
  capture: '预占结算',
  release: '预占释放',
  manual_credit: '人工增加',
  manual_debit: '人工扣减',
  refund_reserve: '退款预占',
  refund_payout: '退款支出',
  refund_return: '退款退回',
}

export const walletKindLabel = (kind: string): string => walletKindLabels[kind] ?? kind

export function normalizeWalletEntry(record: JsonObject): WalletEntry {
  const kind = pickString(record, ['kind', 'type', 'entry_type']) ?? 'unknown'
  return {
    id: pickString(record, ['journal_id', 'id', 'entry_id']) ?? '',
    wallet_id: pickString(record, ['wallet_id']),
    kind,
    type_label: pickString(record, ['type_label']) ?? walletKindLabel(kind),
    available_delta_minor:
      pickString(record, ['available_delta_minor', 'available_minor', 'amount_minor']) ?? '0',
    reserved_delta_minor: pickString(record, ['reserved_delta_minor', 'reserved_minor']) ?? '0',
    business_id:
      pickString(record, ['order_id', 'usage_id', 'hold_id', 'reference_id']) ?? null,
    description: pickString(record, ['reason', 'description', 'detail']),
    price_version: pickString(record, ['price_version', 'price_version_id']),
    usage_id: pickString(record, ['usage_id']),
    created_at: pickString(record, ['created_at', 'booked_at']),
  }
}

export function normalizeAuditEvent(record: JsonObject): AuditEvent {
  const actorKind = pickString(record, ['actor_kind'])
  const actor = pickString(record, ['actor_name', 'actor_id'])
  return {
    id: pickString(record, ['id', 'event_id']) ?? '',
    action: pickString(record, ['action']) ?? 'unknown',
    action_label: pickString(record, ['action_label']),
    actor,
    actor_kind: actorKind,
    resource_type: pickString(record, ['resource_type']),
    resource_id: pickString(record, ['resource_id']),
    reason: pickString(record, ['reason']),
    summary: pickString(record, ['summary', 'detail']),
    request_id: pickString(record, ['request_id']),
    created_at: pickString(record, ['occurred_at', 'created_at', 'at']),
  }
}

const reconciliationKindLabels: Record<string, string> = {
  wallet_rebuild: '钱包重建核对',
  payment_orders: '支付订单核对',
  payment_inbox: '支付回调核对',
  wallet_unfreeze: '钱包解冻核对',
}

const reconciliationStateLabels: Record<string, string> = {
  pending: '待执行',
  running: '执行中',
  matched: '已核对一致',
  mismatch: '存在差异',
  needs_review: '待人工复核',
  completed: '已完成',
  failed: '失败',
}

function displayValue(value: unknown): string {
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  if (Array.isArray(value)) return value.map(displayValue).join('、')
  const object = asObject(value)
  if (object) {
    return Object.entries(object)
      .map(([key, item]) => `${key} ${displayValue(item)}`)
      .join('；')
  }
  return '—'
}

function formatFindings(value: unknown): Array<{ key: string; value: string }> {
  const object = asObject(value)
  if (!object) return []
  return Object.entries(object)
    .map(([key, item]) => ({ key, value: displayValue(item) }))
    .slice(0, 8)
}

function formatStringValue(value: unknown): string {
  return displayValue(value)
}

export function normalizeReconciliation(record: JsonObject): Reconciliation {
  const kind = pickString(record, ['kind']) ?? 'unknown'
  const status = pickString(record, ['status', 'state']) ?? 'unknown'
  return {
    id: pickString(record, ['id', 'reconciliation_id']) ?? '',
    kind,
    kind_label: pickString(record, ['kind_label']) ?? reconciliationKindLabels[kind],
    order_id: pickString(record, ['target_order_id', 'order_id']),
    wallet_id: pickString(record, ['target_wallet_id', 'wallet_id']),
    user_id: pickString(record, ['target_user_id', 'user_id']),
    status,
    state_label:
      pickString(record, ['state_label', 'status_label']) ?? reconciliationStateLabels[status] ?? status,
    expected_minor: pickString(record, ['expected_minor']),
    actual_minor: pickString(record, ['actual_minor']),
    difference_minor: pickString(record, ['difference_minor']),
    error_code: pickString(record, ['error_code']),
    findings: formatFindings(record.findings),
    requested_by: pickString(record, ['requested_by']),
    created_at: pickString(record, ['created_at']),
    completed_at: pickString(record, ['completed_at']),
  }
}

const refundStatusLabels: Record<string, string> = {
  requested: '已申请',
  reserved: '已预占',
  submitted: '已提交渠道',
  succeeded: '已退款',
  failed: '失败',
  unknown: '状态待确认',
  cancelled: '已取消',
}

export function normalizeRefund(record: JsonObject): Refund {
  const status = pickString(record, ['status', 'state']) ?? 'unknown'
  return {
    id: pickString(record, ['id', 'refund_id']) ?? '',
    order_id: pickString(record, ['order_id']) ?? '',
    user_id: pickString(record, ['user_id']),
    wallet_id: pickString(record, ['wallet_id']),
    kind: pickString(record, ['kind']),
    amount_minor: pickString(record, ['amount_minor', 'amount']) ?? '0',
    currency: pickString(record, ['currency']),
    reason: pickString(record, ['reason']),
    status,
    status_label: pickString(record, ['status_label']) ?? refundStatusLabels[status] ?? status,
    provider: pickString(record, ['provider']),
    provider_refund_no: pickString(record, ['provider_refund_no']),
    provider_trade_no: pickString(record, ['provider_trade_no']),
    last_error_code: pickString(record, ['last_error_code']),
    created_at: pickString(record, ['created_at']),
    updated_at: pickString(record, ['updated_at']),
    completed_at: pickString(record, ['completed_at']),
  }
}

function mapHealth(value: string | null): ModelHealth {
  switch (value) {
    case 'healthy':
    case 'ok':
    case 'available':
      return 'healthy'
    case 'configured':
      return 'configured'
    case 'degraded':
      return 'degraded'
    case 'unavailable':
    case 'down':
    case 'failed':
      return 'unavailable'
    default:
      return 'unknown'
  }
}

function normalizePricing(value: unknown): ModelPricing | null {
  const pricing = asObject(value)
  if (!pricing) return null
  return {
    version: pickString(pricing, ['version']) ?? '',
    input_micros_per_million_tokens:
      pickString(pricing, ['input_micros_per_million_tokens', 'input_micros_per_million']) ?? '0',
    output_micros_per_million_tokens:
      pickString(pricing, ['output_micros_per_million_tokens', 'output_micros_per_million']) ?? '0',
    micros_per_minor: pickString(pricing, ['micros_per_minor']) ?? '10000',
    rounding: pickString(pricing, ['rounding']) ?? 'ceil',
    rule: pickString(pricing, ['rule']) ?? 'token_usage',
  }
}

function normalizeCachePolicy(value: unknown): ModelCachePolicy {
  const policy = asObject(value) ?? {}
  return {
    allow: pickBool(policy, ['allow', 'enabled']) ?? false,
    scope: pickString(policy, ['scope']) ?? 'user',
    ttl_days: pickNumber(policy, ['ttl_days', 'ttlDays']) ?? 7,
  }
}

export function normalizeModel(record: JsonObject): QuestionModel {
  const rawCapabilities = pickArray(record, ['capabilities'])
  const capabilities = rawCapabilities.length
    ? rawCapabilities
        .map(item => (typeof item === 'string' ? item : pickString(asObject(item), ['key', 'name'])))
        .filter((item): item is string => Boolean(item))
    : ['text']
  const health = mapHealth(pickString(record, ['health', 'health_status']))
  const pricing = normalizePricing(record.pricing)
  return {
    id: pickString(record, ['id', 'model_id']) ?? '',
    display_name: pickString(record, ['display_name', 'name']) ?? '未命名模型',
    description: pickString(record, ['description', 'summary']),
    provider_ref: pickString(record, ['provider_ref', 'upstream_ref', 'upstream_ref_masked']),
    capabilities,
    enabled: pickBool(record, ['enabled', 'visible']) ?? false,
    is_default: pickBool(record, ['default', 'is_default']) ?? false,
    sort_order: pickNumber(record, ['sort_order']),
    price_version: pickString(record, ['price_version']) ?? pricing?.version ?? null,
    pricing,
    cache_policy: normalizeCachePolicy(record.cache_policy),
    health,
    health_label: pickString(record, ['health_label']),
    version: pickNumber(record, ['version']),
    created_at: pickString(record, ['created_at']),
    updated_at: pickString(record, ['updated_at']),
  }
}

function normalizeUsageRow(record: JsonObject): QuestionUsageSummary['by_model'][number] {
  return {
    model_id: pickString(record, ['offering_id', 'model_id', 'id']) ?? '',
    display_name: pickString(record, ['display_name', 'model_display_name', 'name']) ?? '未命名模型',
    queries: pickNumber(record, ['queries', 'query_count']),
    cache_hits: pickNumber(record, ['cache_hits', 'cache_hit_count']),
    input_tokens: pickNumber(record, ['input_tokens']),
    output_tokens: pickNumber(record, ['output_tokens']),
    total_tokens: pickNumber(record, ['total_tokens']),
    cost_micros: pickString(record, ['cost_micros', 'estimated_micros']),
    pricing: normalizePricing(record.pricing),
  }
}

export function normalizeQuestionUsage(raw: unknown): QuestionUsageSummary {
  const object = asObject(unwrapData(raw)) ?? {}
  const source = pickArray(object, ['by_model', 'models', 'items'])
  const byModel = source.map(item => normalizeUsageRow(asObject(item) ?? {}))
  const pricingPolicy = pickObject(object, ['pricing'])
  const sum = (key: 'queries' | 'cache_hits' | 'input_tokens' | 'output_tokens' | 'total_tokens') =>
    byModel.reduce((total, row) => total + (row[key] ?? 0), 0)
  const pricingKnown = byModel.some(row => row.pricing !== null)
  const automaticCharging = pickBool(pricingPolicy, ['automatic_charging'])
  const costMicros = byModel.reduce((total, row) => {
    if (!row.cost_micros || !/^-?\d+$/.test(row.cost_micros)) return total
    return total + BigInt(row.cost_micros)
  }, 0n)
  const queries = byModel.length ? sum('queries') : pickNumber(object, ['queries', 'query_count', 'total_queries'])
  const cacheHits = byModel.length
    ? sum('cache_hits')
    : pickNumber(object, ['cache_hits', 'cache_hit_count'])
  return {
    queries,
    cache_hits: cacheHits,
    cache_hit_rate: pickNumber(object, ['cache_hit_rate']),
    input_tokens: byModel.length ? sum('input_tokens') : pickNumber(object, ['input_tokens']),
    output_tokens: byModel.length ? sum('output_tokens') : pickNumber(object, ['output_tokens']),
    total_tokens: byModel.length ? sum('total_tokens') : pickNumber(object, ['total_tokens']),
    incomplete_queries: pickNumber(object, ['incomplete_queries', 'usage_incomplete_count']),
    cost_state: pricingKnown ? (automaticCharging === false ? 'unavailable' : 'quoted') : 'unpriced',
    cost_micros: costMicros > 0n ? costMicros.toString() : null,
    price_version:
      pickString(object, ['price_version']) ??
      byModel.find(row => row.pricing?.version)?.pricing?.version ??
      null,
    automatic_charging: automaticCharging,
    unit: pickString(pricingPolicy, ['unit']),
    by_model: byModel,
  }
}

const queryStatusLabels: Record<string, string> = {
  pending: '处理中',
  answered: '已回答',
  cache_hit: '缓存命中',
  needs_review: '待人工复核',
  failed: '失败',
}

export function normalizeQuestionQuery(record: JsonObject): QuestionQuery {
  const cache = pickObject(record, ['cache'])
  const usage = pickObject(record, ['usage'])
  const model = pickObject(record, ['model'])
  const pricing = pickObject(record, ['pricing'])
  const question = pickObject(record, ['question'])
  const calls = pickArray(usage, ['calls']).map(item => {
    const row = asObject(item) ?? {}
    return {
      slot: pickString(row, ['slot']) ?? 'call',
      input_tokens: pickNumber(row, ['input_tokens']),
      output_tokens: pickNumber(row, ['output_tokens']),
      total_tokens: pickNumber(row, ['total_tokens']),
    }
  })
  const status = pickString(record, ['status']) ?? 'unknown'
  const userRef = pickString(record, ['user_ref', 'user_id'])
  return {
    id: pickString(record, ['id', 'query_id']) ?? '',
    user_ref: userRef,
    user_display_name:
      pickString(record, ['user_display_name']) ??
      (userRef ? `用户摘要 ${userRef}` : null),
    task_id: pickString(record, ['task_id']),
    model_id: pickString(record, ['model_id', 'model_offering_id']) ?? pickString(model, ['offering_id', 'id']),
    model_display_name: pickString(record, ['model_display_name']) ?? pickString(model, ['display_name', 'name']),
    mode: pickString(record, ['mode', 'answer_mode']),
    cache_hit: pickBool(record, ['cache_hit']) ?? pickBool(cache, ['hit']) ?? false,
    cache_entry_id: pickString(record, ['cache_entry_id']) ?? pickString(cache, ['entry_id']),
    question_title:
      pickString(record, ['question_title', 'question_preview']) ?? pickString(question, ['title']),
    answer_preview: pickString(record, ['answer_preview', 'answer']),
    input_tokens: pickNumber(usage, ['input_tokens']) ?? pickNumber(record, ['input_tokens']),
    output_tokens: pickNumber(usage, ['output_tokens']) ?? pickNumber(record, ['output_tokens']),
    total_tokens: pickNumber(usage, ['total_tokens']) ?? pickNumber(record, ['total_tokens']),
    usage_complete: pickBool(usage, ['complete']) ?? pickBool(record, ['usage_complete']) ?? false,
    status,
    status_label: pickString(record, ['status_label']) ?? queryStatusLabels[status],
    pricing_state: pickString(pricing, ['state']) ?? pickString(record, ['pricing_state']),
    billing_state: pickString(pricing, ['billing_state']) ?? pickString(record, ['billing_state']),
    charged: pickBool(pricing, ['charged']),
    cost_micros: pickString(pricing, ['cost_micros']) ?? pickString(record, ['cost_micros']),
    content_expired: pickBool(record, ['content_expired']),
    created_at: pickString(record, ['created_at', 'requested_at']),
    calls,
  }
}

function metricValue(summary: JsonObject, keys: string[]): string | null {
  const value = pickNumber(summary, keys)
  if (value === null) return pickString(summary, keys)
  return String(value)
}

function normalizeMetrics(object: JsonObject): DashboardMetric[] {
  const directMetrics = pickArray(object, ['metrics'])
  if (directMetrics.length) {
    return directMetrics.map((item, index) => {
      const row = asObject(item) ?? {}
      return {
        key: pickString(row, ['key', 'name']) ?? `metric_${index}`,
        label: pickString(row, ['label', 'title']) ?? '指标',
        value:
          pickString(row, ['value', 'display_value']) ??
          (pickNumber(row, ['value']) !== null ? String(pickNumber(row, ['value'])) : null),
        unit: pickString(row, ['unit', 'suffix']),
        hint: pickString(row, ['hint', 'detail']),
      }
    })
  }
  const accounts = pickObject(object, ['accounts'])
  const tasks = pickObject(object, ['tasks'])
  const byState = pickObject(tasks, ['by_state'])
  const courses = pickObject(object, ['courses'])
  const plans = pickObject(object, ['plans'])
  const syncs = pickObject(object, ['syncs'])
  if (accounts || tasks) {
    const attention =
      pickNumber(byState, ['needs_attention']) ??
      0
    const syncAttention = pickNumber(syncs, ['needs_attention']) ?? 0
    return [
      {
        key: 'users',
        label: '平台用户',
        value: metricValue(accounts ?? {}, ['total']),
        unit: null,
        hint:
          pickNumber(accounts, ['bound']) !== null
            ? `已绑定 ${pickNumber(accounts, ['bound'])} · 受限 ${pickNumber(accounts, ['blocked']) ?? 0}`
            : null,
      },
      {
        key: 'running',
        label: '执行中尝试',
        value: metricValue(tasks ?? {}, ['active_attempts']),
        unit: null,
        hint: pickNumber(tasks, ['total']) !== null ? `任务总数 ${pickNumber(tasks, ['total'])}` : null,
      },
      {
        key: 'waiting_verification',
        label: '等待平台确认',
        value: metricValue(tasks ?? {}, ['awaiting_verification']),
        unit: null,
        hint: pickNumber(byState, ['waiting_verification']) !== null ? '观察完成不自动视为通过' : null,
      },
      {
        key: 'needs_attention',
        label: '需要关注',
        value: String(attention),
        unit: null,
        hint: `同步异常 ${syncAttention} · 课程 ${pickNumber(courses, ['total']) ?? 0} · 安排 ${pickNumber(plans, ['active']) ?? 0}`,
      },
    ]
  }
  const summary = pickObject(object, ['summary', 'counts', 'task_counts']) ?? pickObject(object, ['metrics']) ?? object
  const definitions: Array<{ key: string; label: string; keys: string[]; hint?: string }> = [
    { key: 'users', label: '平台用户', keys: ['users_total', 'total_users', 'users'] },
    { key: 'active_sessions', label: '活跃会话', keys: ['active_sessions', 'sessions_active'] },
    { key: 'running_tasks', label: '执行中任务', keys: ['running_tasks', 'active_tasks'] },
    {
      key: 'waiting_verification',
      label: '等待平台确认',
      keys: ['waiting_verification_tasks', 'verifying_tasks'],
    },
  ]
  return definitions.map(definition => ({
    key: definition.key,
    label: definition.label,
    value: metricValue(summary, definition.keys),
    unit: null,
    hint: null,
  }))
}

function mapServiceState(value: string | null, enabled?: boolean | null): ServiceState {
  if (enabled === false) return 'unavailable'
  switch (value) {
    case 'healthy':
    case 'ok':
    case 'up':
    case 'online':
    case 'configured':
      return 'healthy'
    case 'degraded':
      return 'degraded'
    case 'unavailable':
    case 'down':
    case 'offline':
    case 'failed':
    case 'disabled':
      return 'unavailable'
    default:
      return enabled === true ? 'healthy' : 'unknown'
  }
}

function normalizeCapabilityResources(object: JsonObject): RuntimeResource[] {
  const capabilities = pickObject(object, ['capabilities'])
  if (!capabilities) return []
  const resources: RuntimeResource[] = []
  const add = (
    key: string,
    label: string,
    value: string | null,
    enabled: boolean | null | undefined,
  ) => {
    resources.push({
      key,
      label,
      value: value || (enabled === false ? '未启用' : enabled === true ? '可用' : '—'),
      unit: null,
      state: mapServiceState(null, enabled),
    })
  }
  const adapter = pickString(capabilities, ['platform_adapter'])
  add('platform', '平台适配器', adapter, pickBool(capabilities, ['course_read']) || pickBool(capabilities, ['platform_check']))
  add('platform_check', '平台任务核验', null, pickBool(capabilities, ['platform_check']))
  add('credential', '凭据验证', null, pickBool(capabilities, ['credential_verify']))
  const executor = pickString(capabilities, ['executor'])
  add('executor', '执行器', executor, pickBool(capabilities, ['executor_enabled']))
  add('media', '媒体证据', null, pickBool(capabilities, ['media_evidence']))
  add('billing', '账务连接', null, pickBool(capabilities, ['billing_connected']))
  add('sessions', '平台会话解析', null, pickBool(capabilities, ['session_resolver']))
  return resources
}

function normalizeResources(object: JsonObject): RuntimeResource[] {
  const capabilityResources = normalizeCapabilityResources(object)
  if (capabilityResources.length) return capabilityResources
  const resourceSource = pickArray(object, ['resources', 'runtime_resources'])
  if (resourceSource.length) {
    return resourceSource.map((item, index) => {
      const row = asObject(item) ?? {}
      return {
        key: pickString(row, ['key', 'name']) ?? `resource_${index}`,
        label: pickString(row, ['label', 'title']) ?? '运行资源',
        value:
          pickString(row, ['value', 'display_value']) ??
          (pickNumber(row, ['value']) !== null ? String(pickNumber(row, ['value'])) : null),
        unit: pickString(row, ['unit', 'suffix']),
        state: mapServiceState(pickString(row, ['state', 'status', 'health'])),
      }
    })
  }
  const runtime = pickObject(object, ['runtime', 'resources'])
  if (!runtime) return []
  const definitions: Array<{ key: string; label: string; keys: string[]; unit?: string }> = [
    { key: 'cpu', label: 'CPU', keys: ['cpu_percent', 'cpu'], unit: '%' },
    { key: 'memory', label: '内存', keys: ['memory_used_mb', 'memory_mb'], unit: 'MB' },
    { key: 'goroutines', label: 'Goroutine', keys: ['goroutines'] },
    { key: 'api_p95', label: 'API P95', keys: ['api_p95_ms', 'p95_ms'], unit: 'ms' },
    { key: 'workers', label: 'Worker 名额', keys: ['workers_used', 'worker_slots'] },
    { key: 'tiku', label: '题库服务', keys: ['tiku_latency_ms'], unit: 'ms' },
  ]
  return definitions
    .map(definition => ({
      key: definition.key,
      label: definition.label,
      value: metricValue(runtime, definition.keys),
      unit: definition.unit ?? null,
      state: mapServiceState(pickString(runtime, [`${definition.key}_status`])),
    }))
    .filter(resource => resource.value !== null)
}

function normalizeThroughput(object: JsonObject): AdminDashboard['throughput'] {
  const throughput = pickObject(object, ['throughput', 'trend', 'task_throughput'])
  const source = pickArray(throughput, ['points', 'items', 'series'])
  const points: ThroughputPoint[] = source.map(item => {
    const row = asObject(item) ?? {}
    return {
      at: pickString(row, ['at', 'time', 'bucket']) ?? '',
      observed: pickNumber(row, ['observed', 'observed_count', 'executed']),
      confirmed: pickNumber(row, ['confirmed', 'confirmed_count']),
    }
  })
  return {
    period: pickString(throughput, ['period', 'range']),
    points,
  }
}

function normalizeFinance(object: JsonObject): DashboardFinance | null {
  const finance = pickObject(object, ['finance', 'billing_summary'])
  if (!finance) return null
  const priced = pickBool(finance, ['priced', 'price_configured'])
  return {
    credited_minor: pickString(finance, ['credited_minor', 'revenue_minor', 'credits_minor']),
    spend_minor: pickString(finance, ['spend_minor', 'consumption_minor', 'debit_minor']),
    pending_credit_orders: pickNumber(finance, ['pending_credit_orders', 'paid_not_credited']),
    reconciliation_count: pickNumber(finance, ['reconciliation_count', 'abnormal_orders']),
    priced,
  }
}

export function normalizeDashboard(raw: unknown): AdminDashboard {
  const object = asObject(unwrapData(raw)) ?? {}
  const limits = pickObject(object, ['limits', 'scheduler', 'concurrency']) ?? {}
  const taskCounts = pickObject(object, ['task_counts', 'counts']) ?? {}
  const tasks = pickObject(object, ['tasks'])
  const byState = pickObject(tasks, ['by_state'])
  const finance = normalizeFinance(object)
  return {
    generated_at: pickString(object, ['generated_at', 'updated_at']),
    metrics: normalizeMetrics(object),
    limits: {
      global_concurrency: pickNumber(limits, ['global_concurrency', 'global_limit']),
      account_concurrency: pickNumber(limits, ['account_concurrency', 'account_limit']),
      task_exclusive: pickBool(limits, ['task_exclusive', 'exclusive_task']),
      running_tasks:
        pickNumber(taskCounts, ['running', 'running_tasks']) ??
        pickNumber(tasks, ['active_attempts']) ??
        pickNumber(object, ['running_tasks', 'active_tasks']),
      queued_tasks:
        pickNumber(taskCounts, ['queued', 'pending', 'queued_tasks']) ??
        pickNumber(byState, ['pending']) ??
        pickNumber(object, ['queued_tasks', 'queue_depth']),
    },
    resources: normalizeResources(object),
    throughput: normalizeThroughput(object),
    state_counts: Object.entries(byState ?? {})
      .map(([state, count]) => ({ state, count: typeof count === 'number' ? count : Number(count) }))
      .filter(item => Number.isFinite(item.count)),
    finance,
    recent_tasks: pickArray(object, ['recent_tasks', 'tasks']).map(item => normalizeTaskSummary(asObject(item) ?? {})),
    events: pickArray(object, ['events', 'recent_events']).map(item => normalizeEvent(asObject(item) ?? {})),
    sections_unavailable: pickArray(object, ['sections_unavailable', 'unavailable_sections']).filter(
      (item): item is string => typeof item === 'string',
    ),
  }
}

function normalizePriceView(value: unknown): PriceView {
  const price = asObject(value) ?? {}
  return {
    scope: pickString(price, ['scope']) ?? 'unknown',
    configured: pickBool(price, ['configured']) ?? false,
    state: pickString(price, ['state']) ?? 'unconfigured',
    price_version_id: pickString(price, ['price_version_id']),
    price_version: pickNumber(price, ['price_version']),
    unit_label: pickString(price, ['unit_label']),
    unit_precision: pickNumber(price, ['unit_precision']),
    price_micros: pickString(price, ['price_micros']),
    input_micros_per_million: pickString(price, ['input_micros_per_million']),
    output_micros_per_million: pickString(price, ['output_micros_per_million']),
    settlement_rule: pickString(price, ['settlement_rule']),
    rounding: pickString(price, ['rounding']),
    min_charge_minor: pickString(price, ['min_charge_minor']),
    settlement_threshold_micros: pickString(price, ['settlement_threshold_micros']),
  }
}

export function normalizeBilling(raw: unknown, etag: string | null = null): BillingSettings {
  const object = asObject(unwrapData(raw)) ?? {}
  const billing = pickObject(object, ['billing', 'settings']) ?? object
  return {
    version: versionFrom(raw, etag) || null,
    updated_at: pickString(billing, ['updated_at']),
    service_charge_enabled: pickBool(billing, ['service_charge_enabled']) ?? false,
    tiku_charge_enabled: pickBool(billing, ['tiku_charge_enabled']) ?? false,
    charging_allowed: pickBool(billing, ['charging_allowed']) ?? false,
    effective_service_charge_enabled: pickBool(billing, ['effective_service_charge_enabled']) ?? false,
    effective_tiku_charge_enabled: pickBool(billing, ['effective_tiku_charge_enabled']) ?? false,
    automatic_charging: pickBool(billing, ['automatic_charging']) ?? false,
    zpay_checkout_enabled: pickBool(billing, ['zpay_checkout_enabled']) ?? false,
    zpay_refund_enabled: pickBool(billing, ['zpay_refund_enabled']) ?? false,
    default_channel: pickString(billing, ['default_channel']),
    min_recharge_minor: pickString(billing, ['min_recharge_minor']),
    max_recharge_minor: pickString(billing, ['max_recharge_minor']),
    payment_provider: pickString(billing, ['payment_provider']),
    checkout_ready: pickBool(billing, ['checkout_ready']) ?? false,
    refund_ready: pickBool(billing, ['refund_ready']) ?? false,
    service: normalizePriceView(billing.service),
    tiku: normalizePriceView(billing.tiku),
  }
}

function normalizeStringMap(value: unknown): Array<{ key: string; value: string }> {
  const object = asObject(value)
  if (!object) return []
  return Object.entries(object).map(([key, item]) => ({
    key,
    value: formatStringValue(item),
  }))
}

export function normalizeRuntimeSettings(raw: unknown): RuntimeSettings {
  const object = asObject(unwrapData(raw)) ?? {}
  const storage = pickObject(object, ['storage'])
  const backup = pickObject(object, ['backup'])
  return {
    runtime: normalizeStringMap(object.runtime),
    storage: storage
      ? {
          status: pickString(storage, ['status']) ?? 'unknown',
          used_percent: pickNumber(storage, ['used_percent']),
          available_bytes: pickString(storage, ['available_bytes']),
        }
      : null,
    backup: backup
      ? {
          status: pickString(backup, ['status']) ?? 'unknown',
          last_recoverable_at: pickString(backup, ['last_recoverable_at']),
          verified_at: pickString(backup, ['verified_at']),
          required: pickBool(backup, ['required']),
        }
      : null,
    retention: normalizeStringMap(object.retention),
    generated_at: pickString(object, ['generated_at']),
  }
}

export function normalizeEvent(record: JsonObject): AdminEvent {
  return {
    event_id: pickString(record, ['event_id', 'id']) ?? '',
    type: pickString(record, ['type', 'event']) ?? 'unknown',
    resource_id: pickString(record, ['resource_id']),
    resource_version: pickNumber(record, ['resource_version', 'version']),
    title: pickString(record, ['title', 'summary']) ?? '运行事件',
    detail: pickString(record, ['detail', 'message']),
    tone: pickString(record, ['tone', 'level']),
    occurred_at: pickString(record, ['occurred_at', 'created_at', 'at']),
  }
}

export function normalizeAdminUserRecord(record: JsonObject): import('./types').AdminUserRecord {
  return {
    id: pickString(record, ['id', 'admin_id']) ?? '',
    username: pickString(record, ['username']) ?? '',
    display_name: pickString(record, ['display_name']) ?? '',
    role: pickString(record, ['role']) ?? 'observer',
    disabled: pickBool(record, ['disabled']) ?? false,
    totp_enabled: pickBool(record, ['totp_enabled']) ?? false,
    version: pickNumber(record, ['version']) ?? 0,
    created_at: pickString(record, ['created_at']),
    updated_at: pickString(record, ['updated_at']),
  }
}

export function normalizeAdminUsersDirectory(raw: unknown): import('./types').AdminUsersDirectory {
  const object = asObject(unwrapData(raw)) ?? {}
  const rolePermissions: Record<string, string[]> = {}
  for (const [role, values] of Object.entries(asObject(object.role_permissions) ?? {})) {
    rolePermissions[role] = Array.isArray(values)
      ? values.filter((value): value is string => typeof value === 'string')
      : []
  }
  return {
    admin_users: pickArray(object, ['admin_users', 'items']).map(item => normalizeAdminUserRecord(asObject(item) ?? {})),
    roles: pickArray(object, ['roles']).filter((role): role is string => typeof role === 'string'),
    role_permissions: rolePermissions,
  }
}

export function normalizeAdminRoleChange(raw: unknown): import('./types').AdminRoleChangeResult {
  const object = asObject(unwrapData(raw)) ?? {}
  return {
    admin_user: normalizeAdminUserRecord(asObject(unwrapData(object.admin_user)) ?? {}),
    sessions_revoked: pickNumber(object, ['sessions_revoked']) ?? 0,
    current_session_revoked: pickBool(object, ['current_session_revoked']) ?? false,
  }
}

export function normalizeAdminSessionsRevoke(raw: unknown): import('./types').AdminSessionsRevokeResult {
  const object = asObject(unwrapData(raw)) ?? {}
  return {
    revoked_sessions: pickNumber(object, ['revoked_sessions']) ?? 0,
    current_session_revoked: pickBool(object, ['current_session_revoked']) ?? false,
  }
}


export function normalizeAdminUserWallet(raw: unknown): import('./types').AdminUserWallet {
  const object = asObject(unwrapData(raw)) ?? {}
  const wallet = normalizeWalletSnapshot(object.wallet ?? object)
  return {
    user_id: pickString(object, ['user_id']) ?? '',
    display_name: pickString(object, ['display_name']),
    wallet,
    wallet_created: pickBool(object, ['wallet_created']) ?? false,
  }
}

export function normalizeAdminWalletItem(record: JsonObject): import('./types').AdminWalletItem {
  return {
    user_id: pickString(record, ['user_id']) ?? '',
    display_name: pickString(record, ['display_name']),
    wallet: normalizeWalletSnapshot(record.wallet),
  }
}

export function normalizeFinanceSummary(raw: unknown): import('./types').FinanceSummary {
  const object = asObject(unwrapData(raw)) ?? {}
  const range = pickObject(object, ['range']) ?? {}
  const rangeMetrics = pickObject(object, ['range_metrics']) ?? {}
  const current = pickObject(object, ['current']) ?? {}
  const wallets = pickObject(current, ['wallets']) ?? {}
  const metric = (source: JsonObject | null, key: string) => {
    const item = pickObject(source, [key]) ?? {}
    return {
      count: pickNumber(item, ['count']) ?? 0,
      amount_minor: pickString(item, ['amount_minor']) ?? '0',
    }
  }
  return {
    as_of: pickString(object, ['as_of']),
    range: {
      from: pickString(range, ['from']),
      to: pickString(range, ['to']),
      all_time: pickBool(range, ['all_time']) ?? true,
      to_exclusive: pickBool(range, ['to_exclusive']) ?? true,
    },
    range_metrics: {
      orders_created: metric(rangeMetrics, 'orders_created'),
      payment_succeeded: metric(rangeMetrics, 'payment_succeeded'),
      payment_credited: metric(rangeMetrics, 'payment_credited'),
      refund_succeeded: metric(rangeMetrics, 'refund_succeeded'),
      ledger_captured: metric(rangeMetrics, 'ledger_captured'),
      ledger_refund_payout: metric(rangeMetrics, 'ledger_refund_payout'),
      ledger_manual_credit: metric(rangeMetrics, 'ledger_manual_credit'),
      ledger_manual_debit: metric(rangeMetrics, 'ledger_manual_debit'),
    },
    current: {
      wallets: {
        count: pickNumber(wallets, ['count']) ?? 0,
        available_minor: pickString(wallets, ['available_minor']) ?? '0',
        reserved_minor: pickString(wallets, ['reserved_minor']) ?? '0',
        tiku_accrued_micros: pickString(wallets, ['tiku_accrued_micros']) ?? '0',
        frozen_count: pickNumber(wallets, ['frozen_count']) ?? 0,
      },
      paid_not_credited: metric(current, 'paid_not_credited'),
      orders_needs_review: metric(current, 'orders_needs_review'),
      refunds_open: metric(current, 'refunds_open'),
    },
  }
}

export function normalizeDataRequest(record: JsonObject): import('./types').DataRequestRecord {
  return {
    id: pickString(record, ['id']) ?? '',
    user_id: pickString(record, ['user_id']),
    kind: pickString(record, ['kind']) ?? 'unknown',
    status: pickString(record, ['status']) ?? 'unknown',
    created_at: pickString(record, ['created_at']),
    updated_at: pickString(record, ['updated_at']),
    expires_at: pickString(record, ['expires_at']),
    error_code: pickString(record, ['error_code']),
    review_reason: pickString(record, ['review_reason']),
  }
}

export function normalizePrivacyReview(raw: unknown): import('./types').PrivacyReviewResult {
  const object = asObject(unwrapData(raw)) ?? {}
  return {
    id: pickString(object, ['id']) ?? '',
    status: pickString(object, ['status']) ?? 'unknown',
    closed: pickBool(object, ['closed']) ?? false,
    message: pickString(object, ['message']),
  }
}
