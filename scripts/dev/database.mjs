// 开发隔离 PostgreSQL 管理：支持 Docker 容器、本机 PostgreSQL（initdb/pg_ctl）及外部 DSN。
// 自动维护主库（byebyeocs_dev）与题库独立库（byebyeocs_tiku_dev）及各自角色。

import crypto from 'node:crypto'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

import {
  IS_WINDOWS,
  delay,
  findExecutable,
  isPortInUse,
  runCommand,
  waitForTcpReady,
  writePrivateFile,
} from './process.mjs'

export const MAIN_DB_USER = 'byebyeocs_main'
export const MAIN_DB_NAME = 'byebyeocs_dev'
export const TIKU_DB_USER = 'byebyeocs_tiku'
export const TIKU_DB_NAME = 'byebyeocs_tiku_dev'

export function workspaceHash(stateDir) {
  return crypto.createHash('sha256').update(path.resolve(stateDir)).digest('hex').slice(0, 8)
}

export function dockerResourceNames(stateDir) {
  const id = workspaceHash(stateDir)
  return {
    container: `byebyeocs-dev-pg-${id}`,
    volume: `byebyeocs-dev-pgdata-${id}`,
  }
}

function extraPostgresBinDirs() {
  const dirs = []
  if (process.env.PGBIN) {
    dirs.push(process.env.PGBIN)
  }
  if (IS_WINDOWS) {
    const roots = [process.env.ProgramFiles || 'C:\\Program Files', process.env['ProgramFiles(x86)']]
    for (const root of roots) {
      if (!root) continue
      const pgRoot = path.join(root, 'PostgreSQL')
      if (!fs.existsSync(pgRoot)) continue
      try {
        const versions = fs.readdirSync(pgRoot).sort((a, b) => Number(b) - Number(a))
        for (const ver of versions) {
          dirs.push(path.join(pgRoot, ver, 'bin'))
        }
      } catch {
        // 忽略不可读目录
      }
    }
  } else {
    for (const base of ['/usr/lib/postgresql', '/usr/local/opt', '/opt/homebrew/opt']) {
      if (!fs.existsSync(base)) continue
      try {
        const entries = fs.readdirSync(base).sort().reverse()
        for (const entry of entries) {
          if (base === '/usr/lib/postgresql' || entry.startsWith('postgresql')) {
            dirs.push(path.join(base, entry, 'bin'))
          }
        }
      } catch {
        // 忽略
      }
    }
  }
  return dirs
}

export function detectLocalPostgresTools() {
  const extra = extraPostgresBinDirs()
  const initdb = findExecutable('initdb', extra)
  const pgCtl = findExecutable('pg_ctl', extra)
  const psql = findExecutable('psql', extra)
  const pgIsReady = findExecutable('pg_isready', extra)
  return {
    available: Boolean(initdb && pgCtl && psql),
    initdb,
    pgCtl,
    psql,
    pgIsReady,
  }
}

export async function detectDockerDaemon() {
  const dockerBin = findExecutable('docker')
  if (!dockerBin) {
    return { available: false, bin: null, reason: '未安装 docker 命令' }
  }
  try {
    const res = await runCommand(dockerBin, ['info', '--format', '{{.ServerVersion}}'], { timeoutMs: 5000 })
    if (res.code === 0) {
      return { available: true, bin: dockerBin, version: res.stdout.trim() }
    }
    return { available: false, bin: dockerBin, reason: (res.stderr || res.stdout || 'Docker 守护进程未运行').trim() }
  } catch (err) {
    return { available: false, bin: dockerBin, reason: err.message }
  }
}

function ensureRandomPassword(filePath) {
  if (fs.existsSync(filePath)) {
    const existing = fs.readFileSync(filePath, 'utf8').trim()
    if (existing.length >= 20) return existing
  }
  // 仅使用字母数字，免除 DSN URL 与 SQL 字面量转义问题。
  const value = crypto.randomBytes(24).toString('hex')
  writePrivateFile(filePath, `${value}\n`)
  return value
}

