import { useEffect, useMemo, useState } from 'react'
import {
  ChevronRight,
  CircleAlert,
  Filter,
  Pause,
  Play,
  Search,
  ShieldCheck,
  SlidersHorizontal,
  UnlockKeyhole,
  Users,
  Wallet,
  X,
} from 'lucide-react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { hasPermission } from '../auth/permissions'
import { getAdminUserWallet, getUser, listUsers, runAccountOperation } from '../api/endpoints'
import type { BulkOperationResult, UserAccount, UserDetail } from '../api/types'
import {
  Avatar,
  Badge,
  Button,
  Card,
  Modal,
  CheckBox,
  EmptyState,
  ErrorState,
  Field,
  IconButton,
  LoadingBlock,
  PageHeader,
  Pagination,
  RefreshError,
  SearchInput,
  Select,
  useToast,
} from '../components'
import { describeError } from '../components/ui'
import { useAsync, useCursorStack, useDebouncedValue } from '../lib/hooks'
import { useIdempotencyKey } from '../lib/idempotency'
import { formatDateTime, formatMoneyMinor, formatNumber, formatPriceMicros } from '../lib/format'

const statusOptions = [
  { value: 'all', label: '全部状态' },
  { value: 'valid', label: '身份已绑定' },
  { value: 'expired', label: '身份未绑定' },
  { value: 'limited', label: '已限制' },
]

const operationCopy = {
  pause: {
    title: '暂停所选账号的后续安排',
    description: '暂停后续安排领取，不会把已经运行的尝试立即标成暂停。',
    button: '确认暂停',
    tone: 'secondary' as const,
  },
  resume: {
    title: '恢复所选账号的后续安排',
    description: '恢复后续安排领取；账号仍需通过平台验证后才能继续。',
    button: '确认恢复',
    tone: 'secondary' as const,
  },
  limit: {
    title: '限制所选账号',
    description: '限制后停止新领取并向活跃尝试发送停止请求。解除限制后仍需重新验证平台会话。',
    button: '确认限制',
    tone: 'danger' as const,
  },
  release: {
    title: '解除所选账号的限制',
    description: '解除限制不会恢复平台会话，账号仍需由持有人重新验证后才能继续。',
    button: '确认解除',
    tone: 'secondary' as const,
  },
}

type OperationKind = keyof typeof operationCopy

function accountStatusBadge(user: UserAccount) {
  if (user.status === 'valid') return <Badge tone="green" dot>{user.status_label ?? '身份已绑定'}</Badge>
  if (user.status === 'expired') return <Badge tone="amber" dot>{user.status_label ?? '身份未绑定'}</Badge>
  if (user.status === 'limited') return <Badge tone="red" dot>{user.status_label ?? '已限制'}</Badge>
  return <Badge>{user.status_label ?? '状态待确认'}</Badge>
}

