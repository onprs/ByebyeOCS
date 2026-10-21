const yuanFormatter = new Intl.NumberFormat('zh-CN', {
  maximumFractionDigits: 0,
})

export function formatMoneyMinor(value: string | null | undefined, options: { sign?: boolean } = {}): string {
  if (value === null || value === undefined || value === '') return '未提供'
  let minor: bigint
  try {
    if (!/^-?\d+$/.test(value)) return '格式异常'
    minor = BigInt(value)
  } catch {
    return '格式异常'
  }
  const negative = minor < 0n
  const absolute = negative ? -minor : minor
  const yuan = absolute / 100n
  const cents = absolute % 100n
  const amount = yuanFormatter.format(Number(yuan)) + '.' + cents.toString().padStart(2, '0')
  if (negative) return `-¥${amount}`
  return options.sign ? `+¥${amount}` : `¥${amount}`
}

export function minorToYuanInput(value: string | null | undefined): string {
  if (value === null || value === undefined || value === '' || !/^-?\d+$/.test(value)) return ''
  const minor = BigInt(value)
  const negative = minor < 0n
  const absolute = negative ? -minor : minor
  const yuan = absolute / 100n
  const cents = (absolute % 100n).toString().padStart(2, '0')
  return `${negative ? '-' : ''}${yuan.toString()}.${cents}`
}

export function formatPriceMicros(value: string | null | undefined): string {
  if (value === null || value === undefined || value === '') return '价格待公布'
  if (!/^-?\d+$/.test(value)) return '格式异常'
  const micros = BigInt(value)
  const negative = micros < 0n
  const absolute = negative ? -micros : micros
  const yuan = absolute / 1_000_000n
  const remainder = absolute % 1_000_000n
  const fraction = remainder.toString().padStart(6, '0').replace(/0+$/, '')
  const grouped = new Intl.NumberFormat('zh-CN').format(Number(yuan))
  return `${negative ? '-' : ''}¥${grouped}${fraction ? `.${fraction}` : ''}`
}

export function formatNumber(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return '未提供'
  return new Intl.NumberFormat('zh-CN').format(value)
}

export function parseYuanToMinor(input: string): string | null {
  const normalized = input.trim()
  if (!/^-?\d+(\.\d{1,2})?$/.test(normalized)) return null
  const negative = normalized.startsWith('-')
  const [yuanPart, centPart = ''] = normalized.replace('-', '').split('.')
  if (!yuanPart) return null
  const minor = BigInt(yuanPart) * 100n + BigInt(centPart.padEnd(2, '0'))
  if (minor === 0n) return '0'
  return `${negative ? '-' : ''}${minor.toString()}`
}

export function parseYuanToMicros(input: string): string | null {
  const normalized = input.trim()
  if (!/^-?\d+(\.\d{1,6})?$/.test(normalized)) return null
  const negative = normalized.startsWith('-')
  const [yuanPart, microPart = ''] = normalized.replace('-', '').split('.')
  if (!yuanPart) return null
  const micros = BigInt(yuanPart) * 1_000_000n + BigInt(microPart.padEnd(6, '0'))
  return `${negative ? '-' : ''}${micros.toString()}`
}

export function formatDateTime(value: string | null | undefined): string {
  if (!value) return '未提供'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '时间格式异常'
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(date)
}

export function formatDate(value: string | null | undefined): string {
  if (!value) return '未提供'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '时间格式异常'
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(date)
}

export function formatPercent(fraction: number | null | undefined): string {
  if (fraction === null || fraction === undefined || !Number.isFinite(fraction)) return '未提供'
  const percent = Math.min(1, Math.max(0, fraction)) * 100
  return `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(percent)}%`
}

export function formatDuration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds)) return '未提供'
  if (seconds < 60) return `${Math.round(seconds)} 秒`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟`
  const hours = Math.floor(minutes / 60)
  return `${hours} 小时 ${minutes % 60} 分钟`
}

export function formatRetryAfter(seconds: number | null | undefined): string {
  if (!seconds || seconds <= 0) return ''
  if (seconds < 60) return `${seconds} 秒后可重试`
  return `${Math.ceil(seconds / 60)} 分钟后可重试`
}

export function truncateMiddle(value: string, head = 8, tail = 6): string {
  if (value.length <= head + tail + 1) return value
  return `${value.slice(0, head)}…${value.slice(-tail)}`
}

export function localTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || '本地时区'
}

export function toCsvValue(value: string | number | null | undefined): string {
  const text = value === null || value === undefined ? '' : String(value)
  return `"${text.replaceAll('"', '""')}"`
}

export function microsToYuanInput(value: string | null | undefined): string {
  if (value === null || value === undefined || value === '' || !/^-?\d+$/.test(value)) return ''
  const micros = BigInt(value)
  const negative = micros < 0n
  const absolute = negative ? -micros : micros
  const yuan = absolute / 1_000_000n
  const fraction = (absolute % 1_000_000n).toString().padStart(6, '0').replace(/0+$/, '')
  return `${negative ? '-' : ''}${yuan.toString()}${fraction ? `.${fraction}` : ''}`
}
