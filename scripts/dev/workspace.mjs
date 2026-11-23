// 本地开发运行目录（.local/dev-stack/）初始化：密钥、配置、迁移与本地开发管理员/题库维护员。
// 保留用户已修改的配置与自定义模型/供应商，不在日志中输出任何明文密钥或密码。

import crypto from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'

import { IS_WINDOWS, runCommand, writePrivateFile } from './process.mjs'

export const DEV_ADMIN_USERNAME = 'dev-admin'
export const DEV_PANEL_USERNAME = 'dev-maintainer'

function ensureBase64Key32(filePath) {
  if (fs.existsSync(filePath)) {
    const existing = fs.readFileSync(filePath, 'utf8').trim()
    try {
      if (Buffer.from(existing, 'base64').length === 32) return
    } catch {
      // 文件损坏则重建
    }
  }
  const value = crypto.randomBytes(32).toString('base64')
  writePrivateFile(filePath, `${value}\n`)
}

function ensureSecretToken(filePath, bytes = 36) {
  if (fs.existsSync(filePath)) {
    const existing = fs.readFileSync(filePath, 'utf8').trim()
    if (existing.length >= 32) return existing
  }
  const value = crypto.randomBytes(bytes).toString('hex')
  writePrivateFile(filePath, `${value}\n`)
  return value
}

function yamlString(value) {
  return JSON.stringify(String(value))
}

function relativeUnix(baseDir, targetPath) {
  const rel = path.relative(baseDir, targetPath)
  // Go filepath 在 Windows 与 Linux 上均支持正斜杠，且避免 YAML 反斜杠转义歧义。
  return rel.split(path.sep).join('/')
}

export function workspacePaths(options) {
  const stateDir = options.stateDir
  const secretsDir = path.join(stateDir, 'secrets')
  return {
    stateDir,
    binDir: path.join(stateDir, 'bin'),
    dataDir: path.join(stateDir, 'data'),
    logsDir: path.join(stateDir, 'logs'),
    secretsDir,
    serverConfig: path.join(stateDir, 'server.yaml'),
    tikuConfig: path.join(stateDir, 'ocs-tiku-fork.json'),
    appKeyFile: path.join(secretsDir, 'application-v1.key'),
    identityKeyFile: path.join(secretsDir, 'identity.key'),
    mainDsnFile: path.join(secretsDir, 'main-database.dsn'),
    tikuDsnFile: path.join(secretsDir, 'tiku-database.dsn'),
    tikuTokenFile: path.join(secretsDir, 'tiku-service-token'),
    adminPasswordFile: path.join(secretsDir, 'dev-admin.password'),
    adminTotpFile: path.join(secretsDir, 'dev-admin-totp.json'),
    panelPasswordFile: path.join(secretsDir, 'panel-maintainer.password'),
    panelHashFile: path.join(secretsDir, 'panel-maintainer.hash'),
    flutterPidFile: path.join(stateDir, 'flutter.pid'),
    adminAssetsDir: path.join(options.repoRoot, 'apps', 'admin', 'dist'),
    clientAssetsDir: path.join(options.repoRoot, 'apps', 'client_flutter', 'build', 'web'),
    tikuPanelAssetsDir: path.join(options.repoRoot, 'services', 'tiku', 'internal', 'platform', 'panel', 'assets'),
  }
}

export function prepareWorkspaceConfigs(options, logger) {
  const paths = workspacePaths(options)
  for (const dir of [paths.stateDir, paths.binDir, paths.dataDir, paths.logsDir, paths.secretsDir]) {
    fs.mkdirSync(dir, { recursive: true, mode: dir === paths.secretsDir ? 0o700 : 0o755 })
  }
  if (!IS_WINDOWS) {
    try {
      fs.chmodSync(paths.secretsDir, 0o700)
    } catch {
      // 忽略
    }
  }

  ensureBase64Key32(paths.appKeyFile)
  ensureBase64Key32(paths.identityKeyFile)
  ensureSecretToken(paths.tikuTokenFile, 32)
  ensureSecretToken(paths.adminPasswordFile, 18)
  ensureSecretToken(paths.panelPasswordFile, 18)

  // 生成主服务 server.yaml（首次或 --reinit-config 时写入；保留用户已有修改）
  if (!fs.existsSync(paths.serverConfig) || options.reinitConfig) {
    const serverYaml = renderServerYaml(options, paths)
    writePrivateFile(paths.serverConfig, serverYaml)
    logger.info('dev', `已生成主服务开发配置：${path.relative(options.repoRoot, paths.serverConfig)}`)
  } else {
    logger.info('dev', `复用已有主服务开发配置：${path.relative(options.repoRoot, paths.serverConfig)}`)
  }

  // 生成或轻量同步题库服务 ocs-tiku-fork.json（保留用户已配置的供应商、模型与设置）
  if (options.modules.tiku) {
    if (!fs.existsSync(paths.tikuConfig) || options.reinitConfig) {
      const tikuJson = buildDefaultTikuConfig(options)
      writePrivateFile(paths.tikuConfig, `${JSON.stringify(tikuJson, null, 2)}\n`)
      logger.info('dev', `已生成题库开发配置：${path.relative(options.repoRoot, paths.tikuConfig)}`)
    } else {
      syncTikuEndpointsIfNeeded(paths.tikuConfig, options, logger)
    }
  }

  return paths
}

