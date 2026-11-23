// 跨平台子进程启动、输出行转发、进程树清理、可执行文件探测与端口就绪检查。

import { spawn } from 'node:child_process'
import fs from 'node:fs'
import http from 'node:http'
import net from 'node:net'
import path from 'node:path'

export const IS_WINDOWS = process.platform === 'win32'

export function exeName(base) {
  return IS_WINDOWS ? `${base}.exe` : base
}

// 在 PATH 与额外候选目录中查找可执行文件，兼容 Windows 扩展名。
export function findExecutable(names, extraDirs = []) {
  const candidates = Array.isArray(names) ? names : [names]
  const pathEnv = process.env.PATH || process.env.Path || ''
  const searchDirs = [
    ...extraDirs.filter(Boolean),
    ...pathEnv.split(path.delimiter).filter(Boolean),
  ]
  const exts = IS_WINDOWS ? ['.exe', '.cmd', '.bat', ''] : ['']

  for (const name of candidates) {
    if (!name) continue
    // 显式路径直接检查
    if (name.includes(path.sep) || (IS_WINDOWS && name.includes('/'))) {
      if (isExecutableFile(name)) return path.resolve(name)
      for (const ext of exts) {
        if (ext && isExecutableFile(name + ext)) return path.resolve(name + ext)
      }
      continue
    }
    for (const dir of searchDirs) {
      for (const ext of exts) {
        const full = path.join(dir, name.endsWith(ext) ? name : name + ext)
        if (isExecutableFile(full)) return full
      }
    }
  }
  return null
}

function isExecutableFile(filePath) {
  try {
    const stat = fs.statSync(filePath)
    if (!stat.isFile()) return false
    if (!IS_WINDOWS) {
      fs.accessSync(filePath, fs.constants.X_OK)
    }
    return true
  } catch {
    return false
  }
}

// Windows 下优先将 npm 解析为直接调用 node.exe + npm-cli.js，彻底规避 cmd.exe 参数转义风险。
export function resolveNpmCommand() {
  const npmBin = findExecutable(['npm'])
  if (!npmBin) return null
  if (IS_WINDOWS) {
    const npmDir = path.dirname(npmBin)
    const cliCandidates = [
      path.join(npmDir, 'node_modules', 'npm', 'bin', 'npm-cli.js'),
      path.join(npmDir, '..', 'node_modules', 'npm', 'bin', 'npm-cli.js'),
    ]
    for (const candidate of cliCandidates) {
      if (fs.existsSync(candidate)) {
        return { command: process.execPath, prefixArgs: [candidate], display: npmBin }
      }
    }
  }
  return { command: npmBin, prefixArgs: [], display: npmBin }
}

// 为 Windows cmd.exe /d /s /c 构造带双引号的安全命令行字符串。
function quoteForCmd(value) {
  const text = String(value)
  if (/[\r\n]/.test(text)) {
    throw new Error('命令行参数不能包含换行符')
  }
  const escaped = text.replace(/"/g, '""')
  if (/[ \t&()<>|^%!]/.test(escaped) || escaped === '') {
    return `"${escaped}"`
  }
  return escaped
}

export function spawnCrossPlatform(command, args = [], options = {}) {
  const spawnOptions = {
    cwd: options.cwd,
    env: options.env ?? process.env,
    stdio: options.stdio ?? ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
    // POSIX 下使用独立进程组，便于关闭时连同孙子进程一并清理。
    detached: !IS_WINDOWS && options.detached !== false,
  }

  const lower = String(command).toLowerCase()
  if (IS_WINDOWS && (lower.endsWith('.cmd') || lower.endsWith('.bat'))) {
    const line = [command, ...args].map(quoteForCmd).join(' ')
    return spawn(process.env.ComSpec || 'cmd.exe', ['/d', '/s', '/c', `"${line}"`], {
      ...spawnOptions,
      windowsVerbatimArguments: true,
    })
  }
  return spawn(command, args, { ...spawnOptions, shell: false })
}

// 逐行读取流并回调，避免中文多字节或粘包截断。
export function attachLineReader(stream, onLine) {
  if (!stream) return
  let buffer = ''
  stream.setEncoding('utf8')
  stream.on('data', (chunk) => {
    buffer += chunk
    let idx = buffer.indexOf('\n')
    while (idx !== -1) {
      const line = buffer.slice(0, idx)
      buffer = buffer.slice(idx + 1)
      onLine(line)
      idx = buffer.indexOf('\n')
    }
  })
  stream.on('end', () => {
    if (buffer.length > 0) {
      onLine(buffer)
      buffer = ''
    }
  })
}

export function runCommand(command, args = [], options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawnCrossPlatform(command, args, {
      cwd: options.cwd,
      env: options.env,
      stdio: [options.input !== undefined ? 'pipe' : 'ignore', 'pipe', 'pipe'],
      detached: false,
    })

    const maxBytes = options.maxOutputBytes ?? 512 * 1024
    let stdout = ''
    let stderr = ''
    let settled = false
    let timer = null

    if (options.timeoutMs > 0) {
      timer = setTimeout(() => {
        if (settled) return
        settled = true
        void stopProcessTree(child, { timeoutMs: 2000 })
        reject(new Error(`命令超时（${options.timeoutMs}ms）：${path.basename(command)}`))
      }, options.timeoutMs)
    }

    child.stdout?.setEncoding('utf8')
    child.stderr?.setEncoding('utf8')
    child.stdout?.on('data', (chunk) => {
      if (stdout.length < maxBytes) stdout += chunk
      if (options.onStdout) options.onStdout(chunk)
    })
    child.stderr?.on('data', (chunk) => {
      if (stderr.length < maxBytes) stderr += chunk
      if (options.onStderr) options.onStderr(chunk)
    })

    if (options.input !== undefined && child.stdin) {
      child.stdin.on('error', () => {})
      child.stdin.end(options.input)
    }

    child.on('error', (err) => {
      if (settled) return
      settled = true
      if (timer) clearTimeout(timer)
      reject(err)
    })

    child.on('close', (code, signal) => {
      if (settled) return
      settled = true
      if (timer) clearTimeout(timer)
      resolve({ code: code ?? (signal ? 1 : 0), signal, stdout, stderr })
    })
  })
}

