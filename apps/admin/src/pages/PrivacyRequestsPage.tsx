import { useEffect, useState } from 'react'
import {
  CheckCircle2,
  CircleAlert,
  Clock3,
  FileDown,
  LockKeyhole,
  RefreshCw,
  ShieldCheck,
  Trash2,
  X,
} from 'lucide-react'
import { listDataRequests, reviewDataRequest } from '../api/endpoints'
import { ApiError } from '../api/client'
import type { DataRequestRecord, PrivacyBlockers, PrivacyReviewAction, PrivacyReviewResult } from '../api/types'
import {
  Badge,
  Button,
  Card,
  EmptyState,
  ErrorState,
  Field,
  LoadingBlock,
  Modal,
  PageHeader,
  Pagination,
  RefreshError,
  StepUpDialog,
  useToast,
} from '../components'
import { formatDateTime, formatNumber } from '../lib/format'
import { useAsync, useCursorStack } from '../lib/hooks'

const kindLabels: Record<string, string> = {
  deletion: '删除申请',
  export: '导出申请',
}

const statusLabels: Record<string, string> = {
  pending: '待处理',
  processing: '处理中',
  ready: '待下载',
  needs_review: '待复核',
  deferred: '已延后',
  rejected: '已驳回',
  completed: '已完成',
  failed: '失败',
  expired: '已过期',
  blocked: '已阻断',
  unknown: '状态待确认',
}

const statusTone: Record<string, 'green' | 'amber' | 'red' | 'neutral' | 'blue' | 'violet'> = {
  pending: 'blue',
  processing: 'blue',
  ready: 'green',
  needs_review: 'amber',
  deferred: 'violet',
  rejected: 'neutral',
  completed: 'green',
  failed: 'red',
  expired: 'neutral',
  blocked: 'red',
}

const actionLabels: Record<PrivacyReviewAction, string> = {
  defer: '延后处理',
  reject: '驳回申请',
  complete: '完成删除',
}

const blockerLabels: Record<string, string> = {
  wallet_balances: '钱包余额未清零',
  open_holds: '未结费用预占',
  unsettled_orders: '未结订单',
  open_refunds: '未结退款',
  open_reconciliations: '未结对账',
  unsettled_usage: '未结用量',
  question_usage: '未结题库用量',
  pending_cache_reports: '待处理缓存报告',
  pending_payment_inbox: '待处理支付通知',
  active_attempts: '进行中的执行尝试',
  active_tasks: '运行中任务',
  billing_blocked_tasks: '计费阻断任务',
  active_course_syncs: '进行中课程同步',
}

function isStepUpRequired(error: unknown): boolean {
  return error instanceof ApiError && error.status === 403 && error.code === 'step_up_required'
}

function blockerItems(blockers: PrivacyBlockers | null): Array<{ key: string; label: string; value: string }> {
  if (!blockers) return []
  return Object.entries(blockers)
    .filter(([, value]) => value === true || (typeof value === 'number' && value > 0))
    .map(([key, value]) => ({
      key,
      label: blockerLabels[key] ?? key,
      value: typeof value === 'boolean' ? '存在' : `${formatNumber(value)} 项`,
    }))
}

