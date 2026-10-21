import { describe, expect, it } from 'vitest'
import type { AdminMe } from '../../src/api/types'
import { hasPermission, landingPath } from '../../src/auth/permissions'

function admin(permissions: string[]): AdminMe {
  return {
    id: 'adm_1',
    username: 'ops',
    display_name: '管理员',
    role: 'owner',
    permissions,
    totp_enabled: true,
    authenticated_at: null,
    session_expires_at: null,
    fresh: true,
  }
}

describe('管理员权限展示', () => {
  it('owner 通配权限覆盖全部入口', () => {
    expect(hasPermission(admin(['*']), 'billing.manage')).toBe(true)
  })

  it('财务角色落地到财务页而不是学习总览', () => {
    const finance = admin(['admin.self', 'billing.read', 'billing.manage', 'wallet.adjust', 'wallet.refund', 'reconciliation.manage'])
    expect(hasPermission(finance, 'wallet.adjust')).toBe(true)
    expect(hasPermission(finance, 'learning.read')).toBe(false)
    expect(landingPath(finance)).toBe('/finance')
  })

  it('观察员可读审计但不能调整钱包', () => {
    const observer = admin(['admin.self', 'learning.read', 'billing.read', 'question:read', 'operations.read', 'audit.read', 'events.read'])
    expect(hasPermission(observer, 'audit.read')).toBe(true)
    expect(hasPermission(observer, 'wallet.adjust')).toBe(false)
  })

  it('权限缺失时回退到设置页', () => {
    expect(landingPath(admin(['admin.self']))).toBe('/settings')
  })
})
