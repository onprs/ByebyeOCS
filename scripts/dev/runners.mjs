// 各组件热生效运行器：Go 服务（主服务/题库）、管理端 Vite、用户端 Flutter、设计变量同步与设计原型。

import fs from 'node:fs'
import path from 'node:path'

import {
  IS_WINDOWS,
  attachLineReader,
  exeName,
  findExecutable,
  resolveNpmCommand,
  runCommand,
  spawnCrossPlatform,
  stopProcessTree,
  waitForHttpReady,
} from './process.mjs'
import { watchTree } from './watcher.mjs'

function formatMigrateSummary(line) {
  if (!line) return ''
  if (line.startsWith('{') && line.endsWith('}')) {
    try {
      const parsed = JSON.parse(line)
      if (typeof parsed.current === 'number' && typeof parsed.required === 'number') {
        return `版本 ${parsed.current}/${parsed.required}`
      }
    } catch {
      // 非结构化输出原样截断显示
    }
  }
  return line.length > 80 ? `${line.slice(0, 77)}…` : line
}

// GoServiceRunner 负责单个 Go 模块的增量构建、可选迁移、启动、健康探针及文件变更热重启。
export class GoServiceRunner {
  constructor({
    name,
    label,
    goBin,
    moduleDir,
    packagePath,
    binDir,
    buildTags = [],
    serveArgs,
    migrateArgs = null,
    readyUrl,
    envProvider = () => process.env,
    watchRoots = [],
    watchMatch = null,
    isMigrationFile = (file) => file.endsWith('.sql'),
    beforeFirstServe = null,
    afterFirstMigrate = null,
    logger,
  }) {
    this.name = name
    this.label = label
    this.goBin = goBin
    this.moduleDir = moduleDir
    this.packagePath = packagePath
    this.binDir = binDir
    this.buildTags = buildTags
    this.serveArgs = serveArgs
    this.migrateArgs = migrateArgs
    this.readyUrl = readyUrl
    this.envProvider = envProvider
    this.watchRoots = watchRoots
    this.watchMatch = watchMatch
    this.isMigrationFile = isMigrationFile
    this.beforeFirstServe = beforeFirstServe
    this.afterFirstMigrate = afterFirstMigrate
    this.logger = logger

    this.seq = 0
    this.currentBin = null
    this.child = null
    this.watcher = null
    this.stopping = false
    this.rebuilding = false
    this.pendingRebuild = false
    this.pendingMigrate = false
    this.pendingReason = ''
    this.firstServeDone = false
    this.status = 'stopped'
  }

  binaryPath(seq) {
    return path.join(this.binDir, exeName(`${this.name}-${seq}`))
  }

  cleanupOldBinaries(keepPath) {
    try {
      const prefix = `${this.name}-`
      for (const entry of fs.readdirSync(this.binDir)) {
        if (!entry.startsWith(prefix)) continue
        const full = path.join(this.binDir, entry)
        if (full === keepPath) continue
        try {
          fs.rmSync(full, { force: true })
        } catch {
          // Windows 上刚退出的文件可能短暂被释放，忽略即可，下次清理
        }
      }
    } catch {
      // 忽略目录未创建等情况
    }
  }

  async compile() {
    this.seq += 1
    const outPath = this.binaryPath(this.seq)
    const args = ['-C', this.moduleDir, 'build']
    if (this.buildTags.length > 0) {
      args.push('-tags', this.buildTags.join(','))
    }
    args.push('-o', outPath, this.packagePath)

    const start = Date.now()
    const res = await runCommand(this.goBin, args, {
      env: this.envProvider(),
      timeoutMs: 180000,
    })
    if (res.code !== 0) {
      try {
        fs.rmSync(outPath, { force: true })
      } catch {
        // 忽略
      }
      const detail = `${res.stderr || ''}\n${res.stdout || ''}`.trim()
      throw new Error(detail || `go build 退出码 ${res.code}`)
    }
    return { binPath: outPath, durationMs: Date.now() - start }
  }

