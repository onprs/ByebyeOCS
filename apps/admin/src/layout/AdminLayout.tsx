import { useEffect, useRef, useState } from 'react'
import { useScrollMemory } from '../lib/useScrollMemory'
import { useTableOverflowAffordance } from '../lib/useTableOverflowAffordance'
import {
  ChevronDown,
  ChevronRight,
  Cpu,
  CreditCard,
  ExternalLink,
  LayoutDashboard,
  ListTodo,
  LogOut,
  KeyRound,
  Menu,
  Moon,
  Settings2,
  ShieldCheck,
  Sun,
  Users,
  X,
} from 'lucide-react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { Logo } from '../components/Logo'
import { SelectionTrack } from '../components/SelectionTrack'
import { Avatar, IconButton } from '../components/ui'
import { useToast } from '../components/toast'
import { useClickOutside } from '../lib/hooks'
import { useAppearance } from '../theme/AppearanceProvider'
import { hasPermission } from '../auth/permissions'
import { tikuPanelUrl } from '../lib/tikuPanel'

const navigation = [
  { path: '/dashboard', label: '运行总览', icon: LayoutDashboard, permission: 'learning.read' },
  { path: '/users', label: '用户与账号', icon: Users, permission: 'learning.read' },
  { path: '/tasks', label: '任务监控', icon: ListTodo, permission: 'learning.read' },
  { path: '/questions', label: '题库查询', icon: Cpu, permission: 'question:read' },
  { path: '/finance', label: '财务与订单', icon: CreditCard, permission: 'billing.read' },
]

function roleLabel(role: string): string {
  const labels: Record<string, string> = {
    owner: '所有者',
    admin: '管理员',
    operator: '运营',
    support: '支持',
    finance: '财务',
    observer: '观察员',
    viewer: '观察员',
  }
  return labels[role] ?? role
}

