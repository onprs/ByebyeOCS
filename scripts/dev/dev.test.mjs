// 全栈热重载开发脚本的核心单元与契约测试（仅使用 Node.js 内置测试运行器）。

import assert from 'node:assert/strict'
import crypto from 'node:crypto'
import fs from 'node:fs'
import http from 'node:http'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import test from 'node:test'

import { parseCliArgs } from './cli.mjs'
import { DevLogger } from './logger.mjs'
import { IS_WINDOWS, delay } from './process.mjs'
import { startTikuPanelGateway, startUserGateway } from './proxy.mjs'
import { watchTree } from './watcher.mjs'
import { buildServerRuntimeEnv, prepareWorkspaceConfigs } from './workspace.mjs'

function makeTempDir(prefix) {
  return fs.mkdtempSync(path.join(os.tmpdir(), prefix))
}

function getFreePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer()
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address()
      srv.close(() => resolve(addr.port))
    })
    srv.on('error', reject)
  })
}

function httpRequest(port, reqPath, { method = 'GET', headers = {}, body = null } = {}) {
  return new Promise((resolve, reject) => {
    const req = http.request({ hostname: '127.0.0.1', port, path: reqPath, method, headers }, (res) => {
      const chunks = []
      res.on('data', (c) => chunks.push(c))
      res.on('end', () => {
        resolve({
          status: res.statusCode,
          headers: res.headers,
          body: Buffer.concat(chunks).toString('utf8'),
        })
      })
    })
    req.on('error', reject)
    if (body) req.write(body)
    req.end()
  })
}

test('CLI 参数解析：默认端口、偏移、模块筛选与冲突校验', () => {
  const repoRoot = '/tmp/fake-repo'
  const def = parseCliArgs([], { repoRoot, env: {} })
  assert.equal(def.command, 'up')
  assert.equal(def.ports.user, 5188)
  assert.equal(def.ports.admin, 5187)
  assert.equal(def.ports.serverUser, 8090)
  assert.equal(def.ports.serverAdmin, 8091)
  assert.equal(def.ports.tiku, 8092)
  assert.equal(def.ports.pg, 25435)
  assert.equal(def.modules.prototypes, false)
  assert.equal(def.devLogin, false)

  const shifted = parseCliArgs(['up', '--port-offset', '100', '--dev-login', '--with-prototypes', '--skip', 'client'], { repoRoot, env: {} })
  assert.equal(shifted.ports.admin, 5287)
  assert.equal(shifted.ports.serverUser, 8190)
  assert.equal(shifted.ports.tiku, 8192)
  assert.equal(shifted.devLogin, true)
  assert.equal(shifted.modules.client, false)
  assert.equal(shifted.modules.prototypes, true)

  const external = parseCliArgs(['--main-dsn', 'postgres://a:b@127.0.0.1:5432/db?sslmode=disable'], { repoRoot, env: {} })
  assert.equal(external.dbMode, 'external')

  assert.throws(
    () => parseCliArgs(['--user-port', '5187', '--admin-port', '5187'], { repoRoot, env: {} }),
    /端口冲突/
  )
  assert.throws(
    () => parseCliArgs(['unknown-cmd'], { repoRoot, env: {} }),
    /未知子命令/
  )
})

test('工作区配置初始化：生成私密密钥与配置，二次启动保留用户自定义模型与设置', () => {
  const tmpRepo = makeTempDir('byebyeocs-dev-ws-')
  try {
    const logger = new DevLogger({ useColor: false })
    const options = parseCliArgs(['--port-offset', '200'], { repoRoot: tmpRepo, env: {} })
    const paths = prepareWorkspaceConfigs(options, logger)

    assert.equal(fs.existsSync(paths.serverConfig), true)
    assert.equal(fs.existsSync(paths.tikuConfig), true)
    assert.equal(Buffer.from(fs.readFileSync(paths.appKeyFile, 'utf8').trim(), 'base64').length, 32)
    assert.equal(Buffer.from(fs.readFileSync(paths.identityKeyFile, 'utf8').trim(), 'base64').length, 32)
    assert.ok(fs.readFileSync(paths.tikuTokenFile, 'utf8').trim().length >= 32)

    if (!IS_WINDOWS) {
      assert.equal(fs.statSync(paths.appKeyFile).mode & 0o077, 0)
      assert.equal(fs.statSync(paths.secretsDir).mode & 0o077, 0)
    }

    // 模拟开发者在题库面板中添加了自定义模型，并在主服务配置中写入注释
    const tikuCfg = JSON.parse(fs.readFileSync(paths.tikuConfig, 'utf8'))
    tikuCfg.models = [{ id: 'custom-model', display_name: '测试模型', model: 'gpt-test' }]
    fs.writeFileSync(paths.tikuConfig, JSON.stringify(tikuCfg, null, 2))
    fs.appendFileSync(paths.serverConfig, '\n# custom-marker\n')

    // 换一组端口二次启动：server.yaml 保留原样，ocs-tiku-fork.json 保留 models 同时更新端口和 origin
    const options2 = parseCliArgs(['--port-offset', '300'], { repoRoot: tmpRepo, env: {} })
    prepareWorkspaceConfigs(options2, logger)

    assert.ok(fs.readFileSync(paths.serverConfig, 'utf8').includes('# custom-marker'))
    const tikuCfg2 = JSON.parse(fs.readFileSync(paths.tikuConfig, 'utf8'))
    assert.equal(tikuCfg2.models.length, 1)
    assert.equal(tikuCfg2.models[0].id, 'custom-model')
    assert.equal(tikuCfg2.listen, `127.0.0.1:${options2.ports.tiku}`)
    assert.equal(tikuCfg2.panel.origin, `http://127.0.0.1:${options2.ports.admin}`)

    const runtimeEnv = buildServerRuntimeEnv(options2)
    assert.equal(runtimeEnv.BYEBYEOCS_USER_ORIGIN, `http://127.0.0.1:${options2.ports.user}`)
    assert.equal(runtimeEnv.BYEBYEOCS_ADMIN_ORIGIN, `http://127.0.0.1:${options2.ports.admin}`)
    assert.equal(runtimeEnv.BYEBYEOCS_DEV_LOGIN, undefined)
  } finally {
    fs.rmSync(tmpRepo, { recursive: true, force: true })
  }
})

