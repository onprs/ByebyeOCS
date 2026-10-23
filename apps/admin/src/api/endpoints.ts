import { apiGet, apiRequest, healthCheck, newIdempotencyKey } from './client'
import type { ApiResponse } from './client'
import {
  asObject,
  normalizeAdminRoleChange,
  normalizeDataRequest,
  normalizeAdminUserWallet,
  normalizeAdminWalletItem,
  normalizeFinanceSummary,
  normalizeAdminSessionsRevoke,
  normalizeAdminUsersDirectory,
  normalizeAuditEvent,
  normalizeAuthSession,
  normalizeBilling,
  normalizeCapabilities,
  normalizeDashboard,
  normalizeEvent,
  normalizeList,
  normalizeMe,
  normalizeModel,
  normalizeOrder,
  normalizePrivacyReview,
  normalizePreferences,
  normalizeQuestionQuery,
  normalizeQuestionUsage,
  normalizeReconciliation,
  normalizeRefund,
  normalizeRuntimeSettings,
  normalizeTaskDetail,
  normalizeTaskSummary,
  normalizeUser,
  normalizeUserDetail,
  normalizeWalletEntry,
  normalizeWalletSnapshot,
  pickArray,
  pickBool,
  pickNumber,
  pickObject,
  pickString,
  unwrapData,
} from './normalize'
import { setCsrfToken } from './session'
import type {
  AdminAuthSession,
  DataRequestPage,
  PrivacyReviewAction,
  PrivacyReviewResult,
  AdminUserWallet,
  AdminWalletPage,
  FinanceSummary,
  AdminRoleChangeResult,
  AdminSessionsRevokeResult,
  AdminUsersDirectory,
  AdminCapabilities,
  AdminDashboard,
  AdminEvent,
  AdminMe,
  AdminPreferences,
  AuditEvent,
  BillingSettings,
  BulkOperationResult,
  ColorScheme,
  HealthStatus,
  Order,
  Paginated,
  QuestionModel,
  QuestionQuery,
  QuestionUsageSummary,
  Reconciliation,
  Refund,
  RuntimeSettings,
  TaskDetail,
  TaskSummary,
  ThemeMode,
  UserAccount,
  UserDetail,
  WalletEntry,
  WalletSnapshot,
} from './types'

export interface LoginResult {
  me: AdminMe
  csrfToken: string | null
  raw: unknown
}

export interface UserListParams {
  search?: string
  cursor?: string | null
  limit?: number
}

export interface TaskListParams {
  states?: string[]
  cursor?: string | null
  limit?: number
  userId?: string
  courseId?: string
  planId?: string
}

export interface OrderListParams {
  status?: string
  paymentStatus?: string
  creditStatus?: string
  userId?: string
  cursor?: string | null
  limit?: number
}

export interface AuditListParams {
  cursor?: string | null
  limit?: number
}

export interface QueryListParams {
  status?: string
  offeringId?: string
  cursor?: string | null
  limit?: number
}

export interface WalletEntriesResult {
  list: Paginated<WalletEntry>
  wallet: WalletSnapshot | null
}

function queryString(params: Record<string, string | number | null | undefined | string[]>): string {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === null || value === undefined || value === '') continue
    if (Array.isArray(value)) {
      for (const item of value) {
        if (item !== '') search.append(key, item)
      }
      continue
    }
    search.set(key, String(value))
  }
  const text = search.toString()
  return text ? `?${text}` : ''
}

function bulkResult(raw: unknown): BulkOperationResult {
  const unwrapped = unwrapData(raw)
  const object = asObject(unwrapped) ?? {}
  const itemsRaw = pickArray(object, ['items', 'results', 'operations'])
  const items = itemsRaw.map(item => {
    const row = asObject(item) ?? {}
    const ok = pickBool(row, ['ok'])
    const status = pickString(row, ['status', 'state', 'result'])
    const code = pickString(row, ['code'])
    const message = pickString(row, ['message', 'detail', 'error'])
    return {
      id: pickString(row, ['id', 'task_id', 'user_id', 'resource_id']) ?? '',
      status: ok === true ? 'updated' : ok === false ? 'failed' : status ?? 'unknown',
      message: [code, message].filter(Boolean).join(' · ') || null,
      applied: pickBool(row, ['applied']),
      stopRequested: pickBool(row, ['stop_requested', 'stopRequest']),
    }
  })
  const updated =
    pickNumber(object, ['updated', 'updated_count', 'applied']) ??
    (items.length ? items.filter(item => item.status === 'updated').length : null)
  const skipped =
    pickNumber(object, ['skipped', 'skipped_count', 'failed_count']) ??
    (items.length ? items.filter(item => item.status !== 'updated').length : null)
  return {
    updated,
    skipped,
    requestId: pickString(object, ['request_id']),
    items,
  }
}

