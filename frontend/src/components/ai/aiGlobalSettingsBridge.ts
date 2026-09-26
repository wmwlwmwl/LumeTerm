// 桥接模块（自 .js 收编后类型化）：AI 全局设置归一化与持久化
import { normalizeAISlashCommands, type AISlashCommand } from './aiSlashCommands.ts'
import { getProxyNodes, type ProxyNode } from '../settings/proxyNodesBridge.ts'

/** AI 协作提示预设 */
interface AICollaborationPromptPreset {
  id: string
  title: string
  text: string
}

export interface AISystemPromptPreset {
  id: string
  title: string
  text: string
}

/** 审批按钮顺序 */
type ApprovalButtonOrder = 'reject-approve' | 'approve-reject'
/** 命令操作按钮顺序 */
type CommandActionButtonOrder = 'terminate-continue' | 'continue-terminate'

type ExecuteApprovalMode = 'basic' | 'read_only' | 'all'

export type AIGlobalSettings = {
  currentProviderId: string
  autoApprovalEnabled: boolean
  alwaysAllowReadOnly: boolean
  alwaysAllowReadOnlyOutsideWorkspace: boolean
  alwaysAllowWrite: boolean
  alwaysAllowWriteOutsideWorkspace: boolean
  alwaysAllowWriteProtected: boolean
  alwaysAllowExecute: boolean
  executeApprovalMode: ExecuteApprovalMode
  allowedCommands: string[]
  deniedCommands: string[]
  slashCommands: AISlashCommand[]
  collaborationPromptPresets: AICollaborationPromptPreset[]
  systemPromptPresets: AISystemPromptPreset[]
  collaborationExtraPrompt: string
  alwaysAllowMcp: boolean
  alwaysAllowModeSwitch: boolean
  alwaysAllowSubtasks: boolean
  alwaysAllowFollowupQuestions: boolean
  soundEnabled: boolean
  soundVolume: number
  mcpEnabled: boolean
  mcpAllowBrowserCalls: boolean
  mcpRequireApproval: boolean
  mcpActivityVisible: boolean
  mcpTerminalFollowLatest: boolean
  terminalIsolation: boolean
  confirmDelete: boolean
  continueAfterToolRejection: boolean
  conversationAutoBackupEnabled: boolean
  conversationAutoBackupRetentionCount: number
  messageNavEnabled: boolean
  aiWorkspaceTabNumbersOnly: boolean
  autoCondenseEnabled: boolean
  autoCondenseThresholdRatio: number
  // 是否把每轮 AI 请求与原始响应流写入 ai.log(默认开启)
  aiDebugLogEnabled: boolean
  approvalButtonOrder: ApprovalButtonOrder
  commandActionButtonOrder: CommandActionButtonOrder
  toolResultTokenThreshold: number
  aiRequestProxyId: string
  updatedAt: number
  proxyNodes: ProxyNode[]
}

export const DEFAULT_CONVERSATION_AUTO_BACKUP_RETENTION_COUNT = 30
export const MAX_CONVERSATION_AUTO_BACKUP_RETENTION_COUNT = 200

const DEFAULT_AI_GLOBAL_SETTINGS: AIGlobalSettings = {
  currentProviderId: '',
  autoApprovalEnabled: false,
  alwaysAllowReadOnly: false,
  alwaysAllowReadOnlyOutsideWorkspace: false,
  alwaysAllowWrite: false,
  alwaysAllowWriteOutsideWorkspace: false,
  alwaysAllowWriteProtected: false,
  alwaysAllowExecute: false,
  executeApprovalMode: 'basic',
  allowedCommands: [],
  deniedCommands: [],
  slashCommands: [],
  collaborationPromptPresets: [],
  systemPromptPresets: [],
  collaborationExtraPrompt: '',
  alwaysAllowMcp: false,
  alwaysAllowModeSwitch: false,
  alwaysAllowSubtasks: false,
  alwaysAllowFollowupQuestions: false,
  soundEnabled: true,
  soundVolume: 0.06,
  mcpEnabled: true,
  mcpAllowBrowserCalls: false,
  mcpRequireApproval: false,
  mcpActivityVisible: false,
  mcpTerminalFollowLatest: true,
  terminalIsolation: true,
  confirmDelete: true,
  continueAfterToolRejection: true,
  conversationAutoBackupEnabled: true,
  conversationAutoBackupRetentionCount: DEFAULT_CONVERSATION_AUTO_BACKUP_RETENTION_COUNT,
  messageNavEnabled: true,
  aiWorkspaceTabNumbersOnly: false,
  autoCondenseEnabled: false,
  autoCondenseThresholdRatio: 0.8,
  aiDebugLogEnabled: true,
  approvalButtonOrder: 'reject-approve',
  commandActionButtonOrder: 'terminate-continue',
  toolResultTokenThreshold: 350000,
  aiRequestProxyId: '',
  updatedAt: 0,
  proxyNodes: [],
}

