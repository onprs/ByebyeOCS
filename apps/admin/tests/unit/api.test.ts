import { describe, expect, it, vi } from 'vitest'
import { ApiError, apiRequest } from '../../src/api/client'
import { getAuthSession, getPreferences, listUsers, login, resolveRefund, runAccountOperation, updatePreferences } from '../../src/api/endpoints'
import { normalizeDashboard } from '../../src/api/normalize'
import { getCsrfToken } from '../../src/api/session'
import { setCsrfToken, subscribeUnauthorized } from '../../src/api/session'
import {
  formatMoneyMinor,
  formatPriceMicros,
  microsToYuanInput,
  parseYuanToMicros,
  parseYuanToMinor,
} from '../../src/lib/format'

function jsonResponse(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

describe('请求适配层', () => {
  it('总览等待队列只取任务待执行数，不用课程同步队列代替', () => {
    const dashboard = normalizeDashboard({
      generated_at: '2026-10-23T00:00:00Z',
      tasks: { total: 2, by_state: { running: 2 }, active_attempts: 2 },
      syncs: { queued: 7, running: 1, needs_attention: 0 },
    })

    expect(dashboard.limits.queued_tasks).toBeNull()
  })
  it('请求始终携带 Cookie，并在写操作中附带 CSRF 与幂等键', async () => {
    setCsrfToken('csrf-test')
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { ok: true }, { ETag: '"v3"' }))
    vi.stubGlobal('fetch', fetchMock)
    await apiRequest('/account-operations', {
      method: 'POST',
      body: { reason: 'test' },
      ifMatch: '"v3"',
      idempotencyKey: 'idem-1',
    })
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/admin/api/v1/account-operations')
    expect(init.credentials).toBe('include')
    expect(init.headers['X-CSRF-Token']).toBe('csrf-test')
    expect(init.headers['Idempotency-Key']).toBe('idem-1')
    expect(init.headers['If-Match']).toBe('"v3"')
    expect(init.body).toBe(JSON.stringify({ reason: 'test' }))
  })

  it('401 会清除 CSRF 并通知会话失效', async () => {
    setCsrfToken('old-token')
    const handler = vi.fn()
    const unsubscribe = subscribeUnauthorized(handler)
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(
          401,
          { error: { code: 'unauthenticated', message: '请重新登录', request_id: 'req_401', retryable: false, scope: 'request' } },
          { 'X-Request-ID': 'req_header' },
        ),
      ),
    )
    await expect(apiRequest('/users')).rejects.toMatchObject({
      status: 401,
      code: 'unauthenticated',
      requestId: 'req_401',
    })
    expect(handler).toHaveBeenCalledTimes(1)
    unsubscribe()
  })

  it('412 保留冲突码、可读信息与请求编号', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(412, {
          error: {
            code: 'version_conflict',
            message: '数据已被其他操作更新，请重新加载。',
            request_id: 'req_412',
            retryable: false,
            scope: 'request',
          },
        }),
      ),
    )
    try {
      await apiRequest('/me/preferences', { method: 'PATCH', body: { theme_mode: 'dark' } })
      throw new Error('应当失败')
    } catch (error) {
      expect(error).toBeInstanceOf(ApiError)
      const apiError = error as ApiError
      expect(apiError.status).toBe(412)
      expect(apiError.code).toBe('version_conflict')
      expect(apiError.requestId).toBe('req_412')
    }
  })

  it('429 将 Retry-After 转为可显示秒数', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(
          429,
          { error: { code: 'rate_limited', message: '请求过于频繁', request_id: 'req_429', retryable: true, scope: 'request' } },
          { 'Retry-After': '45' },
        ),
      ),
    )
    await expect(apiRequest('/tasks')).rejects.toMatchObject({
      status: 429,
      retryAfterSeconds: 45,
      retryable: true,
    })
  })

  it('CSRF 失效时通过 /me 重新取得内存令牌并重试一次', async () => {
    setCsrfToken('stale')
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        jsonResponse(403, {
          error: { code: 'csrf_invalid', message: 'CSRF 校验失败', request_id: 'req_csrf', retryable: false, scope: 'request' },
        }),
      )
      .mockResolvedValueOnce(jsonResponse(200, { csrf_token: 'fresh', me: { id: 'adm_1', username: 'ops' } }))
      .mockResolvedValueOnce(jsonResponse(200, { updated: 1 }))
    vi.stubGlobal('fetch', fetchMock)
    const result = await apiRequest<{ updated: number }>('/account-operations', {
      method: 'POST',
      body: { operation: 'pause', user_ids: ['u1'], reason: '原因' },
    })
    expect(result.data.updated).toBe(1)
    expect(fetchMock).toHaveBeenCalledTimes(3)
    const secondPost = fetchMock.mock.calls[2]
    expect(secondPost[1].headers['X-CSRF-Token']).toBe('fresh')
  })

  it('隐私阻断只解析计数明细，不伪造完成', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(409, {
          error: { code: 'privacy_blocked', message: '存在未结资金或运行中的活动', request_id: 'req_privacy', retryable: false, scope: 'request' },
          blockers: { wallet_balances: false, open_holds: 1, active_tasks: 2, ignored: '文本' },
        }),
      ),
    )
    await expect(
      apiRequest('/data-requests/req_1/review', { method: 'POST', body: { action: 'complete', reason: '完成删除' } }),
    ).rejects.toMatchObject({
      status: 409,
      code: 'privacy_blocked',
      blockers: { wallet_balances: false, open_holds: 1, active_tasks: 2 },
    })
  })

  it('接口错误不使用假成功', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(503, {
          error: { code: 'not_configured', message: '题库尚未配置', request_id: 'req_503', retryable: true, scope: 'service' },
        }),
      ),
    )
    await expect(apiRequest('/question-models')).rejects.toMatchObject({
      status: 503,
      code: 'not_configured',
      scope: 'service',
    })
  })
})

