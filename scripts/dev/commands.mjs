// 启动摘要面板与交互式终端快捷键控制。

import readline from 'node:readline'

function displayWidth(text) {
  let width = 0
  for (const ch of text) {
    width += ch.codePointAt(0) > 0xff ? 2 : 1
  }
  return width
}

function padDisplay(text, targetWidth) {
  const gap = Math.max(0, targetWidth - displayWidth(text))
  return text + ' '.repeat(gap)
}

export function printSummary({ options, dbState, clientRunner, logger }) {
  const { ports, modules } = options
  logger.divider('本地开发环境已就绪')

  const rows = []
  if (modules.client) {
    const modeNote =
      clientRunner?.resolvedMode === 'flutter'
        ? 'Flutter 热重载（保存 Dart 自动触发）'
        : clientRunner?.resolvedMode === 'static'
          ? '静态构建回落（apps/client_flutter/build/web）'
          : '仅 API 代理（未检测到 Flutter SDK 与静态构建）'
    rows.push(['用户端入口', `http://127.0.0.1:${ports.user}`, modeNote])
  }
  if (modules.admin) {
    rows.push(['管理员控制台', `http://127.0.0.1:${ports.admin}`, 'Vite HMR 实时热更新（已开启本地回环免登录）'])
  }
  if (modules.tiku) {
    const panelUrl = modules.admin
      ? `http://127.0.0.1:${ports.admin}/panel/`
      : `http://127.0.0.1:${ports.tiku}/panel/`
    const panelNote = options.watch && modules.admin
      ? '静态资源直读磁盘 0ms 热生效；改 Go 自动重编重启（已开启回环免登录）'
      : '改 Go 自动重编重启（已开启回环免登录）'
    rows.push(['题库维护面板', panelUrl, panelNote])
  }
  if (modules.server) {
    const loginNote = options.devLogin
      ? '已启用 devfixture 本地合成登录（仅本地联调，不连学习通）'
      : '默认关闭外部平台连接（可在 .local/dev-stack/server.yaml 自定义）'
    rows.push(['主服务 API', `User :${ports.serverUser} / Admin :${ports.serverAdmin}`, `改 Go/SQL 自动重编迁移重启 · ${loginNote}`])
  }
  if (modules.tiku) {
    rows.push(['题库服务 API', `http://127.0.0.1:${ports.tiku}`, '独立 PostgreSQL 与独立进程'])
  }
  if (modules.prototypes) {
    rows.push(['设计原型预览', `http://127.0.0.1:${ports.prototypes}/review`, 'Vite HMR 合成数据原型'])
  }
  if (dbState) {
    const dbDetail = dbState.backend === 'external'
      ? '外部 PostgreSQL'
      : `${dbState.backend === 'docker' ? 'Docker' : '本机实例'} 127.0.0.1:${dbState.port}`
    rows.push(['开发数据库', dbDetail, '主库 byebyeocs_dev / 题库 byebyeocs_tiku_dev'])
  }

  for (const [label, url, note] of rows) {
    process.stdout.write(`  ${padDisplay(label, 16)}  ${url}\n`)
    process.stdout.write(`${' '.repeat(20)}${note}\n`)
  }

  if (process.stdin.isTTY) {
    process.stdout.write('\n  快捷键：s 重启主服务 | t 重启题库 | r Flutter热重载 | R 热重启 | m 执行迁移 | d 同步设计变量 | p 地址 | h 帮助 | q 退出\n')
  }
  process.stdout.write('\n')
}

export function attachKeyboardShortcuts({
  onRestartServer,
  onRestartTiku,
  onFlutterHotReload,
  onFlutterHotRestart,
  onMigrate,
  onSyncTokens,
  onPrintSummary,
  onQuit,
  logger,
}) {
  if (!process.stdin.isTTY || typeof process.stdin.setRawMode !== 'function') {
    // 非交互模式（如管道或 CI）：仅响应显式控制命令行（q/quit），忽略 EOF 避免后台启动被误终止。
    let buf = ''
    const onData = (chunk) => {
      buf += chunk.toString('utf8')
      let idx = buf.indexOf('\n')
      while (idx !== -1) {
        const cmd = buf.slice(0, idx).trim().toLowerCase()
        buf = buf.slice(idx + 1)
        if (cmd === 'q' || cmd === 'quit') {
          onQuit('控制指令 quit')
          return
        }
        idx = buf.indexOf('\n')
      }
    }
    process.stdin.on('data', onData)
    process.stdin.resume()
    return () => {
      process.stdin.removeListener('data', onData)
      try {
        process.stdin.pause()
      } catch {
        // 忽略
      }
    }
  }

  readline.emitKeypressEvents(process.stdin)
  process.stdin.setRawMode(true)
  process.stdin.resume()

  const onKeypress = (_str, key) => {
    if (!key) return
    if (key.ctrl && key.name === 'c') {
      onQuit('Ctrl+C')
      return
    }
    switch (key.sequence) {
      case 'q':
      case 'Q':
        onQuit('快捷键 q')
        break
      case 's':
        onRestartServer()
        break
      case 't':
        onRestartTiku()
        break
      case 'r':
        onFlutterHotReload()
        break
      case 'R':
        onFlutterHotRestart()
        break
      case 'm':
        onMigrate()
        break
      case 'd':
        onSyncTokens()
        break
      case 'p':
        onPrintSummary()
        break
      case 'h':
      case '?':
        logger.info('dev', '快捷键：s 重启主服务 | t 重启题库 | r Flutter热重载 | R Flutter热重启 | m 迁移 | d 生成设计变量 | p 打印地址 | q/Ctrl+C 退出')
        break
      default:
        break
    }
  }

  process.stdin.on('keypress', onKeypress)

  return () => {
    process.stdin.removeListener('keypress', onKeypress)
    try {
      process.stdin.setRawMode(false)
      process.stdin.pause()
    } catch {
      // 忽略已关闭的标准输入
    }
  }
}
