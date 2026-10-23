import { apiGet, apiRequest } from './client'

export type CacheReportStatus = 'pending' | 'resolved' | 'rejected'

export interface CacheReport {
  id: string
  status: CacheReportStatus
  reason: string
  created_at: string
  resolved_at: string | null
  query: { id: string; model: { offering_id: string; display_name: string } }
  cache_entry_id: string | null
}

export interface CacheReportPage {
  items: CacheReport[]
  next_cursor: string | null
}

export async function listCacheReports(status: CacheReportStatus, cursor: string | null): Promise<CacheReportPage> {
  const params = new URLSearchParams({ status, limit: '20' })
  if (cursor) params.set('cursor', cursor)
  return (await apiGet<CacheReportPage>(`/question-cache-reports?${params}`)).data
}

export async function reviewCacheReport(
  id: string,
  input: { status: Exclude<CacheReportStatus, 'pending'>; reason: string },
  key: string,
): Promise<void> {
  await apiRequest(`/question-cache-reports/${encodeURIComponent(id)}/review`, {
    method: 'POST', body: input, idempotencyKey: key,
  })
}
