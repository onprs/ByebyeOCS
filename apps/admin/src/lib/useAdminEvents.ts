import { useCallback, useEffect, useRef, useState } from 'react'
import { API_BASE } from '../api/client'
import { normalizeSseEvent } from '../api/endpoints'
import type { AdminEvent } from '../api/types'

export type EventConnection = 'connecting' | 'connected' | 'disconnected' | 'unavailable'

const eventTypes = [
  'message',
  'connected',
  'heartbeat',
  'reauth_required',
  'task.state_changed',
  'task.claimed',
  'task.attempt_finished',
  'task.control_applied',
  'task.control_accepted',
  'task.confirmed',
  'task.recovery_verification',
  'task.platform_confirmed_after_cancel',
  'task.updated',
  'task.attention',
  'plan.revised',
  'plan.control_applied',
  'account.operation_applied',
  'wallet.updated',
  'order.updated',
  'audit.created',
  'question.query.updated',
  'question.cache.invalidated',
  'admin.event',
  'snapshot',
]

export function useAdminEvents(options: {
  enabled: boolean
  onEvent?: (event: AdminEvent) => void
}): { connection: EventConnection; reconnect: () => void; errorCount: number } {
  const { enabled, onEvent } = options
  const [connection, setConnection] = useState<EventConnection>('connecting')
  const [nonce, setNonce] = useState(0)
  const [errorCount, setErrorCount] = useState(0)
  const callbackRef = useRef(onEvent)
  callbackRef.current = onEvent
  const sourceRef = useRef<EventSource | null>(null)
  const errorCountRef = useRef(0)

  const reconnect = useCallback(() => {
    errorCountRef.current = 0
    setErrorCount(0)
    setNonce(value => value + 1)
  }, [])

  useEffect(() => {
    if (!enabled) {
      setConnection('disconnected')
      return
    }
    sourceRef.current?.close()
    errorCountRef.current = 0
    setErrorCount(0)
    setConnection('connecting')
    let source: EventSource
    try {
      source = new EventSource(`${API_BASE}/events`, { withCredentials: true })
    } catch {
      setConnection('unavailable')
      return
    }
    sourceRef.current = source
    const handle = (event: MessageEvent<string>) => {
      const type = event.type
      if (type === 'connected' || type === 'heartbeat' || type === 'message') return
      if (type === 'reauth_required') {
        setConnection('disconnected')
        source.close()
        return
      }
      const parsed = normalizeSseEvent(event.data, type)
      if (parsed) callbackRef.current?.(parsed)
    }
    const handleOpen = () => {
      errorCountRef.current = 0
      setErrorCount(0)
      setConnection('connected')
    }
    const handleError = () => {
      errorCountRef.current += 1
      setErrorCount(errorCountRef.current)
      setConnection('disconnected')
      // 事件流不可用时停止自动重连，避免无界请求；页面保留手动刷新。
      if (errorCountRef.current >= 3) {
        source.close()
        setConnection('unavailable')
      }
    }
    source.addEventListener('open', handleOpen)
    source.addEventListener('error', handleError)
    for (const type of eventTypes) source.addEventListener(type, handle as EventListener)
    return () => {
      source.close()
      sourceRef.current = null
    }
  }, [enabled, nonce])

  return { connection, reconnect, errorCount }
}
