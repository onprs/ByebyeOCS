import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../src/api/client'
import type { Refund } from '../../src/api/types'
import { ToastProvider } from '../../src/components/toast'

const auth = vi.hoisted(() => ({
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
  getFinanceSummary: vi.fn(),
  getBillingSettings: vi.fn(),
  listOrders: vi.fn(),
  listRefunds: vi.fn(),
  resolveRefund: vi.fn(),
}))

vi.mock('../../src/api/endpoints', async importOriginal => {
  const actual = await importOriginal<typeof import('../../src/api/endpoints')>()
  return {
    ...actual,
    getFinanceSummary: api.getFinanceSummary,
    getBillingSettings: api.getBillingSettings,
    listOrders: api.listOrders,
    listRefunds: api.listRefunds,
    resolveRefund: api.resolveRefund,
  }
})

import { FinancePage } from '../../src/pages/FinancePage'

const reservedRefund: Refund = {
  id: 'refund-reserved',
  order_id: 'order-1',
  user_id: 'user-1',
  wallet_id: 'wallet-1',
  kind: 'abnormal_payment',
  amount_minor: '3000',
  currency: 'CNY',
  reason: '异常支付退款',
  status: 'reserved',
  status_label: '已预占',
  provider: 'zpay',
  provider_refund_no: null,
  provider_trade_no: null,
  last_error_code: null,
  created_at: '2026-10-20T02:00:00Z',
  updated_at: '2026-10-20T02:10:00Z',
  completed_at: null,
}

const succeededRefund: Refund = {
  ...reservedRefund,
  id: 'refund-done',
  status: 'succeeded',
  status_label: '已退款',
  completed_at: '2026-10-20T02:20:00Z',
}

function renderFinance() {
  return render(
    <MemoryRouter initialEntries={['/finance']}>
      <ToastProvider>
        <FinancePage />
      </ToastProvider>
    </MemoryRouter>,
  )
}

async function openRefundReview(user: ReturnType<typeof userEvent.setup>) {
  await screen.findByText('refund-reserved')
  await user.click(screen.getByRole('button', { name: '复核退款 refund-reserved' }))
  return screen.findByRole('dialog', { name: '复核退款结果' })
}

describe('退款人工复核', () => {
  beforeEach(() => {
    auth.me = {
      ...auth.me,
      permissions: ['*'],
    }
    api.getFinanceSummary.mockResolvedValue({ data: null, etag: null })
    api.getBillingSettings.mockResolvedValue({ data: null, etag: null })
    api.listOrders.mockResolvedValue({ data: { items: [], next_cursor: null }, etag: null })
    api.listRefunds.mockResolvedValue({
      data: { items: [reservedRefund, succeededRefund], next_cursor: null },
      etag: null,
    })
    api.resolveRefund.mockResolvedValue({
      ...reservedRefund,
      status: 'succeeded',
      status_label: '已退款',
      completed_at: '2026-10-20T03:00:00Z',
    })
  })

  it('只对可复核状态显示入口，并提交明确的已退款结果', async () => {
    const user = userEvent.setup()
    renderFinance()

    await user.click(screen.getByRole('tab', { name: '退款' }))
    await screen.findByText('refund-reserved')

    expect(screen.getByRole('button', { name: '复核退款 refund-reserved' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '复核退款 refund-done' })).not.toBeInTheDocument()
    expect(screen.queryByText(/再次退款/)).not.toBeInTheDocument()

    const dialog = await openRefundReview(user)
    await user.click(within(dialog).getByRole('button', { name: '提交复核结果' }))
    expect(
      within(dialog).getByText('请选择退款核实结果：已退款或未退款。'),
    ).toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: /^已退款/ }))
    await user.type(
      within(dialog).getByLabelText(/核实依据/),
      '渠道账单已核对，交易号一致',
    )
    await user.click(within(dialog).getByRole('button', { name: '提交复核结果' }))

    await waitFor(() => {
      expect(api.resolveRefund).toHaveBeenCalledWith(
        'refund-reserved',
        { outcome: 'succeeded', reason: '渠道账单已核对，交易号一致' },
        expect.any(String),
      )
    })
    expect(await within(dialog).findByText('已记录为已退款。')).toBeInTheDocument()
    await waitFor(() => expect(api.listRefunds).toHaveBeenCalledTimes(2))
    expect(screen.queryByText(/再次退款/)).not.toBeInTheDocument()
  })

  it('近期验证过期时提示重新验证，不重复提交复核', async () => {
    const user = userEvent.setup()
    api.resolveRefund.mockRejectedValue(
      new ApiError(403, {
        code: 'forbidden',
        message: '需要重新验证管理员身份后再执行此操作',
        request_id: 'req_stepup',
        retryable: false,
        scope: 'request',
      }),
    )
    renderFinance()

    await user.click(screen.getByRole('tab', { name: '退款' }))
    const dialog = await openRefundReview(user)
    await user.click(within(dialog).getByRole('button', { name: /^未退款/ }))
    await user.type(within(dialog).getByLabelText(/核实依据/), '渠道账单显示未退款')
    await user.click(within(dialog).getByRole('button', { name: '提交复核结果' }))

    await waitFor(() => expect(api.resolveRefund).toHaveBeenCalledTimes(1))
    expect(await within(dialog).findByText('需要重新验证管理员身份后再执行此操作')).toBeInTheDocument()
    expect(
      await screen.findByRole('dialog', { name: '重新验证管理员身份' }),
    ).toBeInTheDocument()
    expect(api.resolveRefund).toHaveBeenCalledTimes(1)
  })

  it('没有退款权限时不显示复核入口', async () => {
    auth.me = { ...auth.me, permissions: ['billing.read'] }
    const user = userEvent.setup()
    renderFinance()

    await user.click(screen.getByRole('tab', { name: '退款' }))
    await screen.findByText('refund-reserved')
    expect(screen.queryByRole('button', { name: '复核退款 refund-reserved' })).not.toBeInTheDocument()
  })
})
