import { useEffect, useMemo, useState } from 'react'
import {
  Activity,
  ArrowRight,
  CheckCheck,
  ChevronRight,
  CircleAlert,
  Clock3,
  Film,
  Filter,
  ListTodo,
  Pause,
  Play,
  RefreshCw,
  RotateCcw,
  ShieldCheck,
  X,
} from 'lucide-react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { hasPermission } from '../auth/permissions'
import { getTask, listTasks, runTaskOperation } from '../api/endpoints'
import type { BulkOperationResult, TaskDetail, TaskEstimate, TaskMediaEvidence } from '../api/types'
import {
  Avatar,
  Badge,
  Button,
  Card,
  CheckBox,
  EmptyState,
  ErrorState,
  Field,
  IconButton,
  LoadingBlock,
  Modal,
  PageHeader,
  Pagination,
  Progress,
  RefreshError,
  Tabs,
  TaskStateBadge,
  useToast,
} from '../components'
import { describeError } from '../components/ui'
import { useAsync, useCursorStack } from '../lib/hooks'
import { useIdempotencyKey } from '../lib/idempotency'
import { formatDateTime, formatDuration, formatMoneyMinor, formatNumber, formatPercent } from '../lib/format'

const stateGroups: Array<{ value: string; label: string; states: string[] }> = [
  { value: 'all', label: '全部任务', states: [] },
  { value: 'active', label: '执行与验证', states: ['preparing', 'running', 'waiting_verification'] },
  { value: 'queued', label: '等待执行', states: ['pending'] },
  { value: 'attention', label: '需要关注', states: ['needs_attention', 'failed'] },
  { value: 'confirmed', label: '已确认', states: ['confirmed'] },
  { value: 'paused', label: '已暂停', states: ['paused'] },
  { value: 'cancelled', label: '已取消', states: ['cancelled'] },
]

type TaskOperation = 'pause' | 'resume' | 'cancel' | 'reschedule'

const operationCopy: Record<TaskOperation, { title: string; description: string; button: string; tone: 'secondary' | 'danger' | 'primary' }> = {
  pause: {
    title: '暂停所选任务',
    description: '暂停后任务会在安全边界停止。等待验证与平台确认的记录不会因此改写。',
    button: '确认暂停',
    tone: 'secondary',
  },
  resume: {
    title: '继续所选任务',
    description: '继续前会重新核对任务状态和账号会话；不满足条件的任务会跳过并说明原因。',
    button: '确认继续',
    tone: 'secondary',
  },
  cancel: {
    title: '取消所选任务',
    description: '平台已确认、已取消的任务会跳过。未消费的费用预占按账务规则释放或保留。',
    button: '确认取消',
    tone: 'danger',
  },
  reschedule: {
    title: '重新调度所选任务',
    description: '只对允许重新调度的任务生效，平台已确认任务不会回退重试。',
    button: '确认重新调度',
    tone: 'primary',
  },
}

const desiredStateLabels: Record<string, string> = {
  run: '运行',
  pause: '暂停',
  cancel: '取消',
}

const billingStateLabels: Record<string, string> = {
  disabled: '未启用扣费',
  unpriced: '未定价',
  no_charge: '未扣费',
  reserved: '费用已预占',
  settled: '已结算',
  released: '已释放预占',
  needs_review: '计费待复核',
  reserve_failed: '预占失败',
  billing_not_connected: '未连接账本',
  automatic_charging_disabled: '自动扣费关闭',
  billing_settlement_failed: '结算失败',
  billing_release_failed: '释放失败',
}

function platformLabel(status: string | null): { text: string; tone: 'green' | 'amber' | 'neutral' } {
  if (!status) return { text: '尚未确认', tone: 'neutral' }
  const normalized = status.toLowerCase()
  if (normalized === 'confirmed' || normalized === 'passed' || normalized === 'success') {
    return { text: '已确认', tone: 'green' }
  }
  if (normalized === 'unconfirmed' || normalized === 'pending' || normalized === 'checking') {
    return { text: '尚未确认', tone: 'neutral' }
  }
  if (normalized === 'failed' || normalized === 'rejected') return { text: '未通过', tone: 'amber' }
  return { text: status, tone: 'neutral' }
}

