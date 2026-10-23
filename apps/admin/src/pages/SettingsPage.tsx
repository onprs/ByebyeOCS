import { useEffect, useMemo, useState } from 'react'
import {
  Check,
  CircleAlert,
  Cloud,
  Cpu,
  Globe2,
  HardDrive,
  Palette,
  RefreshCw,
  Save,
  Server,
  ShieldCheck,
  Wallet,
} from 'lucide-react'
import { useLocation, useNavigate } from 'react-router-dom'
import { getBillingSettings, getHealth, getMaintenance, getRuntimeSettings, updateBillingSettings } from '../api/endpoints'
import type { RuntimeSettings } from '../api/types'
import { useAuth } from '../auth/AuthProvider'
import { hasPermission } from '../auth/permissions'
import {
  Badge,
  Button,
  Callout,
  Card,
  CardHeader,
  ErrorState,
  Field,
  LoadingBlock,
  PageHeader,
  RefreshError,
  Select,
  Switch,
  useToast,
} from '../components'
import { describeError } from '../components/ui'
import {
  formatDateTime,
  formatPriceMicros,
  microsToYuanInput,
  minorToYuanInput,
  parseYuanToMicros,
  parseYuanToMinor,
} from '../lib/format'
import { useAsync } from '../lib/hooks'
import { colorSchemeOptions, themeModeOptions, useAppearance } from '../theme/AppearanceProvider'

type SettingsSection = 'appearance' | 'billing' | 'runtime' | 'maintenance'

const sections: Array<{ id: SettingsSection; label: string; icon: typeof Palette }> = [
  { id: 'appearance', label: '外观偏好', icon: Palette },
  { id: 'billing', label: '计费与价格', icon: Wallet },
  { id: 'runtime', label: '运行安全', icon: ShieldCheck },
  { id: 'maintenance', label: '维护信息', icon: Server },
]

function sectionFromQuery(raw: string | null): SettingsSection {
  if (raw === 'appearance' || raw === 'billing' || raw === 'runtime' || raw === 'maintenance') return raw
  if (raw === 'security') return 'runtime'
  return 'appearance'
}

export function SettingsPage() {
  const location = useLocation()
  const navigate = useNavigate()
  const { me } = useAuth()
  const initial = sectionFromQuery(new URLSearchParams(location.search).get('section'))
  const [section, setSection] = useState<SettingsSection>(initial)
  const allowedSections = sections.filter(item => {
    if (item.id === 'appearance') return true
    if (item.id === 'billing') return hasPermission(me, 'billing.read')
    return hasPermission(me, 'operations.read')
  })
  const effectiveSection = allowedSections.some(item => item.id === section) ? section : 'appearance'

  const select = (next: SettingsSection) => {
    if (!allowedSections.some(item => item.id === next)) return
    setSection(next)
    navigate(`/settings?section=${next}`, { replace: true })
  }

  return (
    <>
      <PageHeader
        eyebrow="平台设置"
        title="系统设置"
        description="外观偏好跨设备同步；计费与运行参数变更会保留版本。"
        actions={<Badge tone="green"><ShieldCheck size={12} /> 敏感值不在此页面回显</Badge>}
      />

      <div className="settings-layout">
        <nav className="settings-nav" aria-label="设置分组">
          {allowedSections.map(item => (
            <button key={item.id} className={effectiveSection === item.id ? 'active' : ''} onClick={() => select(item.id)}>
              <item.icon size={15} />
              {item.label}
            </button>
          ))}
        </nav>
        <div>
          {effectiveSection === 'appearance' ? <AppearancePanel /> : null}
          {effectiveSection === 'billing' ? <BillingPanel /> : null}
          {effectiveSection === 'runtime' ? <RuntimePanel /> : null}
          {effectiveSection === 'maintenance' ? <MaintenancePanel /> : null}
        </div>
      </div>
    </>
  )
}