  async runMigrate(binPath = this.currentBin) {
    if (!this.migrateArgs || !binPath) return
    this.logger.info(this.name, `执行 ${this.label} 数据库迁移…`)
    const res = await runCommand(binPath, this.migrateArgs, {
      env: this.envProvider(),
      timeoutMs: 120000,
    })
    if (res.code !== 0) {
      throw new Error(`${this.label} 迁移失败：${(res.stderr || res.stdout).trim()}`)
    }
    const rawSummary = res.stdout.trim().split(/\r?\n/).pop() || ''
    const summary = formatMigrateSummary(rawSummary)
    this.logger.success(this.name, `${this.label} 数据库迁移完成${summary ? `（${summary}）` : ''}`)
  }

  async startInitial({ watch = true } = {}) {
    this.status = 'building'
    this.logger.info(this.name, `正在编译 ${this.label}…`)
    const { binPath, durationMs } = await this.compile()
    this.currentBin = binPath
    this.logger.info(this.name, `${this.label} 编译完成（${durationMs}ms）`)

    if (this.beforeFirstServe) {
      await this.beforeFirstServe(binPath)
    }
    await this.runMigrate(binPath)
    if (this.afterFirstMigrate) {
      await this.afterFirstMigrate(binPath)
    }
    await this.spawnAndWait(binPath)
    this.firstServeDone = true
    this.cleanupOldBinaries(binPath)

    if (watch && this.watchRoots.length > 0) {
      this.startWatcher()
    }
  }

  async spawnAndWait(binPath) {
    const start = Date.now()
    const child = spawnCrossPlatform(binPath, this.serveArgs, {
      cwd: this.moduleDir,
      env: this.envProvider(),
      stdio: ['ignore', 'pipe', 'pipe'],
    })
    this.child = child
    this.status = 'starting'

    let exitedEarly = false
    let exitInfo = ''
    child.on('exit', (code, signal) => {
      if (this.child === child) {
        this.child = null
        if (!this.stopping && this.status === 'running') {
          this.status = 'crashed'
          this.logger.error(this.name, `${this.label} 意外退出（code=${code}, signal=${signal || 'none'}），修改代码保存后将自动重建拉起`)
        }
      }
      exitedEarly = true
      exitInfo = `code=${code}, signal=${signal || 'none'}`
    })

    attachLineReader(child.stdout, (line) => this.logger.childLine(this.name, line, false))
    attachLineReader(child.stderr, (line) => this.logger.childLine(this.name, line, true))

    const ready = await waitForHttpReady(this.readyUrl, {
      timeoutMs: 30000,
      intervalMs: 180,
      isAborted: () => this.stopping || exitedEarly,
    })

    if (this.stopping) return
    if (!ready) {
      await stopProcessTree(child)
      this.status = 'error'
      throw new Error(
        exitedEarly
          ? `${this.label} 启动后立即退出（${exitInfo}），请查看上方日志`
          : `${this.label} 在 30 秒内未通过就绪检查（${this.readyUrl}）`
      )
    }

    this.status = 'running'
    this.logger.success(this.name, `${this.label} 已就绪（启动 ${Date.now() - start}ms）`)
  }

  startWatcher() {
    this.watcher = watchTree({
      roots: this.watchRoots,
      matchFile: this.watchMatch || ((file) => /\.(go|sql|mod|sum|yaml|yml|json)$/i.test(file)),
      debounceMs: 280,
      onChange: (files) => {
        const hasSql = files.some((file) => this.isMigrationFile(file))
        const sample = files.slice(0, 2).map((f) => path.relative(this.moduleDir, f)).join(', ')
        const extra = files.length > 2 ? ` 等 ${files.length} 个文件` : ''
        this.requestRebuild(`${sample}${extra}`, { runMigrate: hasSql })
      },
      onError: (err) => this.logger.warn(this.name, err.message),
    })
  }

  requestRebuild(reason, { runMigrate = false } = {}) {
    if (this.stopping) return
    this.pendingRebuild = true
    this.pendingMigrate = this.pendingMigrate || runMigrate
    this.pendingReason = reason
    if (!this.rebuilding) {
      void this.drainRebuildQueue()
    }
  }

