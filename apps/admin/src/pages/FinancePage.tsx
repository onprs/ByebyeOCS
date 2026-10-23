import { useEffect, useRef, useState } from 'react'
import {
  ArrowRight,
  CheckCircle2,
  ChevronRight,
  CircleAlert,
  FileCheck2,
  RefreshCw,
  ShieldCheck,
  Wallet,
  X,
} from 'lucide-react'
import { Link, useSearchParams } from 'react-router-dom'
import {
  createReconciliation,
  createRefund,
  createWalletAdjustment,
  getBillingSettings,
  getFinanceSummary,
  getWalletEntries,
  listAdminWallets,
  listAuditEvents,
  listOrders,
  listReconciliations,
  listRefunds,
  resolveRefund,
} from '../api/endpoints'
import { ApiError } from '../api/client'
import type {
  Order,
  Reconciliation,
  Refund,
  WalletSnapshot,
} from '../api/types'
import {
  Badge,
  Button,
  Card,
  EmptyState,
  ErrorState,
  Field,
  IconButton,
  LoadingBlock,
  Modal,
  Money,
  PageHeader,
  Pagination,
  RefreshError,
  SearchInput,
  Select,
  Tabs,
  StepUpDialog,
  useToast,
} from '../components'
import { describeError } from '../components/ui'
import { useAuth } from '../auth/AuthProvider'
import { hasPermission } from '../auth/permissions'
import { formatDateTime, formatMoneyMinor, formatNumber, formatPriceMicros, parseYuanToMinor } from '../lib/format'
import { useAsync, useCursorStack, useDebouncedValue } from '../lib/hooks'
import { useIdempotencyKey } from '../lib/idempotency'

type FinanceTab = 'orders' | 'wallet' | 'refunds' | 'audit' | 'reconciliation'

const orderStatusOptions = [
  { value: '', label: '全部订单状态' },
  { value: 'pending_payment', label: '待支付' },
  { value: 'completed', label: '已完成' },
  { value: 'closed', label: '已关闭' },
  { value: 'needs_review', label: '需要核对' },
]

const paymentStatusOptions = [
  { value: '', label: '全部支付状态' },
  { value: 'unpaid', label: '未支付' },
  { value: 'paid', label: '支付成功' },
  { value: 'failed', label: '支付失败' },
  { value: 'refunded', label: '已退款' },
  { value: 'unknown', label: '支付未知' },
]

const creditStatusOptions = [
  { value: '', label: '全部到账状态' },
  { value: 'not_credited', label: '未入账' },
  { value: 'credited', label: '已入账' },
  { value: 'needs_review', label: '到账待核对' },
]

const reconciliationKindOptions = [
  { value: 'wallet_rebuild', label: '钱包重建核对' },
  { value: 'wallet_unfreeze', label: '钱包解冻核对' },
  { value: 'payment_orders', label: '支付订单核对' },
  { value: 'payment_inbox', label: '支付回调核对' },
]

const refundKindOptions = [
  { value: 'abnormal_payment', label: '异常支付' },
  { value: 'duplicate_payment', label: '重复支付' },
  { value: 'chargeback', label: '渠道退回' },
  { value: 'manual_correction', label: '人工更正' },
]

const refundStatusOptions = [
  { value: '', label: '全部退款状态' },
  { value: 'requested', label: '已申请' },
  { value: 'reserved', label: '已预占' },
  { value: 'submitted', label: '已提交渠道' },
  { value: 'succeeded', label: '已退款' },
  { value: 'failed', label: '失败' },
  { value: 'unknown', label: '状态待确认' },
  { value: 'cancelled', label: '已取消' },
]

function orderTone(order: Order): 'green' | 'amber' | 'red' | 'neutral' | 'violet' {
  if (order.credit_status === 'credited') return 'green'
  if (order.credit_status === 'needs_review' || order.status === 'abnormal') return 'red'
  if (order.payment_status === 'paid') return 'amber'
  if (order.payment_status === 'refunded') return 'violet'
  return 'neutral'
}

function paymentLabel(status: string): string {
  const labels: Record<string, string> = {
    unpaid: '未支付',
    paid: '支付成功',
    failed: '支付失败',
    refunded: '已退款',
    unknown: '支付未知',
  }
  return labels[status] ?? status
}

function creditLabel(status: string): string {
  const labels: Record<string, string> = {
    not_credited: '未入账',
    credited: '已入账',
    needs_review: '到账待核对',
  }
  return labels[status] ?? status
}

