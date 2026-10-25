import { useLayoutEffect, useRef } from 'react'

/** 保留会话内的页面滚动；异步内容恢复高度后再定位。 */
export function useScrollMemory(pathname: string) {
  const positions = useRef(new Map<string, number>())
  useLayoutEffect(() => {
    const target = positions.current.get(pathname) ?? 0
    let restoring = true
    const restore = () => {
      window.scrollTo(0, target)
      if (document.documentElement.scrollHeight - innerHeight >= target) {
        restoring = false
        observer?.disconnect()
      }
    }
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(restore)
    const remember = () => { if (!restoring) positions.current.set(pathname, window.scrollY) }
    const cancelRestore = () => { restoring = false; observer?.disconnect() }
    observer?.observe(document.body)
    restore()
    window.addEventListener('scroll', remember, { passive: true })
    window.addEventListener('wheel', cancelRestore, { passive: true })
    window.addEventListener('pointerdown', cancelRestore, { passive: true })
    return () => {
      observer?.disconnect()
      window.removeEventListener('scroll', remember)
      window.removeEventListener('wheel', cancelRestore)
      window.removeEventListener('pointerdown', cancelRestore)
    }
  }, [pathname])
}
