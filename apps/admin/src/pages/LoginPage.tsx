import { useState } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'
import { AlertCircle, ShieldCheck, UserRound } from 'lucide-react'
import { useAuth } from '../auth/AuthProvider'
import { BootScreen } from '../auth/RequireAuth'
import { Button, Field, Logo } from '../components'
import { describeError } from '../components/ui'

export function LoginPage() {
  const {
    phase,
    capabilitiesError,
    sessionExpired,
    login,
    retryBootstrap,
    dismissSessionExpired,
  } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from ?? '/'
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [totp, setTotp] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<unknown | null>(null)
  const [fieldError, setFieldError] = useState('')

  if (phase === 'boot') return <BootScreen />
  if (phase === 'error') return <BootScreen error={capabilitiesError} onRetry={retryBootstrap} />
  if (phase === 'authenticated') return <Navigate to={from} replace />

  const disabled = phase === 'authentication_disabled'

  const submit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (disabled || submitting) return
    setFieldError('')
    setError(null)
    if (!username.trim()) {
      setFieldError('请输入管理员用户名。')
      return
    }
    if (!password) {
      setFieldError('请输入管理员密码。')
      return
    }
    const normalizedTotp = totp.replace(/\s+/g, '')
    if (!/^\d{6}$/.test(normalizedTotp)) {
      setFieldError('请输入 6 位动态验证码。')
      return
    }
    setSubmitting(true)
    try {
      await login({ username: username.trim(), password, totp: normalizedTotp })
      setPassword('')
      setTotp('')
      navigate(from, { replace: true })
    } catch (reason) {
      setError(reason)
    } finally {
      setSubmitting(false)
    }
  }

  const errorDetails = error ? describeError(error) : null

  return (
    <div className="auth-shell">
      <aside className="auth-aside" aria-label="管理控制台说明">
        <div className="auth-aside-content">
          <Logo />
          <p className="auth-aside-summary">用户、任务与财务管理。</p>
        </div>
      </aside>

      <main className="auth-main">
        <div className="auth-panel">
          <div className="auth-mobile-brand">
            <Logo />
          </div>
          <div className="auth-panel-heading">
            <h2>{disabled ? '管理员认证尚未配置' : '登录管理控制台'}</h2>
            <p>
              {disabled
                ? '管理员认证尚未启用。请由部署维护人员完成管理员账号与验证配置后再登录。'
                : ''}
            </p>
          </div>

          {sessionExpired ? (
            <div className="auth-status">
              <div className="callout callout-warning" role="status">
                <AlertCircle size={16} />
                <div>会话已失效，请重新登录。未保存的操作需要在登录后重新提交。</div>
                <button
                  aria-label="关闭会话提示"
                  className="icon-button"
                  onClick={dismissSessionExpired}
                >
                  ×
                </button>
              </div>
            </div>
          ) : null}

          {disabled ? (
            <div className="auth-status">
              <div className="callout">
                <ShieldCheck size={16} />
                <div>
                  管理员登录暂未启用。请由平台维护人员完成账号与验证配置后再使用。
                </div>
              </div>
              <div className="error-actions u-margin-top-14px" >
                <Button onClick={retryBootstrap}>重新检查服务配置</Button>
              </div>
            </div>
          ) : (
            <form className="auth-form" onSubmit={event => void submit(event)} noValidate>
              <Field label="管理员用户名" htmlFor="admin-username">
                <input
                  id="admin-username"
                  name="username"
                  autoComplete="username"
                  value={username}
                  onChange={event => setUsername(event.target.value)}
                  placeholder="输入管理员用户名"
                  disabled={submitting}
                  autoFocus
                />
              </Field>
              <Field label="密码" htmlFor="admin-password">
                <input
                  id="admin-password"
                  name="password"
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={event => setPassword(event.target.value)}
                  placeholder="输入管理员密码"
                  disabled={submitting}
                />
              </Field>
              <Field
                label="动态验证码"
                htmlFor="admin-totp"
              >
                <input
                  id="admin-totp"
                  name="totp"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  value={totp}
                  onChange={event => setTotp(event.target.value.replace(/\s+/g, '').slice(0, 6))}
                  placeholder="6 位数字"
                  maxLength={6}
                  pattern="[0-9]{6}"
                  disabled={submitting}
                />
              </Field>
              {fieldError ? (
                <p className="form-error" role="alert">
                  {fieldError}
                </p>
              ) : null}
              {errorDetails ? (
                <p className="form-error" role="alert">{errorDetails.message}{errorDetails.retryAfter ? ` · ${errorDetails.retryAfter}` : ''}</p>
              ) : null}
              <Button
                type="submit"
                variant="primary"
                className="btn-block auth-submit"
                loading={submitting}
                disabled={disabled}
              >
                <UserRound size={16} />
                {submitting ? '正在验证' : '登录管理控制台'}
              </Button>
              <div className="auth-footnote">
                密码与动态码仅用于本次登录。
              </div>
            </form>
          )}
        </div>
        <footer className="auth-main-footer">
          © {new Date().getFullYear()} ByebyeOCS · 版权所有 · Created by onprs
        </footer>
      </main>
    </div>
  )
}
