#!/usr/bin/env node
// 跨平台（Windows / Ubuntu）全栈开发脚本端到端冒烟验证，供本地复核与 CI 运行。

import { spawn } from 'node:child_process'
import fs from 'node:fs'
import http from 'node:http'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { delay, runCommand } from './process.mjs'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const REPO_ROOT = path.resolve(__dirname, '..', '..')

const PORT_OFFSET = Number.parseInt(process.env.BYEBYEOCS_CI_PORT_OFFSET || '240', 10)
const DB_MODE = process.env.BYEBYEOCS_CI_DB_MODE || 'local-pg'
const STATE_DIR = fs.mkdtempSync(path.join(os.tmpdir(), 'byebyeocs-dev-ci-'))

const PORTS = {
  user: 5188 + PORT_OFFSET,
  admin: 5187 + PORT_OFFSET,
  serverUser: 8090 + PORT_OFFSET,
  serverAdmin: 8091 + PORT_OFFSET,
  tiku: 8092 + PORT_OFFSET,
}

function httpGet(url, timeoutMs = 8000) {
  return new Promise((resolve, reject) => {
    const req = http.get(url, { timeout: timeoutMs }, (res) => {
      let body = ''
      res.on('data', (chunk) => {
        body += chunk
      })
      res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body }))
    })
    req.on('error', reject)
    req.on('timeout', () => req.destroy(new Error(`请求超时：${url}`)))
  })
}

