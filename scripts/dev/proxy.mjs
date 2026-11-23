// 本地开发同源反向代理：
// 1. 用户端同源入口（默认 127.0.0.1:5188）：/api/* 透传主服务（SSE 无缓冲），其余 HTTP/WebSocket 透传 Flutter Dev 或静态构建产物。
// 2. 题库面板热读代理（默认 127.0.0.1:18092）：/panel/ 静态资源实时直读磁盘，/panel/api/* 透传题库 Go 服务。

import fs from 'node:fs'
import http from 'node:http'
import net from 'node:net'
import path from 'node:path'

import { delay } from './process.mjs'

const MIME_TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.ico': 'image/x-icon',
  '.woff2': 'font/woff2',
  '.ttf': 'font/ttf',
}

const PANEL_CSP = "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

function buildProxyHeaders(req, { preserveHost = true } = {}) {
  const headers = { ...req.headers }
  // 去掉 hop-by-hop 连接头，防止误切断 keep-alive 或 SSE 流。
  delete headers['proxy-connection']
  if (!preserveHost) {
    delete headers.host
  }
  const existingXff = headers['x-forwarded-for']
  const remote = req.socket?.remoteAddress?.replace(/^::ffff:/, '') || '127.0.0.1'
  headers['x-forwarded-for'] = existingXff ? `${existingXff}, ${remote}` : remote
  if (!headers['x-forwarded-proto']) {
    headers['x-forwarded-proto'] = 'http'
  }
  return headers
}

// 将单次 HTTP 请求透传至本地回环上游，支持 SSE 无缓冲与幂等 GET 重启窗口短重试。
async function forwardHttp(req, res, targetPort, {
  isStream = false,
  preserveHost = true,
  retryOnRefused = false,
  onUnavailable = null,
} = {}) {
  const canRetry = retryOnRefused && !isStream && (req.method === 'GET' || req.method === 'HEAD')
  const maxAttempts = canRetry ? 4 : 1

  for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
    const result = await attemptForward(req, res, targetPort, { isStream, preserveHost })
    if (result.done) return
    if (result.refused && attempt < maxAttempts && !res.headersSent && !req.destroyed) {
      await delay(180)
      continue
    }
    if (!res.headersSent) {
      if (onUnavailable) {
        onUnavailable(req, res, result.error)
      } else {
        const payload = JSON.stringify({
          error: {
            code: 'service_restarting',
            message: '上游开发服务正在启动或热重启，请稍后重试',
          },
        })
        res.writeHead(502, {
          'Content-Type': 'application/json; charset=utf-8',
          'Cache-Control': 'no-store',
        })
        res.end(payload)
      }
    }
    return
  }
}

function attemptForward(req, res, targetPort, { isStream, preserveHost }) {
  return new Promise((resolve) => {
    let settled = false
    const finish = (outcome) => {
      if (settled) return
      settled = true
      resolve(outcome)
    }

    const upstreamReq = http.request({
      hostname: '127.0.0.1',
      port: targetPort,
      method: req.method,
      path: req.url,
      headers: buildProxyHeaders(req, { preserveHost }),
    })

    if (isStream) {
      upstreamReq.setTimeout(0)
      res.setTimeout(0)
      req.socket?.setNoDelay(true)
      res.socket?.setNoDelay(true)
    } else {
      upstreamReq.setTimeout(120000, () => {
        upstreamReq.destroy(new Error('upstream timeout'))
      })
    }

    const abortUpstream = () => {
      if (!upstreamReq.destroyed) upstreamReq.destroy()
    }
    req.once('aborted', abortUpstream)
    res.once('close', () => {
      if (!res.writableEnded) abortUpstream()
    })

    upstreamReq.on('response', (upstreamRes) => {
      const contentType = String(upstreamRes.headers['content-type'] || '')
      const streamResponse = isStream || contentType.includes('text/event-stream')
      if (streamResponse) {
        res.setTimeout(0)
        res.socket?.setNoDelay(true)
      }
      res.writeHead(upstreamRes.statusCode || 502, upstreamRes.headers)
      if (streamResponse && typeof res.flushHeaders === 'function') {
        res.flushHeaders()
      }
      upstreamRes.pipe(res)
      upstreamRes.once('end', () => finish({ done: true }))
      upstreamRes.once('error', () => {
        if (!res.writableEnded) res.end()
        finish({ done: true })
      })
    })

    upstreamReq.on('error', (err) => {
      const refused = err?.code === 'ECONNREFUSED' || err?.code === 'ECONNRESET'
      finish({ done: false, refused, error: err })
    })

    if (req.method === 'GET' || req.method === 'HEAD') {
      upstreamReq.end()
    } else {
      req.pipe(upstreamReq)
    }
  })
}