const VALID_APPROVAL_BUTTON_ORDERS = new Set(['reject-approve', 'approve-reject'])
const VALID_COMMAND_ACTION_BUTTON_ORDERS = new Set(['terminate-continue', 'continue-terminate'])
let cachedAIGlobalSettings: AIGlobalSettings | null = null

export function getCachedAIGlobalSettings(): AIGlobalSettings | null {
  return cachedAIGlobalSettings
}

function getAppBridge() {
  return window?.go?.wailsapp?.AIBindings || window?.go?.wailsapp?.App
}

function normalizeStringList(values: unknown): string[] {
  if (!Array.isArray(values)) {
    return []
  }
  const seen = new Set<string>()
  const normalized: string[] = []
  values.forEach((value) => {
    if (typeof value !== 'string') {
      return
    }
    const nextValue = value.trim()
    if (!nextValue || seen.has(nextValue)) {
      return
    }
    seen.add(nextValue)
    normalized.push(nextValue)
  })
  return normalized
}

function normalizeApprovalButtonOrder(value: unknown): ApprovalButtonOrder {
  const nextValue = typeof value === 'string' ? value.trim() : ''
  return (VALID_APPROVAL_BUTTON_ORDERS.has(nextValue) ? nextValue : 'reject-approve') as ApprovalButtonOrder
}

function normalizeCommandActionButtonOrder(value: unknown): CommandActionButtonOrder {
  const nextValue = typeof value === 'string' ? value.trim() : ''
  return (VALID_COMMAND_ACTION_BUTTON_ORDERS.has(nextValue) ? nextValue : 'terminate-continue') as CommandActionButtonOrder
}

/** 自动压缩触发阈值：非法或缺省回退 0.8，并夹取到 [0.3, 0.95]（与后端 NormalizeAIGlobalSettings 一致） */
function normalizeAICondenseThresholdRatio(value: unknown): number {
  const parsed = Number(value)
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return 0.8
  }
  return Math.min(0.95, Math.max(0.3, parsed))
}

function normalizeSoundVolume(value: unknown): number {
  const parsed = Number(value)
  if (!Number.isFinite(parsed)) {
    return 0.06
  }
  if (parsed < 0) {
    return 0
  }
  if (parsed > 1) {
    return 1
  }
  return parsed
}

function normalizeToolResultTokenThreshold(value: unknown): number {
  const parsed = Number(value)
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return 350000
  }
  return Math.max(1, Math.trunc(parsed))
}

function normalizeConversationAutoBackupRetentionCount(value: unknown): number {
  const parsed = Number(value)
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return DEFAULT_CONVERSATION_AUTO_BACKUP_RETENTION_COUNT
  }
  return Math.min(MAX_CONVERSATION_AUTO_BACKUP_RETENTION_COUNT, Math.max(1, Math.trunc(parsed)))
}

function normalizeProxyType(value: unknown): 'http' | 'socks5' {
  return String(value || '').trim().toLowerCase() === 'http' ? 'http' : 'socks5'
}

function normalizeProxyNode(node: unknown, index = 0): ProxyNode | null {
  const n = (node ?? {}) as Record<string, unknown>
  const host = typeof n.host === 'string' ? n.host.trim() : ''
  if (!host) {
    return null
  }
  const parsedPort = parseInt(String(n.port ?? '').trim(), 10)
  const port = Number.isFinite(parsedPort) && parsedPort > 0 && parsedPort <= 65535 ? parsedPort : 1080
  const type = normalizeProxyType(n.type)
  const generatedId = `proxy-${type}-${host.toLowerCase()}-${port}-${index + 1}`
  const id = typeof n.id === 'string' && n.id.trim() ? n.id.trim() : generatedId
  return {
    id,
    name: typeof n.name === 'string' ? n.name.trim() : '',
    type,
    host,
    port,
    username: typeof n.username === 'string' ? n.username.trim() : '',
    password: typeof n.password === 'string' ? n.password : '',
    updatedAt: Number.isFinite(Number(n.updatedAt)) && Number(n.updatedAt) > 0 ? Number(n.updatedAt) : Date.now(),
  }
}

