import { expect, test } from '@playwright/test'
import { installFakeApi } from './fake-api'

test('登录请求进行中保持按钮宽度，并阻止重复提交', async ({ page }) => {
  await installFakeApi(page)
  let release!: () => void
  const pending = new Promise<void>(resolve => { release = resolve })
  await page.route('**/admin/api/v1/auth/login', async route => { await pending; await route.fallback() })
  await page.goto('/login')
  await page.getByLabel('管理员用户名').fill('ops')
  await page.getByLabel('密码').fill('correct-horse')
  await page.getByLabel(/动态验证码/).fill('123456')
  const submit = page.getByRole('button', { name: '登录管理控制台' })
  const before = await submit.boundingBox()
  try {
    await submit.click()
    await expect(submit).toBeDisabled()
    await expect(submit).toHaveAttribute('aria-busy', 'true')
    const during = await submit.boundingBox()
    expect(during!.width).toBeCloseTo(before!.width, 0)
  } finally { release() }
  await expect(page.getByRole('heading', { name: '运行总览', exact: true })).toBeVisible()
})

test('页签支持键盘，减少动效时立即落位，返回页面恢复滚动', async ({ page }) => {
  const state = await installFakeApi(page)
  state.loggedIn = true
  await page.setViewportSize({ width: 1440, height: 650 })
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.goto('/finance')
  const orders = page.getByRole('tab', { name: '充值订单', exact: true })
  const wallet = page.getByRole('tab', { name: '钱包流水', exact: true })
  await orders.focus()
  await page.keyboard.press('ArrowRight')
  await expect(wallet).toHaveAttribute('aria-selected', 'true')
  await expect(wallet).toBeFocused()
  const selected = await wallet.boundingBox()
  const indicator = await page.locator('.tabs > .selection-indicator').boundingBox()
  expect(indicator!.x).toBeCloseTo(selected!.x, 0)
  expect(indicator!.width).toBeCloseTo(selected!.width, 0)
  await page.keyboard.press('ArrowLeft')
  await expect(orders).toHaveAttribute('aria-selected', 'true')
  await expect(page.locator('[aria-busy=true]')).toHaveCount(0)
  await page.evaluate(() => window.scrollTo(0, 240))
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(0)
  const scroll = await page.evaluate(() => window.scrollY)
  await page.getByRole('link', { name: '系统设置', exact: true }).click()
  await expect(page.getByRole('heading', { name: '系统设置', exact: true })).toBeVisible()
  await page.getByRole('link', { name: '财务与订单', exact: true }).click()
  await expect(page.getByRole('heading', { name: '财务与订单', exact: true })).toBeVisible()
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeCloseTo(scroll, 0)
})