function AppearancePanel() {
  const { preferences, status, error, saving, effectiveTheme, updateAppearance, reload } = useAppearance()
  const unavailable = status === 'unavailable'
  const disabled = saving || unavailable || status === 'loading'

  return (
    <div className="settings-stack">
      <Card>
        <CardHeader
          title="外观偏好"
          subtitle="保存后会在其他管理设备使用，不影响普通用户偏好。"
          actions={
            <Badge tone={status === 'ready' ? 'green' : status === 'unavailable' ? 'amber' : status === 'error' ? 'red' : 'neutral'}>
              {status === 'ready' ? '已同步' : status === 'loading' ? '同步中' : status === 'unavailable' ? '同步未配置' : status === 'error' ? '同步失败' : '未同步'}
            </Badge>
          }
        />
        <div className="settings-body">
          {unavailable ? (
            <div style={{ marginBottom: 18 }}>
              <Callout tone="warning">
                外观同步暂不可用，修改无法保存到其他设备。当前使用默认外观。请稍后重新读取。
              </Callout>
              <div style={{ marginTop: 10 }}>
                <Button size="sm" onClick={reload}><RefreshCw size={13} /> 重新读取偏好</Button>
              </div>
            </div>
          ) : null}
          {error && !unavailable ? <div style={{ marginBottom: 18 }}><ErrorState error={error} onRetry={reload} compact /></div> : null}

          <div className="settings-block">
            <div className="settings-block-heading">
              <h3>配色方案</h3>
              <p>默认石墨。切换配色不改变权限、路由或数据。</p>
            </div>
            <div className="palette-options" role="group" aria-label="选择配色方案">
              {colorSchemeOptions.map(option => (
                <button
                  type="button"
                  key={option.value}
                  className={`palette-option palette-${option.value} ${preferences.color_scheme === option.value ? 'selected' : ''}`}
                  aria-pressed={preferences.color_scheme === option.value}
                  disabled={disabled}
                  onClick={() => void updateAppearance({ color_scheme: option.value })}
                >
                  <span className="palette-sample" aria-hidden="true"><i /><span><b /><em /><em /></span></span>
                  <span className="palette-caption">
                    <b>{option.label}</b>
                    {preferences.color_scheme === option.value ? <Check size={12} /> : option.value === 'graphite' ? <small>默认</small> : null}
                  </span>
                  <small className="palette-description">{option.description}</small>
                </button>
              ))}
            </div>
          </div>

          <div className="settings-block">
            <div className="settings-block-heading">
              <h3>界面主题</h3>
              <p>可跟随设备，也可以固定浅色或深色。当前有效主题：{effectiveTheme === 'dark' ? '深色' : '浅色'}。</p>
            </div>
            <div className="theme-mode-options" role="group" aria-label="选择界面主题">
              {themeModeOptions.map(option => (
                <button
                  type="button"
                  key={option.value}
                  className={preferences.theme_mode === option.value ? 'selected' : ''}
                  aria-pressed={preferences.theme_mode === option.value}
                  disabled={disabled}
                  onClick={() => void updateAppearance({ theme_mode: option.value })}
                >
                  <b>{option.label}</b>
                  <span>{option.description}</span>
                </button>
              ))}
            </div>
          </div>

          <div className="settings-save-row">
            <span className="form-hint">
              当前偏好版本：{preferences.version > 0 ? `v${preferences.version}` : '未提供版本'}
              {saving ? ' · 正在保存' : ''}
            </span>
            <Button onClick={reload} disabled={saving}><RefreshCw size={13} /> 重新读取</Button>
          </div>
        </div>
      </Card>
    </div>
  )
}

