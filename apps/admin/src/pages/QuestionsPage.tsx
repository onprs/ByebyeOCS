import { useEffect, useState } from 'react'
import {
  Check,
  ChevronRight,
  CircleAlert,
  Cpu,
  Pencil,
  Plus,
  RefreshCw,
  ShieldCheck,
  Sparkles,
  TestTube2,
  X,
} from 'lucide-react'
import {
  createQuestionModel,
  getQuestionUsage,
  invalidateQuestionCache,
  listQuestionModels,
  listQuestionQueries,
  testQuestionModel,
  updateQuestionModel,
} from '../api/endpoints'
import type { ModelPricing, QuestionModel, QuestionQuery, QuestionUsageSummary } from '../api/types'
import { useAuth } from '../auth/AuthProvider'
import { hasPermission } from '../auth/permissions'
import {
  Badge,
  Button,
  Card,
  CardHeader,
  EmptyState,
  ErrorState,
  Field,
  IconButton,
  LoadingBlock,
  Modal,
  PageHeader,
  Pagination,
  RefreshError,
  Select,
  Switch,
  useToast,
} from '../components'
import { describeError } from '../components/ui'
import {
  formatDateTime,
  formatNumber,
  formatPriceMicros,
  microsToYuanInput,
  parseYuanToMicros,
  truncateMiddle,
} from '../lib/format'
import { useAsync, useCursorStack, useDebouncedValue } from '../lib/hooks'

interface ModelDraft {
  display_name: string
  description: string
  provider_ref: string
  capabilities: string[]
  input_price: string
  output_price: string
  price_version: string
  micros_per_minor: string
  rounding: string
  rule: string
  cache_allow: boolean
  cache_scope: string
  cache_ttl_days: string
  enabled: boolean
  is_default: boolean
  sort_order: string
}

interface ModelTestResult {
  kind: string | null
  status: string | null
  reachable: boolean | null
  functional: boolean | null
  latencyMs: number | null
  errorCode: string | null
  requestId: string | null
}

function providerLabel(model: QuestionModel): string {
  return model.provider_ref ?? '未提供模型标识'
}

function capabilityLabel(capability: string): string {
  if (capability === 'text') return '文本'
  if (capability === 'vision' || capability === 'image') return '图片'
  return capability
}

function healthBadge(model: QuestionModel) {
  if (model.health === 'healthy') return <Badge tone="green" dot>{model.health_label ?? '健康'}</Badge>
  if (model.health === 'configured') return <Badge tone="green">已配置</Badge>
  if (model.health === 'degraded') return <Badge tone="amber" dot>{model.health_label ?? '降级'}</Badge>
  if (model.health === 'unavailable') return <Badge tone="red" dot>{model.health_label ?? '不可用'}</Badge>
  return <Badge>{model.health_label ?? '状态未知'}</Badge>
}

function priceLabel(pricing: ModelPricing | null): { text: string; pending: boolean } {
  if (!pricing) return { text: '价格待公布', pending: true }
  return {
    text: `${formatPriceMicros(pricing.input_micros_per_million_tokens)} / ${formatPriceMicros(pricing.output_micros_per_million_tokens)}`,
    pending: false,
  }
}

function usageRate(usage: QuestionUsageSummary | null): number | null {
  if (!usage) return null
  if (usage.queries !== null && usage.cache_hits !== null && usage.queries > 0) {
    return (usage.cache_hits / usage.queries) * 100
  }
  if (usage.cache_hit_rate !== null) {
    return usage.cache_hit_rate <= 1 ? usage.cache_hit_rate * 100 : usage.cache_hit_rate
  }
  return null
}

function usageCostLabel(usage: QuestionUsageSummary | null): string {
  if (!usage) return '未提供'
  if (usage.cost_state === 'unpriced') return '价格待公布'
  if (usage.automatic_charging === false) return '自动扣费未启用'
  if (usage.cost_micros) return formatPriceMicros(usage.cost_micros)
  return '暂无扣费'
}

const pricingStateLabels: Record<string, string> = {
  quoted: '已报价',
  unpriced: '未定价',
  free: '免费',
  cache_hit: '缓存命中',
  usage_incomplete: '用量不完整',
}

const billingStateLabels: Record<string, string> = {
  unpriced: '未定价',
  automatic_charging_disabled: '自动扣费关闭',
  billing_not_connected: '未连接账本',
  no_charge: '未扣费',
  reserved: '费用已预占',
  settled: '已结算',
  released: '已释放预占',
  reserve_failed: '预占失败',
  billing_settlement_failed: '结算失败',
  billing_release_failed: '释放失败',
}

function queryStatusTone(status: string): 'green' | 'amber' | 'red' | 'neutral' {
  if (status === 'answered' || status === 'cache_hit') return 'green'
  if (status === 'pending' || status === 'needs_review') return 'amber'
  if (status === 'failed') return 'red'
  return 'neutral'
}

function draftFrom(model: QuestionModel | null): ModelDraft {
  const pricing = model?.pricing ?? null
  return {
    display_name: model?.display_name ?? '',
    description: model?.description ?? '',
    provider_ref: model?.provider_ref ?? '',
    capabilities: model?.capabilities?.length ? model.capabilities : ['text'],
    input_price: microsToYuanInput(pricing?.input_micros_per_million_tokens),
    output_price: microsToYuanInput(pricing?.output_micros_per_million_tokens),
    price_version: pricing?.version ?? model?.price_version ?? '',
    micros_per_minor: '10000',
    rounding: pricing?.rounding ?? 'ceil',
    rule: pricing?.rule ?? 'token_usage',
    cache_allow: model?.cache_policy.allow ?? true,
    cache_scope: model?.cache_policy.scope ?? 'user',
    cache_ttl_days: String(model?.cache_policy.ttl_days ?? 7),
    enabled: model?.enabled ?? false,
    is_default: model?.is_default ?? false,
    sort_order: model?.sort_order === null || model?.sort_order === undefined ? '' : String(model.sort_order),
  }
}