async function runRound(roundName, verifyHotReload) {
  console.log(`\n=== CI 冒烟验证：${roundName} ===`)
  const logLines = []
  const args = [
    path.join(REPO_ROOT, 'scripts', 'dev.mjs'),
    'up',
    '--db', DB_MODE,
    '--port-offset', String(PORT_OFFSET),
    '--dev-login',
    '--client-mode', 'static',
    '--state-dir', STATE_DIR,
  ]
  if (process.env.BYEBYEOCS_DEV_MAIN_DSN) {
    args.push('--main-dsn', process.env.BYEBYEOCS_DEV_MAIN_DSN)
  }
  if (process.env.BYEBYEOCS_DEV_TIKU_DSN) {
    args.push('--tiku-dsn', process.env.BYEBYEOCS_DEV_TIKU_DSN)
  }

  const child = spawn(process.execPath, args, {
    cwd: REPO_ROOT,
    stdio: ['pipe', 'pipe', 'pipe'],
  })

  let readyResolve
  const readyPromise = new Promise((resolve) => {
    readyResolve = resolve
  })

  const collect = (chunk, prefix = '') => {
    for (const line of chunk.toString('utf8').split(/\r?\n/)) {
      if (!line.trim()) continue
      logLines.push(prefix + line)
      process.stdout.write(`${prefix}${line}\n`)
      if (line.includes('本地开发环境已就绪')) {
        readyResolve()
      }
    }
  }
  child.stdout.on('data', (c) => collect(c))
  child.stderr.on('data', (c) => collect(c, '[ERR] '))

  try {
    let startTimer
    try {
      await Promise.race([
        readyPromise,
        new Promise((_, rej) => {
          startTimer = setTimeout(() => rej(new Error('等待开发环境就绪超时（120s）')), 120000)
          startTimer.unref?.()
        }),
        new Promise((_, rej) => child.once('exit', (code) => rej(new Error(`scripts/dev.mjs 提前退出，退出码 ${code}`)))),
      ])
    } finally {
      if (startTimer) clearTimeout(startTimer)
    }

    const checks = [
      ['主服务就绪', `http://127.0.0.1:${PORTS.serverAdmin}/health/ready`],
      ['题库就绪', `http://127.0.0.1:${PORTS.tiku}/readyz`],
      ['用户端同源能力', `http://127.0.0.1:${PORTS.user}/api/v1/capabilities`],
      ['管理端 Vite 页面', `http://127.0.0.1:${PORTS.admin}/`],
      ['管理端回环自动登录', `http://127.0.0.1:${PORTS.admin}/admin/api/v1/auth/session`],
      ['同域题库面板页面', `http://127.0.0.1:${PORTS.admin}/panel/`],
      ['同域题库面板自动登录', `http://127.0.0.1:${PORTS.admin}/panel/api/session`],
    ]

    for (const [label, url] of checks) {
      const res = await httpGet(url)
      console.log(`CHECK [${label}] ${url} -> HTTP ${res.status}`)
      if (res.status !== 200) {
        throw new Error(`${label} 状态码异常：${res.status}，响应：${res.body.slice(0, 200)}`)
      }
      if (url.endsWith('/api/v1/capabilities')) {
        const cap = JSON.parse(res.body)
        if (!cap.authentication?.enabled || !cap.features?.tiku) {
          throw new Error(`用户端能力配置异常：${res.body}`)
        }
      }
      if (url.endsWith('/admin/api/v1/auth/session')) {
        const session = JSON.parse(res.body)
        if (!session.authenticated || !res.headers['set-cookie']) {
          throw new Error(`管理端自动登录未签发会话：${res.body}`)
        }
      }
      if (url.endsWith('/panel/') && res.headers['x-byebyeocs-live-asset'] !== '1') {
        throw new Error('题库面板未命中磁盘实时热读代理')
      }
      if (url.endsWith('/panel/api/session')) {
        const panelSession = JSON.parse(res.body)
        if (panelSession.username !== 'dev-maintainer' || !panelSession.local_auto_login) {
          throw new Error(`题库面板自动登录响应异常：${res.body}`)
        }
      }
    }

    if (verifyHotReload) {
      console.log('\n=== 验证 Go 主服务保存触发增量重编热重启 ===')
      const probeFile = path.join(REPO_ROOT, 'server', 'internal', 'transport', 'httpapi', 'ci_hot_reload_probe_tmp.go')
      const prevLen = logLines.length
      fs.writeFileSync(probeFile, 'package httpapi\n\nconst _ciHotReloadProbe = 1\n', 'utf8')
      try {
        const deadline = Date.now() + 45000
        let reloaded = false
        while (Date.now() < deadline) {
          const recent = logLines.slice(prevLen).join('\n')
          if (recent.includes('主服务（devfixture） 已就绪')) {
            reloaded = true
            break
          }
          await delay(300)
        }
        if (!reloaded) {
          throw new Error('45 秒内未观察到主服务热编译重启完成')
        }
        const afterReady = await httpGet(`http://127.0.0.1:${PORTS.serverAdmin}/health/ready`)
        if (afterReady.status !== 200) {
          throw new Error(`热重启后健康检查失败：HTTP ${afterReady.status}`)
        }
        console.log('CHECK [主服务热编译重启] 成功，/health/ready -> 200')
      } finally {
        fs.rmSync(probeFile, { force: true })
        await delay(500)
      }
    }

    // 通过标准输入发送 quit 指令实现跨 Windows / Linux 的优雅退出校验。
    const exited = new Promise((resolve) => child.once('exit', (code, signal) => resolve({ code, signal })))
    child.stdin.write('q\n')
    let stopTimer
    let exitInfo
    try {
      exitInfo = await Promise.race([
        exited,
        new Promise((_, rej) => {
          stopTimer = setTimeout(() => rej(new Error('优雅退出超时（35s）')), 35000)
          stopTimer.unref?.()
        }),
      ])
    } finally {
      if (stopTimer) clearTimeout(stopTimer)
    }
    console.log(`CHECK [${roundName} 优雅退出]`, JSON.stringify(exitInfo))
    if (exitInfo.code !== 0) {
      throw new Error(`退出码非 0：${JSON.stringify(exitInfo)}`)
    }
  } catch (err) {
    try {
      child.stdin.write('q\n')
    } catch {
      // 忽略
    }
    await delay(2000)
    try {
      child.kill('SIGKILL')
    } catch {
      // 忽略
    }
    throw err
  }
}

async function main() {
  try {
    await runRound('第 1 轮（冷启动初始化 + 自动登录 + Go 热重启）', true)
    await runRound('第 2 轮（复用已有数据库与配置二次启动）', false)
  } finally {
    const resetRes = await runCommand(
      process.execPath,
      [path.join(REPO_ROOT, 'scripts', 'dev.mjs'), 'reset', '--yes', '--state-dir', STATE_DIR],
      { cwd: REPO_ROOT, timeoutMs: 30000 }
    )
    if (resetRes.stdout) process.stdout.write(resetRes.stdout)
    if (resetRes.stderr) process.stderr.write(resetRes.stderr)
    fs.rmSync(STATE_DIR, { recursive: true, force: true })
  }
  console.log('\n全部 CI 全栈冒烟检查通过。')
}

main().catch((err) => {
  console.error('CI 冒烟验证失败：', err.message)
  process.exit(1)
})