describe('端点适配', () => {
  it('列表统一读取 items 与 next_cursor', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(200, {
          items: [{ id: 'u1', display_name: '林晓', status: 'valid', wallet: { id: 'w1', available_minor: '1250' } }],
          next_cursor: 'cursor_2',
        }),
      ),
    )
    const response = await listUsers({ search: '林' })
    expect(response.data.items[0]).toMatchObject({
      id: 'u1',
      display_name: '林晓',
      wallet_id: 'w1',
      balance_minor: '1250',
    })
    expect(response.data.next_cursor).toBe('cursor_2')
  })

  it('管理员登录按实际契约发送 totp_code 并读取嵌套 admin 与 CSRF', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(200, {
          mode: 'web',
          csrf_token: 'csrf-login',
          expires_at: '2026-10-21T06:00:00Z',
          permissions: ['*'],
          admin: {
            id: 'adm_1',
            username: 'ops',
            display_name: '运营管理员',
            role: 'owner',
            totp_enabled: true,
            permissions: ['*'],
            session: {
              authenticated_at: '2026-10-20T06:00:00Z',
              expires_at: '2026-10-21T06:00:00Z',
              fresh: true,
            },
          },
        }),
      ),
    )
    const result = await login({ username: 'ops', password: 'secret', totp: '123456' })
    const body = JSON.parse(String(vi.mocked(fetch).mock.calls[0][1]?.body))
    expect(body).toEqual({ username: 'ops', password: 'secret', totp_code: '123456' })
    expect(result.me).toMatchObject({
      id: 'adm_1',
      display_name: '运营管理员',
      role: 'owner',
      session_expires_at: '2026-10-21T06:00:00Z',
    })
    expect(getCsrfToken()).toBe('csrf-login')
  })

  it('认证会话接口复用 CSRF 且不把匿名状态视为登录', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(200, {
          authenticated: false,
          kind: 'admin',
          permissions: [],
          capabilities: { admin: { methods: ['password+totp'], step_up: true }, totp: true },
        }),
      ),
    )
    const response = await getAuthSession()
    expect(response.data.authenticated).toBe(false)
    expect(response.data.capabilities.adminMethods).toEqual(['password+totp'])
    expect(getCsrfToken()).toBeNull()
  })

  it('偏好更新使用 "vN" 形式的 If-Match', async () => {
    setCsrfToken('csrf')
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(
        200,
        { color_scheme: 'grove', theme_mode: 'dark', version: 8 },
        { ETag: '"v8"' },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)
    const result = await updatePreferences({ color_scheme: 'grove', theme_mode: 'dark' }, 7)
    const init = fetchMock.mock.calls[0][1]
    expect(init.headers['If-Match']).toBe('"v7"')
    expect(result.data.version).toBe(8)
  })

  it('偏好读取可从 ETag 推导版本', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(200, { color_scheme: 'indigo', theme_mode: 'system' }, { ETag: '"v12"' }),
      ),
    )
    const response = await getPreferences()
    expect(response.data).toMatchObject({ color_scheme: 'indigo', theme_mode: 'system', version: 12 })
  })

  it('批量操作读取服务端实际更新数量', async () => {
    setCsrfToken('csrf')
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(200, {
          items: [
            { user_id: 'u1', ok: true, status: 'pause_plans' },
            { user_id: 'u2', ok: false, code: 'conflict', message: '账号状态不适用' },
          ],
        }),
      ),
    )
    const result = await runAccountOperation({ operation: 'pause', userIds: ['u1', 'u2'], reason: '巡检' })
    expect(result.updated).toBe(1)
    expect(result.skipped).toBe(1)
    expect(result.items[1]).toMatchObject({ id: 'u2', status: 'failed' })
  })

  it('退款复核提交明确结果与理由，并携带幂等键', async () => {
    setCsrfToken('csrf-refund')
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, {
        id: 'refund-1',
        order_id: 'order-1',
        user_id: 'user-1',
        wallet_id: 'wallet-1',
        kind: 'abnormal_payment',
        amount_minor: '3000',
        currency: 'CNY',
        reason: '渠道退款成功',
        status: 'succeeded',
        created_at: '2026-10-20T02:00:00Z',
        updated_at: '2026-10-20T03:00:00Z',
        completed_at: '2026-10-20T03:00:00Z',
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const refund = await resolveRefund(
      'refund-1',
      { outcome: 'succeeded', reason: '渠道账单已核对，交易号一致' },
      'idem-refund-review-1',
    )

    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/admin/api/v1/refunds/refund-1/resolve')
    expect(init.method).toBe('POST')
    expect(init.headers['Idempotency-Key']).toBe('idem-refund-review-1')
    expect(JSON.parse(init.body)).toEqual({ outcome: 'succeeded', reason: '渠道账单已核对，交易号一致' })
    expect(refund).toMatchObject({ id: 'refund-1', status: 'succeeded', status_label: '已退款' })
  })
})

describe('金额与价格格式', () => {
  it('整数分按元显示，不通过浮点运算', () => {
    expect(formatMoneyMinor('123456')).toBe('¥1,234.56')
    expect(formatMoneyMinor('-5')).toBe('-¥0.05')
    expect(formatMoneyMinor(null)).toBe('—')
    expect(formatMoneyMinor('bad')).toBe('格式异常')
  })

  it('输入元转换为整数分并校验精度', () => {
    expect(parseYuanToMinor('12.34')).toBe('1234')
    expect(parseYuanToMinor('-0.05')).toBe('-5')
    expect(parseYuanToMinor('12.345')).toBeNull()
    expect(parseYuanToMinor('0')).toBe('0')
  })

  it('token 单价使用整数微元并支持往返转换', () => {
    expect(parseYuanToMicros('2.5')).toBe('2500000')
    expect(parseYuanToMicros('0.000001')).toBe('1')
    expect(formatPriceMicros('2500000')).toBe('¥2.5')
    expect(microsToYuanInput('2500000')).toBe('2.5')
    expect(formatPriceMicros(null)).toBe('价格待公布')
  })
})