test('递归文件监听器：防抖合并、忽略构建目录并捕获新增子目录', async () => {
  const root = makeTempDir('byebyeocs-dev-watch-')
  const sub = path.join(root, 'pkg')
  const ignored = path.join(root, 'node_modules')
  fs.mkdirSync(sub, { recursive: true })
  fs.mkdirSync(ignored, { recursive: true })

  const batches = []
  const watcher = watchTree({
    roots: [root],
    matchFile: (file) => file.endsWith('.go'),
    debounceMs: 120,
    onChange: (files) => batches.push(files),
  })

  try {
    fs.writeFileSync(path.join(ignored, 'skip.go'), 'package skip\n')
    fs.writeFileSync(path.join(sub, 'a.go'), 'package pkg\n')
    fs.writeFileSync(path.join(sub, 'b.go'), 'package pkg\n')
    await delay(320)

    assert.equal(batches.length, 1)
    assert.equal(batches[0].some((f) => f.endsWith('skip.go')), false)
    assert.equal(batches[0].some((f) => f.endsWith('a.go')), true)
    assert.equal(batches[0].some((f) => f.endsWith('b.go')), true)

    // 新增子目录与文件应被自动挂载并触发
    const addedDir = path.join(root, 'added')
    fs.mkdirSync(addedDir)
    await delay(100)
    fs.writeFileSync(path.join(addedDir, 'c.go'), 'package added\n')
    await delay(320)

    assert.ok(batches.flat().some((f) => f.endsWith('c.go')))
  } finally {
    watcher.close()
    fs.rmSync(root, { recursive: true, force: true })
  }
})