function normalizeAICollaborationPromptPresets(values: unknown): AICollaborationPromptPreset[] {
  if (!Array.isArray(values)) {
    return []
  }
  const seen = new Set<string>()
  const normalized: AICollaborationPromptPreset[] = []
  values.forEach((value, index) => {
    const v = value as Record<string, unknown> | null | undefined
    const text = typeof v?.text === 'string' ? v.text.replace(/\r\n/g, '\n').trim() : ''
    if (!text) {
      return
    }
    const rawId = typeof v?.id === 'string' ? v.id.trim() : ''
    const id = rawId || `collab-preset-${Date.now()}-${index + 1}`
    if (seen.has(id)) {
      return
    }
    const rawTitle = typeof v?.title === 'string' ? v.title.trim() : ''
    seen.add(id)
    normalized.push({
      id,
      title: rawTitle || text,
      text,
    })
  })
  return normalized
}

export function normalizeAISystemPromptPresets(values: unknown): AISystemPromptPreset[] {
  if (!Array.isArray(values)) {
    return []
  }
  const seen = new Set<string>()
  const normalized: AISystemPromptPreset[] = []
  values.forEach((value, index) => {
    const v = value as Record<string, unknown> | null | undefined
    const text = typeof v?.text === 'string' ? v.text.replace(/\r\n/g, '\n').trim() : ''
    if (!text) {
      return
    }
    const rawId = typeof v?.id === 'string' ? v.id.trim() : ''
    const id = rawId || `system-prompt-preset-${Date.now()}-${index + 1}`
    if (seen.has(id)) {
      return
    }
    const rawTitle = typeof v?.title === 'string' ? v.title.trim() : ''
    seen.add(id)
    normalized.push({
      id,
      title: rawTitle || text,
      text,
    })
  })
  return normalized
}

function normalizeProxyNodes(values: unknown): ProxyNode[] {
  if (!Array.isArray(values)) {
    return []
  }
  const seen = new Set<string>()
  const normalized: ProxyNode[] = []
  values.forEach((value, index) => {
    const nextNode = normalizeProxyNode(value, index)
    if (!nextNode || seen.has(nextNode.id)) {
      return
    }
    seen.add(nextNode.id)
    normalized.push(nextNode)
  })
  return normalized
}