// —— 认证与能力 ——

export async function getCapabilities(): Promise<ApiResponse<AdminCapabilities>> {
  const response = await apiGet<unknown>('/capabilities', { skipUnauthorizedHandler: true })
  return { data: normalizeCapabilities(response.data), etag: response.etag }
}

export async function getAuthSession(): Promise<ApiResponse<AdminAuthSession>> {
  const response = await apiGet<unknown>('/auth/session', { skipUnauthorizedHandler: true })
  const session = normalizeAuthSession(response.data)
  if (session.csrf_token) setCsrfToken(session.csrf_token)
  return { data: session, etag: response.etag }
}

export async function login(input: { username: string; password: string; totp: string }): Promise<LoginResult> {
  // 登录失败不应触发全局会话清理，由表单展示服务端错误。
  const response = await apiRequest<unknown>('/auth/login', {
    method: 'POST',
    body: { username: input.username, password: input.password, totp_code: input.totp },
    skipUnauthorizedHandler: true,
    retryCsrf: false,
  })
  const data = asObject(unwrapData(response.data)) ?? {}
  const token = pickString(data, ['csrf_token', 'csrfToken'])
  if (token) setCsrfToken(token)
  return {
    me: normalizeMe(data),
    csrfToken: token,
    raw: response.data,
  }
}

export async function getMe(): Promise<ApiResponse<AdminMe>> {
  const response = await apiGet<unknown>('/me', { skipUnauthorizedHandler: true })
  return {
    data: normalizeMe(response.data),
    etag: response.etag,
  }
}

export async function stepUp(input: { password: string; totp: string }): Promise<string | null> {
  const response = await apiRequest<unknown>('/auth/step-up', {
    method: 'POST',
    body: { password: input.password, totp_code: input.totp },
    retryCsrf: true,
  })
  const data = asObject(unwrapData(response.data)) ?? {}
  const token = pickString(data, ['csrf_token'])
  if (token) setCsrfToken(token)
  return token
}

export async function logout(): Promise<void> {
  await apiRequest<unknown>('/auth/logout', {
    method: 'POST',
    body: {},
    skipUnauthorizedHandler: true,
  })
}

export async function getPreferences(): Promise<ApiResponse<AdminPreferences>> {
  const response = await apiGet<unknown>('/me/preferences')
  return { data: normalizePreferences(response.data, response.etag), etag: response.etag }
}

export async function updatePreferences(
  input: { color_scheme: ColorScheme; theme_mode: ThemeMode },
  version: number,
): Promise<ApiResponse<AdminPreferences>> {
  const response = await apiRequest<unknown>('/me/preferences', {
    method: 'PATCH',
    body: input,
    ifMatch: version > 0 ? `"v${version}"` : null,
    retryCsrf: true,
  })
  const data = normalizePreferences(response.data, response.etag)
  if (response.data === null || response.data === undefined) {
    data.color_scheme = input.color_scheme
    data.theme_mode = input.theme_mode
  }
  if (data.version === 0 && version > 0) data.version = version + 1
  return { data, etag: response.etag }
}

// —— 总览与事件 ——

export async function getDashboard(): Promise<ApiResponse<AdminDashboard>> {
  const response = await apiGet<unknown>('/dashboard')
  return { data: normalizeDashboard(response.data), etag: response.etag }
}

const eventTitles: Record<string, string> = {
  'task.state_changed': '任务状态已更新',
  'task.confirmed': '任务已获平台确认',
  'task.recovery_verification': '崩溃恢复后的任务核验',
  'task.platform_confirmed_after_cancel': '取消后平台补记确认',
  'task.control_applied': '任务控制已应用',
  'task.control_accepted': '任务停止请求已接受',
  'account.operation_applied': '账号操作已应用',
  'plan.revised': '安排已更新',
  'plan.control_applied': '安排启停已应用',
  'wallet.updated': '钱包余额已更新',
  'order.updated': '订单状态已更新',
  'question.query.updated': '题库查询已更新',
  'question.cache.invalidated': '题库缓存已失效',
}

