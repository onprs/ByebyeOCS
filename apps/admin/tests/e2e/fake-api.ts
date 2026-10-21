import type { Page, Route } from '@playwright/test'

export interface FakeState {
  loggedIn: boolean
  csrf: string
  preferences: { color_scheme: string; theme_mode: string; version: number }
  billing: {
    version: number
    service_charge_enabled: boolean
    tiku_charge_enabled: boolean
    charging_allowed: boolean
    effective_service_charge_enabled: boolean
    effective_tiku_charge_enabled: boolean
    automatic_charging: boolean
    zpay_checkout_enabled: boolean
    zpay_refund_enabled: boolean
    default_channel: string
    min_recharge_minor: string
    max_recharge_minor: string
    service: Record<string, unknown>
    tiku: Record<string, unknown>
  }
  lastAccountOperation: unknown
  lastTaskOperation: unknown
  lastPrivacyReview: unknown
  dataRequests: Array<Record<string, unknown>>
  lastPreferencePatch: { ifMatch: string | null; csrf: string | null; body: unknown } | null
  adminUsers: Array<Record<string, unknown>>
}

const me = {
  id: '4b1f0c2e-9a41-4c86-8b0d-0d4a2f9c7e11',
  username: 'ops',
  display_name: '运营管理员',
  totp_enabled: true,
  session: {
    channel: 'web',
    expires_at: '2026-10-21T06:00:00Z',
    authenticated_at: '2026-10-20T06:00:00Z',
    fresh: true,
  },
  permissions: ['*'],
}

const users = [
  {
    user_id: 'u_1001',
    display_name: '林晓',
    school: '晨光大学',
    identity_bound: true,
    identity_namespace: 'chaoxing',
    course_count: 6,
    plan_count: 1,
    task_count: 4,
    incomplete_task_count: 1,
    last_synced_at: '2026-10-20T06:30:00Z',
    created_at: '2026-09-01T02:00:00Z',
  },
  {
    user_id: 'u_1002',
    display_name: '周明',
    school: '北岭学院',
    identity_bound: false,
    identity_namespace: '',
    course_count: 2,
    plan_count: 0,
    task_count: 0,
    incomplete_task_count: 0,
    last_synced_at: '2026-10-19T11:12:00Z',
    created_at: '2026-09-02T02:00:00Z',
  },
  {
    user_id: 'u_1003',
    display_name: '陈澈',
    school: '海川大学',
    identity_bound: true,
    identity_namespace: 'chaoxing',
    blocked_code: 'admin_blocked',
    blocked_reason: '人工复核中',
    course_count: 9,
    plan_count: 2,
    task_count: 8,
    incomplete_task_count: 3,
    last_synced_at: '2026-10-18T09:00:00Z',
    created_at: '2026-08-20T02:00:00Z',
  },
]

const tasks = [
  {
    id: 'task_design',
    user_id: 'u_1001',
    state: 'running',
    desired_state: 'run',
    version: 7,
    task_kind: 'video',
    title: '设计思维章节任务',
    task_point_id: 'tp_1',
    course_id: 'course_1',
    course_title: '设计思维',
    chapter_id: 'chapter_1',
    chapter_title: '第一章',
    plan_refs: [{ plan_id: 'plan_1', revision_id: 'rev_1', version: 2 }],
    attempt_count: 1,
    failure_count: 0,
    platform: { status: 'unconfirmed', passed: false, checked_at: '2026-10-20T06:31:02Z' },
    observation: { fraction: 0.42, source: 'executor', observed_at: '2026-10-20T06:31:00Z', note: '原生执行器观察值' },
    media_evidence: { available: false },
    billing: { state: 'disabled', configured: false, amount_minor: 0, currency: 'CNY' },
    estimate: { remaining_seconds_min: 120, remaining_seconds_max: 300, estimated: true },
    created_at: '2026-10-20T06:20:00Z',
    updated_at: '2026-10-20T06:31:00Z',
  },
  {
    id: 'task_english',
    user_id: 'u_1002',
    state: 'needs_attention',
    desired_state: 'run',
    version: 3,
    task_kind: 'video',
    title: '英语课巩固',
    course_id: 'course_2',
    course_title: '大学英语',
    chapter_id: 'chapter_2',
    chapter_title: 'Unit 2',
    plan_refs: [],
    attempt_count: 1,
    failure_count: 1,
    last_error_code: 'session_expired',
    platform: { status: 'unconfirmed', passed: false },
    observation: { fraction: 0.1, source: 'executor', observed_at: '2026-10-20T06:25:00Z' },
    media_evidence: { available: false },
    billing: { state: 'disabled', configured: false, amount_minor: 0 },
    estimate: { remaining_seconds_min: null, remaining_seconds_max: null, estimated: true },
    created_at: '2026-10-20T06:20:00Z',
    updated_at: '2026-10-20T06:25:00Z',
  },
  {
    id: 'task_math',
    user_id: 'u_1001',
    state: 'confirmed',
    desired_state: 'run',
    version: 9,
    task_kind: 'video',
    title: '高等数学练习',
    course_id: 'course_3',
    course_title: '高等数学',
    chapter_id: 'chapter_3',
    chapter_title: '极限',
    plan_refs: [],
    attempt_count: 2,
    failure_count: 0,
    confirmed_at: '2026-10-20T05:10:00Z',
    platform: { status: 'confirmed', passed: true, checked_at: '2026-10-20T05:10:00Z' },
    observation: { fraction: 1, source: 'executor', observed_at: '2026-10-20T05:09:00Z' },
    media_evidence: { available: false },
    billing: { state: 'disabled', configured: false, amount_minor: 0 },
    estimate: { remaining_seconds_min: null, remaining_seconds_max: null, estimated: true },
    created_at: '2026-10-20T05:00:00Z',
    updated_at: '2026-10-20T05:10:00Z',
  },
]

