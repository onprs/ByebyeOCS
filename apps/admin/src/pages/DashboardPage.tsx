import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Activity,
  ArrowRight,
  CheckCheck,
  CircleAlert,
  Clock3,
  Cpu,
  Database,
  Gauge,
  HardDrive,
  ListTodo,
  RefreshCw,
  Server,
  ShieldCheck,
} from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { hasPermission } from '../auth/permissions'
import { getDashboard, getFinanceSummary, getHealth, getRuntimeSettings, listTasks } from '../api/endpoints'
import type { AdminEvent, DashboardMetric, RuntimeResource } from '../api/types'
import {
  Badge,
  Button,
  Card,
  CardHeader,
  EmptyState,
  ErrorState,
  LoadingBlock,
  Metric,
  PageHeader,
  TaskStateBadge,
} from '../components'
import {
  formatDateTime,
  formatMoneyMinor,
  formatNumber,
} from '../lib/format'
import { useAdminEvents } from '../lib/useAdminEvents'
import { useAsync } from '../lib/hooks'

function metricTone(metric: DashboardMetric) {
  if (metric.key.includes('attention') || metric.key.includes('failed')) return <Badge tone="amber">需关注</Badge>
  if (metric.key.includes('running')) return <Badge tone="blue">执行中</Badge>
  if (metric.key.includes('confirmed')) return <Badge tone="green">已确认</Badge>
  return null
}

function resourceBadge(resource: RuntimeResource) {
  if (resource.state === 'healthy') return <Badge tone="green" dot>健康</Badge>
  if (resource.state === 'degraded') return <Badge tone="amber" dot>降级</Badge>
  if (resource.state === 'unavailable') return <Badge tone="red" dot>不可用</Badge>
  return null
}

function resourceIcon(key: string) {
  if (key.includes('cpu')) return <Cpu size={13} />
  if (key.includes('memory')) return <HardDrive size={13} />
  if (key.includes('worker')) return <Gauge size={13} />
  if (key.includes('tiku') || key.includes('question')) return <Database size={13} />
  return <Activity size={13} />
}

function throughputPath(points: Array<{ observed: number | null; confirmed: number | null }>, key: 'observed' | 'confirmed'): string {
  const values = points.map(point => point[key])
  const max = Math.max(1, ...values.filter((value): value is number => value !== null))
  const width = 640
  const height = 132
  const step = points.length > 1 ? width / (points.length - 1) : width
  const coords = points
    .map((point, index) => {
      const value = point[key]
      if (value === null) return null
      return [index * step, height - (Math.min(value, max) / max) * height] as const
    })
    .filter((value): value is readonly [number, number] => value !== null)
  if (coords.length === 0) return ''
  if (coords.length === 1) return `M${coords[0][0]} ${coords[0][1]}L${coords[0][0]} ${coords[0][1]}`
  return coords.map(([x, y], index) => `${index === 0 ? 'M' : 'L'}${x.toFixed(1)} ${y.toFixed(1)}`).join(' ')
}

function eventTone(event: AdminEvent): string {
  const raw = `${event.tone ?? ''} ${event.type}`.toLowerCase()
  if (raw.includes('fail') || raw.includes('error') || raw.includes('attention')) return 'amber'
  if (raw.includes('confirm') || raw.includes('credit') || raw.includes('success')) return 'green'
  if (raw.includes('running') || raw.includes('update')) return 'blue'
  return ''
}