// 透明转发 WebSocket upgrade（供 Flutter DWDS 热重载通道使用）。
function forwardUpgrade(req, clientSocket, head, targetPort, activeSockets = null) {
  const upstreamSocket = net.connect({ host: '127.0.0.1', port: targetPort })
  activeSockets?.add(clientSocket)
  activeSockets?.add(upstreamSocket)

  const cleanup = () => {
    activeSockets?.delete(clientSocket)
    activeSockets?.delete(upstreamSocket)
    if (!clientSocket.destroyed) clientSocket.destroy()
    if (!upstreamSocket.destroyed) upstreamSocket.destroy()
  }

  upstreamSocket.once('connect', () => {
    const lines = [`${req.method} ${req.url} HTTP/${req.httpVersion}`]
    for (let i = 0; i < req.rawHeaders.length; i += 2) {
      lines.push(`${req.rawHeaders[i]}: ${req.rawHeaders[i + 1]}`)
    }
    lines.push('', '')
    upstreamSocket.write(lines.join('\r\n'))
    if (head && head.length > 0) {
      upstreamSocket.write(head)
    }
    clientSocket.pipe(upstreamSocket)
    upstreamSocket.pipe(clientSocket)
  })

  upstreamSocket.on('error', cleanup)
  clientSocket.on('error', cleanup)
  upstreamSocket.on('close', cleanup)
  clientSocket.on('close', cleanup)
}

function renderHtmlPage({ title, heading, body, autoRefreshSec = 0 }) {
  const refresh = autoRefreshSec > 0 ? `<meta http-equiv="refresh" content="${autoRefreshSec}">` : ''
  return `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  ${refresh}
  <title>${title}</title>
  <style>
    body { font-family: system-ui, -apple-system, sans-serif; background: #f6f7f9; color: #181a1f; margin: 0; padding: 48px 20px; }
    main { max-width: 560px; margin: 0 auto; background: #fff; border: 1px solid #e2e5ea; border-radius: 12px; padding: 28px; }
    h1 { font-size: 18px; margin: 0 0 12px; }
    p { font-size: 14px; line-height: 1.65; color: #4b5563; margin: 0 0 10px; }
    code { background: #f1f3f5; padding: 2px 6px; border-radius: 4px; font-size: 13px; }
  </style>
</head>
<body>
  <main>
    <h1>${heading}</h1>
    ${body}
  </main>
</body>
</html>`
}

function listenLoopback(server, port) {
  return new Promise((resolve, reject) => {
    const onError = (err) => {
      server.removeListener('listening', onListen)
      reject(err)
    }
    const onListen = () => {
      server.removeListener('error', onError)
      resolve()
    }
    server.once('error', onError)
    server.once('listening', onListen)
    server.listen(port, '127.0.0.1')
  })
}

function closeServer(server, activeSockets = null) {
  return new Promise((resolve) => {
    if (!server) return resolve()
    if (activeSockets) {
      for (const sock of activeSockets) {
        if (!sock.destroyed) sock.destroy()
      }
      activeSockets.clear()
    }
    server.close(() => resolve())
    if (typeof server.closeAllConnections === 'function') {
      server.closeAllConnections()
    }
  })
}