  async drainRebuildQueue() {
    if (this.rebuilding || this.stopping) return
    this.rebuilding = true
    try {
      while (this.pendingRebuild && !this.stopping) {
        const reason = this.pendingReason
        const needMigrate = this.pendingMigrate
        this.pendingRebuild = false
        this.pendingMigrate = false

        this.logger.info(this.name, `检测到变更（${reason}），重新编译 ${this.label}…`)
        let compiled
        try {
          compiled = await this.compile()
        } catch (err) {
          this.logger.error(this.name, `${this.label} 编译失败，保留当前运行状态并等待下次保存：\n${err.message}`)
          continue
        }

        if (this.stopping) break
        this.logger.info(this.name, `${this.label} 编译成功（${compiled.durationMs}ms），正在热重启…`)

        const oldChild = this.child
        this.child = null
        this.status = 'restarting'
        if (oldChild) {
          await stopProcessTree(oldChild, { timeoutMs: 6000 })
        }
        if (this.stopping) break

        try {
          if (needMigrate) {
            await this.runMigrate(compiled.binPath)
          }
          this.currentBin = compiled.binPath
          await this.spawnAndWait(compiled.binPath)
          this.cleanupOldBinaries(compiled.binPath)
        } catch (err) {
          this.logger.error(this.name, `${this.label} 热重启失败：${err.message}`)
        }
      }
    } finally {
      this.rebuilding = false
    }
  }

  async stop() {
    this.stopping = true
    this.watcher?.close()
    this.watcher = null
    if (this.child) {
      const target = this.child
      this.child = null
      await stopProcessTree(target, { timeoutMs: 6000 })
    }
    this.status = 'stopped'
  }
}

// ViteRunner 用于拉起 apps/admin 与可选 prototypes 的 Vite HMR 开发服务器。
export class ViteRunner {
  constructor({
    name,
    label,
    projectDir,
    port,
    extraArgs = [],
    extraEnv = {},
    autoInstall = true,
    logger,
  }) {
    this.name = name
    this.label = label
    this.projectDir = projectDir
    this.port = port
    this.extraArgs = extraArgs
    this.extraEnv = extraEnv
    this.autoInstall = autoInstall
    this.logger = logger
    this.child = null
    this.stopping = false
    this.status = 'stopped'
  }

  async ensureDependencies(npm) {
    const nodeModules = path.join(this.projectDir, 'node_modules')
    if (fs.existsSync(nodeModules)) return
    if (!this.autoInstall) {
      throw new Error(`${this.label} 缺少 node_modules，请先执行 npm --prefix ${this.projectDir} ci`)
    }
    this.logger.info(this.name, `检测到 ${this.label} 尚未安装依赖，正在执行 npm ci…`)
    const res = await runCommand(npm.command, [...npm.prefixArgs, 'ci'], {
      cwd: this.projectDir,
      timeoutMs: 300000,
    })
    if (res.code !== 0) {
      throw new Error(`${this.label} 安装依赖失败：${(res.stderr || res.stdout).trim()}`)
    }
    this.logger.success(this.name, `${this.label} 依赖安装完成`)
  }

  async start() {
    const npm = resolveNpmCommand()
    if (!npm) {
      throw new Error(`未找到 npm，无法启动 ${this.label}`)
    }
    await this.ensureDependencies(npm)

    const viteCli = path.join(this.projectDir, 'node_modules', 'vite', 'bin', 'vite.js')
    const useDirectVite = fs.existsSync(viteCli)
    const command = useDirectVite ? process.execPath : npm.command
    const args = useDirectVite
      ? [viteCli, '--host', '127.0.0.1', '--port', String(this.port), '--strictPort', ...this.extraArgs]
      : [...npm.prefixArgs, 'run', 'dev', '--', '--host', '127.0.0.1', '--port', String(this.port), '--strictPort', ...this.extraArgs]

    this.logger.info(this.name, `正在启动 ${this.label}（Vite HMR · http://127.0.0.1:${this.port}）…`)
    const child = spawnCrossPlatform(command, args, {
      cwd: this.projectDir,
      env: { ...process.env, ...this.extraEnv },
      stdio: ['ignore', 'pipe', 'pipe'],
    })
    this.child = child
    this.status = 'starting'

    let exitedEarly = false
    child.on('exit', (code, signal) => {
      if (this.child === child) {
        this.child = null
        if (!this.stopping) {
          this.status = 'crashed'
          this.logger.error(this.name, `${this.label} 已退出（code=${code}, signal=${signal || 'none'}）`)
        }
      }
      exitedEarly = true
    })

    attachLineReader(child.stdout, (line) => {
      if (!this.stopping) this.logger.childLine(this.name, line, false)
    })
    attachLineReader(child.stderr, (line) => {
      if (!this.stopping) this.logger.childLine(this.name, line, true)
    })

    const ready = await waitForHttpReady(`http://127.0.0.1:${this.port}/`, {
      timeoutMs: 30000,
      intervalMs: 250,
      isAborted: () => this.stopping || exitedEarly,
    })
    if (this.stopping) return
    if (!ready) {
      await stopProcessTree(child)
      this.status = 'error'
      throw new Error(`${this.label} 未能在 30 秒内就绪（http://127.0.0.1:${this.port}）`)
    }
    this.status = 'running'
    this.logger.success(this.name, `${this.label} 已就绪（Vite HMR 实时热生效）`)
  }

