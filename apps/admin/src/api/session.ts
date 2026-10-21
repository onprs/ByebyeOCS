// CSRF 与未授权通知只保存在当前页面内存，刷新后通过 /me 重新取得。
let csrfToken: string | null = null
let unauthorizedHandler: (() => void) | null = null

export function setCsrfToken(token: string | null) {
  csrfToken = token
}

export function getCsrfToken(): string | null {
  return csrfToken
}

export function clearSessionMemory() {
  csrfToken = null
}

export function subscribeUnauthorized(handler: (() => void) | null) {
  unauthorizedHandler = handler
  return () => {
    if (unauthorizedHandler === handler) unauthorizedHandler = null
  }
}

export function notifyUnauthorized() {
  clearSessionMemory()
  unauthorizedHandler?.()
}
