// 命令行参数解析、默认端口计算与帮助文案。

import path from 'node:path'

export const ALL_MODULES = ['db', 'server', 'tiku', 'admin', 'client', 'tokens', 'prototypes']
export const DEFAULT_MODULES = ['db', 'server', 'tiku', 'admin', 'client', 'tokens']

const VALID_COMMANDS = new Set(['up', 'doctor', 'down', 'reset', 'help'])
const VALID_DB_MODES = new Set(['auto', 'docker', 'local-pg', 'external'])
const VALID_CLIENT_MODES = new Set(['auto', 'flutter', 'static', 'off'])
const VALID_RELOAD_MODES = new Set(['hot-reload', 'hot-restart'])
const VALID_LOG_LEVELS = new Set(['debug', 'info', 'warn', 'error'])

export const HELP_TEXT = `ByebyeOCS 一键全栈热重载开发调试脚本（支持 Windows / Ubuntu）

用法：
  node scripts/dev.mjs [子命令] [选项]
  ./scripts/dev.sh [子命令] [选项]          # Ubuntu / macOS
  .\\scripts\\dev.ps1 [子命令] [选项]        # Windows PowerShell

子命令：
  up（默认）    准备开发数据库、初始化本地开发配置与迁移，拉起全部服务并开启实时热生效
  doctor        只读自检开发工具链（Go / Node / Flutter / Docker / PostgreSQL）、子模块与端口占用
  down          停止本工作区启动的开发 PostgreSQL 实例
  reset         彻底重置本工作区的本地开发数据库与配置（需确认或加 --yes）
  help          显示本帮助信息

常用选项：
  --dev-login                     使用 devfixture 标签编译主服务并启用本地合成登录（仅本地联调）
  --only <a,b>                    仅启用指定模块：db,server,tiku,admin,client,tokens,prototypes
  --skip <a,b>                    跳过指定模块（逗号分隔，可重复传入）
  --with-prototypes               一并拉起 prototypes/ 设计原型（默认 127.0.0.1:5186）
  --db <auto|docker|local-pg|external>
                                  开发数据库模式（默认 auto：优先 Docker，其次本机 PostgreSQL）
  --pg-image <image>              Docker PostgreSQL 镜像（默认优先复用本地 16/17 镜像，否则 postgres:17-alpine）
  --main-dsn <url>                使用外部主库 PostgreSQL DSN（自动切换为 external 数据库模式）
  --tiku-dsn <url>                使用外部题库 PostgreSQL DSN
  --client-mode <auto|flutter|static|off>
                                  用户端模式（默认 auto：有 Flutter 启动热重载，否则回落静态构建产物）
  --flutter <path>                指定 Flutter 可执行文件路径（也可设置 FLUTTER 环境变量）
  --flutter-device <device>       Flutter 运行设备（默认 web-server；原生设备自动注入 API_BASE_URL）
  --flutter-reload <hot-reload|hot-restart>
                                  Flutter 文件保存自动触发方式（默认 hot-reload，发送 r）
  --port-offset <n>               整体平移所有默认端口（便于多工作区并行开发）
  --user-port <n>                 用户端同源入口端口（默认 5188）
  --flutter-port <n>              Flutter 内部开发服务端口（默认 5189）
  --admin-port <n>                管理员端 Vite 入口端口，同域挂载 /panel/（默认 5187）
  --server-user-port <n>          主服务用户监听端口（默认 8090）
  --server-admin-port <n>         主服务管理监听端口（默认 8091）
  --tiku-port <n>                 独立题库服务监听端口（默认 8092）
  --tiku-proxy-port <n>           题库面板静态热读内部端口（默认 18092）
  --pg-port <n>                   开发隔离 PostgreSQL 端口（默认 25435）
  --prototypes-port <n>           设计原型端口（默认 5186）
  --log-level <level>             主服务日志级别：debug | info | warn | error（默认 info）
  --state-dir <path>              开发运行态目录（默认 .local/dev-stack）
  --reinit-config                 重新生成开发配置（保留数据库数据）
  --keep-db                       退出脚本时保持开发数据库运行（加速下次启动）
  --no-watch                      仅拉起服务，关闭文件监听自动重启与重载
  --no-auto-install               依赖缺失时不自动执行 npm ci / flutter pub get
  --yes, -y                       reset 时跳过交互确认

交互快捷键（运行中在终端按键）：
  s  重编并重启主服务      t  重编并重启题库服务      m  执行两库数据库迁移
  r  Flutter 热重载        R  Flutter 热重启          d  重新生成设计变量
  p  查看所有访问地址      h  查看快捷键说明          q  优雅停止全部服务
`

function parseIntArg(name, raw, min, max) {
  if (!/^-?\d+$/.test(String(raw ?? ''))) {
    throw new Error(`参数 ${name} 需要整数，收到：${raw}`)
  }
  const value = Number.parseInt(String(raw), 10)
  if (value < min || value > max) {
    throw new Error(`参数 ${name} 超出范围（${min}–${max}）：${value}`)
  }
  return value
}