function renderServerYaml(options, paths) {
  const userOrigin = `http://127.0.0.1:${options.ports.user}`
  const adminOrigin = `http://127.0.0.1:${options.ports.admin}`
  const tikuEnabled = options.modules.tiku
  const adminAssetsRel = relativeUnix(paths.stateDir, paths.adminAssetsDir)
  const clientAssetsRel = relativeUnix(paths.stateDir, paths.clientAssetsDir)

  return `# 由 scripts/dev.mjs 生成的本地开发配置（仅限回环调试）。
# 可按需自定义学习通接入或并发参数；常规启动不会覆盖本文件，重置请加 --reinit-config。
mode: development

server:
  user_listen: ${yamlString(`127.0.0.1:${options.ports.serverUser}`)}
  admin_listen: ${yamlString(`127.0.0.1:${options.ports.serverAdmin}`)}
  read_header_timeout: 5s
  read_timeout: 10s
  write_timeout: 15s
  idle_timeout: 1m
  shutdown_timeout: 10s
  trusted_proxies:
    - "127.0.0.1/32"
    - "::1/128"

database:
  dsn_file: "secrets/main-database.dsn"
  max_connections: 16
  connect_timeout: 5s
  query_timeout: 5s

log:
  level: ${yamlString(options.logLevel)}

auth:
  enabled: true
  encryption_key_file: "secrets/application-v1.key"
  encryption_key_version: 1
  identity_key_file: "secrets/identity.key"
  cookie_secure: false
  local_admin_auto_login: true
  user_origin: ${yamlString(userOrigin)}
  admin_origin: ${yamlString(adminOrigin)}
  session_ttl: 12h

storage:
  data_dir: "data"
  admin_assets_dir: ${yamlString(adminAssetsRel)}
  client_assets_dir: ${yamlString(clientAssetsRel)}

learning:
  enabled: false
  execution_enabled: false
  execution_evidence_reference: ""
  global_concurrency: 4
  account_concurrency: 4
  request_interval: 300ms
  chapter_concurrency: 4

payments:
  enabled: false
  charging_enabled: false
  merchant_id: ""
  key_file: ""
  notify_url: ""
  return_url: ""

questions:
  enabled: ${tikuEnabled ? 'true' : 'false'}
  allow_shared_cache: false
  url: ${yamlString(`http://127.0.0.1:${options.ports.tiku}`)}
  token_file: "secrets/tiku-service-token"
  timeout: 95s
  concurrency: 4

operations:
  disk_protection: true
  retention_interval: 24h
  export_max_bytes: 8388608
  max_event_streams: 64
  require_fresh_backup: false
  backup_status_file: "secrets/backup-status.json"
  backup_max_age: 15m
`
}

function buildDefaultTikuConfig(options) {
  return {
    listen: `127.0.0.1:${options.ports.tiku}`,
    database_dsn_file: 'secrets/tiku-database.dsn',
    database_max_connections: 6,
    service_token_file: 'secrets/tiku-service-token',
    default_model_id: '',
    caption_model_id: '',
    allow_loopback_upstreams: true,
    prompt_version: 'fork-p1',
    normalization_version: 'fork-n1',
    limits: {
      max_request_bytes: 33554432,
      max_images: 8,
      max_image_bytes: 5242880,
      max_image_pixels: 16000000,
      max_output_tokens: 32768,
      concurrency: 4,
      timeout_seconds: 90,
      idempotency_retention_hours: 168,
    },
    cache: {
      ttl_days: 7,
      max_ttl_days: 30,
      shared_enabled: false,
    },
    models: [],
    panel: {
      enabled: true,
      origin: `http://127.0.0.1:${options.ports.admin}`,
      local_auto_login: true,
      users: [
        {
          username: DEV_PANEL_USERNAME,
          password_hash_file: 'secrets/panel-maintainer.hash',
          role: 'maintainer',
        },
      ],
    },
  }
}

