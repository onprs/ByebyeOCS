import { LoaderCircle, ShieldCheck } from 'lucide-react'
import { Navigate, useLocation } from 'react-router-dom'
import type { ReactNode } from 'react'
import { useAuth } from './AuthProvider'
import { Logo } from '../components/Logo'
import { ErrorState } from '../components/ui'

export function BootScreen({ error, onRetry }: { error?: unknown; onRetry?: () => void }) {
  return (
    <div className="boot-screen">
      <span className="boot-mark">
        <Logo compact />
      </span>
      {error ? (
        <>
          <h1>无法载入管理控制台</h1>
          <p>无法加载管理控制台。</p>
          <ErrorState error={error} onRetry={onRetry} />
        </>
      ) : (
        <>
          <span className="loading-inline">
            <LoaderCircle size={18} className="spinner" />
            正在加载管理控制台
          </span>
          <p style={{ marginTop: 12 }}>
            <ShieldCheck size={12} /> 正在检查会话与认证能力
          </p>
        </>
      )}
    </div>
  )
}

export function RequireAuth({ children }: { children: ReactNode }) {
  const { phase, capabilitiesError, retryBootstrap } = useAuth()
  const location = useLocation()

  if (phase === 'boot') return <BootScreen />
  if (phase === 'error') return <BootScreen error={capabilitiesError} onRetry={retryBootstrap} />
  if (phase === 'unauthenticated' || phase === 'authentication_disabled') {
    return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
  }
  return <>{children}</>
}
