import { useEffect, useState } from 'react'
import {
  CircleAlert,
  KeyRound,
  RefreshCw,
  ShieldCheck,
  UserCog,
  Users,
  X,
} from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { changeAdminRole, listAdminUsers, revokeAdminSessions } from '../api/endpoints'
import { ApiError } from '../api/client'
import type { AdminRoleChangeResult, AdminSessionsRevokeResult, AdminUserRecord, AdminUsersDirectory } from '../api/types'
import { useAuth } from '../auth/AuthProvider'
import {
  Avatar,
  Badge,
  Button,
  Card,
  CardHeader,
  EmptyState,
  ErrorState,
  Field,
  LoadingBlock,
  Modal,
  PageHeader,
  RefreshError,
  Select,
  StepUpDialog,
  useToast,
} from '../components'
import { useAsync } from '../lib/hooks'
import { formatDateTime } from '../lib/format'

const roleLabels: Record<string, string> = {
  owner: '所有者',
  operator: '运营',
  support: '支持',
  finance: '财务',
  observer: '观察员',
}

function isStepUpRequired(error: unknown): boolean {
  return error instanceof ApiError && error.status === 403 && error.code === 'step_up_required'
}

function roleLabel(role: string): string {
  return roleLabels[role] ?? role
}

export function AdministratorsPage() {
  const { me, logout } = useAuth()
  const navigate = useNavigate()
  const { notify } = useToast()
  const users = useAsync(() => listAdminUsers().then(response => response.data), [])
  const [stepUpOpen, setStepUpOpen] = useState(false)
  const [editing, setEditing] = useState<AdminUserRecord | null>(null)
  const [revoking, setRevoking] = useState<AdminUserRecord | null>(null)
  const directory: AdminUsersDirectory | null = users.data

  const handleStepUpSuccess = () => {
    users.reload()
  }

  const finishSessionRevoked = async () => {
    notify('当前管理员会话已被撤销，请重新登录。', 'warning')
    try {
      await logout()
    } finally {
      navigate('/login', { replace: true })
    }
  }

  return (
    <>
      <PageHeader
        title="管理员与角色"

        actions={
          <Button onClick={() => users.reload()} loading={users.loading}>
            <RefreshCw size={14} /> 刷新
          </Button>
        }
      />

      <div className="system-strip">
        <span>
          <ShieldCheck size={14} />
          <b>所有者专属入口</b>
          <span className="strip-secondary">管理员账号由平台维护；本页面不展示密码、动态验证码或会话令牌</span>
        </span>
      </div>

      <CalloutText />

      {users.loading && !users.data ? <LoadingBlock rows={5} /> : null}
      {users.error && !users.data ? (
        isStepUpRequired(users.error) ? (
          <div className="error-state not-configured">
            <span className="error-state-icon"><KeyRound size={16} /></span>
            <div className="error-state-copy">
              <h3>需要重新验证管理员身份</h3>
              <p>查看管理员需要再次验证身份，当前验证已过期。</p>
              <div className="error-actions">
                <Button size="sm" onClick={() => setStepUpOpen(true)}>重新验证身份</Button>
              </div>
            </div>
          </div>
        ) : (
          <ErrorState error={users.error} onRetry={users.reload} />
        )
      ) : null}
      {users.error && users.data ? <RefreshError error={users.error} onRetry={users.reload} /> : null}

      {directory ? (
        <>
          <Card>
            <CardHeader
              title="管理员账号"

              actions={<Badge tone="blue"><Users size={14} /> {directory.admin_users.length} 个账号</Badge>}
            />
            {directory.admin_users.length ? (
              <div className="table-scroll">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>管理员</th>
                      <th>角色</th>
                      <th>双重验证</th>
                      <th>状态</th>
                      <th>版本</th>
                      <th>最近更新</th>
                      <th aria-label="操作" />
                    </tr>
                  </thead>
                  <tbody>
                    {directory.admin_users.map((admin, index) => (
                      <tr key={admin.id}>
                        <td>
                          <div className="cell-button">
                            <Avatar name={admin.display_name || admin.username} index={index} small />
                            <span className="cell-stack">
                              <span className="cell-primary">{admin.display_name || admin.username}</span>
                              <span className="cell-sub mono">{admin.username} · {admin.id}</span>
                              {admin.id === me?.id ? <span className="cell-sub">当前管理员</span> : null}
                            </span>
                          </div>
                        </td>
                        <td><Badge tone={admin.role === 'owner' ? 'violet' : 'blue'}>{roleLabel(admin.role)}</Badge></td>
                        <td><Badge tone={admin.totp_enabled ? 'green' : 'amber'}>{admin.totp_enabled ? '已启用' : '未启用'}</Badge></td>
                        <td><Badge tone={admin.disabled ? 'red' : 'green'} dot>{admin.disabled ? '已停用' : '正常'}</Badge></td>
                        <td className="mono">v{admin.version}</td>
                        <td className="muted">{formatDateTime(admin.updated_at)}</td>
                        <td>
                          <div className="model-row-actions">
                            <Button size="sm" variant="ghost" onClick={() => setEditing(admin)} disabled={admin.disabled}>
                              <UserCog size={14} /> 变更角色
                            </Button>
                            <Button size="sm" variant="ghost" onClick={() => setRevoking(admin)}>
                              <X size={14} /> 撤销会话
                            </Button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <EmptyState title="暂无管理员记录" description="尚未添加管理员账号。" />
            )}

          </Card>

          <Card className="section-gap">
            <CardHeader
              title="角色与权限"

            />
            <div className="role-permission-grid">
              {directory.roles.map(role => (
                <div className="role-permission-card" key={role}>
                  <div className="role-permission-heading">
                    <Badge tone={role === 'owner' ? 'violet' : 'blue'}>{roleLabel(role)}</Badge>
                    <span className="mono">{role}</span>
                  </div>
                  <div className="role-permission-tags">
                    {(directory.role_permissions[role] ?? []).map(permission => (
                      <span className="tag" key={permission}>{permission === '*' ? '全部权限' : permission}</span>
                    ))}
                  </div>
                </div>
              ))}
            </div>

          </Card>
        </>
      ) : null}

      <StepUpDialog
        open={stepUpOpen}
        onClose={() => setStepUpOpen(false)}
        onSuccess={handleStepUpSuccess}
      />

      <RoleChangeDialog
        admin={editing}
        roles={directory?.roles ?? []}
        onClose={() => setEditing(null)}
        onSaved={() => users.reload()}
        onSessionRevoked={() => void finishSessionRevoked()}
        onStepUpNeeded={() => setStepUpOpen(true)}
      />

      <RevokeSessionsDialog
        admin={revoking}
        onClose={() => setRevoking(null)}
        onSaved={() => users.reload()}
        onSessionRevoked={() => void finishSessionRevoked()}
        onStepUpNeeded={() => setStepUpOpen(true)}
      />
    </>
  )
}

function CalloutText() {
  return (
    <div className="settings-body u-padding-0-0-16px" >
      <div className="callout">
        <KeyRound size={16} />
        <div>只有所有者可以查看和变更管理员；敏感操作需要再次验证。密码与动态验证码不会在列表、弹窗或审计内容中显示。</div>
      </div>
    </div>
  )
}

function RoleChangeDialog({
  admin,
  roles,
  onClose,
  onSaved,
  onSessionRevoked,
  onStepUpNeeded,
}: {
  admin: AdminUserRecord | null
  roles: string[]
  onClose: () => void
  onSaved: () => void
  onSessionRevoked: () => void
  onStepUpNeeded: () => void
}) {
  const { notify } = useToast()
  const [role, setRole] = useState('')
  const [reason, setReason] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<unknown | null>(null)
  const [result, setResult] = useState<AdminRoleChangeResult | null>(null)

  useEffect(() => {
    if (admin) {
      setRole(admin.role)
      setReason('')
      setError(null)
      setResult(null)
    }
  }, [admin])

  if (!admin) return null

  const submit = async () => {
    if (role === admin.role) {
      setError(new Error('新角色与当前角色相同。'))
      return
    }
    if (reason.trim().length < 4) {
      setError(new Error('操作理由至少需要 4 个字符。'))
      return
    }
    setSaving(true)
    setError(null)
    try {
      const response = await changeAdminRole(
        admin.id,
        { role, reason: reason.trim() },
        admin.version,
      )
      setResult(response.data)
      notify('管理员角色已更新。', 'success')
      onSaved()
      if (response.data.current_session_revoked) onSessionRevoked()
    } catch (reasonValue) {
      setError(reasonValue)
      if (isStepUpRequired(reasonValue)) onStepUpNeeded()
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      title="变更管理员角色"
      description={`${admin.display_name || admin.username} · 当前版本 v${admin.version}`}
      actions={
        result ? (
          <Button variant="primary" onClick={onClose}>完成</Button>
        ) : (
          <>
            <Button onClick={onClose} disabled={saving}>取消</Button>
            <Button variant="primary" onClick={() => void submit()} loading={saving}>保存角色</Button>
          </>
        )
      }
    >
      {result ? (
        <div className="callout callout-success">
          <ShieldCheck size={16} />
          <div>
            角色已更新为 {roleLabel(result.admin_user.role)}。
            {result.sessions_revoked ? ` 已撤销 ${result.sessions_revoked} 个会话。` : ''}
            {result.current_session_revoked ? ' 当前管理员的会话也已撤销。' : ''}
          </div>
        </div>
      ) : (
        <>
          <Field label="新角色" required>
            <Select
              label="新角色"
              value={role}
              onChange={setRole}
              options={roles.map(item => ({ value: item, label: `${roleLabel(item)}（${item}）` }))}
            />
          </Field>
          <Field label="操作理由" required hint="至少 4 个字符，最多 500 个字符；会记录到审计。">
            <textarea rows={3} value={reason} onChange={event => setReason(event.target.value)} maxLength={500} />
          </Field>
          {admin.role === 'owner' ? (
            <div className="callout callout-warning">
              <CircleAlert size={16} />
              <div>移出所有者会改变全部权限；最后一个启用中的所有者不能被移出。</div>
            </div>
          ) : null}
          {error ? (
            <div className="u-margin-top-12px" >
              <ErrorState error={error} compact />
              {isStepUpRequired(error) ? (
                <div className="u-margin-top-8px" >
                  <Button size="sm" onClick={onStepUpNeeded}>重新验证管理员身份</Button>
                </div>
              ) : null}
            </div>
          ) : null}
        </>
      )}
    </Modal>
  )
}

function RevokeSessionsDialog({
  admin,
  onClose,
  onSaved,
  onSessionRevoked,
  onStepUpNeeded,
}: {
  admin: AdminUserRecord | null
  onClose: () => void
  onSaved: () => void
  onSessionRevoked: () => void
  onStepUpNeeded: () => void
}) {
  const { notify } = useToast()
  const [reason, setReason] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<unknown | null>(null)
  const [result, setResult] = useState<AdminSessionsRevokeResult | null>(null)

  useEffect(() => {
    if (admin) {
      setReason('')
      setError(null)
      setResult(null)
    }
  }, [admin])

  if (!admin) return null

  const submit = async () => {
    if (reason.trim().length < 4) {
      setError(new Error('操作理由至少需要 4 个字符。'))
      return
    }
    setSaving(true)
    setError(null)
    try {
      const response = await revokeAdminSessions(admin.id, reason.trim())
      setResult(response)
      notify('管理员会话撤销请求已完成。', 'success')
      onSaved()
      if (response.current_session_revoked) onSessionRevoked()
    } catch (reasonValue) {
      setError(reasonValue)
      if (isStepUpRequired(reasonValue)) onStepUpNeeded()
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      title="撤销管理员会话"
      description={`${admin.display_name || admin.username} · ${admin.username}`}
      actions={
        result ? (
          <Button variant="primary" onClick={onClose}>完成</Button>
        ) : (
          <>
            <Button onClick={onClose} disabled={saving}>取消</Button>
            <Button variant="danger" onClick={() => void submit()} loading={saving}>撤销全部会话</Button>
          </>
        )
      }
    >
      {result ? (
        <div className="callout callout-success">
          <ShieldCheck size={16} />
          <div>
            已撤销 {result.revoked_sessions} 个会话。
            {result.current_session_revoked ? ' 当前管理员会话也已被撤销。' : ''}
          </div>
        </div>
      ) : (
        <>
          <div className="callout callout-warning">
            <CircleAlert size={16} />
            <div>撤销后该管理员需要重新使用密码与动态验证码登录才能继续操作。</div>
          </div>
          <Field label="操作理由" required hint="至少 4 个字符，最多 500 个字符；会记录到审计。">
            <textarea rows={3} value={reason} onChange={event => setReason(event.target.value)} maxLength={500} />
          </Field>
          {error ? (
            <div className="u-margin-top-12px" >
              <ErrorState error={error} compact />
              {isStepUpRequired(error) ? (
                <div className="u-margin-top-8px" >
                  <Button size="sm" onClick={onStepUpNeeded}>重新验证管理员身份</Button>
                </div>
              ) : null}
            </div>
          ) : null}
        </>
      )}
    </Modal>
  )
}