  async stop() {
    this.stopping = true
    if (this.child) {
      const target = this.child
      this.child = null
      await stopProcessTree(target, { timeoutMs: 5000 })
    }
    this.status = 'stopped'
  }
}

// FlutterClientRunner 负责用户端 Flutter 热重载（web-server 或指定原生设备）及无 SDK 时的静态降级。
export class FlutterClientRunner {
  constructor({
    repoRoot,
    flutterDir,
    staticWebDir,
    pidFile,
    clientMode,
    flutterBinHint,
    flutterDevice,
    flutterPort,
    serverUserPort,
    reloadMode,
    autoInstall,
    watch,
    logger,
  }) {
    this.repoRoot = repoRoot
    this.flutterDir = flutterDir
    this.staticWebDir = staticWebDir
    this.pidFile = pidFile
    this.clientMode = clientMode
    this.flutterBinHint = flutterBinHint
    this.flutterDevice = flutterDevice
    this.flutterPort = flutterPort
    this.serverUserPort = serverUserPort
    this.reloadMode = reloadMode
    this.autoInstall = autoInstall
    this.watch = watch
    this.logger = logger

    this.resolvedMode = 'off'
    this.flutterBin = null
    this.child = null
    this.watcher = null
    this.ready = false
    this.stopping = false
    this.pubGetting = false
    this.status = 'stopped'
  }

  detectFlutter() {
    if (this.flutterBinHint) {
      return findExecutable(this.flutterBinHint)
    }
    return findExecutable('flutter')
  }

  hasStaticWebBuild() {
    return fs.existsSync(path.join(this.staticWebDir, 'index.html'))
  }

