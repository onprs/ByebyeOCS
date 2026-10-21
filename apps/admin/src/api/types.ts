export type ColorScheme = 'graphite' | 'grove' | 'indigo'
export type ThemeMode = 'system' | 'light' | 'dark'

export interface Paginated<T> {
  items: T[]
  next_cursor: string | null
}

export interface AdminMe {
  id: string
  username: string
  display_name: string
  role: string
  permissions: string[]
  totp_enabled: boolean
  authenticated_at: string | null
  session_expires_at: string | null
  fresh: boolean | null
}

export interface AdminAuthSession {
  authenticated: boolean
  csrf_token: string | null
  channel: string | null
  expires_at: string | null
  authenticated_at: string | null
  fresh: boolean | null
  permissions: string[]
  capabilities: {
    adminMethods: string[]
    stepUp: boolean
    webCookie: boolean
    totp: boolean
  }
}

export interface AdminCapabilities {
  api_version: string
  audience: string
  minimum_client_version: string | null
  authentication: {
    enabled: boolean
    login_methods: string[]
  }
  features: Record<string, boolean>
  appearance: {
    default_color_scheme: ColorScheme
    color_schemes: ColorScheme[]
    default_theme_mode: ThemeMode
    theme_modes: ThemeMode[]
  }
  pricing: {
    service_configured: boolean
    tiku_configured: boolean
    automatic_charging: boolean
  }
  request_id?: string | null
  generated_at?: string | null
}

export interface AdminPreferences {
  color_scheme: ColorScheme
  theme_mode: ThemeMode
  version: number
  notifications?: {
    task_attention?: boolean
    wallet_changes?: boolean
  }
}

export interface DashboardMetric {
  key: string
  label: string
  value: string | null
  unit: string | null
  hint: string | null
}

export type ServiceState = 'healthy' | 'degraded' | 'unavailable' | 'unknown'

export interface RuntimeResource {
  key: string
  label: string
  value: string | null
  unit: string | null
  state: ServiceState
}

export interface ThroughputPoint {
  at: string
  observed: number | null
  confirmed: number | null
}

export interface DashboardFinance {
  credited_minor: string | null
  spend_minor: string | null
  pending_credit_orders: number | null
  reconciliation_count: number | null
  priced: boolean | null
}

export interface AdminDashboard {
  generated_at: string | null
  metrics: DashboardMetric[]
  limits: {
    global_concurrency: number | null
    account_concurrency: number | null
    task_exclusive: boolean | null
    running_tasks: number | null
    queued_tasks: number | null
  }
  resources: RuntimeResource[]
  throughput: {
    period: string | null
    points: ThroughputPoint[]
  }
  state_counts: Array<{ state: string; count: number }>
  finance: DashboardFinance | null
  recent_tasks: TaskSummary[]
  events: AdminEvent[]
  sections_unavailable: string[]
}

export type UserAccountState = 'valid' | 'expired' | 'limited' | 'unknown'

export interface UserAccount {
  id: string
  display_name: string
  school: string | null
  status: UserAccountState
  status_label: string | null
  status_detail: string | null
  identity_bound: boolean | null
  identity_namespace: string | null
  blocked_code: string | null
  blocked_until: string | null
  masked_subject: string | null
  course_count: number | null
  plan_count: number | null
  task_count: number | null
  incomplete_task_count: number | null
  running_task_count: number | null
  wallet_id: string | null
  balance_minor: string | null
  cumulative_spend_minor: string | null
  account_concurrency: number | null
  last_synced_at: string | null
  last_active_at: string | null
  created_at: string | null
}

export interface UserDetail extends UserAccount {
  credential_state: string | null
  session_expires_at: string | null
  active_plan_count: number | null
  recent_tasks: TaskSummary[]
  notes: string | null
}

export type TaskState =
  | 'pending'
  | 'preparing'
  | 'running'
  | 'waiting_verification'
  | 'confirmed'
  | 'paused'
  | 'needs_attention'
  | 'failed'
  | 'cancelled'
  | 'unknown'

export interface TaskObservation {
  fraction: number | null
  source: string | null
  observed_at: string | null
  detail: string | null
}

export interface TaskPlatformEvidence {
  status: string
  passed: boolean | null
  checked_at: string | null
  basis: string | null
  source: string | null
  play_time_seconds: number | null
}

export interface TaskMediaEvidence {
  available: boolean
  records: Array<{
    id: string | null
    observed_at: string | null
    duration_seconds: number | null
    detail: string | null
  }>
  note: string | null
  source: string | null
}

export interface TaskEstimate {
  remaining_seconds_min: number | null
  remaining_seconds_max: number | null
  estimated: boolean
}

export interface TaskBilling {
  state: string | null
  configured: boolean | null
  amount_kind: string | null
  quoted_amount_minor: string | null
  currency: string | null
  price_version: string | null
  error_code: string | null
}