function syncTikuEndpointsIfNeeded(configPath, options, logger) {
  try {
    const parsed = JSON.parse(fs.readFileSync(configPath, 'utf8'))
    const desiredListen = `127.0.0.1:${options.ports.tiku}`
    const desiredOrigin = `http://127.0.0.1:${options.ports.admin}`
    let changed = false
    if (parsed.listen !== desiredListen) {
      parsed.listen = desiredListen
      changed = true
    }
    if (parsed.panel && parsed.panel.enabled && parsed.panel.origin !== desiredOrigin) {
      parsed.panel.origin = desiredOrigin
      changed = true
    }
    if (changed) {
      writePrivateFile(configPath, `${JSON.stringify(parsed, null, 2)}\n`)
      logger.info('dev', '已同步题库开发配置的监听地址与面板来源（保留其余自定义项）')
    } else {
      logger.info('dev', `复用已有题库开发配置：${path.relative(options.repoRoot, configPath)}`)
    }
  } catch (err) {
    logger.warn('dev', `题库配置文件解析异常，保持原文件未覆盖：${err.message}`)
  }
}

// 为主服务运行期构造精准的环境变量覆盖，避免因本次命令行端口或开关覆写用户 server.yaml。
export function buildServerRuntimeEnv(options) {
  const env = {
    ...process.env,
    BYEBYEOCS_USER_LISTEN: `127.0.0.1:${options.ports.serverUser}`,
    BYEBYEOCS_ADMIN_LISTEN: `127.0.0.1:${options.ports.serverAdmin}`,
    BYEBYEOCS_USER_ORIGIN: `http://127.0.0.1:${options.ports.user}`,
    BYEBYEOCS_ADMIN_ORIGIN: `http://127.0.0.1:${options.ports.admin}`,
    BYEBYEOCS_LOG_LEVEL: options.logLevel,
  }
  if (options.modules.tiku) {
    env.BYEBYEOCS_TIKU_ENABLED = 'true'
    env.BYEBYEOCS_TIKU_URL = `http://127.0.0.1:${options.ports.tiku}`
  } else {
    env.BYEBYEOCS_TIKU_ENABLED = 'false'
  }
  if (options.devLogin) {
    env.BYEBYEOCS_DEV_LOGIN = '1'
  } else {
    delete env.BYEBYEOCS_DEV_LOGIN
  }
  return env
}

// 确保题库维护员 bcrypt 密码哈希已生成（调用编译好的 ocs-tiku-fork panel-password）。
export async function ensureTikuPanelHash(tikuBin, paths, logger) {
  if (fs.existsSync(paths.panelHashFile) && fs.statSync(paths.panelHashFile).size > 20) {
    return
  }
  if (fs.existsSync(paths.panelHashFile)) {
    fs.rmSync(paths.panelHashFile, { force: true })
  }
  const res = await runCommand(tikuBin, [
    'panel-password',
    '-out', paths.panelHashFile,
    '-password-file', paths.panelPasswordFile,
  ], { timeoutMs: 30000 })
  if (res.code !== 0) {
    throw new Error(`生成题库面板维护密码哈希失败：${(res.stderr || res.stdout).trim()}`)
  }
  logger.info('tiku', `已生成本地题库维护员凭据（账号 ${DEV_PANEL_USERNAME}，已启用回环自动登录）`)
}

// 确保本地开发管理员账号已创建（幂等：已存在时不报错）。
export async function ensureDevAdminAccount(serverBin, paths, runtimeEnv, logger) {
  if (fs.existsSync(paths.adminTotpFile)) {
    return
  }
  const res = await runCommand(serverBin, [
    'admin', 'create',
    '--config', paths.serverConfig,
    '--username', DEV_ADMIN_USERNAME,
    '--password-file', paths.adminPasswordFile,
    '--totp-output-file', paths.adminTotpFile,
  ], { env: runtimeEnv, timeoutMs: 30000 })
  if (res.code === 0) {
    logger.info('server', `已创建本地开发管理员（账号 ${DEV_ADMIN_USERNAME}，已启用回环自动登录）`)
    return
  }
  const combined = `${res.stdout}\n${res.stderr}`
  if (combined.includes('conflict') || combined.includes('已存在')) {
    logger.info('server', `本地开发管理员 ${DEV_ADMIN_USERNAME} 已存在，直接复用`)
    return
  }
  throw new Error(`创建本地开发管理员失败：${combined.trim()}`)
}
