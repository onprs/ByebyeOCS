import { expect, test } from '@playwright/test'
import type { Page } from '@playwright/test'
import { installFakeApi } from './fake-api'
import type { FakeState } from './fake-api'

async function login(page: Page, state: FakeState) {
  await page.goto('/login')
  await expect(page.getByRole('heading', { name: '登录管理控制台' })).toBeVisible()
  await page.getByLabel('管理员用户名').fill('ops')
  await page.getByLabel('密码').fill('correct-horse')
  await page.getByLabel(/动态验证码/).fill('123456')
  await page.getByRole('button', { name: /登录管理控制台/ }).click()
  await expect(page.getByRole('heading', { name: '运行总览' })).toBeVisible()
  expect(state.loggedIn).toBe(true)
}

test.describe('管理员控制台', () => {
  test('登录校验、认证与导航', async ({ page }) => {
    const state = await installFakeApi(page)
    const consoleErrors: string[] = []
    const externalRequests: string[] = []
    page.on('console', message => {
      // 浏览器会把预期的 401 登录失败和 404 打印为资源加载错误；只收集脚本错误。
      if (message.type() === 'error' && !message.text().includes('Failed to load resource')) {
        consoleErrors.push(message.text())
      }
    })
    page.on('request', request => {
      const host = new URL(request.url()).hostname
      if (host !== '127.0.0.1' && host !== 'localhost') externalRequests.push(request.url())
    })

    await page.goto('/login')
    await page.getByRole('button', { name: /登录管理控制台/ }).click()
    await expect(page.getByText('请输入管理员用户名。')).toBeVisible()

    await page.getByLabel('管理员用户名').fill('ops')
    await page.getByLabel('密码').fill('wrong-password')
    await page.getByLabel(/动态验证码/).fill('000000')
    await page.getByRole('button', { name: /登录管理控制台/ }).click()
    await expect(page.getByText('用户名、密码或动态验证码不正确。')).toBeVisible()
    await expect(page.getByRole('alert')).toHaveCount(1)
    await expect(page.getByText(/请求编号 req_e2e_401/)).toHaveCount(0)

    await page.getByLabel('密码').fill('correct-horse')
    await page.getByLabel(/动态验证码/).fill('123456')
    await page.getByRole('button', { name: /登录管理控制台/ }).click()
    await expect(page.getByRole('heading', { name: '运行总览' })).toBeVisible()
    expect(state.loggedIn).toBe(true)

    await expect(page.getByText('实时事件').first()).toBeVisible()
    await expect(page.locator('html')).toHaveAttribute('data-color-scheme', 'graphite')

    for (const [label, heading] of [
      ['用户与账号', '用户与账号'],
      ['任务监控', '任务监控'],
      ['财务与订单', '财务与订单'],
      ['系统设置', '系统设置'],
    ] as const) {
      await page.getByRole('link', { name: label, exact: true }).click()
      await expect(page.getByRole('heading', { name: heading, level: 1 })).toBeVisible()
    }

    const sensitiveText = await page.locator('body').innerText()
    expect(sensitiveText).not.toContain('correct-horse')
    expect(sensitiveText).not.toContain(state.csrf)
    expect(sensitiveText).not.toContain('000000')
    expect(await page.evaluate(() => localStorage.length)).toBe(0)
    expect(consoleErrors).toEqual([])
    expect(externalRequests).toEqual([])
  })

  test('账号筛选、当前页多选、理由确认与任务证据', async ({ page }) => {
    const state = await installFakeApi(page)
    await login(page, state)

    await page.getByRole('link', { name: '用户与账号', exact: true }).click()
    await expect(page.getByRole('heading', { name: '用户与账号', level: 1 })).toBeVisible()
    await page.getByPlaceholder('搜索用户、用户编号或学校…').fill('林')
    await expect(page.getByRole('button', { name: /林 林晓 u_1001/ })).toBeVisible()
    await expect(page.getByText('周明')).toHaveCount(0)

    await page.getByLabel('选择 林晓').check()
    await expect(page.getByText(/已选/)).toContainText('已选 1 个账号')
    await page.getByRole('button', { name: /暂停安排/ }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toContainText('操作范围：1 个显式选择的账号')
    await dialog.getByRole('button', { name: '确认暂停' }).click()
    await expect(dialog.getByText('请填写操作原因，便于后续审计。')).toBeVisible()
    await dialog.getByLabel(/操作原因/).fill('会话异常，暂停后续调度')
    await dialog.getByRole('button', { name: '确认暂停' }).click()
    await expect(dialog.getByText(/已更新 1 个账号/)).toBeVisible()
    expect(state.lastAccountOperation).toMatchObject({
      action: 'pause_plans',
      user_ids: ['u_1001'],
      reason: '会话异常，暂停后续调度',
    })
    await dialog.getByRole('button', { name: '完成' }).click()

    await page.getByRole('button', { name: '查看 林晓 详情' }).click()
    const userDrawer = page.getByRole('dialog')
    await expect(userDrawer.getByText('钱包与余额')).toBeVisible()
    await expect(userDrawer.getByText('w_1001')).toBeVisible()
    await expect(userDrawer.getByText('¥2,982.50')).toBeVisible()
    await userDrawer.getByRole('button', { name: /查看钱包流水/ }).click()
    await expect(page.getByRole('heading', { name: '财务与订单', level: 1 })).toBeVisible()
    await expect(page.getByText('充值到账')).toBeVisible()

    await page.getByRole('link', { name: '任务监控', exact: true }).click()
    await expect(page.getByRole('heading', { name: '任务监控', level: 1 })).toBeVisible()
    await page.getByRole('button', { name: '查看任务 设计思维章节任务' }).click()
    const taskDrawer = page.getByRole('dialog')
    await expect(taskDrawer.getByText('执行观察', { exact: true })).toBeVisible()
    await expect(taskDrawer.getByText('平台确认依据', { exact: true })).toBeVisible()
    await expect(taskDrawer.getByText('媒体记录', { exact: true })).toBeVisible()
    await expect(taskDrawer.getByText('无播放记录')).toBeVisible()
    await expect(taskDrawer.getByText('尚未确认')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(taskDrawer).toBeHidden()

    await page.getByRole('tab', { name: '已确认' }).click()
    await expect(page.getByText('高等数学练习')).toBeVisible()
    await expect(page.getByText('设计思维章节任务')).toHaveCount(0)
  })

  test('外观偏好跨设备写入，刷新后保持', async ({ page }) => {
    const state = await installFakeApi(page)
    const preferenceRequests: Array<{ method: string; ifMatch: string | null; csrf: string | null }> = []
    page.on('request', request => {
      if (request.url().includes('/me/preferences') && request.method() === 'PATCH') {
        preferenceRequests.push({
          method: request.method(),
          ifMatch: request.headers()['if-match'] ?? null,
          csrf: request.headers()['x-csrf-token'] ?? null,
        })
      }
    })

    await login(page, state)
    await page.getByRole('link', { name: '系统设置', exact: true }).click()
    await expect(page.getByRole('heading', { name: '系统设置', level: 1 })).toBeVisible()
    await page.getByRole('button', { name: /青禾/ }).click()
    await expect(page.locator('html')).toHaveAttribute('data-color-scheme', 'grove')
    await expect.poll(() => preferenceRequests.length).toBe(1)
    await page.getByRole('button', { name: /^深色/ }).click()
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')

    await expect.poll(() => preferenceRequests.length).toBeGreaterThanOrEqual(2)
    expect(preferenceRequests[0].ifMatch).toBe('"v7"')
    expect(preferenceRequests[0].csrf).toBe(state.csrf)
    expect(state.preferences).toMatchObject({ color_scheme: 'grove', theme_mode: 'dark' })

    await page.reload()
    await expect(page.getByRole('heading', { name: '系统设置', level: 1 })).toBeVisible()
    await expect(page.locator('html')).toHaveAttribute('data-color-scheme', 'grove')
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
    await expect(page.getByRole('button', { name: /青禾/ })).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByRole('button', { name: /^深色/ })).toHaveAttribute('aria-pressed', 'true')
  })

  test('窄屏导航、表格与页面宽度', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    const state = await installFakeApi(page)
    await login(page, state)
    await expect(page.getByRole('button', { name: '展开导航' })).toBeVisible()
    await page.getByRole('button', { name: '展开导航' }).click()
    await page.getByRole('link', { name: '用户与账号', exact: true }).click()
    await expect(page.getByRole('heading', { name: '用户与账号', level: 1 })).toBeVisible()
    const noOverflow = await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth + 1,
    )
    expect(noOverflow).toBe(true)
    await expect(page.getByText('林晓')).toBeVisible()
    await page.getByLabel('选择 林晓').check()
    await expect(page.getByText(/已选/)).toContainText('已选 1 个账号')
  })

  test('429 限流反馈与重试', async ({ page }) => {
    const state = await installFakeApi(page)
    await login(page, state)
    await page.route('**/admin/api/v1/users*', route =>
      route.fulfill({
        status: 429,
        headers: { 'Content-Type': 'application/json', 'Retry-After': '30', 'X-Request-ID': 'req_e2e_429' },
        body: JSON.stringify({
          error: {
            code: 'rate_limited',
            message: '请求过于频繁，请稍后再试。',
            request_id: 'req_e2e_429',
            retryable: true,
            scope: 'request',
          },
        }),
      }),
    )
    await page.getByRole('link', { name: '用户与账号', exact: true }).click()
    await expect(page.getByRole('heading', { name: '请求过于频繁' })).toBeVisible()
    await expect(page.getByText('请求编号 req_e2e_429')).toBeVisible()
    await expect(page.getByText('30 秒后可重试')).toBeVisible()

    await page.unroute('**/admin/api/v1/users*')
    await page.getByRole('button', { name: '重新加载' }).click()
    await expect(page.getByText('林晓')).toBeVisible()
  })

  test('退出登录清除前端会话并回到登录页', async ({ page }) => {
    const state = await installFakeApi(page)
    await login(page, state)
    await page.getByRole('button', { name: '管理员账户菜单' }).click()
    await page.getByRole('menuitem', { name: '退出登录' }).click()
    await expect(page.getByRole('heading', { name: '登录管理控制台' })).toBeVisible()
    expect(state.loggedIn).toBe(false)
  })
})


