import * as Dialog from '@radix-ui/react-dialog'
import {
  AlertCircle,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  Inbox,
  LoaderCircle,
  Search,
  X,
} from 'lucide-react'
import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { ApiError } from '../api/client'
import type { TaskState } from '../api/types'
import { formatMoneyMinor, formatRetryAfter } from '../lib/format'

export function Button({
  children,
  variant = 'secondary',
  size,
  loading = false,
  className = '',
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
  size?: 'sm'
  loading?: boolean
}) {
  return (
    <button
      className={`btn btn-${variant} ${size === 'sm' ? 'btn-sm' : ''} ${className}`}
      {...props}
      disabled={props.disabled || loading}
    >
      {loading ? <LoaderCircle size={14} className="spinner" aria-hidden="true" /> : null}
      {children}
    </button>
  )
}

export function IconButton({
  children,
  label,
  danger = false,
  className = '',
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { label: string; danger?: boolean }) {
  return (
    <button
      type="button"
      className={`icon-button ${danger ? 'is-danger' : ''} ${className}`}
      aria-label={label}
      title={label}
      {...props}
    >
      {children}
    </button>
  )
}

export function Badge({
  children,
  tone = 'neutral',
  dot = false,
}: {
  children: ReactNode
  tone?: 'neutral' | 'green' | 'amber' | 'red' | 'blue' | 'violet'
  dot?: boolean
}) {
  return (
    <span className={`badge badge-${tone}`}>
      {dot ? <i aria-hidden="true" /> : null}
      {children}
    </span>
  )
}

const taskTone: Record<TaskState, 'neutral' | 'green' | 'amber' | 'red' | 'blue' | 'violet'> = {
  pending: 'neutral',
  preparing: 'blue',
  running: 'blue',
  waiting_verification: 'amber',
  confirmed: 'green',
  paused: 'neutral',
  needs_attention: 'amber',
  failed: 'red',
  cancelled: 'neutral',
  unknown: 'neutral',
}

export function TaskStateBadge({ state, label }: { state: TaskState; label?: string }) {
  return (
    <Badge tone={taskTone[state]} dot={state === 'running' || state === 'confirmed'}>
      {label ?? state}
    </Badge>
  )
}

export function Card({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <section className={`card ${className}`}>{children}</section>
}

export function CardHeader({
  title,
  subtitle,
  actions,
}: {
  title: string
  subtitle?: string
  actions?: ReactNode
}) {
  return (
    <div className="card-header">
      <div>
        <h2>{title}</h2>
        {subtitle ? <p>{subtitle}</p> : null}
      </div>
      {actions ? <div className="card-header-actions">{actions}</div> : null}
    </div>
  )
}

export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow?: string
  title: string
  description?: string
  actions?: ReactNode
}) {
  return (
    <header className="page-header">
      <div className="page-header-copy">
        {eyebrow ? <div className="eyebrow">{eyebrow}</div> : null}
        <h1>{title}</h1>
        {description ? <p>{description}</p> : null}
      </div>
      {actions ? <div className="page-actions">{actions}</div> : null}
    </header>
  )
}

export function SearchInput({
  value,
  onChange,
  placeholder = '搜索',
  label,
}: {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  label?: string
}) {
  return (
    <label className="search-input">
      <Search size={14} aria-hidden="true" />
      <input
        type="search"
        aria-label={label ?? placeholder}
        value={value}
        onChange={event => onChange(event.target.value)}
        placeholder={placeholder}
      />
      {value ? (
        <button type="button" aria-label="清除搜索" onClick={() => onChange('')}>
          <X size={12} />
        </button>
      ) : null}
    </label>
  )
}

export interface SelectOption {
  value: string
  label: string
}

export function Select({
  value,
  onChange,
  options,
  label,
  disabled = false,
}: {
  value: string
  onChange: (value: string) => void
  options: Array<SelectOption | string>
  label: string
  disabled?: boolean
}) {
  return (
    <span className="select-wrap">
      <select
        aria-label={label}
        value={value}
        disabled={disabled}
        onChange={event => onChange(event.target.value)}
      >
        {options.map(option => {
          const item = typeof option === 'string' ? { value: option, label: option } : option
          return (
            <option value={item.value} key={item.value}>
              {item.label}
            </option>
          )
        })}
      </select>
      <ChevronDown size={13} aria-hidden="true" />
    </span>
  )
}

export function Field({
  label,
  hint,
  children,
  required = false,
  htmlFor,
}: {
  label: string
  hint?: ReactNode
  children: ReactNode
  required?: boolean
  htmlFor?: string
}) {
  return (
    <label className="field" htmlFor={htmlFor}>
      <span>
        {label}
        {required ? <b className="required"> *</b> : null}
      </span>
      {children}
      {hint ? <small>{hint}</small> : null}
    </label>
  )
}

