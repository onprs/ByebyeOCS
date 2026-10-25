import { useEffect, useRef, useState } from 'react'
import {
  ChevronRight,
  CircleAlert,
  ExternalLink,
  RefreshCw,
  ShieldCheck,
  Sparkles,
  X,
} from 'lucide-react'
import {
  getQuestionUsage,
  invalidateQuestionCache,
  listQuestionModels,
  listQuestionQueries,
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
  useToast,
} from '../components'
import { formatDateTime, formatNumber, formatPriceMicros, truncateMiddle } from '../lib/format'
import { openTikuPanel } from '../lib/tikuPanel'
import { QuestionReportsPanel } from './QuestionReportsPanel'
import { useAsync, useCursorStack, useDebouncedValue } from '../lib/hooks'

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

function capabilityLabel(capability: string): string {
  if (capability === 'text') return '文本'
  if (capability === 'vision' || capability === 'image') return '图片'
  return capability
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
  if (!usage) return '—'
  if (usage.cost_state === 'unpriced') return '价格待公布'
  if (usage.automatic_charging === false) return '自动扣费未启用'
  if (usage.cost_micros) return formatPriceMicros(usage.cost_micros)
  return '暂无扣费'
}

function queryStatusTone(status: string): 'green' | 'amber' | 'red' | 'neutral' {
  if (status === 'answered' || status === 'cache_hit') return 'green'
  if (status === 'pending' || status === 'needs_review') return 'amber'
  if (status === 'failed') return 'red'
  return 'neutral'
}