export function normalizeAIGlobalSettings(settings: unknown): AIGlobalSettings {
  const s = (settings ?? {}) as Record<string, unknown>
  const settingsWithoutMessageActionBarAtBottom = { ...s }
  delete settingsWithoutMessageActionBarAtBottom.messageActionBarAtBottom
  const alwaysAllowReadOnly = Boolean(s.alwaysAllowReadOnly)
  const alwaysAllowWrite = Boolean(s.alwaysAllowWrite)
  const alwaysAllowExecute = Boolean(s.alwaysAllowExecute)
  const rawExecuteApprovalMode = typeof s.executeApprovalMode === 'string' ? s.executeApprovalMode.trim() : ''
  const executeApprovalMode: ExecuteApprovalMode = rawExecuteApprovalMode === 'read_only'
    ? 'read_only'
    : (rawExecuteApprovalMode === 'all'
      ? 'all'
      : 'basic')
  const allowedCommands = normalizeStringList(s.allowedCommands)
  const deniedCommands = normalizeStringList(s.deniedCommands)
  const slashCommands = normalizeAISlashCommands(s.slashCommands)
  const collaborationPromptPresets = normalizeAICollaborationPromptPresets(s.collaborationPromptPresets)
  const systemPromptPresets = normalizeAISystemPromptPresets(s.systemPromptPresets)
  const proxyNodes = normalizeProxyNodes(s.proxyNodes)
  const rawAIRequestProxyId = typeof s.aiRequestProxyId === 'string' ? s.aiRequestProxyId.trim() : ''
  const aiRequestProxyId = proxyNodes.some((node) => node.id === rawAIRequestProxyId) ? rawAIRequestProxyId : ''
  const updatedAt = Number.isFinite(Number(s.updatedAt)) && Number(s.updatedAt) > 0 ? Number(s.updatedAt) : Date.now()
  const soundEnabled = s.soundEnabled !== false
  const soundVolume = normalizeSoundVolume(s.soundVolume)
  const toolResultTokenThreshold = normalizeToolResultTokenThreshold(s.toolResultTokenThreshold)
  const conversationAutoBackupRetentionCount = normalizeConversationAutoBackupRetentionCount(s.conversationAutoBackupRetentionCount)
  const conversationAutoBackupEnabled = Object.prototype.hasOwnProperty.call(s, 'conversationAutoBackupEnabled')
    ? s.conversationAutoBackupEnabled === true
    : DEFAULT_AI_GLOBAL_SETTINGS.conversationAutoBackupEnabled

  return {
    ...DEFAULT_AI_GLOBAL_SETTINGS,
    ...settingsWithoutMessageActionBarAtBottom,
    currentProviderId: typeof s.currentProviderId === 'string' ? s.currentProviderId.trim() : '',
    autoApprovalEnabled: alwaysAllowReadOnly || alwaysAllowWrite || alwaysAllowExecute,
    alwaysAllowReadOnly,
    alwaysAllowReadOnlyOutsideWorkspace: Boolean(s.alwaysAllowReadOnlyOutsideWorkspace),
    alwaysAllowWrite,
    alwaysAllowWriteOutsideWorkspace: Boolean(s.alwaysAllowWriteOutsideWorkspace),
    alwaysAllowWriteProtected: Boolean(s.alwaysAllowWriteProtected),
    alwaysAllowExecute,
    executeApprovalMode,
    allowedCommands,
    deniedCommands,
    slashCommands,
    collaborationPromptPresets,
    systemPromptPresets,
    collaborationExtraPrompt: typeof s.collaborationExtraPrompt === 'string' ? s.collaborationExtraPrompt.replace(/\r\n/g, '\n').trim() : '',
    alwaysAllowMcp: Boolean(s.alwaysAllowMcp),
    alwaysAllowModeSwitch: Boolean(s.alwaysAllowModeSwitch),
    alwaysAllowSubtasks: Boolean(s.alwaysAllowSubtasks),
    alwaysAllowFollowupQuestions: Boolean(s.alwaysAllowFollowupQuestions),
    soundEnabled,
    soundVolume,
    toolResultTokenThreshold,
    mcpEnabled: s.mcpEnabled !== false,
    mcpAllowBrowserCalls: Boolean(s.mcpAllowBrowserCalls),
    mcpRequireApproval: Boolean(s.mcpRequireApproval),
    mcpActivityVisible: Boolean(s.mcpActivityVisible),
    mcpTerminalFollowLatest: s.mcpTerminalFollowLatest !== false,
    terminalIsolation: s.terminalIsolation !== false,
    confirmDelete: s.confirmDelete !== false,
    continueAfterToolRejection: s.continueAfterToolRejection !== false,
    conversationAutoBackupEnabled,
    conversationAutoBackupRetentionCount,
    messageNavEnabled: s.messageNavEnabled !== false,
    aiWorkspaceTabNumbersOnly: Boolean(s.aiWorkspaceTabNumbersOnly),
    autoCondenseEnabled: Boolean(s.autoCondenseEnabled),
    autoCondenseThresholdRatio: normalizeAICondenseThresholdRatio(s.autoCondenseThresholdRatio),
    aiDebugLogEnabled: s.aiDebugLogEnabled !== false,
    approvalButtonOrder: normalizeApprovalButtonOrder(s.approvalButtonOrder),
    commandActionButtonOrder: normalizeCommandActionButtonOrder(s.commandActionButtonOrder),
    aiRequestProxyId,
    updatedAt,
    proxyNodes,
  }
}

export async function getAIGlobalSettings(): Promise<AIGlobalSettings> {
  const bridge = getAppBridge()
  if (!bridge?.GetAIGlobalSettings) {
    cachedAIGlobalSettings = DEFAULT_AI_GLOBAL_SETTINGS
    return cachedAIGlobalSettings
  }
  try {
    const [settings, proxyNodes] = await Promise.all([bridge.GetAIGlobalSettings(), getProxyNodes()])
    cachedAIGlobalSettings = normalizeAIGlobalSettings({ ...settings, proxyNodes })
    return cachedAIGlobalSettings
  } catch {
    cachedAIGlobalSettings = DEFAULT_AI_GLOBAL_SETTINGS
    return cachedAIGlobalSettings
  }
}

export async function saveAIGlobalSettings(settings: unknown): Promise<AIGlobalSettings> {
  const normalizedSettings = {
    ...normalizeAIGlobalSettings(settings),
    updatedAt: Date.now(),
  }
  const settingsToSave = { ...normalizedSettings } as Omit<AIGlobalSettings, 'proxyNodes'> & { proxyNodes?: ProxyNode[] }
  delete settingsToSave.proxyNodes
  const bridge = getAppBridge()
  cachedAIGlobalSettings = normalizedSettings
  if (!bridge?.SaveAIGlobalSettings) {
    return normalizedSettings
  }
  await bridge.SaveAIGlobalSettings(JSON.stringify(settingsToSave))
  return normalizedSettings
}
