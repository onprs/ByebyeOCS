import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'

const authState = vi.hoisted(() => ({
  value: {
    phase: 'unauthenticated',
    capabilities: {
      api_version: 'v1',
      audience: 'admin',
      minimum_client_version: null,
      authentication: { enabled: true, login_methods: ['password_totp'] },
      features: {},
      appearance: {
        default_color_scheme: 'graphite',
        color_schemes: ['graphite', 'grove', 'indigo'],
        default_theme_mode: 'system',
        theme_modes: ['system', 'light', 'dark'],
      },
      pricing: { service_configured: false, tiku_configured: false, automatic_charging: false },
    },
    capabilitiesError: null,
    me: null,
    sessionExpired: false,
    login: vi.fn(),
    logout: vi.fn(),
    retryBootstrap: vi.fn(),
    dismissSessionExpired: vi.fn(),
  } as Record<string, unknown>,
}))

vi.mock('../../src/auth/AuthProvider', () => ({
  useAuth: () => authState.value,
}))

vi.mock('../../src/auth/RequireAuth', () => ({
  BootScreen: ({ error }: { error?: unknown }) => <div>启动失败 {String(error ?? '')}</div>,
}))

import { LoginPage } from '../../src/pages/LoginPage'
import { ApiError } from '../../src/api/client'

function renderLogin(children: ReactNode = <LoginPage />) {
  return render(
    <MemoryRouter initialEntries={['/login']}>
      <Routes>
        <Route path="/login" element={children} />
        <Route path="/" element={<div>运行总览页面</div>} />
        <Route path="/dashboard" element={<div>运行总览页面</div>} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('管理员登录', () => {
  beforeEach(() => {
    const base = authState.value
    base.phase = 'unauthenticated'
    base.sessionExpired = false
    base.login = vi.fn()
    authState.value = { ...base }
  })

  it('认证未启用时不提供默认账号或密码表单', () => {
    authState.value = { ...authState.value, phase: 'authentication_disabled' }
    renderLogin()
    expect(screen.getByRole('heading', { name: '管理员认证尚未配置' })).toBeInTheDocument()
    expect(screen.queryByLabelText('管理员用户名')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('密码')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重新检查服务配置' })).toBeInTheDocument()
  })

  it('要求用户名、密码与 6 位动态验证码', async () => {
    const user = userEvent.setup()
    renderLogin()
    await user.click(screen.getByRole('button', { name: /登录管理控制台/ }))
    expect(screen.getByText('请输入管理员用户名。')).toBeInTheDocument()

    await user.type(screen.getByLabelText('管理员用户名'), 'ops')
    await user.type(screen.getByLabelText('密码'), 'correct-horse')
    await user.type(screen.getByLabelText(/动态验证码/), '12')
    await user.click(screen.getByRole('button', { name: /登录管理控制台/ }))
    expect(screen.getByText('请输入 6 位动态验证码。')).toBeInTheDocument()
    expect(authState.value.login).not.toHaveBeenCalled()
  })

  it('登录成功后只把规范化验证码交给认证上下文', async () => {
    const user = userEvent.setup()
    ;(authState.value.login as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)
    renderLogin()
    await user.type(screen.getByLabelText('管理员用户名'), '  ops  ')
    await user.type(screen.getByLabelText('密码'), 'correct-horse')
    await user.type(screen.getByLabelText(/动态验证码/), '123 456')
    await user.click(screen.getByRole('button', { name: /登录管理控制台/ }))
    await waitFor(() => {
      expect(authState.value.login).toHaveBeenCalledWith({
        username: 'ops',
        password: 'correct-horse',
        totp: '123456',
      })
    })
    expect(await screen.findByText('运行总览页面')).toBeInTheDocument()
  })

  it('服务端登录失败时只显示一条真实错误', async () => {
    const user = userEvent.setup()
    ;(authState.value.login as ReturnType<typeof vi.fn>).mockRejectedValue(
      new ApiError(401, {
        code: 'invalid_credentials',
        message: '用户名、密码或动态验证码不正确。',
        request_id: 'req_login_failed',
        retryable: false,
        scope: 'request',
      }),
    )
    renderLogin()
    await user.type(screen.getByLabelText('管理员用户名'), 'ops')
    await user.type(screen.getByLabelText('密码'), 'wrong')
    await user.type(screen.getByLabelText(/动态验证码/), '123456')
    await user.click(screen.getByRole('button', { name: /登录管理控制台/ }))
    expect(await screen.findByText('用户名、密码或动态验证码不正确。')).toBeInTheDocument()
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(screen.queryByText('请求编号 req_login_failed')).not.toBeInTheDocument()
  })
})