export function normalizeSseEvent(raw: string, eventType = ''): AdminEvent | null {
  try {
    const data = JSON.parse(raw) as unknown
    const object = asObject(data)
    if (!object) return null
    const type = eventType || pickString(object, ['type']) || 'unknown'
    const event = normalizeEvent({ ...object, type })
    if (!event.title || event.title === '运行事件') {
      event.title = eventTitles[type] ?? '运行事件'
    }
    if (!event.detail) {
      const state = pickString(object, ['state', 'status'])
      const billState = pickString(object, ['billing_state'])
      const cacheHit = pickBool(object, ['cache_hit'])
      const parts = [
        state ? `状态：${state}` : null,
        billState ? `计费：${billState}` : null,
        cacheHit === null ? null : cacheHit ? '缓存命中' : '未命中缓存',
        event.resource_version !== null ? `版本 ${event.resource_version}` : null,
      ].filter(Boolean)
      event.detail = parts.length ? parts.join(' · ') : null
    }
    return event
  } catch {
    return null
  }
}

// —— 用户与账号 ——

export async function listUsers(params: UserListParams = {}): Promise<ApiResponse<Paginated<UserAccount>>> {
  const response = await apiGet<unknown>(`/users${queryString({ ...params, limit: params.limit ?? 25 })}`)
  return { data: normalizeList(response.data, normalizeUser), etag: response.etag }
}

export async function getUser(id: string): Promise<ApiResponse<UserDetail>> {
  const response = await apiGet<unknown>(`/users/${encodeURIComponent(id)}`)
  return { data: normalizeUserDetail(response.data), etag: response.etag }
}

export async function runAccountOperation(input: {
  operation: 'pause' | 'resume' | 'limit' | 'release'
  userIds: string[]
  reason: string
}, idempotencyKey?: string): Promise<BulkOperationResult> {
  const actionMap = {
    pause: 'pause_plans',
    resume: 'resume_plans',
    limit: 'block',
    release: 'unblock',
  } as const
  const response = await apiRequest<unknown>('/account-operations', {
    method: 'POST',
    idempotencyKey: idempotencyKey ?? newIdempotencyKey(),
    body: {
      action: actionMap[input.operation],
      user_ids: input.userIds,
      reason: input.reason,
    },
  })
  return bulkResult(response.data)
}

// —— 任务 ——

export async function listTasks(params: TaskListParams = {}): Promise<ApiResponse<Paginated<TaskSummary>>> {
  const response = await apiGet<unknown>(
    `/tasks${queryString({
      state: params.states ?? [],
      user_id: params.userId,
      course_id: params.courseId,
      plan_id: params.planId,
      cursor: params.cursor,
      limit: params.limit ?? 25,
    })}`,
  )
  return { data: normalizeList(response.data, normalizeTaskSummary), etag: response.etag }
}

export async function getTask(id: string): Promise<ApiResponse<TaskDetail>> {
  const response = await apiGet<unknown>(`/tasks/${encodeURIComponent(id)}`)
  const data = asObject(unwrapData(response.data)) ?? {}
  return {
    data: normalizeTaskDetail(pickObject(data, ['task']) ?? data),
    etag: response.etag,
  }
}

export async function runTaskOperation(input: {
  taskIds: string[]
  operation: 'pause' | 'resume' | 'cancel' | 'reschedule'
  reason: string
}, idempotencyKey?: string): Promise<BulkOperationResult> {
  const actionMap = {
    pause: 'pause',
    resume: 'resume',
    cancel: 'cancel',
    reschedule: 'redispatch',
  } as const
  const response = await apiRequest<unknown>('/task-operations', {
    method: 'POST',
    idempotencyKey: idempotencyKey ?? newIdempotencyKey(),
    body: {
      task_ids: input.taskIds,
      action: actionMap[input.operation],
      reason: input.reason,
    },
  })
  return bulkResult(response.data)
}

// —— 财务 ——