function buildRoleAndDatabaseSql(mainPassword, tikuPassword) {
  return [
    `DO $$`,
    `BEGIN`,
    `  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '${MAIN_DB_USER}') THEN`,
    `    CREATE ROLE ${MAIN_DB_USER} LOGIN PASSWORD '${mainPassword}';`,
    `  ELSE`,
    `    ALTER ROLE ${MAIN_DB_USER} WITH LOGIN PASSWORD '${mainPassword}';`,
    `  END IF;`,
    `  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '${TIKU_DB_USER}') THEN`,
    `    CREATE ROLE ${TIKU_DB_USER} LOGIN PASSWORD '${tikuPassword}';`,
    `  ELSE`,
    `    ALTER ROLE ${TIKU_DB_USER} WITH LOGIN PASSWORD '${tikuPassword}';`,
    `  END IF;`,
    `END $$;`,
    `SELECT 'CREATE DATABASE ${MAIN_DB_NAME} OWNER ${MAIN_DB_USER} ENCODING ''UTF8'''`,
    `  WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = '${MAIN_DB_NAME}')\\gexec`,
    `SELECT 'CREATE DATABASE ${TIKU_DB_NAME} OWNER ${TIKU_DB_USER} ENCODING ''UTF8'''`,
    `  WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = '${TIKU_DB_NAME}')\\gexec`,
    `ALTER DATABASE ${MAIN_DB_NAME} OWNER TO ${MAIN_DB_USER};`,
    `ALTER DATABASE ${TIKU_DB_NAME} OWNER TO ${TIKU_DB_USER};`,
    '',
  ].join('\n')
}

async function pickDockerPostgresImage(dockerBin, requestedImage) {
  if (requestedImage) return requestedImage
  try {
    const res = await runCommand(dockerBin, ['images', '--format', '{{.Repository}}:{{.Tag}}'], { timeoutMs: 5000 })
    if (res.code === 0) {
      const images = res.stdout.split(/\r?\n/).map((line) => line.trim()).filter(Boolean)
      for (const prefix of ['postgres:17', 'postgres:16', 'postgres:18']) {
        const hit = images.find((img) => img.startsWith(prefix))
        if (hit) return hit
      }
    }
  } catch {
    // 忽略本地镜像探测失败，回落默认镜像
  }
  return 'postgres:17-alpine'
}

