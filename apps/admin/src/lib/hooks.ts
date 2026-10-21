import { useCallback, useEffect, useRef, useState } from 'react'

export interface AsyncState<T> {
  data: T | null
  error: unknown | null
  loading: boolean
  reload: () => void
  setData: (data: T | null) => void
}

export function useAsync<T>(
  loader: () => Promise<T>,
  deps: unknown[],
  options: { enabled?: boolean } = {},
): AsyncState<T> {
  const enabled = options.enabled ?? true
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<unknown | null>(null)
  const [loading, setLoading] = useState(enabled)
  const [nonce, setNonce] = useState(0)
  const loaderRef = useRef(loader)
  loaderRef.current = loader
  const depsKey = JSON.stringify(deps ?? [])

  useEffect(() => {
    if (!enabled) {
      setLoading(false)
      return
    }
    let cancelled = false
    setLoading(true)
    setError(null)
    loaderRef
      .current()
      .then(result => {
        if (cancelled) return
        setData(result)
      })
      .catch(reason => {
        if (cancelled) return
        setError(reason)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
    // depsKey 由调用方显式描述依赖，避免匿名 loader 每次渲染触发请求。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [depsKey, enabled, nonce])

  const reload = useCallback(() => setNonce(value => value + 1), [])

  return { data, error, loading, reload, setData }
}

export function useDebouncedValue<T>(value: T, delay = 320): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(value), delay)
    return () => window.clearTimeout(timer)
  }, [value, delay])
  return debounced
}

export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() =>
    typeof window !== 'undefined' ? window.matchMedia(query).matches : false,
  )
  useEffect(() => {
    const media = window.matchMedia(query)
    const update = () => setMatches(media.matches)
    update()
    media.addEventListener('change', update)
    return () => media.removeEventListener('change', update)
  }, [query])
  return matches
}

export function useDocumentTitle(title: string) {
  useEffect(() => {
    if (title) document.title = `${title} · ByebyeOCS 管理控制台`
  }, [title])
}

export function useClickOutside(
  ref: React.RefObject<HTMLElement | null>,
  onOutside: () => void,
  active = true,
) {
  useEffect(() => {
    if (!active) return
    const handlePointer = (event: MouseEvent | TouchEvent) => {
      const node = ref.current
      if (node && event.target instanceof Node && !node.contains(event.target)) onOutside()
    }
    const handleKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onOutside()
    }
    document.addEventListener('mousedown', handlePointer)
    document.addEventListener('touchstart', handlePointer)
    document.addEventListener('keydown', handleKey)
    return () => {
      document.removeEventListener('mousedown', handlePointer)
      document.removeEventListener('touchstart', handlePointer)
      document.removeEventListener('keydown', handleKey)
    }
  }, [ref, onOutside, active])
}

export function useCursorStack(resetKey: string) {
  const [cursor, setCursor] = useState<string | null>(null)
  const [stack, setStack] = useState<Array<string | null>>([])
  const [page, setPage] = useState(1)
  const [lastKey, setLastKey] = useState(resetKey)

  if (lastKey !== resetKey) {
    setLastKey(resetKey)
    setCursor(null)
    setStack([])
    setPage(1)
  }

  const goNext = useCallback(
    (nextCursor: string | null) => {
      if (!nextCursor) return
      setStack(current => [...current, cursor])
      setCursor(nextCursor)
      setPage(value => value + 1)
    },
    [cursor],
  )

  const goPrevious = useCallback(() => {
    setStack(current => {
      if (!current.length) return current
      const nextStack = current.slice(0, -1)
      setCursor(current[current.length - 1] ?? null)
      setPage(value => Math.max(1, value - 1))
      return nextStack
    })
  }, [])

  return {
    cursor,
    page,
    hasPrevious: stack.length > 0,
    goNext,
    goPrevious,
    reset: () => {
      setCursor(null)
      setStack([])
      setPage(1)
    },
  }
}