export async function stopProcessTree(child, { timeoutMs = 5000 } = {}) {
  if (!child || !child.pid || child.exitCode !== null || child.signalCode !== null) {
    return
  }

  const exited = new Promise((resolve) => {
    child.once('exit', () => resolve())
    child.once('close', () => resolve())
  })

  if (IS_WINDOWS) {
    let softRejected = false
    try {
      const res = await runCommand('taskkill', ['/pid', String(child.pid), '/T'], { timeoutMs: 3000 })
      if (res.code !== 0) softRejected = true
    } catch {
      softRejected = true
    }
    const waitMs = softRejected ? 200 : Math.min(timeoutMs, 2500)
    const done = await Promise.race([
      exited.then(() => true),
      delay(waitMs).then(() => false),
    ])
    if (!done && child.exitCode === null) {
      try {
        await runCommand('taskkill', ['/pid', String(child.pid), '/T', '/F'], { timeoutMs: 3000 })
      } catch {
        try {
          child.kill('SIGKILL')
        } catch {
          // 忽略已退出的进程
        }
      }
      await Promise.race([exited, delay(2000)])
    }
    return
  }

  // POSIX：优先向整个进程组发送 SIGTERM，确保孙子进程同步优雅退出。
  const sendGroup = (sig) => {
    try {
      process.kill(-child.pid, sig)
      return true
    } catch {
      try {
        child.kill(sig)
        return true
      } catch {
        return false
      }
    }
  }

  sendGroup('SIGTERM')
  const done = await Promise.race([
    exited.then(() => true),
    delay(timeoutMs).then(() => false),
  ])
  if (!done && child.exitCode === null) {
    sendGroup('SIGKILL')
    await Promise.race([exited, delay(2000)])
  }
}

export function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

export function isPortInUse(host, port, timeoutMs = 400) {
  return new Promise((resolve) => {
    const socket = net.createConnection({ host, port })
    let finished = false
    const finish = (inUse) => {
      if (finished) return
      finished = true
      socket.destroy()
      resolve(inUse)
    }
    socket.setTimeout(timeoutMs)
    socket.once('connect', () => finish(true))
    socket.once('timeout', () => finish(false))
    socket.once('error', () => finish(false))
  })
}

export async function waitForTcpReady(host, port, { timeoutMs = 20000, intervalMs = 200, isAborted = () => false } = {}) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (isAborted()) return false
    if (await isPortInUse(host, port, 300)) return true
    await delay(intervalMs)
  }
  return false
}

export async function waitForHttpReady(url, { timeoutMs = 30000, intervalMs = 250, acceptStatus = (code) => code >= 200 && code < 400, isAborted = () => false } = {}) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (isAborted()) return false
    const ok = await probeHttp(url, acceptStatus, 1000)
    if (ok) return true
    await delay(intervalMs)
  }
  return false
}

function probeHttp(url, acceptStatus, timeoutMs) {
  return new Promise((resolve) => {
    const req = http.get(url, { timeout: timeoutMs }, (res) => {
      res.resume()
      resolve(acceptStatus(res.statusCode || 0))
    })
    req.on('timeout', () => {
      req.destroy()
      resolve(false)
    })
    req.on('error', () => resolve(false))
  })
}

// 统一将文件设为仅当前用户可读（POSIX 0600；Windows 上仅保证普通可读写，由文件系统 ACL 保护）。
export function writePrivateFile(filePath, content) {
  fs.mkdirSync(path.dirname(filePath), { recursive: true, mode: 0o700 })
  if (!IS_WINDOWS) {
    try {
      fs.chmodSync(path.dirname(filePath), 0o700)
    } catch {
      // 忽略无法改权限的特殊挂载
    }
  }
  fs.writeFileSync(filePath, content, { encoding: 'utf8', mode: 0o600 })
  if (!IS_WINDOWS) {
    try {
      fs.chmodSync(filePath, 0o600)
    } catch {
      // 忽略
    }
  }
}