export async function ensureDatabase(options, logger) {
  const secretsDir = path.join(options.stateDir, 'secrets')
  const mainDsnFile = path.join(secretsDir, 'main-database.dsn')
  const tikuDsnFile = path.join(secretsDir, 'tiku-database.dsn')

  // 1. 外部数据库模式
  if (options.dbMode === 'external') {
    const mainDsn = options.mainDsn || (fs.existsSync(mainDsnFile) ? fs.readFileSync(mainDsnFile, 'utf8').trim() : '')
    const tikuDsn = options.tikuDsn || (fs.existsSync(tikuDsnFile) ? fs.readFileSync(tikuDsnFile, 'utf8').trim() : '')
    if (options.modules.server && !mainDsn) {
      throw new Error('external 数据库模式需要提供 --main-dsn（或设置 BYEBYEOCS_DEV_MAIN_DSN）')
    }
    if (options.modules.tiku && !tikuDsn) {
      throw new Error('external 数据库模式且启用题库时，需要提供独立的 --tiku-dsn（或设置 BYEBYEOCS_DEV_TIKU_DSN）')
    }
    if (mainDsn) writePrivateFile(mainDsnFile, `${mainDsn}\n`)
    if (tikuDsn) writePrivateFile(tikuDsnFile, `${tikuDsn}\n`)
    logger.info('db', '使用外部 PostgreSQL 连接（跳过本地数据库生命周期管理）')
    return {
      backend: 'external',
      managed: false,
      mainDsnFile,
      tikuDsnFile,
      stop: async () => {},
    }
  }

  // 2. 决定使用 docker 还是 local-pg
  let chosenBackend = options.dbMode
  let dockerInfo = null
  let pgTools = null

  if (chosenBackend === 'auto') {
    dockerInfo = await detectDockerDaemon()
    if (dockerInfo.available) {
      chosenBackend = 'docker'
    } else {
      pgTools = detectLocalPostgresTools()
      if (pgTools.available) {
        chosenBackend = 'local-pg'
      } else {
        throw new Error(
          `未找到可用的本地 PostgreSQL 环境：\n` +
          `  - Docker 不可用（${dockerInfo.reason}）\n` +
          `  - 本机未检测到完整的 PostgreSQL 命令（initdb / pg_ctl / psql）\n` +
          `请安装并启动 Docker，或安装 PostgreSQL 16+，或使用 --main-dsn / --tiku-dsn 连接外部数据库。`
        )
      }
    }
  } else if (chosenBackend === 'docker') {
    dockerInfo = await detectDockerDaemon()
    if (!dockerInfo.available) {
      throw new Error(`Docker 不可用：${dockerInfo.reason}`)
    }
  } else if (chosenBackend === 'local-pg') {
    pgTools = detectLocalPostgresTools()
    if (!pgTools.available) {
      throw new Error('本机未检测到完整的 PostgreSQL 命令（需要 initdb、pg_ctl、psql）')
    }
  }

  const adminPassword = ensureRandomPassword(path.join(secretsDir, 'pg-admin.password'))
  const mainPassword = ensureRandomPassword(path.join(secretsDir, 'pg-main.password'))
  const tikuPassword = ensureRandomPassword(path.join(secretsDir, 'pg-tiku.password'))
  const pgPort = options.ports.pg
  const initSql = buildRoleAndDatabaseSql(mainPassword, tikuPassword)

  if (chosenBackend === 'docker') {
    const result = await startDockerPostgres({
      dockerBin: dockerInfo.bin,
      repoRoot: options.repoRoot,
      stateDir: options.stateDir,
      pgPort,
      pgImage: options.pgImage,
      adminPassword,
      initSql,
      logger,
    })
    writePrivateFile(mainDsnFile, `postgres://${MAIN_DB_USER}:${mainPassword}@127.0.0.1:${pgPort}/${MAIN_DB_NAME}?sslmode=disable\n`)
    writePrivateFile(tikuDsnFile, `postgres://${TIKU_DB_USER}:${tikuPassword}@127.0.0.1:${pgPort}/${TIKU_DB_NAME}?sslmode=disable\n`)
    return { ...result, mainDsnFile, tikuDsnFile }
  }

  const result = await startLocalPostgres({
    pgTools,
    stateDir: options.stateDir,
    pgPort,
    adminPassword,
    initSql,
    logger,
  })
  writePrivateFile(mainDsnFile, `postgres://${MAIN_DB_USER}:${mainPassword}@127.0.0.1:${pgPort}/${MAIN_DB_NAME}?sslmode=disable\n`)
  writePrivateFile(tikuDsnFile, `postgres://${TIKU_DB_USER}:${tikuPassword}@127.0.0.1:${pgPort}/${TIKU_DB_NAME}?sslmode=disable\n`)
  return { ...result, mainDsnFile, tikuDsnFile }
}

