// 跨平台递归目录监听器：支持防抖合并、新目录自动挂载以及 inotify 受限时的轮询回退。

import fs from 'node:fs'
import path from 'node:path'

const DEFAULT_IGNORE_DIRS = new Set([
  '.git',
  '.local',
  '.dart_tool',
  '.idea',
  '.vscode',
  'node_modules',
  'build',
  'dist',
  'bin',
  'coverage',
  'test-results',
  'playwright-report',
  '__pycache__',
])

export function watchTree({
  roots,
  ignoreDirs = DEFAULT_IGNORE_DIRS,
  matchFile = () => true,
  debounceMs = 260,
  pollIntervalMs = 800,
  onChange,
  onError = () => {},
}) {
  const rootList = (Array.isArray(roots) ? roots : [roots])
    .filter(Boolean)
    .map((item) => path.resolve(item))

  const watchers = new Map()
  const pendingFiles = new Set()
  let debounceTimer = null
  let pollTimer = null
  let pollSnapshot = new Map()
  let closed = false

  const emitChange = (fullPath) => {
    if (closed) return
    const normalized = path.resolve(fullPath)
    if (!matchFile(normalized)) return
    pendingFiles.add(normalized)
    if (debounceTimer) clearTimeout(debounceTimer)
    debounceTimer = setTimeout(() => {
      debounceTimer = null
      if (closed || pendingFiles.size === 0) return
      const files = [...pendingFiles]
      pendingFiles.clear()
      Promise.resolve(onChange(files)).catch((err) => onError(err))
    }, debounceMs)
  }

  const shouldIgnoreDir = (dirName) => {
    if (!dirName) return true
    if (ignoreDirs.has(dirName)) return true
    return false
  }

  const scanFiles = () => {
    const result = new Map()
    const visit = (target) => {
      let stat
      try {
        stat = fs.lstatSync(target)
      } catch {
        return
      }
      if (stat.isSymbolicLink()) return
      if (stat.isFile()) {
        if (matchFile(target)) {
          result.set(target, `${stat.mtimeMs}:${stat.size}`)
        }
        return
      }
      if (!stat.isDirectory()) return
      let entries
      try {
        entries = fs.readdirSync(target, { withFileTypes: true })
      } catch {
        return
      }
      for (const entry of entries) {
        if (entry.isSymbolicLink()) continue
        const child = path.join(target, entry.name)
        if (entry.isDirectory()) {
          if (!shouldIgnoreDir(entry.name)) visit(child)
        } else if (entry.isFile() && matchFile(child)) {
          try {
            const st = fs.statSync(child)
            result.set(child, `${st.mtimeMs}:${st.size}`)
          } catch {
            // 文件可能在扫描期间被移动或删除
          }
        }
      }
    }
    for (const root of rootList) visit(root)
    return result
  }

  const startPollingFallback = (reason) => {
    if (closed || pollTimer) return
    for (const w of watchers.values()) {
      try {
        w.close()
      } catch {
        // 忽略关闭错误
      }
    }
    watchers.clear()
    onError(new Error(`目录原生监听受限（${reason}），已自动切换为定时轮询模式`))
    pollSnapshot = scanFiles()
    pollTimer = setInterval(() => {
      if (closed) return
      const next = scanFiles()
      for (const [file, sig] of next.entries()) {
        if (pollSnapshot.get(file) !== sig) {
          emitChange(file)
        }
      }
      for (const file of pollSnapshot.keys()) {
        if (!next.has(file)) {
          emitChange(file)
        }
      }
      pollSnapshot = next
    }, pollIntervalMs)
  }

  const attachDir = (dirPath) => {
    if (closed || pollTimer || watchers.has(dirPath)) return
    let stat
    try {
      stat = fs.lstatSync(dirPath)
    } catch {
      return
    }
    if (stat.isSymbolicLink()) return
    if (stat.isFile()) {
      // 单文件监听通过其父目录捕获，兼容原子重命名写回（如编辑器保存 temp -> rename）。
      const parent = path.dirname(dirPath)
      const targetName = path.basename(dirPath)
      const key = `file:${dirPath}`
      if (watchers.has(key)) return
      try {
        const w = fs.watch(parent, { persistent: true }, (_eventType, filename) => {
          if (closed) return
          if (!filename || String(filename) === targetName) {
            emitChange(dirPath)
          }
        })
        w.on('error', (err) => {
          if (err?.code === 'ENOSPC' || err?.code === 'EMFILE') {
            startPollingFallback(err.code)
          }
        })
        watchers.set(key, w)
      } catch (err) {
        if (err?.code === 'ENOSPC' || err?.code === 'EMFILE') {
          startPollingFallback(err.code)
        }
      }
      return
    }
    if (!stat.isDirectory()) return

    try {
      const w = fs.watch(dirPath, { persistent: true }, (_eventType, filename) => {
        if (closed) return
        if (!filename) return
        const name = String(filename)
        if (shouldIgnoreDir(name)) return
        const full = path.join(dirPath, name)
        try {
          const st = fs.lstatSync(full)
          if (st.isDirectory() && !st.isSymbolicLink()) {
            attachTree(full)
            // 新建目录内可能已有源码文件，扫描并触发变更检查
            const entries = fs.readdirSync(full)
            for (const item of entries) emitChange(path.join(full, item))
            return
          }
        } catch {
          // 删除或重命名旧路径
          if (watchers.has(full)) {
            try {
              watchers.get(full).close()
            } catch {
              // 忽略
            }
            watchers.delete(full)
          }
        }
        emitChange(full)
      })
      w.on('error', (err) => {
        if (err?.code === 'ENOSPC' || err?.code === 'EMFILE') {
          startPollingFallback(err.code)
        }
      })
      watchers.set(dirPath, w)
    } catch (err) {
      if (err?.code === 'ENOSPC' || err?.code === 'EMFILE') {
        startPollingFallback(err.code)
        return
      }
      return
    }

    let entries
    try {
      entries = fs.readdirSync(dirPath, { withFileTypes: true })
    } catch {
      return
    }
    for (const entry of entries) {
      if (entry.isDirectory() && !entry.isSymbolicLink() && !shouldIgnoreDir(entry.name)) {
        attachDir(path.join(dirPath, entry.name))
      }
    }
  }

  const attachTree = (target) => {
    attachDir(target)
  }

  for (const root of rootList) {
    if (fs.existsSync(root)) {
      attachTree(root)
    }
  }

  return {
    close() {
      closed = true
      if (debounceTimer) clearTimeout(debounceTimer)
      if (pollTimer) clearInterval(pollTimer)
      for (const w of watchers.values()) {
        try {
          w.close()
        } catch {
          // 忽略关闭错误
        }
      }
      watchers.clear()
      pendingFiles.clear()
    },
    size() {
      return watchers.size
    },
  }
}
