#!/usr/bin/env node
// ByebyeOCS 跨平台（Windows / Ubuntu）全栈一键拉起与热重载开发调试入口。
// 仅依赖 Node.js 22+ 标准库，不依赖维护者本机私有配置或额外第三方进程管理工具。

import fs from 'node:fs'
import path from 'node:path'
import readline from 'node:readline/promises'
import { fileURLToPath } from 'node:url'

import { HELP_TEXT, parseCliArgs } from './dev/cli.mjs'
import { attachKeyboardShortcuts, printSummary } from './dev/commands.mjs'
import {
  detectDockerDaemon,
  detectLocalPostgresTools,
  ensureDatabase,
  resetWorkspaceDatabase,
  stopWorkspaceDatabase,
} from './dev/database.mjs'
import { DevLogger } from './dev/logger.mjs'
import {
  IS_WINDOWS,
  findExecutable,
  isPortInUse,
  resolveNpmCommand,
  runCommand,
} from './dev/process.mjs'
import { startTikuPanelGateway, startUserGateway } from './dev/proxy.mjs'
import {
  DesignTokensWatcher,
  FlutterClientRunner,
  GoServiceRunner,
  ViteRunner,
} from './dev/runners.mjs'
import { watchTree } from './dev/watcher.mjs'
import {
  buildServerRuntimeEnv,
  ensureDevAdminAccount,
  ensureTikuPanelHash,
  prepareWorkspaceConfigs,
} from './dev/workspace.mjs'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const REPO_ROOT = path.resolve(__dirname, '..')

function checkNodeVersion() {
  const [major, minor] = process.versions.node.split('.').map((n) => Number.parseInt(n, 10))
  if (major < 22 || (major === 22 && minor < 12)) {
    throw new Error(`需要 Node.js 22.12 或以上，当前版本为 ${process.versions.node}`)
  }
}

async function runDoctor(options, logger) {
  logger.divider('开发环境自检')

  const report = (ok, label, detail) => {
    const icon = ok ? '✓' : '✗'
    if (ok) {
      logger.success('dev', `${icon} ${label}：${detail}`)
    } else {
      logger.warn('dev', `${icon} ${label}：${detail}`)
    }
  }

  report(true, 'Node.js', `${process.versions.node}（${process.platform}/${process.arch}）`)

  const goBin = findExecutable('go')
  if (goBin) {
    const ver = await runCommand(goBin, ['version'], { timeoutMs: 5000 }).catch(() => ({ stdout: '' }))
    report(true, 'Go 工具链', `${(ver.stdout || '').trim() || goBin}`)
  } else {
    report(false, 'Go 工具链', '未安装，无法启动主服务与题库服务')
  }

  const npm = resolveNpmCommand()
  report(Boolean(npm), 'npm', npm ? npm.display : '未找到 npm，无法启动管理端与设计原型')

  const pyBin = findExecutable(IS_WINDOWS ? ['python', 'python3', 'py'] : ['python3', 'python'])
  report(Boolean(pyBin), 'Python 3', pyBin || '未安装（不影响核心服务，仅跳过 design/tokens.json 自动同步）')

  const flutterBin = options.flutterBin ? findExecutable(options.flutterBin) : findExecutable('flutter')
  const staticWebReady = fs.existsSync(path.join(REPO_ROOT, 'apps', 'client_flutter', 'build', 'web', 'index.html'))
  if (flutterBin) {
    report(true, 'Flutter SDK', flutterBin)
  } else if (staticWebReady) {
    report(false, 'Flutter SDK', '未检测到（用户端可自动回落至 apps/client_flutter/build/web 静态产物；设置 FLUTTER 可开启热重载）')
  } else {
    report(false, 'Flutter SDK', '未检测到，且无静态构建产物（不影响后端与管理端调试）')
  }

  const tikuMain = path.join(REPO_ROOT, 'services', 'tiku', 'cmd', 'ocs-tiku-fork', 'main.go')
  report(
    fs.existsSync(tikuMain),
    '题库子模块',
    fs.existsSync(tikuMain) ? 'services/tiku 已就绪' : '未初始化，请执行 git submodule update --init --recursive'
  )

  const docker = await detectDockerDaemon()
  report(docker.available, 'Docker', docker.available ? `守护进程可用（${docker.version}）` : docker.reason)

  const localPg = detectLocalPostgresTools()
  report(localPg.available, '本机 PostgreSQL', localPg.available ? `${localPg.pgCtl}` : '未检测到完整的 initdb / pg_ctl / psql')

  // 端口预检
  const checks = [
    ['主服务 User API', options.ports.serverUser],
    ['主服务 Admin API', options.ports.serverAdmin],
    ['题库服务 API', options.ports.tiku],
    ['题库面板热读代理', options.ports.tikuProxy],
    ['管理员控制台 Vite', options.ports.admin],
    ['用户端同源入口', options.ports.user],
    ['Flutter 内部端口', options.ports.flutter],
    ['开发数据库端口', options.ports.pg],
  ]
  if (options.modules.prototypes) {
    checks.push(['设计原型 Vite', options.ports.prototypes])
  }

  logger.divider('端口占用状态（127.0.0.1）')
  for (const [label, port] of checks) {
    const busy = await isPortInUse('127.0.0.1', port)
    if (busy) {
      logger.warn('dev', `端口 ${port}（${label}）当前已被占用；可使用 --port-offset 避让`)
    } else {
      logger.info('dev', `端口 ${port}（${label}）空闲`)
    }
  }
}

