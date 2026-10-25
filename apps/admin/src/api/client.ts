import { clearSessionMemory, getCsrfToken, notifyUnauthorized, setCsrfToken } from './session'

export const API_BASE = (import.meta.env.VITE_ADMIN_API_BASE || '/admin/api/v1').replace(/\/+$/, '')
export const HEALTH_BASE = (import.meta.env.VITE_HEALTH_BASE || '/health').replace(/\/+$/, '')

export interface ApiErrorPayload {
  code: string
  message: string
  request_id: string
  retryable: boolean
  scope: string
}

export type ApiBlockerValue = boolean | number
export type ApiBlockers = Record<string, ApiBlockerValue>

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId: string
  readonly retryable: boolean
  readonly scope: string
  readonly retryAfterSeconds: number | null
  readonly blockers: ApiBlockers | null

  constructor(
    status: number,
    payload: ApiErrorPayload,
    retryAfterSeconds: number | null = null,
    blockers: ApiBlockers | null = null,
  ) {
    super(payload.message)
    this.name = 'ApiError'
    this.status = status
    this.code = payload.code
    this.requestId = payload.request_id
    this.retryable = payload.retryable
    this.scope = payload.scope
    this.retryAfterSeconds = retryAfterSeconds
    this.blockers = blockers
  }
}

export interface ApiResponse<T> {
  data: T
  etag: string | null
}

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE'
  body?: unknown
  ifMatch?: string | null
  signal?: AbortSignal
  skipUnauthorizedHandler?: boolean
  retryCsrf?: boolean
  idempotencyKey?: string | null
  headers?: Record<string, string>
}

function parseRetryAfter(value: string | null): number | null {
  if (!value) return null
  const seconds = Number(value)
  if (Number.isFinite(seconds) && seconds >= 0) return Math.ceil(seconds)
  const date = new Date(value)
  if (!Number.isNaN(date.getTime())) {
    return Math.max(0, Math.ceil((date.getTime() - Date.now()) / 1000))
  }
  return null
}

function fallbackMessage(status: number): string {
  if (status === 401) return '登录状态已失效，请重新登录。'
  if (status === 403) return '当前管理员无权执行此操作。'
  if (status === 404) return '请求的管理接口不存在。'
  if (status === 412) return '数据已被其他操作更新，请重新加载。'
  if (status === 428) return '此操作缺少版本条件，请重新加载。'
  if (status === 429) return '请求过于频繁，请稍后重试。'
  if (status === 503) return '服务暂不可用或尚未配置。'
  return '请求失败，请稍后重试。'
}

async function readPayload(response: Response): Promise<unknown> {
  const text = await response.text()
  if (!text) return null
  try {
    return JSON.parse(text)
  } catch {
    return text
  }
}

function toApiError(response: Response, payload: unknown, headerRequestId: string | null): ApiError {
  const record = payload && typeof payload === 'object' ? (payload as Record<string, unknown>) : null
  const rawError = record?.error && typeof record.error === 'object' ? (record.error as Record<string, unknown>) : null
  const blockers: ApiBlockers | null = record?.blockers && typeof record.blockers === 'object' && !Array.isArray(record.blockers)
    ? Object.fromEntries(
        Object.entries(record.blockers as Record<string, unknown>).filter(
          (entry): entry is [string, ApiBlockerValue] =>
            typeof entry[1] === 'boolean' || (typeof entry[1] === 'number' && Number.isFinite(entry[1])),
        ),
      )
    : null
  return new ApiError(
    response.status,
    {
      code: typeof rawError?.code === 'string' ? rawError.code : `http_${response.status}`,
      message: typeof rawError?.message === 'string' ? rawError.message : fallbackMessage(response.status),
      request_id:
        typeof rawError?.request_id === 'string'
          ? rawError.request_id
          : (headerRequestId ?? '—'),
      retryable: typeof rawError?.retryable === 'boolean' ? rawError.retryable : response.status === 429 || response.status >= 500,
      scope: typeof rawError?.scope === 'string' ? rawError.scope : 'request',
    },
    parseRetryAfter(response.headers.get('Retry-After')),
    blockers,
  )
}

