// 统一开发日志输出：带时间戳、对齐标签与可选终端配色，不记录私密内容。

const RESET = '\x1b[0m'
const DIM = '\x1b[2m'
const BOLD = '\x1b[1m'
const COLORS = {
  dev: '\x1b[36m',     // 青色
  db: '\x1b[35m',      // 紫色
  server: '\x1b[32m',  // 绿色
  tiku: '\x1b[33m',    // 黄色
  admin: '\x1b[34m',   // 蓝色
  client: '\x1b[96m',  // 亮青
  proxy: '\x1b[90m',   // 灰
  tokens: '\x1b[95m',  // 亮紫
  proto: '\x1b[94m',   // 亮蓝
  warn: '\x1b[33m',
  error: '\x1b[31m',
  success: '\x1b[32m',
}

const TAG_WIDTH = 7

function timestamp() {
  const now = new Date()
  const hh = String(now.getHours()).padStart(2, '0')
  const mm = String(now.getMinutes()).padStart(2, '0')
  const ss = String(now.getSeconds()).padStart(2, '0')
  return `${hh}:${mm}:${ss}`
}

export class DevLogger {
  constructor({ useColor = Boolean(process.stdout.isTTY && !process.env.NO_COLOR) } = {}) {
    this.useColor = useColor
  }

  colorize(code, text) {
    if (!this.useColor || !code) return text
    return `${code}${text}${RESET}`
  }

  prefix(scope) {
    const padded = String(scope).slice(0, TAG_WIDTH).padEnd(TAG_WIDTH, ' ')
    const time = this.colorize(DIM, timestamp())
    const tag = this.colorize(COLORS[scope] || COLORS.dev, `[${padded}]`)
    return `${time} ${tag}`
  }

  info(scope, message) {
    process.stdout.write(`${this.prefix(scope)} ${message}\n`)
  }

  success(scope, message) {
    const text = this.colorize(COLORS.success, message)
    process.stdout.write(`${this.prefix(scope)} ${text}\n`)
  }

  warn(scope, message) {
    const text = this.colorize(COLORS.warn, `警告：${message}`)
    process.stderr.write(`${this.prefix(scope)} ${text}\n`)
  }

  error(scope, message) {
    const text = this.colorize(COLORS.error, `错误：${message}`)
    process.stderr.write(`${this.prefix(scope)} ${text}\n`)
  }

  // 透传子进程单行输出；对 Go slog JSON 日志做紧凑展示以提高调试可读性。
  childLine(scope, rawLine, isStderr = false) {
    const line = rawLine.replace(/\r$/, '')
    if (!line.trim()) return
    const formatted = formatGoJsonLog(line)
    const out = isStderr ? process.stderr : process.stdout
    out.write(`${this.prefix(scope)} ${formatted}\n`)
  }

  divider(title = '') {
    const line = title ? `── ${title} ` + '─'.repeat(Math.max(4, 54 - title.length)) : '─'.repeat(60)
    process.stdout.write(`\n${this.colorize(BOLD, line)}\n`)
  }
}

function formatGoJsonLog(line) {
  if (!line.startsWith('{') || !line.endsWith('}')) return line
  try {
    const parsed = JSON.parse(line)
    if (typeof parsed !== 'object' || parsed === null || typeof parsed.msg !== 'string') {
      return line
    }
    const level = typeof parsed.level === 'string' ? parsed.level.toUpperCase() : 'INFO'
    const extras = []
    for (const [key, value] of Object.entries(parsed)) {
      if (key === 'time' || key === 'level' || key === 'msg') continue
      if (value === undefined || value === null || value === '') continue
      const rendered = typeof value === 'string' ? value : JSON.stringify(value)
      extras.push(`${key}=${rendered}`)
    }
    return extras.length > 0 ? `[${level}] ${parsed.msg} (${extras.join(' ')})` : `[${level}] ${parsed.msg}`
  } catch {
    return line
  }
}