export function QuestionsPage() {
  const { me } = useAuth()
  const canManage = hasPermission(me, 'question:manage')
  const modelsRequestRef = useRef(0)
  const [models, setModels] = useState<QuestionModel[]>([])
  const [modelsLoading, setModelsLoading] = useState(true)
  const [modelsError, setModelsError] = useState<unknown | null>(null)
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

  const loadModels = async () => {
    // 只读目录可能被初次加载与手动刷新同时请求；只允许最后一次结果写入。
    const request = ++modelsRequestRef.current
    setModelsLoading(true)
    setModelsError(null)
    try {
      const response = await listQuestionModels({ limit: 100 })
      if (request !== modelsRequestRef.current) return
      setModels(response.data.items)
    } catch (error) {
      if (request !== modelsRequestRef.current) return
      setModelsError(error)
    } finally {
      if (request === modelsRequestRef.current) setModelsLoading(false)
    }
  }

  useEffect(() => {
    void loadModels()
    // 目录仅在页面进入和显式刷新时读取。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const refreshAll = () => {
    void loadModels()
    usage.reload()
    queries.reload()
  }

  const rate = usageRate(usage.data)
  const pricedModels = models.filter(model => model.pricing !== null)
  const tikuConfigured = pricedModels.length > 0
  const allModels = models

  return (
    <>
      <PageHeader
        title="题库查询与用量"

        actions={
          <>
            <Button onClick={refreshAll} loading={modelsLoading || usage.loading || queries.loading}>
              <RefreshCw size={14} /> 刷新
            </Button>
            <Button variant="primary" onClick={openTikuPanel}>
              <ExternalLink size={14} /> 进入题库面板
            </Button>
          </>
        }
      />

      <div className="system-strip">
        <span>
          <span className={`service-dot ${tikuConfigured ? 'healthy' : 'degraded'}`} />
          <b>{tikuConfigured ? '题库已发布模型价格' : '模型价格未配置'}</b>
          <span className="strip-secondary">{pricedModels.length ? `${pricedModels.length} 个模型已定价 · ` : ''}密钥不会在此页面显示</span>
        </span>
        <span className="strip-secondary">模型与价格由题库面板发布；缓存命中 token 记为 0</span>
      </div>

      <section className="metric-band question-metrics">
        <div className="metric">
          <div className="metric-label">累计查询</div>
          <div className="metric-value">{formatNumber(usage.data?.queries ?? null)}</div>
          <div className="metric-foot"><span>按模型累计</span></div>
        </div>
        <div className="metric">
          <div className="metric-label">精确缓存命中</div>
          <div className="metric-value">{rate === null ? <span className="muted u-font-size-14px" >—</span> : `${rate.toFixed(1)}%`}</div>
          <div className="metric-foot"><span>{usage.data?.cache_hits === null || usage.data?.cache_hits === undefined ? '—' : `${formatNumber(usage.data.cache_hits)} 次命中`}</span></div>
        </div>
        <div className="metric">
          <div className="metric-label">Token 用量</div>
          <div className="metric-value">{formatNumber(usage.data?.total_tokens ?? null)}</div>
          <div className="metric-foot"><span>输入 {formatNumber(usage.data?.input_tokens ?? null)} · 输出 {formatNumber(usage.data?.output_tokens ?? null)}</span></div>
        </div>
        <div className="metric">
          <div className="metric-label">成本状态</div>
          <div className="metric-value u-font-size-16px" >{usageCostLabel(usage.data)}</div>
          <div className="metric-foot">
            {usage.data?.incomplete_queries ? <Badge tone="amber">{usage.data.incomplete_queries} 次用量不完整</Badge> : <span>实际扣费受计费配置约束</span>}
          </div>
        </div>
      </section>

      {usage.error && !usage.data ? (
        <div className="u-margin-bottom-16px" ><ErrorState error={usage.error} onRetry={usage.reload} compact /></div>
      ) : null}

      <Card>
        <CardHeader
          title="模型与价格"

          actions={
            <div className="card-header-actions">
              {models.length ? <Badge tone="blue">{models.length} 个条目</Badge> : null}
              <Button size="sm" onClick={openTikuPanel}><ExternalLink size={14} /> 题库面板</Button>
            </div>
          }
        />
        {modelsError && models.length ? <RefreshError error={modelsError} onRetry={() => void loadModels()} /> : null}
        {modelsLoading && !models.length ? (
          <LoadingBlock rows={3} />
        ) : modelsError && !models.length ? (
          <div className="u-padding-18px" ><ErrorState error={modelsError} onRetry={() => void loadModels()} /></div>
        ) : models.length ? (
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>公开条目</th>
                  <th>能力</th>
                  <th>输入 / 输出价格</th>
                  <th>缓存</th>
                  <th>默认</th>
                  <th>状态</th>
                </tr>
              </thead>
              <tbody>
                {models.map(model => {
                  const price = priceLabel(model.pricing)
                  return (
                    <tr key={model.id} className={!model.enabled ? 'model-disabled-row' : ''}>
                      <td>
                        <div>
                          <b>{model.display_name}</b>
                          <small className="cell-sub mono">{model.id}</small>
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
                        <Badge tone={!model.enabled ? 'neutral' : model.health === 'unavailable' ? 'red' : 'green'}>
                          {!model.enabled ? '已下架' : model.health === 'unavailable' ? '模型不可用' : '已发布'}
                        </Badge>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <EmptyState
            title="题库尚未发布公开模型"
            description="在题库维护面板发布模型、价格与缓存策略后，用户才能选择模型。"
            action={<Button variant="primary" onClick={openTikuPanel}><ExternalLink size={14} /> 打开题库面板</Button>}
          />
        )}
      </Card>

      <div className="question-side question-side-wide">
        <Card>
          <CardHeader title="计费口径" actions={tikuConfigured ? <Badge tone="green">已发布模型价格</Badge> : <Badge tone="amber">价格待公布</Badge>} />
          <div className="cache-policy-list">
            <div><span>输入</span><b>按实际输入 token</b></div>
            <div><span>输出</span><b>按实际输出 token</b></div>
            <div><span>缓存命中</span><b>0 token</b></div>
            <div><span>成本单位</span><b>整数微元</b></div>
            <div><span>不完整用量</span><b>不自动扣费</b></div>
          </div>
          <div className="model-table-note">
            <Sparkles size={14} /> 结算以实际 token 与查询冻结的价格版本为依据；页面展示不参与计费。
          </div>
        </Card>
        <Card>
          <CardHeader title="维护入口" />
          <div className="cache-policy-list">
            <div><span>模型与价格</span><b>题库维护面板</b></div>
            <div><span>准确缓存</span><b>题库维护面板</b></div>
            <div><span>查询与用量</span><b>本页只读核对</b></div>
            <div><span>答案报告</span><b>本页复核</b></div>
          </div>
          <div className="model-table-note">
            <ShieldCheck size={14} /> 题库面板使用独立维护账号；本页不保存密钥或改写价格。
          </div>
        </Card>
      </div>

      <Card className="section-gap">
        <CardHeader
          title="查询与用量"

          actions={
            <Button size="sm" onClick={() => { queries.reload(); usage.reload() }} loading={queries.loading}>
              <RefreshCw size={14} /> 刷新
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
          <div className="u-padding-18px" ><ErrorState error={queries.error} onRetry={queries.reload} /></div>
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
                            <span className="cell-primary">{query.question_title ?? '—'}</span>
                            {query.content_expired ? <span className="cell-sub">题目与答案已过保留期</span> : null}
                            <span className="cell-sub mono">{query.id}</span>
                          </span>
                        </button>
                      </td>
                      <td className="mono muted">{query.user_ref ?? '—'}</td>
                      <td>{query.model_display_name ?? query.model_id ?? '—'}</td>
                      <td>{query.mode === 'cache_first' ? '缓存优先' : query.mode === 'always_llm' ? '每次模型' : query.mode ?? '—'}</td>
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

      <QuestionReportsPanel canManage={canManage} />



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
          <X size={14} />
        </button>
      ) : null}
    </label>
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
          <span className="workspace-icon u-width-40px u-height-40px" ><Sparkles size={16} /></span>
          <div className="u-flex-1 u-min-width-0" >
            <h2>{query.question_title ?? '—'}</h2>
            <p>{query.model_display_name ?? query.model_id ?? '—'} · {query.mode === 'cache_first' ? '缓存优先' : query.mode === 'always_llm' ? '每次模型' : query.mode ?? '—'}</p>
          </div>
          <Badge tone={query.cache_hit ? 'green' : 'neutral'}>{query.cache_hit ? '缓存命中' : '未命中缓存'}</Badge>
        </div>
        <dl className="detail-list">
          <div><dt>用户摘要</dt><dd className="mono">{query.user_ref ?? '—'}</dd></div>
          <div><dt>状态</dt><dd>{query.status_label ?? query.status}</dd></div>
          <div><dt>缓存条目</dt><dd className="mono">{query.cache_entry_id ? truncateMiddle(query.cache_entry_id, 10, 8) : '—'}</dd></div>
          <div><dt>输入 token</dt><dd>{formatNumber(query.input_tokens)}</dd></div>
          <div><dt>输出 token</dt><dd>{formatNumber(query.output_tokens)}</dd></div>
          <div><dt>总 token</dt><dd>{formatNumber(query.total_tokens)}</dd></div>
          <div><dt>用量完整性</dt><dd>{query.usage_complete ? '完整' : '待核对，不自动扣费'}</dd></div>
          <div><dt>价格状态</dt><dd>{query.pricing_state ? pricingStateLabels[query.pricing_state] ?? query.pricing_state : '—'}</dd></div>
          <div><dt>计费状态</dt><dd>{query.billing_state ? billingStateLabels[query.billing_state] ?? query.billing_state : '—'}{query.charged ? ' · 已扣费' : ''}</dd></div>
          <div><dt>成本</dt><dd>{query.cost_micros ? formatPriceMicros(query.cost_micros) : '—'}</dd></div>
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
          <div className="callout callout-warning u-margin-top-14px" >
            <CircleAlert size={16} />
            <div>用量不完整的查询不会自动扣费，需要人工核对用量记录。</div>
          </div>
        )}
        <div className="detail-actions">
          {canManage && query.cache_hit && query.cache_entry_id ? (
            <Button variant="danger" onClick={() => setInvalidateOpen(true)}><X size={14} /> 标记缓存失效</Button>
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