export interface TaskSummary {
  id: string
  title: string
  task_kind: string | null
  course_title: string | null
  chapter_title: string | null
  user_id: string | null
  user_display_name: string | null
  state: TaskState
  state_label: string | null
  progress_fraction: number | null
  platform_status: string | null
  updated_at: string | null
  last_error_code: string | null
  attempt_count: number | null
  failure_count: number | null
}

export interface TaskDetail extends TaskSummary {
  desired_state: string | null
  version: number | null
  owner_plan_id: string | null
  observation: TaskObservation | null
  platform: TaskPlatformEvidence | null
  media_evidence: TaskMediaEvidence | null
  estimate: TaskEstimate | null
  billing: TaskBilling | null
  timeline: Array<{
    at: string | null
    title: string
    detail: string | null
    tone: string | null
  }>
  can_pause: boolean | null
  can_resume: boolean | null
  can_cancel: boolean | null
  can_reschedule: boolean | null
}

export type OrderStatus =
  | 'pending'
  | 'paid'
  | 'credited'
  | 'credit_pending'
  | 'closed'
  | 'refunded'
  | 'abnormal'
  | 'unknown'

export interface Order {
  id: string
  user_id: string | null
  wallet_id: string | null
  user_display_name: string | null
  amount_minor: string
  currency: string | null
  channel: string | null
  provider: string | null
  merchant_order_no: string | null
  payment_status: string
  credit_status: string
  status: OrderStatus
  status_label: string | null
  late_payment: boolean | null
  paid_at: string | null
  credited_at: string | null
  closed_at: string | null
  expires_at: string | null
  created_at: string | null
  updated_at: string | null
  version: number | null
  checkout_state: string | null
  reconciliation_state: string | null
  note: string | null
}

export interface WalletEntry {
  id: string
  wallet_id: string | null
  kind: string
  type_label: string | null
  available_delta_minor: string
  reserved_delta_minor: string
  business_id: string | null
  description: string | null
  price_version: string | null
  usage_id: string | null
  created_at: string | null
}

export interface WalletSnapshot {
  wallet_id: string
  currency: string | null
  available_minor: string | null
  reserved_minor: string | null
  frozen: boolean | null
  frozen_reason: string | null
  tiku_accrued_micros: string | null
  version: number | null
  updated_at: string | null
}

export interface AuditEvent {
  id: string
  action: string
  action_label: string | null
  actor: string | null
  actor_kind: string | null
  resource_type: string | null
  resource_id: string | null
  reason: string | null
  summary: string | null
  request_id: string | null
  created_at: string | null
}

export interface Reconciliation {
  id: string
  kind: string
  kind_label: string | null
  order_id: string | null
  wallet_id: string | null
  user_id: string | null
  status: string
  state_label: string | null
  expected_minor: string | null
  actual_minor: string | null
  difference_minor: string | null
  error_code: string | null
  findings: Array<{ key: string; value: string }>
  requested_by: string | null
  created_at: string | null
  completed_at: string | null
}

export interface Refund {
  id: string
  order_id: string
  user_id: string | null
  wallet_id: string | null
  kind: string | null
  amount_minor: string
  currency: string | null
  reason: string | null
  status: string
  status_label: string | null
  provider: string | null
  provider_refund_no: string | null
  provider_trade_no: string | null
  last_error_code: string | null
  created_at: string | null
  updated_at: string | null
  completed_at: string | null
}

export type ModelHealth = 'configured' | 'healthy' | 'degraded' | 'unavailable' | 'unknown'

export interface ModelPricing {
  version: string
  input_micros_per_million_tokens: string
  output_micros_per_million_tokens: string
  micros_per_minor: string
  rounding: string
  rule: string
}

export interface ModelCachePolicy {
  allow: boolean
  scope: string
  ttl_days: number
}

export interface QuestionModel {
  id: string
  display_name: string
  description: string | null
  provider_ref: string | null
  capabilities: string[]
  enabled: boolean
  is_default: boolean
  sort_order: number | null
  price_version: string | null
  pricing: ModelPricing | null
  cache_policy: ModelCachePolicy
  health: ModelHealth
  health_label: string | null
  version: number | null
  created_at: string | null
  updated_at: string | null
}

export interface QuestionUsageModel {
  model_id: string
  display_name: string
  queries: number | null
  cache_hits: number | null
  input_tokens: number | null
  output_tokens: number | null
  total_tokens: number | null
  cost_micros: string | null
  pricing: ModelPricing | null
}

export interface QuestionUsageSummary {
  queries: number | null
  cache_hits: number | null
  cache_hit_rate: number | null
  input_tokens: number | null
  output_tokens: number | null
  total_tokens: number | null
  incomplete_queries: number | null
  cost_state: 'unpriced' | 'estimated' | 'quoted' | 'unavailable'
  cost_micros: string | null
  price_version: string | null
  automatic_charging: boolean | null
  unit: string | null
  by_model: QuestionUsageModel[]
}