export async function startUserGateway({
  listenPort,
  serverUserPort,
  flutterPort,
  getClientMode,
  isClientReady = () => true,
  isServerEnabled,
  logger,
}) {
  const upgradeSockets = new Set()
  const server = http.createServer((req, res) => {
    const urlPath = (req.url || '/').split('?')[0]
    const isApi = urlPath === '/api' || urlPath.startsWith('/api/') || urlPath.startsWith('/health/')
    const isSse = urlPath === '/api/v1/events'

    if (isApi) {
      if (!isServerEnabled()) {
        res.writeHead(503, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store' })
        res.end(JSON.stringify({ error: { code: 'service_disabled', message: '本次开发会话未启动主服务' } }))
        return
      }
      void forwardHttp(req, res, serverUserPort, {
        isStream: isSse,
        preserveHost: true,
        retryOnRefused: true,
      })
      return
    }

    const mode = getClientMode()
    if (mode === 'flutter') {
      const renderCompilingPage = (response) => {
        response.writeHead(503, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' })
        response.end(renderHtmlPage({
          title: '正在编译用户端 · ByebyeOCS',
          heading: 'Flutter 用户端正在编译启动…',
          body: '<p>首次启动编译通常需要数秒，页面将每 2 秒自动刷新并进入应用。</p><p>也可以在终端查看 <code>[client]</code> 实时构建日志。</p>',
          autoRefreshSec: 2,
        }))
      }
      if (!isClientReady()) {
        renderCompilingPage(res)
        return
      }
      void forwardHttp(req, res, flutterPort, {
        preserveHost: true,
        retryOnRefused: false,
        onUnavailable: (_req, response) => renderCompilingPage(response),
      })
      return
    }

    if (mode === 'static') {
      if (!isServerEnabled()) {
        res.writeHead(503, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' })
        res.end(renderHtmlPage({
          title: '主服务未启动 · ByebyeOCS',
          heading: '静态用户端需要主服务托管',
          body: '<p>当前会话跳过了 <code>server</code> 模块，因此无法提供静态用户端页面。</p>',
        }))
        return
      }
      void forwardHttp(req, res, serverUserPort, {
        preserveHost: true,
        retryOnRefused: true,
      })
      return
    }

    res.writeHead(503, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' })
    res.end(renderHtmlPage({
      title: '用户端未启用 · ByebyeOCS',
      heading: '当前未检测到 Flutter SDK 或已构建的用户端静态页面',
      body: '<p>接口代理 <code>/api/v1/*</code> 已就绪。如需本地实时热重载调试用户端，请安装 Flutter SDK（或设置 <code>FLUTTER</code> 环境变量）后重新运行开发脚本。</p>',
    }))
  })

  server.on('upgrade', (req, socket, head) => {
    if (getClientMode() !== 'flutter') {
      socket.destroy()
      return
    }
    forwardUpgrade(req, socket, head, flutterPort, upgradeSockets)
  })

  await listenLoopback(server, listenPort)
  logger.info('proxy', `用户端同源入口已监听：http://127.0.0.1:${listenPort}`)
  return {
    server,
    port: listenPort,
    close: () => closeServer(server, upgradeSockets),
  }
}

function serveLivePanelAsset(req, res, urlPath, assetsDir) {
  if (req.method !== 'GET' && req.method !== 'HEAD') {
    res.writeHead(405, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store' })
    res.end(JSON.stringify({ error: { code: 'method_not_allowed', message: '此页面不支持该操作' } }))
    return
  }

  let relative = urlPath.slice('/panel/'.length)
  if (!relative || relative.endsWith('/')) {
    relative += 'index.html'
  }
  try {
    relative = decodeURIComponent(relative)
  } catch {
    res.writeHead(400, { 'Content-Type': 'text/plain; charset=utf-8' })
    res.end('Bad Request')
    return
  }

  const root = path.resolve(assetsDir)
  const resolved = path.resolve(root, relative)
  const relCheck = path.relative(root, resolved)
  if (relCheck.startsWith('..') || path.isAbsolute(relCheck)) {
    res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' })
    res.end('Not Found')
    return
  }

  let stat
  try {
    stat = fs.statSync(resolved)
  } catch {
    res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' })
    res.end('Not Found')
    return
  }
  if (!stat.isFile()) {
    res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' })
    res.end('Not Found')
    return
  }

  const ext = path.extname(resolved).toLowerCase()
  const headers = {
    'Content-Type': MIME_TYPES[ext] || 'application/octet-stream',
    'Cache-Control': 'no-store',
    'X-Content-Type-Options': 'nosniff',
    'Referrer-Policy': 'no-referrer',
    'X-Frame-Options': 'DENY',
    'Content-Security-Policy': PANEL_CSP,
    'X-ByebyeOCS-Live-Asset': '1',
  }

  if (req.method === 'HEAD') {
    res.writeHead(200, headers)
    res.end()
    return
  }

  const data = fs.readFileSync(resolved)
  headers['Content-Length'] = String(data.length)
  res.writeHead(200, headers)
  res.end(data)
}

export async function startTikuPanelGateway({
  listenPort,
  tikuPort,
  assetsDir,
  liveAssets = true,
  logger,
}) {
  const server = http.createServer((req, res) => {
    const urlPath = (req.url || '/').split('?')[0]

    if (urlPath === '/panel') {
      res.writeHead(308, { Location: '/panel/' })
      res.end()
      return
    }

    if (liveAssets && urlPath.startsWith('/panel/') && !urlPath.startsWith('/panel/api/')) {
      serveLivePanelAsset(req, res, urlPath, assetsDir)
      return
    }

    void forwardHttp(req, res, tikuPort, {
      preserveHost: true,
      retryOnRefused: true,
    })
  })

  await listenLoopback(server, listenPort)
  logger.info('proxy', `题库面板热读代理已监听：127.0.0.1:${listenPort}（资源直读磁盘，/panel/api 转发至 ${tikuPort}）`)
  return {
    server,
    port: listenPort,
    close: () => closeServer(server),
  }
}