function BillingPanel() {
  const { notify } = useToast()
  const { me } = useAuth()
  const canManage = hasPermission(me, 'billing.manage')
  const navigate = useNavigate()
  const billing = useAsync(() => getBillingSettings().then(response => response.data), [])
  const [servicePrice, setServicePrice] = useState('')
  const [unitLabel, setUnitLabel] = useState('有效任务点')
  const [unitPrecision, setUnitPrecision] = useState('0')
  const [settlementRule, setSettlementRule] = useState('per_event')
  const [rounding, setRounding] = useState('ceil')
  const [minCharge, setMinCharge] = useState('0.00')
  const [minRecharge, setMinRecharge] = useState('')
  const [maxRecharge, setMaxRecharge] = useState('')
  const [defaultChannel, setDefaultChannel] = useState('alipay')
  const [serviceCharge, setServiceCharge] = useState(false)
  const [tikuCharge, setTikuCharge] = useState(false)
  const [checkoutEnabled, setCheckoutEnabled] = useState(false)
  const [refundEnabled, setRefundEnabled] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<unknown | null>(null)

  useEffect(() => {
    const data = billing.data
    if (!data) return
    setServicePrice(microsToYuanInput(data.service.price_micros))
    setUnitLabel(data.service.unit_label ?? '有效任务点')
    setUnitPrecision(String(data.service.unit_precision ?? 0))
    setSettlementRule(data.service.settlement_rule ?? 'per_event')
    setRounding(data.service.rounding ?? 'ceil')
    setMinCharge(minorToYuanInput(data.service.min_charge_minor) || '0.00')
    setMinRecharge(minorToYuanInput(data.min_recharge_minor))
    setMaxRecharge(minorToYuanInput(data.max_recharge_minor))
    setDefaultChannel(data.default_channel ?? 'alipay')
    setServiceCharge(data.service_charge_enabled)
    setTikuCharge(data.tiku_charge_enabled)
    setCheckoutEnabled(data.zpay_checkout_enabled)
    setRefundEnabled(data.zpay_refund_enabled)
  }, [billing.data])

  const configured = billing.data?.service.configured ?? false
  const automatic = billing.data?.automatic_charging ?? false

  const save = async () => {
    if (!billing.data) return
    setError(null)
    const priceMicros = servicePrice.trim() ? parseYuanToMicros(servicePrice) : null
    if (servicePrice.trim() && (priceMicros === null || BigInt(priceMicros) <= 0n)) {
      setError(new Error('服务单价必须是大于零的金额。'))
      return
    }
    if (!servicePrice.trim() && billing.data.service.configured) {
      setError(new Error('服务价格已配置，不能通过留空清除；请填写新的价格。'))
      return
    }
    const precision = Number(unitPrecision)
    if (!Number.isInteger(precision) || precision < 0 || precision > 6) {
      setError(new Error('单位精度必须是 0 到 6 的整数。'))
      return
    }
    const minChargeMinor = minCharge.trim() ? parseYuanToMinor(minCharge) : '0'
    const minRechargeMinor = minRecharge.trim() ? parseYuanToMinor(minRecharge) : null
    const maxRechargeMinor = maxRecharge.trim() ? parseYuanToMinor(maxRecharge) : null
    if (minChargeMinor === null || (minRecharge.trim() && minRechargeMinor === null) || (maxRecharge.trim() && maxRechargeMinor === null)) {
      setError(new Error('金额格式无效，最多两位小数。'))
      return
    }
    if (serviceCharge && priceMicros === null) {
      setError(new Error('启用服务扣费前必须配置服务价格。'))
      return
    }
    const currentService = billing.data.service
    const priceChanged =
      (currentService.price_micros ?? '') !== (priceMicros ?? '') ||
      (currentService.unit_precision ?? 0) !== precision ||
      (currentService.settlement_rule ?? 'per_event') !== settlementRule ||
      (currentService.rounding ?? 'ceil') !== rounding ||
      (currentService.min_charge_minor ?? '0') !== (minChargeMinor ?? '0') ||
      (currentService.unit_label ?? '有效任务点') !== (unitLabel.trim() || '有效任务点')
    setSaving(true)
    try {
      const response = await updateBillingSettings(
        {
          service_charge_enabled: serviceCharge,
          tiku_charge_enabled: tikuCharge,
          zpay_checkout_enabled: checkoutEnabled,
          zpay_refund_enabled: refundEnabled,
          default_channel: defaultChannel,
          min_recharge_minor: minRechargeMinor,
          max_recharge_minor: maxRechargeMinor,
          ...(priceMicros && priceChanged
            ? {
                service_price: {
                  price_micros: priceMicros,
                  unit_precision: precision,
                  settlement_rule: settlementRule,
                  rounding,
                  min_charge_minor: minChargeMinor ?? '0',
                  unit_label: unitLabel.trim() || '有效任务点',
                },
              }
            : {}),
        },
        billing.data.version ?? 0,
        billing.data,
      )
      billing.setData(response.data)
      notify('计费设置已保存。', 'success')
    } catch (reason) {
      setError(reason)
      notify(describeError(reason).message, 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="settings-stack">
      <Card>
        <CardHeader
          title="计费与价格"
          subtitle="主服务按平台已确认的有效任务点计量；价格版本保留历史版本。"
          actions={<Badge tone={configured ? 'green' : 'amber'}>{configured ? '服务价格已配置' : '服务价格未配置'}</Badge>}
        />
        <div className="settings-body">
          {billing.error && billing.data ? <RefreshError error={billing.error} onRetry={billing.reload} /> : null}
          {billing.loading && !billing.data ? <LoadingBlock rows={4} /> : null}
          {billing.error && !billing.data ? <ErrorState error={billing.error} onRetry={billing.reload} /> : null}
          {billing.data ? (
            <>
              <div className="billing-status">
                <div><span>服务价格</span><b>{formatPriceMicros(billing.data.service.price_micros)} / {billing.data.service.unit_label ?? '单位'}</b></div>
                <div><span>价格版本</span><b>{billing.data.service.price_version ?? '未提供'}</b></div>
                <div><span>实际扣费</span><b>{automatic ? '已启用' : '未启用'}</b></div>
                <div><span>最近更新</span><b>{formatDateTime(billing.data.updated_at)}</b></div>
              </div>
              <div className="field-row">
                <Field label="有效任务点单价（元 / 单位）" hint={canManage ? '留空表示未配置。按整数微元保存。' : '当前角色只有计费读取权限。'}>
                  <input value={servicePrice} onChange={event => setServicePrice(event.target.value)} inputMode="decimal" placeholder="未配置" disabled={saving || !canManage} />
                </Field>
                <Field label="计价单位" hint="例如“有效任务点”。">
                  <input value={unitLabel} onChange={event => setUnitLabel(event.target.value)} maxLength={40} disabled={saving || !canManage} />
                </Field>
              </div>
              <div className="field-row">
                <Field label="单位精度" hint="0 到 6 位小数。">
                  <input value={unitPrecision} onChange={event => setUnitPrecision(event.target.value)} inputMode="numeric" disabled={saving || !canManage} />
                </Field>
                <Field label="最低扣费（元）" hint="单次结算最低金额，可为 0。">
                  <input value={minCharge} onChange={event => setMinCharge(event.target.value)} inputMode="decimal" disabled={saving || !canManage} />
                </Field>
              </div>
              <div className="field-row">
                <Field label="结算规则">
                  <Select
                    label="结算规则"
                    value={settlementRule}
                    onChange={setSettlementRule}
                    options={[
                      { value: 'per_event', label: '按事件' },
                      { value: 'per_event_min_charge', label: '按事件并应用最低扣费' },
                    ]}
                    disabled={saving || !canManage}
                  />
                </Field>
                <Field label="取整方式">
                  <Select label="取整方式" value={rounding} onChange={setRounding} options={[{ value: 'ceil', label: '向上取整' }, { value: 'floor', label: '向下取整' }, { value: 'half_up', label: '按四舍五入' }]} disabled={saving || !canManage} />
                </Field>
              </div>

              <div className="settings-section-title">计费开关</div>
              <div className="settings-toggle">
                <div><b>主服务扣费开关</b><p>仅对平台已确认、此前未计费的有效任务点计量。实际扣费还需运行时授权（当前{automatic ? '已授权' : '未授权'}）。</p></div>
                <Switch label="主服务扣费开关" checked={serviceCharge} onChange={setServiceCharge} disabled={saving || !canManage} />
              </div>
              <div className="settings-toggle">
                <div><b>题库扣费开关</b><p>题库独立收费；未定价、用量不完整或运行时未授权时不自动扣费。</p></div>
                <Switch label="题库扣费开关" checked={tikuCharge} onChange={setTikuCharge} disabled={saving || !canManage} />
              </div>
              <div className="settings-facts">
                <div><span>服务扣费设置</span><b>{billing.data.service_charge_enabled ? '已开启' : '关闭'}</b></div>
                <div><span>题库扣费设置</span><b>{billing.data.tiku_charge_enabled ? '已开启' : '关闭'}</b></div>
                <div><span>运行时扣费授权</span><b>{billing.data.charging_allowed ? '允许' : '不允许'}</b></div>
                <div><span>服务实际扣费</span><b>{billing.data.effective_service_charge_enabled ? '生效' : '不生效'}</b></div>
                <div><span>题库实际扣费</span><b>{billing.data.effective_tiku_charge_enabled ? '生效' : '不生效'}</b></div>
              </div>
              {serviceCharge && !billing.data.charging_allowed ? (
                <Callout tone="warning" icon={<CircleAlert size={15} />}>
                  扣费开关已打开，但当前运行时未授权实际扣费，不会产生扣费。
                </Callout>
              ) : null}

              <div className="settings-section-title">支付渠道</div>
              <div className="billing-status" style={{ gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' }}>
                <div><span>服务商</span><b>{billing.data.payment_provider ?? '未配置'}</b></div>
                <div><span>收银台</span><b>{billing.data.checkout_ready ? '已就绪' : '未就绪'}</b></div>
                <div><span>退款能力</span><b>{billing.data.refund_ready ? '已就绪' : '未就绪'}</b></div>
              </div>
              <div className="field-row">
                <Field label="默认支付渠道">
                  <Select
                    label="默认支付渠道"
                    value={defaultChannel}
                    onChange={setDefaultChannel}
                    options={[{ value: 'alipay', label: '支付宝' }, { value: 'wxpay', label: '微信支付' }]}
                    disabled={saving || !canManage}
                  />
                </Field>
                <Field label="充值金额范围（元）" hint="最小与最大值的格式为最多两位小数。">
                  <div className="inline-fields">
                    <input value={minRecharge} onChange={event => setMinRecharge(event.target.value)} inputMode="decimal" placeholder="最小" disabled={saving || !canManage} />
                    <span>—</span>
                    <input value={maxRecharge} onChange={event => setMaxRecharge(event.target.value)} inputMode="decimal" placeholder="最大" disabled={saving || !canManage} />
                  </div>
                </Field>
              </div>
              <div className="settings-inline-toggles">
                <label className="native-check"><input type="checkbox" checked={checkoutEnabled} onChange={event => setCheckoutEnabled(event.target.checked)} disabled={saving || !canManage} />启用 ZPay 收银台</label>
                <label className="native-check"><input type="checkbox" checked={refundEnabled} onChange={event => setRefundEnabled(event.target.checked)} disabled={saving || !canManage} />启用 ZPay 退款</label>
              </div>
              {serviceCharge && !configured ? (
                <Callout tone="warning" icon={<CircleAlert size={15} />}>
                  服务价格尚未配置；保存前请先补全，页面不会自动启用扣费。
                </Callout>
              ) : null}
              {error ? <div style={{ marginTop: 14 }}><ErrorState error={error} compact /></div> : null}
              <div className="settings-save-row">
                <span className="form-hint">配置版本：{(billing.data.version ?? 0) > 0 ? `v${billing.data.version}` : '未提供'}</span>
                <Button onClick={billing.reload} disabled={saving}><RefreshCw size={13} /> 重新读取</Button>
                {canManage ? (
                  <Button variant="primary" onClick={() => void save()} loading={saving}>
                    <Save size={13} /> 保存计费设置
                  </Button>
                ) : null}
              </div>
            </>
          ) : null}
        </div>
      </Card>

      <Card>
        <CardHeader
          title="题库计价"
          subtitle="题库按公开模型的 token 价格单独计费，不在全局设置中维护费率。"
          actions={<Button size="sm" onClick={() => navigate('/questions')}>管理模型与价格</Button>}
        />
        <div className="settings-body" style={{ paddingTop: 16 }}>
          <div className="billing-status" style={{ gridTemplateColumns: 'repeat(2, minmax(0, 1fr))' }}>
            <div><span>题库扣费开关</span><b>{billing.data?.tiku_charge_enabled ? '已启用' : '未启用'}</b></div>
            <div><span>计价方式</span><b>按公开模型 token 价格</b></div>
          </div>
          <p className="form-hint" style={{ marginTop: 10 }}>
            输入、输出价格与缓存策略在“题库与模型”按条目维护；实际查询使用所选的已发布模型价格。
          </p>
        </div>
      </Card>
    </div>
  )
}

function backupStatusLabel(value: string | null | undefined): string {
  const labels: Record<string, string> = {
    ok: '已验证',
    not_configured: '未配置',
    unavailable: '不可读取',
    unverified: '未通过校验',
    invalid_clock: '时间异常',
    stale: '已过期',
    unknown: '未知',
  }
  return labels[value ?? 'unknown'] ?? value ?? '未知'
}

function storageStatusLabel(value: string | null | undefined): string {
  const labels: Record<string, string> = {
    ok: '正常',
    warning: '空间偏紧',
    protected: '受保护',
    maintenance: '维护中',
    unknown: '未知',
  }
  return labels[value ?? 'unknown'] ?? value ?? '未知'
}

function byteLabel(value: string | null): string {
  if (!value || !/^\d+$/.test(value)) return '未提供'
  const bytes = BigInt(value)
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let index = 0
  let scaled = bytes
  while (scaled >= 1024n && index < units.length - 1) {
    scaled /= 1024n
    index += 1
  }
  return `${scaled.toString()} ${units[index]}`
}

const runtimeKeyLabels: Record<string, string> = {
  global_concurrency: '全局执行名额',
  account_concurrency: '账号默认并发',
  execution_enabled: '任务执行启用',
  instance_id: '节点标识',
  session_ttl: '会话有效期',
  idle_ttl: '空闲会话上限',
  lease_ttl: '任务租约时长',
  heartbeat_interval: '心跳间隔',
  poll_interval: '调度轮询间隔',
  verification_interval: '核验轮询间隔',
  max_attempts: '最大尝试次数',
  retry_base: '重试基础间隔',
  max_verifications: '最大核验次数',
  course_snapshot_ttl: '课程快照有效期',
}

const retentionKeyLabels: Record<string, string> = {
  stream_days: '事件流保留',
  outbox_days: '待投递事件保留',
  question_content_days: '题目内容保留',
  export_hours: '导出链接有效期',
  financial_records: '财务记录',
}

function friendlyFacts(
  items: Array<{ key: string; value: string }>,
  family: 'runtime' | 'retention',
): Array<{ key: string; value: string }> {
  const labels = family === 'runtime' ? runtimeKeyLabels : retentionKeyLabels
  return items.map(item => {
    let value = item.value
    if (value === 'true') value = '已启用'
    else if (value === 'false') value = '未启用'
    else if (value === 'retained') value = '保留'
    else if (family === 'retention' && item.key === 'stream_days') value = `${item.value} 天`
    else if (family === 'retention' && item.key === 'outbox_days') value = `${item.value} 天`
    else if (family === 'retention' && item.key === 'question_content_days') value = `${item.value} 天`
    else if (family === 'retention' && item.key === 'export_hours') value = `${item.value} 小时`
    return { key: labels[item.key] ?? item.key, value }
  })
}

function FactList({ items, empty }: { items: Array<{ key: string; value: string }>; empty: string }) {
  if (!items.length) return <p className="form-hint">{empty}</p>
  return (
    <div className="settings-facts">
      {items.map(item => (
        <div key={item.key}><span>{item.key}</span><b>{item.value}</b></div>
      ))}
    </div>
  )
}

function RuntimePanel() {
  const runtime = useAsync(() => getRuntimeSettings().then(response => response.data), [])

  return (
    <div className="settings-stack">
      <Card>
        <CardHeader
          title="运行与并发"
          subtitle="当前运行的只读参数；运行参数在此仅查看，修改请通过部署配置。"
          actions={<Button size="sm" onClick={runtime.reload} loading={runtime.loading}><RefreshCw size={13} /> 刷新</Button>}
        />
        <div className="settings-body">
          {runtime.loading && !runtime.data ? <LoadingBlock rows={3} /> : null}
          {runtime.error && !runtime.data ? <ErrorState error={runtime.error} onRetry={runtime.reload} /> : null}
          {runtime.data ? (
            <>
              <div className="detail-section-title"><Server size={13} /> 运行时配置</div>
              <FactList items={friendlyFacts(runtime.data.runtime, 'runtime')} empty="暂无可展示的运行时配置。" />
              <div className="detail-section-title" style={{ marginTop: 20 }}><HardDrive size={13} /> 存储状态</div>
              <div className="settings-facts">
                <div><span>存储状态</span><b>{storageStatusLabel(runtime.data.storage?.status)}</b></div>
                <div><span>使用率</span><b>{runtime.data.storage?.used_percent === null || runtime.data.storage?.used_percent === undefined ? '未提供' : `${runtime.data.storage.used_percent.toFixed(1)}%`}</b></div>
                <div><span>可用空间</span><b>{byteLabel(runtime.data.storage?.available_bytes ?? null)}</b></div>
              </div>
              <div className="detail-section-title" style={{ marginTop: 20 }}><Cloud size={13} /> 备份状态</div>
              <div className="settings-facts">
                <div><span>备份状态</span><b>{backupStatusLabel(runtime.data.backup?.status)}</b></div>
                <div><span>最近可恢复点</span><b>{formatDateTime(runtime.data.backup?.last_recoverable_at ?? null)}</b></div>
                <div><span>最近验证</span><b>{formatDateTime(runtime.data.backup?.verified_at ?? null)}</b></div>
                <div><span>是否要求新备份</span><b>{runtime.data.backup?.required === null || runtime.data.backup?.required === undefined ? '未提供' : runtime.data.backup.required ? '要求' : '不要求'}</b></div>
              </div>
              <div className="detail-section-title" style={{ marginTop: 20 }}><Cloud size={13} /> 数据保留</div>
              <FactList items={friendlyFacts(runtime.data.retention, 'retention')} empty="暂无数据保留配置。" />
              <p className="form-hint" style={{ marginTop: 14 }}>生成时间：{formatDateTime(runtime.data.generated_at)}</p>
            </>
          ) : null}
        </div>
      </Card>

      <Card>
        <CardHeader title="安全约束" subtitle="以下安全约束始终生效。" />
        <div className="security-rules">
          <div><ShieldCheck size={15} /><span><b>凭据加密与脱敏</b><small>密码、Cookie、会话令牌和支付密钥不会在页面或日志中展示。</small></span><Badge tone="green">强制</Badge></div>
          <div><Cpu size={15} /><span><b>同任务独占</b><small>同一账号的同一任务不会重复执行，不同任务可在限额内并发。</small></span><Badge tone="green">强制</Badge></div>
          <div><CircleAlert size={15} /><span><b>异常停止</b><small>验证码、身份失效、403 与 429 停止相关操作，不无限重试。</small></span><Badge tone="green">强制</Badge></div>
          <div><Wallet size={15} /><span><b>敏感财务验证</b><small>人工调整、退款与对账要求管理员再次验证和独立理由。</small></span><Badge tone="green">强制</Badge></div>
        </div>
      </Card>
    </div>
  )
}

const featureLabels: Record<string, string> = {
  learning: '学习与任务',
  billing: '账本与支付',
  questions: '题库查询',
  operations: '维护与审计',
  preferences: '外观偏好',
  events: '实时事件',
  course_sync: '课程同步',
  scheduling: '任务调度',
  task_execution: '任务执行',
  wallet: '钱包',
  payments: '支付',
  tiku: '题库',
  dashboard: '运行总览',
  users: '用户与账号',
  tasks: '任务监控',
  finance: '财务与订单',
  question_bank: '题库与模型',
}

function authMethodLabel(method: string): string {
  if (method === 'password+totp') return '密码 + 动态验证码'
  if (method === 'password_totp') return '密码 + 动态验证码'
  if (method === 'password') return '密码'
  if (method === 'totp') return '动态验证码'
  return method
}

function MaintenancePanel() {
  const { capabilities } = useAuth()
  const live = useAsync(() => getHealth('live').then(response => response.data), [])
  const ready = useAsync(() => getHealth('ready').then(response => response.data), [])
  const maintenance = useAsync(() => getMaintenance().then(response => response.data), [])
  const features = useMemo(() => Object.entries(capabilities?.features ?? {}), [capabilities?.features])
  const data: RuntimeSettings | null = maintenance.data

  return (
    <div className="settings-stack">
      <Card>
        <CardHeader
          title="服务健康"
          subtitle="检查服务就绪状态，不代表外部平台或支付渠道可用。"
          actions={<Button size="sm" onClick={() => { live.reload(); ready.reload(); maintenance.reload() }}><RefreshCw size={13} /> 检查</Button>}
        />
        <div className="settings-body">
          <div className="health-grid">
            <div>
              <span className={`service-dot ${live.data?.status === 'ok' ? 'healthy' : live.error ? 'unavailable' : 'degraded'}`} />
              <div>
                <b>存活检查</b>
                <span>{live.data?.status === 'ok' ? '服务存活' : live.error ? describeError(live.error).message : '检查中'}</span>
              </div>
            </div>
            <div>
              <span className={`service-dot ${ready.data?.status === 'ok' ? 'healthy' : ready.error ? 'unavailable' : 'degraded'}`} />
              <div>
                <b>就绪检查</b>
                <span>{ready.data?.status === 'ok' ? '服务已就绪' : ready.error ? describeError(ready.error).message : '检查中'}</span>
              </div>
            </div>
          </div>
          {live.error ? <div style={{ marginTop: 14 }}><ErrorState error={live.error} onRetry={live.reload} compact /></div> : null}
          {ready.error ? <div style={{ marginTop: 10 }}><ErrorState error={ready.error} onRetry={ready.reload} compact /></div> : null}
        </div>
      </Card>

      <Card>
        <CardHeader title="维护与保留" subtitle="查看维护状态与数据保留；此面板不执行命令。" />
        <div className="settings-body">
          {maintenance.loading && !data ? <LoadingBlock rows={3} /> : null}
          {maintenance.error && !data ? <ErrorState error={maintenance.error} onRetry={maintenance.reload} /> : null}
          {data ? (
            <>
              <div className="settings-facts">
                <div><span>存储状态</span><b>{storageStatusLabel(data.storage?.status)}</b></div>
                <div><span>存储使用率</span><b>{data.storage?.used_percent === null || data.storage?.used_percent === undefined ? '未提供' : `${data.storage.used_percent.toFixed(1)}%`}</b></div>
                <div><span>可用空间</span><b>{byteLabel(data.storage?.available_bytes ?? null)}</b></div>
                <div><span>备份状态</span><b>{backupStatusLabel(data.backup?.status)}</b></div>
                <div><span>最近可恢复点</span><b>{formatDateTime(data.backup?.last_recoverable_at ?? null)}</b></div>
                <div><span>最近验证</span><b>{formatDateTime(data.backup?.verified_at ?? null)}</b></div>
              </div>
              <FactList items={friendlyFacts(data.retention, 'retention')} empty="暂无数据保留配置。" />
              <p className="form-hint" style={{ marginTop: 14 }}>生成时间：{formatDateTime(data.generated_at)}</p>
            </>
          ) : null}
        </div>
      </Card>

      <Card>
        <CardHeader title="功能与可用性" subtitle="当前可用的功能与登录方式；关闭的功能不能在本页启用。" />
        <div className="settings-body">
          <div className="settings-facts">
            <div><span>服务版本</span><b>{capabilities?.api_version ?? '未提供'}</b></div>
            <div><span>最小客户端版本</span><b>{capabilities?.minimum_client_version ?? '未要求'}</b></div>
            <div><span>登录验证</span><b>{capabilities?.authentication.enabled ? '已启用' : '未启用'}</b></div>
            <div><span>自动扣费</span><b>{capabilities?.pricing.automatic_charging ? '已启用' : '未启用'}</b></div>
            <div><span>状态生成时间</span><b>{formatDateTime(capabilities?.generated_at ?? null)}</b></div>
          </div>
          <div className="capability-list">
            <div className="detail-section-title"><Cpu size={13} /> 可用功能</div>
            {features.length ? features.map(([key, enabled]) => (
              <div key={key}><span>{featureLabels[key] ?? key}</span><Badge tone={enabled ? 'green' : 'neutral'}>{enabled ? '已启用' : '关闭'}</Badge></div>
            )) : <p className="form-hint">暂无可确认的功能状态。</p>}
          </div>
          <div className="capability-list">
            <div className="detail-section-title"><Globe2 size={13} /> 登录方式</div>
            {capabilities?.authentication.login_methods.length ? (
              capabilities.authentication.login_methods.map(method => <div key={method}><span>{authMethodLabel(method)}</span><Badge tone="neutral">可用</Badge></div>)
            ) : <p className="form-hint">暂无可用登录方式。</p>}
          </div>
        </div>
      </Card>
    </div>
  )
}
