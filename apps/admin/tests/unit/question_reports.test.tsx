import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../src/api/client'
import type { CacheReport } from '../../src/api/questionReports'
import { ToastProvider } from '../../src/components/toast'

const api = vi.hoisted(() => ({ list: vi.fn(), review: vi.fn(), invalidate: vi.fn() }))
vi.mock('../../src/api/questionReports', () => ({ listCacheReports: api.list, reviewCacheReport: api.review }))
vi.mock('../../src/api/endpoints', () => ({ invalidateQuestionCache: api.invalidate }))

import { QuestionReportsPanel } from '../../src/pages/QuestionReportsPanel'

const report: CacheReport = {
  id: 'report-pending', status: 'pending', reason: '答案与本题选项无法对应',
  created_at: '2026-10-22T12:00:00Z', resolved_at: null,
  query: { id: 'qq_report_source', model: { offering_id: 'mdl_review', display_name: '已发布模型' } },
  cache_entry_id: null,
}

function show(canManage = true) {
  return render(<ToastProvider><QuestionReportsPanel canManage={canManage} /></ToastProvider>)
}

describe('用户答案报告处理入口', () => {
  beforeEach(() => {
    api.list.mockResolvedValue({ items: [report], next_cursor: null })
    api.review.mockResolvedValue(undefined)
    api.invalidate.mockResolvedValue({ fork_invalidated: true })
  })

  it('只读账号可查看反馈并切换状态，不能复核报告', async () => {
    const user = userEvent.setup()
    show(false)
    expect(await screen.findByText(report.reason)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: `处理报告 ${report.id}` })).not.toBeInTheDocument()
    await user.selectOptions(screen.getByLabelText('报告状态'), 'resolved')
    await waitFor(() => expect(api.list).toHaveBeenLastCalledWith('resolved', null))
  })

  it('非缓存报告也可结清，失败重试保留同一业务键', async () => {
    const user = userEvent.setup()
    api.review.mockRejectedValueOnce(new ApiError(503, { code: 'service_unavailable', message: '暂时无法保存', retryable: true, scope: 'request', request_id: 'req_fixture' }))
    show()
    await user.click(await screen.findByRole('button', { name: `处理报告 ${report.id}` }))
    const dialog = await screen.findByRole('dialog', { name: '处理答案报告' })
    await user.click(within(dialog).getByRole('button', { name: '保存处理结果' }))
    expect(api.review).not.toHaveBeenCalled()
    await user.type(within(dialog).getByLabelText('核对说明'), '已核对选项并处理该反馈')
    await user.click(within(dialog).getByRole('button', { name: '保存处理结果' }))
    await within(dialog).findByText('暂时无法保存')
    const first = api.review.mock.calls[0]
    expect(first[0]).toBe(report.id)
    expect(first[1]).toEqual({ status: 'resolved', reason: '已核对选项并处理该反馈' })
    await user.click(within(dialog).getByRole('button', { name: '保存处理结果' }))
    await waitFor(() => expect(api.review).toHaveBeenCalledTimes(2))
    expect(api.review.mock.calls[1][2]).toBe(first[2])
    expect(api.invalidate).not.toHaveBeenCalled()
    await waitFor(() => expect(api.list).toHaveBeenCalledTimes(2))
  })

  it('带缓存引用的报告可以使错误缓存失效', async () => {
    const user = userEvent.setup()
    api.list.mockResolvedValue({ items: [{ ...report, cache_entry_id: 'cache_answer_record' }], next_cursor: null })
    show()
    await user.click(await screen.findByRole('button', { name: `失效报告缓存 ${report.id}` }))
    const dialog = await screen.findByRole('dialog', { name: '使报告缓存失效' })
    await user.type(within(dialog).getByLabelText('核对说明'), '已确认缓存答案不适用于当前题目')
    await user.click(within(dialog).getByRole('button', { name: '保存处理结果' }))
    await waitFor(() => expect(api.invalidate).toHaveBeenCalledWith({ queryId: report.query.id, cacheEntryId: 'cache_answer_record', reason: '已确认缓存答案不适用于当前题目' }))
    expect(api.review).not.toHaveBeenCalled()
  })
})
