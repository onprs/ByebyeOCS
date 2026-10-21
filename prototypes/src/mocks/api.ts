/** 本地模拟 API：不发起网络请求，也不接收真实密码或支付凭据。 */
const latency = <T>(data: T, delay = 450): Promise<{ source: 'mock'; data: T }> => new Promise(resolve => window.setTimeout(() => resolve({ source: 'mock', data }), delay))
export const mockApi = {
  syncCourses: () => latency({ courseCount: 6, syncedAt: new Date().toISOString() }, 650),
  verifyDemoIdentity: () => latency({ internalUserId: 'U-1001', displayName: '沈知夏', courseCount: 6 }, 600),
  testConnection: (target: string) => latency({ target, state: 'simulated_reachable' as const }, 550),
}