export async function listOrders(params: OrderListParams = {}): Promise<ApiResponse<Paginated<Order>>> {
  const response = await apiGet<unknown>(
    `/orders${queryString({
      status: params.status,
      payment_status: params.paymentStatus,
      credit_status: params.creditStatus,
      user_id: params.userId,
      cursor: params.cursor,
      limit: params.limit ?? 25,
    })}`,
  )
  return { data: normalizeList(response.data, normalizeOrder), etag: response.etag }
}

export async function getWalletEntries(
  walletId: string,
  params: { cursor?: string | null; limit?: number } = {},
): Promise<WalletEntriesResult> {
  const response = await apiGet<unknown>(
    `/wallets/${encodeURIComponent(walletId)}/entries${queryString({ ...params, limit: params.limit ?? 25 })}`,
  )
  const object = asObject(unwrapData(response.data))
  return {
    list: normalizeList(response.data, normalizeWalletEntry),
    wallet: object?.wallet ? normalizeWalletSnapshot(object.wallet) : null,
  }
}

export async function createWalletAdjustment(input: {
  walletId: string
  direction: 'credit' | 'debit'
  amountMinor: string
  reason: string
  reference?: string
}, idempotencyKey?: string): Promise<unknown> {
  const response = await apiRequest<unknown>('/wallet-adjustments', {
    method: 'POST',
    idempotencyKey: idempotencyKey ?? newIdempotencyKey(),
    body: {
      wallet_id: input.walletId,
      direction: input.direction,
      amount_minor: input.amountMinor,
      reason: input.reason,
      reference: input.reference || undefined,
    },
  })
  return response.data
}

export async function listAuditEvents(params: AuditListParams = {}): Promise<ApiResponse<Paginated<AuditEvent>>> {
  const response = await apiGet<unknown>(
    `/audit-events${queryString({ cursor: params.cursor, limit: params.limit ?? 25 })}`,
  )
  return { data: normalizeList(response.data, normalizeAuditEvent), etag: response.etag }
}

export async function listReconciliations(
  params: { cursor?: string | null; limit?: number; status?: string; kind?: string } = {},
): Promise<ApiResponse<Paginated<Reconciliation>>> {
  const response = await apiGet<unknown>(
    `/reconciliations${queryString({ ...params, limit: params.limit ?? 25 })}`,
  )
  return { data: normalizeList(response.data, normalizeReconciliation), etag: response.etag }
}

export async function createReconciliation(input: {
  kind: string
  orderId?: string
  walletId?: string
  userId?: string
  reason: string
}, idempotencyKey?: string): Promise<Reconciliation> {
  const response = await apiRequest<unknown>('/reconciliations', {
    method: 'POST',
    idempotencyKey: idempotencyKey ?? newIdempotencyKey(),
    body: {
      kind: input.kind,
      order_id: input.orderId || undefined,
      wallet_id: input.walletId || undefined,
      user_id: input.userId || undefined,
      reason: input.reason,
    },
  })
  const data = asObject(unwrapData(response.data)) ?? {}
  return normalizeReconciliation(pickObject(data, ['reconciliation']) ?? data)
}

export async function listRefunds(
  params: { status?: string; orderId?: string; userId?: string; cursor?: string | null; limit?: number } = {},
): Promise<ApiResponse<Paginated<Refund>>> {
  const response = await apiGet<unknown>(
    `/refunds${queryString({
      status: params.status,
      order_id: params.orderId,
      user_id: params.userId,
      cursor: params.cursor,
      limit: params.limit ?? 25,
    })}`,
  )
  return { data: normalizeList(response.data, normalizeRefund), etag: response.etag }
}

export async function createRefund(input: {
  orderId: string
  kind: string
  amountMinor: string
  reason: string
}, idempotencyKey?: string): Promise<Refund> {
  const response = await apiRequest<unknown>('/refunds', {
    method: 'POST',
    idempotencyKey: idempotencyKey ?? newIdempotencyKey(),
    body: {
      order_id: input.orderId,
      kind: input.kind,
      amount_minor: input.amountMinor,
      reason: input.reason,
    },
  })
  const data = asObject(unwrapData(response.data)) ?? {}
  return normalizeRefund(pickObject(data, ['refund']) ?? data)
}