  async start() {
    if (this.clientMode === 'off') {
      this.resolvedMode = 'off'
      this.status = 'disabled'
      return
    }

    if (this.clientMode === 'static') {
      if (this.hasStaticWebBuild()) {
        this.resolvedMode = 'static'
        this.status = 'static'
        this.logger.info('client', '用户端运行在静态构建模式（由主服务提供 apps/client_flutter/build/web）')
      } else {
        this.resolvedMode = 'off'
        this.status = 'unavailable'
        this.logger.warn('client', '未找到 apps/client_flutter/build/web/index.html，请先执行 make flutter-web 或切换到 Flutter 热重载模式')
      }
      return
    }

    const flutterBin = this.detectFlutter()
    if (!flutterBin) {
      if (this.clientMode === 'flutter') {
        throw new Error('已指定 --client-mode flutter，但未找到 Flutter SDK；请安装 Flutter 或设置 FLUTTER / --flutter 路径')
      }
      if (this.hasStaticWebBuild()) {
        this.resolvedMode = 'static'
        this.status = 'static'
        this.logger.warn('client', '未检测到 Flutter SDK，用户端自动回落为已有静态构建产物（apps/client_flutter/build/web）；配置 FLUTTER 环境变量即可开启热重载')
      } else {
        this.resolvedMode = 'off'
        this.status = 'unavailable'
        this.logger.warn('client', '未检测到 Flutter SDK 且无静态构建产物，已跳过用户端前端；接口代理 /api/v1/* 仍可用')
      }
      return
    }

    this.flutterBin = flutterBin
    this.resolvedMode = 'flutter'

    const packageConfig = path.join(this.flutterDir, '.dart_tool', 'package_config.json')
    if (!fs.existsSync(packageConfig)) {
      if (!this.autoInstall) {
        throw new Error('apps/client_flutter 尚未安装 Dart 依赖，请先执行 flutter pub get')
      }
      await this.runPubGet()
    }

    const isWebServer = this.flutterDevice === 'web-server'
    const args = ['run', '-d', this.flutterDevice, '--pid-file', this.pidFile]
    if (isWebServer) {
      args.push('--web-hostname', '127.0.0.1', '--web-port', String(this.flutterPort))
    } else {
      // 非 web-server（如 Android/桌面）自动注入本机主服务地址，调试构建允许回环 HTTP。
      args.push(`--dart-define=API_BASE_URL=http://127.0.0.1:${this.serverUserPort}`)
    }

    this.logger.info('client', `正在启动 Flutter 用户端（设备 ${this.flutterDevice}${isWebServer ? ` · 内部端口 ${this.flutterPort}` : ''}）…`)
    const child = spawnCrossPlatform(flutterBin, args, {
      cwd: this.flutterDir,
      stdio: ['pipe', 'pipe', 'pipe'],
    })
    this.child = child
    this.status = 'compiling'

    child.stdin?.on('error', () => {})

    const handleLine = (line, isStderr) => {
      this.logger.childLine('client', line, isStderr)
      if (!this.ready && /is being served at|Flutter run key commands|To hot reload|Syncing files to device/i.test(line)) {
        this.ready = true
        this.status = 'running'
        this.logger.success('client', 'Flutter 用户端已就绪，保存 Dart 文件将自动触发热重载')
      }
    }

    attachLineReader(child.stdout, (line) => handleLine(line, false))
    attachLineReader(child.stderr, (line) => handleLine(line, true))

    child.on('exit', (code, signal) => {
      if (this.child === child) {
        this.child = null
        this.ready = false
        if (!this.stopping) {
          this.status = 'crashed'
          this.logger.error('client', `Flutter 进程已退出（code=${code}, signal=${signal || 'none'}）`)
        }
      }
    })

    if (this.watch) {
      this.startWatcher()
    }
  }

  async runPubGet() {
    if (!this.flutterBin || this.pubGetting) return
    this.pubGetting = true
    try {
      this.logger.info('client', '正在执行 flutter pub get…')
      const res = await runCommand(this.flutterBin, ['pub', 'get'], {
        cwd: this.flutterDir,
        timeoutMs: 180000,
      })
      if (res.code !== 0) {
        throw new Error(`flutter pub get 失败：${(res.stderr || res.stdout).trim()}`)
      }
      this.logger.success('client', 'flutter pub get 完成')
    } finally {
      this.pubGetting = false
    }
  }

  startWatcher() {
    this.watcher = watchTree({
      roots: [
        path.join(this.flutterDir, 'lib'),
        path.join(this.flutterDir, 'assets'),
        path.join(this.flutterDir, 'web'),
        path.join(this.flutterDir, 'pubspec.yaml'),
      ],
      matchFile: (file) => /\.(dart|yaml|json|html|css|js|ttf|otf|png|svg)$/i.test(file),
      debounceMs: 320,
      onChange: async (files) => {
        if (this.stopping || !this.child) return
        const hasPubspec = files.some((file) => path.basename(file) === 'pubspec.yaml')
        if (hasPubspec) {
          try {
            await this.runPubGet()
            this.triggerReload('hot-restart', 'pubspec.yaml 更新')
          } catch (err) {
            this.logger.error('client', err.message)
          }
          return
        }
        const sample = path.relative(this.flutterDir, files[0])
        const hasWebShell = files.some((file) => file.includes(`${path.sep}web${path.sep}`))
        const action = hasWebShell ? 'hot-restart' : this.reloadMode
        this.triggerReload(action, files.length > 1 ? `${sample} 等 ${files.length} 个文件` : sample)
      },
      onError: (err) => this.logger.warn('client', err.message),
    })
  }