async function startDockerPostgres({ dockerBin, stateDir, pgPort, pgImage, adminPassword, initSql, logger }) {
  const { container, volume } = dockerResourceNames(stateDir)
  const inspect = await runCommand(dockerBin, ['inspect', container], { timeoutMs: 8000 })

  if (inspect.code === 0) {
    const [info] = JSON.parse(inspect.stdout)
    const running = Boolean(info?.State?.Running)
    const binding = info?.HostConfig?.PortBindings?.['5432/tcp']?.[0]
    const mappedPort = binding?.HostPort ? Number(binding.HostPort) : null
    if (mappedPort && mappedPort !== pgPort) {
      throw new Error(
        `已存在本工作区开发数据库容器 ${container}，但其绑定端口为 ${mappedPort} 而非 ${pgPort}。` +
        `请使用 --pg-port ${mappedPort}，或执行 "node scripts/dev.mjs reset" 重建开发数据库。`
      )
    }
    if (running) {
      logger.info('db', `复用运行中的 Docker PostgreSQL 容器 ${container}（127.0.0.1:${pgPort}）`)
    } else {
      if (await isPortInUse('127.0.0.1', pgPort)) {
        throw new Error(`端口 127.0.0.1:${pgPort} 已被其他进程占用，请通过 --pg-port 或 --port-offset 指定其他端口`)
      }
      logger.info('db', `启动已有 Docker PostgreSQL 容器 ${container}（127.0.0.1:${pgPort}）…`)
      const started = await runCommand(dockerBin, ['start', container], { timeoutMs: 30000 })
      if (started.code !== 0) {
        throw new Error(`启动容器 ${container} 失败：${(started.stderr || started.stdout).trim()}`)
      }
    }
  } else {
    if (await isPortInUse('127.0.0.1', pgPort)) {
      throw new Error(`端口 127.0.0.1:${pgPort} 已被其他进程占用，请通过 --pg-port 或 --port-offset 指定其他端口`)
    }
    const image = await pickDockerPostgresImage(dockerBin, pgImage)
    logger.info('db', `创建隔离开发 PostgreSQL 容器 ${container}（镜像 ${image}，端口 127.0.0.1:${pgPort}）…`)
    // 通过临时 0600 env-file 传入超级用户密码，避免密码出现在进程命令行中。
    const envFile = path.join(stateDir, 'secrets', `.docker-pg-${Date.now()}.env`)
    writePrivateFile(envFile, `POSTGRES_USER=postgres\nPOSTGRES_PASSWORD=${adminPassword}\n`)
    try {
      const created = await runCommand(dockerBin, [
        'run', '-d',
        '--name', container,
        '--env-file', envFile,
        '-p', `127.0.0.1:${pgPort}:5432`,
        '-v', `${volume}:/var/lib/postgresql/data`,
        image,
      ], { timeoutMs: 180000 })
      if (created.code !== 0) {
        throw new Error(`创建 PostgreSQL 容器失败：${(created.stderr || created.stdout).trim()}`)
      }
    } finally {
      try {
        fs.rmSync(envFile, { force: true })
      } catch {
        // 忽略清理失败
      }
    }
  }

  // 等待容器内 PostgreSQL 就绪
  const readyDeadline = Date.now() + 45000
  let ready = false
  while (Date.now() < readyDeadline) {
    const check = await runCommand(dockerBin, ['exec', container, 'pg_isready', '-U', 'postgres', '-h', '127.0.0.1'], { timeoutMs: 4000 })
    if (check.code === 0 && (await isPortInUse('127.0.0.1', pgPort, 300))) {
      ready = true
      break
    }
    await delay(400)
  }
  if (!ready) {
    throw new Error(`Docker PostgreSQL 容器 ${container} 在 45 秒内未就绪，请检查 "docker logs ${container}"`)
  }

  // 幂等初始化双独立角色与双独立数据库
  const sqlRes = await runCommand(
    dockerBin,
    ['exec', '-i', container, 'psql', '-U', 'postgres', '-d', 'postgres', '-v', 'ON_ERROR_STOP=1'],
    { input: initSql, timeoutMs: 15000 }
  )
  if (sqlRes.code !== 0) {
    throw new Error(`初始化主库与题库角色失败：${(sqlRes.stderr || sqlRes.stdout).trim()}`)
  }
  logger.success('db', `PostgreSQL 已就绪（Docker · 127.0.0.1:${pgPort} · 主库 ${MAIN_DB_NAME} / 题库 ${TIKU_DB_NAME}）`)

  return {
    backend: 'docker',
    managed: true,
    container,
    volume,
    port: pgPort,
    async stop() {
      logger.info('db', `正在停止 Docker PostgreSQL 容器 ${container}…`)
      await runCommand(dockerBin, ['stop', '-t', '10', container], { timeoutMs: 20000 })
    },
  }
}