async function refreshCsrfToken(): Promise<boolean> {
  try {
    const response = await fetch(`${API_BASE}/auth/session`, {
      method: 'GET',
      credentials: 'include',
      cache: 'no-store',
      headers: { Accept: 'application/json' },
    })
    if (!response.ok) return false
    const payload = (await response.json()) as Record<string, unknown>
    const token = typeof payload.csrf_token === 'string' ? payload.csrf_token : null
    if (token) {
      setCsrfToken(token)
      return true
    }
  } catch {
    return false
  }
  return false
}

export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<ApiResponse<T>> {
  const method = options.method ?? 'GET'
  const isWrite = method !== 'GET'
  const headers: Record<string, string> = { Accept: 'application/json', ...options.headers }
  if (options.body !== undefined) headers['Content-Type'] = 'application/json'
  if (options.ifMatch) headers['If-Match'] = options.ifMatch
  if (isWrite) {
    const token = getCsrfToken()
    if (token) headers['X-CSRF-Token'] = token
    if (options.idempotencyKey) headers['Idempotency-Key'] = options.idempotencyKey
  }

  let response: Response
  try {
    response = await fetch(`${API_BASE}${path}`, {
      method,
      credentials: 'include',
      cache: 'no-store',
      referrerPolicy: 'same-origin',
      headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: options.signal,
    })
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    throw new ApiError(0, {
      code: 'network_unavailable',
      message: '无法连接管理服务，请检查网络或服务状态。',
      request_id: '—',
      retryable: true,
      scope: 'network',
    })
  }

  const requestId = response.headers.get('X-Request-ID')
  const payload = (await readPayload(response)) as unknown
  if (!response.ok) {
    const error = toApiError(response, payload, requestId)
    if (response.status === 401) {
      if (!options.skipUnauthorizedHandler) notifyUnauthorized()
      throw error
    }
    if (
      response.status === 403 &&
      error.code === 'csrf_invalid' &&
      options.retryCsrf !== false &&
      method !== 'GET'
    ) {
      const refreshed = await refreshCsrfToken()
      if (refreshed) {
        return apiRequest<T>(path, { ...options, retryCsrf: false })
      }
      clearSessionMemory()
      notifyUnauthorized()
    }
    throw error
  }

  const etag = response.headers.get('ETag')
  return { data: payload as T, etag }
}

export function newIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `admin-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

export async function apiGet<T>(path: string, options: Omit<RequestOptions, 'method' | 'body'> = {}): Promise<ApiResponse<T>> {
  return apiRequest<T>(path, { ...options, method: 'GET' })
}

export async function healthCheck(path: 'live' | 'ready'): Promise<ApiResponse<{ status: string; request_id: string | null }>> {
  let response: Response
  try {
    response = await fetch(`${HEALTH_BASE}/${path}`, {
      method: 'GET',
      credentials: 'include',
      cache: 'no-store',
      headers: { Accept: 'application/json' },
    })
  } catch {
    throw new ApiError(0, {
      code: 'network_unavailable',
      message: '无法连接健康检查接口。',
      request_id: '—',
      retryable: true,
      scope: 'network',
    })
  }
  const requestId = response.headers.get('X-Request-ID')
  const payload = (await readPayload(response)) as unknown
  if (!response.ok) throw toApiError(response, payload, requestId)
  const record = payload && typeof payload === 'object' ? (payload as Record<string, unknown>) : {}
  return {
    data: {
      status: typeof record.status === 'string' ? record.status : 'unknown',
      request_id: typeof record.request_id === 'string' ? record.request_id : requestId,
    },
    etag: response.headers.get('ETag'),
  }
}
