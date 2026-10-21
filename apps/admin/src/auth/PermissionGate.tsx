import { ShieldOff } from 'lucide-react'
import type { ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from './AuthProvider'
import { hasPermission, landingPath } from './permissions'
import { Button, PageHeader } from '../components'

export function PermissionGate({ permission, children }: { permission: string; children: ReactNode }) {
  const { me } = useAuth()
  const navigate = useNavigate()
  if (me && me.permissions.length > 0 && !hasPermission(me, permission)) {
    return (
      <>
        <PageHeader eyebrow="访客权限" title="没有访问权限" description="当前管理员角色不包含此页面所需的读取权限。" />
        <div className="error-state">
          <span className="error-state-icon"><ShieldOff size={18} /></span>
          <div className="error-state-copy">
            <h3>当前角色无权查看此页面</h3>
            <p>请联系平台所有者确认访问权限。</p>
            <div className="error-actions">
              <Button onClick={() => navigate(landingPath(me), { replace: true })}>返回可访问页面</Button>
            </div>
          </div>
        </div>
      </>
    )
  }
  return <>{children}</>
}