test('财务角色落地财务页并隐藏无权限入口', async ({ page }) => {
  const state = await installFakeApi(page, {
    role: 'finance',
    permissions: [
      'admin.self',
      'billing.read',
      'billing.manage',
      'wallet.adjust',
      'wallet.refund',
      'reconciliation.manage',
    ],
  })
  await page.goto('/login')
  await page.getByLabel('管理员用户名').fill('ops')
  await page.getByLabel('密码').fill('correct-horse')
  await page.getByLabel(/动态验证码/).fill('123456')
  await page.getByRole('button', { name: /登录管理控制台/ }).click()
  await expect(page.getByRole('heading', { name: '财务与订单', level: 1 })).toBeVisible()
  expect(state.loggedIn).toBe(true)
  await expect(page.getByRole('link', { name: '运行总览', exact: true })).toHaveCount(0)
  await expect(page.getByRole('link', { name: '财务与订单', exact: true })).toBeVisible()
  await expect(page.getByRole('tab', { name: '对账' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '人工操作与审计' })).toHaveCount(0)
  await page.getByRole('link', { name: '系统设置', exact: true }).click()
  await expect(page.getByRole('button', { name: /计费与价格/ })).toBeVisible()
  await expect(page.getByRole('button', { name: /运行安全/ })).toHaveCount(0)
})


