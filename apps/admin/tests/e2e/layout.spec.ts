import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { resolve } from 'node:path'
import { installFakeApi } from './fake-api'

// 合成数据覆盖有内容的页面；截图仅在显式启用时写入本地维护目录。
const screenshots = process.env.BYEBYEOCS_UI_SCREENSHOTS === '1'
const directory = resolve(process.cwd(), '../../.local/screenshots/ui-review/admin')

for (const width of [1920, 1440, 1280, 1024, 768, 390]) {
  test(`${width}px 页面与详情保持在视口内，关闭后恢复焦点`, async ({ page }) => {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 })
    const state = await installFakeApi(page)
    state.loggedIn = true
    if (screenshots) await mkdir(directory, { recursive: true })
    for (const route of ['dashboard', 'users', 'tasks', 'finance', 'questions', 'settings']) {
      await page.goto('/' + route)
      await expect(page.locator('main h1')).toBeVisible()
      await expect(page.locator('[aria-busy=true]')).toHaveCount(0)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true)
      if (screenshots) await page.screenshot({ animations: 'disabled', path: resolve(directory, `${route}-${width}.png`) })
    }
    await page.goto('/tasks')
    const opener = page.getByRole('button', { name: '查看任务 设计思维章节任务' })
    await opener.click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByText('平台确认依据', { exact: true })).toBeVisible()
    await dialog.evaluate(async element => {
      await Promise.all(element.getAnimations().map(animation => animation.finished))
    })
    const bounds = await dialog.boundingBox()
    expect(bounds!.x).toBeGreaterThanOrEqual(0)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width + 1)
    if (screenshots) await page.screenshot({ animations: 'disabled', path: resolve(directory, `task-detail-${width}.png`) })
    await page.keyboard.press('Escape')
    await expect(dialog).toBeHidden()
    await expect(opener).toBeFocused()
  })
}

test('三种配色在浅深模式下保留内容与外观选择', async ({ page }) => {
  const state = await installFakeApi(page)
  state.loggedIn = true
  await page.goto('/settings')
  if (screenshots) await mkdir(directory, { recursive: true })
  for (const [scheme, label] of [['graphite', '石墨'], ['grove', '青禾'], ['indigo', '靛蓝']]) {
    await page.getByRole('button', { name: new RegExp(label) }).click()
    for (const [mode, text] of [['light', '浅色'], ['dark', '深色']]) {
      await page.getByRole('button', { name: new RegExp('^' + text) }).click()
      await expect(page.locator('html')).toHaveAttribute('data-color-scheme', scheme)
      await expect(page.locator('html')).toHaveAttribute('data-theme', mode)
      await expect(page.getByRole('heading', { name: '系统设置', exact: true })).toBeVisible()
      if (screenshots) await page.screenshot({ animations: 'disabled', path: resolve(directory, `settings-${scheme}-${mode}.png`) })
    }
  }
})