export function FinancePage() {
  const { me } = useAuth()
  const canAdjust = hasPermission(me, 'wallet.adjust')
  const canRefund = hasPermission(me, 'wallet.refund')
  const canReconcile = hasPermission(me, 'reconciliation.manage')
  const canReadAudit = hasPermission(me, 'audit.read')
  const [searchParams] = useSearchParams()
  const initialWallet = searchParams.get('wallet') ?? ''
  const initialUser = searchParams.get('user') ?? null
  const initialAction = searchParams.get('action') ?? ''
  const [tab, setTab] = useState<FinanceTab>(initialWallet ? 'wallet' : 'orders')
  const [walletTarget, setWalletTarget] = useState<{ walletId: string; userId: string | null; orderId: string | null } | null>(
    initialWallet ? { walletId: initialWallet, userId: initialUser, orderId: null } : null,
  )
  const [reconciliationTarget, setReconciliationTarget] = useState<{ orderId: string; walletId: string | null; userId: string | null } | null>(null)
  const [adjustTarget, setAdjustTarget] = useState<{ walletId: string; userId: string | null } | null>(
    initialWallet ? { walletId: initialWallet, userId: initialUser } : null,
  )
  const [adjustOpen, setAdjustOpen] = useState(Boolean(initialWallet && initialAction === 'adjust' && canAdjust))
  const [refundRefresh, setRefundRefresh] = useState(0)
  const summary = useAsync(() => getFinanceSummary().then(response => response.data), [])
  const billing = useAsync(() => getBillingSettings().then(response => response.data), [])
  const handledQuery = useRef(`${initialWallet}|${initialUser ?? ''}|${initialAction}`)

  useEffect(() => {
    const walletId = searchParams.get('wallet') ?? ''
    const userId = searchParams.get('user') ?? null
    const action = searchParams.get('action') ?? ''
    const key = `${walletId}|${userId ?? ''}|${action}`
    if (!walletId || key === handledQuery.current) return
    handledQuery.current = key
    setWalletTarget({ walletId, userId, orderId: null })
    setTab('wallet')
    if (action === 'adjust' && canAdjust) {
      setAdjustTarget({ walletId, userId })
      setAdjustOpen(true)
    }
  }, [searchParams, canAdjust])

  const openWallet = (target: { walletId: string; userId: string | null; orderId: string | null }) => {
    setWalletTarget(target)
    setTab('wallet')
  }

  const refresh = () => {
    summary.reload()
    billing.reload()
  }

  const billingReady = billing.data
  const checkoutState = billingReady
    ? billingReady.checkout_ready
      ? '支付入口已就绪'
      : '支付入口未就绪'
    : '支付配置未提供'
  const current = summary.data?.current
  const range = summary.data?.range
  const rangeMetrics = summary.data?.range_metrics

  return (
    <>
      <PageHeader
        eyebrow="财务与对账"
        title="财务与订单"
        description="用户支付与余额到账分别记录。汇总、订单、钱包流水、退款、人工调整与对账可在本页核对。"
        actions={
          <>
            <Button onClick={refresh} loading={summary.loading || billing.loading}>
              <RefreshCw size={14} /> 刷新
            </Button>
            {canAdjust ? (
              <Button onClick={() => { setAdjustTarget(null); setAdjustOpen(true) }}>
                <Wallet size={14} /> 人工余额调整
              </Button>
            ) : null}
          </>
        }
      />

      <div className="system-strip">
        <span>
          <span className={`service-dot ${billingReady?.checkout_ready ? 'healthy' : billing.error ? 'unavailable' : 'degraded'}`} />
          <b>{checkoutState}</b>
          <span className="strip-secondary">
            {billingReady
              ? `${billingReady.payment_provider?.toUpperCase() ?? '支付渠道'} · 服务实际扣费${billingReady.effective_service_charge_enabled ? '生效' : '未生效'} · 题库实际扣费${billingReady.effective_tiku_charge_enabled ? '生效' : '未生效'}`
              : '计费设置暂不可用'}
          </span>
        </span>
        <span className="strip-secondary">
          {summary.data
            ? `全量财务汇总${range?.all_time ? '（全部时间）' : ''} · 截至 ${formatDateTime(summary.data.as_of)}`
            : '全量财务汇总尚未载入'}
        </span>
      </div>

      <section className="metric-band finance-metrics">
        <div className="metric">
          <div className="metric-label">钱包可用余额</div>
          <div className="metric-value" style={{ fontSize: 22 }}>{current ? formatMoneyMinor(current.wallets.available_minor) : <span className="muted" style={{ fontSize: 15 }}>未提供</span>}</div>
          <div className="metric-foot"><span>{current ? `${formatNumber(current.wallets.count)} 个钱包 · 冻结 ${formatNumber(current.wallets.frozen_count)}` : '汇总未载入'}</span><Badge tone="green">全量</Badge></div>
        </div>
        <div className="metric">
          <div className="metric-label">支付成功未入账</div>
          <div className="metric-value" style={{ fontSize: 21 }}>{current ? formatMoneyMinor(current.paid_not_credited.amount_minor) : <span className="muted" style={{ fontSize: 15 }}>未提供</span>}</div>
          <div className="metric-foot"><span>{current ? `${formatNumber(current.paid_not_credited.count)} 笔 · 等待账本处理` : '汇总未载入'}</span></div>
        </div>
        <div className="metric">
          <div className="metric-label">订单待核对</div>
          <div className="metric-value" style={{ fontSize: 21 }}>{current ? formatMoneyMinor(current.orders_needs_review.amount_minor) : <span className="muted" style={{ fontSize: 15 }}>未提供</span>}</div>
          <div className="metric-foot"><span>{current ? `${formatNumber(current.orders_needs_review.count)} 笔 · 以账本为准` : '汇总未载入'}</span></div>
        </div>
        <div className="metric">
          <div className="metric-label">退款处理中</div>
          <div className="metric-value" style={{ fontSize: 21 }}>{current ? formatMoneyMinor(current.refunds_open.amount_minor) : <span className="muted" style={{ fontSize: 15 }}>未提供</span>}</div>
          <div className="metric-foot"><span>{current ? `${formatNumber(current.refunds_open.count)} 笔 · 不含已成功` : '汇总未载入'}</span></div>
        </div>
      </section>

      {summary.error && summary.data ? <RefreshError error={summary.error} onRetry={summary.reload} /> : null}
      {summary.error && !summary.data ? (
        <div style={{ marginBottom: 16 }}><ErrorState error={summary.error} onRetry={summary.reload} compact /></div>
      ) : null}
      {billing.error ? (
        <div style={{ marginBottom: 16 }}><ErrorState error={billing.error} onRetry={billing.reload} compact /></div>
      ) : null}

      {rangeMetrics ? (
        <Card className="finance-aggregate-card">
          <div className="finance-aggregate-heading">
            <div>
              <h3>{range?.all_time ? '全部时间资金汇总' : '区间资金汇总'}</h3>
              <p>支付、入账、退款和账本结算分别统计；汇总覆盖当前时间范围内的全部记录。</p>
            </div>
            <Badge tone="blue">{range?.all_time ? '全量' : '区间'}</Badge>
          </div>
          <div className="finance-aggregate-grid">
            <div><span>支付成功</span><b>{formatMoneyMinor(rangeMetrics.payment_succeeded.amount_minor)}</b><small>{formatNumber(rangeMetrics.payment_succeeded.count)} 笔</small></div>
            <div><span>余额已入账</span><b>{formatMoneyMinor(rangeMetrics.payment_credited.amount_minor)}</b><small>{formatNumber(rangeMetrics.payment_credited.count)} 笔</small></div>
            <div><span>退款成功</span><b>{formatMoneyMinor(rangeMetrics.refund_succeeded.amount_minor)}</b><small>{formatNumber(rangeMetrics.refund_succeeded.count)} 笔</small></div>
            <div><span>预占结算</span><b>{formatMoneyMinor(rangeMetrics.ledger_captured.amount_minor)}</b><small>{formatNumber(rangeMetrics.ledger_captured.count)} 笔</small></div>
            <div><span>人工增加</span><b>{formatMoneyMinor(rangeMetrics.ledger_manual_credit.amount_minor)}</b><small>{formatNumber(rangeMetrics.ledger_manual_credit.count)} 笔</small></div>
            <div><span>人工扣减</span><b>{formatMoneyMinor(rangeMetrics.ledger_manual_debit.amount_minor)}</b><small>{formatNumber(rangeMetrics.ledger_manual_debit.count)} 笔</small></div>
            <div><span>退款支出</span><b>{formatMoneyMinor(rangeMetrics.ledger_refund_payout.amount_minor)}</b><small>{formatNumber(rangeMetrics.ledger_refund_payout.count)} 笔</small></div>
            <div><span>新建订单</span><b>{formatNumber(rangeMetrics.orders_created.count)} 笔</b><small>{formatMoneyMinor(rangeMetrics.orders_created.amount_minor)}</small></div>
          </div>
        </Card>
      ) : null}

      <Card>
        <Tabs
          value={tab}
          onChange={value => setTab(value as FinanceTab)}
          items={[
            { value: 'orders', label: '充值订单' },
            { value: 'wallet', label: '钱包流水' },
            { value: 'refunds', label: '退款' },
            ...(canReadAudit ? [{ value: 'audit', label: '人工操作与审计' }] : []),
            ...(canReconcile ? [{ value: 'reconciliation', label: '对账' }] : []),
          ]}
        />
        {tab === 'orders' ? (
          <OrdersPanel
            refundReady={Boolean(billingReady?.refund_ready && canRefund)}
            canReconcile={canReconcile}
            onOpenWallet={openWallet}
            onOpenReconciliation={target => setReconciliationTarget(target)}
            onChanged={() => { summary.reload(); setRefundRefresh(value => value + 1) }}
          />
        ) : null}
        {tab === 'wallet' ? (
          <WalletPanel
            target={walletTarget}
            onChooseWallet={target => setWalletTarget(target)}
            onAdjust={wallet => { setAdjustTarget(wallet); setAdjustOpen(true) }}
            canAdjust={canAdjust}
          />
        ) : null}
        {tab === 'refunds' ? (
          <RefundsPanel
            refreshToken={refundRefresh}
            canRefund={canRefund}
            onResolved={() => {
              summary.reload()
              setRefundRefresh(value => value + 1)
            }}
          />
        ) : null}
        {canReadAudit && tab === 'audit' ? (
          <AuditPanel onAdjust={() => { setAdjustTarget(null); setAdjustOpen(true) }} canAdjust={canAdjust} />
        ) : null}
        {canReconcile && tab === 'reconciliation' ? (
          <ReconciliationPanel
            initialTarget={reconciliationTarget}
            onTargetHandled={() => setReconciliationTarget(null)}
            onCreated={() => summary.reload()}
            canReconcile={canReconcile}
          />
        ) : null}
      </Card>

      <div className="card-footer-link" style={{ border: 0, paddingLeft: 2 }}>
        <ShieldCheck size={12} /> 付款返回不会直接增加余额。金额以最小单位保存，账本流水只能通过调整或对账流程更正。
        <Link to="/settings?section=billing">查看计费设置 <ArrowRight size={12} /></Link>
      </div>

      <AdjustmentDialog
        open={adjustOpen}
        target={adjustTarget}
        onClose={() => { setAdjustOpen(false); setAdjustTarget(null) }}
        onSaved={() => summary.reload()}
      />
    </>
  )
}

