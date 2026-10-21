import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { getPreferences, updatePreferences } from '../api/endpoints'
import type { ApiError } from '../api/client'
import type { AdminPreferences, ColorScheme, ThemeMode } from '../api/types'
import { useAuth } from '../auth/AuthProvider'
import { useToast } from '../components/toast'
import { useMediaQuery } from '../lib/hooks'

type PreferenceStatus = 'idle' | 'loading' | 'ready' | 'unavailable' | 'error'

interface AppearanceContextValue {
  preferences: AdminPreferences
  status: PreferenceStatus
  error: unknown | null
  saving: boolean
  effectiveTheme: 'light' | 'dark'
  updateAppearance: (changes: Partial<Pick<AdminPreferences, 'color_scheme' | 'theme_mode'>>) => Promise<boolean>
  reload: () => void
}

const AppearanceContext = createContext<AppearanceContextValue | null>(null)

const systemDefault: AdminPreferences = {
  color_scheme: 'graphite',
  theme_mode: 'system',
  version: 0,
}

export function AppearanceProvider({ children }: { children: ReactNode }) {
  const { phase, capabilities, me } = useAuth()
  const { notify } = useToast()
  const [preferences, setPreferences] = useState<AdminPreferences>(systemDefault)
  const [status, setStatus] = useState<PreferenceStatus>('idle')
  const [error, setError] = useState<unknown | null>(null)
  const [saving, setSaving] = useState(false)
  const [nonce, setNonce] = useState(0)
  const savingRef = useRef(false)
  const systemDark = useMediaQuery('(prefers-color-scheme: dark)')
  const previousUser = useRef<string | null>(null)
  const authenticated = phase === 'authenticated'

  const defaults = useMemo<AdminPreferences>(() => {
    const fallback = capabilities?.appearance
    return {
      color_scheme: fallback?.default_color_scheme ?? 'graphite',
      theme_mode: fallback?.default_theme_mode ?? 'system',
      version: 0,
    }
  }, [capabilities])

  useEffect(() => {
    if (!authenticated) {
      setPreferences(defaults)
      setStatus('idle')
      setError(null)
      previousUser.current = null
      return
    }
    const userKey = me?.id ?? 'authenticated'
    if (previousUser.current === userKey && nonce === 0) return
    previousUser.current = userKey
    let cancelled = false
    setStatus('loading')
    setError(null)
    getPreferences()
      .then(response => {
        if (cancelled) return
        setPreferences(response.data)
        setStatus('ready')
      })
      .catch(reason => {
        if (cancelled) return
        const statusCode = (reason as { status?: number }).status
        if (statusCode === 404 || statusCode === 503) {
          setPreferences(defaults)
          setStatus('unavailable')
        } else {
          setError(reason)
          setStatus('error')
        }
      })
    return () => {
      cancelled = true
    }
    // nonce 用于手动重载，defaults 只在能力变化时更新。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [authenticated, me?.id, nonce, defaults.color_scheme, defaults.theme_mode])

  const effectiveTheme: 'light' | 'dark' =
    preferences.theme_mode === 'system' ? (systemDark ? 'dark' : 'light') : preferences.theme_mode

  useEffect(() => {
    document.documentElement.dataset.colorScheme = preferences.color_scheme
    document.documentElement.dataset.theme = effectiveTheme
    document.documentElement.style.colorScheme = effectiveTheme
  }, [preferences.color_scheme, effectiveTheme])

  useEffect(() => {
    if (!authenticated) {
      setPreferences(defaults)
      document.documentElement.dataset.colorScheme = defaults.color_scheme
      document.documentElement.dataset.theme =
        defaults.theme_mode === 'system' ? (systemDark ? 'dark' : 'light') : defaults.theme_mode
    }
  }, [authenticated, defaults, systemDark])

  const updateAppearance = useCallback(
    async (changes: Partial<Pick<AdminPreferences, 'color_scheme' | 'theme_mode'>>): Promise<boolean> => {
      if (!authenticated) return false
      if (status === 'unavailable') {
        notify('外观偏好同步尚未配置，无法保存到其他设备。', 'warning')
        return false
      }
      if (savingRef.current) return false
      const next: AdminPreferences = { ...preferences, ...changes }
      const previous = preferences
      setPreferences(next)
      setSaving(true)
      savingRef.current = true
      try {
        const response = await updatePreferences(
          { color_scheme: next.color_scheme as ColorScheme, theme_mode: next.theme_mode as ThemeMode },
          preferences.version,
        )
        setPreferences(response.data)
        setStatus('ready')
        setError(null)
        notify('外观偏好已同步。', 'success')
        return true
      } catch (reason) {
        const statusCode = (reason as { status?: number }).status
        setPreferences(previous)
        if (statusCode === 412 || statusCode === 428) {
          setError(reason)
          notify('外观偏好已在其他设备更新，已重新载入最新版本。', 'warning')
          setNonce(value => value + 1)
        } else {
          setError(reason)
          notify((reason as ApiError).message ?? '外观偏好保存失败，已恢复原设置。', 'error')
        }
        return false
      } finally {
        savingRef.current = false
        setSaving(false)
      }
    },
    [authenticated, notify, preferences, status],
  )

  const value = useMemo<AppearanceContextValue>(
    () => ({
      preferences,
      status,
      error,
      saving,
      effectiveTheme,
      updateAppearance,
      reload: () => setNonce(value => value + 1),
    }),
    [preferences, status, error, saving, effectiveTheme, updateAppearance],
  )

  return <AppearanceContext.Provider value={value}>{children}</AppearanceContext.Provider>
}

export function useAppearance(): AppearanceContextValue {
  const context = useContext(AppearanceContext)
  if (!context) throw new Error('缺少外观上下文')
  return context
}

export const colorSchemeOptions: Array<{ value: ColorScheme; label: string; description: string }> = [
  { value: 'graphite', label: '石墨', description: '中性色与低饱和蓝，适合持续监控与批量处理。' },
  { value: 'grove', label: '青禾', description: '暖白底与森林绿，留白更舒展。' },
  { value: 'indigo', label: '靛蓝', description: '墨蓝导航与靛蓝强调，层级更鲜明。' },
]

export const themeModeOptions: Array<{ value: ThemeMode; label: string; description: string }> = [
  { value: 'system', label: '跟随系统', description: '随设备外观自动切换。' },
  { value: 'light', label: '浅色', description: '始终使用浅色界面。' },
  { value: 'dark', label: '深色', description: '始终使用深色界面。' },
]
