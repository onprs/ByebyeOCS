import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ToastProvider } from '../../src/components/toast'
import type { TaskDetail, UserAccount } from '../../src/api/types'

const auth = vi.hoisted(() => ({
  me: {
    id: 'adm_1',
    username: 'ops',
    display_name: '运营管理员',
    role: 'owner',
    permissions: ['*'],
    totp_enabled: true,
    authenticated_at: null,
    session_expires_at: null,
    fresh: true,
  },
  capabilities: null,
}))

vi.mock('../../src/auth/AuthProvider', () => ({
  useAuth: () => auth,
}))

const api = vi.hoisted(() => ({
  listUsers: vi.fn(),
  getUser: vi.fn(),
  runAccountOperation: vi.fn(),
  listTasks: vi.fn(),
  getTask: vi.fn(),
  runTaskOperation: vi.fn(),
}))

vi.mock('../../src/api/endpoints', () => ({
  listUsers: api.listUsers,
  getUser: api.getUser,
  runAccountOperation: api.runAccountOperation,
  listTasks: api.listTasks,
  getTask: api.getTask,
  runTaskOperation: api.runTaskOperation,
}))

import { UsersPage } from '../../src/pages/UsersPage'
import { TasksPage } from '../../src/pages/TasksPage'

const userAccount: UserAccount = {
  id: 'u_1001',
  display_name: '林晓',
  school: '晨光大学',
  status: 'valid',
  status_label: '会话有效',
  status_detail: null,
  identity_bound: true,
  identity_namespace: 'chaoxing',
  blocked_code: null,
  blocked_until: null,
  masked_subject: null,
  course_count: 6,
  plan_count: 1,
  task_count: 4,
  incomplete_task_count: 1,
  running_task_count: 1,
  wallet_id: null,
  balance_minor: null,
  cumulative_spend_minor: null,
  account_concurrency: null,
  last_synced_at: '2026-10-20T06:30:00Z',
  last_active_at: '2026-10-20T06:30:00Z',
  created_at: '2026-09-01T02:00:00Z',
}

const taskSummary = {
  id: 'task_1',
  title: '设计思维章节任务',
  task_kind: 'video',
  course_title: '设计思维',
  chapter_title: '第一章',
  user_id: 'u_1001',
  user_display_name: null,
  state: 'running' as const,
  state_label: '执行中',
  progress_fraction: 0.42,
  platform_status: 'unconfirmed',
  updated_at: '2026-10-20T06:31:00Z',
  last_error_code: null,
  attempt_count: 1,
  failure_count: 0,
}

const taskDetail: TaskDetail = {
  ...taskSummary,
  desired_state: 'run',
  version: 7,
  owner_plan_id: 'plan_1',
  observation: {
    fraction: 0.42,
    source: 'executor',
    observed_at: '2026-10-20T06:31:00Z',
    detail: '本地执行器观察',
  },
  platform: { status: 'unconfirmed', passed: false, checked_at: '2026-10-20T06:31:02Z', basis: null, source: null, play_time_seconds: null },
  media_evidence: { available: false, records: [], note: '执行器没有返回媒体解码记录', source: null },
  estimate: { remaining_seconds_min: 120, remaining_seconds_max: 300, estimated: true },
  billing: { state: 'disabled', configured: false, amount_kind: 'none', quoted_amount_minor: '0', currency: 'CNY', price_version: null, error_code: null },
  timeline: [{ at: '2026-10-20T06:30:00Z', title: '任务开始', detail: null, tone: 'info' }],
  can_pause: true,
  can_resume: false,
  can_cancel: true,
  can_reschedule: false,
}

function renderPage(path: string, element: React.ReactNode) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <ToastProvider>
        <Routes>
          <Route path={path.split('?')[0]} element={element} />
        </Routes>
      </ToastProvider>
    </MemoryRouter>,
  )
}

describe('账号与任务页面', () => {
  beforeEach(() => {
    api.listUsers.mockResolvedValue({ data: { items: [userAccount], next_cursor: null }, etag: null })
    api.getUser.mockResolvedValue({
      data: {
        ...userAccount,
        credential_state: '已加密，不可查看',
        session_expires_at: '2026-10-27T06:30:00Z',
        active_plan_count: 1,
        recent_tasks: [taskSummary],
        plans: [],
        courses: [],
        notes: null,
      },
      etag: null,
    })
    api.runAccountOperation.mockResolvedValue({ updated: 1, skipped: 0, requestId: 'req_bulk', items: [] })
    api.listTasks.mockResolvedValue({ data: { items: [taskSummary], next_cursor: null }, etag: null })
    api.getTask.mockResolvedValue({ data: taskDetail, etag: null })
    api.runTaskOperation.mockResolvedValue({ updated: 1, skipped: 0, requestId: 'req_task', items: [] })
  })

  it('当前页选择后提交带理由的批量操作，并显示服务端实际数量', async () => {
    const user = userEvent.setup()
    renderPage('/users', <UsersPage />)
    expect(await screen.findByText('林晓')).toBeInTheDocument()

    await user.click(screen.getByLabelText('选择 林晓'))
    expect(screen.getByText(/已选/)).toHaveTextContent('已选 1 个账号')
    await user.click(screen.getByRole('button', { name: /暂停安排/ }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('操作范围：1 个显式选择的账号。')).toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: '确认暂停' }))
    expect(within(dialog).getByText('请填写操作原因，便于后续审计。')).toBeInTheDocument()
    expect(api.runAccountOperation).not.toHaveBeenCalled()

    await user.type(within(dialog).getByLabelText(/操作原因/), '账号会话异常，先暂停后续调度')
    await user.click(within(dialog).getByRole('button', { name: '确认暂停' }))
    await waitFor(() => {
      expect(api.runAccountOperation).toHaveBeenCalledWith(
        {
          operation: 'pause',
          userIds: ['u_1001'],
          reason: '账号会话异常，先暂停后续调度',
        },
        expect.any(String),
      )
    })
    expect(await within(dialog).findByText(/已更新 1 个账号/)).toBeInTheDocument()
  })

  it('任务详情分别展示执行观察、平台确认和媒体事实', async () => {
    const user = userEvent.setup()
    renderPage('/tasks', <TasksPage />)
    expect(await screen.findByText('设计思维章节任务')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '查看任务 设计思维章节任务' }))
    const drawer = await screen.findByRole('dialog')
    expect(await within(drawer).findByText('执行观察')).toBeInTheDocument()
    expect(within(drawer).getByText('平台确认依据')).toBeInTheDocument()
    expect(within(drawer).getByText('媒体记录')).toBeInTheDocument()
    expect(within(drawer).getByText('无播放记录')).toBeInTheDocument()
    expect(within(drawer).getByText('尚未确认')).toBeInTheDocument()
    expect(within(drawer).getByText(/约 2 分钟/)).toBeInTheDocument()

    await user.click(within(drawer).getByRole('button', { name: /^暂停$/ }))
    const operationDialog = await screen.findByRole('dialog', { name: '暂停所选任务' })
    await user.click(within(operationDialog).getByRole('button', { name: '确认暂停' }))
    expect(within(operationDialog).getByText('请填写操作原因，便于后续审计。')).toBeInTheDocument()
    expect(api.runTaskOperation).not.toHaveBeenCalled()
  })
})