export function PrivacyRequestsPage() {
  const { notify } = useToast()
  const cursor = useCursorStack('')
  const requests = useAsync(
    () => listDataRequests({ cursor: cursor.cursor, limit: 25 }).then(response => response.data),
    [cursor.cursor],
  )
  const [reviewing, setReviewing] = useState<DataRequestRecord | null>(null)

  return (
    <>
      <PageHeader
        title="隐私与数据请求"
        description="复核删除申请，并查看导出申请状态。完成删除会关闭账号并清除会话与身份密文；账务与审计按保留策略保留。"
        actions={
          <Button onClick={() => requests.reload()} loading={requests.loading}>
            <RefreshCw size={14} /> 刷新
          </Button>
        }
      />

      <div className="system-strip">
        <span>
          <ShieldCheck size={13} />
          <b>删除复核需要再次验证管理员身份</b>
          <span className="strip-secondary">阻断只显示计数，不暴露金额、题目或身份内容</span>
        </span>
      </div>

      <Card>
        {requests.loading && !requests.data ? <LoadingBlock rows={5} /> : null}
        {requests.error && !requests.data ? (
          <div style={{ padding: 18 }}><ErrorState error={requests.error} onRetry={requests.reload} /></div>
        ) : null}
        {requests.error && requests.data ? <RefreshError error={requests.error} onRetry={requests.reload} /> : null}
        {requests.data?.items.length ? (
          <>
            <div className="table-scroll">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>申请</th>
                    <th>类型</th>
                    <th>用户</th>
                    <th>状态</th>
                    <th>复核信息</th>
                    <th>创建 / 更新</th>
                    <th>有效期</th>
                    <th aria-label="操作" />
                  </tr>
                </thead>
                <tbody>
                  {requests.data.items.map(item => {
                    const actionable = item.kind === 'deletion' && (item.status === 'needs_review' || item.status === 'deferred')
                    return (
                      <tr key={item.id} className={item.error_code === 'closure_blocked' ? 'is-alert' : ''}>
                        <td>
                          <span className="cell-stack">
                            <span className="cell-primary mono">{item.id}</span>
                            {item.error_code ? <span className="cell-sub">阻断：{item.error_code}</span> : null}
                          </span>
                        </td>
                        <td>
                          <Badge tone={item.kind === 'deletion' ? 'violet' : 'blue'}>
                            {item.kind === 'deletion' ? <Trash2 size={10} /> : <FileDown size={10} />}
                            {kindLabels[item.kind] ?? item.kind}
                          </Badge>
                        </td>
                        <td className="mono">{item.user_id ?? '未提供'}</td>
                        <td><Badge tone={statusTone[item.status] ?? 'neutral'} dot>{statusLabels[item.status] ?? item.status}</Badge></td>
                        <td className="muted" style={{ whiteSpace: 'normal', maxWidth: 300 }}>
                          {item.review_reason ?? '未提供'}
                        </td>
                        <td className="muted">
                          <div>{formatDateTime(item.created_at)}</div>
                          <div>{formatDateTime(item.updated_at)}</div>
                        </td>
                        <td className="muted">{formatDateTime(item.expires_at)}</td>
                        <td>
                          {actionable ? (
                            <Button size="sm" variant={item.status === 'deferred' ? 'secondary' : 'ghost'} onClick={() => setReviewing(item)}>
                              <FileDown size={12} /> {item.status === 'deferred' ? '继续复核' : '复核'}
                            </Button>
                          ) : null}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
            <p className="mobile-scroll-hint">左右滑动查看完整表格</p>
            <Pagination
              hasPrevious={cursor.hasPrevious}
              hasNext={Boolean(requests.data.next_cursor)}
              onPrevious={cursor.goPrevious}
              onNext={() => cursor.goNext(requests.data?.next_cursor ?? null)}
              page={cursor.page}
              info={`当前页 ${requests.data.items.length} 条申请${requests.data.next_cursor ? '，还有更多记录' : '，已到末页'}`}
              disabled={requests.loading}
            />
          </>
        ) : !requests.loading && !requests.error ? (
          <EmptyState title="暂无隐私请求" description="用户申请导出或删除数据后会显示在这里。" />
        ) : null}
      </Card>

      <div className="card-footer-link" style={{ border: 0, paddingLeft: 2 }}>
        <LockKeyhole size={12} /> 完成删除后用户不能登录；旧会话不会恢复。驳回或延后不会自动恢复旧会话、安排或任务执行。
      </div>

      <ReviewDialog
        request={reviewing}
        onClose={() => setReviewing(null)}
        onSaved={() => {
          requests.reload()
          notify('隐私申请复核已保存。', 'success')
        }}
      />
    </>
  )
}

function ReviewDialog({
  request,
  onClose,
  onSaved,
}: {
  request: DataRequestRecord | null
  onClose: () => void
  onSaved: () => void
}) {
  const [action, setAction] = useState<PrivacyReviewAction>('defer')
  const [reason, setReason] = useState('')
  const [acknowledged, setAcknowledged] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<unknown | null>(null)
  const [result, setResult] = useState<PrivacyReviewResult | null>(null)
  const [stepUpOpen, setStepUpOpen] = useState(false)

  useEffect(() => {
    if (request) {
      setAction('defer')
      setReason('')
      setAcknowledged(false)
      setError(null)
      setResult(null)
    }
  }, [request])

  if (!request) return null

  const blockers = error instanceof ApiError ? blockerItems(error.blockers) : []
  const errorMessage = error instanceof ApiError ? error.message : error instanceof Error ? error.message : null

  const submit = async () => {
    if (reason.trim().length < 4) {
      setError(new Error('复核理由至少需要 4 个字符。'))
      return
    }
    if (action === 'complete' && !acknowledged) {
      setError(new Error('完成删除前请确认账号关闭与会话清除的影响。'))
      return
    }
    setSaving(true)
    setError(null)
    try {
      const response = await reviewDataRequest(request.id, { action, reason: reason.trim() })
      setResult(response)
      onSaved()
    } catch (reasonValue) {
      setError(reasonValue)
      if (isStepUpRequired(reasonValue)) setStepUpOpen(true)
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <Modal
        open
        onClose={onClose}
        title="复核隐私申请"
        description={`${kindLabels[request.kind] ?? request.kind} · ${request.id}`}
        wide
        actions={
          result ? (
            <Button variant="primary" onClick={onClose}>关闭</Button>
          ) : (
            <>
              <Button onClick={onClose} disabled={saving}>取消</Button>
              <Button
                variant={action === 'complete' ? 'danger' : action === 'reject' ? 'danger' : 'primary'}
                onClick={() => void submit()}
                loading={saving}
              >
                {actionLabels[action]}
              </Button>
            </>
          )
        }
      >
        {result ? (
          <div className={`callout ${result.status === 'completed' ? 'callout-success' : 'callout-warning'}`}>
            {result.closed ? <CheckCircle2 size={16} /> : <CircleAlert size={16} />}
            <div>
              <b>{result.status === 'completed' ? '删除已完成' : result.status === 'rejected' ? '申请已驳回' : '申请已延后'}</b>
              <p>{result.message}</p>
            </div>
          </div>
        ) : (
          <>
            <div className="privacy-actions" role="group" aria-label="选择复核动作">
              {(['defer', 'reject', 'complete'] as PrivacyReviewAction[]).map(item => (
                <button
                  type="button"
                  key={item}
                  className={`privacy-action ${action === item ? 'selected' : ''} ${item === 'complete' ? 'danger' : ''}`}
                  onClick={() => { setAction(item); setError(null) }}
                  aria-pressed={action === item}
                >
                  {item === 'defer' ? <Clock3 size={15} /> : item === 'complete' ? <Trash2 size={15} /> : <X size={15} />}
                  <b>{actionLabels[item]}</b>
                  <span>
                    {item === 'defer'
                      ? '保留删除申请，账号仍不可用，稍后继续复核。'
                      : item === 'reject'
                        ? '申请改为已驳回；用户可重新验证，但不会自动恢复旧会话或任务。'
                        : '关闭账号并清除会话与身份密文，账务与审计保留。'}
                  </span>
                </button>
              ))}
            </div>

            <Field label="复核理由" required hint="4–2000 个字符，会写入审计记录。">
              <textarea rows={4} value={reason} onChange={event => { setReason(event.target.value); if (error) setError(null) }} maxLength={2000} />
            </Field>

            {action === 'complete' ? (
              <>
                <div className="callout callout-warning">
                  <CircleAlert size={15} />
                  <div>完成删除后不可恢复旧会话；钱包、订单、退款、对账与审计不会被删除。存在未结资金或运行活动时会被阻断。</div>
                </div>
                <label className="native-check" style={{ marginTop: 12 }}>
                  <input type="checkbox" checked={acknowledged} onChange={event => setAcknowledged(event.target.checked)} />
                  我已核对阻断情况并确认永久关闭该账号
                </label>
              </>
            ) : null}

            {error ? (
              <div style={{ marginTop: 14 }}>
                {blockers.length ? (
                  <div className="callout callout-danger" role="alert">
                    <CircleAlert size={15} />
                    <div>
                      <b>{errorMessage ?? '存在未结资金或运行中的活动，暂不能完成删除。'}</b>
                      <div className="blocker-list">
                        {blockers.map(item => (
                          <span key={item.key}><b>{item.label}</b> {item.value}</span>
                        ))}
                      </div>
                      <p className="form-hint">申请仍保留在待复核状态。请先处理对应资金或运行活动后再复核。</p>
                    </div>
                  </div>
                ) : (
                  <ErrorState error={error} compact />
                )}
                {isStepUpRequired(error) ? (
                  <div style={{ marginTop: 8 }}>
                    <Button size="sm" onClick={() => setStepUpOpen(true)}>重新验证管理员身份</Button>
                  </div>
                ) : null}
              </div>
            ) : null}
          </>
        )}
      </Modal>
      <StepUpDialog
        open={stepUpOpen}
        onClose={() => setStepUpOpen(false)}
        onSuccess={() => { setStepUpOpen(false); void submit() }}
      />
    </>
  )
}