async function runReset(options, logger) {
  if (!options.yes) {
    if (!process.stdin.isTTY) {
      throw new Error('非交互终端执行 reset 时必须显式传入 --yes')
    }
    const rl = readline.createInterface({ input: process.stdin, output: process.stdout })
    try {
      const answer = await rl.question(`确认删除本工作区的开发数据库与配置目录（${options.stateDir}）？输入 yes 继续：`)
      if (answer.trim().toLowerCase() !== 'yes') {
        logger.info('dev', '已取消重置')
        return
      }
    } finally {
      rl.close()
    }
  }

  await resetWorkspaceDatabase(options, logger)
  if (fs.existsSync(options.stateDir)) {
    fs.rmSync(options.stateDir, { recursive: true, force: true })
    logger.success('dev', `已清空开发态目录：${options.stateDir}`)
  }
  logger.success('dev', '本地开发环境已彻底重置，下次运行 up 将重新初始化')
}

async function preflightPorts(options) {
  const toCheck = []
  if (options.modules.server) {
    toCheck.push(['主服务 User API', options.ports.serverUser])
    toCheck.push(['主服务 Admin API', options.ports.serverAdmin])
  }
  if (options.modules.tiku) {
    toCheck.push(['题库服务 API', options.ports.tiku])
    if (options.watch && options.modules.admin) {
      toCheck.push(['题库面板热读代理', options.ports.tikuProxy])
    }
  }
  if (options.modules.admin) {
    toCheck.push(['管理员控制台', options.ports.admin])
  }
  if (options.modules.client) {
    toCheck.push(['用户端同源入口', options.ports.user])
  }
  if (options.modules.prototypes) {
    toCheck.push(['设计原型', options.ports.prototypes])
  }

  const occupied = []
  for (const [label, port] of toCheck) {
    if (await isPortInUse('127.0.0.1', port)) {
      occupied.push(`${label}（127.0.0.1:${port}）`)
    }
  }
  if (occupied.length > 0) {
    throw new Error(
      `以下开发端口已被占用：\n  - ${occupied.join('\n  - ')}\n` +
      `请先释放占用端口，或使用 --port-offset 100（及各端口覆盖参数）避让，不自动停止其他项目进程。`
    )
  }
}