const taskDetail = {
  ...tasks[0],
  attempts: [
    {
      id: 'attempt_1',
      attempt_no: 1,
      state: 'running',
      fence_token: 4,
      started_at: '2026-10-20T06:30:00Z',
      heartbeat_at: '2026-10-20T06:31:00Z',
      observation: tasks[0].observation,
      platform: tasks[0].platform,
      media_evidence: tasks[0].media_evidence,
    },
  ],
  commands: [
    { id: 'cmd_1', kind: 'pause', status: 'rejected', actor_kind: 'admin', reason: '测试命令', created_at: '2026-10-20T06:29:00Z' },
  ],
}

const orders = [
  {
    id: 'ORD-20261020-001',
    user_id: 'u_1001',
    wallet_id: 'w_1001',
    amount_minor: '300000',
    currency: 'CNY',
    channel: 'alipay',
    status: 'completed',
    payment_status: 'paid',
    credit_status: 'credited',
    provider: 'zpay',
    merchant_order_no: '202610200001',
    late_payment: false,
    created_at: '2026-10-20T06:09:40Z',
    updated_at: '2026-10-20T06:10:04Z',
    expires_at: '2026-10-20T06:39:40Z',
    paid_at: '2026-10-20T06:10:00Z',
    credited_at: '2026-10-20T06:10:04Z',
    version: 3,
    checkout_state: 'completed',
  },
  {
    id: 'ORD-20261020-002',
    user_id: 'u_1002',
    wallet_id: 'w_1002',
    amount_minor: '100000',
    currency: 'CNY',
    channel: 'wxpay',
    status: 'completed',
    payment_status: 'paid',
    credit_status: 'not_credited',
    provider: 'zpay',
    merchant_order_no: '202610200002',
    late_payment: false,
    created_at: '2026-10-20T06:19:00Z',
    updated_at: '2026-10-20T06:20:00Z',
    expires_at: '2026-10-20T06:49:00Z',
    paid_at: '2026-10-20T06:20:00Z',
    version: 2,
    checkout_state: 'completed',
  },
]

const walletEntries = [
  {
    journal_id: 'J-001',
    kind: 'recharge',
    reason: '充值订单到账',
    available_delta_minor: '300000',
    reserved_delta_minor: '0',
    order_id: 'ORD-20261020-001',
    created_at: '2026-10-20T06:10:04Z',
  },
  {
    journal_id: 'J-002',
    kind: 'hold',
    reason: '任务点预占',
    available_delta_minor: '-1750',
    reserved_delta_minor: '1750',
    created_at: '2026-10-20T06:12:00Z',
  },
]

const questionModels = [
  {
    id: 'qmo_standard',
    offering_id: 'qmo_standard',
    display_name: '标准模型',
    description: '适用于常见单选与判断题',
    provider_ref: 'provider/standard',
    capabilities: ['text'],
    enabled: true,
    default: true,
    sort_order: 1,
    price_version: 'v1',
    pricing: {
      version: 'v1',
      input_micros_per_million_tokens: '2000000',
      output_micros_per_million_tokens: '8000000',
      micros_per_minor: '10000',
      rounding: 'ceil',
      rule: 'token_usage',
    },
    cache_policy: { allow: true, scope: 'user', ttl_days: 7 },
    health: 'healthy',
    version: 2,
    created_at: '2026-10-20T03:00:00Z',
    updated_at: '2026-10-20T03:00:00Z',
  },
  {
    id: 'qmo_vision',
    offering_id: 'qmo_vision',
    display_name: '图像模型',
    description: '支持含图题目',
    provider_ref: 'provider/vision',
    capabilities: ['text', 'vision'],
    enabled: false,
    default: false,
    sort_order: 2,
    price_version: '',
    pricing: null,
    cache_policy: { allow: false, scope: 'user', ttl_days: 7 },
    health: 'degraded',
    version: 1,
    created_at: '2026-10-20T03:00:00Z',
    updated_at: '2026-10-20T03:00:00Z',
  },
]