async function startLocalPostgres({ pgTools, stateDir, pgPort, adminPassword, initSql, logger }) {
  const pgDataDir = path.join(stateDir, 'pgdata')
  const pgLogFile = path.join(stateDir, 'logs', 'postgres.log')
  // POSIX Unix socket 路径受 sun_path（约 104–108 字节）限制，统一放在系统临时目录短路径下。
  const socketDir = path.join(os.tmpdir(), `bbo-pg-${workspaceHash(stateDir)}`)
  fs.mkdirSync(path.dirname(pgLogFile), { recursive: true })

  const initialized = fs.existsSync(path.join(pgDataDir, 'PG_VERSION'))
  if (!initialized) {
    fs.mkdirSync(pgDataDir, { recursive: true, mode: 0o700 })
    const pwFile = path.join(stateDir, 'secrets', 'pg-admin.password')
    logger.info('db', `初始化本机隔离 PostgreSQL 数据目录 ${pgDataDir}…`)
    const init = await runCommand(pgTools.initdb, [
      '-D', pgDataDir,
      '-U', 'postgres',
      '--encoding=UTF8',
      '--no-locale',
      '--auth=scram-sha-256',
      `--pwfile=${pwFile}`,
    ], { timeoutMs: 60000 })
    if (init.code !== 0) {
      throw new Error(`initdb 初始化失败：${(init.stderr || init.stdout).trim()}`)
    }
  }

  const status = await runCommand(pgTools.pgCtl, ['-D', pgDataDir, 'status'], { timeoutMs: 5000 })
  if (status.code === 0) {
    logger.info('db', `复用运行中的本机 PostgreSQL 实例（127.0.0.1:${pgPort}）`)
  } else {
    if (await isPortInUse('127.0.0.1', pgPort)) {
      throw new Error(`端口 127.0.0.1:${pgPort} 已被其他进程占用，请通过 --pg-port 或 --port-offset 指定其他端口`)
    }
    let pgOpts = `-h 127.0.0.1 -p ${pgPort}`
    if (!IS_WINDOWS) {
      fs.mkdirSync(socketDir, { recursive: true, mode: 0o700 })
      pgOpts += ` -k '${socketDir.replace(/'/g, "'\\''")}'`
    }
    logger.info('db', `启动本机隔离 PostgreSQL 实例（127.0.0.1:${pgPort}）…`)
    // pg_ctl start 会拉起常驻 postgres 进程；使用 stdio: 'ignore' 防止 Windows 后台进程继承管道句柄导致阻塞，日志由 -l 写入文件。
    const started = await runCommand(pgTools.pgCtl, [
      '-D', pgDataDir,
      '-l', pgLogFile,
      '-o', pgOpts,
      '-w',
      'start',
    ], { stdio: 'ignore', timeoutMs: 30000 })
    if (started.code !== 0) {
      const logTail = fs.existsSync(pgLogFile)
        ? fs.readFileSync(pgLogFile, 'utf8').trim().split(/\r?\n/).slice(-8).join('\n')
        : ''
      throw new Error(`pg_ctl 启动失败（退出码 ${started.code}，日志：${pgLogFile}）${logTail ? `\n${logTail}` : ''}`)
    }
  }

  const tcpOk = await waitForTcpReady('127.0.0.1', pgPort, { timeoutMs: 25000 })
  if (!tcpOk) {
    throw new Error(`本机 PostgreSQL 在 127.0.0.1:${pgPort} 未就绪，请查看日志：${pgLogFile}`)
  }

  const sqlRes = await runCommand(
    pgTools.psql,
    ['-h', '127.0.0.1', '-p', String(pgPort), '-U', 'postgres', '-d', 'postgres', '-v', 'ON_ERROR_STOP=1'],
    {
      env: { ...process.env, PGPASSWORD: adminPassword },
      input: initSql,
      timeoutMs: 15000,
    }
  )
  if (sqlRes.code !== 0) {
    throw new Error(`初始化主库与题库角色失败：${(sqlRes.stderr || sqlRes.stdout).trim()}`)
  }
  logger.success('db', `PostgreSQL 已就绪（本机实例 · 127.0.0.1:${pgPort} · 主库 ${MAIN_DB_NAME} / 题库 ${TIKU_DB_NAME}）`)

  return {
    backend: 'local-pg',
    managed: true,
    pgDataDir,
    port: pgPort,
    async stop() {
      logger.info('db', '正在停止本机开发 PostgreSQL 实例…')
      await runCommand(pgTools.pgCtl, ['-D', pgDataDir, '-m', 'fast', 'stop'], { timeoutMs: 15000 })
    },
  }
}