// 手动复核历史退款时只记录管理员核实的结果；服务端不会向支付渠道发起退款请求。
export async function resolveRefund(
  id: string,
  input: { outcome: 'succeeded' | 'failed'; reason: string },
  idempotencyKey?: string,
): Promise<Refund> {
  const response = await apiRequest<unknown>(`/refunds/${encodeURIComponent(id)}/resolve`, {
    method: 'POST',
    idempotencyKey: idempotencyKey ?? newIdempotencyKey(),
    body: { outcome: input.outcome, reason: input.reason },
  })
  const data = asObject(unwrapData(response.data)) ?? {}
  return normalizeRefund(pickObject(data, ['refund']) ?? data)
}


export async function getAdminUserWallet(userId: string): Promise<ApiResponse<AdminUserWallet>> {
  const response = await apiGet<unknown>(`/users/${encodeURIComponent(userId)}/wallet`)
  return { data: normalizeAdminUserWallet(response.data), etag: response.etag }
}

export async function listAdminWallets(
  params: { search?: string; cursor?: string | null; limit?: number } = {},
): Promise<ApiResponse<AdminWalletPage>> {
  const response = await apiGet<unknown>(
    `/wallets${queryString({ search: params.search, cursor: params.cursor, limit: params.limit ?? 25 })}`,
  )
  const page = normalizeList(response.data, normalizeAdminWalletItem)
  return {
    data: { items: page.items, next_cursor: page.next_cursor ?? '' },
    etag: response.etag,
  }
}

export async function getFinanceSummary(
  params: { from?: string; to?: string } = {},
): Promise<ApiResponse<FinanceSummary>> {
  const response = await apiGet<unknown>(`/finance/summary${queryString(params)}`)
  return { data: normalizeFinanceSummary(response.data), etag: response.etag }
}

// —— 题库与模型 ——

export async function listQuestionModels(
  params: { cursor?: string | null; limit?: number } = {},
): Promise<ApiResponse<Paginated<QuestionModel>>> {
  const response = await apiGet<unknown>(`/question-models${queryString({ ...params, limit: params.limit ?? 100 })}`)
  return { data: normalizeList(response.data, normalizeModel), etag: response.etag }
}

export async function createQuestionModel(input: Record<string, unknown>): Promise<QuestionModel> {
  const response = await apiRequest<unknown>('/question-models', {
    method: 'POST',
    idempotencyKey: newIdempotencyKey(),
    body: input,
  })
  const data = asObject(unwrapData(response.data)) ?? {}
  return normalizeModel(pickObject(data, ['model', 'offering']) ?? data)
}

