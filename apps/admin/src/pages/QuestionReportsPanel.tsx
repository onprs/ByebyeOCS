import { useRef, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { newIdempotencyKey } from '../api/client'
import { invalidateQuestionCache } from '../api/endpoints'
import { listCacheReports, reviewCacheReport } from '../api/questionReports'
import type { CacheReport, CacheReportStatus } from '../api/questionReports'
import {
  Badge, Button, Card, CardHeader, EmptyState, ErrorState, Field,
  LoadingBlock, Modal, Pagination, RefreshError, Select, useToast,
} from '../components'
import { formatDateTime } from '../lib/format'
import { useAsync, useCursorStack } from '../lib/hooks'

const statusLabels: Record<CacheReportStatus, string> = { pending: '待处理', resolved: '已处理', rejected: '已驳回' }

export function QuestionReportsPanel({ canManage }: { canManage: boolean }) {
  const [status, setStatus] = useState<CacheReportStatus>('pending')
  const pagination = useCursorStack(status)
  const reports = useAsync(() => listCacheReports(status, pagination.cursor), [status, pagination.cursor], { resetOnDepsChange: true })
  const [selected, setSelected] = useState<{ report: CacheReport; invalidate: boolean } | null>(null)

  return (
    <>
      <Card className="section-gap">
        <CardHeader
          title="用户答案报告"
          subtitle="查看用户反馈，记录核对结果或使相关缓存失效。"
          actions={<Button size="sm" onClick={reports.reload} loading={reports.loading}><RefreshCw size={13} /> 刷新报告</Button>}
        />
        <div className="table-toolbar">
          <Select label="报告状态" value={status} onChange={value => setStatus(value as CacheReportStatus)} options={Object.entries(statusLabels).map(([value, label]) => ({ value, label }))} />
        </div>
        {reports.error && reports.data ? <RefreshError error={reports.error} onRetry={reports.reload} /> : null}
        {reports.loading && !reports.data ? <LoadingBlock rows={3} /> : reports.error && !reports.data ? (
          <div style={{ padding: 18 }}><ErrorState error={reports.error} onRetry={reports.reload} /></div>
        ) : reports.data?.items.length ? (
          <>
            <div className="table-scroll">
              <table className="data-table">
                <thead><tr><th>模型与查询</th><th>用户反馈</th><th>时间</th><th>状态</th><th aria-label="报告操作" /></tr></thead>
                <tbody>
                  {reports.data.items.map(report => (
                    <tr key={report.id}>
                      <td><b>{report.query.model.display_name}</b><small className="cell-sub mono">{report.query.id}</small></td>
                      <td style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', maxWidth: 360 }}>{report.reason}</td>
                      <td>{formatDateTime(report.created_at)}</td>
                      <td><Badge tone={report.status === 'pending' ? 'amber' : 'neutral'}>{statusLabels[report.status]}</Badge></td>
                      <td>
                        {canManage && report.status === 'pending' ? (
                          <div className="model-row-actions">
                            <Button size="sm" variant="ghost" aria-label={`处理报告 ${report.id}`} onClick={() => setSelected({ report, invalidate: false })}>记录结果</Button>
                            {report.cache_entry_id ? <Button size="sm" variant="ghost" aria-label={`失效报告缓存 ${report.id}`} onClick={() => setSelected({ report, invalidate: true })}>使缓存失效</Button> : null}
                          </div>
                        ) : null}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <Pagination page={pagination.page} hasPrevious={pagination.hasPrevious} hasNext={Boolean(reports.data.next_cursor)} info={`当前页 ${reports.data.items.length} 条报告`} onPrevious={pagination.goPrevious} onNext={() => pagination.goNext(reports.data?.next_cursor ?? null)} disabled={reports.loading} />
          </>
        ) : <EmptyState title="暂无此状态的报告" description="用户提交的答案问题会在这里显示。" />}
      </Card>
      {selected ? <ReportReviewDialog key={`${selected.report.id}/${selected.invalidate}`} {...selected} onClose={() => setSelected(null)} onSaved={() => { setSelected(null); reports.reload() }} /> : null}
    </>
  )
}

function ReportReviewDialog({ report, invalidate, onClose, onSaved }: { report: CacheReport; invalidate: boolean; onClose: () => void; onSaved: () => void }) {
  const [status, setStatus] = useState<'resolved' | 'rejected'>('resolved')
  const [reason, setReason] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [validation, setValidation] = useState('')
  const request = useRef<{ signature: string; key: string } | null>(null)
  const { notify } = useToast()

  async function save() {
    const text = reason.trim()
    if ([...text].length < 5 || [...text].length > 500) {
      setValidation('请填写 5–500 字的核对说明。')
      return
    }
    const payload = { status, reason: text }
    const signature = JSON.stringify([report.id, payload])
    if (request.current?.signature !== signature) request.current = { signature, key: newIdempotencyKey() }
    setValidation('')
    setError(null)
    setSaving(true)
    try {
      if (invalidate) {
        await invalidateQuestionCache({ queryId: report.query.id, cacheEntryId: report.cache_entry_id, reason: text })
        notify('已处理缓存失效请求及相关报告。', 'success')
      } else {
        await reviewCacheReport(report.id, payload, request.current.key)
        notify('报告处理结果已保存。', 'success')
      }
      onSaved()
    } catch (failure) {
      setError(failure)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal open onClose={() => { if (!saving) onClose() }} title={invalidate ? '使报告缓存失效' : '处理答案报告'} actions={<><Button onClick={onClose} disabled={saving}>取消</Button><Button variant="primary" onClick={() => void save()} loading={saving}>保存处理结果</Button></>}>
      <div className="form-stack">
        <p>{report.reason}</p>
        {invalidate ? <p>后续新查询将重新获取答案，待处理的关联报告会标记为已处理。</p> : (
          <Select label="处理结果" value={status} onChange={value => setStatus(value as 'resolved' | 'rejected')} disabled={saving} options={[{ value: 'resolved', label: '已处理' }, { value: 'rejected', label: '驳回报告' }]} />
        )}
        <Field label="核对说明" hint="请填写处理依据或采取的操作。">
          <textarea aria-label="核对说明" rows={4} maxLength={1000} value={reason} disabled={saving} onChange={event => setReason(event.target.value)} />
        </Field>
        {validation ? <p role="alert" className="field-error">{validation}</p> : null}
        {error ? <ErrorState error={error} compact /> : null}
      </div>
    </Modal>
  )
}