function operationAllowed(task: TaskDetail, operation: TaskOperation): boolean {
  const explicit =
    operation === 'pause'
      ? task.can_pause
      : operation === 'resume'
        ? task.can_resume
        : operation === 'cancel'
          ? task.can_cancel
          : task.can_reschedule
  if (explicit !== null) return explicit
  if (operation === 'cancel') return task.state !== 'confirmed' && task.state !== 'cancelled'
  if (operation === 'pause') return ['pending', 'preparing', 'running', 'waiting_verification'].includes(task.state)
  if (operation === 'resume') return task.state === 'paused'
  return ['pending', 'paused', 'failed', 'needs_attention', 'waiting_verification', 'cancelled'].includes(task.state)
}

export function TasksPage() {
  const location = useLocation()
  const navigate = useNavigate()
  const { notify } = useToast()
  const { me } = useAuth()
  const canOperate = hasPermission(me, 'learning.tasks.operate')
  const canReadBilling = hasPermission(me, 'billing.read')
  const initial = new URLSearchParams(location.search)
  const initialGroup = initial.get('group') === 'attention' || initial.get('status') === 'attention'
    ? 'attention'
    : initial.get('state') === 'confirmed'
      ? 'confirmed'
      : 'all'
  const [group, setGroup] = useState(initialGroup)
  const [owner, setOwner] = useState(initial.get('owner') ?? initial.get('user_id') ?? '')
  const [detailId, setDetailId] = useState<string | null>(initial.get('task'))
  const [selected, setSelected] = useState<string[]>([])
  const cursor = useCursorStack(`${group}|${owner}`)
  const activeGroup = stateGroups.find(item => item.value === group) ?? stateGroups[0]
  const tasks = useAsync(
    () =>
      listTasks({
        states: activeGroup.states,
        userId: owner || undefined,
        cursor: cursor.cursor,
        limit: 25,
      }).then(response => response.data),
    [group, owner, cursor.cursor],
  )

  useEffect(() => {
    setSelected([])
  }, [group, owner, cursor.cursor])

  const visible = tasks.data?.items ?? []
  const selectedSet = useMemo(() => new Set(selected), [selected])
  const allVisibleSelected = visible.length > 0 && visible.every(task => selected.includes(task.id))

  const toggle = (id: string) => {
    setSelected(current => (current.includes(id) ? current.filter(item => item !== id) : [...current, id]))
  }

  const toggleAllVisible = () => {
    if (allVisibleSelected) {
      setSelected(current => current.filter(id => !visible.some(task => task.id === id)))
    } else {
      setSelected(current => [...new Set([...current, ...visible.map(task => task.id)])])
    }
  }

  const [operation, setOperation] = useState<{ kind: TaskOperation; taskIds: string[] } | null>(null)
  const openOperation = (kind: TaskOperation, taskIds?: string[]) => {
    if (!canOperate) {
      notify('当前管理员角色无权执行任务控制。', 'warning')
      return
    }
    const targets = taskIds?.length ? taskIds : selected
    if (!targets.length) {
      notify('请先选择任务。', 'warning')
      return
    }
    setOperation({ kind, taskIds: targets })
  }

  const clearFilters = () => {
    setGroup('all')
    setOwner('')
  }

  return (
    <>
      <PageHeader
        eyebrow="任务运行"
        title="任务监控"
        description="执行观察、平台确认与媒体记录分别展示。观察进度不会自动变成平台完成。"
        actions={
          <>
            {owner ? (
              <Button size="sm" onClick={() => setOwner('')}>
                用户 {owner} <X size={12} />
              </Button>
            ) : null}
            <Button onClick={() => tasks.reload()} loading={tasks.loading}>
              <RefreshCw size={14} /> 刷新
            </Button>
            {canReadBilling ? (
              <Button onClick={() => navigate('/settings?section=billing')}>
                <Clock3 size={14} /> 计费设置
              </Button>
            ) : null}
          </>
        }
      />

      <Card>
        <Tabs value={group} onChange={value => setGroup(value)} items={stateGroups.map(item => ({ value: item.value, label: item.label }))} />
        <div className="table-toolbar">
          <div className="table-toolbar-group">
            <span className="form-hint">按任务状态筛选，按最近创建时间排序。</span>
          </div>
          <div className="table-toolbar-group">
            {owner ? <span className="tag">用户 {owner}</span> : null}
            <Button size="sm" variant="ghost" onClick={clearFilters}>
              <Filter size={13} /> 重置筛选
            </Button>
          </div>
        </div>

        {canOperate && selected.length ? (
          <div className="bulk-bar">
            <span>
              <ListTodo size={13} /> 已选 <b>{selected.length}</b> 项任务
            </span>
            <div className="bulk-bar-actions">
              <Button size="sm" variant="ghost" onClick={() => openOperation('pause')}>
                <Pause size={12} /> 暂停
              </Button>
              <Button size="sm" variant="ghost" onClick={() => openOperation('resume')}>
                <Play size={12} /> 继续
              </Button>
              <Button size="sm" variant="ghost" onClick={() => openOperation('reschedule')}>
                <RotateCcw size={12} /> 重新调度
              </Button>
              <Button size="sm" variant="ghost" onClick={() => openOperation('cancel')}>
                <X size={12} /> 取消任务
              </Button>
              <IconButton label="清除选择" onClick={() => setSelected([])}>
                <X size={14} />
              </IconButton>
            </div>
          </div>
        ) : null}

        {tasks.error && tasks.data ? <RefreshError error={tasks.error} onRetry={tasks.reload} /> : null}
        {tasks.loading && !tasks.data ? (
          <LoadingBlock rows={5} />
        ) : tasks.error && !tasks.data ? (
          <div style={{ padding: 18 }}>
            <ErrorState error={tasks.error} onRetry={tasks.reload} />
          </div>
        ) : visible.length ? (
          <>
            <div className="table-scroll">
              <table className="data-table">
                <thead>
                  <tr>
                    {canOperate ? (
                      <th style={{ width: 42 }}>
                        <CheckBox checked={allVisibleSelected} onChange={toggleAllVisible} label="选择当前页全部任务" />
                      </th>
                    ) : null}
                    <th>任务 / 课程</th>
                    <th>用户</th>
                    <th>当前状态</th>
                    <th>执行观察</th>
                    <th>平台确认</th>
                    <th>最近更新</th>
                    <th aria-label="操作" />
                  </tr>
                </thead>
                <tbody>
                  {visible.map((task, index) => {
                    const platform = platformLabel(task.platform_status)
                    return (
                      <tr
                        key={task.id}
                        className={`${selectedSet.has(task.id) ? 'is-selected' : ''} ${task.state === 'needs_attention' || task.state === 'failed' ? 'is-alert' : ''}`}
                      >
                        {canOperate ? (
                          <td>
                            <CheckBox
                              checked={selectedSet.has(task.id)}
                              onChange={() => toggle(task.id)}
                              label={`选择任务 ${task.title}`}
                            />
                          </td>
                        ) : null}
                        <td>
                          <button className="cell-button" onClick={() => setDetailId(task.id)} aria-label={`查看任务 ${task.title}`}>
                            <span className="cell-stack">
                              <span className="cell-sub mono">{task.id}</span>
                              <span className="cell-primary">{task.title}</span>
                              <span className="cell-sub">{task.course_title ?? '未提供课程'}</span>
                            </span>
                          </button>
                        </td>
                        <td>
                          <button
                            className="cell-button"
                            onClick={() => navigate(`/users?search=${encodeURIComponent(task.user_id ?? '')}`)}
                          >
                            <Avatar name={task.user_display_name ?? '?'} index={index} small />
                            <span>{task.user_display_name ?? task.user_id ?? '未提供'}</span>
                          </button>
                        </td>
                        <td>
                          <TaskStateBadge state={task.state} label={task.state_label ?? undefined} />
                          {task.last_error_code ? <small className="cell-sub">{task.last_error_code}</small> : null}
                        </td>
                        <td style={{ minWidth: 130 }}>
                          <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8, marginBottom: 6 }}>
                            <span>{formatPercent(task.progress_fraction)}</span>
                          </div>
                          <Progress
                            value={task.progress_fraction}
                            tone={task.state === 'confirmed' ? 'green' : task.state === 'needs_attention' ? 'amber' : undefined}
                          />
                        </td>
                        <td>
                          <Badge tone={platform.tone} dot={platform.tone === 'green'}>
                            {platform.tone === 'green' ? <CheckCheck size={11} /> : null}
                            {platform.text}
                          </Badge>
                        </td>
                        <td className="muted">{formatDateTime(task.updated_at)}</td>
                        <td>
                          <IconButton label={`查看任务 ${task.id}`} onClick={() => setDetailId(task.id)}>
                            <ChevronRight size={16} />
                          </IconButton>
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
              hasNext={Boolean(tasks.data?.next_cursor)}
              onPrevious={cursor.goPrevious}
              onNext={() => cursor.goNext(tasks.data?.next_cursor ?? null)}
              page={cursor.page}
              info={`当前页 ${visible.length} 项任务${tasks.data?.next_cursor ? '，还有更多记录' : '，已到末页'}`}
              disabled={tasks.loading}
            />
          </>
        ) : (
          <EmptyState
            title={group !== 'all' || owner ? '没有符合条件的任务' : '暂无任务记录'}
            description={
              group !== 'all' || owner
                ? '调整状态分组或清除用户筛选后再试。'
                : '暂无任务记录。'
            }
            action={<Button onClick={clearFilters}>清除筛选</Button>}
          />
        )}
      </Card>

      <div className="card-footer-link" style={{ border: 0, paddingLeft: 2 }}>
        <CircleAlert size={12} /> 验证码、账号失效、403 或 429 会停止相关操作并等待人工处理，不会无限重试。
        <Link to="/users?state=expired">查看身份未绑定账号 <ArrowRight size={12} /></Link>
      </div>

      <TaskDetailDrawer
        id={detailId}
        onClose={() => setDetailId(null)}
        onOperation={(kind, taskId) => openOperation(kind, [taskId])}
        canOperate={canOperate}
      />

      <TaskOperationDialog
        operation={operation}
        onClose={() => setOperation(null)}
        onCompleted={() => {
          setSelected([])
          setDetailId(null)
          tasks.reload()
        }}
      />
    </>
  )
}

function TaskDetailDrawer({
  id,
  onClose,
  onOperation,
  canOperate,
}: {
  id: string | null
  onClose: () => void
  onOperation: (kind: TaskOperation, taskId: string) => void
  canOperate: boolean
}) {
  const detail = useAsync(
    () => (id ? getTask(id).then(response => response.data) : Promise.resolve(null)),
    [id],
    { enabled: Boolean(id) },
  )

  return (
    <Modal open={Boolean(id)} onClose={onClose} title="任务详情" description={id ?? undefined} drawer wide>
      {detail.loading ? <LoadingBlock rows={6} /> : null}
      {detail.error ? <ErrorState error={detail.error} onRetry={detail.reload} /> : null}
      {detail.data ? (
        <TaskDetailContent
          task={detail.data}
          onOperation={kind => onOperation(kind, detail.data!.id)}
          onReload={detail.reload}
          canOperate={canOperate}
        />
      ) : null}
    </Modal>
  )
}

function mediaRecords(media: TaskMediaEvidence | null): TaskMediaEvidence['records'] {
  if (!media || !media.available) return []
  return media.records
}

function estimateText(estimate: TaskEstimate | null): string {
  if (!estimate || !estimate.estimated) return '未提供剩余时间估算'
  if (estimate.remaining_seconds_min === null && estimate.remaining_seconds_max === null) {
    return '未提供剩余时间估算'
  }
  if (estimate.remaining_seconds_min !== null && estimate.remaining_seconds_max !== null) {
    return `约 ${formatDuration(estimate.remaining_seconds_min)} – ${formatDuration(estimate.remaining_seconds_max)} · 估算`
  }
  return `约 ${formatDuration(estimate.remaining_seconds_min ?? estimate.remaining_seconds_max)} · 估算`
}

function TaskDetailContent({
  task,
  onOperation,
  onReload,
  canOperate,
}: {
  task: TaskDetail
  onOperation: (kind: TaskOperation) => void
  onReload: () => void
  canOperate: boolean
}) {
  const media = mediaRecords(task.media_evidence)
  const platform = platformLabel(task.platform?.status ?? task.platform_status)
  return (
    <>
      <div className="account-detail-header">
        <span className="workspace-icon" style={{ width: 40, height: 40 }}>
          <ListTodo size={18} />
        </span>
        <div style={{ flex: 1, minWidth: 0 }}>
          <h2>{task.title}</h2>
          <p>{task.id} · {task.course_title ?? '未提供课程'}</p>
        </div>
        <TaskStateBadge state={task.state} label={task.state_label ?? undefined} />
      </div>

      <div className="info-grid">
        <div className="info-tile">
          <span>所属用户</span>
          <b>{task.user_display_name ?? task.user_id ?? '未提供'}</b>
        </div>
        <div className="info-tile">
          <span>任务版本</span>
          <b>{task.version === null ? '未提供' : `v${task.version}`}</b>
        </div>
        <div className="info-tile">
          <span>期望状态</span>
          <b>{task.desired_state ? desiredStateLabels[task.desired_state] ?? task.desired_state : '未提供'}</b>
        </div>
        <div className="info-tile">
          <span>剩余时间</span>
          <b style={{ fontSize: 11 }}>{estimateText(task.estimate)}</b>
        </div>
        <div className="info-tile">
          <span>尝试次数</span>
          <b>{formatNumber(task.attempt_count)}</b>
        </div>
        <div className="info-tile">
          <span>计费状态</span>
          <b>{task.billing?.state ? billingStateLabels[task.billing.state] ?? task.billing.state : task.billing?.configured === false ? '未连接账本' : '未提供'}</b>
        </div>
        <div className="info-tile">
          <span>计费金额</span>
          <b>{task.billing?.quoted_amount_minor ? formatMoneyMinor(task.billing.quoted_amount_minor) : '未提供'}</b>
        </div>
      </div>

      <div className="detail-section">
        <div className="detail-section-title"><Activity size={13} /> 执行观察</div>
        <div className="callout">
          <Activity size={15} />
          <div style={{ flex: 1 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', gap: 10, marginBottom: 8 }}>
              <b>{formatPercent(task.observation?.fraction ?? task.progress_fraction)}</b>
              <span>{task.observation?.source ?? '来源未提供'}</span>
            </div>
            <Progress value={task.observation?.fraction ?? task.progress_fraction} />
            <div className="form-hint" style={{ marginTop: 7 }}>
              观察时间：{formatDateTime(task.observation?.observed_at ?? null)}
              {task.observation?.detail ? ` · ${task.observation.detail}` : ''}
            </div>
          </div>
        </div>
      </div>

      <div className="detail-section">
        <div className="detail-section-title"><CheckCheck size={13} /> 平台确认依据</div>
        <div className="callout">
          <CheckCheck size={15} />
          <div>
            <Badge tone={platform.tone} dot={platform.tone === 'green'}>{platform.text}</Badge>
            <div className="form-hint" style={{ marginTop: 7 }}>
              核验时间：{formatDateTime(task.platform?.checked_at ?? null)}
              {task.platform?.basis ? ` · 依据：${task.platform.basis}` : ' · 暂无确认依据'}
            </div>
          </div>
        </div>
        {platform.tone !== 'green' ? (
          <p className="form-hint" style={{ marginTop: 8 }}>
            执行观察结束或进度达到 100% 不等于平台已确认，需等待平台核验结果。
          </p>
        ) : null}
      </div>

      <div className="detail-section">
        <div className="detail-section-title"><Film size={13} /> 媒体记录</div>
        {media.length ? (
          <dl className="detail-list">
            {media.map((record, index) => (
              <div key={record.id ?? index}>
                <dt>{record.id ?? `记录 ${index + 1}`}</dt>
                <dd>
                  观察于 {formatDateTime(record.observed_at)}
                  {record.duration_seconds !== null ? ` · ${formatDuration(record.duration_seconds)}` : ''}
                  {record.detail ? <span className="cell-sub">{record.detail}</span> : null}
                </dd>
              </div>
            ))}
          </dl>
        ) : (
          <div className="callout">
            <Film size={15} />
            <div>
              <b>无播放记录</b>
              <div className="form-hint" style={{ marginTop: 4 }}>
                {task.media_evidence?.note ?? '暂无可用媒体证据。'}
                {task.media_evidence?.source ? ` 来源：${task.media_evidence.source}` : ''}
              </div>
            </div>
          </div>
        )}
      </div>

      {task.timeline.length ? (
        <div className="detail-section">
          <div className="detail-section-title"><Clock3 size={13} /> 任务时间线</div>
          <dl className="detail-list">
            {task.timeline.map((item, index) => (
              <div key={`${item.at ?? 'unknown'}-${index}`}>
                <dt>{formatDateTime(item.at)}</dt>
                <dd>
                  <b>{item.title}</b>
                  {item.detail ? <span className="cell-sub">{item.detail}</span> : null}
                </dd>
              </div>
            ))}
          </dl>
        </div>
      ) : null}

      <div className="detail-actions">
        {canOperate ? <Button
          disabled={!operationAllowed(task, 'pause')}
          onClick={() => onOperation('pause')}
        >
          <Pause size={13} /> 暂停
        </Button> : null}
        {canOperate ? (
          <>
            <Button
              disabled={!operationAllowed(task, 'resume')}
              onClick={() => onOperation('resume')}
            >
              <Play size={13} /> 继续
            </Button>
            <Button
              disabled={!operationAllowed(task, 'reschedule')}
              onClick={() => onOperation('reschedule')}
            >
              <RotateCcw size={13} /> 重新调度
            </Button>
            <Button
              variant="danger"
              disabled={!operationAllowed(task, 'cancel')}
              onClick={() => onOperation('cancel')}
            >
              <X size={13} /> 取消任务
            </Button>
          </>
        ) : null}
        <Button variant="ghost" onClick={onReload}><RefreshCw size={13} /> 刷新证据</Button>
      </div>
      <p className="form-hint" style={{ marginTop: 10 }}>
        {canOperate
          ? '控制操作必须填写原因，操作前会重新核对任务状态；平台已确认任务不会回退重试。'
          : '当前角色只有任务读取权限，控制操作需要任务操作权限。'}
      </p>
    </>
  )
}

function TaskOperationDialog({
  operation,
  onClose,
  onCompleted,
}: {
  operation: { kind: TaskOperation; taskIds: string[] } | null
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
        taskIds: operation.taskIds,
        operation: operation.kind,
        reason: reason.trim(),
      }
      const response = await runTaskOperation(payload, idempotency.keyFor(payload))
      setResult(response)
      onCompleted()
      if (response.updated === null) {
        notify('任务操作已受理，未返回处理数量。', 'success')
      } else {
        notify(
          `已受理 ${response.updated} 项任务${response.skipped ? `，跳过 ${response.skipped} 项` : ''}。`,
          response.skipped ? 'warning' : 'success',
        )
      }
    } catch (reasonValue) {
      setError(reasonValue)
      notify(describeError(reasonValue).message, 'error')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      title={copy.title}
      description={`操作范围：${operation.taskIds.length} 项显式选择的任务。`}
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
                ? '本次操作已受理，但没有返回更新数量。'
                : `已受理 ${result.updated} 项任务${result.skipped ? `，跳过 ${result.skipped} 项` : '。'}`}
            </div>
          </div>
          {result.items.length ? (
            <div className="detail-section">
              <div className="detail-section-title">逐项结果</div>
              <dl className="detail-list">
                {result.items.map(item => (
                  <div key={`${item.id}-${item.status}`}>
                    <dt className="mono">{item.id || '未提供编号'}</dt>
                    <dd>
                      {item.status}
                      {item.stopRequested ? ' · 停止请求已接受' : ''}
                      {item.applied === false && !item.stopRequested ? ' · 等待执行器确认' : ''}
                      {item.message ? ` · ${item.message}` : ''}
                    </dd>
                  </div>
                ))}
              </dl>
            </div>
          ) : null}
        </>
      ) : (
        <>
          <div className={`callout ${operation.kind === 'cancel' ? 'callout-warning' : ''}`}>
            <CircleAlert size={16} />
            <div>{copy.description}</div>
          </div>
          <div style={{ marginTop: 18 }}>
            <Field
              label="操作原因"
              required
              hint="审计会记录操作者、任务集合、原因与请求编号。"
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
          <p className="form-hint" style={{ marginTop: 12 }}>
            状态不适用的任务会被跳过；平台已确认任务不会回退重试。
          </p>
        </>
      )}
    </Modal>
  )
}