export function UsersPage() {
  const location = useLocation()
  const navigate = useNavigate()
  const { notify } = useToast()
  const { me } = useAuth()
  const canOperate = hasPermission(me, 'learning.accounts.operate')
  const canReadBilling = hasPermission(me, 'billing.read')
  const canAdjustWallet = hasPermission(me, 'wallet.adjust')
  const initial = new URLSearchParams(location.search)
  const [query, setQuery] = useState(initial.get('search') ?? '')
  const debouncedQuery = useDebouncedValue(query, 300)
  const [status, setStatus] = useState(initial.get('state') ?? 'all')
  const [selected, setSelected] = useState<string[]>([])
  const [detailId, setDetailId] = useState<string | null>(null)
  const cursor = useCursorStack(debouncedQuery)
  const users = useAsync(
    () =>
      listUsers({
        search: debouncedQuery || undefined,
        cursor: cursor.cursor,
        limit: 25,
      }).then(response => response.data),
    [debouncedQuery, cursor.cursor],
  )

  useEffect(() => {
    setSelected([])
  }, [debouncedQuery, status, cursor.cursor])

  const pageUsers = users.data?.items ?? []
  const visible = status === 'all' ? pageUsers : pageUsers.filter(user => user.status === status)
  const allVisibleSelected = visible.length > 0 && visible.every(user => selected.includes(user.id))
  const selectedSet = useMemo(() => new Set(selected), [selected])

  const toggle = (id: string) => {
    setSelected(current => (current.includes(id) ? current.filter(item => item !== id) : [...current, id]))
  }

  const toggleAllVisible = () => {
    if (allVisibleSelected) {
      setSelected(current => current.filter(id => !visible.some(user => user.id === id)))
    } else {
      setSelected(current => [...new Set([...current, ...visible.map(user => user.id)])])
    }
  }

  const clearFilters = () => {
    setQuery('')
    setStatus('all')
  }

  const [operation, setOperation] = useState<{ kind: OperationKind; userIds: string[] } | null>(null)

  const openOperation = (kind: OperationKind, userIds?: string[]) => {
    if (!canOperate) {
      notify('当前管理员角色无权执行账号操作。', 'warning')
      return
    }
    const targets = userIds?.length ? userIds : selected
    if (!targets.length) {
      notify('请先选择账号。', 'warning')
      return
    }
    setOperation({ kind, userIds: targets })
  }

  return (
    <>
      <PageHeader
        title="用户与账号"

        actions={
          <Button onClick={() => users.reload()} loading={users.loading}>
            <Search size={14} /> 重新查询
          </Button>
        }
      />

      <Card>
        <div className="table-toolbar">
          <div className="table-toolbar-group">
            <SearchInput
              value={query}
              onChange={value => setQuery(value)}
              placeholder="搜索用户、用户编号或学校…"
            />
            <Select
              label="账号状态筛选"
              value={status}
              onChange={value => setStatus(value)}
              options={statusOptions}
            />
          </div>
          <div className="table-toolbar-group">
            {status !== 'all' || debouncedQuery ? (
              <Button size="sm" variant="ghost" onClick={clearFilters}>
                <Filter size={14} /> 重置筛选
              </Button>
            ) : null}
          </div>
        </div>

        {canOperate && selected.length ? (
          <div className="bulk-bar">
            <span>
              <Users size={14} /> 已选 <b>{selected.length}</b> 个账号
              {selected.some(id => !visible.some(user => user.id === id)) ? (
                <span className="tag">含跨页选择</span>
              ) : null}
            </span>
            <div className="bulk-bar-actions">
              <Button size="sm" variant="ghost" onClick={() => openOperation('pause')}>
                <Pause size={14} /> 暂停安排
              </Button>
              <Button size="sm" variant="ghost" onClick={() => openOperation('resume')}>
                <Play size={14} /> 恢复安排
              </Button>
              <Button size="sm" variant="ghost" onClick={() => openOperation('limit')}>
                <ShieldCheck size={14} /> 限制账号
              </Button>
              <Button size="sm" variant="ghost" onClick={() => openOperation('release')}>
                <UnlockKeyhole size={14} /> 解除限制
              </Button>
              <IconButton label="清除选择" onClick={() => setSelected([])}>
                <X size={14} />
              </IconButton>
            </div>
          </div>
        ) : null}

        {users.error && users.data ? <RefreshError error={users.error} onRetry={users.reload} /> : null}
        {users.loading && !users.data ? (
          <LoadingBlock rows={4} />
        ) : users.error && !users.data ? (
          <div className="u-padding-18px" >
            <ErrorState error={users.error} onRetry={users.reload} />
          </div>
        ) : visible.length ? (
          <>
            <div className="table-scroll">
              <table className="data-table users-table">
                <thead>
                  <tr>
                    {canOperate ? (
                      <th className="u-width-42px" >
                        <CheckBox
                          checked={allVisibleSelected}
                          onChange={toggleAllVisible}
                          label="选择当前页全部账号"
                        />
                      </th>
                    ) : null}
                    <th>用户 / 账号</th>
                    <th>账号状态</th>
                    <th>课程</th>
                    <th>安排</th>
                    <th>未完成任务</th>
                    <th>最近同步</th>
                    <th aria-label="操作" />
                  </tr>
                </thead>
                <tbody>
                  {visible.map((user, index) => (
                    <tr key={user.id} className={selectedSet.has(user.id) ? 'is-selected' : ''}>
                      {canOperate ? (
                        <td className="cell-select">
                          <CheckBox
                            checked={selectedSet.has(user.id)}
                            onChange={() => toggle(user.id)}
                            label={`选择 ${user.display_name}`}
                          />
                        </td>
                      ) : null}
                      <td className="cell-title">
                        <button className="cell-button" title={user.display_name} onClick={() => setDetailId(user.id)}>
                          <Avatar name={user.display_name} index={index} small />
                          <span className="cell-stack">
                            <span className="cell-primary">{user.display_name}</span>
                            <span className="cell-sub">
                              {user.id}
                              {user.school ? ` · ${user.school}` : ''}
                            </span>
                          </span>
                        </button>
                      </td>
                      <td data-label="账号状态">
                        {accountStatusBadge(user)}
                        {user.status_detail ? <small className="cell-sub">{user.status_detail}</small> : null}
                      </td>
                      <td data-label="课程">{formatNumber(user.course_count)}</td>
                      <td data-label="安排">{formatNumber(user.plan_count)}</td>
                      <td data-label="未完成任务">{formatNumber(user.incomplete_task_count)}</td>
                      <td className="muted" data-label="最近同步">{formatDateTime(user.last_synced_at)}</td>
                      <td className="cell-actions" data-label="操作">
                        <IconButton label={`查看 ${user.display_name} 详情`} onClick={() => setDetailId(user.id)}>
                          <ChevronRight size={16} />
                        </IconButton>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <Pagination
              hasPrevious={cursor.hasPrevious}
              hasNext={Boolean(users.data?.next_cursor)}
              onPrevious={cursor.goPrevious}
              onNext={() => cursor.goNext(users.data?.next_cursor ?? null)}
              page={cursor.page}
              info={`当前页 ${visible.length} 个账号${users.data?.next_cursor ? '，还有更多记录' : '，已到末页'}`}
              disabled={users.loading}
            />
          </>
        ) : (
          <EmptyState
            title={debouncedQuery || status !== 'all' ? '没有符合条件的账号' : '暂无账号记录'}
            description={
              debouncedQuery || status !== 'all'
                ? '调整搜索词或状态筛选后再试。'
                : '暂无账号记录。'
            }
            action={<Button onClick={clearFilters}>清除筛选</Button>}
          />
        )}
      </Card>



      <UserDetailDrawer
        id={detailId}
        onClose={() => setDetailId(null)}
        onOperation={kind => {
          if (!detailId) return
          openOperation(kind, [detailId])
        }}
        onOpenTasks={userId => {
          setDetailId(null)
          navigate(`/tasks?owner=${encodeURIComponent(userId)}`)
        }}
        onOpenWallet={(userId, walletId, adjust) => {
          setDetailId(null)
          const query = new URLSearchParams({ wallet: walletId, user: userId })
          if (adjust) query.set('action', 'adjust')
          navigate(`/finance?${query.toString()}`)
        }}
        canOperate={canOperate}
        canReadBilling={canReadBilling}
        canAdjustWallet={canAdjustWallet}
      />

      <AccountOperationDialog
        operation={operation}
        onClose={() => setOperation(null)}
        onCompleted={() => {
          setSelected([])
          setDetailId(null)
          users.reload()
        }}
      />
    </>
  )
}

function UserDetailDrawer({
  id,
  onClose,
  onOperation,
  onOpenTasks,
  onOpenWallet,
  canOperate,
  canReadBilling,
  canAdjustWallet,
}: {
  id: string | null
  onClose: () => void
  onOperation: (kind: OperationKind) => void
  onOpenTasks: (userId: string) => void
  onOpenWallet: (userId: string, walletId: string, adjust: boolean) => void
  canOperate: boolean
  canReadBilling: boolean
  canAdjustWallet: boolean
}) {
  const detail = useAsync(
    () => (id ? getUser(id).then(response => response.data) : Promise.resolve(null)),
    [id],
    { enabled: Boolean(id), resetOnDepsChange: true },
  )

  return (
    <Modal
      open={Boolean(id)}
      onClose={onClose}
      title="用户与账号详情"
      description={id ? `用户 ${id}` : undefined}
      drawer
    >
      {detail.loading ? <LoadingBlock rows={5} /> : null}
      {detail.error ? <ErrorState error={detail.error} onRetry={detail.reload} /> : null}
      {detail.data ? <UserDetailContent user={detail.data} onOperation={onOperation} onOpenTasks={onOpenTasks} onOpenWallet={onOpenWallet} canOperate={canOperate} canReadBilling={canReadBilling} canAdjustWallet={canAdjustWallet} /> : null}
    </Modal>
  )
}

function UserDetailContent({
  user,
  onOperation,
  onOpenTasks,
  onOpenWallet,
  canOperate,
  canReadBilling,
  canAdjustWallet,
}: {
  user: UserDetail
  onOperation: (kind: OperationKind) => void
  onOpenTasks: (userId: string) => void
  onOpenWallet: (userId: string, walletId: string, adjust: boolean) => void
  canOperate: boolean
  canReadBilling: boolean
  canAdjustWallet: boolean
}) {
  const wallet = useAsync(
    () => (canReadBilling ? getAdminUserWallet(user.id).then(response => response.data) : Promise.resolve(null)),
    [user.id, canReadBilling],
    { enabled: canReadBilling, resetOnDepsChange: true },
  )
  const walletSnapshot = wallet.data?.wallet ?? null
  return (
    <>
      <div className="account-detail-header">
        <Avatar name={user.display_name} />
        <div className="u-flex-1 u-min-width-0" >
          <h2>{user.display_name}</h2>
          <p>
            {user.id}
            {user.school ? ` · ${user.school}` : ''}
          </p>
        </div>
        {accountStatusBadge(user)}
      </div>

      <div className="info-grid">
        <div className="info-tile">
          <span>课程数量</span>
          <b>{formatNumber(user.course_count)}</b>
        </div>
        <div className="info-tile">
          <span>安排数量</span>
          <b>{formatNumber(user.plan_count)}</b>
        </div>
        <div className="info-tile">
          <span>未完成任务</span>
          <b>{formatNumber(user.incomplete_task_count)}</b>
        </div>
        <div className="info-tile">
          <span>任务总数</span>
          <b>{formatNumber(user.task_count)}</b>
        </div>
      </div>

      <div className="detail-section">
        <div className="detail-section-title"><ShieldCheck size={14} /> 身份与会话</div>
        <dl className="detail-list">
          <div><dt>学校</dt><dd>{user.school ?? '—'}</dd></div>
          <div><dt>身份命名空间</dt><dd className="mono">{user.identity_namespace ?? '—'}</dd></div>
          <div><dt>身份绑定</dt><dd>{user.identity_bound === null ? '—' : user.identity_bound ? '已绑定' : '未绑定'}</dd></div>
          <div><dt>账号状态</dt><dd>{user.status_label ?? '—'}</dd></div>
          <div><dt>受限原因</dt><dd>{user.status_detail ?? '无'}</dd></div>
          <div><dt>受限截止</dt><dd>{formatDateTime(user.blocked_until)}</dd></div>
          <div><dt>最近同步</dt><dd>{formatDateTime(user.last_synced_at)}</dd></div>
          <div><dt>账号创建时间</dt><dd>{formatDateTime(user.created_at)}</dd></div>
        </dl>
      </div>

      <div className="detail-section">
        <div className="detail-section-title"><CircleAlert size={14} /> 钱包与余额</div>
        {!canReadBilling ? (
          <div className="callout">
            <ShieldCheck size={16} />
            <div>当前角色没有财务读取权限，无法读取钱包与流水。</div>
          </div>
        ) : wallet.loading && !wallet.data ? (
          <LoadingBlock rows={2} />
        ) : wallet.error ? (
          <ErrorState error={wallet.error} onRetry={wallet.reload} compact />
        ) : walletSnapshot ? (
          <>
            <dl className="detail-list">
              <div><dt>钱包编号</dt><dd className="mono">{walletSnapshot.wallet_id}</dd></div>
              <div><dt>可用余额</dt><dd>{formatMoneyMinor(walletSnapshot.available_minor)}</dd></div>
              <div><dt>预占金额</dt><dd>{formatMoneyMinor(walletSnapshot.reserved_minor)}</dd></div>
              <div><dt>题库计费尾差</dt><dd>{formatPriceMicros(walletSnapshot.tiku_accrued_micros)}</dd></div>
              <div><dt>钱包状态</dt><dd>{walletSnapshot.frozen ? `已冻结${walletSnapshot.frozen_reason ? ` · ${walletSnapshot.frozen_reason}` : ''}` : '正常'}</dd></div>
              <div><dt>钱包版本</dt><dd>{walletSnapshot.version === null ? '—' : `v${walletSnapshot.version}`}</dd></div>
              <div><dt>备注</dt><dd>{user.notes ?? '—'}</dd></div>
            </dl>
            {wallet.data?.wallet_created ? (
              <p className="form-hint u-margin-top-8px" >该用户此前没有钱包，本次读取时已创建空钱包。</p>
            ) : null}
            <div className="detail-actions">
              <Button onClick={() => onOpenWallet(user.id, walletSnapshot.wallet_id, false)}>
                <Wallet size={14} /> 查看钱包流水
              </Button>
              {canAdjustWallet ? (
                <Button onClick={() => onOpenWallet(user.id, walletSnapshot.wallet_id, true)}>
                  <SlidersHorizontal size={14} /> 调整余额
                </Button>
              ) : null}
            </div>
          </>
        ) : null}
        <div className="callout u-margin-top-12px" >
          用户是账务归属主体。余额与流水可在钱包页查看；账号详情不显示凭据或会话令牌。
        </div>
      </div>

      <div className="detail-section">
        <div className="detail-section-title"><Users size={14} /> 最近任务</div>
        {user.recent_tasks.length ? (
          <dl className="detail-list">
            {user.recent_tasks.slice(0, 5).map(task => (
              <div key={task.id}>
                <dt>{task.state_label ?? task.state}</dt>
                <dd>
                  <b>{task.title}</b>
                  <small className="cell-sub">{task.id} · {task.course_title ?? '—'}</small>
                </dd>
              </div>
            ))}
          </dl>
        ) : (
          <p className="form-hint">暂无最近任务。</p>
        )}
      </div>

      <div className="detail-actions">
        <Button onClick={() => onOpenTasks(user.id)}><Search size={14} /> 查看关联任务</Button>
        {canOperate ? (
          <>
            <Button onClick={() => onOperation('pause')}><Pause size={14} /> 暂停安排</Button>
            <Button onClick={() => onOperation('resume')}><Play size={14} /> 恢复安排</Button>
            <Button
              variant={user.status === 'limited' ? 'secondary' : 'danger'}
              onClick={() => onOperation(user.status === 'limited' ? 'release' : 'limit')}
            >
              <UnlockKeyhole size={14} /> {user.status === 'limited' ? '解除限制' : '限制账号'}
            </Button>
          </>
        ) : null}
      </div>
      <p className="form-hint u-margin-top-10px" >
        详情不会显示密码、Cookie 或会话令牌。账号操作会记录原因并保留审计。
      </p>
    </>
  )
}

function AccountOperationDialog({
  operation,
  onClose,
  onCompleted,
}: {
  operation: { kind: OperationKind; userIds: string[] } | null
  onClose: () => void
  onCompleted: () => void
}) {
  const { notify } = useToast()
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<unknown | null>(null)
  const [result, setResult] = useState<BulkOperationResult | null>(null)
  const idempotency = useIdempotencyKey()

  useEffect(() => {
    if (operation) {
      idempotency.reset()
      setReason('')
      setError(null)
      setResult(null)
    }
  }, [operation])

  if (!operation) return null
  const copy = operationCopy[operation.kind]
  const details = error ? describeError(error) : null

  const submit = async () => {
    if (!reason.trim()) {
      setError(new Error('请填写操作原因，便于后续审计。'))
      return
    }
    setSubmitting(true)
    setError(null)
    try {
      const payload = {
        operation: operation.kind,
        userIds: operation.userIds,
        reason: reason.trim(),
      }
      const response = await runAccountOperation(payload, idempotency.keyFor(payload))
      setResult(response)
      onCompleted()
      if (response.updated === null) {
        notify('操作已受理，未返回更新数量。', 'success')
      } else {
        notify(
          `已更新 ${response.updated} 个账号${response.skipped ? `，跳过 ${response.skipped} 个` : ''}。`,
          response.skipped ? 'warning' : 'success',
        )
      }
    } catch (reasonValue) {
      setError(reasonValue)
      const description = describeError(reasonValue)
      notify(description.message, 'error')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      title={copy.title}
      description={`操作范围：${operation.userIds.length} 个显式选择的账号。`}
      actions={
        result ? (
          <Button variant="primary" onClick={onClose}>完成</Button>
        ) : (
          <>
            <Button onClick={onClose} disabled={submitting}>取消</Button>
            <Button variant={copy.tone} onClick={() => void submit()} loading={submitting}>
              {copy.button}
            </Button>
          </>
        )
      }
    >
      {result ? (
        <>
          <div className="callout callout-success">
            <ShieldCheck size={16} />
            <div>
              {result.updated === null
                ? '操作已受理，但没有返回更新数量。'
                : `已更新 ${result.updated} 个账号${result.skipped ? `，跳过 ${result.skipped} 个` : '。'}`}
            </div>
          </div>
          {result.items.length ? (
            <div className="detail-section">
              <div className="detail-section-title">逐项结果</div>
              <dl className="detail-list">
                {result.items.map(item => (
                  <div key={`${item.id}-${item.status}`}>
                    <dt className="mono">{item.id || '—'}</dt>
                    <dd>{item.status}{item.message ? ` · ${item.message}` : ''}</dd>
                  </div>
                ))}
              </dl>
            </div>
          ) : null}
        </>
      ) : (
        <>
          <div className="callout callout-warning">
            <CircleAlert size={16} />
            <div>{copy.description}</div>
          </div>
          <div className="u-margin-top-18px" >
            <Field
              label="操作原因"
              required
              hint="原因会写入审计记录。请说明依据，不要填写密码、Cookie 或会话令牌。"
            >
              <textarea
                rows={4}
                value={reason}
                onChange={event => {
                  setReason(event.target.value)
                  if (error) setError(null)
                }}
                placeholder="填写本次操作原因…"
                maxLength={500}
              />
            </Field>
          </div>
          {details ? <ErrorState error={error} compact /> : null}
          <p className="form-hint u-margin-top-12px" >
            选择范围不会随分页改变。每个账号会按当前状态、权限与并发限制逐项处理。
          </p>
        </>
      )}
    </Modal>
  )
}
