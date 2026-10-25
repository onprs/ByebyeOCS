import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Activity,
  ArrowRight,
  CheckCheck,
  CircleAlert,
  Cpu,
  Database,
  Gauge,
  HardDrive,
  ListTodo,
  RefreshCw,
} from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { hasPermission } from '../auth/permissions'
import { getDashboard, getHealth, getRuntimeSettings, listTasks } from '../api/endpoints'
import type { AdminEvent, DashboardMetric, RuntimeResource } from '../api/types'
import {
  Badge,
  Button,
  Card,
  CardHeader,
  ErrorState,
  LoadingBlock,
  Metric,
  PageHeader,
  TaskStateBadge,
} from '../components'
import {
  formatDateTime,
  formatNumber,
} from '../lib/format'
import { useAdminEvents } from '../lib/useAdminEvents'
import { useAsync } from '../lib/hooks'

function resourceBadge(resource: RuntimeResource) {
  if (resource.state === 'healthy') return <Badge tone="green" dot>健康</Badge>
  if (resource.state === 'degraded') return <Badge tone="amber" dot>降级</Badge>
  if (resource.state === 'unavailable') return <Badge tone="red" dot>不可用</Badge>
  return null
}

function resourceIcon(key: string) {
  if (key.includes('cpu')) return <Cpu size={14} />
  if (key.includes('memory')) return <HardDrive size={14} />
  if (key.includes('worker')) return <Gauge size={14} />
  if (key.includes('tiku') || key.includes('question')) return <Database size={14} />
  return <Activity size={14} />
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
    }).slice(0, 5)
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

        actions={
          <>
            <span className="form-hint">{generatedAt ? `生成于 ${formatDateTime(generatedAt)}` : '—'}</span>
            <Button onClick={() => { dashboard.reload(); health.reload(); tasksPreview.reload(); runtimeInfo.reload() }} loading={dashboard.loading || tasksPreview.loading}>
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
            <CircleAlert size={14} /> {formatNumber(attentionValue)} 项需要关注 <ArrowRight size={14} />
          </button>
        ) : data.sections_unavailable.length ? (
          <span className="strip-secondary">部分汇总暂未提供：{data.sections_unavailable.join('、')}</span>
        ) : null}
      </div>

      <section className="metric-band" aria-label="关键指标">
        {metricSlots.slice(0, 4).map(metric => (
          <Metric key={metric.key} label={metric.label} value={metric.value} unit={metric.unit} />
        ))}
      </section>

      <div className="dashboard-columns">
        <div className="dashboard-column">
        <Card>
          <CardHeader
            title={chartPoints.length ? '任务运行趋势' : '任务状态分布'}

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
          {chartPoints.length ? (
            <div className="queue-facts">
              <span>执行中 <b>{formatNumber(data.limits.running_tasks)}</b></span>
              <span>等待队列 <b>{formatNumber(data.limits.queued_tasks)}</b></span>
              <span>等待验证 <b>{formatNumber(Number(data.metrics.find(metric => metric.key === 'waiting_verification')?.value ?? NaN))}</b></span>
              <span>需要关注 <b>{formatNumber(attentionValue)}</b></span>
            </div>
          ) : null}
   </Card>
        <Card>
          <CardHeader
            title="正在处理"

            actions={<Badge tone="blue"><ListTodo size={14} /> {tasksPreview.data?.items.length ?? 0} 项</Badge>}
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
                    <span>{task.course_title ?? '—'}</span>
                  </div>
                  <span className="compact-row-user mono">{task.user_id ?? '—'}</span>
                  <TaskStateBadge state={task.state} label={task.state_label ?? undefined} />
                </button>
              ))}
            </>
          ) : tasksPreview.error ? (
            <div className="resource-empty">
              任务摘要读取失败。<button className="text-link" onClick={tasksPreview.reload}>重试</button>
            </div>
          ) : (
            <p className="empty-line">当前没有执行中、等待验证或需要关注的任务。</p>
          )}

        </Card>
        </div>

        <div className="dashboard-column">
        <Card>
          <CardHeader
            title="运行资源"

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
                    {resource.value ?? '—'}
                    {resource.unit ? <small>{resource.unit}</small> : null}
                    {resourceBadge(resource) ? <span className="u-margin-left-6px" >{resourceBadge(resource)}</span> : null}
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
            <span>同一任务 <b>{data.limits.task_exclusive === null ? '—' : data.limits.task_exclusive ? '独占执行' : '未启用独占'}</b></span>
          </div>

        </Card>
        <Card>
          <CardHeader
            title="实时事件"

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
            <p className="empty-line">当前角色没有事件读取权限。</p>
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
                    {event.type.includes('confirm') ? <CheckCheck size={14} /> : event.type.includes('attention') ? <CircleAlert size={14} /> : <Activity size={14} />}
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
            <p className="empty-line">暂无可显示的实时事件。</p>
          )}

        </Card>
        </div>
      </div>


    </>
  )
}