async function runUp(options, logger) {
  const goBin = (options.modules.server || options.modules.tiku) ? findExecutable('go') : null
  if ((options.modules.server || options.modules.tiku) && !goBin) {
    throw new Error('未找到 go 命令，请安装 Go 1.26+ 并加入 PATH')
  }

  if (options.modules.tiku) {
    const tikuMain = path.join(REPO_ROOT, 'services', 'tiku', 'cmd', 'ocs-tiku-fork', 'main.go')
    if (!fs.existsSync(tikuMain)) {
      throw new Error('题库子模块尚未初始化，请先执行：git submodule update --init --recursive（或加 --skip tiku 跳过题库）')
    }
  }

  await preflightPorts(options)

  // 运行时资源句柄，用于异常回滚和优雅关闭
  let dbState = null
  let tikuRunner = null
  let tikuPanelGateway = null
  let tikuAssetsWatcher = null
  let serverRunner = null
  let userGateway = null
  let clientRunner = null
  let adminRunner = null
  let prototypesRunner = null
  let tokensWatcher = null
  let detachKeyboard = () => {}

  let shuttingDown = false
  let forceExitRequested = false

  const shutdown = async (reason, exitCode = 0) => {
    if (shuttingDown) {
      if (!forceExitRequested) {
        forceExitRequested = true
        logger.warn('dev', '再次收到退出信号，立即强制终止…')
        process.exit(130)
      }
      return
    }
    shuttingDown = true
    detachKeyboard()
    logger.info('dev', `正在停止本地开发环境（${reason}）…`)

    tokensWatcher?.stop()
    tikuAssetsWatcher?.close()

    await Promise.allSettled([
      prototypesRunner?.stop(),
      clientRunner?.stop(),
      adminRunner?.stop(),
      userGateway?.close(),
    ])
    await Promise.allSettled([
      serverRunner?.stop(),
      tikuPanelGateway?.close(),
    ])
    await Promise.allSettled([
      tikuRunner?.stop(),
    ])

    if (dbState?.managed && !options.keepDb) {
      try {
        await dbState.stop()
      } catch (err) {
        logger.warn('db', `停止开发数据库时出错：${err.message}`)
      }
    } else if (dbState?.managed && options.keepDb) {
      logger.info('db', '已按 --keep-db 保留开发数据库继续运行（需要停止时可执行 node scripts/dev.mjs down）')
    }

    logger.success('dev', '全部本地开发服务已退出')
    process.exit(exitCode)
  }

  const onSigInt = () => void shutdown('SIGINT', 0)
  const onSigTerm = () => void shutdown('SIGTERM', 0)
  process.on('SIGINT', onSigInt)
  process.on('SIGTERM', onSigTerm)

  try {
    const paths = prepareWorkspaceConfigs(options, logger)

    if (options.modules.db) {
      dbState = await ensureDatabase(options, logger)
    }

    // 1. 独立题库服务 + 题库面板 0ms 磁盘热读网关
    const useLivePanelGateway = Boolean(options.modules.tiku && options.watch && options.modules.admin)
    if (options.modules.tiku) {
      const tikuDir = path.join(REPO_ROOT, 'services', 'tiku')
      const assetsDirResolved = path.resolve(paths.tikuPanelAssetsDir)

      tikuRunner = new GoServiceRunner({
        name: 'tiku',
        label: '题库服务',
        goBin,
        moduleDir: tikuDir,
        packagePath: './cmd/ocs-tiku-fork',
        binDir: paths.binDir,
        serveArgs: ['serve', '-config', paths.tikuConfig],
        migrateArgs: ['migrate', '-config', paths.tikuConfig],
        readyUrl: `http://127.0.0.1:${options.ports.tiku}/readyz`,
        watchRoots: [tikuDir],
        watchMatch: (file) => {
          const resolved = path.resolve(file)
          // 开启热读网关时，面板前端资源无需重编 Go；仅后端 Go/SQL/mod 变更触发重启。
          if (useLivePanelGateway && resolved.startsWith(assetsDirResolved + path.sep)) {
            return false
          }
          if (resolved.includes(`${path.sep}frontend${path.sep}`)) {
            return false
          }
          return /\.(go|sql|mod|sum|html|css|js)$/i.test(resolved)
        },
        beforeFirstServe: (binPath) => ensureTikuPanelHash(binPath, paths, logger),
        logger,
      })

      await tikuRunner.startInitial({ watch: options.watch })

      if (useLivePanelGateway) {
        tikuPanelGateway = await startTikuPanelGateway({
          listenPort: options.ports.tikuProxy,
          tikuPort: options.ports.tiku,
          assetsDir: paths.tikuPanelAssetsDir,
          liveAssets: true,
          logger,
        })
        tikuAssetsWatcher = watchTree({
          roots: [paths.tikuPanelAssetsDir],
          matchFile: (file) => /\.(html|css|js|svg|json)$/i.test(file),
          debounceMs: 180,
          onChange: (files) => {
            const names = files.map((f) => path.basename(f)).join(', ')
            logger.info('tiku', `题库面板资源已更新（${names}），刷新浏览器即刻生效`)
          },
          onError: (err) => logger.warn('tiku', err.message),
        })
      }
    }

    // 2. 主服务（含自动迁移、本地开发管理员创建、可选 devfixture 本地合成登录）
    if (options.modules.server) {
      const serverDir = path.join(REPO_ROOT, 'server')
      const serverEnv = () => buildServerRuntimeEnv(options)
      const buildTags = options.devLogin ? ['devfixture'] : []

      serverRunner = new GoServiceRunner({
        name: 'server',
        label: options.devLogin ? '主服务（devfixture）' : '主服务',
        goBin,
        moduleDir: serverDir,
        packagePath: './cmd/byebyeocs',
        binDir: paths.binDir,
        buildTags,
        serveArgs: ['serve', '--config', paths.serverConfig],
        migrateArgs: ['migrate', 'up', '--config', paths.serverConfig],
        readyUrl: `http://127.0.0.1:${options.ports.serverAdmin}/health/ready`,
        envProvider: serverEnv,
        watchRoots: [
          path.join(serverDir, 'cmd'),
          path.join(serverDir, 'internal'),
          path.join(serverDir, 'go.mod'),
          path.join(serverDir, 'go.sum'),
        ],
        watchMatch: (file) => {
          if (file.endsWith('_test.go')) return false
          return /\.(go|sql|mod|sum|json|yaml|yml)$/i.test(file)
        },
        afterFirstMigrate: (binPath) => ensureDevAdminAccount(binPath, paths, serverEnv(), logger),
        logger,
      })

      await serverRunner.startInitial({ watch: options.watch })
    }

    // 3. 管理员控制台 Vite HMR（同域代理 /admin/api、/health 与 /panel/）
    if (options.modules.admin) {
      const tikuProxyTarget = useLivePanelGateway
        ? `http://127.0.0.1:${options.ports.tikuProxy}`
        : `http://127.0.0.1:${options.ports.tiku}`

      adminRunner = new ViteRunner({
        name: 'admin',
        label: '管理员控制台',
        projectDir: path.join(REPO_ROOT, 'apps', 'admin'),
        port: options.ports.admin,
        autoInstall: options.autoInstall,
        extraEnv: {
          BYEBYEOCS_DEV_ADMIN_PORT: String(options.ports.admin),
          BYEBYEOCS_DEV_ADMIN_API_TARGET: `http://127.0.0.1:${options.ports.serverAdmin}`,
          BYEBYEOCS_DEV_TIKU_TARGET: tikuProxyTarget,
        },
        logger,
      })
      await adminRunner.start()
    }

    // 4. 用户端 Flutter 热重载 + 同源 /api 网关
    if (options.modules.client) {
      clientRunner = new FlutterClientRunner({
        repoRoot: REPO_ROOT,
        flutterDir: path.join(REPO_ROOT, 'apps', 'client_flutter'),
        staticWebDir: paths.clientAssetsDir,
        pidFile: paths.flutterPidFile,
        clientMode: options.clientMode,
        flutterBinHint: options.flutterBin,
        flutterDevice: options.flutterDevice,
        flutterPort: options.ports.flutter,
        serverUserPort: options.ports.serverUser,
        reloadMode: options.flutterReload,
        autoInstall: options.autoInstall,
        watch: options.watch,
        logger,
      })

      // 仅在真正启用 flutter web-server 时才额外预检 flutter 内部端口
      if (clientRunner.detectFlutter() && options.clientMode !== 'static' && options.flutterDevice === 'web-server') {
        if (await isPortInUse('127.0.0.1', options.ports.flutter)) {
          throw new Error(`Flutter 内部端口 127.0.0.1:${options.ports.flutter} 已被占用，请通过 --flutter-port 或 --port-offset 避让`)
        }
      }

      await clientRunner.start()

      userGateway = await startUserGateway({
        listenPort: options.ports.user,
        serverUserPort: options.ports.serverUser,
        flutterPort: options.ports.flutter,
        getClientMode: () => clientRunner.resolvedMode,
        isClientReady: () => clientRunner.ready,
        isServerEnabled: () => options.modules.server,
        logger,
      })
    }

    // 5. 设计变量热联动（design/tokens.json -> 三端 CSS/Dart）
    if (options.modules.tokens) {
      tokensWatcher = new DesignTokensWatcher({ repoRoot: REPO_ROOT, logger })
      tokensWatcher.start({ watch: options.watch })
    }

    // 6. 可选设计原型（--with-prototypes）
    if (options.modules.prototypes) {
      prototypesRunner = new ViteRunner({
        name: 'proto',
        label: '设计原型',
        projectDir: path.join(REPO_ROOT, 'prototypes'),
        port: options.ports.prototypes,
        autoInstall: options.autoInstall,
        logger,
      })
      await prototypesRunner.start()
    }

    printSummary({ options, dbState, clientRunner, logger })

    detachKeyboard = attachKeyboardShortcuts({
      onRestartServer: () => {
        if (serverRunner) serverRunner.requestRebuild('手动触发', { runMigrate: false })
        else logger.warn('dev', '当前会话未启用主服务')
      },
      onRestartTiku: () => {
        if (tikuRunner) tikuRunner.requestRebuild('手动触发', { runMigrate: false })
        else logger.warn('dev', '当前会话未启用题库服务')
      },
      onFlutterHotReload: () => {
        clientRunner?.triggerReload('hot-reload', '快捷键 r')
      },
      onFlutterHotRestart: () => {
        clientRunner?.triggerReload('hot-restart', '快捷键 R')
      },
      onMigrate: async () => {
        try {
          if (tikuRunner) await tikuRunner.runMigrate()
          if (serverRunner) await serverRunner.runMigrate()
        } catch (err) {
          logger.error('dev', err.message)
        }
      },
      onSyncTokens: () => {
        void tokensWatcher?.generate('快捷键 d')
      },
      onPrintSummary: () => printSummary({ options, dbState, clientRunner, logger }),
      onQuit: (why) => void shutdown(why, 0),
      logger,
    })

    // 保持主事件循环存活，直到收到退出信号或快捷键 q。
    await new Promise(() => {})
  } catch (err) {
    logger.error('dev', err?.message || String(err))
    process.removeListener('SIGINT', onSigInt)
    process.removeListener('SIGTERM', onSigTerm)
    await shutdown('启动失败回滚', 1)
  }
}

async function main() {
  const logger = new DevLogger()
  try {
    checkNodeVersion()
    const options = parseCliArgs(process.argv.slice(2), { repoRoot: REPO_ROOT })
    switch (options.command) {
      case 'help':
        process.stdout.write(HELP_TEXT)
        return
      case 'doctor':
        await runDoctor(options, logger)
        return
      case 'down':
        await stopWorkspaceDatabase(options, logger)
        return
      case 'reset':
        await runReset(options, logger)
        return
      case 'up':
        await runUp(options, logger)
        return
    }
  } catch (err) {
    logger.error('dev', err?.message || String(err))
    process.exit(1)
  }
}

void main()