export function DashboardPage() {
  const navigate = useNavigate()
  const { me } = useAuth()
  const canReadEvents = hasPermission(me, 'events.read')
  const canReadBilling = hasPermission(me, 'billing.read')
  const canReadOperations = hasPermission(me, 'operations.read')
  const dashboard = useAsync(() => getDashboard().then(response => response.data), [])
  const health = useAsync(() => getHealth('ready').then(response => response.data), [])
  const tasksPreview = useAsync(
    () =>
      listTasks({
        states: ['preparing', 'running', 'waiting_verification', 'needs_attention'],
        limit: 5,
      }).then(response => response.data),
    [],
  )
  const financeSummary = useAsync(
    () => (canReadBilling ? getFinanceSummary().then(response => response.data) : Promise.resolve(null)),
    [canReadBilling],
  )
  const runtimeInfo = useAsync(
    () => (canReadOperations ? getRuntimeSettings().then(response => response.data) : Promise.resolve(null)),
    [canReadOperations],
  )
  const runtimeValues = useMemo(() => {
    const values: Record<string, string> = {}
    for (const item of runtimeInfo.data?.runtime ?? []) values[item.key] = item.value
    return values
  }, [runtimeInfo.data])
  const [liveEvents, setLiveEvents] = useState<AdminEvent[]>([])
  const refreshTimer = useRef<number | null>(null)

  const live = useAdminEvents({
    enabled: canReadEvents,
    onEvent: event => {
      setLiveEvents(current => [...current.slice(-19), event])
      if (refreshTimer.current) window.clearTimeout(refreshTimer.current)
      refreshTimer.current = window.setTimeout(() => {
        dashboard.reload()
        health.reload()
        tasksPreview.reload()
        financeSummary.reload()
      }, 1200)
    },
  })

  useEffect(() => () => {
    if (refreshTimer.current) window.clearTimeout(refreshTimer.current)
  }, [])

  const data = dashboard.data
  const globalConcurrency = data?.limits.global_concurrency ?? Number(runtimeValues.global_concurrency ?? NaN)
  const accountConcurrency = data?.limits.account_concurrency ?? Number(runtimeValues.account_concurrency ?? NaN)
  const events = useMemo(() => {
    const fromServer = data?.events ?? []
    const ids = new Set(fromServer.map(event => event.event_id))
    const merged = [...liveEvents.filter(event => !ids.has(event.event_id)), ...fromServer]
    return merged.sort((a, b) => {
      const left = a.occurred_at ? Date.parse(a.occurred_at) : 0
      const right = b.occurred_at ? Date.parse(b.occurred_at) : 0
      return right - left
    }).slice(0, 6)
  }, [data?.events, liveEvents])

  const attentionMetric = data?.metrics.find(metric => metric.key.includes('attention'))
  const attentionValue = attentionMetric?.value ? Number(attentionMetric.value) : null
  const healthReady = health.data?.status === 'ok'
  const healthUnavailable = Boolean(health.error)
  const generatedAt = data?.generated_at ?? null

  if (dashboard.loading && !data) {
    return (
      <>
        <PageHeader
          title="运行总览"
          description="服务运行、任务吞吐、资源状态与需要处理的异常。"
        />
        <LoadingBlock rows={5} />
      </>
    )
  }

  if (dashboard.error && !data) {
    return (
      <>
        <PageHeader
          title="运行总览"
          description="服务运行、任务吞吐、资源状态与需要处理的异常。"
        />
        <ErrorState error={dashboard.error} onRetry={dashboard.reload} />
      </>
    )
  }

  if (!data) return null

  const metricSlots: DashboardMetric[] = [...data.metrics]
  while (metricSlots.length < 4) {
    metricSlots.push({ key: `empty_${metricSlots.length}`, label: '暂无指标', value: null, unit: null, hint: null })
  }

  const chartPoints = data.throughput.points
  const observedPath = throughputPath(chartPoints, 'observed')
  const confirmedPath = throughputPath(chartPoints, 'confirmed')
  const totalObserved = chartPoints.reduce((sum, point) => sum + (point.observed ?? 0), 0)
  const hasObserved = chartPoints.some(point => point.observed !== null)
  const hasConfirmed = chartPoints.some(point => point.confirmed !== null)

  return (
    <>
      <PageHeader
        title="运行总览"
        description="服务运行、任务吞吐、资源状态与需要处理的异常。"
        actions={
          <>
            <span className="form-hint">{generatedAt ? `生成于 ${formatDateTime(generatedAt)}` : '未提供生成时间'}</span>
            <Button onClick={() => { dashboard.reload(); health.reload(); tasksPreview.reload(); financeSummary.reload(); runtimeInfo.reload() }} loading={dashboard.loading || tasksPreview.loading || financeSummary.loading}>
              <RefreshCw size={14} /> 刷新
            </Button>
          </>
        }
      />

      <div className="system-strip">
        <span>
          <span className={`service-dot ${healthReady ? 'healthy' : healthUnavailable ? 'unavailable' : 'degraded'}`} />
          <b>{healthReady ? '管理服务已就绪' : healthUnavailable ? '就绪检查不可用' : '正在检查服务就绪状态'}</b>
        </span>
        {attentionValue !== null && attentionValue > 0 ? (
          <button className="strip-action" onClick={() => navigate('/tasks?group=attention')}>
            <CircleAlert size={13} /> {formatNumber(attentionValue)} 项需要关注 <ArrowRight size={12} />
          </button>
        ) : data.sections_unavailable.length ? (
          <span className="strip-secondary">部分汇总暂未提供：{data.sections_unavailable.join('、')}</span>
        ) : null}
      </div>

      <section className="metric-band" aria-label="关键指标">
        {metricSlots.slice(0, 4).map(metric => (
          <Metric key={metric.key} label={metric.label} value={metric.value} unit={metric.unit}>
            {metric.hint ? <span>{metric.hint}</span> : null}
            {metricTone(metric)}
          </Metric>
        ))}
      </section>

      <div className="dashboard-grid-main">
        <Card>
          <CardHeader
            title={chartPoints.length ? '任务运行趋势' : '任务状态分布'}
            subtitle={chartPoints.length ? '执行观察与平台确认分别统计，不以观察进度推断平台完成。' : '暂无趋势数据，展示当前任务分布。'}
            actions={
              chartPoints.length ? (
                <div className="chart-legend">
                  <span><i /> 执行观察</span>
                  <span><i className="secondary" /> 平台确认</span>
                </div>
              ) : null
            }
          />
          {chartPoints.length ? (
            <>
              <div className="throughput-summary">
                <div className="throughput-value">
                  <b>{formatNumber(totalObserved)}</b>
                  <span>{data.throughput.period ? `${data.throughput.period}累计观察事件` : '累计观察事件'}</span>
                </div>
                <span className="form-hint">{chartPoints.length} 个时间桶</span>
              </div>
              <div className="throughput-chart">
                <div className="chart-grid" aria-hidden="true">
                  {Array.from({ length: 4 }, (_, index) => <i key={index} />)}
                </div>
                <svg viewBox="0 0 640 132" preserveAspectRatio="none" role="img" aria-label="执行观察与平台确认趋势">
                  {hasObserved ? (
                    <>
                      <path d={`${observedPath}L640 132L0 132Z`} fill="var(--accent)" opacity="0.07" />
                      <path d={observedPath} fill="none" stroke="var(--accent)" strokeWidth="2" vectorEffect="non-scaling-stroke" />
                    </>
                  ) : null}
                  {hasConfirmed ? (
                    <path
                      d={confirmedPath}
                      fill="none"
                      stroke="var(--chart-secondary)"
                      strokeWidth="1.8"
                      strokeDasharray="4 4"
                      vectorEffect="non-scaling-stroke"
                    />
                  ) : null}
                </svg>
              </div>
              <div className="chart-axis">
                <span>{chartPoints[0]?.at ? formatDateTime(chartPoints[0].at) : '起始'}</span>
                <span>{formatDateTime(chartPoints[Math.floor(chartPoints.length / 2)]?.at ?? null)}</span>
                <span>{formatDateTime(chartPoints[chartPoints.length - 1]?.at ?? null)}</span>
              </div>
            </>
          ) : data.state_counts.length ? (
            <div className="state-distribution">
              {data.state_counts.map(item => {
                const max = Math.max(...data.state_counts.map(entry => entry.count), 1)
                const labels: Record<string, string> = {
                  pending: '待执行',
                  preparing: '准备中',
                  running: '执行中',
                  waiting_verification: '等待验证',
                  confirmed: '平台已确认',
                  paused: '已暂停',
                  needs_attention: '需要人工处理',
                  failed: '失败',
                  cancelled: '已取消',
                }
                return (
                  <div key={item.state} className="state-distribution-row">
                    <span>{labels[item.state] ?? item.state}</span>
                    <span className="state-distribution-bar"><i style={{ width: `${Math.max(3, (item.count / max) * 100)}%` }} /></span>
                    <b>{formatNumber(item.count)}</b>
                  </div>
                )
              })}
            </div>
          ) : (
            <div className="chart-empty">暂无趋势或状态数据</div>
          )}
          <div className="queue-facts">
            <span>执行中 <b>{formatNumber(data.limits.running_tasks)}</b></span>
            <span>等待队列 <b>{formatNumber(data.limits.queued_tasks)}</b></span>
            <span>等待验证 <b>{formatNumber(Number(data.metrics.find(metric => metric.key === 'waiting_verification')?.value ?? NaN))}</b></span>
            <span>需要关注 <b>{formatNumber(attentionValue)}</b></span>
          </div>
          <div className="card-footer-link">
            <ShieldCheck size={11} /> 平台确认需要查询依据，观察进度不等于完成。
            <button onClick={() => navigate('/tasks')}>查看任务 <ArrowRight size={12} /></button>
          </div>
        </Card>

        <Card>
          <CardHeader
            title="运行资源"
            subtitle="运行状态与当前并发。"
            actions={
              healthReady ? <Badge tone="green" dot>就绪</Badge> : healthUnavailable ? <Badge tone="amber">未确认</Badge> : null
            }
          />
          {data.resources.length ? (
            <div className="resource-list">
              {data.resources.map(resource => (
                <div className="resource-row" key={resource.key}>
                  <div className="resource-row-label">
                    <span>{resourceIcon(resource.key)} {resource.label}</span>
                  </div>
                  <div className="resource-row-value">
                    {resource.value ?? '未提供'}
                    {resource.unit ? <small>{resource.unit}</small> : null}
                    {resourceBadge(resource) ? <span style={{ marginLeft: 6 }}>{resourceBadge(resource)}</span> : null}
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <div className="resource-empty">暂无资源监测数据</div>
          )}
          <div className="resource-limits">
            <span>全局执行名额 <b>{formatNumber(Number.isFinite(globalConcurrency) ? globalConcurrency : null)}</b></span>
            <span>账号默认并发 <b>{formatNumber(Number.isFinite(accountConcurrency) ? accountConcurrency : null)}</b></span>
            <span>执行中任务 <b>{formatNumber(data.limits.running_tasks)}</b></span>
            <span>等待队列 <b>{formatNumber(data.limits.queued_tasks)}</b></span>
            <span>同一任务 <b>{data.limits.task_exclusive === null ? '未提供' : data.limits.task_exclusive ? '独占执行' : '未启用独占'}</b></span>
          </div>
          <div className="card-footer-link">
            <Server size={11} /> 执行名额与并发限制
            <button onClick={() => navigate('/settings?section=runtime')}>运行设置 <ArrowRight size={12} /></button>
          </div>
        </Card>
      </div>

      <div className="dashboard-grid-bottom">
        <Card>
          <CardHeader
            title="正在处理"
            subtitle="执行、验证与需要关注的任务。"
            actions={<Badge tone="blue"><ListTodo size={11} /> {tasksPreview.data?.items.length ?? 0} 项</Badge>}
          />
          {tasksPreview.loading && !tasksPreview.data ? (
            <LoadingBlock rows={3} />
          ) : tasksPreview.data?.items.length ? (
            <>
              <div className="compact-head">
                <span>任务 / 课程</span>
                <span>用户</span>
                <span>状态</span>
              </div>
              {tasksPreview.data.items.map(task => (
                <button
                  className="compact-row"
                  key={task.id}
                  onClick={() => navigate(`/tasks?task=${encodeURIComponent(task.id)}`)}
                >
                  <div className="compact-row-main">
                    <b>{task.title}</b>
                    <span>{task.course_title ?? '未提供课程'}</span>
                  </div>
                  <span className="compact-row-user mono">{task.user_id ?? '未提供'}</span>
                  <TaskStateBadge state={task.state} label={task.state_label ?? undefined} />
                </button>
              ))}
            </>
          ) : tasksPreview.error ? (
            <div className="resource-empty">
              任务摘要读取失败。<button className="text-link" onClick={tasksPreview.reload}>重试</button>
            </div>
          ) : (
            <EmptyState title="暂无进行中的任务" description="当前没有执行、验证或需要关注的任务。" />
          )}
          <div className="card-footer-link">
            <Activity size={11} /> 最近更新 {formatDateTime(tasksPreview.data?.items[0]?.updated_at ?? null)}
            <button onClick={() => navigate('/tasks')}>全部任务 <ArrowRight size={12} /></button>
          </div>
        </Card>

        <Card>
          <CardHeader
            title="财务摘要"
            subtitle="支付成功与余额到账分别记录，汇总覆盖完整账本。"
            actions={financeSummary.data ? <Badge tone="blue">全量</Badge> : null}
          />
          {!canReadBilling ? (
            <EmptyState title="当前角色没有财务读取权限" description="财务页面需要财务读取权限。" />
          ) : financeSummary.loading && !financeSummary.data ? (
            <LoadingBlock rows={3} />
          ) : financeSummary.error ? (
            <div style={{ padding: 16 }}>
              <ErrorState error={financeSummary.error} onRetry={financeSummary.reload} compact />
            </div>
          ) : financeSummary.data ? (
            <>
              <div className="finance-summary">
                <div className="finance-summary-main">
                  <span>钱包可用余额</span>
                  <b>{formatMoneyMinor(financeSummary.data.current.wallets.available_minor)}</b>
                </div>
                <div className="finance-summary-grid">
                  <span>钱包 <b>{formatNumber(financeSummary.data.current.wallets.count)} 个 · 冻结 {formatNumber(financeSummary.data.current.wallets.frozen_count)} 个</b></span>
                  <span>支付成功未入账 <b>{formatMoneyMinor(financeSummary.data.current.paid_not_credited.amount_minor)} · {formatNumber(financeSummary.data.current.paid_not_credited.count)} 笔</b></span>
                  <span>订单待核对 <b>{formatMoneyMinor(financeSummary.data.current.orders_needs_review.amount_minor)} · {formatNumber(financeSummary.data.current.orders_needs_review.count)} 笔</b></span>
                  <span>退款处理中 <b>{formatMoneyMinor(financeSummary.data.current.refunds_open.amount_minor)} · {formatNumber(financeSummary.data.current.refunds_open.count)} 笔</b></span>
                </div>
              </div>
              <div className="card-footer-link">
                <ShieldCheck size={11} /> 付款返回不会直接增加余额
                <button onClick={() => navigate('/finance')}>财务详情 <ArrowRight size={12} /></button>
              </div>
            </>
          ) : (
            <EmptyState title="暂无财务摘要" description="账本尚未产生可汇总的记录。" />
          )}
        </Card>

        <Card>
          <CardHeader
            title="实时事件"
            subtitle="运行状态变化会实时显示；连接中断时显示为已断开。"
            actions={
              canReadEvents ? (
                <Badge tone={live.connection === 'connected' ? 'green' : live.connection === 'connecting' ? 'blue' : 'amber'} dot>
                  {live.connection === 'connected'
                    ? '已连接'
                    : live.connection === 'connecting'
                      ? '连接中'
                      : live.connection === 'unavailable'
                        ? '不可用'
                        : '已断开'}
                </Badge>
              ) : <Badge tone="neutral">无读取权限</Badge>
            }
          />
          {!canReadEvents ? (
            <EmptyState title="当前角色没有事件读取权限" description="运行摘要仍可查看；实时事件需要 events.read 权限。" />
          ) : events.length ? (
            <div className="event-list">
              {events.map(event => (
                <button
                  className="event-row"
                  key={event.event_id || `${event.type}-${event.occurred_at}`}
                  onClick={() => {
                    const type = `${event.type} ${event.tone ?? ''}`.toLowerCase()
                    if (type.includes('wallet') || type.includes('order') || type.includes('credit')) {
                      navigate('/finance')
                    } else if (type.includes('user') || type.includes('session') || type.includes('account')) {
                      navigate('/users')
                    } else if (event.resource_id) {
                      navigate(`/tasks?task=${encodeURIComponent(event.resource_id)}`)
                    } else {
                      navigate('/tasks')
                    }
                  }}
                >
                  <span className={`event-icon ${eventTone(event)}`}>
                    {event.type.includes('confirm') ? <CheckCheck size={12} /> : event.type.includes('attention') ? <CircleAlert size={12} /> : <Activity size={12} />}
                  </span>
                  <span className="event-copy">
                    <b>{event.title}</b>
                    <span>{event.detail ?? event.type}</span>
                  </span>
                  <time>{event.occurred_at ? formatDateTime(event.occurred_at) : ''}</time>
                </button>
              ))}
            </div>
          ) : (
            <EmptyState title="暂无实时事件" description="暂无可显示的事件，运行状态以页面摘要为准。" />
          )}
          <div className="card-footer-link">
            {!canReadEvents ? (
              <><ShieldCheck size={11} /> 当前角色没有事件读取权限</>
            ) : live.connection === 'connected' ? (
              <><Clock3 size={11} /> 事件持续接收中</>
            ) : (
              <><CircleAlert size={11} /> 事件流未连接，可刷新页面获取最新状态</>
            )}
            {canReadEvents && live.connection !== 'connected' ? (
              <button onClick={() => { live.reconnect(); dashboard.reload(); tasksPreview.reload(); financeSummary.reload() }}>重试连接 <RefreshCw size={11} /></button>
            ) : null}
          </div>
        </Card>
      </div>

      <div className="dashboard-footer">
        <span><ShieldCheck size={11} /> 需要关注的异常会显示最新状态</span>
        <Button variant="ghost" size="sm" onClick={() => navigate('/settings')}>查看配置 <ArrowRight size={12} /></Button>
      </div>
    </>
  )
}
