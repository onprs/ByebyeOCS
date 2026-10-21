import { createContext, useContext, useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import type { Account, Ledger, Order, Plan, QuestionModel, Status, Task } from './data'
import { courses, initialAccounts, initialLedger, initialOrders, initialPlans, initialQuestionModels, initialTasks, statusLabel } from './data'

interface PrototypeState { tasks: Task[]; plans: Plan[]; accounts: Account[]; questionModels: QuestionModel[]; balance: number; ledger: Ledger[]; orders: Order[]; audit: string[]; settings: Record<string, string> }
const freshState = (): PrototypeState => ({ tasks: structuredClone(initialTasks), plans: structuredClone(initialPlans), accounts: structuredClone(initialAccounts), questionModels: structuredClone(initialQuestionModels), balance: 2450, ledger: structuredClone(initialLedger), orders: structuredClone(initialOrders), audit: [], settings: {} })
const storageKey = 'byebyeocs-design-v1'
function loadState(): PrototypeState { try { const raw = localStorage.getItem(storageKey); if (raw) { const parsed = JSON.parse(raw); if (Array.isArray(parsed.tasks) && Array.isArray(parsed.plans) && Array.isArray(parsed.audit) && typeof parsed.balance === 'number' && parsed.settings) return { ...parsed, questionModels: Array.isArray(parsed.questionModels) ? parsed.questionModels : structuredClone(initialQuestionModels) } } } catch { /* 存储不可用时使用全新的合成数据。 */ } return freshState() }
interface Store extends PrototypeState { toast: string; notify: (message: string) => void; reset: () => void; setTaskStatus: (ids: string[], status: Status) => void; savePlan: (plan: Plan, baseVersion?: number) => boolean; togglePlan: (id: string) => void; updateAccounts: (ids: string[], action: string, reason: string) => void; createOrder: (amount: number, channel: string) => Order; transitionOrder: (id: string, status: Order['status']) => void; adjustBalance: (accountId: string, cents: number, reason: string) => void; saveSettings: (values: Record<string, string>) => void; updateQuestionModel: (id: string, patch: Partial<QuestionModel>) => void; setDefaultQuestionModel: (id: string) => void; createQuestionModel: (model: QuestionModel) => void }
const Context = createContext<Store | null>(null)
export function StoreProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<PrototypeState>(loadState)
  const [toast, setToast] = useState('')
  useEffect(() => { try { localStorage.setItem(storageKey, JSON.stringify(state)) } catch { /* 演示仍可在内存中继续。 */ } }, [state])
  useEffect(() => { if (!toast) return; const timer = window.setTimeout(() => setToast(''), 3400); return () => clearTimeout(timer) }, [toast])
  useEffect(() => { const onStorage = (event: StorageEvent) => { if (event.key === storageKey) setState(loadState()) }; window.addEventListener('storage', onStorage); return () => window.removeEventListener('storage', onStorage) }, [])
  const notify = (message: string) => setToast(message)
  const setTaskStatus = (ids: string[], status: Status) => {
    setState(s => {
      const tasks = s.tasks.map(t => ids.includes(t.id) ? { ...t, status, updated: '刚刚', history: [...t.history, { time: new Date().toLocaleTimeString('zh-CN', { hour12: false }), text: `${statusLabel[status]}${status === 'confirmed' ? ' · 接收模拟平台查询结果' : ' · 操作已记录'}` }] } : t)
      return { ...s, tasks, accounts: s.accounts.map(a => ({ ...a, running: tasks.filter(t => t.owner === a.id && ['running', 'preparing'].includes(t.status)).length })), audit: [`任务 ${ids.join('、')} → ${statusLabel[status]}`, ...s.audit] }
    })
    notify(`已更新 ${ids.length} 项任务：${statusLabel[status]}`)
  }
  const savePlan = (plan: Plan, baseVersion?: number) => {
    // 同步读取最新本地版本，用于演示跨标签页修改冲突；正式系统由服务端进行条件更新。
    const current = loadState().plans.find(p => p.id === plan.id)
    if (current && baseVersion !== undefined && current.version !== baseVersion) { setState(loadState()); notify('安排已在其他页面更新，请重新打开后编辑'); return false }
    setState(s => ({ ...s, plans: s.plans.some(p => p.id === plan.id) ? s.plans.map(p => p.id === plan.id ? { ...plan, version: p.version + 1 } : p) : [...s.plans, plan] }))
    notify('安排已保存，修改将应用于尚未开始的任务'); return true
  }
  const togglePlan = (id: string) => { setState(s => ({ ...s, plans: s.plans.map(p => p.id === id ? { ...p, enabled: !p.enabled, version: p.version + 1 } : p) })); notify('安排状态已更新；进行中的任务可单独暂停') }
  const updateAccounts = (ids: string[], action: string, reason: string) => {
    setState(s => ({ ...s, accounts: s.accounts.map(a => ids.includes(a.id) ? { ...a, state: action === 'limit' ? 'limited' : action === 'release' ? 'expired' : a.state, running: action === 'pause' || action === 'limit' ? 0 : a.running, issue: action === 'limit' ? '管理员限制' : action === 'release' ? '待重新验证' : a.issue } : a), tasks: s.tasks.map(t => ids.includes(t.owner) && ['pause', 'limit'].includes(action) && ['queued', 'preparing', 'running'].includes(t.status) ? { ...t, status: 'paused' } : t), audit: [`${ids.join('、')} · ${action} · ${reason}`, ...s.audit] }))
    notify(`已处理 ${ids.length} 个账号，操作已记录`)
  }
  const createOrder = (amount: number, channel: string) => {
    const order: Order = { id: String(Date.now()), user: '沈知夏', amount, channel, status: 'pending', time: '刚刚' }
    setState(s => ({ ...s, orders: [order, ...s.orders] })); return order
  }
  const transitionOrder = (id: string, status: Order['status']) => {
    setState(s => {
      const order = s.orders.find(o => o.id === id)
      if (!order || order.status === 'credited' || order.status === 'closed' || order.status === status || (status === 'credited' && order.status !== 'paid') || (status === 'paid' && order.status !== 'pending')) return s
      return { ...s, orders: s.orders.map(o => o.id === id ? { ...o, status } : o), balance: s.balance + (status === 'credited' ? order.amount : 0), accounts: status === 'credited' ? s.accounts.map(a => a.id === 'U-1001' ? { ...a, balance: s.balance + order.amount } : a) : s.accounts, ledger: status === 'credited' ? [{ id: `L-${id}`, label: '余额充值', kind: 'recharge', amount: order.amount, time: '刚刚', detail: `${order.channel} · 已到账` }, ...s.ledger] : s.ledger }
    })
  }
  const adjustBalance = (accountId: string, cents: number, reason: string) => { setState(s => ({ ...s, accounts: s.accounts.map(a => a.id === accountId ? { ...a, balance: a.balance + cents } : a), balance: accountId === 'U-1001' ? s.balance + cents : s.balance, ledger: accountId === 'U-1001' ? [{ id: `ADJ-${Date.now()}`, label: '人工余额调整', kind: 'adjust', amount: cents, time: '刚刚', detail: reason }, ...s.ledger] : s.ledger, audit: [`财务调整 ${accountId} · ${cents} 分 · ${reason}`, ...s.audit] })); notify('余额调整已记录，审计记录已生成') }
  const saveSettings = (values: Record<string, string>) => { setState(s => ({ ...s, settings: { ...s.settings, ...values }, audit: ['更新系统配置（演示）', ...s.audit] })); notify('设置已保存到当前浏览器') }
  const updateQuestionModel = (id: string, patch: Partial<QuestionModel>) => {
    setState(s => {
      let models = s.questionModels.map(m => m.id === id ? { ...m, ...patch, ...(patch.isDefault ? { enabled: true } : {}) } : m)
      if (patch.isDefault) models = models.map(m => ({ ...m, isDefault: m.id === id }))
      const currentDefault = models.find(m => m.isDefault)
      if (!currentDefault || !currentDefault.enabled) { const fallback = models.find(m => m.enabled); models = models.map(m => ({ ...m, isDefault: m.id === fallback?.id })) }
      return { ...s, questionModels: models, audit: [`题库模型 ${id} · 更新配置（演示）`, ...s.audit] }
    })
    notify('模型配置已更新；真实计费与上游调用未启用')
  }
  const setDefaultQuestionModel = (id: string) => updateQuestionModel(id, { isDefault: true, enabled: true })
  const createQuestionModel = (model: QuestionModel) => { setState(s => { const next = model.isDefault ? { ...model, enabled: true } : model; const models = model.isDefault ? s.questionModels.map(m => ({ ...m, isDefault: false })) : s.questionModels; return { ...s, questionModels: [...models, next], audit: [`新增公开模型 ${model.name}（演示）`, ...s.audit] } }); notify('公开模型已创建；确认配置后再开启可见') }
  return <Context.Provider value={{ ...state, toast, notify, reset: () => { setState(freshState()); notify('已恢复初始演示数据') }, setTaskStatus, savePlan, togglePlan, updateAccounts, createOrder, transitionOrder, adjustBalance, saveSettings, updateQuestionModel, setDefaultQuestionModel, createQuestionModel }}>{children}</Context.Provider>
}
export const useStore = () => { const store = useContext(Context); if (!store) throw new Error('缺少原型状态容器'); return store }
export const useLiveCourses = () => { const { tasks } = useStore(); return courses.map(c => ({ ...c, confirmed: c.confirmed + tasks.filter(t => t.owner === 'U-1001' && t.courseId === c.id && t.status === 'confirmed').length - initialTasks.filter(t => t.owner === 'U-1001' && t.courseId === c.id && t.status === 'confirmed').length })) }
