import { useCallback, useRef } from 'react'
import { newIdempotencyKey } from '../api/client'

// 同一组输入重试时复用幂等键；修改输入才生成新键，避免网络失败后重复记账。
export function useIdempotencyKey() {
  const current = useRef<{ hash: string; key: string } | null>(null)
  const reset = useCallback(() => {
    current.current = null
  }, [])
  const keyFor = useCallback((payload: unknown) => {
    const hash = JSON.stringify(payload)
    if (!current.current || current.current.hash !== hash) {
      current.current = { hash, key: newIdempotencyKey() }
    }
    return current.current.key
  }, [])
  return { keyFor, reset }
}