export function AdminLayout() {
  const { me, logout } = useAuth()
  const { notify } = useToast()
  const { effectiveTheme, updateAppearance, saving, status } = useAppearance()
  const navigate = useNavigate()
  const location = useLocation()
  const [navOpen, setNavOpen] = useState(false)
  const [profileOpen, setProfileOpen] = useState(false)
  const [loggingOut, setLoggingOut] = useState(false)
  const profileRef = useRef<HTMLDivElement | null>(null)
  useClickOutside(profileRef, () => setProfileOpen(false), profileOpen)
  useScrollMemory(location.pathname)
  useTableOverflowAffordance()

  useEffect(() => {
    setNavOpen(false)
    setProfileOpen(false)
  }, [location.pathname])

  const visibleNavigation = navigation.filter(item => hasPermission(me, item.permission))
  const active =
    [
      ...navigation,
      { path: '/administrators', label: '管理员与角色', icon: KeyRound },
      { path: '/privacy-requests', label: '隐私与数据请求', icon: ShieldCheck },
      { path: '/settings', label: '系统设置', icon: Settings2 },
    ].find(item =>
      location.pathname.startsWith(item.path),
    ) ?? visibleNavigation[0] ?? { path: '/settings', label: '系统设置' }

  const toggleTheme = async () => {
    const next = effectiveTheme === 'dark' ? 'light' : 'dark'
    await updateAppearance({ theme_mode: next })
  }

  const handleLogout = async () => {
    if (loggingOut) return
    setLoggingOut(true)
    try {
      await logout()
      notify('已退出管理控制台。', 'success')
      navigate('/login', { replace: true })
    } catch {
      notify('本地登录状态已清除，未能完成服务退出。', 'warning')
      navigate('/login', { replace: true })
    } finally {
      setLoggingOut(false)
    }
  }

  return (
    <div className="admin-shell">
      {navOpen ? <div className="sidebar-scrim" onClick={() => setNavOpen(false)} aria-hidden="true" /> : null}
      <aside className={`admin-sidebar ${navOpen ? 'is-open' : ''}`} aria-label="管理导航">
        <div className="sidebar-brand">
          <Logo />
        </div>
        <div className="admin-workspace">
          <span className="workspace-icon">
            <ShieldCheck size={14} />
          </span>
          <div className="admin-workspace-copy">
            <b>运营工作空间</b>
            <span>管理员控制台</span>
          </div>
          <ChevronDown size={14} aria-hidden="true" />
        </div>
        <SelectionTrack value={location.pathname} className="nav-region">
        <div className="nav-heading">工作空间</div>
        <nav className="admin-nav">
          {visibleNavigation.map(item => (
            <NavLink
              key={item.path}
              to={item.path}
              className={({ isActive }) => `admin-nav-item ${isActive ? 'active' : ''}`}
            >
              <item.icon size={16} aria-hidden="true" />
              <span>{item.label}</span>
            </NavLink>
          ))}
          {hasPermission(me, 'question:read') ? (
            <a
              className="admin-nav-item"
              href={tikuPanelUrl}
              target="_blank"
              rel="noreferrer"
              title="在新标签页打开题库维护面板"
              onClick={() => setNavOpen(false)}
            >
              <ExternalLink size={16} aria-hidden="true" />
              <span>题库维护面板</span>
            </a>
          ) : null}
        </nav>
        <div className="nav-heading">平台管理</div>
        <nav className="admin-nav">
          {hasPermission(me, 'admin.users.manage') ? (
            <NavLink
              to="/administrators"
              className={({ isActive }) => `admin-nav-item ${isActive ? 'active' : ''}`}
            >
              <KeyRound size={16} aria-hidden="true" />
              <span>管理员与角色</span>
            </NavLink>
          ) : null}
          {hasPermission(me, 'privacy.review') ? (
            <NavLink
              to="/privacy-requests"
              className={({ isActive }) => `admin-nav-item ${isActive ? 'active' : ''}`}
            >
              <ShieldCheck size={16} aria-hidden="true" />
              <span>隐私与数据请求</span>
            </NavLink>
          ) : null}
          <NavLink
            to="/settings"
            className={({ isActive }) => `admin-nav-item ${isActive ? 'active' : ''}`}
          >
            <Settings2 size={16} aria-hidden="true" />
            <span>系统设置</span>
          </NavLink>
        </nav>
        </SelectionTrack>
        <div className="admin-sidebar-bottom">
          <button className="sidebar-admin" onClick={() => navigate('/settings')} aria-label="打开账户与设置">
            <Avatar name={me?.display_name ?? '管'} small />
            <span className="sidebar-admin-copy">
              <b>{me?.display_name ?? '管理员'}</b>
              <span>{roleLabel(me?.role ?? 'admin')}</span>
            </span>
            <ChevronRight size={14} aria-hidden="true" />
          </button>
        </div>
      </aside>

      <div className="admin-main">
        <header className="admin-topbar">
          <div className="topbar-left">
            <IconButton
              label={navOpen ? '收起导航' : '展开导航'}
              className="menu-button"
              onClick={() => setNavOpen(value => !value)}
            >
              {navOpen ? <X size={16} /> : <Menu size={16} />}
            </IconButton>
            <div className="breadcrumbs" aria-label="当前位置">
              <span>工作空间</span>
              <ChevronRight size={14} aria-hidden="true" />
              <strong>{active.label}</strong>
            </div>
          </div>
          <div className="topbar-right">
            <IconButton
              label={effectiveTheme === 'dark' ? '切换为浅色' : '切换为深色'}
              onClick={() => void toggleTheme()}
              disabled={saving || status === 'unavailable' || status === 'loading'}
            >
              {effectiveTheme === 'dark' ? <Sun size={16} /> : <Moon size={16} />}
            </IconButton>
            <span className="topbar-divider" aria-hidden="true" />
            <div className="profile-wrap" ref={profileRef}>
              <button
                className="profile-button"
                aria-haspopup="menu"
                aria-expanded={profileOpen}
                aria-label="管理员账户菜单"
                onClick={() => setProfileOpen(value => !value)}
              >
                <Avatar name={me?.display_name ?? '管'} small />
                <span className="profile-button-copy">
                  <b>{me?.display_name ?? '管理员'}</b>
                  <span>{roleLabel(me?.role ?? 'admin')}</span>
                </span>
                <ChevronDown size={14} aria-hidden="true" />
              </button>
              {profileOpen ? (
                <div className="menu" role="menu">
                  <div className="menu-section">
                    <strong>{me?.display_name ?? '管理员'}</strong>
                    <span>
                      {me?.username ?? '—'} · {roleLabel(me?.role ?? 'admin')}
                    </span>
                  </div>
                  <button
                    className="menu-item"
                    role="menuitem"
                    onClick={() => {
                      setProfileOpen(false)
                      navigate('/settings')
                    }}
                  >
                    <Settings2 size={14} />
                    外观与系统设置
                  </button>
                  <button className="menu-item danger" role="menuitem" onClick={() => void handleLogout()}>
                    <LogOut size={14} />
                    退出登录
                  </button>
                </div>
              ) : null}
            </div>
          </div>
        </header>
        <main className={`admin-content ${['/tasks', '/finance'].includes(location.pathname) ? 'table-workspace' : ''}`}>
          <div key={location.pathname} className="page-view"><Outlet /></div>
        </main>
      </div>
    </div>
  )
}