test('财务人工调整与对账表单提交', async ({ page }) => {
  const state = await installFakeApi(page)
  await login(page, state)
  await page.getByRole('link', { name: '财务与订单', exact: true }).click()
  await page.getByRole('button', { name: /人工余额调整/ }).click()
  let dialog = page.getByRole('dialog')
  await dialog.getByLabel('钱包编号').fill('w_1001')
  await dialog.getByLabel('调整金额（元）').fill('10.00')
  await dialog.getByLabel('操作理由').fill('客服复核后补记')
  await dialog.getByRole('checkbox').check()
  await dialog.getByRole('button', { name: '确认并提交' }).click()
  await expect(dialog.getByText(/调整已写入账本/)).toBeVisible()
  await expect(dialog.getByText(/J-ADJ-001/)).toBeVisible()
  await dialog.getByRole('button', { name: '完成' }).click()

  await page.getByRole('tab', { name: '对账' }).click()
  await page.getByRole('button', { name: /发起对账/ }).click()
  dialog = page.getByRole('dialog', { name: '发起对账' })
  await dialog.getByLabel('订单编号').fill('ORD-20261020-001')
  await dialog.getByLabel('对账理由').fill('人工核对订单与账本')
  await dialog.getByRole('button', { name: '提交对账' }).click()
  await expect(dialog.getByText(/对账记录已创建/)).toBeVisible()
  await dialog.getByRole('button', { name: '关闭' }).last().click()

  await page.getByRole('tab', { name: '钱包流水' }).click()
  await page.getByPlaceholder('搜索用户名称或 UUID').fill('林')
  await page.getByRole('button', { name: /林晓/ }).click()
  await expect(page.getByText('充值到账')).toBeVisible()
  await page.getByRole('tab', { name: '退款' }).click()
  await expect(page.getByText('异常订单更正')).toBeVisible()
  await expect(page.locator('tbody tr', { hasText: '异常订单更正' }).getByText('已退款')).toBeVisible()
})

test('管理端不再提供题库查询页，维护入口只指向独立面板', async ({ page }) => {
  const state = await installFakeApi(page)
  await login(page, state)
  await expect(page.getByRole('link', { name: '题库查询', exact: true })).toHaveCount(0)
  await expect(page.getByRole('link', { name: '题库维护面板' })).toHaveAttribute('href', '/panel/')
  await page.goto('/questions')
  await expect(page.getByRole('heading', { name: '页面不存在', level: 1 })).toBeVisible()
  await page.goto('/tasks')
  await expect(page.getByRole('heading', { name: '任务监控', level: 1 })).toBeVisible()
  await expect(page.getByRole('heading', { name: '用户答案报告' })).toBeVisible()
  await expect(page.getByRole('button', { name: '测试', exact: true })).toHaveCount(0)
})