const questionQueries = [
  {
    id: 'qq_001',
    user_ref: 'u_ab12cd34ef56',
    model: { offering_id: 'qmo_standard', display_name: '标准模型' },
    mode: 'cache_first',
    status: 'answered',
    cache: { hit: false, entry_id: 'cache_abcdefghijklmnop' },
    usage: { input_tokens: 1284, output_tokens: 96, total_tokens: 1380, complete: true, calls: [{ slot: 'answer', input_tokens: 1284, output_tokens: 96, total_tokens: 1380, status: 'ok' }] },
    pricing: { state: 'quoted', cost_micros: '4716', billing_state: 'no_charge', charged: false },
    question_preview: '下列说法中正确的是？',
    created_at: '2026-10-20T06:28:00Z',
    completed_at: '2026-10-20T06:28:05Z',
  },
]

function json(route: Route, status: number, body: unknown, headers: Record<string, string> = {}) {
  return route.fulfill({
    status,
    headers: { 'Content-Type': 'application/json; charset=utf-8', 'X-Request-ID': 'req_e2e', ...headers },
    body: JSON.stringify(body),
  })
}

function apiError(route: Route, status: number, code: string, message: string, retryable = false, retryAfter?: string) {
  return json(
    route,
    status,
    { error: { code, message, request_id: `req_e2e_${status}`, retryable, scope: 'request' } },
    retryAfter ? { 'Retry-After': retryAfter } : {},
  )
}

