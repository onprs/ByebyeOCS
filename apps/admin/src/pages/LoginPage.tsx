import { useState } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'
import { AlertCircle, KeyRound, LockKeyhole, ShieldCheck, UserRound } from 'lucide-react'
import { useAuth } from '../auth/AuthProvider'
import { BootScreen } from '../auth/RequireAuth'
import { Button, Field, Logo } from '../components'
import { ErrorState, describeError } from '../components/ui'

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
          <div className="auth-aside-copy">
            <div className="auth-aside-kicker">运营控制台</div>
            <h1>
              可信的运营判断，
              <span>从真实状态开始。</span>
            </h1>
            <p>
              管理用户与账号、跟踪任务执行、复核资金与题库用量。每项操作都由独立管理员身份和权限约束。
            </p>
            <ul className="auth-points">
              <li>
                <ShieldCheck size={13} />
                管理员账号认证，密码与动态验证码双重校验
              </li>
              <li>
                <LockKeyhole size={13} />
                登录状态仅保存在当前浏览器的安全会话中
              </li>
              <li>
                <KeyRound size={13} />
                敏感操作提交前再次确认并记录审计
              </li>
            </ul>
          </div>
        </div>
        <div className="auth-aside-footer">BYEBYEOCS ADMIN · 管理控制台</div>
      </aside>

      <main className="auth-main">
        <div className="auth-panel">
          <div className="auth-mobile-brand">
            <Logo />
          </div>
          <div className="auth-panel-heading">
            <div className="eyebrow">管理员登录</div>
            <h2>{disabled ? '管理员认证尚未配置' : '登录管理控制台'}</h2>
            <p>
              {disabled
                ? '管理员认证尚未启用。请由部署维护人员完成管理员账号与验证配置后再登录。'
                : '使用管理员账号登录，需要密码和第二因素动态验证码。'}
            </p>
          </div>

          {sessionExpired ? (
            <div className="auth-status">
              <div className="callout callout-warning" role="status">
                <AlertCircle size={15} />
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
                <ShieldCheck size={15} />
                <div>
                  管理员登录暂未启用。请由平台维护人员完成账号与验证配置后再使用。
                </div>
              </div>
              <div className="error-actions" style={{ marginTop: 14 }}>
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
                hint="输入认证器当前的 6 位验证码。验证码只在本次登录中使用。"
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
                <div className="auth-error">
                  <ErrorState error={error} />
                  {errorDetails.retryAfter ? (
                    <p className="form-hint" style={{ marginTop: 8 }}>
                      {errorDetails.retryAfter}
                    </p>
                  ) : null}
                </div>
              ) : null}
              <Button
                type="submit"
                variant="primary"
                className="btn-block auth-submit"
                loading={submitting}
                disabled={disabled}
              >
                <UserRound size={15} />
                {submitting ? '正在验证' : '登录管理控制台'}
              </Button>
              <div className="auth-footnote">
                <LockKeyhole size={12} />
                <span>
                  密码与动态验证码不会写入浏览器存储，登录状态由安全会话维护。
                </span>
              </div>
            </form>
          )}
        </div>
        <div className="auth-main-footer">
          ByebyeOCS 管理控制台
        </div>
      </main>
    </div>
  )
}
