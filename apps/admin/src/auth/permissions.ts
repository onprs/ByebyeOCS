import type { AdminMe } from '../api/types'

export function hasPermission(me: AdminMe | null, permission: string): boolean {
  if (!me) return false
  if (me.permissions.length === 0) return true
  return me.permissions.includes('*') || me.permissions.includes(permission)
}

export function hasAnyPermission(me: AdminMe | null, permissions: string[]): boolean {
  return permissions.some(permission => hasPermission(me, permission))
}

export function landingPath(me: AdminMe | null): string {
  if (!me) return '/login'
  if (hasPermission(me, 'learning.read')) return '/dashboard'
  if (hasPermission(me, 'billing.read')) return '/finance'
  return '/settings'
}
