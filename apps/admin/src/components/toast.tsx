import { AlertCircle, CheckCircle2, Info, X } from 'lucide-react'
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'

type ToastTone = 'success' | 'error' | 'warning' | 'info'

interface ToastItem {
  id: number
  message: string
  tone: ToastTone
}

interface ToastContextValue {
  notify: (message: string, tone?: ToastTone) => void
}

const ToastContext = createContext<ToastContextValue | null>(null)

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([])
  const nextId = useRef(1)
  const timers = useRef(new Map<number, number>())

  const remove = useCallback((id: number) => {
    setItems(current => current.filter(item => item.id !== id))
    const timer = timers.current.get(id)
    if (timer) window.clearTimeout(timer)
    timers.current.delete(id)
  }, [])

  const notify = useCallback(
    (message: string, tone: ToastTone = 'info') => {
      if (!message) return
      const id = nextId.current++
      setItems(current => [...current.slice(-3), { id, message, tone }])
      const timer = window.setTimeout(() => remove(id), tone === 'error' ? 8000 : 5000)
      timers.current.set(id, timer)
    },
    [remove],
  )

  useEffect(
    () => () => {
      for (const timer of timers.current.values()) window.clearTimeout(timer)
      timers.current.clear()
    },
    [],
  )

  const value = useMemo(() => ({ notify }), [notify])

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div className="toast-stack" aria-live="polite" aria-atomic="false">
        {items.map(item => (
          <div className={`toast ${item.tone}`} key={item.id} role={item.tone === 'error' ? 'alert' : 'status'}>
            {item.tone === 'success' ? (
              <CheckCircle2 size={16} />
            ) : item.tone === 'error' || item.tone === 'warning' ? (
              <AlertCircle size={16} />
            ) : (
              <Info size={16} />
            )}
            <span className="toast-copy">{item.message}</span>
            <button className="toast-close" aria-label="关闭提示" onClick={() => remove(item.id)}>
              <X size={14} />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}

export function useToast(): ToastContextValue {
  const context = useContext(ToastContext)
  if (!context) throw new Error('缺少提示上下文')
  return context
}