function parseModuleList(name, raw) {
  const list = String(raw || '')
    .split(',')
    .map((item) => item.trim().toLowerCase())
    .filter(Boolean)
  if (list.length === 0) {
    throw new Error(`参数 ${name} 不能为空`)
  }
  for (const item of list) {
    if (!ALL_MODULES.includes(item)) {
      throw new Error(`参数 ${name} 包含未知模块 "${item}"，可选：${ALL_MODULES.join(', ')}`)
    }
  }
  return list
}

export function parseCliArgs(argv, { repoRoot, env = process.env } = {}) {
  const args = [...argv]
  let command = 'up'
  if (args.length > 0 && !args[0].startsWith('-')) {
    command = args.shift()
  }
  if (command === '-h' || command === '--help') {
    command = 'help'
  }
  if (!VALID_COMMANDS.has(command)) {
    throw new Error(`未知子命令 "${command}"；可用子命令：up、doctor、down、reset、help`)
  }

  let portOffset = env.BYEBYEOCS_DEV_PORT_OFFSET
    ? parseIntArg('BYEBYEOCS_DEV_PORT_OFFSET', env.BYEBYEOCS_DEV_PORT_OFFSET, -2000, 30000)
    : 0
  const explicitPorts = {}
  const onlyList = []
  const skipList = []

  const options = {
    command,
    repoRoot,
    stateDir: env.BYEBYEOCS_DEV_STATE_DIR
      ? path.resolve(repoRoot, env.BYEBYEOCS_DEV_STATE_DIR)
      : path.join(repoRoot, '.local', 'dev-stack'),
    devLogin: env.BYEBYEOCS_DEV_LOGIN === '1',
    dbMode: env.BYEBYEOCS_DEV_DB_MODE || 'auto',
    pgImage: env.BYEBYEOCS_DEV_PG_IMAGE || '',
    mainDsn: env.BYEBYEOCS_DEV_MAIN_DSN || '',
    tikuDsn: env.BYEBYEOCS_DEV_TIKU_DSN || '',
    clientMode: env.BYEBYEOCS_DEV_CLIENT_MODE || 'auto',
    flutterBin: env.FLUTTER || '',
    flutterDevice: env.BYEBYEOCS_DEV_FLUTTER_DEVICE || 'web-server',
    flutterReload: env.BYEBYEOCS_DEV_FLUTTER_RELOAD || 'hot-reload',
    logLevel: env.BYEBYEOCS_DEV_LOG_LEVEL || 'info',
    withPrototypes: false,
    keepDb: false,
    watch: true,
    autoInstall: true,
    reinitConfig: false,
    yes: false,
  }

  const nextValue = (flag, index) => {
    if (index + 1 >= args.length) {
      throw new Error(`参数 ${flag} 缺少取值`)
    }
    return args[index + 1]
  }

  for (let i = 0; i < args.length; i += 1) {
    const token = args[i]
    if (token === '-h' || token === '--help') {
      options.command = 'help'
      return options
    }
    const eqIdx = token.indexOf('=')
    const flag = eqIdx > 0 ? token.slice(0, eqIdx) : token
    const inlineValue = eqIdx > 0 ? token.slice(eqIdx + 1) : null
    const readVal = () => {
      if (inlineValue !== null) return inlineValue
      const val = nextValue(flag, i)
      i += 1
      return val
    }

    switch (flag) {
      case '--dev-login':
        options.devLogin = true
        break
      case '--with-prototypes':
        options.withPrototypes = true
        break
      case '--keep-db':
        options.keepDb = true
        break
      case '--no-watch':
        options.watch = false
        break
      case '--no-auto-install':
        options.autoInstall = false
        break
      case '--reinit-config':
        options.reinitConfig = true
        break
      case '--yes':
      case '-y':
        options.yes = true
        break
      case '--only':
        onlyList.push(...parseModuleList(flag, readVal()))
        break
      case '--skip':
        skipList.push(...parseModuleList(flag, readVal()))
        break
      case '--db':
        options.dbMode = readVal().trim().toLowerCase()
        break
      case '--pg-image':
        options.pgImage = readVal().trim()
        break
      case '--main-dsn':
        options.mainDsn = readVal().trim()
        break
      case '--tiku-dsn':
        options.tikuDsn = readVal().trim()
        break
      case '--client-mode':
        options.clientMode = readVal().trim().toLowerCase()
        break
      case '--flutter':
        options.flutterBin = readVal().trim()
        break
      case '--flutter-device':
        options.flutterDevice = readVal().trim()
        break
      case '--flutter-reload':
        options.flutterReload = readVal().trim().toLowerCase()
        break
      case '--log-level':
        options.logLevel = readVal().trim().toLowerCase()
        break
      case '--state-dir':
        options.stateDir = path.resolve(repoRoot, readVal().trim())
        break
      case '--port-offset':
        portOffset = parseIntArg(flag, readVal(), -2000, 30000)
        break
      case '--user-port':
        explicitPorts.user = parseIntArg(flag, readVal(), 1024, 65535)
        break
      case '--flutter-port':
        explicitPorts.flutter = parseIntArg(flag, readVal(), 1024, 65535)
        break
      case '--admin-port':
        explicitPorts.admin = parseIntArg(flag, readVal(), 1024, 65535)
        break
      case '--server-user-port':
        explicitPorts.serverUser = parseIntArg(flag, readVal(), 1024, 65535)
        break
      case '--server-admin-port':
        explicitPorts.serverAdmin = parseIntArg(flag, readVal(), 1024, 65535)
        break
      case '--tiku-port':
        explicitPorts.tiku = parseIntArg(flag, readVal(), 1024, 65535)
        break
      case '--tiku-proxy-port':
        explicitPorts.tikuProxy = parseIntArg(flag, readVal(), 1024, 65535)
        break
      case '--pg-port':
        explicitPorts.pg = parseIntArg(flag, readVal(), 1024, 65535)
        break
      case '--prototypes-port':
        explicitPorts.prototypes = parseIntArg(flag, readVal(), 1024, 65535)
        break
      default:
        throw new Error(`未知参数 "${token}"，请使用 --help 查看全部选项`)
    }
  }

  if (options.mainDsn || options.tikuDsn) {
    if (options.dbMode === 'auto') {
      options.dbMode = 'external'
    }
  }

  if (!VALID_DB_MODES.has(options.dbMode)) {
    throw new Error(`无效的 --db 模式 "${options.dbMode}"，可选：auto, docker, local-pg, external`)
  }
  if (!VALID_CLIENT_MODES.has(options.clientMode)) {
    throw new Error(`无效的 --client-mode "${options.clientMode}"，可选：auto, flutter, static, off`)
  }
  if (!VALID_RELOAD_MODES.has(options.flutterReload)) {
    throw new Error(`无效的 --flutter-reload "${options.flutterReload}"，可选：hot-reload, hot-restart`)
  }
  if (!VALID_LOG_LEVELS.has(options.logLevel)) {
    throw new Error(`无效的 --log-level "${options.logLevel}"，可选：debug, info, warn, error`)
  }

  // 计算最终启用模块集合
  const enabled = new Set(onlyList.length > 0 ? onlyList : DEFAULT_MODULES)
  if (options.withPrototypes) enabled.add('prototypes')
  for (const skipped of skipList) enabled.delete(skipped)
  if (options.clientMode === 'off') enabled.delete('client')

  options.modules = {
    db: enabled.has('db'),
    server: enabled.has('server'),
    tiku: enabled.has('tiku'),
    admin: enabled.has('admin'),
    client: enabled.has('client'),
    tokens: enabled.has('tokens'),
    prototypes: enabled.has('prototypes'),
  }

  // 计算端口并校验无冲突
  const basePort = (name, defaultVal) => {
    const val = explicitPorts[name] ?? defaultVal + portOffset
    if (val < 1024 || val > 65535) {
      throw new Error(`端口 ${name} 计算结果 ${val} 超出 1024–65535，请调整 --port-offset 或显式指定端口`)
    }
    return val
  }

  options.ports = {
    user: basePort('user', 5188),
    flutter: basePort('flutter', 5189),
    admin: basePort('admin', 5187),
    serverUser: basePort('serverUser', 8090),
    serverAdmin: basePort('serverAdmin', 8091),
    tiku: basePort('tiku', 8092),
    tikuProxy: basePort('tikuProxy', 18092),
    pg: basePort('pg', 25435),
    prototypes: basePort('prototypes', 5186),
  }

  // 仅校验当前会用到的端口是否互不重叠
  const activePortEntries = []
  if (options.modules.db && options.dbMode !== 'external') activePortEntries.push(['pg', options.ports.pg])
  if (options.modules.server) {
    activePortEntries.push(['serverUser', options.ports.serverUser])
    activePortEntries.push(['serverAdmin', options.ports.serverAdmin])
  }
  if (options.modules.tiku) {
    activePortEntries.push(['tiku', options.ports.tiku])
    if (options.watch) activePortEntries.push(['tikuProxy', options.ports.tikuProxy])
  }
  if (options.modules.admin) activePortEntries.push(['admin', options.ports.admin])
  if (options.modules.client) {
    activePortEntries.push(['user', options.ports.user])
    if (options.clientMode !== 'static') activePortEntries.push(['flutter', options.ports.flutter])
  }
  if (options.modules.prototypes) activePortEntries.push(['prototypes', options.ports.prototypes])

  const seenPorts = new Map()
  for (const [name, port] of activePortEntries) {
    if (seenPorts.has(port)) {
      throw new Error(`端口冲突：${seenPorts.get(port)} 与 ${name} 均使用了 ${port}`)
    }
    seenPorts.set(port, name)
  }

  return options
}
