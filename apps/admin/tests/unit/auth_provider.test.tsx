import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthProvider, useAuth } from '../../src/auth/AuthProvider'

const api = vi.hoisted(() => ({
  getAuthSession: vi.fn(),
  getCapabilities: vi.fn(),
  getMe: vi.fn(),
  login: vi.fn(),
  logout: vi.fn(),
}))

vi.mock('../../src/api/endpoints', async importOriginal => {
  const actual = await importOriginal<typeof import('../../src/api/endpoints')>()
  return {
    ...actual,
    getAuthSession: api.getAuthSession,
    getCapabilities: api.getCapabilities,
    getMe: api.getMe,
    login: api.login,
    logout: api.logout,
  }
})

const capabilities = {
  api_version: 'v1',
  audience: 'admin',
  minimum_client_version: null,
  authentication: { enabled: true, login_methods: ['password_totp'] },
  features: {},
}

const unauthenticatedSession = {
  authenticated: false,
  csrf_token: null,
  channel: 'web',
  expires_at: null,
  authenticated_at: null,
  fresh: null,
  permissions: [],
  capabilities: { adminMethods: [], stepUp: true, webCookie: true, totp: true },
}

const authenticatedSession = {
  ...unauthenticatedSession,
  authenticated: true,
  capabilities: { ...unauthenticatedSession.capabilities, adminMethods: ['password_totp'] },
}

const adminMe = {
  id: 'adm_1',
  username: 'ops',
  display_name: '运营管理员',
  role: 'owner',
  permissions: ['*'],
  totp_enabled: true,
  authenticated_at: null,
  session_expires_at: null,
  fresh: true,
}

function wrapper({ children }: { children: ReactNode }) {
  return <AuthProvider>{children}</AuthProvider>
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

describe('管理端身份引导', () => {
  beforeEach(() => {
    api.getCapabilities.mockResolvedValue({ data: capabilities, etag: null })
    api.getMe.mockResolvedValue({ data: adminMe, etag: null })
    api.logout.mockResolvedValue({ data: null, etag: null })
  })

  it('引导进行中重复重试只发出一次会话请求', async () => {
    const sessionGate = deferred<{ data: typeof unauthenticatedSession; etag: null }>()
    api.getAuthSession.mockReturnValue(sessionGate.promise)

    const { result } = renderHook(() => useAuth(), { wrapper })
    await waitFor(() => expect(api.getAuthSession).toHaveBeenCalledTimes(1))

    act(() => {
      result.current.retryBootstrap()
      result.current.retryBootstrap()
    })
    await waitFor(() => expect(result.current.phase).toBe('boot'))
    expect(api.getAuthSession).toHaveBeenCalledTimes(1)

    await act(async () => {
      sessionGate.resolve({ data: unauthenticatedSession, etag: null })
    })
    await waitFor(() => expect(result.current.phase).toBe('unauthenticated'))
    expect(api.getAuthSession).toHaveBeenCalledTimes(1)
  })

  it('退出后迟到的引导响应不会把管理员置回已登录', async () => {
    const sessionGate = deferred<{ data: typeof authenticatedSession; etag: null }>()
    api.getAuthSession.mockReturnValue(sessionGate.promise)

    const { result } = renderHook(() => useAuth(), { wrapper })
    await waitFor(() => expect(api.getAuthSession).toHaveBeenCalledTimes(1))

    await act(async () => {
      await result.current.logout()
    })
    expect(result.current.phase).toBe('unauthenticated')

    await act(async () => {
      sessionGate.resolve({ data: authenticatedSession, etag: null })
    })
    await waitFor(() => expect(result.current.phase).toBe('unauthenticated'))
    expect(result.current.me).toBeNull()
    expect(api.getMe).not.toHaveBeenCalled()
  })
})
