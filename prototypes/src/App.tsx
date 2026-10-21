import { createContext, useContext, useEffect, useRef, useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { ArrowLeft, ArrowRight, Check, CheckCheck, ChevronDown, Layers, Laptop, Monitor, Moon, Palette, RotateCcw, Smartphone, Sun, X } from 'lucide-react'
import type { ReactNode } from 'react'
import { AdminApp } from './admin/AdminApp'
import { ClientApp } from './client/ClientApp'
import { directions } from './data'
import { StoreProvider, useStore } from './store'
import { Badge, Button, IconButton, Logo, Modal } from './ui'

interface PreviewContext { design: string; theme: string; mobile: boolean; href: (path: string) => string; go: (path: string) => void; setTheme: (theme: string) => void; setDesign: (design: string) => void }
interface Appearance { design: string; theme: string }
const appearanceKey = 'byebyeocs-appearance-v1'
const readAppearance = (key: string): Appearance => { try { const stored = JSON.parse(localStorage.getItem(key) || '{}'); return { design: directions.some(d => d.id === stored?.design) ? stored.design : 'graphite', theme: stored?.theme === 'dark' ? 'dark' : 'light' } } catch { return { design: 'graphite', theme: 'light' } } }
const Preview = createContext<PreviewContext | null>(null)
export function usePreview() { const ctx = useContext(Preview); if (!ctx) throw new Error('缺少预览上下文'); return ctx }
export function AppLink({ to, children, className = '', ...rest }: { to: string; children: ReactNode; className?: string; onClick?: () => void }) { const { href } = usePreview(); return <Link to={href(to)} className={className} {...rest}>{children}</Link> }
export function App() { return <StoreProvider><PreviewApp /></StoreProvider> }
function PreviewApp() {
  const location = useLocation(), navigate = useNavigate(), params = new URLSearchParams(location.search)
  const preferenceKey = `${appearanceKey}:${location.pathname.startsWith('/admin') ? 'admin' : 'client'}`
  const [appearance, setAppearance] = useState<Appearance & { scope: string }>(() => ({ ...readAppearance(preferenceKey), scope: preferenceKey }))
  const preferences = appearance.scope === preferenceKey ? appearance : readAppearance(preferenceKey)
  const design = directions.some(d => d.id === params.get('design')) ? params.get('design')! : preferences.design
  const theme = ['light', 'dark'].includes(params.get('theme') || '') ? params.get('theme')! : preferences.theme
  const appearanceRef = useRef<Appearance>({ design, theme })
  appearanceRef.current = { design, theme }
  const mobile = location.pathname.startsWith('/mobile')
  const embedded = params.get('embed') === '1'
  const gallery = location.pathname === '/' || location.pathname === '/review'
  const href = (path: string) => { const target = new URL(path, window.location.origin); target.searchParams.set('design', design); target.searchParams.set('theme', theme); if (embedded) target.searchParams.set('embed', '1'); return `${target.pathname}${target.search}` }
  const go = (path: string) => navigate(href(path))
  // 只有显式选择才保存偏好，评审页的三个嵌入预览不会相互覆盖。
  const changeAppearance = (changes: Partial<Appearance>) => { const nextAppearance = { ...appearanceRef.current, ...changes }; appearanceRef.current = nextAppearance; setAppearance({ ...nextAppearance, scope: preferenceKey }); try { localStorage.setItem(preferenceKey, JSON.stringify(nextAppearance)) } catch { /* 存储不可用时保留本次页面内选择。 */ } const next = new URLSearchParams(location.search); next.set('design', nextAppearance.design); next.set('theme', nextAppearance.theme); navigate(`${location.pathname}?${next}`) }
  const setTheme = (value: string) => { if (['light', 'dark'].includes(value)) changeAppearance({ theme: value }) }
  const setDesign = (value: string) => { if (directions.some(d => d.id === value)) changeAppearance({ design: value }) }
  const [scenarios, setScenarios] = useState(false)
  const store = useStore()
  useEffect(() => { document.documentElement.dataset.design = gallery ? 'graphite' : design; document.documentElement.dataset.theme = theme; document.documentElement.dataset.mobile = String(mobile); document.documentElement.style.colorScheme = theme; document.title = gallery ? 'ByebyeOCS · 外观主题预览' : `ByebyeOCS · ${location.pathname.startsWith('/admin') ? '管理控制台' : '学习空间'}` }, [design, theme, gallery, mobile, location.pathname])
  useEffect(() => { setAppearance({ ...readAppearance(preferenceKey), scope: preferenceKey }); const handleStorage = (event: StorageEvent) => { if (event.key === preferenceKey || event.key === null) setAppearance({ ...readAppearance(preferenceKey), scope: preferenceKey }) }; window.addEventListener('storage', handleStorage); return () => window.removeEventListener('storage', handleStorage) }, [preferenceKey])
  useEffect(() => { window.scrollTo(0, 0) }, [location.pathname])
  return <Preview.Provider value={{ design, theme, mobile, href, go, setTheme, setDesign }}>
    {!gallery && !embedded && <div className="reviewbar"><Link to="/review" className="review-back"><ArrowLeft size={13} /><span>设计评审</span></Link><span className="review-divider"/><div className="review-directions">{directions.map(d => <button key={d.id} className={design === d.id ? 'active' : ''} onClick={() => setDesign(d.id)}><i style={{ background: d.color }} />{d.num} {d.name}</button>)}</div><div className="review-surfaces"><AppLink to="/admin/dashboard"><Monitor size={13} /><span>管理端</span></AppLink><AppLink to="/client/home"><Laptop size={13} /><span>用户端</span></AppLink><AppLink to="/mobile/home"><Smartphone size={13} /><span>移动端</span></AppLink></div><div className="review-tools"><IconButton label={theme === 'dark' ? '切换浅色' : '切换深色'} onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}>{theme === 'dark' ? <Sun size={14} /> : <Moon size={14} />}</IconButton><button className="scenario-button" onClick={() => setScenarios(true)}><Layers size={13} /><span>演示场景</span></button></div></div>}
    {gallery ? <Gallery /> : location.pathname.startsWith('/admin') ? <AdminApp /> : <ClientApp />}
    {store.toast && <div className="toast" role="status"><CheckCheck size={17} /><span>{store.toast}</span><button aria-label="关闭通知" onClick={() => store.notify('')}><X size={14} /></button></div>}
    <Modal open={scenarios} onClose={() => setScenarios(false)} title="原型演示场景" description="仅修改当前浏览器的合成数据，不访问学习通、题库或支付平台。">
      <div className="scenario-grid"><Button onClick={() => { store.setTaskStatus(['TK-8126'], 'confirmed'); setScenarios(false) }}><Check size={16} />接收平台确认（模拟）</Button><Button onClick={() => { store.setTaskStatus(['TK-8124'], 'attention'); setScenarios(false) }}>身份验证失效（模拟）</Button><Button onClick={() => { store.setTaskStatus(['TK-8125'], 'failed'); setScenarios(false) }}>资源不可用（模拟）</Button><Button onClick={() => { store.reset(); setScenarios(false) }}><RotateCcw size={16} />恢复初始数据</Button></div><p className="form-hint">运行进度不会自动变成成功。平台确认场景由这里显式触发；充值场景在付款面板中分两步演示。</p>
    </Modal>
  </Preview.Provider>
}
function Gallery() {
  const [surface, setSurface] = useState('admin'), [dark, setDark] = useState(false)
  const theme = dark ? 'dark' : 'light'
  return <div className="gallery-page"><header className="gallery-header"><Logo /><span className="gallery-project">PRODUCT DESIGN / ROUND 01</span><Badge>交互原型 · 设计评审</Badge></header>
    <main className="gallery-main"><div className="gallery-intro"><div><div className="eyebrow"><span className="tiny-line" /> 从学习，到有序。</div><h1>同一个产品，<br/><span>三种不同的表达。</span></h1><p>默认使用石墨，也可以选择更适合你的配色。<br/>管理员的高效工作台，与学生的轻量学习空间。</p></div><div className="gallery-brief"><span className="brief-number">03</span><div><b>可选外观主题</b><p>双端独立界面 · 可操作流程<br/>桌面与移动布局 · 浅色与深色</p></div><span className="brief-foot">UI REVIEW BEFORE IMPLEMENTATION <ArrowDownSymbol /></span></div></div>
    <div className="gallery-toolbar"><div className="segmented"><button className={surface === 'admin' ? 'active' : ''} onClick={() => setSurface('admin')}><Monitor size={15} />管理员控制台</button><button className={surface === 'client' ? 'active' : ''} onClick={() => setSurface('client')}><Laptop size={15} />用户学习空间</button><button className={surface === 'mobile' ? 'active' : ''} onClick={() => setSurface('mobile')}><Smartphone size={15} />移动端布局</button></div><Button variant="ghost" onClick={() => setDark(!dark)}>{dark ? <Sun size={15} /> : <Moon size={15} />}{dark ? '浅色预览' : '深色预览'}</Button></div>
    <div className="direction-grid">{directions.map(d => <article key={d.id} className={`direction-card direction-${d.id}`}><div className="direction-title"><span className="direction-number">{d.num}</span><div><h2>{d.name} <span>{d.english}</span></h2><p>{d.subtitle}</p></div><i style={{ background: d.color }} /></div><Link to={`/${surface}/${surface === 'admin' ? 'dashboard' : 'home'}?design=${d.id}&theme=${theme}`} className={`direction-preview ${surface === 'mobile' ? 'mobile-preview' : ''}`} aria-label={`打开${d.name}${surface === 'admin' ? '管理端' : '用户端'}原型`}><div className="mini-browser"><i/><i/><i/><span>byebyeocs · {surface === 'admin' ? 'console' : 'learn'}</span></div><div className="preview-viewport"><iframe title={`${d.name}方向预览`} src={`/${surface}/${surface === 'admin' ? 'dashboard' : 'home'}?design=${d.id}&theme=${theme}&embed=1`} tabIndex={-1} loading="lazy" /><div className="preview-hover">进入交互预览 <ArrowUpSymbol /></div></div></Link><div className="direction-copy"><div>{d.tags.map(tag => <span key={tag}>{tag}</span>)}</div><p>{d.description}</p><Link className="direction-enter" to={`/${surface}/${surface === 'admin' ? 'dashboard' : 'home'}?design=${d.id}&theme=${theme}`}>探索这个方向<ArrowRight size={16} /></Link></div></article>)}</div>
    <section className="review-guide"><div><Palette size={21}/><h2>设计已确认，体验继续打磨。</h2><p>选择一个方向进入后，可在顶部切换风格、主题与产品端。</p></div><div><b>01 / 看整体</b><p>布局、信息密度与品牌感</p></div><div><b>02 / 试流程</b><p>批量账号、安排编辑与充值到账</p></div><div><b>03 / 看细节</b><p>移动布局、异常提示与深色主题</p></div></section>
    <section className="gallery-comparisons"><span>截图对照 / 三个方向，同一个页面</span><div>{[{ name: '管理首页', file: 'admin-dashboard' }, { name: '题库与模型', file: 'admin-questions' }, { name: '用户首页', file: 'client-home' }, { name: '任务安排', file: 'client-plans' }, { name: '钱包充值', file: 'client-wallet' }, { name: '手机首页', file: 'mobile-home' }, { name: '手机任务安排', file: 'mobile-plans' }].map(item => <a href={`/screenshots/comparison-${item.file}.png`} target="_blank" rel="noreferrer" key={item.file}>{item.name}<ArrowUpSymbol/></a>)}</div></section>
    <footer className="gallery-footer"><span>全部数据与外部状态均为模拟，未连接真实账号及支付。</span><span>移动端为 React 视觉原型，尚未实现 Flutter。</span><span>默认石墨 · 主服务按用量 · 单价待公布 <ChevronDown size={12}/></span></footer>
    </main></div>
}
function ArrowDownSymbol() { return <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor"><path d="M12 3v17m-6-6 6 6 6-6" /></svg> }
function ArrowUpSymbol() { return <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5"><path d="M6 18 18 6M6 6h12v12" /></svg> }
