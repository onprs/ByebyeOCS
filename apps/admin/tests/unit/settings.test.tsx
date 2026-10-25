import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { BillingSettings } from '../../src/api/types'
import { ToastProvider } from '../../src/components/toast'
import { AppearanceProvider } from '../../src/theme/AppearanceProvider'

const auth = vi.hoisted(() => ({
  phase: 'authenticated',
  capabilities: {
    api_version: 'v1',
    audience: 'admin',
    minimum_client_version: null,
    authentication: { enabled: true, login_methods: ['password', 'totp'] },
    features: {},
    appearance: {
      default_color_scheme: 'graphite',
      default_theme_mode: 'system',
      color_schemes: ['graphite', 'grove', 'indigo'],
      theme_modes: ['system', 'light', 'dark'],
    },
    pricing: {
      service_configured: false,
      tiku_configured: false,
      automatic_charging: false,
    },
  },
  me: {
    id: 'adm_1',
    username: 'ops',
    display_name: '运营管理员',
    role: 'owner',
    permissions: ['*'],
    totp_enabled: true,
    authenticated_at: null,
    session_expires_at: null,
    fresh: true,
  },
}))

vi.mock('../../src/auth/AuthProvider', () => ({
  useAuth: () => auth,
}))

const api = vi.hoisted(() => ({
  getBillingSettings: vi.fn(),
  updateBillingSettings: vi.fn(),
  getPreferences: vi.fn(),
  updatePreferences: vi.fn(),
}))

vi.mock('../../src/api/endpoints', async importOriginal => {
  const actual = await importOriginal<typeof import('../../src/api/endpoints')>()
  return {
    ...actual,
    getBillingSettings: api.getBillingSettings,
    updateBillingSettings: api.updateBillingSettings,
    getPreferences: api.getPreferences,
    updatePreferences: api.updatePreferences,
  }
})

import { SettingsPage } from '../../src/pages/SettingsPage'

function billingSettings(rounding: string): BillingSettings {
  return {
    version: 4,
    updated_at: '2026-10-20T02:00:00Z',
    service_charge_enabled: false,
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
    payment_provider: 'zpay',
    checkout_ready: false,
    refund_ready: false,
    service: {
      scope: 'service_task_point',
      configured: true,
      state: 'disabled',
      price_version_id: 'price_1',
      price_version: 2,
      unit_label: '有效任务点',
      unit_precision: 0,
      price_micros: '120000',
      input_micros_per_million: null,
      output_micros_per_million: null,
      settlement_rule: 'per_event',
      rounding,
      min_charge_minor: '0',
      settlement_threshold_micros: null,
    },
    tiku: {
      scope: 'tiku_tokens',
      configured: true,
      state: 'disabled',
      price_version_id: 'price_tiku',
      price_version: 1,
      unit_label: 'tokens',
      unit_precision: 0,
      price_micros: null,
      input_micros_per_million: '7654321',
      output_micros_per_million: '1234567',
      settlement_rule: 'token_usage',
      rounding: 'ceil',
      min_charge_minor: null,
      settlement_threshold_micros: '9000000',
    },
  }
}

function renderSettings() {
  return render(
    <MemoryRouter initialEntries={['/settings?section=billing']}>
      <ToastProvider>
        <AppearanceProvider>
          <Routes>
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="/questions" element={<div>题库查询页面</div>} />
          </Routes>
        </AppearanceProvider>
      </ToastProvider>
    </MemoryRouter>,
  )
}

describe('计费设置价格契约', () => {
  beforeEach(() => {
    api.getPreferences.mockResolvedValue({
      data: { color_scheme: 'graphite', theme_mode: 'system', version: 1 },
      etag: '"v1"',
    })
    api.updatePreferences.mockResolvedValue({
      data: { color_scheme: 'graphite', theme_mode: 'system', version: 2 },
      etag: '"v2"',
    })
    api.updateBillingSettings.mockResolvedValue({
      data: billingSettings('half_up'),
      etag: '"v5"',
    })
  })

  it('加载已有 half_up 取整方式时保留原值并可保存', async () => {
    api.getBillingSettings.mockResolvedValue({
      data: billingSettings('half_up'),
      etag: '"v4"',
    })
    const user = userEvent.setup()
    renderSettings()

    const rounding = await screen.findByLabelText('取整方式')
    await waitFor(() => expect(rounding).toHaveValue('half_up'))
    expect(screen.getByRole('option', { name: '按四舍五入' })).toBeInTheDocument()

    const price = screen.getByRole('textbox', { name: /有效任务点单价/ })
    await user.clear(price)
    await user.type(price, '0.13')
    await user.click(screen.getByRole('button', { name: '保存计费设置' }))
    await waitFor(() => expect(api.updateBillingSettings).toHaveBeenCalled())
    expect(api.updateBillingSettings.mock.calls[0][0].service_price.rounding).toBe('half_up')
  })

  it('题库费率只指向公开模型价格，不回显全局旧费率', async () => {
    api.getBillingSettings.mockResolvedValue({
      data: billingSettings('ceil'),
      etag: '"v4"',
    })
    renderSettings()

    await screen.findByText('题库按公开模型的 token 价格单独计费，不在全局设置中维护费率。')
    expect(screen.getByText('按公开模型 token 价格')).toBeInTheDocument()
    expect(screen.queryByText('输入价格')).not.toBeInTheDocument()
    expect(screen.queryByText('输出价格')).not.toBeInTheDocument()
    expect(screen.queryByText('结算门槛')).not.toBeInTheDocument()
    expect(screen.queryByText('¥7.654321')).not.toBeInTheDocument()
    expect(screen.queryByText('¥1.234567')).not.toBeInTheDocument()
  })

  it('从题库计价打开独立的题库维护面板', async () => {
    api.getBillingSettings.mockResolvedValue({
      data: billingSettings('ceil'),
      etag: '"v4"',
    })
    const user = userEvent.setup()
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    renderSettings()

    await screen.findByText('题库按公开模型的 token 价格单独计费，不在全局设置中维护费率。')
    await user.click(screen.getByRole('button', { name: '打开题库面板' }))
    expect(open).toHaveBeenCalledWith('/panel/', '_blank', 'noopener,noreferrer')
  })
})