  triggerReload(kind = 'hot-reload', reason = '手动触发') {
    if (this.resolvedMode !== 'flutter' || !this.child) {
      this.logger.warn('client', '当前未运行 Flutter 热重载进程')
      return
    }
    if (!this.ready) {
      this.logger.info('client', 'Flutter 仍在首次编译，完成后生效')
      return
    }
    const key = kind === 'hot-restart' ? 'R' : 'r'
    const label = kind === 'hot-restart' ? '热重启 (R)' : '热重载 (r)'
    this.logger.info('client', `触发 Flutter ${label}（${reason}）…`)

    if (this.child.stdin && !this.child.stdin.destroyed && this.child.stdin.writable) {
      this.child.stdin.write(`${key}\n`)
      return
    }
    // POSIX 兜底：通过 --pid-file 向 Flutter 发送 SIGUSR1/SIGUSR2
    if (!IS_WINDOWS && fs.existsSync(this.pidFile)) {
      const pid = Number.parseInt(fs.readFileSync(this.pidFile, 'utf8').trim(), 10)
      if (Number.isInteger(pid) && pid > 0) {
        try {
          process.kill(pid, kind === 'hot-restart' ? 'SIGUSR2' : 'SIGUSR1')
        } catch {
          // 忽略进程已退出的情况
        }
      }
    }
  }

  async stop() {
    this.stopping = true
    this.watcher?.close()
    this.watcher = null
    if (this.child) {
      const target = this.child
      this.child = null
      try {
        if (target.stdin && !target.stdin.destroyed && target.stdin.writable) {
          target.stdin.write('q\n')
        }
      } catch {
        // 忽略
      }
      await stopProcessTree(target, { timeoutMs: 5000 })
    }
    try {
      fs.rmSync(this.pidFile, { force: true })
    } catch {
      // 忽略
    }
    this.status = 'stopped'
  }
}

// DesignTokensWatcher 监听 design/tokens.json 并在变更时自动重新生成三端设计变量。
export class DesignTokensWatcher {
  constructor({ repoRoot, logger }) {
    this.repoRoot = repoRoot
    this.logger = logger
    this.tokensFile = path.join(repoRoot, 'design', 'tokens.json')
    this.scriptFile = path.join(repoRoot, 'scripts', 'generate-design-tokens.py')
    this.pythonBin = null
    this.watcher = null
    this.running = false
  }

  start({ watch = true } = {}) {
    if (!fs.existsSync(this.tokensFile) || !fs.existsSync(this.scriptFile)) return
    this.pythonBin = findExecutable(IS_WINDOWS ? ['python', 'python3', 'py'] : ['python3', 'python'])
    if (!this.pythonBin) {
      this.logger.warn('tokens', '未检测到 Python 3，跳过 design/tokens.json 自动同步')
      return
    }
    if (!watch) return
    this.watcher = watchTree({
      roots: [this.tokensFile],
      matchFile: (file) => path.resolve(file) === path.resolve(this.tokensFile),
      debounceMs: 250,
      onChange: () => {
        void this.generate('design/tokens.json 已修改')
      },
      onError: (err) => this.logger.warn('tokens', err.message),
    })
    this.logger.info('tokens', '已监听 design/tokens.json（修改后自动同步三端 CSS 与 Dart 变量）')
  }

  async generate(reason = '手动触发') {
    if (!this.pythonBin) {
      this.logger.warn('tokens', '未检测到 Python 3，无法生成设计变量')
      return
    }
    if (this.running) return
    this.running = true
    try {
      this.logger.info('tokens', `重新生成设计变量（${reason}）…`)
      const args = path.basename(this.pythonBin).toLowerCase().startsWith('py') && IS_WINDOWS
        ? ['-3', this.scriptFile]
        : [this.scriptFile]
      const res = await runCommand(this.pythonBin, args, { cwd: this.repoRoot, timeoutMs: 30000 })
      if (res.code !== 0) {
        this.logger.error('tokens', `设计变量生成失败：\n${(res.stderr || res.stdout).trim()}`)
      } else {
        this.logger.success('tokens', '设计变量已同步至管理端、题库面板与 Flutter 客户端')
      }
    } finally {
      this.running = false
    }
  }

  stop() {
    this.watcher?.close()
    this.watcher = null
  }
}