export async function installFakeApi(
  page: Page,
  options: { role?: string; permissions?: string[] } = {},
): Promise<FakeState> {
  const sessionMe = {
    ...me,
    role: options.role ?? 'owner',
    permissions: options.permissions ?? ['*'],
  }
  const state: FakeState = {
    loggedIn: false,
    csrf: 'csrf-e2e-token',
    preferences: { color_scheme: 'graphite', theme_mode: 'system', version: 7 },
    billing: {
      version: 3,
      service_charge_enabled: true,
      tiku_charge_enabled: false,
      charging_allowed: false,
      effective_service_charge_enabled: false,
      effective_tiku_charge_enabled: false,
      automatic_charging: false,
      zpay_checkout_enabled: false,
      zpay_refund_enabled: false,
      default_channel: 'alipay',
      min_recharge_minor: '100',
      max_recharge_minor: '100000',
      service: {
        scope: 'service_task_point',
        configured: true,
        state: 'active',
        price_version_id: 'pv_1',
        price_version: 1,
        unit_label: '有效任务点',
        unit_precision: 0,
        price_micros: '1500000',
        settlement_rule: 'per_event',
        rounding: 'ceil',
        min_charge_minor: '0',
      },
      tiku: { scope: 'tiku_tokens', configured: false, state: 'unconfigured', settlement_threshold_micros: '10000' },
    },
    lastAccountOperation: null,
    lastTaskOperation: null,
    lastPrivacyReview: null,
    dataRequests: [
      {
        id: '757b2acc-fb35-4f89-a1d0-13ccbc5c4fbb',
        user_id: 'bdfe3620-547f-46b0-8278-6fd7a8b85748',
        kind: 'deletion',
        status: 'needs_review',
        created_at: '2026-10-20T06:00:00Z',
        updated_at: '2026-10-20T06:00:00Z',
        expires_at: null,
        review_reason: '',
        error_code: '',
      },
      {
        id: 'd09c31d7-95e2-45f1-9385-cedbbd86a9e8',
        user_id: 'bdfe3620-547f-46b0-8278-6fd7a8b85748',
        kind: 'export',
        status: 'ready',
        created_at: '2026-10-20T05:00:00Z',
        updated_at: '2026-10-20T05:01:00Z',
        expires_at: '2026-10-21T05:01:00Z',
        review_reason: '',
        error_code: '',
      },
    ],
    lastPreferencePatch: null,
    adminUsers: [
      {
        id: me.id,
        username: 'ops',
        display_name: '运营管理员',
        role: 'owner',
        disabled: false,
        totp_enabled: true,
        version: 1,
        created_at: '2026-09-01T02:00:00Z',
        updated_at: '2026-10-20T06:00:00Z',
      },
      {
        id: 'b7f1a2c3-4d5e-4f60-8a91-b2c3d4e5f601',
        username: 'finance.ops',
        display_name: '财务管理员',
        role: 'finance',
        disabled: false,
        totp_enabled: true,
        version: 3,
        created_at: '2026-09-02T02:00:00Z',
        updated_at: '2026-10-19T06:00:00Z',
      },
    ],
  }

  // 事件流在浏览器内以测试替身接收，避免测试环境依赖长连接。
  await page.addInitScript(() => {
    class FakeEventSource {
      url: string
      withCredentials: boolean
      readyState = 0
      private listeners = new Map<string, Set<(event: Event) => void>>()
      constructor(url: string, init?: { withCredentials?: boolean }) {
        this.url = url
        this.withCredentials = init?.withCredentials ?? false
        window.setTimeout(() => {
          this.readyState = 1
          this.emit('open', new Event('open'))
        }, 30)
      }
      addEventListener(type: string, callback: EventListenerOrEventListenerObject | null) {
        if (!callback) return
        const handler = typeof callback === 'function' ? callback : () => undefined
        const set = this.listeners.get(type) ?? new Set()
        set.add(handler as (event: Event) => void)
        this.listeners.set(type, set)
      }
      removeEventListener(type: string, callback: EventListenerOrEventListenerObject | null) {
        if (!callback) return
        this.listeners.get(type)?.delete(callback as (event: Event) => void)
      }
      close() {
        this.readyState = 2
      }
      private emit(type: string, event: Event) {
        for (const listener of this.listeners.get(type) ?? []) listener(event)
      }
    }
    Object.defineProperty(window, 'EventSource', { configurable: true, writable: true, value: FakeEventSource })
  })

  await page.route('**/admin/api/v1/**', async route => {
    const request = route.request()
    const method = request.method()
    const url = new URL(request.url())
    const path = url.pathname.replace('/admin/api/v1', '')
    let body: Record<string, unknown> | undefined
    try {
      body = (request.postDataJSON() ?? undefined) as Record<string, unknown> | undefined
    } catch {
      body = undefined
    }

    const requireCsrf = () => {
      if (method === 'GET' || path === '/auth/login') return true
      return request.headers()['x-csrf-token'] === state.csrf
    }
    const sessionPayload = (authenticated: boolean) => ({
      authenticated,
      kind: 'admin',
      fresh: authenticated,
      permissions: authenticated ? ['*'] : [],
      ...(authenticated
        ? {
            csrf_token: state.csrf,
            channel: 'web',
            expires_at: '2026-10-21T06:00:00Z',
            authenticated_at: '2026-10-20T06:00:00Z',
          }
        : {}),
      capabilities: {
        identity: { provider: 'chaoxing', methods: ['password'], qr_scan: false, verification: 'configured' },
        admin: { methods: ['password+totp'], rbac: false, step_up: true, owner_id: '*' },
        web_cookie: true,
        app_bearer: true,
        refresh_rotation: true,
        totp: true,
      },
    })

    if (path === '/capabilities' && method === 'GET') {
      return json(route, 200, {
        api_version: 'v1',
        audience: 'admin',
        minimum_client_version: null,
        authentication: { enabled: true, login_methods: ['password_totp'] },
        features: {
          learning: true,
          billing: true,
          questions: true,
          operations: true,
          preferences: true,
          events: true,
        },
        appearance: {
          default_color_scheme: 'graphite',
          color_schemes: ['graphite', 'grove', 'indigo'],
          default_theme_mode: 'system',
          theme_modes: ['system', 'light', 'dark'],
        },
        pricing: { service_configured: true, tiku_configured: false, automatic_charging: true },
        request_id: 'req_e2e',
      })
    }

    if (path === '/auth/session' && method === 'GET') {
      return json(route, 200, sessionPayload(state.loggedIn))
    }

    if (path === '/auth/login' && method === 'POST') {
      if (body?.username !== 'ops' || body?.password !== 'correct-horse' || body?.totp_code !== '123456') {
        return apiError(route, 401, 'unauthenticated', '用户名、密码或动态验证码不正确。')
      }
      state.loggedIn = true
      return json(route, 200, {
        mode: 'web',
        csrf_token: state.csrf,
        expires_at: '2026-10-21T06:00:00Z',
        permissions: sessionMe.permissions,
        admin: sessionMe,
      })
    }

    if (path === '/me' && method === 'GET') {
      if (!state.loggedIn) return apiError(route, 401, 'unauthenticated', '登录状态已失效，请重新登录。')
      return json(route, 200, sessionMe)
    }

    if (!state.loggedIn) return apiError(route, 401, 'unauthenticated', '登录状态已失效，请重新登录。')

    if (path === '/auth/logout' && method === 'POST') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      state.loggedIn = false
      return json(route, 200, { revoked: true, platform_revoked: false })
    }

    if (path === '/auth/step-up' && method === 'POST') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      if (body?.password !== 'correct-horse' || body?.totp_code !== '123456') {
        return apiError(route, 401, 'unauthenticated', '密码或验证码不正确。')
      }
      return json(route, 200, { authenticated_at: '2026-10-20T06:50:00Z', fresh_until: '2026-10-20T07:00:00Z', csrf_token: state.csrf })
    }

    if (path === '/me/preferences' && method === 'GET') {
      return json(route, 200, { ...state.preferences }, { ETag: `"v${state.preferences.version}"` })
    }

    if (path === '/me/preferences' && method === 'PATCH') {
      state.lastPreferencePatch = {
        ifMatch: request.headers()['if-match'] ?? null,
        csrf: request.headers()['x-csrf-token'] ?? null,
        body,
      }
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      if (request.headers()['if-match'] !== `"v${state.preferences.version}"`) {
        return apiError(route, 412, 'version_conflict', '外观偏好已在其他设备更新，请重新加载。')
      }
      state.preferences = {
        color_scheme: typeof body?.color_scheme === 'string' ? body.color_scheme : state.preferences.color_scheme,
        theme_mode: typeof body?.theme_mode === 'string' ? body.theme_mode : state.preferences.theme_mode,
        version: state.preferences.version + 1,
      }
      return json(route, 200, { ...state.preferences }, { ETag: `"v${state.preferences.version}"` })
    }

    if (path === '/dashboard' && method === 'GET') {
      return json(route, 200, {
        generated_at: '2026-10-20T06:32:00Z',
        tasks: {
          total: 3,
          by_state: { running: 1, needs_attention: 1, confirmed: 1, waiting_verification: 2 },
          active_attempts: 1,
          media_evidence_available: 0,
          awaiting_verification: 2,
        },
        courses: { total: 17, stale: 2 },
        plans: { active: 3, paused: 1 },
        syncs: { queued: 0, running: 1, needs_attention: 1 },
        accounts: { total: 128, bound: 96, blocked: 2 },
        capabilities: {
          platform_adapter: 'chaoxing-readonly',
          course_read: true,
          platform_check: false,
          credential_verify: false,
          execution_enabled: false,
          executor: '',
          executor_enabled: false,
          media_evidence: false,
          billing_connected: true,
          session_resolver: true,
          disabled_reasons: ['executor_not_configured', 'execution_disabled_by_configuration'],
        },
      })
    }

    if (path === '/users' && method === 'GET') {
      const search = (url.searchParams.get('search') ?? '').toLowerCase()
      const filtered = users.filter(user => !search || `${user.display_name}${user.user_id}${user.school}`.toLowerCase().includes(search))
      return json(route, 200, { items: filtered, next_cursor: null })
    }

    if (path.startsWith('/users/') && method === 'GET' && !path.endsWith('/wallet')) {
      const id = decodeURIComponent(path.slice('/users/'.length))
      const user = users.find(item => item.user_id === id)
      if (!user) return apiError(route, 404, 'not_found', '用户不存在。')
      return json(route, 200, {
        ...user,
        courses: [],
        plans: [],
        tasks: tasks.filter(task => task.user_id === id),
      })
    }

    if (path === '/account-operations' && method === 'POST') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      state.lastAccountOperation = body
      const ids = Array.isArray(body?.user_ids) ? body.user_ids : []
      return json(route, 200, {
        items: ids.map(id => ({ user_id: id, ok: true, status: body?.action })),
      })
    }

    if (path === '/tasks' && method === 'GET') {
      const states = url.searchParams.getAll('state').flatMap(value => value.split(','))
      const owner = url.searchParams.get('user_id')
      const filtered = tasks.filter(task => {
        const matchesState = states.length === 0 || states.includes(task.state)
        const matchesOwner = !owner || task.user_id === owner
        return matchesState && matchesOwner
      })
      return json(route, 200, { items: filtered, next_cursor: null })
    }

    if (path.startsWith('/tasks/') && method === 'GET') {
      const id = decodeURIComponent(path.slice('/tasks/'.length))
      const found = tasks.find(task => task.id === id)
      if (!found) return apiError(route, 404, 'not_found', '任务不存在。')
      if (id === 'task_design') return json(route, 200, taskDetail)
      return json(route, 200, { ...found, attempts: [], commands: [] })
    }

    if (path === '/task-operations' && method === 'POST') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      state.lastTaskOperation = body
      const ids = Array.isArray(body?.task_ids) ? body.task_ids : []
      return json(route, 200, {
        items: ids.map(id => ({ task_id: id, ok: true, state: 'paused', applied: true, stop_requested: false })),
      })
    }

    if (path === '/orders' && method === 'GET') {
      const status = url.searchParams.get('status')
      const payment = url.searchParams.get('payment_status')
      const credit = url.searchParams.get('credit_status')
      const userID = url.searchParams.get('user_id')
      const filtered = orders.filter(order =>
        (!status || order.status === status) &&
        (!payment || order.payment_status === payment) &&
        (!credit || order.credit_status === credit) &&
        (!userID || order.user_id === userID),
      )
      return json(route, 200, { items: filtered, next_cursor: null })
    }

    if (path.startsWith('/users/') && path.endsWith('/wallet') && method === 'GET') {
      const userId = decodeURIComponent(path.slice('/users/'.length, -'/wallet'.length))
      return json(route, 200, {
        user_id: userId,
        display_name: users.find(user => user.user_id === userId)?.display_name ?? '',
        wallet: {
          id: userId === 'u_1002' ? 'w_1002' : 'w_1001',
          currency: 'CNY',
          available_minor: '298250',
          reserved_minor: '1750',
          tiku_accrued_micros: '0',
          frozen: false,
          version: 4,
          updated_at: '2026-10-20T06:12:00Z',
        },
        wallet_created: false,
      })
    }

    if (path === '/wallets' && method === 'GET') {
      const search = (url.searchParams.get('search') ?? '').toLowerCase()
      const items = [users[0], users[1]]
        .filter(user => !search || `${user.display_name}${user.user_id}`.toLowerCase().includes(search))
        .map(user => ({
          user_id: user.user_id,
          display_name: user.display_name,
          wallet: {
            id: user.user_id === 'u_1001' ? 'w_1001' : 'w_1002',
            currency: 'CNY',
            available_minor: user.user_id === 'u_1001' ? '298250' : '1000',
            reserved_minor: '0',
            tiku_accrued_micros: '0',
            frozen: false,
            version: 4,
            updated_at: '2026-10-20T06:12:00Z',
          },
        }))
      return json(route, 200, { items, next_cursor: '' })
    }

    if (path === '/finance/summary' && method === 'GET') {
      return json(route, 200, {
        as_of: '2026-10-20T06:32:00Z',
        range: { from: '', to: '', all_time: true, to_exclusive: true },
        range_metrics: {
          orders_created: { count: 2, amount_minor: '400000' },
          payment_succeeded: { count: 2, amount_minor: '400000' },
          payment_credited: { count: 1, amount_minor: '300000' },
          refund_succeeded: { count: 0, amount_minor: '0' },
          ledger_captured: { count: 1, amount_minor: '1750' },
          ledger_refund_payout: { count: 0, amount_minor: '0' },
          ledger_manual_credit: { count: 1, amount_minor: '1000' },
          ledger_manual_debit: { count: 0, amount_minor: '0' },
        },
        current: {
          wallets: {
            count: 2,
            available_minor: '299250',
            reserved_minor: '1750',
            tiku_accrued_micros: '0',
            frozen_count: 0,
          },
          paid_not_credited: { count: 1, amount_minor: '100000' },
          orders_needs_review: { count: 0, amount_minor: '0' },
          refunds_open: { count: 0, amount_minor: '0' },
        },
      })
    }

    if (path === '/refunds' && method === 'GET') {
      return json(route, 200, {
        items: [
          {
            id: 'refund_1',
            order_id: 'ORD-20261020-001',
            user_id: 'u_1001',
            wallet_id: 'w_1001',
            kind: 'manual_correction',
            amount_minor: '500',
            currency: 'CNY',
            status: 'succeeded',
            reason: '异常订单更正',
            provider: 'zpay',
            provider_refund_no: 'ZP-REF-1',
            provider_trade_no: 'ZP-TRADE-1',
            created_at: '2026-10-20T06:40:00Z',
            updated_at: '2026-10-20T06:41:00Z',
            completed_at: '2026-10-20T06:41:00Z',
          },
        ],
        next_cursor: '',
      })
    }

    if (path.startsWith('/wallets/') && path.endsWith('/entries') && method === 'GET') {
      const walletId = decodeURIComponent(path.slice('/wallets/'.length, -'/entries'.length))
      return json(route, 200, {
        wallet: {
          id: walletId,
          user_id: 'u_1001',
          available_minor: '298250',
          reserved_minor: '1750',
          frozen: false,
          updated_at: '2026-10-20T06:12:00Z',
        },
        items: walletEntries,
        next_cursor: null,
      })
    }

    if (path === '/wallet-adjustments' && method === 'POST') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      return json(route, 200, {
        wallet: { id: body?.wallet_id, user_id: 'u_1001', available_minor: '300250', reserved_minor: '1750' },
        journal_id: 'J-ADJ-001',
        direction: body?.direction,
        amount_minor: body?.amount_minor,
      })
    }

    if (path === '/admin-users' && method === 'GET') {
      return json(route, 200, {
        admin_users: state.adminUsers,
        roles: ['owner', 'operator', 'support', 'finance', 'observer'],
        role_permissions: {
          owner: ['*'],
          operator: ['admin.self', 'learning.read', 'learning.tasks.operate', 'learning.accounts.operate', 'operations.read', 'events.read', 'question:read'],
          support: ['admin.self', 'learning.read', 'question:read', 'privacy.review'],
          finance: ['admin.self', 'billing.read', 'billing.manage', 'wallet.adjust', 'wallet.refund', 'reconciliation.manage'],
          observer: ['admin.self', 'learning.read', 'billing.read', 'question:read', 'operations.read', 'audit.read', 'events.read'],
        },
      })
    }

    if (path.startsWith('/admin-users/') && path.endsWith('/role') && method === 'PATCH') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      const id = decodeURIComponent(path.slice('/admin-users/'.length, -'/role'.length))
      const target = state.adminUsers.find(item => item.id === id)
      if (!target) return apiError(route, 404, 'not_found', '管理员账号不存在')
      if (request.headers()['if-match'] !== `"v${target.version}"`) {
        return apiError(route, 412, 'version_conflict', '管理员角色已更新，请重新加载。')
      }
      target.role = typeof body?.role === 'string' ? body.role : target.role
      target.version = Number(target.version) + 1
      target.updated_at = '2026-10-20T07:00:00Z'
      return json(route, 200, {
        admin_user: target,
        sessions_revoked: 1,
        current_session_revoked: false,
      }, { ETag: `"v${target.version}"` })
    }

    if (path.startsWith('/admin-users/') && path.endsWith('/revoke-sessions') && method === 'POST') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      return json(route, 200, { revoked_sessions: 2, current_session_revoked: false })
    }

    if (path === '/data-requests' && method === 'GET') {
      return json(route, 200, { items: state.dataRequests, next_cursor: null })
    }

    if (/^\/data-requests\/[^/]+\/review$/.test(path) && method === 'POST') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      const id = path.split('/')[2]
      const target = state.dataRequests.find(item => item.id === id)
      if (!target) return apiError(route, 404, 'not_found', '删除申请不存在。')
      state.lastPrivacyReview = { id, ...body }
      if (body?.action === 'complete' && target.status === 'needs_review') {
        return json(route, 409, {
          error: {
            code: 'privacy_blocked',
            message: '存在未结资金或运行中的活动，暂不能完成删除；申请已保留在 needs_review',
            request_id: 'req_privacy_blocked',
            retryable: false,
            scope: 'request',
          },
          blockers: {
            wallet_balances: false,
            open_holds: 0,
            unsettled_orders: 0,
            open_refunds: 0,
            open_reconciliations: 0,
            unsettled_usage: 0,
            question_usage: 0,
            pending_cache_reports: 0,
            pending_payment_inbox: 0,
            active_attempts: 0,
            active_tasks: 2,
            billing_blocked_tasks: 0,
            active_course_syncs: 0,
          },
        })
      }
      if (body?.action === 'reject') {
        target.status = 'rejected'
        target.review_reason = body?.reason ?? ""
      } else if (body?.action === 'defer') {
        target.status = 'deferred'
        target.review_reason = body?.reason ?? ""
      } else {
        target.status = 'completed'
        target.review_reason = body?.reason ?? ""
      }
      target.updated_at = '2026-10-20T06:10:00Z'
      return json(route, 200, {
        id,
        status: target.status,
        closed: target.status === 'completed',
        message: target.status === 'rejected'
          ? '已驳回删除申请；可重新验证身份进入，任务保持暂停，不会自动恢复旧会话或执行'
          : target.status === 'deferred' ? '已记录延后处理，删除尚未完成' : '账号已关闭',
      })
    }

    if (path === '/audit-events' && method === 'GET') {
      return json(route, 200, {
        items: [
          {
            id: 'audit_1',
            actor_kind: 'admin',
            actor_id: me.id,
            action: 'wallet.adjusted',
            resource_type: 'wallet',
            resource_id: 'w_1001',
            reason: '经复核的异常订单更正',
            request_id: 'req_audit_1',
            occurred_at: '2026-10-20T06:20:00Z',
          },
        ],
        next_cursor: null,
      })
    }

    if (path === '/reconciliations' && method === 'GET') {
      return json(route, 200, {
        items: [
          {
            id: 'rec_1',
            kind: 'payment_orders',
            status: 'mismatch',
            target_user_id: 'u_1002',
            target_wallet_id: 'w_1002',
            target_order_id: 'ORD-20261020-002',
            findings: { mismatched: '1' },
            expected_minor: '100000',
            actual_minor: '0',
            difference_minor: '100000',
            requested_by: me.id,
            created_at: '2026-10-20T06:21:00Z',
            completed_at: '2026-10-20T06:21:04Z',
          },
        ],
        next_cursor: null,
      })
    }

    if (path === '/reconciliations' && method === 'POST') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      return json(route, 201, {
        id: 'rec_2',
        kind: body?.kind,
        status: 'matched',
        target_user_id: body?.user_id ?? '',
        target_wallet_id: body?.wallet_id ?? '',
        target_order_id: body?.order_id ?? '',
        findings: {},
        requested_by: me.id,
        created_at: '2026-10-20T06:35:00Z',
        completed_at: '2026-10-20T06:35:01Z',
      })
    }

    if (path === '/refunds' && method === 'POST') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      return apiError(route, 503, 'not_configured', 'ZPay 退款能力尚未完成核实，当前未启用')
    }

    if (path === '/settings/billing' && method === 'GET') {
      return json(route, 200, {
        version: state.billing.version,
        updated_at: '2026-10-20T06:00:00Z',
        service_charge_enabled: state.billing.service_charge_enabled,
        tiku_charge_enabled: state.billing.tiku_charge_enabled,
        charging_allowed: state.billing.charging_allowed,
        effective_service_charge_enabled: state.billing.effective_service_charge_enabled,
        effective_tiku_charge_enabled: state.billing.effective_tiku_charge_enabled,
        automatic_charging: state.billing.automatic_charging,
        zpay_checkout_enabled: state.billing.zpay_checkout_enabled,
        zpay_refund_enabled: state.billing.zpay_refund_enabled,
        default_channel: state.billing.default_channel,
        min_recharge_minor: state.billing.min_recharge_minor,
        max_recharge_minor: state.billing.max_recharge_minor,
        service: state.billing.service,
        tiku: state.billing.tiku,
        payment_provider: 'zpay',
        checkout_ready: false,
        refund_ready: false,
      }, { ETag: `"v${state.billing.version}"` })
    }

    if (path === '/settings/billing' && method === 'PATCH') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      if (request.headers()['if-match'] !== `"v${state.billing.version}"`) {
        return apiError(route, 412, 'version_conflict', '计费设置已被其他操作更新，请重新加载。')
      }
      state.billing = {
        ...state.billing,
        ...(body ?? {}),
        version: state.billing.version + 1,
      }
      return json(route, 200, {
        version: state.billing.version,
        updated_at: '2026-10-20T06:50:00Z',
        service_charge_enabled: state.billing.service_charge_enabled,
        tiku_charge_enabled: state.billing.tiku_charge_enabled,
        charging_allowed: state.billing.charging_allowed,
        effective_service_charge_enabled: state.billing.effective_service_charge_enabled,
        effective_tiku_charge_enabled: state.billing.effective_tiku_charge_enabled,
        automatic_charging: state.billing.automatic_charging,
        zpay_checkout_enabled: state.billing.zpay_checkout_enabled,
        zpay_refund_enabled: state.billing.zpay_refund_enabled,
        default_channel: state.billing.default_channel,
        min_recharge_minor: state.billing.min_recharge_minor,
        max_recharge_minor: state.billing.max_recharge_minor,
        service: state.billing.service,
        tiku: state.billing.tiku,
        payment_provider: 'zpay',
        checkout_ready: false,
        refund_ready: false,
      }, { ETag: `"v${state.billing.version}"` })
    }

    if ((path === '/settings/runtime' || path === '/maintenance') && method === 'GET') {
      return json(route, 200, {
        runtime: { global_concurrency: 8, account_concurrency: 2, execution_enabled: false, instance_id: 'test-node' },
        storage: { status: 'ok', used_percent: 42.5, available_bytes: '27917287424' },
        retention: { stream_days: 3, outbox_days: 7, question_content_days: 7, export_hours: 24, financial_records: 'retained' },
        generated_at: '2026-10-20T06:32:00Z',
      })
    }

    if (path === '/question-models' && method === 'GET') {
      return json(route, 200, { items: questionModels, next_cursor: null })
    }

    if (path === '/question-models' && method === 'POST') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      return json(route, 201, {
        id: 'qmo_new',
        offering_id: 'qmo_new',
        ...body,
        enabled: body?.enabled === true,
        default: body?.default === true,
        health: 'configured',
        version: 1,
        created_at: '2026-10-20T06:50:00Z',
        updated_at: '2026-10-20T06:50:00Z',
      })
    }

    if (path.startsWith('/question-models/') && path.endsWith('/tests') && method === 'POST') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      if (body?.kind === 'functional' && body?.confirm_cost !== true) {
        return apiError(route, 428, 'cost_confirmation_required', '功能性测试需要费用确认。')
      }
      return json(route, 200, {
        kind: body?.kind,
        status: 'ok',
        reachable: true,
        functional: body?.kind === 'functional' ? true : false,
        latency_ms: 42,
        error_code: '',
        cost_confirmed: body?.kind === 'functional',
      })
    }

    if (path.startsWith('/question-models/') && method === 'PATCH') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      if (request.headers()['if-match'] !== '"v2"' && request.headers()['if-match'] !== '"v1"') {
        return apiError(route, 412, 'version_conflict', '模型条目已被其他操作更新，请重新加载。')
      }
      const id = decodeURIComponent(path.slice('/question-models/'.length))
      const current = questionModels.find(model => model.id === id)
      return json(route, 200, {
        ...current,
        ...body,
        enabled: body?.enabled ?? current?.enabled,
        default: body?.default ?? current?.default,
        version: (current?.version ?? 1) + 1,
        updated_at: '2026-10-20T06:51:00Z',
      }, { ETag: `"v${(current?.version ?? 1) + 1}"` })
    }

    if (path === '/question-usage' && method === 'GET') {
      return json(route, 200, {
        items: [
          {
            offering_id: 'qmo_standard',
            display_name: '标准模型',
            queries: 12,
            cache_hits: 3,
            input_tokens: 8420,
            output_tokens: 960,
            total_tokens: 9380,
            cost_micros: '0',
            pricing: questionModels[0].pricing,
          },
        ],
        pricing: { automatic_charging: false, unit: 'micros_per_million_tokens', per_model: true },
        next_cursor: null,
      })
    }

    if (path === '/question-queries' && method === 'GET') {
      return json(route, 200, { items: questionQueries, next_cursor: null })
    }

    if (path === '/question-cache/invalidations' && method === 'POST') {
      if (!requireCsrf()) return apiError(route, 403, 'csrf_invalid', 'CSRF 校验失败。')
      return json(route, 200, { invalidated: true, cache_entry_id: body?.cache_entry_id })
    }

    return apiError(route, 404, 'not_found', `测试假服务未实现 ${method} ${path}`)
  })

  await page.route('**/health/**', async route => {
    const path = new URL(route.request().url()).pathname
    return json(route, 200, { status: 'ok', request_id: path.endsWith('ready') ? 'req_e2e_ready' : 'req_e2e_live' })
  })

  return state
}