export async function stopWorkspaceDatabase(options, logger) {
  let stoppedAny = false

  // 1. 尝试停止 Docker 容器
  const dockerInfo = await detectDockerDaemon()
  if (dockerInfo.available) {
    const { container } = dockerResourceNames(options.stateDir)
    const inspect = await runCommand(dockerInfo.bin, ['inspect', '--format', '{{.State.Running}}', container], { timeoutMs: 5000 })
    if (inspect.code === 0 && inspect.stdout.trim() === 'true') {
      logger.info('db', `正在停止 Docker 容器 ${container}…`)
      await runCommand(dockerInfo.bin, ['stop', '-t', '10', container], { timeoutMs: 20000 })
      logger.success('db', `已停止 Docker 容器 ${container}`)
      stoppedAny = true
    }
  }

  // 2. 尝试停止本机 pgdata 实例
  const pgDataDir = path.join(options.stateDir, 'pgdata')
  if (fs.existsSync(path.join(pgDataDir, 'PG_VERSION'))) {
    const pgTools = detectLocalPostgresTools()
    if (pgTools.pgCtl) {
      const status = await runCommand(pgTools.pgCtl, ['-D', pgDataDir, 'status'], { timeoutMs: 5000 })
      if (status.code === 0) {
        logger.info('db', `正在停止本机 PostgreSQL 实例（${pgDataDir}）…`)
        await runCommand(pgTools.pgCtl, ['-D', pgDataDir, '-m', 'fast', 'stop'], { timeoutMs: 15000 })
        logger.success('db', '已停止本机 PostgreSQL 实例')
        stoppedAny = true
      }
    }
  }

  if (!stoppedAny) {
    logger.info('db', '本工作区没有正在运行的开发数据库实例')
  }
}

export async function resetWorkspaceDatabase(options, logger) {
  await stopWorkspaceDatabase(options, logger)

  const dockerInfo = await detectDockerDaemon()
  if (dockerInfo.available) {
    const { container, volume } = dockerResourceNames(options.stateDir)
    const hasContainer = await runCommand(dockerInfo.bin, ['container', 'inspect', container], { timeoutMs: 5000 })
    if (hasContainer.code === 0) {
      const rmContainer = await runCommand(dockerInfo.bin, ['rm', '-f', container], { timeoutMs: 15000 })
      if (rmContainer.code === 0) {
        logger.info('db', `已移除开发数据库容器 ${container}`)
      }
    }
    const hasVolume = await runCommand(dockerInfo.bin, ['volume', 'inspect', volume], { timeoutMs: 5000 })
    if (hasVolume.code === 0) {
      const rmVolume = await runCommand(dockerInfo.bin, ['volume', 'rm', '-f', volume], { timeoutMs: 15000 })
      if (rmVolume.code === 0) {
        logger.info('db', `已移除开发数据库数据卷 ${volume}`)
      }
    }
  }
  if (!IS_WINDOWS) {
    try {
      fs.rmSync(path.join(os.tmpdir(), `bbo-pg-${workspaceHash(options.stateDir)}`), { recursive: true, force: true })
    } catch {
      // 忽略
    }
  }
}