test('页面接口返回 401 时清除内存会话并回到登录页', async ({ page }) => {
  const state = await installFakeApi(page)
  await login(page, state)
  await page.route('**/admin/api/v1/users*', route =>
    route.fulfill({
      status: 401,
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req_e2e_401_session' },
      body: JSON.stringify({
        error: {
          code: 'unauthenticated',
          message: '登录已失效，请重新验证身份',
          request_id: 'req_e2e_401_session',
          retryable: false,
          scope: 'request',
        },
      }),
    }),
  )
  await page.getByRole('link', { name: '用户与账号', exact: true }).click()
  await expect(page.getByRole('heading', { name: '登录管理控制台' })).toBeVisible()
  await expect(page.getByText('会话已失效，请重新登录。')).toBeVisible()
  expect(await page.evaluate(() => localStorage.length)).toBe(0)
})


test('owner 可查看管理员、变更角色并撤销会话', async ({ page }) => {
  const state = await installFakeApi(page)
  await login(page, state)
  await page.getByRole('link', { name: '管理员与角色', exact: true }).click()
  await expect(page.getByRole('heading', { name: '管理员与角色', level: 1 })).toBeVisible()
  const targetRow = page.locator('tr', { hasText: '财务管理员' })
  await expect(targetRow).toBeVisible()
  await targetRow.getByRole('button', { name: '变更角色' }).click()
  let dialog = page.getByRole('dialog', { name: '变更管理员角色' })
  await dialog.getByLabel('新角色').selectOption('observer')
  await dialog.getByLabel('操作理由').fill('调整职责为只读观察')
  await dialog.getByRole('button', { name: '保存角色' }).click()
  await expect(dialog.getByText(/角色已更新为 观察员/)).toBeVisible()
  await dialog.getByRole('button', { name: '完成' }).click()
  const role = state.adminUsers.find(item => item.username === 'finance.ops')
  expect(role?.role).toBe('observer')

  await targetRow.getByRole('button', { name: '撤销会话' }).click()
  dialog = page.getByRole('dialog', { name: '撤销管理员会话' })
  await dialog.getByLabel('操作理由').fill('管理员设备需要重新登录')
  await dialog.getByRole('button', { name: '撤销全部会话' }).click()
  await expect(dialog.getByText(/已撤销 2 个会话/)).toBeVisible()
  const bodyText = await page.locator('body').innerText()
  expect(bodyText).not.toContain('totp_secret')
  expect(bodyText).not.toContain('password_hash')
  expect(bodyText).not.toContain('csrf')
})

test('owner 可复核删除申请并查看资金与运行阻断', async ({ page }) => {
  const state = await installFakeApi(page)
  await login(page, state)
  await page.getByRole('link', { name: '隐私与数据请求', exact: true }).click()
  await expect(page.getByRole('heading', { name: '隐私与数据请求', level: 1 })).toBeVisible()
  const row = page.locator('tr', { hasText: '757b2acc-fb35-4f89-a1d0-13ccbc5c4fbb' })
  await expect(row).toContainText('待复核')
  await row.getByRole('button', { name: '复核' }).click()

  let dialog = page.getByRole('dialog', { name: '复核隐私申请' })
  await dialog.locator('.privacy-actions').getByRole('button', { name: /完成删除/ }).click()
  await dialog.getByLabel('我已核对阻断情况并确认永久关闭该账号').check()
  await dialog.getByLabel('复核理由').fill('核对资金和运行任务后确认删除')
  await dialog.locator('.modal-actions').getByRole('button', { name: '完成删除' }).click()
  await expect(dialog.getByText(/存在未结资金或运行中的活动/)).toBeVisible()
  await expect(dialog.getByText('运行中任务')).toBeVisible()
  await expect(dialog.getByText('2 项')).toBeVisible()
  expect(state.lastPrivacyReview).toMatchObject({ action: 'complete' })
  await dialog.getByRole('button', { name: '取消' }).click()

  await row.getByRole('button', { name: '复核' }).click()
  dialog = page.getByRole('dialog', { name: '复核隐私申请' })
  await dialog.locator('.privacy-actions').getByRole('button', { name: /驳回申请/ }).click()
  await dialog.getByLabel('复核理由').fill('合成测试驳回，保留账号验证重新登录')
  await dialog.locator('.modal-actions').getByRole('button', { name: '驳回申请' }).click()
  await expect(dialog.getByText('申请已驳回')).toBeVisible()
  expect(state.lastPrivacyReview).toMatchObject({ action: 'reject' })
  await dialog.locator('.modal-actions').getByRole('button', { name: '关闭' }).click()
  await expect(row).toContainText('已驳回')
})