export interface QuestionQuery {
  id: string
  user_ref: string | null
  user_display_name: string | null
  task_id: string | null
  model_id: string | null
  model_display_name: string | null
  mode: string | null
  cache_hit: boolean
  cache_entry_id: string | null
  question_title: string | null
  answer_preview: string | null
  input_tokens: number | null
  output_tokens: number | null
  total_tokens: number | null
  usage_complete: boolean
  status: string
  status_label: string | null
  pricing_state: string | null
  billing_state: string | null
  charged: boolean | null
  cost_micros: string | null
  content_expired: boolean | null
  created_at: string | null
  calls: Array<{
    slot: string
    input_tokens: number | null
    output_tokens: number | null
    total_tokens: number | null
  }>
}

export interface PriceView {
  scope: string
  configured: boolean
  state: string
  price_version_id: string | null
  price_version: number | null
  unit_label: string | null
  unit_precision: number | null
  price_micros: string | null
  input_micros_per_million: string | null
  output_micros_per_million: string | null
  settlement_rule: string | null
  rounding: string | null
  min_charge_minor: string | null
  settlement_threshold_micros: string | null
}

export interface BillingSettings {
  version: number | null
  updated_at: string | null
  service_charge_enabled: boolean
  tiku_charge_enabled: boolean
  charging_allowed: boolean
  effective_service_charge_enabled: boolean
  effective_tiku_charge_enabled: boolean
  automatic_charging: boolean
  zpay_checkout_enabled: boolean
  zpay_refund_enabled: boolean
  default_channel: string | null
  min_recharge_minor: string | null
  max_recharge_minor: string | null
  payment_provider: string | null
  checkout_ready: boolean
  refund_ready: boolean
  service: PriceView
  tiku: PriceView
}

export interface RuntimeSettings {
  runtime: Array<{ key: string; value: string }>
  storage: {
    status: string
    used_percent: number | null
    available_bytes: string | null
  } | null
  backup: {
    status: string
    last_recoverable_at: string | null
    verified_at: string | null
    required: boolean | null
  } | null
  retention: Array<{ key: string; value: string }>
  generated_at: string | null
}

export interface AdminEvent {
  event_id: string
  type: string
  resource_id: string | null
  resource_version: number | null
  title: string
  detail: string | null
  tone: string | null
  occurred_at: string | null
}

export interface HealthStatus {
  status: string
  request_id: string | null
}

export interface BulkOperationResult {
  updated: number | null
  skipped: number | null
  requestId: string | null
  items: Array<{
    id: string
    status: string
    message: string | null
    applied: boolean | null
    stopRequested: boolean | null
  }>
}

export interface AdminUserRecord {
  id: string
  username: string
  display_name: string
  role: string
  disabled: boolean
  totp_enabled: boolean
  version: number
  created_at: string | null
  updated_at: string | null
}

export interface AdminUsersDirectory {
  admin_users: AdminUserRecord[]
  roles: string[]
  role_permissions: Record<string, string[]>
}

export interface AdminRoleChangeResult {
  admin_user: AdminUserRecord
  sessions_revoked: number
  current_session_revoked: boolean
}

export interface AdminSessionsRevokeResult {
  revoked_sessions: number
  current_session_revoked: boolean
}

export interface AdminUserWallet {
  user_id: string
  display_name: string | null
  wallet: WalletSnapshot
  wallet_created: boolean
}

export interface AdminWalletItem {
  user_id: string
  display_name: string | null
  wallet: WalletSnapshot
}

export interface AdminWalletPage {
  items: AdminWalletItem[]
  next_cursor: string
}

export interface FinanceMetric {
  count: number
  amount_minor: string
}

export interface FinanceSummary {
  as_of: string | null
  range: {
    from: string | null
    to: string | null
    all_time: boolean
    to_exclusive: boolean
  }
  range_metrics: {
    orders_created: FinanceMetric
    payment_succeeded: FinanceMetric
    payment_credited: FinanceMetric
    refund_succeeded: FinanceMetric
    ledger_captured: FinanceMetric
    ledger_refund_payout: FinanceMetric
    ledger_manual_credit: FinanceMetric
    ledger_manual_debit: FinanceMetric
  }
  current: {
    wallets: {
      count: number
      available_minor: string
      reserved_minor: string
      tiku_accrued_micros: string
      frozen_count: number
    }
    paid_not_credited: FinanceMetric
    orders_needs_review: FinanceMetric
    refunds_open: FinanceMetric
  }
}

export interface DataRequestRecord {
  id: string
  user_id: string | null
  kind: 'export' | 'deletion' | string
  status: string
  created_at: string | null
  updated_at: string | null
  expires_at: string | null
  error_code: string | null
  review_reason: string | null
}

export interface DataRequestPage {
  items: DataRequestRecord[]
  next_cursor: string | null
}

export type PrivacyReviewAction = 'defer' | 'reject' | 'complete'

export interface PrivacyReviewResult {
  id: string
  status: 'deferred' | 'rejected' | 'completed' | string
  closed: boolean
  message: string | null
}

export type PrivacyBlockers = Record<string, boolean | number>
