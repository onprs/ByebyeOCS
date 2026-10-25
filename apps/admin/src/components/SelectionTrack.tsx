import { useLayoutEffect, useRef, useState, type HTMLAttributes, type ReactNode } from 'react'

interface Bounds { x: number; y: number; width: number; height: number }

/** 为页签与导航保留一个持续移动的选中背景。 */
export function SelectionTrack({ value, children, className = '', ...props }: HTMLAttributes<HTMLDivElement> & { value: string; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  const [bounds, setBounds] = useState<Bounds | null>(null)
  useLayoutEffect(() => {
    const root = ref.current
    if (!root) return
    const active = root.querySelector<HTMLElement>('[aria-selected="true"], [aria-current="page"], button.active, a.active')
    const measure = () => {
      if (!active) { setBounds(null); return }
      const parent = root.getBoundingClientRect()
      const item = active.getBoundingClientRect()
      const next = { x: item.left - parent.left + root.scrollLeft, y: item.top - parent.top + root.scrollTop, width: item.width, height: item.height }
      setBounds(current => current && Object.keys(next).every(key => current[key as keyof Bounds] === next[key as keyof Bounds]) ? current : next)
    }
    measure()
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure)
    observer?.observe(root)
    if (active) observer?.observe(active)
    window.addEventListener('resize', measure)
    return () => { observer?.disconnect(); window.removeEventListener('resize', measure) }
  }, [value])
  return <div {...props} ref={ref} className={`selection-track ${className}`}>
    <span className="selection-indicator" aria-hidden="true" style={bounds ? { width: bounds.width, height: bounds.height, transform: `translate(${bounds.x}px, ${bounds.y}px)`, opacity: 1 } : { opacity: 0 }} />
    {children}
  </div>
}