export function QuestionsPage() {
  const { me } = useAuth()
  const canManage = hasPermission(me, 'question:manage')
  const { notify } = useToast()
  const [modelCursor, setModelCursor] = useState<string | null>(null)
  const [models, setModels] = useState<QuestionModel[]>([])
  const [modelsLoading, setModelsLoading] = useState(true)
  const [modelsError, setModelsError] = useState<unknown | null>(null)
  const [editing, setEditing] = useState<QuestionModel | null | 'new'>(null)
  const [testModel, setTestModel] = useState<QuestionModel | null>(null)
  const [savingId, setSavingId] = useState<string | null>(null)
  const [queryStatus, setQueryStatus] = useState('')
  const [queryModelId, setQueryModelId] = useState('')
  const queryCursor = useCursorStack(`${queryStatus}|${queryModelId}`)
  const [querySearch, setQuerySearch] = useState('')
  const [queryDetail, setQueryDetail] = useState<QuestionQuery | null>(null)
  const debouncedSearch = useDebouncedValue(querySearch, 250)
  const usage = useAsync(() => getQuestionUsage().then(response => response.data), [])
  const queries = useAsync(
    () =>
      listQuestionQueries({
        status: queryStatus || undefined,
        offeringId: queryModelId || undefined,
        cursor: queryCursor.cursor,
        limit: 25,
      }).then(response => response.data),
    [queryStatus, queryModelId, queryCursor.cursor],
  )
  const visibleQueries = (queries.data?.items ?? []).filter(query => {
    if (!debouncedSearch.trim()) return true
    const needle = debouncedSearch.trim().toLowerCase()
    return `${query.question_title ?? ''}${query.id}${query.user_ref ?? ''}`.toLowerCase().includes(needle)
  })

  const loadModels = async (cursor: string | null = null, append = false) => {
    setModelsLoading(true)
    setModelsError(null)
    try {
      const response = await listQuestionModels({ cursor, limit: 100 })
      setModels(current => (append ? [...current, ...response.data.items] : response.data.items))
      setModelCursor(response.data.next_cursor)
    } catch (error) {
      setModelsError(error)
    } finally {
      setModelsLoading(false)
    }
  }

  useEffect(() => {
    void loadModels()
    // 模型目录仅在页面进入和显式刷新时读取。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const refreshAll = () => {
    void loadModels()
    usage.reload()
    queries.reload()
  }

  const toggleModel = async (model: QuestionModel) => {
    setSavingId(model.id)
    try {
      await updateQuestionModel(model.id, { enabled: !model.enabled }, model.version)
      notify(model.enabled ? '模型已下架，历史记录不会删除。' : '模型已发布给用户。', 'success')
      await loadModels()
    } catch (error) {
      notify(describeError(error).message, 'error')
    } finally {
      setSavingId(null)
    }
  }

  const rate = usageRate(usage.data)
  const defaultModel = models.find(model => model.is_default) ?? models[0] ?? null
  const pricedModels = models.filter(model => model.pricing !== null)
  const tikuConfigured = pricedModels.length > 0
  const sharedModels = models.filter(model => model.cache_policy.scope === 'shared' && model.cache_policy.allow)
  const cacheTtls = models.map(model => model.cache_policy.ttl_days)
  const allModels = models

  return (
    <>
      <PageHeader
        eyebrow="题库管理"
        title="题库与模型"
        description="发布公开模型、配置价格与精确缓存，并核对 token 用量。"
        actions={
          <>
            <Button onClick={refreshAll} loading={modelsLoading || usage.loading || queries.loading}>
              <RefreshCw size={14} /> 刷新
            </Button>
            {canManage ? (
              <>
                <Button onClick={() => setTestModel(defaultModel)} disabled={!defaultModel}>
                  <TestTube2 size={14} /> 连接测试
                </Button>
                <Button variant="primary" onClick={() => setEditing('new')}>
                  <Plus size={14} /> 新增模型
                </Button>
              </>
            ) : null}
          </>
        }
      />

      <div className="system-strip">
        <span>
          <span className={`service-dot ${tikuConfigured ? 'healthy' : 'degraded'}`} />
          <b>{tikuConfigured ? '已发布模型价格' : '模型价格未配置'}</b>
          <span className="strip-secondary">{pricedModels.length ? `${pricedModels.length} 个模型已定价 · ` : ''}密钥不会在此页面显示</span>
        </span>
        <span className="strip-secondary">缓存命中 token 记为 0；模糊历史检索不算缓存命中</span>
      </div>

      <section className="metric-band question-metrics">
        <div className="metric">
          <div className="metric-label">累计查询</div>
          <div className="metric-value">{formatNumber(usage.data?.queries ?? null)}</div>
          <div className="metric-foot"><span>按模型累计</span></div>
        </div>
        <div className="metric">
          <div className="metric-label">精确缓存命中</div>
          <div className="metric-value">{rate === null ? <span className="muted" style={{ fontSize: 15 }}>未提供</span> : `${rate.toFixed(1)}%`}</div>
          <div className="metric-foot"><span>{usage.data?.cache_hits === null || usage.data?.cache_hits === undefined ? '命中次数未提供' : `${formatNumber(usage.data.cache_hits)} 次命中`}</span></div>
        </div>
        <div className="metric">
          <div className="metric-label">Token 用量</div>
          <div className="metric-value">{formatNumber(usage.data?.total_tokens ?? null)}</div>
          <div className="metric-foot"><span>输入 {formatNumber(usage.data?.input_tokens ?? null)} · 输出 {formatNumber(usage.data?.output_tokens ?? null)}</span></div>
        </div>
        <div className="metric">
          <div className="metric-label">成本状态</div>
          <div className="metric-value" style={{ fontSize: 17 }}>{usageCostLabel(usage.data)}</div>
          <div className="metric-foot">
            {usage.data?.incomplete_queries ? <Badge tone="amber">{usage.data.incomplete_queries} 次用量不完整</Badge> : <span>实际扣费受计费配置约束</span>}
          </div>
        </div>
      </section>

      {usage.error && !usage.data ? (
        <div style={{ marginBottom: 16 }}><ErrorState error={usage.error} onRetry={usage.reload} compact /></div>
      ) : null}

      <Card>
        <CardHeader
          title="公开模型目录"
          subtitle="用户只能选择已启用的公开模型；价格与缓存策略随条目保存。"
          actions={models.length ? <Badge tone="blue">{models.length} 个已加载</Badge> : null}
        />
        {modelsError && models.length ? <RefreshError error={modelsError} onRetry={() => void loadModels()} /> : null}
        {modelsLoading && !models.length ? (
          <LoadingBlock rows={4} />
        ) : modelsError && !models.length ? (
          <div style={{ padding: 18 }}><ErrorState error={modelsError} onRetry={() => void loadModels()} /></div>
        ) : models.length ? (
          <>
            <div className="table-scroll">
              <table className="data-table model-table">
                <thead>
                  <tr>
                    <th>公开条目</th>
                    <th>能力</th>
                    <th>输入 / 输出价格</th>
                    <th>缓存</th>
                    <th>默认</th>
                    <th>可见</th>
                    <th>健康</th>
                    <th aria-label="操作" />
                  </tr>
                </thead>
                <tbody>
                  {models.map(model => {
                    const price = priceLabel(model.pricing)
                    return (
                      <tr key={model.id} className={!model.enabled ? 'model-disabled-row' : ''}>
                        <td>
                          <div className="model-name-cell">
                            <span className="model-icon"><Cpu size={15} /></span>
                            <div>
                              <b>{model.display_name}</b>
                              <small>{providerLabel(model)}{model.description ? ` · ${model.description}` : ''}</small>
                              <small className="mono">{model.id} · v{model.version ?? '—'}</small>
                            </div>
                          </div>
                        </td>
                        <td>
                          <div className="capability-tags">
                            {model.capabilities.map(capability => <span key={capability}>{capabilityLabel(capability)}</span>)}
                          </div>
                        </td>
                        <td>
                          {price.pending ? <span className="unpriced">{price.text}</span> : (
                            <span className="price-cell">
                              {price.text}
                              <small>每百万 token</small>
                            </span>
                          )}
                        </td>
                        <td>
                          <Badge tone={model.cache_policy.allow ? 'green' : 'neutral'}>
                            {model.cache_policy.allow ? (model.cache_policy.scope === 'shared' ? '共享' : '用户') : '关闭'}
                          </Badge>
                          {model.cache_policy.allow ? <small className="cell-sub">{model.cache_policy.ttl_days} 天</small> : null}
                        </td>
                        <td>{model.is_default ? <Badge tone="blue">默认</Badge> : <span className="muted">—</span>}</td>
                        <td>
                          {canManage ? (
                            <Switch
                              checked={model.enabled}
                              label={`${model.display_name} 可见性`}
                              disabled={savingId === model.id}
                              onChange={() => void toggleModel(model)}
                            />
                          ) : (
                            <Badge tone={model.enabled ? 'green' : 'neutral'}>{model.enabled ? '可见' : '已下架'}</Badge>
                          )}
                        </td>
                        <td>{healthBadge(model)}</td>
                        <td>
                          {canManage ? (
                            <div className="model-row-actions">
                              <Button size="sm" variant="ghost" onClick={() => setTestModel(model)}>
                                <TestTube2 size={12} /> 测试
                              </Button>
                              <Button size="sm" variant="ghost" onClick={() => setEditing(model)}>
                                <Pencil size={12} /> 编辑
                              </Button>
                            </div>
                          ) : null}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
            <p className="mobile-scroll-hint">左右滑动查看完整表格</p>
            <div className="model-table-note">
              <ShieldCheck size={12} /> 下架不删除历史；已引用该模型的后续任务会提示重新选择，不静默切换。
              {modelCursor ? (
                <Button size="sm" variant="ghost" onClick={() => void loadModels(modelCursor, true)}>
                  加载更多模型
                </Button>
              ) : null}
            </div>
          </>
        ) : (
          <EmptyState
            title="尚未发布公开模型"
            description="新增模型条目并配置能力、价格与缓存后才能供用户选择。"
            action={<Button variant="primary" onClick={() => setEditing('new')}><Plus size={13} /> 新增模型</Button>}
          />
        )}
      </Card>

      <div className="question-side question-side-wide">
        <Card>
          <CardHeader title="缓存策略" actions={<Badge tone="green">精确匹配</Badge>} />
          <div className="cache-policy-list">
            <div><span>匹配方式</span><b>规范化题目完全一致</b></div>
            <div><span>默认作用域</span><b>{sharedModels.length ? '存在共享缓存模型' : '用户作用域'}</b></div>
            <div><span>共享缓存</span><b>{sharedModels.length ? `${sharedModels.length} 个模型开启` : '未开启'}</b></div>
            <div>
              <span>模型 TTL</span>
              <b>{cacheTtls.length ? (Math.min(...cacheTtls) === Math.max(...cacheTtls) ? `${Math.min(...cacheTtls)} 天` : `${Math.min(...cacheTtls)}–${Math.max(...cacheTtls)} 天`) : '未提供'}</b>
            </div>
            <div><span>缓存命中</span><b>token 记为 0</b></div>
          </div>
          <div className="callout callout-warning" style={{ margin: 16 }}>
            <CircleAlert size={15} />
            <div>共享缓存默认关闭。开启前需要隐私评估、失效策略与用户可见说明；错误缓存可单独失效。</div>
          </div>
        </Card>
        <Card>
          <CardHeader title="token 用量口径" actions={tikuConfigured ? <Badge tone="green">已发布模型价格</Badge> : <Badge tone="amber">价格待公布</Badge>} />
          <div className="cache-policy-list">
            <div><span>输入</span><b>按实际输入 token</b></div>
            <div><span>输出</span><b>按实际输出 token</b></div>
            <div><span>缓存命中</span><b>0 token</b></div>
            <div><span>成本单位</span><b>整数微元</b></div>
            <div><span>不完整用量</span><b>不自动扣费</b></div>
          </div>
          <div className="model-table-note">
            <Sparkles size={12} /> 结算以实际 token 与价格版本为依据；页面展示不参与计费。
          </div>
        </Card>
      </div>

      <Card className="section-gap">
        <CardHeader
          title="查询与用量"
          subtitle="跨用户查询展示模型、作答方式、缓存与 token 分解。"
          actions={
            <Button size="sm" onClick={() => { queries.reload(); usage.reload() }} loading={queries.loading}>
              <RefreshCw size={13} /> 刷新
            </Button>
          }
        />
        {usage.data?.by_model.length ? (
          <div className="usage-by-model">
            <div className="usage-by-model-head">
              <span>模型</span><span>查询</span><span>命中</span><span>输入 token</span><span>输出 token</span><span>成本</span>
            </div>
            {usage.data.by_model.map(row => (
              <div key={row.model_id || row.display_name} className="usage-by-model-row">
                <span>{row.display_name}</span>
                <span>{formatNumber(row.queries)}</span>
                <span>{formatNumber(row.cache_hits)}</span>
                <span>{formatNumber(row.input_tokens)}</span>
                <span>{formatNumber(row.output_tokens)}</span>
                <span>{row.pricing ? (usage.data?.automatic_charging === false ? '未启用扣费' : row.cost_micros ? formatPriceMicros(row.cost_micros) : '未产生扣费') : '价格待公布'}</span>
              </div>
            ))}
          </div>
        ) : null}
        <div className="table-toolbar">
          <div className="table-toolbar-group">
            <SearchFilter value={querySearch} onChange={setQuerySearch} />
            <Select
              label="按状态筛选查询"
              value={queryStatus}
              onChange={setQueryStatus}
              options={[
                { value: '', label: '全部查询状态' },
                { value: 'pending', label: '处理中' },
                { value: 'answered', label: '已回答' },
                { value: 'cache_hit', label: '缓存命中' },
                { value: 'needs_review', label: '待人工复核' },
                { value: 'failed', label: '失败' },
              ]}
            />
            <Select
              label="按模型筛选查询"
              value={queryModelId}
              onChange={setQueryModelId}
              options={[{ value: '', label: '全部模型' }, ...allModels.map(model => ({ value: model.id, label: model.display_name }))]}
            />
          </div>
          {debouncedSearch ? <span className="form-hint">搜索作用于当前页</span> : null}
        </div>
        {queries.error && queries.data ? <RefreshError error={queries.error} onRetry={queries.reload} /> : null}
        {queries.loading && !queries.data ? (
          <LoadingBlock rows={4} />
        ) : queries.error && !queries.data ? (
          <div style={{ padding: 18 }}><ErrorState error={queries.error} onRetry={queries.reload} /></div>
        ) : visibleQueries.length ? (
          <>
            <div className="table-scroll">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>查询 / 题目</th>
                    <th>用户摘要</th>
                    <th>模型</th>
                    <th>作答方式</th>
                    <th>缓存</th>
                    <th>tokens</th>
                    <th>状态</th>
                    <th>时间</th>
                    <th aria-label="操作" />
                  </tr>
                </thead>
                <tbody>
                  {visibleQueries.map(query => (
                    <tr key={query.id}>
                      <td>
                        <button className="cell-button" onClick={() => setQueryDetail(query)}>
                          <span className="cell-stack">
                            <span className="cell-primary">{query.question_title ?? '题目内容已过期或未提供'}</span>
                            {query.content_expired ? <span className="cell-sub">题目与答案已过保留期</span> : null}
                            <span className="cell-sub mono">{query.id}</span>
                          </span>
                        </button>
                      </td>
                      <td className="mono muted">{query.user_ref ?? '未提供'}</td>
                      <td>{query.model_display_name ?? query.model_id ?? '未提供'}</td>
                      <td>{query.mode === 'cache_first' ? '缓存优先' : query.mode === 'always_llm' ? '每次模型' : query.mode ?? '未提供'}</td>
                      <td>{query.cache_hit ? <Badge tone="green">命中 · 0 token</Badge> : <span className="muted">未命中</span>}</td>
                      <td>{formatNumber(query.total_tokens)}</td>
                      <td><Badge tone={queryStatusTone(query.status)}>{query.status_label ?? query.status}</Badge></td>
                      <td className="muted">{formatDateTime(query.created_at)}</td>
                      <td>
                        <IconButton label={`查看查询 ${query.id}`} onClick={() => setQueryDetail(query)}>
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
              hasPrevious={queryCursor.hasPrevious}
              hasNext={Boolean(queries.data?.next_cursor)}
              onPrevious={queryCursor.goPrevious}
              onNext={() => queryCursor.goNext(queries.data?.next_cursor ?? null)}
              page={queryCursor.page}
              info={`当前页 ${visibleQueries.length} 条查询${queries.data?.next_cursor ? '，还有更多记录' : '，已到末页'}`}
              disabled={queries.loading}
            />
          </>
        ) : (
          <EmptyState title="暂无查询记录" description="暂无题库查询记录；未定价时不显示虚假金额或收入。" />
        )}
      </Card>

      <div className="card-footer-link" style={{ border: 0, paddingLeft: 2 }}>
        <ShieldCheck size={12} /> 模型密钥与上游地址不会在此页面显示；公开条目、价格版本、查询事实与用量汇总用于核对。
      </div>

      <ModelEditorDialog
        model={editing}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null)
          void loadModels()
        }}
      />

      <ModelTestDialog
        models={models}
        model={testModel}
        onClose={() => setTestModel(null)}
        onTested={() => {
          usage.reload()
          void loadModels()
        }}
      />

      <QueryDetailDialog
        query={queryDetail}
        onClose={() => setQueryDetail(null)}
        onInvalidated={() => {
          setQueryDetail(null)
          queries.reload()
        }}
        canManage={canManage}
      />
    </>
  )
}

function SearchFilter({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return (
    <label className="search-input">
      <span className="sr-only">搜索当前页查询</span>
      <input
        type="search"
        aria-label="搜索当前页查询"
        value={value}
        onChange={event => onChange(event.target.value)}
        placeholder="筛选当前页题目或查询编号"
      />
      {value ? (
        <button type="button" aria-label="清除搜索" onClick={() => onChange('')}>
          <X size={12} />
        </button>
      ) : null}
    </label>
  )
}

function ModelEditorDialog({
  model,
  onClose,
  onSaved,
}: {
  model: QuestionModel | 'new' | null
  onClose: () => void
  onSaved: () => void
}) {
  const { notify } = useToast()
  const isNew = model === 'new'
  const current = model && model !== 'new' ? model : null
  const [draft, setDraft] = useState<ModelDraft>(() => draftFrom(current))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<unknown | null>(null)
  const [sharedAcknowledged, setSharedAcknowledged] = useState(false)

  useEffect(() => {
    if (model) {
      setDraft(draftFrom(current))
      setError(null)
      setSharedAcknowledged(false)
    }
    // current 由 model 派生，仅需要在打开时重置草稿。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [model])

  if (!model) return null

  const update = <K extends keyof ModelDraft>(key: K, value: ModelDraft[K]) => {
    setDraft(currentDraft => ({ ...currentDraft, [key]: value }))
    if (error) setError(null)
  }

  const toggleCapability = (capability: string) => {
    const next = draft.capabilities.includes(capability)
      ? draft.capabilities.filter(item => item !== capability)
      : [...draft.capabilities, capability]
    update('capabilities', next.includes('text') ? next : ['text', ...next])
  }

  const save = async () => {
    if (!draft.display_name.trim()) {
      setError(new Error('请填写公开名称。'))
      return
    }
    if (!draft.provider_ref.trim()) {
      setError(new Error('请填写模型标识。'))
      return
    }
    if (!draft.capabilities.includes('text')) {
      setError(new Error('模型至少需要文本能力。'))
      return
    }
    const hasPrice = draft.input_price.trim() !== '' || draft.output_price.trim() !== ''
    if (!hasPrice && !isNew && current?.pricing) {
      setError(new Error('已发布价格不能通过留空移除；请填写新价格或保留当前价格。'))
      return
    }
    const inputPrice = draft.input_price.trim() === '' ? null : parseYuanToMicros(draft.input_price)
    const outputPrice = draft.output_price.trim() === '' ? null : parseYuanToMicros(draft.output_price)
    if (hasPrice && (inputPrice === null || outputPrice === null)) {
      setError(new Error('输入和输出价格需同时填写，最多六位小数。'))
      return
    }
    if (hasPrice && !draft.price_version.trim()) {
      setError(new Error('配置价格时必须填写价格版本。'))
      return
    }
    if (hasPrice && !draft.rule.trim()) {
      setError(new Error('配置价格时必须填写结算规则。'))
      return
    }
    const ttl = Number(draft.cache_ttl_days)
    if (!Number.isSafeInteger(ttl) || ttl < 1 || ttl > 30) {
      setError(new Error('缓存 TTL 必须在 1 到 30 天之间。'))
      return
    }
    const sortOrder = draft.sort_order.trim() === '' ? null : Number(draft.sort_order)
    if (sortOrder !== null && !Number.isSafeInteger(sortOrder)) {
      setError(new Error('排序值必须是整数。'))
      return
    }
    if (draft.is_default && !draft.enabled) {
      setError(new Error('已下架的模型不能设为默认。'))
      return
    }
    if (draft.cache_allow && draft.cache_scope === 'shared' && !sharedAcknowledged) {
      setError(new Error('共享缓存需要先确认隐私评估与失效方案。'))
      return
    }
    const pricing = hasPrice
      ? {
          version: draft.price_version.trim(),
          input_micros_per_million_tokens: inputPrice,
          output_micros_per_million_tokens: outputPrice,
          micros_per_minor: '10000',
          rounding: draft.rounding,
          rule: draft.rule.trim(),
        }
      : null
    const original = current?.pricing ?? null
    const ratesChanged =
      original !== null &&
      pricing !== null &&
      (original.input_micros_per_million_tokens !== pricing.input_micros_per_million_tokens ||
        original.output_micros_per_million_tokens !== pricing.output_micros_per_million_tokens ||
        original.rounding !== pricing.rounding ||
        original.rule !== pricing.rule ||
        original.micros_per_minor !== pricing.micros_per_minor)
    const pricingChanged =
      isNew ||
      (!original && pricing !== null) ||
      (original !== null &&
        pricing !== null &&
        (ratesChanged || original.version !== pricing.version))
    if (original !== null && ratesChanged && draft.price_version.trim() === original.version) {
      setError(new Error('修改题库费率必须使用新的价格版本；价格版本发布后不可覆盖。'))
      return
    }
    setSaving(true)
    setError(null)
    const payload: Record<string, unknown> = {
      display_name: draft.display_name.trim(),
      description: draft.description.trim(),
      provider_ref: draft.provider_ref.trim(),
      capabilities: draft.capabilities,
      enabled: draft.enabled,
      default: draft.is_default,
      sort_order: sortOrder,
      cache_policy: {
        allow: draft.cache_allow,
        scope: draft.cache_allow ? draft.cache_scope : 'user',
        ttl_days: ttl,
      },
    }
    if (pricingChanged) {
      payload.price_version = pricing?.version ?? ''
      payload.pricing = pricing
    }
    try {
      if (isNew) {
        await createQuestionModel(payload)
        notify('公开模型已创建。', 'success')
      } else if (current) {
        await updateQuestionModel(current.id, payload, current.version)
        notify('模型配置已更新。', 'success')
      }
      onSaved()
    } catch (reason) {
      setError(reason)
      notify(describeError(reason).message, 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      title={isNew ? '新增公开模型' : '编辑公开模型'}
      description="仅公开用户需要的信息；密钥与上游地址不会显示在管理端。保存时会校验条目版本。"
      wide
      actions={
        <>
          <Button onClick={onClose} disabled={saving}>取消</Button>
          <Button variant="primary" onClick={() => void save()} loading={saving}>
            <Check size={14} /> 保存模型
          </Button>
        </>
      }
    >
      <div className="field-row">
        <Field label="公开名称" required>
          <input value={draft.display_name} onChange={event => update('display_name', event.target.value)} placeholder="用户看到的模型名称" maxLength={80} />
        </Field>
        <Field label="模型标识" required hint="填写题库服务中已配置的模型标识，不填写密钥或完整上游地址。">
          <input value={draft.provider_ref} onChange={event => update('provider_ref', event.target.value)} placeholder="provider/model-alias" maxLength={128} />
        </Field>
      </div>
      <Field label="公开说明">
        <input value={draft.description} onChange={event => update('description', event.target.value)} placeholder="简短说明模型适用场景" maxLength={500} />
      </Field>
      <Field label="能力" required hint="当前支持文本与图片；文本能力为必需项。">
        <div className="capability-picker">
          {[{ key: 'text', label: '文本' }, { key: 'vision', label: '图片' }].map(capability => (
            <button
              type="button"
              key={capability.key}
              className={draft.capabilities.includes(capability.key) ? 'selected' : ''}
              aria-pressed={draft.capabilities.includes(capability.key)}
              disabled={capability.key === 'text'}
              onClick={() => toggleCapability(capability.key)}
            >
              {capability.label}
              {draft.capabilities.includes(capability.key) ? <Check size={11} /> : null}
            </button>
          ))}
        </div>
      </Field>
      <div className="field-row">
        <Field label="输入价格（元 / 百万 token）" hint="留空表示未定价；保存为整数微元。">
          <input value={draft.input_price} onChange={event => update('input_price', event.target.value)} inputMode="decimal" placeholder="未定价" />
        </Field>
        <Field label="输出价格（元 / 百万 token）" hint="留空表示未定价；保存为整数微元。">
          <input value={draft.output_price} onChange={event => update('output_price', event.target.value)} inputMode="decimal" placeholder="未定价" />
        </Field>
      </div>
      <div className="field-row">
        <Field label="价格版本" hint="查询会保留当时的版本；修改费率必须填写不同于当前条目的新版本。">
          <input value={draft.price_version} onChange={event => update('price_version', event.target.value)} placeholder="例如 v1" maxLength={64} />
        </Field>
        <Field label="排序">
          <input value={draft.sort_order} onChange={event => update('sort_order', event.target.value)} inputMode="numeric" placeholder="可选" />
        </Field>
      </div>
      {current?.pricing && current.pricing.micros_per_minor !== '10000' ? (
        <div className="callout callout-warning" style={{ marginBottom: 14 }}>
          <CircleAlert size={15} />
          <div>该条目旧价格使用 {current.pricing.micros_per_minor} 微元/分。当前固定换算为 10000；保存费率时会写入新价格版本。</div>
        </div>
      ) : null}
      <div className="field-row">
        <Field label="固定换算" hint="当前固定 10000 微元 = 1 分，不支持其他换算。">
          <input value="10000 微元 / 1 分" readOnly disabled />
        </Field>
        <Field label="取整方式">
          <Select label="价格取整方式" value={draft.rounding} onChange={value => update('rounding', value)} options={[{ value: 'ceil', label: '向上取整' }, { value: 'floor', label: '向下取整' }]} />
        </Field>
      </div>
      <Field label="结算规则" hint="随价格版本保存；查询会冻结当时的规则。">
        <input value={draft.rule} onChange={event => update('rule', event.target.value)} placeholder="token_usage" maxLength={64} />
      </Field>
      <div className="settings-toggle">
        <div><b>允许精确缓存</b><p>只缓存成功、非空且未报错的规范化题目。模糊历史检索不会作为缓存命中。</p></div>
        <Switch label="允许缓存" checked={draft.cache_allow} onChange={value => update('cache_allow', value)} />
      </div>
      <div className="field-row" style={{ marginTop: 14 }}>
        <Field label="缓存作用域">
          <Select label="缓存作用域" value={draft.cache_scope} onChange={value => update('cache_scope', value)} options={[{ value: 'user', label: '当前用户' }, { value: 'shared', label: '跨用户共享' }]} />
        </Field>
        <Field label="缓存 TTL（天）">
          <input value={draft.cache_ttl_days} onChange={event => update('cache_ttl_days', event.target.value)} inputMode="numeric" />
        </Field>
      </div>
      {draft.cache_allow && draft.cache_scope === 'shared' ? (
        <div className="callout callout-warning">
          <CircleAlert size={15} />
          <div>
            <div>共享缓存默认关闭。开启前需要隐私确认、错误传播评估和失效方案。</div>
            <label className="native-check" style={{ marginTop: 8 }}>
              <input type="checkbox" checked={sharedAcknowledged} onChange={event => { setSharedAcknowledged(event.target.checked); if (error) setError(null) }} />
              我确认已完成共享缓存的隐私评估与失效方案
            </label>
          </div>
        </div>
      ) : null}
      <div className="settings-toggle">
        <div><b>保存后可见</b><p>关闭时保留配置但用户不可选择；已引用条目的后续任务会提示重新选择。</p></div>
        <Switch label="模型可见" checked={draft.enabled} onChange={value => update('enabled', value)} />
      </div>
      <div className="settings-toggle">
        <div><b>设为默认</b><p>用户选择“跟随平台默认”时使用。已下架模型不能设为默认。</p></div>
        <Switch label="默认模型" checked={draft.is_default} onChange={value => update('is_default', value)} />
      </div>
      {error ? <div style={{ marginTop: 14 }}><ErrorState error={error} compact /></div> : null}
      <div className="callout" style={{ marginTop: 14 }}>
        <ShieldCheck size={15} />
        <div>价格与缓存设置随公开条目生效；真实扣费还取决于计费配置与支付商户状态，本页面不会自行启用扣费。</div>
      </div>
    </Modal>
  )
}

function ModelTestDialog({
  models,
  model,
  onClose,
  onTested,
}: {
  models: QuestionModel[]
  model: QuestionModel | null
  onClose: () => void
  onTested: () => void
}) {
  const [selectedId, setSelectedId] = useState(model?.id ?? '')
  const [result, setResult] = useState<ModelTestResult | null>(null)
  const [error, setError] = useState<unknown | null>(null)
  const [testing, setTesting] = useState<'reachability' | 'functional' | null>(null)
  const [costConfirmed, setCostConfirmed] = useState(false)

  useEffect(() => {
    if (model) {
      setSelectedId(model.id)
      setResult(null)
      setError(null)
      setCostConfirmed(false)
    }
  }, [model])

  if (!model) return null
  const selected = models.find(item => item.id === selectedId) ?? model

  const run = async (kind: 'reachability' | 'functional') => {
    if (kind === 'functional' && !costConfirmed) {
      setError(new Error('功能性测试可能产生模型费用，请先确认。'))
      return
    }
    setTesting(kind)
    setError(null)
    setResult(null)
    try {
      const response = await testQuestionModel(selected.id, kind)
      setResult(response)
      onTested()
    } catch (reason) {
      setError(reason)
    } finally {
      setTesting(null)
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      title="模型连接测试"
      description="可达性测试与功能性测试分开执行。功能性测试会真实调用模型，可能产生 token 费用。"
      wide
      actions={<Button onClick={onClose}>关闭</Button>}
    >
      <Field label="测试模型">
        <Select label="测试模型" value={selectedId} onChange={setSelectedId} options={models.map(item => ({ value: item.id, label: item.display_name }))} />
      </Field>
      <div className="test-grid">
        <div className="test-card">
          <div className="test-card-heading">
            <span className="workspace-icon"><ShieldCheck size={16} /></span>
            <div><b>可达性测试</b><p>检查题库服务连接，不代表模型实际可答，不产生模型调用。</p></div>
          </div>
          <Button size="sm" onClick={() => void run('reachability')} loading={testing === 'reachability'} disabled={Boolean(testing)}>
            <RefreshCw size={13} /> 测试可达性
          </Button>
        </div>
        <div className="test-card">
          <div className="test-card-heading">
            <span className="workspace-icon"><Sparkles size={16} /></span>
            <div><b>功能性测试</b><p>会实际调用模型并产生 token 费用，结果记录用量。测试不能替代真实答案核验。</p></div>
          </div>
          <label className="native-check" style={{ marginBottom: 10 }}>
            <input type="checkbox" checked={costConfirmed} onChange={event => setCostConfirmed(event.target.checked)} />
            我确认本次功能性测试可能产生费用
          </label>
          <Button size="sm" variant="primary" onClick={() => void run('functional')} loading={testing === 'functional'} disabled={Boolean(testing)}>
            <TestTube2 size={13} /> 运行功能测试
          </Button>
        </div>
      </div>
      {error ? <div style={{ marginTop: 14 }}><ErrorState error={error} compact /></div> : null}
      {result ? (
        <div className="connection-result" role="status">
          <Check size={15} />
          <div>
            <b>{result.status === 'ok' ? '测试已通过' : result.status === 'failed' ? '测试未通过' : '测试结果已返回'}</b>
            <p>
              可达性：{result.reachable === null ? '未提供' : result.reachable ? '通过' : '未通过'}
              {' · '}功能性：{result.functional === null ? '未提供' : result.functional ? '通过' : '未通过'}
              {result.latencyMs !== null ? ` · ${result.latencyMs} ms` : ''}
            </p>
            <span className="form-hint">
              {result.errorCode ? `错误码：${result.errorCode}` : '无错误码'}
            </span>
          </div>
        </div>
      ) : null}
      <div className="callout" style={{ marginTop: 14 }}>
        <CircleAlert size={15} />
        <div>可达性成功不能证明模型答案正确；功能性测试费用按题库服务用量记录处理。</div>
      </div>
    </Modal>
  )
}

function QueryDetailDialog({
  query,
  onClose,
  onInvalidated,
  canManage,
}: {
  query: QuestionQuery | null
  onClose: () => void
  onInvalidated: () => void
  canManage: boolean
}) {
  const { notify } = useToast()
  const [invalidateOpen, setInvalidateOpen] = useState(false)
  const [reason, setReason] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<unknown | null>(null)

  useEffect(() => {
    if (!query) {
      setInvalidateOpen(false)
      setReason('')
      setError(null)
    }
  }, [query])

  if (!query) return null

  const invalidate = async () => {
    if (!reason.trim()) {
      setError(new Error('请填写缓存失效原因。'))
      return
    }
    setSaving(true)
    setError(null)
    try {
      await invalidateQuestionCache({ queryId: query.id, cacheEntryId: query.cache_entry_id, reason: reason.trim() })
      notify('缓存失效请求已提交。', 'success')
      onInvalidated()
    } catch (reasonValue) {
      setError(reasonValue)
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <Modal open={Boolean(query)} onClose={onClose} title="题库查询详情" description={query.id} drawer wide>
        <div className="account-detail-header">
          <span className="workspace-icon" style={{ width: 40, height: 40 }}><Sparkles size={18} /></span>
          <div style={{ flex: 1, minWidth: 0 }}>
            <h2>{query.question_title ?? '题目内容已过期或未提供'}</h2>
            <p>{query.model_display_name ?? query.model_id ?? '未提供模型'} · {query.mode === 'cache_first' ? '缓存优先' : query.mode === 'always_llm' ? '每次模型' : query.mode ?? '未提供作答方式'}</p>
          </div>
          <Badge tone={query.cache_hit ? 'green' : 'neutral'}>{query.cache_hit ? '缓存命中' : '未命中缓存'}</Badge>
        </div>
        <dl className="detail-list">
          <div><dt>用户摘要</dt><dd className="mono">{query.user_ref ?? '未提供'}</dd></div>
          <div><dt>状态</dt><dd>{query.status_label ?? query.status}</dd></div>
          <div><dt>缓存条目</dt><dd className="mono">{query.cache_entry_id ? truncateMiddle(query.cache_entry_id, 10, 8) : '未提供'}</dd></div>
          <div><dt>输入 token</dt><dd>{formatNumber(query.input_tokens)}</dd></div>
          <div><dt>输出 token</dt><dd>{formatNumber(query.output_tokens)}</dd></div>
          <div><dt>总 token</dt><dd>{formatNumber(query.total_tokens)}</dd></div>
          <div><dt>用量完整性</dt><dd>{query.usage_complete ? '完整' : '待核对，不自动扣费'}</dd></div>
          <div><dt>价格状态</dt><dd>{query.pricing_state ? pricingStateLabels[query.pricing_state] ?? query.pricing_state : '未提供'}</dd></div>
          <div><dt>计费状态</dt><dd>{query.billing_state ? billingStateLabels[query.billing_state] ?? query.billing_state : '未提供'}{query.charged ? ' · 已扣费' : ''}</dd></div>
          <div><dt>成本</dt><dd>{query.cost_micros ? formatPriceMicros(query.cost_micros) : '未提供'}</dd></div>
          <div><dt>查询时间</dt><dd>{formatDateTime(query.created_at)}</dd></div>
        </dl>
        {query.answer_preview ? (
          <div className="detail-section">
            <div className="detail-section-title">答案预览</div>
            <div className="answer-preview">{query.answer_preview}</div>
          </div>
        ) : null}
        {query.calls.length ? (
          <div className="detail-section">
            <div className="detail-section-title">调用分解</div>
            <dl className="detail-list">
              {query.calls.map((call, index) => (
                <div key={`${call.slot}-${index}`}>
                  <dt>{call.slot}</dt>
                  <dd>输入 {formatNumber(call.input_tokens)} · 输出 {formatNumber(call.output_tokens)} · 总 {formatNumber(call.total_tokens)}</dd>
                </div>
              ))}
            </dl>
          </div>
        ) : null}
        {query.usage_complete ? null : (
          <div className="callout callout-warning" style={{ marginTop: 14 }}>
            <CircleAlert size={15} />
            <div>用量不完整的查询不会自动扣费，需要人工核对用量记录。</div>
          </div>
        )}
        <div className="detail-actions">
          {canManage && query.cache_hit && query.cache_entry_id ? (
            <Button variant="danger" onClick={() => setInvalidateOpen(true)}><X size={13} /> 标记缓存失效</Button>
          ) : null}
          <Button variant="ghost" onClick={onClose}>关闭</Button>
        </div>
      </Modal>

      <Modal
        open={invalidateOpen}
        onClose={() => setInvalidateOpen(false)}
        title="标记缓存失效"
        description="缓存失效不会删除财务证据或历史查询，只会阻止后续复用该精确缓存。"
        actions={
          <>
            <Button onClick={() => setInvalidateOpen(false)} disabled={saving}>取消</Button>
            <Button variant="danger" onClick={() => void invalidate()} loading={saving}>确认失效</Button>
          </>
        }
      >
        <Field label="失效原因" required hint="会记录操作者、原因和影响范围。">
          <textarea rows={3} value={reason} onChange={event => { setReason(event.target.value); if (error) setError(null) }} maxLength={1000} />
        </Field>
        {error ? <ErrorState error={error} compact /> : null}
      </Modal>
    </>
  )
}
