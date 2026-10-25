import { useEffect } from 'react'

/** 表格溢出时在容器边缘显示阴影，滚动到末端自动消失。 */
export function useTableOverflowAffordance() {
  useEffect(() => {
    const observed = new WeakSet<Element>()
    const update = (element: HTMLElement) => {
      element.dataset.overflow = element.scrollWidth > element.clientWidth + 1 ? 'true' : 'false'
    }
    const observe = () => {
      document.querySelectorAll<HTMLElement>('.table-scroll').forEach(element => {
        update(element)
        if (observed.has(element)) return
        observed.add(element)
        new ResizeObserver(() => update(element)).observe(element)
        new MutationObserver(() => update(element)).observe(element, { childList: true, subtree: true })
      })
    }
    observe()
    const mutations = new MutationObserver(observe)
    mutations.observe(document.body, { childList: true, subtree: true })
    window.addEventListener('resize', observe)
    return () => {
      mutations.disconnect()
      window.removeEventListener('resize', observe)
    }
  }, [])
}