export async function updateQuestionModel(
  id: string,
  input: Record<string, unknown>,
  version: number | null,
): Promise<QuestionModel> {
  const response = await apiRequest<unknown>(`/question-models/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: input,
    ifMatch: version !== null && version > 0 ? `"v${version}"` : null,
  })
  const data = asObject(unwrapData(response.data)) ?? {}
  return normalizeModel(pickObject(data, ['model', 'offering']) ?? data)
}

export async function testQuestionModel(
  id: string,
  kind: 'reachability' | 'functional',
): Promise<{
  kind: string | null
  status: string | null
  reachable: boolean | null
  functional: boolean | null
  latencyMs: number | null
  errorCode: string | null
  requestId: string | null
}> {
  const response = await apiRequest<unknown>(`/question-models/${encodeURIComponent(id)}/tests`, {
    method: 'POST',
    idempotencyKey: newIdempotencyKey(),
    body: { kind, confirm_cost: kind === 'functional' },
  })
  const data = asObject(unwrapData(response.data)) ?? {}
  return {
    kind: pickString(data, ['kind']),
    status: pickString(data, ['status']),
    reachable: pickBool(data, ['reachable']),
    functional: pickBool(data, ['functional']),
    latencyMs: pickNumber(data, ['latency_ms']),
    errorCode: pickString(data, ['error_code']),
    requestId: pickString(data, ['request_id']),
  }
}

export async function getQuestionUsage(
  params: { offering_id?: string } = {},
): Promise<ApiResponse<QuestionUsageSummary>> {
  const response = await apiGet<unknown>(`/question-usage${queryString(params)}`)
  return { data: normalizeQuestionUsage(response.data), etag: response.etag }
}

export async function listQuestionQueries(
  params: QueryListParams = {},
): Promise<ApiResponse<Paginated<QuestionQuery>>> {
  const response = await apiGet<unknown>(
    `/question-queries${queryString({
      status: params.status,
      offering_id: params.offeringId,
      cursor: params.cursor,
      limit: params.limit ?? 25,
    })}`,
  )
  return { data: normalizeList(response.data, normalizeQuestionQuery), etag: response.etag }
}

export async function invalidateQuestionCache(input: {
  queryId: string
  cacheEntryId: string | null
  reason: string
}): Promise<unknown> {
  const response = await apiRequest<unknown>('/question-cache/invalidations', {
    method: 'POST',
    idempotencyKey: newIdempotencyKey(),
    body: {
      query_id: input.queryId,
      cache_entry_id: input.cacheEntryId ?? undefined,
      reason: input.reason,
    },
  })
  return response.data
}


// —— 管理员账号与角色（owner） ——

export async function listAdminUsers(): Promise<ApiResponse<AdminUsersDirectory>> {
  const response = await apiGet<unknown>('/admin-users')
  return { data: normalizeAdminUsersDirectory(response.data), etag: response.etag }
}

export async function changeAdminRole(
  id: string,
  input: { role: string; reason: string },
  version: number,
): Promise<ApiResponse<AdminRoleChangeResult>> {
  const response = await apiRequest<unknown>(`/admin-users/${encodeURIComponent(id)}/role`, {
    method: 'PATCH',
    body: input,
    ifMatch: version > 0 ? `"v${version}"` : null,
  })
  return { data: normalizeAdminRoleChange(response.data), etag: response.etag }
}

export async function revokeAdminSessions(
  id: string,
  reason: string,
): Promise<AdminSessionsRevokeResult> {
  const response = await apiRequest<unknown>(`/admin-users/${encodeURIComponent(id)}/revoke-sessions`, {
    method: 'POST',
    body: { reason },
  })
  return normalizeAdminSessionsRevoke(response.data)
}


// —— 隐私与数据请求 ——

export async function listDataRequests(
  params: { cursor?: string | null; limit?: number } = {},
): Promise<ApiResponse<DataRequestPage>> {
  const response = await apiGet<unknown>(`/data-requests${queryString({ ...params, limit: params.limit ?? 25 })}`)
  const page = normalizeList(response.data, normalizeDataRequest)
  return { data: { items: page.items, next_cursor: page.next_cursor }, etag: response.etag }
}

export async function reviewDataRequest(
  id: string,
  input: { action: PrivacyReviewAction; reason: string },
): Promise<PrivacyReviewResult> {
  const response = await apiRequest<unknown>(`/data-requests/${encodeURIComponent(id)}/review`, {
    method: 'POST',
    body: input,
  })
  return normalizePrivacyReview(response.data)
}

// —— 设置与维护 ——

export async function getBillingSettings(): Promise<ApiResponse<BillingSettings>> {
  const response = await apiGet<unknown>('/settings/billing')
  return { data: normalizeBilling(response.data, response.etag), etag: response.etag }
}

export async function updateBillingSettings(
  input: Record<string, unknown>,
  version: number,
  fallback?: BillingSettings,
): Promise<ApiResponse<BillingSettings>> {
  const response = await apiRequest<unknown>('/settings/billing', {
    method: 'PATCH',
    body: input,
    ifMatch: version > 0 ? `"v${version}"` : null,
  })
  let data = normalizeBilling(response.data, response.etag)
  if ((response.data === null || response.data === undefined) && fallback) {
    data = { ...fallback, version: data.version ?? (version > 0 ? version + 1 : null) }
  } else if (data.version === null && version > 0) {
    data.version = version + 1
  }
  return { data, etag: response.etag }
}

export async function getRuntimeSettings(): Promise<ApiResponse<RuntimeSettings>> {
  const response = await apiGet<unknown>('/settings/runtime')
  return { data: normalizeRuntimeSettings(response.data), etag: response.etag }
}

export async function getMaintenance(): Promise<ApiResponse<RuntimeSettings>> {
  const response = await apiGet<unknown>('/maintenance')
  return { data: normalizeRuntimeSettings(response.data), etag: response.etag }
}

export async function getHealth(path: 'live' | 'ready'): Promise<ApiResponse<HealthStatus>> {
  const response = await healthCheck(path)
  return {
    data: { status: response.data.status, request_id: response.data.request_id },
    etag: response.etag,
  }
}