function OrdersPanel({
  refundReady,
  canReconcile,
  onOpenWallet,
  onOpenReconciliation,
  onChanged,
}: {
  refundReady: boolean
  canReconcile: boolean
  onOpenWallet: (target: { walletId: string; userId: string | null; orderId: string | null }) => void
  onOpenReconciliation: (target: { orderId: string; walletId: string | null; userId: string | null }) => void
  onChanged: () => void
}) {
  const [status, setStatus] = useState('')
  const [paymentStatus, setPaymentStatus] = useState('')
  const [creditStatus, setCreditStatus] = useState('')
  const [userId, setUserId] = useState('')
  const debouncedUserId = useDebouncedValue(userId, 350)
  const [detailId, setDetailId] = useState<string | null>(null)
  const [refundOrder, setRefundOrder] = useState<Order | null>(null)
  const cursor = useCursorStack(`${status}|${paymentStatus}|${creditStatus}|${debouncedUserId}`)
  const orders = useAsync(
    () =>
      listOrders({
        status: status || undefined,
        paymentStatus: paymentStatus || undefined,
        creditStatus: creditStatus || undefined,
        userId: debouncedUserId || undefined,
        cursor: cursor.cursor,
        limit: 25,
      }).then(response => response.data),
    [status, paymentStatus, creditStatus, debouncedUserId, cursor.cursor],
  )
  const detail = orders.data?.items.find(order => order.id === detailId) ?? null

  const resetFilters = () => {
    setStatus('')
    setPaymentStatus('')
    setCreditStatus('')
    setUserId('')
  }

  return (
    <>
      <div className="table-toolbar">
        <div className="table-toolbar-group">
          <Select label="订单状态" value={status} onChange={setStatus} options={orderStatusOptions} />
          <Select label="支付状态" value={paymentStatus} onChange={setPaymentStatus} options={paymentStatusOptions} />
          <Select label="到账状态" value={creditStatus} onChange={setCreditStatus} options={creditStatusOptions} />
          <label className="filter-input">
            <span>用户编号</span>
            <input
              value={userId}
              onChange={event => setUserId(event.target.value)}
              placeholder="按用户编号筛选"
              aria-label="按用户编号筛选订单"
            />
          </label>
        </div>
        <Button size="sm" variant="ghost" onClick={resetFilters}>
          <X size={13} /> 清除筛选
        </Button>
      </div>

      {orders.error && orders.data ? <RefreshError error={orders.error} onRetry={orders.reload} /> : null}
      {orders.loading && !orders.data ? (
        <LoadingBlock rows={5} />
      ) : orders.error && !orders.data ? (
        <div style={{ padding: 18 }}><ErrorState error={orders.error} onRetry={orders.reload} /></div>
      ) : orders.data?.items.length ? (
        <>
          <div className="table-scroll">
            <table className="data-table order-table">
              <thead>
                <tr>
                  <th>订单</th>
                  <th>用户编号</th>
                  <th>金额</th>
                  <th>用户支付</th>
                  <th>余额到账</th>
                  <th>渠道</th>
                  <th>创建时间</th>
                  <th aria-label="操作" />
                </tr>
              </thead>
              <tbody>
                {orders.data.items.map(order => (
                  <tr key={order.id}>
                    <td>
                      <button className="cell-button" onClick={() => setDetailId(order.id)}>
                        <span className="cell-stack">
                          <span className="cell-primary mono">{order.id}</span>
                          <span className="cell-sub">{order.merchant_order_no ?? '商户订单号未提供'}</span>
                        </span>
                      </button>
                    </td>
                    <td className="mono">{order.user_id ?? '未提供'}</td>
                    <td className="cell-primary"><Money minor={order.amount_minor} /></td>
                    <td><Badge tone={order.payment_status === 'paid' ? 'green' : order.payment_status === 'failed' ? 'red' : 'neutral'}>{paymentLabel(order.payment_status)}</Badge></td>
                    <td><Badge tone={order.credit_status === 'credited' ? 'green' : order.credit_status === 'needs_review' ? 'red' : 'amber'}>{creditLabel(order.credit_status)}</Badge></td>
                    <td>{order.channel ?? order.provider ?? '未提供'}</td>
                    <td className="muted">{formatDateTime(order.created_at)}</td>
                    <td>
                      <IconButton label={`查看订单 ${order.id}`} onClick={() => setDetailId(order.id)}>
                        <ChevronRight size={16} />
                      </IconButton>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="mobile-scroll-hint">左右滑动查看完整表格</p>
          <Pagination
            hasPrevious={cursor.hasPrevious}
            hasNext={Boolean(orders.data.next_cursor)}
            onPrevious={cursor.goPrevious}
            onNext={() => cursor.goNext(orders.data?.next_cursor ?? null)}
            page={cursor.page}
            info={`当前页 ${orders.data.items.length} 笔订单${orders.data.next_cursor ? '，还有更多记录' : '，已到末页'}`}
            disabled={orders.loading}
          />
        </>
      ) : (
        <EmptyState title="没有符合条件的订单" description="调整支付、到账或用户编号筛选后再试。汇总覆盖全部记录，不受当前页筛选影响。" />
      )}

      <Modal
        open={Boolean(detail)}
        onClose={() => setDetailId(null)}
        title="订单详情"
        description={detail ? `充值订单 ${detail.id}` : undefined}
        drawer
        actions={
          detail ? (
            <>
              {detail.wallet_id ? (
                <Button
                  onClick={() => {
                    setDetailId(null)
                    onOpenWallet({ walletId: detail.wallet_id!, userId: detail.user_id, orderId: detail.id })
                  }}
                >
                  <Wallet size={14} /> 钱包流水
                </Button>
              ) : null}
              {canReconcile ? (
                <Button
                  onClick={() => {
                    setDetailId(null)
                    onOpenReconciliation({ orderId: detail.id, walletId: detail.wallet_id, userId: detail.user_id })
                  }}
                >
                  <FileCheck2 size={14} /> 发起对账
                </Button>
              ) : null}
              {refundReady && detail.payment_status === 'paid' && detail.credit_status === 'credited' ? (
                <Button variant="danger" onClick={() => setRefundOrder(detail)}>
                  发起退款
                </Button>
              ) : null}
              <Button onClick={() => setDetailId(null)}>关闭</Button>
            </>
          ) : null
        }
      >
        {detail ? (
          <>
            <div className="order-detail-amount">
              <span>充值金额</span>
              <b>{formatMoneyMinor(detail.amount_minor)}</b>
              <Badge tone={orderTone(detail)} dot>{detail.status_label ?? detail.status}</Badge>
            </div>
            <dl className="detail-list">
              <div><dt>本地订单</dt><dd className="mono">{detail.id}</dd></div>
              <div><dt>用户编号</dt><dd className="mono">{detail.user_id ?? '未提供'}</dd></div>
              <div><dt>钱包编号</dt><dd className="mono">{detail.wallet_id ?? '未提供'}</dd></div>
              <div><dt>商户订单号</dt><dd className="mono">{detail.merchant_order_no ?? '未提供'}</dd></div>
              <div><dt>支付渠道</dt><dd>{[detail.provider, detail.channel].filter(Boolean).join(' · ') || '未提供'}</dd></div>
              <div><dt>用户支付</dt><dd>{paymentLabel(detail.payment_status)}</dd></div>
              <div><dt>支付时间</dt><dd>{formatDateTime(detail.paid_at)}</dd></div>
              <div><dt>余额到账</dt><dd>{creditLabel(detail.credit_status)}</dd></div>
              <div><dt>到账时间</dt><dd>{formatDateTime(detail.credited_at)}</dd></div>
              <div><dt>迟到支付</dt><dd>{detail.late_payment === null ? '未提供' : detail.late_payment ? '是' : '否'}</dd></div>
              <div><dt>结账状态</dt><dd>{detail.checkout_state ?? '未提供'}</dd></div>
              <div><dt>创建时间</dt><dd>{formatDateTime(detail.created_at)}</dd></div>
              <div><dt>过期时间</dt><dd>{formatDateTime(detail.expires_at)}</dd></div>
              <div><dt>关闭时间</dt><dd>{formatDateTime(detail.closed_at)}</dd></div>
            </dl>
            <div className="callout" style={{ marginTop: 16 }}>
              <ShieldCheck size={15} />
              <div>支付成功与余额到账是两项独立事实。付款返回、渠道成功或页面跳转都不直接增加余额。</div>
            </div>
          </>
        ) : null}
      </Modal>

      <RefundDialog
        order={refundOrder}
        onClose={() => setRefundOrder(null)}
        onSaved={() => {
          onChanged()
          orders.reload()
        }}
      />
    </>
  )
}

function WalletPanel({
  target,
  onChooseWallet,
  onAdjust,
  canAdjust,
}: {
  target: { walletId: string; userId: string | null; orderId: string | null } | null
  onChooseWallet: (target: { walletId: string; userId: string | null; orderId: string | null }) => void
  onAdjust: (target: { walletId: string; userId: string | null }) => void
  canAdjust: boolean
}) {
  const { notify } = useToast()
  const [input, setInput] = useState(target?.walletId ?? '')
  const [walletId, setWalletId] = useState(target?.walletId ?? '')
  const [snapshot, setSnapshot] = useState<{ walletId: string; wallet: WalletSnapshot } | null>(null)
  const cursor = useCursorStack(walletId)
  const entries = useAsync(
    () =>
      walletId
        ? getWalletEntries(walletId, { cursor: cursor.cursor, limit: 25 }).then(response => response)
        : Promise.resolve(null),
    [walletId, cursor.cursor],
    { enabled: Boolean(walletId), resetOnDepsChange: true },
  )

  useEffect(() => {
    if (target?.walletId) {
      setInput(target.walletId)
      setWalletId(target.walletId)
    }
  }, [target?.walletId])

  useEffect(() => {
    const wallet = entries.data?.wallet
    if (wallet) setSnapshot({ walletId, wallet })
  }, [entries.data?.wallet, walletId])

  const activeSnapshot = snapshot && snapshot.walletId === walletId ? snapshot.wallet : null

  const load = () => {
    const value = input.trim()
    if (!value) {
      notify('请输入钱包编号。', 'warning')
      return
    }
    const sameWallet = value === target?.walletId
    setSnapshot(null)
    setWalletId(value)
    onChooseWallet({
      walletId: value,
      userId: sameWallet ? target?.userId ?? null : null,
      orderId: sameWallet ? target?.orderId ?? null : null,
    })
  }

  return (
    <>
      <div className="finance-section-head">
        <div>
          <h3>钱包流水</h3>
          <p>钱包编号用于读取流水。可从订单详情进入，或直接输入钱包编号。</p>
        </div>
        <div className="wallet-query">
          <input
            value={input}
            onChange={event => setInput(event.target.value)}
            aria-label="钱包编号"
            placeholder="钱包 UUID"
            className="mono"
          />
          <Button size="sm" onClick={load} loading={entries.loading}>读取流水</Button>
          {canAdjust && walletId ? (
            <Button size="sm" variant="ghost" onClick={() => onAdjust({ walletId, userId: target?.userId ?? null })}>
              <Wallet size={13} /> 调整此钱包
            </Button>
          ) : null}
        </div>
      </div>
      <div className="wallet-search-row">
        <WalletSearchBox
          onSelect={wallet => {
            setInput(wallet.walletId)
            setSnapshot(null)
            setWalletId(wallet.walletId)
            onChooseWallet({ walletId: wallet.walletId, userId: wallet.userId, orderId: null })
          }}
        />
        <span className="form-hint">搜索只返回已有钱包；支持用户或钱包 UUID。</span>
      </div>

      {target?.orderId ? (
        <div className="wallet-context">
          <span>来源订单 <b className="mono">{target.orderId}</b></span>
          <span>用户编号 <b className="mono">{target.userId ?? '未提供'}</b></span>
          <span>钱包编号 <b className="mono">{walletId || '未提供'}</b></span>
        </div>
      ) : null}

      {walletId && activeSnapshot ? (
        <div className="wallet-context">
          <span>用户编号 <b className="mono">{target?.userId ?? '未提供'}</b></span>
          <span>可用余额 <b>{formatMoneyMinor(activeSnapshot.available_minor)}</b></span>
          <span>预占金额 <b>{formatMoneyMinor(activeSnapshot.reserved_minor)}</b></span>
          <span>题库计费尾差 <b>{formatPriceMicros(activeSnapshot.tiku_accrued_micros)}</b></span>
          <span>钱包状态 <b>{activeSnapshot.frozen === null ? '未提供' : activeSnapshot.frozen ? `已冻结${activeSnapshot.frozen_reason ? ` · ${activeSnapshot.frozen_reason}` : ''}` : '正常'}</b></span>
        </div>
      ) : null}

      {!walletId ? (
        <div className="finance-empty-inline">
          <Wallet size={18} />
          <p>从订单详情选择“钱包流水”，或在下方输入钱包编号。账本不会用用户列表推算余额。</p>
        </div>
      ) : entries.loading && !entries.data ? (
        <LoadingBlock rows={4} />
      ) : entries.error ? (
        <div style={{ padding: 18 }}><ErrorState error={entries.error} onRetry={entries.reload} /></div>
      ) : entries.data?.list.items.length ? (
        <>
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>记账时间</th>
                  <th>业务</th>
                  <th>可用变动</th>
                  <th>预占变动</th>
                  <th>说明</th>
                  <th>关联单据</th>
                </tr>
              </thead>
              <tbody>
                {entries.data.list.items.map(entry => (
                  <tr key={entry.id}>
                    <td className="muted">{formatDateTime(entry.created_at)}</td>
                    <td><span className="cell-primary">{entry.type_label ?? entry.kind}</span><small className="cell-sub mono">{entry.id}</small></td>
                    <td className={Number(entry.available_delta_minor) > 0 ? 'positive-text' : Number(entry.available_delta_minor) < 0 ? 'negative-text' : ''}>
                      <Money minor={entry.available_delta_minor} signed={Number(entry.available_delta_minor) > 0} />
                    </td>
                    <td className={Number(entry.reserved_delta_minor) > 0 ? 'positive-text' : Number(entry.reserved_delta_minor) < 0 ? 'negative-text' : ''}>
                      <Money minor={entry.reserved_delta_minor} signed={Number(entry.reserved_delta_minor) > 0} />
                    </td>
                    <td className="muted" style={{ whiteSpace: 'normal', maxWidth: 280 }}>{entry.description ?? '未提供'}</td>
                    <td className="mono muted">{entry.business_id ?? '未提供'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="mobile-scroll-hint">左右滑动查看完整表格</p>
          <Pagination
            hasPrevious={cursor.hasPrevious}
            hasNext={Boolean(entries.data.list.next_cursor)}
            onPrevious={cursor.goPrevious}
            onNext={() => cursor.goNext(entries.data?.list.next_cursor ?? null)}
            page={cursor.page}
            info={`当前页 ${entries.data.list.items.length} 条流水${entries.data.list.next_cursor ? '，还有更多记录' : '，已到末页'}`}
          />
        </>
      ) : (
        <EmptyState title="暂无钱包流水" description="该钱包暂无流水记录。" />
      )}
    </>
  )
}

function RefundsPanel({
  refreshToken,
  canRefund,
  onResolved,
}: {
  refreshToken: number
  canRefund: boolean
  onResolved: () => void
}) {
  const [status, setStatus] = useState('')
  const [orderId, setOrderId] = useState('')
  const [userId, setUserId] = useState('')
  const [reviewing, setReviewing] = useState<Refund | null>(null)
  const debouncedOrder = useDebouncedValue(orderId, 350)
  const debouncedUser = useDebouncedValue(userId, 350)
  const cursor = useCursorStack(`${status}|${debouncedOrder}|${debouncedUser}`)
  const refunds = useAsync(
    () =>
      listRefunds({
        status: status || undefined,
        orderId: debouncedOrder || undefined,
        userId: debouncedUser || undefined,
        cursor: cursor.cursor,
        limit: 25,
      }).then(response => response.data),
    [status, debouncedOrder, debouncedUser, cursor.cursor, refreshToken],
  )

  const clear = () => {
    setStatus('')
    setOrderId('')
    setUserId('')
  }

  return (
    <>
      <div className="finance-section-head">
        <div>
          <h3>退款记录</h3>
          <p>退款请求、渠道处理状态与原订单分别记录。</p>
        </div>
        <Button size="sm" onClick={() => refunds.reload()} loading={refunds.loading}>
          <RefreshCw size={13} /> 刷新
        </Button>
      </div>
      <div className="table-toolbar">
        <div className="table-toolbar-group">
          <Select label="退款状态" value={status} onChange={setStatus} options={refundStatusOptions} />
          <label className="filter-input">
            <span>订单</span>
            <input value={orderId} onChange={event => setOrderId(event.target.value)} className="mono" placeholder="订单 UUID" aria-label="按订单筛选退款" />
          </label>
          <label className="filter-input">
            <span>用户</span>
            <input value={userId} onChange={event => setUserId(event.target.value)} className="mono" placeholder="用户 UUID" aria-label="按用户筛选退款" />
          </label>
        </div>
        <Button size="sm" variant="ghost" onClick={clear}><X size={13} /> 清除筛选</Button>
      </div>
      {refunds.loading && !refunds.data ? (
        <LoadingBlock rows={4} />
      ) : refunds.error && !refunds.data ? (
        <div style={{ padding: 18 }}><ErrorState error={refunds.error} onRetry={refunds.reload} /></div>
      ) : refunds.data?.items.length ? (
        <>
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>退款</th>
                  <th>订单</th>
                  <th>用户</th>
                  <th>金额</th>
                  <th>类型</th>
                  <th>状态</th>
                  <th>原因</th>
                  <th>创建 / 完成</th>
                  <th aria-label="操作" />
                </tr>
              </thead>
              <tbody>
                {refunds.data.items.map(refund => (
                  <tr key={refund.id}>
                    <td className="mono">{refund.id}</td>
                    <td className="mono">{refund.order_id}</td>
                    <td className="mono">{refund.user_id ?? '未提供'}</td>
                    <td className="cell-primary"><Money minor={refund.amount_minor} /></td>
                    <td>{refund.kind ? refundKindOptions.find(option => option.value === refund.kind)?.label ?? refund.kind : '未提供'}</td>
                    <td><Badge tone={refundTone(refund.status)}>{refund.status_label ?? refund.status}</Badge></td>
                    <td className="muted" style={{ whiteSpace: 'normal', maxWidth: 260 }}>{refund.reason ?? '未提供'}</td>
                    <td className="muted">
                      <div>{formatDateTime(refund.created_at)}</div>
                      <div>{formatDateTime(refund.completed_at)}</div>
                    </td>
                    <td>
                      {canRefund && refundNeedsReview(refund.status) ? (
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`复核退款 ${refund.id}`}
                          onClick={() => setReviewing(refund)}
                        >
                          <FileCheck2 size={12} /> 复核结果
                        </Button>
                      ) : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="mobile-scroll-hint">左右滑动查看完整表格</p>
          <Pagination
            hasPrevious={cursor.hasPrevious}
            hasNext={Boolean(refunds.data.next_cursor)}
            onPrevious={cursor.goPrevious}
            onNext={() => cursor.goNext(refunds.data?.next_cursor ?? null)}
            page={cursor.page}
            info={`当前页 ${refunds.data.items.length} 笔退款${refunds.data.next_cursor ? '，还有更多记录' : '，已到末页'}`}
            disabled={refunds.loading}
          />
        </>
      ) : (
        <EmptyState title={status || debouncedOrder || debouncedUser ? '没有符合条件的退款' : '暂无退款记录'} description="调整状态、订单或用户筛选后再试。" />
      )}
      <RefundReviewDialog
        refund={reviewing}
        onClose={() => setReviewing(null)}
        onResolved={() => onResolved()}
      />
    </>
  )
}

const refundReviewStatuses = new Set(['reserved', 'submitted', 'unknown'])

function refundNeedsReview(status: string): boolean {
  return refundReviewStatuses.has(status)
}

function requiresReauth(error: unknown): boolean {
  // 账本近期验证失败按既有财务口径统一返回 403；沿用其他资金操作的重新验证提示。
  return error instanceof ApiError && error.status === 403
}

function refundTone(status: string): 'green' | 'amber' | 'red' | 'neutral' | 'violet' {
  if (status === 'succeeded') return 'green'
  if (status === 'failed') return 'red'
  if (status === 'requested' || status === 'reserved' || status === 'submitted') return 'amber'
  if (status === 'cancelled') return 'neutral'
  return 'violet'
}

function RefundReviewDialog({
  refund,
  onClose,
  onResolved,
}: {
  refund: Refund | null
  onClose: () => void
  onResolved: () => void
}) {
  const { notify } = useToast()
  const [outcome, setOutcome] = useState<'succeeded' | 'failed' | null>(null)
  const [reason, setReason] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<unknown | null>(null)
  const [result, setResult] = useState<Refund | null>(null)
  const [stepUpOpen, setStepUpOpen] = useState(false)
  const idempotency = useIdempotencyKey()

  useEffect(() => {
    if (refund) {
      idempotency.reset()
      setOutcome(null)
      setReason('')
      setError(null)
      setResult(null)
      setStepUpOpen(false)
    }
    // refund 变化代表打开另一笔退款，仅此时重置输入。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refund])

  if (!refund) return null

  const submit = async () => {
    if (!outcome) {
      setError(new Error('请选择退款核实结果：已退款或未退款。'))
      return
    }
    const trimmed = reason.trim()
    if (trimmed.length < 5) {
      setError(new Error('请填写至少 5 个字符的核实依据。'))
      return
    }
    setSaving(true)
    setError(null)
    try {
      const payload = { refundId: refund.id, outcome, reason: trimmed }
      const updated = await resolveRefund(
        refund.id,
        { outcome, reason: trimmed },
        idempotency.keyFor(payload),
      )
      setResult(updated)
      notify(outcome === 'succeeded' ? '已记录为已退款。' : '已记录为未退款。', 'success')
      onResolved()
    } catch (reasonValue) {
      setError(reasonValue)
      if (requiresReauth(reasonValue)) setStepUpOpen(true)
    } finally {
      setSaving(false)
    }
  }

  const statusLabel = (status: string) =>
    refundStatusOptions.find(option => option.value === status)?.label ?? status

  return (
    <Modal
      open
      onClose={onClose}
      title="复核退款结果"
      description={`退款 ${refund.id} · 订单 ${refund.order_id}`}
      wide
      actions={
        result ? (
          <Button variant="primary" onClick={onClose}>关闭</Button>
        ) : (
          <>
            <Button onClick={onClose} disabled={saving}>取消</Button>
            <Button variant="primary" onClick={() => void submit()} loading={saving}>提交复核结果</Button>
          </>
        )
      }
    >
      {result ? (
        <div className="callout callout-success" role="status">
          <CheckCircle2 size={16} />
          <div>
            {result.status === 'succeeded' ? '已记录为已退款。' : '已记录为未退款。'}
            <div className="form-hint" style={{ marginTop: 6 }}>退款状态：{statusLabel(result.status)}</div>
          </div>
        </div>
      ) : (
        <>
          <div className="callout callout-warning">
            <CircleAlert size={15} />
            <div>请先核对支付渠道的实际退款结果。这里只登记核实结果，不会再次发起渠道退款。</div>
          </div>
          <div className="privacy-actions" role="group" aria-label="选择退款核实结果" style={{ marginTop: 14 }}>
            <button
              type="button"
              className={`privacy-action ${outcome === 'succeeded' ? 'selected' : ''}`}
              aria-pressed={outcome === 'succeeded'}
              onClick={() => { setOutcome('succeeded'); setError(null) }}
            >
              <CheckCircle2 size={15} />
              <b>已退款</b>
              <span>渠道已确认退款成功，记录退款支出并完成本次退款。</span>
            </button>
            <button
              type="button"
              className={`privacy-action ${outcome === 'failed' ? 'selected' : ''}`}
              aria-pressed={outcome === 'failed'}
              onClick={() => { setOutcome('failed'); setError(null) }}
            >
              <CircleAlert size={15} />
              <b>未退款</b>
              <span>渠道没有退款，预占金额退回可用余额并结束本次退款。</span>
            </button>
          </div>
          <Field label="核实依据" required hint="填写渠道账单或交易号等核实依据，至少 5 个字符；会写入审计。">
            <textarea
              rows={4}
              value={reason}
              onChange={event => { setReason(event.target.value); if (error) setError(null) }}
              maxLength={500}
              placeholder="填写本次核实的渠道结果与依据…"
            />
          </Field>
          {error ? (
            <div style={{ marginTop: 12 }}>
              <ErrorState error={error} compact />
              {requiresReauth(error) ? (
                <div style={{ marginTop: 8 }}>
                  <Button size="sm" onClick={() => setStepUpOpen(true)}>重新验证管理员身份</Button>
                </div>
              ) : null}
            </div>
          ) : null}
        </>
      )}
      <StepUpDialog
        open={stepUpOpen}
        onClose={() => setStepUpOpen(false)}
        onSuccess={() => { void submit() }}
      />
    </Modal>
  )
}

function WalletSearchBox({
  onSelect,
  disabled = false,
}: {
  onSelect: (wallet: { walletId: string; userId: string | null; label: string }) => void
  disabled?: boolean
}) {
  const [query, setQuery] = useState('')
  const debounced = useDebouncedValue(query, 320)
  const [open, setOpen] = useState(false)
  const results = useAsync(
    () =>
      debounced.trim()
        ? listAdminWallets({ search: debounced.trim(), limit: 8 }).then(response => response.data)
        : Promise.resolve(null),
    [debounced],
  )

  return (
    <div className="user-lookup">
      <div onFocus={() => setOpen(true)}>
        <SearchInput
          value={query}
          onChange={value => { setQuery(value); setOpen(true) }}
          placeholder="搜索用户名称或 UUID"
        />
      </div>
      {open && debounced.trim() ? (
        <div className="user-lookup-results">
          {results.loading ? <span className="form-hint">正在搜索钱包…</span> : null}
          {results.error ? <span className="form-error">{describeError(results.error).message}</span> : null}
          {results.data?.items.length ? (
            results.data.items.map(item => (
              <button
                type="button"
                key={item.wallet.wallet_id}
                className="user-lookup-option"
                disabled={disabled}
                onClick={() => {
                  onSelect({
                    walletId: item.wallet.wallet_id,
                    userId: item.user_id,
                    label: item.display_name || item.user_id,
                  })
                  setQuery('')
                  setOpen(false)
                }}
              >
                <Wallet size={14} />
                <span>
                  <b>{item.display_name || '未命名用户'}</b>
                  <small className="mono">{item.user_id} · 可用 {formatMoneyMinor(item.wallet.available_minor)} · 预占 {formatMoneyMinor(item.wallet.reserved_minor)}</small>
                </span>
                <ChevronRight size={13} />
              </button>
            ))
          ) : !results.loading && !results.error ? (
            <span className="form-hint">没有找到已有钱包的用户；可先打开用户详情创建或读取钱包。</span>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

function AuditPanel({ onAdjust, canAdjust }: { onAdjust: () => void; canAdjust: boolean }) {
  const cursor = useCursorStack('')
  const audit = useAsync(
    () => listAuditEvents({ cursor: cursor.cursor, limit: 25 }).then(response => response.data),
    [cursor.cursor],
  )

  return (
    <>
      <div className="finance-section-head">
        <div>
          <h3>人工操作与审计</h3>
          <p>显示脱敏审计记录。人工余额调整需要再次验证并填写独立理由。</p>
        </div>
        <div className="table-toolbar-group">
          <Button size="sm" onClick={() => audit.reload()} loading={audit.loading}>
            <RefreshCw size={13} /> 刷新
          </Button>
          {canAdjust ? (
            <Button size="sm" variant="primary" onClick={onAdjust}>
              <Wallet size={14} /> 人工余额调整
            </Button>
          ) : null}
        </div>
      </div>
      {audit.error && audit.data ? <RefreshError error={audit.error} onRetry={audit.reload} /> : null}
      {audit.loading && !audit.data ? (
        <LoadingBlock rows={5} />
      ) : audit.error && !audit.data ? (
        <div style={{ padding: 18 }}><ErrorState error={audit.error} onRetry={audit.reload} /></div>
      ) : audit.data?.items.length ? (
        <>
          <div className="audit-list finance-audit-list">
            {audit.data.items.map(event => (
              <div key={event.id}>
                <ShieldCheck size={16} />
                <span>
                  <b>{event.action_label ?? event.action}</b>
                  <p>
                    资源 {event.resource_type ?? '未提供'} · <span className="mono">{event.resource_id ?? '未提供'}</span>
                    {event.reason ? <><br />原因：{event.reason}</> : null}
                  </p>
                  <small className="form-hint">
                    操作者 {event.actor_kind ?? '未知'}{event.actor ? ` · ${event.actor}` : ''} · {formatDateTime(event.created_at)}
                    {event.request_id ? ` · 请求 ${event.request_id}` : ''}
                  </small>
                </span>
              </div>
            ))}
          </div>
          <Pagination
            hasPrevious={cursor.hasPrevious}
            hasNext={Boolean(audit.data.next_cursor)}
            onPrevious={cursor.goPrevious}
            onNext={() => cursor.goNext(audit.data?.next_cursor ?? null)}
            page={cursor.page}
            info={`当前页 ${audit.data.items.length} 条审计记录${audit.data.next_cursor ? '，还有更多记录' : '，已到末页'}`}
          />
        </>
      ) : (
        <EmptyState title="暂无审计记录" description="人工调整、账号限制与任务控制会记录到审计。" />
      )}
    </>
  )
}

function ReconciliationPanel({
  initialTarget,
  onTargetHandled,
  onCreated,
  canReconcile,
}: {
  initialTarget: { orderId: string; walletId: string | null; userId: string | null } | null
  onTargetHandled: () => void
  onCreated: () => void
  canReconcile: boolean
}) {
  const [status, setStatus] = useState('')
  const [kind, setKind] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [prefill, setPrefill] = useState<{ orderId?: string; walletId?: string; userId?: string } | null>(null)
  const cursor = useCursorStack(`${status}|${kind}`)
  const list = useAsync(
    () =>
      listReconciliations({
        status: status || undefined,
        kind: kind || undefined,
        cursor: cursor.cursor,
        limit: 25,
      }).then(response => response.data),
    [status, kind, cursor.cursor],
  )

  useEffect(() => {
    if (initialTarget && canReconcile) {
      setPrefill({
        orderId: initialTarget.orderId,
        walletId: initialTarget.walletId ?? undefined,
        userId: initialTarget.userId ?? undefined,
      })
      setDialogOpen(true)
      setStatus('')
      setKind('')
      onTargetHandled()
    }
  }, [initialTarget, canReconcile, onTargetHandled])

  const openNew = () => {
    setPrefill(null)
    setDialogOpen(true)
  }

  return (
    <>
      <div className="finance-section-head">
        <div>
          <h3>对账与异常复核</h3>
          <p>核对订单、支付与账本，查看差异、发现与完成时间。状态以核对结果为准。</p>
        </div>
        <div className="table-toolbar-group">
          {canReconcile ? (
            <Button size="sm" variant="primary" onClick={openNew}>
              <FileCheck2 size={13} /> 发起对账
            </Button>
          ) : null}
          <Button size="sm" onClick={() => list.reload()} loading={list.loading}>
            <RefreshCw size={13} /> 刷新
          </Button>
        </div>
      </div>
      <div className="table-toolbar">
        <div className="table-toolbar-group">
          <Select
            label="对账状态筛选"
            value={status}
            onChange={setStatus}
            options={[
              { value: '', label: '全部对账状态' },
              { value: 'pending', label: '待执行' },
              { value: 'running', label: '执行中' },
              { value: 'matched', label: '核对一致' },
              { value: 'mismatch', label: '存在差异' },
              { value: 'incomplete', label: '信息不完整' },
              { value: 'failed', label: '失败' },
            ]}
          />
          <Select
            label="对账类型筛选"
            value={kind}
            onChange={setKind}
            options={[{ value: '', label: '全部对账类型' }, ...reconciliationKindOptions]}
          />
        </div>
      </div>
      {list.error && list.data ? <RefreshError error={list.error} onRetry={list.reload} /> : null}
      {list.loading && !list.data ? (
        <LoadingBlock rows={4} />
      ) : list.error && !list.data ? (
        <div style={{ padding: 18 }}><ErrorState error={list.error} onRetry={list.reload} /></div>
      ) : list.data?.items.length ? (
        <>
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>对账记录</th>
                  <th>目标</th>
                  <th>状态</th>
                  <th>预期 / 实际 / 差异</th>
                  <th>发现</th>
                  <th>创建 / 完成</th>
                </tr>
              </thead>
              <tbody>
                {list.data.items.map(item => (
                  <tr key={item.id}>
                    <td><span className="cell-primary">{item.kind_label ?? item.kind}</span><small className="cell-sub mono">{item.id}</small></td>
                    <td className="mono muted">
                      {item.order_id ? <div>订单 {item.order_id}</div> : null}
                      {item.wallet_id ? <div>钱包 {item.wallet_id}</div> : null}
                      {item.user_id ? <div>用户 {item.user_id}</div> : null}
                      {!item.order_id && !item.wallet_id && !item.user_id ? '未指定' : null}
                    </td>
                    <td><Badge tone={item.status === 'matched' ? 'green' : item.status === 'mismatch' || item.status === 'failed' ? 'red' : item.status === 'pending' || item.status === 'running' ? 'amber' : 'neutral'}>{item.state_label ?? item.status}</Badge>{item.error_code ? <small className="cell-sub">{item.error_code}</small> : null}</td>
                    <td>
                      <span className="cell-sub">预期 {formatMoneyMinor(item.expected_minor)}</span>
                      <span className="cell-sub">实际 {formatMoneyMinor(item.actual_minor)}</span>
                      <span className="cell-sub">差异 {formatMoneyMinor(item.difference_minor)}</span>
                    </td>
                    <td className="muted" style={{ whiteSpace: 'normal', minWidth: 180 }}>
                      {item.findings.length ? item.findings.map(finding => <div key={finding.key}>{finding.key}: {finding.value}</div>) : '无'}
                    </td>
                    <td className="muted">
                      <div>{formatDateTime(item.created_at)}</div>
                      <div>{formatDateTime(item.completed_at)}</div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="mobile-scroll-hint">左右滑动查看完整表格</p>
          <Pagination
            hasPrevious={cursor.hasPrevious}
            hasNext={Boolean(list.data.next_cursor)}
            onPrevious={cursor.goPrevious}
            onNext={() => cursor.goNext(list.data?.next_cursor ?? null)}
            page={cursor.page}
            info={`当前页 ${list.data.items.length} 条对账记录${list.data.next_cursor ? '，还有更多记录' : '，已到末页'}`}
          />
        </>
      ) : (
        <EmptyState title="暂无对账记录" description="暂无对账记录，可发起一次钱包或支付订单核对。" />
      )}
      <ReconciliationDialog
        open={dialogOpen}
        prefill={prefill}
        onClose={() => setDialogOpen(false)}
        onCreated={() => {
          list.reload()
          onCreated()
        }}
      />
    </>
  )
}

function ReconciliationDialog({
  open,
  prefill,
  onClose,
  onCreated,
}: {
  open: boolean
  prefill: { orderId?: string; walletId?: string; userId?: string } | null
  onClose: () => void
  onCreated: () => void
}) {
  const { notify } = useToast()
  const [kind, setKind] = useState('payment_orders')
  const [orderId, setOrderId] = useState('')
  const [walletId, setWalletId] = useState('')
  const [userId, setUserId] = useState('')
  const [reason, setReason] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<unknown | null>(null)
  const [result, setResult] = useState<Reconciliation | null>(null)
  const [stepUpOpen, setStepUpOpen] = useState(false)
  const idempotency = useIdempotencyKey()

  useEffect(() => {
    if (!open) return
    idempotency.reset()
    setKind(prefill?.orderId ? 'payment_orders' : 'wallet_rebuild')
    setOrderId(prefill?.orderId ?? '')
    setWalletId(prefill?.walletId ?? '')
    setUserId(prefill?.userId ?? '')
    setReason('')
    setError(null)
    setResult(null)
  }, [open, prefill])

  const submit = async () => {
    if (reason.trim().length < 5) {
      setError(new Error('对账理由至少需要 5 个字符。'))
      return
    }
    if (kind === 'wallet_unfreeze' && !walletId.trim()) {
      setError(new Error('钱包解冻核对必须指定钱包编号。'))
      return
    }
    setSaving(true)
    setError(null)
    try {
      const payload = {
        kind,
        orderId: orderId.trim() || undefined,
        walletId: walletId.trim() || undefined,
        userId: userId.trim() || undefined,
        reason: reason.trim(),
      }
      const record = await createReconciliation(payload, idempotency.keyFor(payload))
      setResult(record)
      notify('对账请求已提交。', 'success')
      onCreated()
    } catch (reasonValue) {
      setError(reasonValue)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="发起对账"
      description="按类型核对订单、支付与账本，并记录差异。涉及资金的操作需要再次验证管理员身份。"
      wide
      actions={
        result ? (
          <Button variant="primary" onClick={onClose}>关闭</Button>
        ) : (
          <>
            <Button onClick={onClose} disabled={saving}>取消</Button>
            <Button variant="primary" onClick={() => void submit()} loading={saving}>提交对账</Button>
          </>
        )
      }
    >
      {result ? (
        <div className="callout callout-success">
          <CheckCircle2 size={16} />
          <div>
            对账记录已创建：{result.state_label ?? result.status}
            {result.difference_minor ? `，差异 ${formatMoneyMinor(result.difference_minor)}` : ''}。
            <br /><span className="form-hint">编号 {result.id}</span>
          </div>
        </div>
      ) : (
        <>
          <Field label="对账类型" required>
            <Select label="对账类型" value={kind} onChange={setKind} options={reconciliationKindOptions} />
          </Field>
          <div className="field-row">
            <Field label="订单编号" hint="支付类对账可指定订单；可留空。">
              <input value={orderId} onChange={event => setOrderId(event.target.value)} className="mono" placeholder="可选" />
            </Field>
            <Field label="钱包编号" hint="钱包解冻核对必须填写。">
              <input value={walletId} onChange={event => setWalletId(event.target.value)} className="mono" placeholder="可选" />
            </Field>
          </div>
          <Field label="用户编号" hint="可留空。">
            <input value={userId} onChange={event => setUserId(event.target.value)} className="mono" placeholder="可选" />
          </Field>
          <Field label="对账理由" required hint="至少 5 个字符，最多 500 个字符。">
            <textarea rows={3} value={reason} onChange={event => setReason(event.target.value)} maxLength={500} />
          </Field>
          {error ? (
            <div style={{ marginTop: 12 }}>
              <ErrorState error={error} compact />
              {describeError(error).status === 403 ? (
                <div style={{ marginTop: 8 }}><Button size="sm" onClick={() => setStepUpOpen(true)}>重新验证管理员身份</Button></div>
              ) : null}
            </div>
          ) : null}
          <div className="callout callout-warning" style={{ marginTop: 12 }}>
            <CircleAlert size={15} />
            <div>对账会冻结存在差异的钱包，需人工复核后再处理。</div>
          </div>
        </>
      )}
      <StepUpDialog open={stepUpOpen} onClose={() => setStepUpOpen(false)} />
    </Modal>
  )
}

function AdjustmentDialog({
  open,
  target,
  onClose,
  onSaved,
}: {
  open: boolean
  target: { walletId: string; userId: string | null } | null
  onClose: () => void
  onSaved: () => void
}) {
  const { notify } = useToast()
  const [walletId, setWalletId] = useState(target?.walletId ?? '')
  const [direction, setDirection] = useState<'credit' | 'debit'>('credit')
  const [amount, setAmount] = useState('')
  const [reason, setReason] = useState('')
  const [reference, setReference] = useState('')
  const [acknowledged, setAcknowledged] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<unknown | null>(null)
  const [result, setResult] = useState<null | { journalId: string | null; available: string | null; amount: string | null }>(null)
  const [stepUpOpen, setStepUpOpen] = useState(false)
  const idempotency = useIdempotencyKey()

  useEffect(() => {
    if (open) {
      idempotency.reset()
      setWalletId(target?.walletId ?? '')
      setDirection('credit')
      setAmount('')
      setReason('')
      setReference('')
      setAcknowledged(false)
      setError(null)
      setResult(null)
    }
  }, [open, target])

  const submit = async () => {
    if (!walletId.trim()) {
      setError(new Error('请输入钱包编号。'))
      return
    }
    const amountMinor = parseYuanToMinor(amount)
    if (amountMinor === null || amountMinor === '0' || amountMinor.startsWith('-')) {
      setError(new Error('请输入大于零的金额，最多两位小数。'))
      return
    }
    if (reason.trim().length < 5) {
      setError(new Error('调整理由至少需要 5 个字符。'))
      return
    }
    if (!acknowledged) {
      setError(new Error('请确认已核对钱包、方向与金额。'))
      return
    }
    setSaving(true)
    setError(null)
    try {
      const payload = {
        walletId: walletId.trim(),
        direction,
        amountMinor,
        reason: reason.trim(),
        reference: reference.trim(),
      }
      const raw = (await createWalletAdjustment(payload, idempotency.keyFor(payload))) as {
        journal_id?: string
        amount_minor?: string
        wallet?: { available_minor?: string }
      } | null
      setResult({
        journalId: raw?.journal_id ?? null,
        available: raw?.wallet?.available_minor ?? null,
        amount: raw?.amount_minor ?? null,
      })
      notify('人工余额调整已记账。', 'success')
      onSaved()
      setAcknowledged(false)
    } catch (reasonValue) {
      setError(reasonValue)
    } finally {
      setSaving(false)
    }
  }

  const errorDetails = error ? describeError(error) : null

  return (
    <>
      <Modal
        open={open}
        onClose={onClose}
        title="人工余额调整"
        description="直接调整指定钱包的可用余额。提交前会再次验证管理员身份，并核对余额与金额。"
        wide
        actions={
          result ? (
            <Button variant="primary" onClick={onClose}>完成</Button>
          ) : (
            <>
              <Button onClick={onClose} disabled={saving}>取消</Button>
              <Button variant="primary" onClick={() => void submit()} loading={saving}>确认并提交</Button>
            </>
          )
        }
      >
        {result ? (
          <div className="callout callout-success">
            <CheckCircle2 size={16} />
            <div>
              调整已写入账本。
              <br />
              流水 {result.journalId ?? '未提供编号'}
              {result.amount ? ` · 金额 ${formatMoneyMinor(result.amount)}` : ''}
              {result.available ? ` · 调整后可用 ${formatMoneyMinor(result.available)}` : ''}
            </div>
          </div>
        ) : (
          <>
            <Field label="搜索用户钱包" hint="按用户名称或 UUID 筛选已有钱包；也可直接填写钱包编号。">
              <WalletSearchBox onSelect={wallet => setWalletId(wallet.walletId)} disabled={saving} />
            </Field>
            <Field
              label="钱包编号"
              required
              hint={target?.userId ? `对应用户：${target.userId}` : '使用钱包编号；可从用户详情、订单详情或钱包搜索进入。'}
            >
              <input value={walletId} onChange={event => setWalletId(event.target.value)} className="mono" />
            </Field>
            <div className="field-row">
              <Field label="调整方向" required>
                <Select
                  label="调整方向"
                  value={direction}
                  onChange={value => setDirection(value as 'credit' | 'debit')}
                  options={[{ value: 'credit', label: '增加可用余额' }, { value: 'debit', label: '扣减可用余额' }]}
                />
              </Field>
              <Field label="调整金额（元）" required hint="必须大于零，最多两位小数。">
                <input value={amount} onChange={event => setAmount(event.target.value)} inputMode="decimal" placeholder="例如 10.00" />
              </Field>
            </div>
            <Field label="操作理由" required hint="至少 5 个字符，最多 500 个字符。">
              <textarea rows={3} value={reason} onChange={event => setReason(event.target.value)} maxLength={500} />
            </Field>
            <Field label="关联单据" hint="可选，最多 200 个字符。">
              <input value={reference} onChange={event => setReference(event.target.value)} maxLength={200} />
            </Field>
            <label className="native-check">
              <input type="checkbox" checked={acknowledged} onChange={event => setAcknowledged(event.target.checked)} />
              我已核对钱包、调整方向、金额与原因，并确认执行本次调整。
            </label>
            {error ? (
              <div style={{ marginTop: 12 }}>
                <ErrorState error={error} compact />
                {errorDetails?.status === 403 ? (
                  <div style={{ marginTop: 8 }}>
                    <Button size="sm" onClick={() => setStepUpOpen(true)}>重新验证管理员身份</Button>
                  </div>
                ) : null}
              </div>
            ) : null}
            <div className="callout callout-warning" style={{ marginTop: 14 }}>
              <ShieldCheck size={15} />
              <div>正常到账余额原则上不退回。异常资金、预占释放与账务更正应使用对账或退款流程。</div>
            </div>
          </>
        )}
      </Modal>
      <StepUpDialog open={stepUpOpen} onClose={() => setStepUpOpen(false)} />
    </>
  )
}

function RefundDialog({
  order,
  onClose,
  onSaved,
}: {
  order: Order | null
  onClose: () => void
  onSaved: () => void
}) {
  const { notify } = useToast()
  const [kind, setKind] = useState('abnormal_payment')
  const [amount, setAmount] = useState('')
  const [reason, setReason] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<unknown | null>(null)
  const [result, setResult] = useState<Refund | null>(null)
  const [stepUpOpen, setStepUpOpen] = useState(false)
  const idempotency = useIdempotencyKey()

  useEffect(() => {
    if (order) {
      idempotency.reset()
      setKind('abnormal_payment')
      setAmount(formatMoneyMinor(order.amount_minor).replace('¥', '').replaceAll(',', ''))
      setReason('')
      setError(null)
      setResult(null)
    }
  }, [order])

  const submit = async () => {
    if (!order) return
    const amountMinor = parseYuanToMinor(amount)
    if (amountMinor === null || amountMinor === '0' || amountMinor.startsWith('-')) {
      setError(new Error('请输入大于零的退款金额。'))
      return
    }
    if (reason.trim().length < 5) {
      setError(new Error('退款理由至少需要 5 个字符。'))
      return
    }
    setSaving(true)
    setError(null)
    try {
      const payload = {
        orderId: order.id,
        kind,
        amountMinor,
        reason: reason.trim(),
      }
      const refund = await createRefund(payload, idempotency.keyFor(payload))
      setResult(refund)
      notify('退款请求已提交。', 'success')
      onSaved()
    } catch (reasonValue) {
      setError(reasonValue)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      open={Boolean(order)}
      onClose={onClose}
      title="发起退款"
      description={order ? `订单 ${order.id}` : undefined}
      actions={
        result ? (
          <Button variant="primary" onClick={onClose}>关闭</Button>
        ) : (
          <>
            <Button onClick={onClose} disabled={saving}>取消</Button>
            <Button variant="danger" onClick={() => void submit()} loading={saving}>提交退款</Button>
          </>
        )
      }
    >
      {result ? (
        <div className="callout callout-success">
          <CheckCircle2 size={16} />
          <div>退款记录已创建：{result.status ?? '待处理'}{result.id ? ` · ${result.id}` : ''}。</div>
        </div>
      ) : (
        <>
          <div className="callout callout-warning">
            <CircleAlert size={15} />
            <div>退款按原订单实际可退金额与审批条件处理。正常到账余额原则上不退回，已消费余额不支持用户主动退回。</div>
          </div>
          <div className="field-row" style={{ marginTop: 16 }}>
            <Field label="退款类型" required>
              <Select label="退款类型" value={kind} onChange={setKind} options={refundKindOptions} />
            </Field>
            <Field label="退款金额（元）" required>
              <input value={amount} onChange={event => setAmount(event.target.value)} inputMode="decimal" />
            </Field>
          </div>
          <Field label="退款理由" required hint="至少 5 个字符。">
            <textarea rows={3} value={reason} onChange={event => setReason(event.target.value)} maxLength={500} />
          </Field>
          {error ? (
            <div style={{ marginTop: 12 }}>
              <ErrorState error={error} compact />
              {describeError(error).status === 403 ? (
                <div style={{ marginTop: 8 }}><Button size="sm" onClick={() => setStepUpOpen(true)}>重新验证管理员身份</Button></div>
              ) : null}
            </div>
          ) : null}
        </>
      )}
      <StepUpDialog open={stepUpOpen} onClose={() => setStepUpOpen(false)} />
    </Modal>
  )
}