test('用户端同源网关：API 转发、SSE 实时流透传与 WebSocket 透明升级', async () => {
  const logger = new DevLogger({ useColor: false })
  const apiPort = await getFreePort()
  const flutterPort = await getFreePort()
  const gatewayPort = await getFreePort()

  const apiServer = http.createServer((req, res) => {
    if (req.url === '/api/v1/events') {
      res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' })
      res.write('data: hello-sse\n\n')
      setTimeout(() => res.end('data: bye-sse\n\n'), 60)
      return
    }
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ url: req.url, host: req.headers.host, origin: req.headers.origin }))
  })

  const flutterServer = http.createServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' })
    res.end(`flutter-page:${req.url}`)
  })
  const upgradedSockets = new Set()
  flutterServer.on('upgrade', (req, socket) => {
    upgradedSockets.add(socket)
    socket.on('close', () => upgradedSockets.delete(socket))
    socket.on('end', () => socket.destroy())
    const key = req.headers['sec-websocket-key'] || ''
    const accept = crypto.createHash('sha1').update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`).digest('base64')
    socket.write(
      'HTTP/1.1 101 Switching Protocols\r\n' +
      'Upgrade: websocket\r\n' +
      'Connection: Upgrade\r\n' +
      `Sec-WebSocket-Accept: ${accept}\r\n\r\n`
    )
    socket.on('data', (chunk) => socket.write(chunk))
  })

  await new Promise((r) => apiServer.listen(apiPort, '127.0.0.1', r))
  await new Promise((r) => flutterServer.listen(flutterPort, '127.0.0.1', r))

  const gateway = await startUserGateway({
    listenPort: gatewayPort,
    serverUserPort: apiPort,
    flutterPort,
    getClientMode: () => 'flutter',
    isServerEnabled: () => true,
    logger,
  })

  try {
    // 1. /api/v1 请求走主服务并保留原始 Host 与 Origin
    const apiRes = await httpRequest(gatewayPort, '/api/v1/capabilities', {
      headers: { Origin: `http://127.0.0.1:${gatewayPort}` },
    })
    assert.equal(apiRes.status, 200)
    const parsed = JSON.parse(apiRes.body)
    assert.equal(parsed.host, `127.0.0.1:${gatewayPort}`)
    assert.equal(parsed.origin, `http://127.0.0.1:${gatewayPort}`)

    // 2. SSE 无缓冲流式响应
    const sseRes = await httpRequest(gatewayPort, '/api/v1/events')
    assert.equal(sseRes.status, 200)
    assert.ok(sseRes.headers['content-type'].includes('text/event-stream'))
    assert.ok(sseRes.body.includes('data: hello-sse'))
    assert.ok(sseRes.body.includes('data: bye-sse'))

    // 3. 普通页面走 Flutter 上游
    const pageRes = await httpRequest(gatewayPort, '/courses')
    assert.equal(pageRes.status, 200)
    assert.equal(pageRes.body, 'flutter-page:/courses')

    // 4. WebSocket upgrade 透明双向透传
    const wsPayload = await new Promise((resolve, reject) => {
      const sock = net.connect(gatewayPort, '127.0.0.1', () => {
        sock.write(
          'GET /$dwdsSseHandler HTTP/1.1\r\n' +
          `Host: 127.0.0.1:${gatewayPort}\r\n` +
          'Upgrade: websocket\r\n' +
          'Connection: Upgrade\r\n' +
          'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n' +
          'Sec-WebSocket-Version: 13\r\n\r\n'
        )
      })
      let buf = ''
      let upgraded = false
      sock.on('data', (chunk) => {
        buf += chunk.toString('utf8')
        if (!upgraded && buf.includes('\r\n\r\n')) {
          upgraded = true
          sock.write('PING-WS')
          return
        }
        if (upgraded && buf.includes('PING-WS')) {
          sock.destroy()
          resolve(buf)
        }
      })
      sock.on('error', reject)
    })
    assert.ok(wsPayload.includes('101 Switching Protocols'))
    assert.ok(wsPayload.includes('PING-WS'))
  } finally {
    await gateway.close()
    for (const s of upgradedSockets) s.destroy()
    apiServer.closeAllConnections?.()
    flutterServer.closeAllConnections?.()
    await new Promise((r) => apiServer.close(r))
    await new Promise((r) => flutterServer.close(r))
  }
})

test('题库面板热读代理：磁盘资源 0ms 热生效、防路径穿越及 API 透传', async () => {
  const logger = new DevLogger({ useColor: false })
  const assetsDir = makeTempDir('byebyeocs-panel-assets-')
  fs.writeFileSync(path.join(assetsDir, 'index.html'), '<h1>v1</h1>')
  fs.writeFileSync(path.join(assetsDir, 'app.js'), 'console.log("v1")')

  const tikuPort = await getFreePort()
  const proxyPort = await getFreePort()
  const tikuServer = http.createServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ path: req.url, host: req.headers.host, origin: req.headers.origin }))
  })
  await new Promise((r) => tikuServer.listen(tikuPort, '127.0.0.1', r))

  const gateway = await startTikuPanelGateway({
    listenPort: proxyPort,
    tikuPort,
    assetsDir,
    liveAssets: true,
    logger,
  })

  try {
    // 1. /panel 重定向至 /panel/
    const redir = await httpRequest(proxyPort, '/panel')
    assert.equal(redir.status, 308)
    assert.equal(redir.headers.location, '/panel/')

    // 2. /panel/ 和 /panel/app.js 直读磁盘并立即响应修改
    const v1 = await httpRequest(proxyPort, '/panel/app.js')
    assert.equal(v1.status, 200)
    assert.equal(v1.body, 'console.log("v1")')
    assert.equal(v1.headers['cache-control'], 'no-store')

    fs.writeFileSync(path.join(assetsDir, 'app.js'), 'console.log("v2-hot")')
    const v2 = await httpRequest(proxyPort, '/panel/app.js')
    assert.equal(v2.status, 200)
    assert.equal(v2.body, 'console.log("v2-hot")')

    // 3. 路径穿越应被拒绝
    const traversal = await httpRequest(proxyPort, '/panel/%2e%2e/secret')
    assert.equal(traversal.status, 404)

    // 4. /panel/api/* 透传至题库 Go 后端并保留原始 Host / Origin
    const api = await httpRequest(proxyPort, '/panel/api/session', {
      headers: { Host: '127.0.0.1:5187', Origin: 'http://127.0.0.1:5187' },
    })
    assert.equal(api.status, 200)
    const data = JSON.parse(api.body)
    assert.equal(data.path, '/panel/api/session')
    assert.equal(data.host, '127.0.0.1:5187')
    assert.equal(data.origin, 'http://127.0.0.1:5187')
  } finally {
    await gateway.close()
    tikuServer.closeAllConnections?.()
    await new Promise((r) => tikuServer.close(r))
    fs.rmSync(assetsDir, { recursive: true, force: true })
  }
})
