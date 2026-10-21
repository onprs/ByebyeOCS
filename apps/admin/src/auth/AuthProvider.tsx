import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { getAuthSession, getCapabilities, getMe, login as loginRequest, logout as logoutRequest } from '../api/endpoints'
import type { AdminCapabilities, AdminMe } from '../api/types'
import { clearSessionMemory, subscribeUnauthorized } from '../api/session'

type AuthPhase = 'boot' | 'unauthenticated' | 'authenticated' | 'authentication_disabled' | 'error'

interface AuthContextValue {
  phase: AuthPhase
  capabilities: AdminCapabilities | null
  capabilitiesError: unknown | null
  me: AdminMe | null
  sessionExpired: boolean
  login: (input: { username: string; password: string; totp: string }) => Promise<void>
  logout: () => Promise<void>
  retryBootstrap: () => void
  dismissSessionExpired: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [phase, setPhase] = useState<AuthPhase>('boot')
  const [capabilities, setCapabilities] = useState<AdminCapabilities | null>(null)
  const [capabilitiesError, setCapabilitiesError] = useState<unknown | null>(null)
  const [me, setMe] = useState<AdminMe | null>(null)
  const [sessionExpired, setSessionExpired] = useState(false)
  const [bootstrapNonce, setBootstrapNonce] = useState(0)
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  const bootstrap = useCallback(async () => {
    setPhase('boot')
    setCapabilitiesError(null)
    setMe(null)
    clearSessionMemory()

    const capabilityResult = await getCapabilities()
      .then(response => ({ ok: true as const, data: response.data }))
      .catch(error => ({ ok: false as const, error }))
    if (!mounted.current) return
    if (capabilityResult.ok) setCapabilities(capabilityResult.data)

    const sessionResult = await getAuthSession()
      .then(response => ({ ok: true as const, data: response.data }))
      .catch(error => ({ ok: false as const, error }))
    if (!mounted.current) return

    if (sessionResult.ok) {
      const session = sessionResult.data
      if (session.authenticated) {
        try {
          const meResponse = await getMe()
          if (!mounted.current) return
          setMe(meResponse.data)
          setPhase('authenticated')
          setSessionExpired(false)
          return
        } catch (error) {
          if (!mounted.current) return
          const status = (error as { status?: number }).status
          if (status === 401 || status === 403) {
            setPhase('unauthenticated')
          } else {
            setCapabilitiesError(error)
            setPhase('error')
          }
          return
        }
      }
      if (session.capabilities.adminMethods.length > 0 || capabilityResult.ok && capabilityResult.data.authentication.enabled) {
        setPhase('unauthenticated')
        return
      }
      setPhase('authentication_disabled')
      return
    }

    // 兼容尚未挂载 /auth/session 的旧入口：仅在能力声明认证已启用时探测 /me。
    if (capabilityResult.ok && capabilityResult.data.authentication.enabled) {
      try {
        const meResponse = await getMe()
        if (!mounted.current) return
        setMe(meResponse.data)
        setPhase('authenticated')
        setSessionExpired(false)
      } catch (error) {
        if (!mounted.current) return
        const status = (error as { status?: number }).status
        if (status === 401 || status === 403) {
          setPhase('unauthenticated')
        } else {
          setCapabilitiesError(error)
          setPhase('error')
        }
      }
      return
    }

    if (capabilityResult.ok) {
      setPhase('authentication_disabled')
      return
    }

    setCapabilitiesError(sessionResult.error ?? capabilityResult.error)
    setPhase('error')
  }, [])

  useEffect(() => {
    void bootstrap()
  }, [bootstrap, bootstrapNonce])

  useEffect(
    () =>
      subscribeUnauthorized(() => {
        setMe(null)
        setSessionExpired(true)
        setPhase('unauthenticated')
      }),
    [],
  )

  const login = useCallback(
    async (input: { username: string; password: string; totp: string }) => {
      const result = await loginRequest(input)
      setMe(result.me)
      setSessionExpired(false)
      setPhase('authenticated')
      if (!capabilities) {
        getCapabilities()
          .then(response => {
            if (mounted.current) setCapabilities(response.data)
          })
          .catch(() => undefined)
      }
    },
    [capabilities],
  )

  const logout = useCallback(async () => {
    try {
      await logoutRequest()
    } finally {
      clearSessionMemory()
      setMe(null)
      setPhase('unauthenticated')
      setSessionExpired(false)
    }
  }, [])

  const retryBootstrap = useCallback(() => setBootstrapNonce(value => value + 1), [])
  const dismissSessionExpired = useCallback(() => setSessionExpired(false), [])

  const value = useMemo<AuthContextValue>(
    () => ({
      phase,
      capabilities,
      capabilitiesError,
      me,
      sessionExpired,
      login,
      logout,
      retryBootstrap,
      dismissSessionExpired,
    }),
    [
      phase,
      capabilities,
      capabilitiesError,
      me,
      sessionExpired,
      login,
      logout,
      retryBootstrap,
      dismissSessionExpired,
    ],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext)
  if (!context) throw new Error('缺少认证上下文')
  return context
}