export function Switch({
  checked,
  onChange,
  label,
  disabled = false,
}: {
  checked: boolean
  onChange: (checked: boolean) => void
  label: string
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      className={`switch ${checked ? 'on' : ''}`}
      disabled={disabled}
      onClick={() => onChange(!checked)}
    >
      <span />
    </button>
  )
}

export function Tabs({
  value,
  onChange,
  items,
}: {
  value: string
  onChange: (value: string) => void
  items: Array<{ value: string; label: string; count?: number | null }>
}) {
  return (
    <div className="tabs" role="tablist">
      {items.map(item => (
        <button
          type="button"
          role="tab"
          key={item.value}
          aria-selected={item.value === value}
          className={item.value === value ? 'active' : ''}
          onClick={() => onChange(item.value)}
        >
          {item.label}
          {item.count !== undefined && item.count !== null ? <span>{item.count}</span> : null}
        </button>
      ))}
    </div>
  )
}

export function Modal({
  open,
  onClose,
  title,
  description,
  children,
  actions,
  wide = false,
  drawer = false,
  closeLabel = '关闭',
}: {
  open: boolean
  onClose: () => void
  title: string
  description?: string
  children: ReactNode
  actions?: ReactNode
  wide?: boolean
  drawer?: boolean
  closeLabel?: string
}) {
  return (
    <Dialog.Root open={open} onOpenChange={value => (!value ? onClose() : undefined)}>
      <Dialog.Portal>
        <Dialog.Overlay className="modal-overlay" />
        <Dialog.Content className={`modal-content ${wide ? 'wide' : ''} ${drawer ? 'drawer' : ''}`}>
          <div className="modal-heading">
            <div>
              <Dialog.Title>{title}</Dialog.Title>
              {description ? (
                <Dialog.Description>{description}</Dialog.Description>
              ) : (
                <Dialog.Description className="sr-only">{title}</Dialog.Description>
              )}
            </div>
            <Dialog.Close asChild>
              <IconButton label={closeLabel}>
                <X size={18} />
              </IconButton>
            </Dialog.Close>
          </div>
          <div className="modal-body">{children}</div>
          {actions ? <div className="modal-actions">{actions}</div> : null}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

export function EmptyState({
  title = '暂无符合条件的内容',
  description = '调整筛选条件或稍后重试。',
  action,
}: {
  title?: string
  description?: string
  action?: ReactNode
}) {
  return (
    <div className="empty-state">
      <span className="empty-state-icon">
        <Inbox size={20} />
      </span>
      <h3>{title}</h3>
      <p>{description}</p>
      {action}
    </div>
  )
}

export function LoadingBlock({ rows = 3 }: { rows?: number }) {
  return (
    <div className="loading-block" role="status" aria-label="正在加载">
      {Array.from({ length: rows }, (_, index) => (
        <div className="skeleton" key={index} />
      ))}
      <span className="sr-only">正在加载</span>
    </div>
  )
}

export function InlineLoading({ text = '正在加载' }: { text?: string }) {
  return (
    <span className="loading-inline" role="status">
      <LoaderCircle size={14} className="spinner" />
      {text}
    </span>
  )
}

export function describeError(error: unknown): {
  title: string
  message: string
  code: string | null
  requestId: string | null
  retryable: boolean
  retryAfter: string
  status: number
} {
  if (error instanceof ApiError) {
    const titles: Record<number, string> = {
      401: '登录状态已失效',
      403: '没有执行此操作的权限',
      404: '功能暂不可用',
      409: '操作发生冲突',
      412: '数据版本已变化',
      422: '提交内容无效',
      428: '缺少版本条件',
      429: '请求过于频繁',
      503: '服务尚未配置或暂不可用',
    }
    let title = titles[error.status] ?? '加载失败'
    if (error.status === 503 && error.code === 'not_configured') title = '功能尚未配置'
    if (error.status === 404) title = '功能暂不可用'
    if (error.status === 0) title = '无法连接服务'
    return {
      title,
      message: error.message,
      code: error.code,
      requestId: error.requestId === '未提供' ? null : error.requestId,
      retryable: error.retryable,
      retryAfter: formatRetryAfter(error.retryAfterSeconds),
      status: error.status,
    }
  }
  if (error instanceof Error) {
    return {
      title: '操作未完成',
      message: error.message,
      code: null,
      requestId: null,
      retryable: false,
      retryAfter: '',
      status: 0,
    }
  }
  return {
    title: '操作未完成',
    message: '请求没有成功完成，请稍后重试。',
    code: null,
    requestId: null,
    retryable: false,
    retryAfter: '',
    status: 0,
  }
}

export function ErrorState({
  error,
  onRetry,
  compact = false,
}: {
  error: unknown
  onRetry?: () => void
  compact?: boolean
}) {
  const details = describeError(error)
  const notConfigured = details.status === 503
  return (
    <div className={`error-state ${notConfigured ? 'not-configured' : ''} ${compact ? 'is-compact' : ''}`} role="alert">
      <span className="error-state-icon">
        {details.status === 403 ? <CircleAlert size={17} /> : <AlertCircle size={17} />}
      </span>
      <div className="error-state-copy">
        <h3>{details.title}</h3>
        <p>{details.message}</p>
        <div className="error-meta">
          {details.code ? <span>错误码 {details.code}</span> : null}
          {details.requestId ? <span>请求编号 {details.requestId}</span> : null}
          {details.retryAfter ? <span>{details.retryAfter}</span> : null}
          {details.retryable ? <span>可重试</span> : null}
        </div>
        {onRetry && details.status !== 403 && details.status !== 401 ? (
          <div className="error-actions">
            <Button size="sm" onClick={onRetry}>
              重新加载
            </Button>
          </div>
        ) : null}
      </div>
    </div>
  )
}

export function RefreshError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const details = describeError(error)
  return (
    <div className="refresh-error" role="alert">
      <CircleAlert size={14} />
      <span>
        {details.title}：{details.message}
        {details.requestId ? ` · 请求编号 ${details.requestId}` : ''}
        {details.retryAfter ? ` · ${details.retryAfter}` : ''}
      </span>
      <Button size="sm" variant="ghost" onClick={onRetry}>重试</Button>
    </div>
  )
}

export function Callout({
  children,
  tone = 'neutral',
  icon,
}: {
  children: ReactNode
  tone?: 'neutral' | 'warning' | 'danger' | 'success'
  icon?: ReactNode
}) {
  return (
    <div className={`callout ${tone !== 'neutral' ? `callout-${tone}` : ''}`}>
      {icon ?? <CircleAlert size={15} />}
      <div>{children}</div>
    </div>
  )
}

export function Metric({
  label,
  value,
  unit,
  children,
}: {
  label: string
  value: string | number | null
  unit?: string | null
  children?: ReactNode
}) {
  return (
    <div className="metric">
      <div className="metric-label">{label}</div>
      <div className="metric-value">
        {value === null || value === '' ? <span className="muted" style={{ fontSize: 15 }}>未提供</span> : value}
        {unit ? <small>{unit}</small> : null}
      </div>
      <div className="metric-foot">{children}</div>
    </div>
  )
}

export function Progress({ value, tone }: { value: number | null; tone?: 'green' | 'amber' }) {
  const percent = value === null ? 0 : Math.min(100, Math.max(0, value * 100))
  return (
    <span className={`progress ${tone ? `is-${tone}` : ''}`} aria-hidden="true">
      <span style={{ width: `${percent}%` }} />
    </span>
  )
}

export function Avatar({ name, index = 0, small = false }: { name: string; index?: number; small?: boolean }) {
  const initial = name.trim().slice(0, 1) || '?'
  return <span className={`avatar avatar-${index % 5} ${small ? 'small' : ''}`}>{initial}</span>
}

export function Pagination({
  hasPrevious,
  hasNext,
  onPrevious,
  onNext,
  page,
  info,
  disabled = false,
}: {
  hasPrevious: boolean
  hasNext: boolean
  onPrevious: () => void
  onNext: () => void
  page: number
  info: string
  disabled?: boolean
}) {
  return (
    <div className="pagination">
      <span className="pagination-info">{info}</span>
      <div className="pagination-actions">
        <span className="page-number">第 {page} 页</span>
        <IconButton label="上一页" disabled={!hasPrevious || disabled} onClick={onPrevious}>
          <ChevronLeft size={15} />
        </IconButton>
        <IconButton label="下一页" disabled={!hasNext || disabled} onClick={onNext}>
          <ChevronRight size={15} />
        </IconButton>
      </div>
    </div>
  )
}

export function Money({ minor, signed = false }: { minor: string | null; signed?: boolean }) {
  return <span className="number">{formatMoneyMinor(minor, { sign: signed })}</span>
}

export function CheckBox({
  checked,
  onChange,
  label,
}: {
  checked: boolean
  onChange: () => void
  label: string
}) {
  return (
    <label className="check-label" title={label}>
      <input type="checkbox" checked={checked} onChange={onChange} aria-label={label} />
      {checked ? <Check size={10} aria-hidden="true" style={{ display: 'none' }} /> : null}
    </label>
  )
}

export function SectionHint({ children }: { children: ReactNode }) {
  return <div className="form-hint">{children}</div>
}
