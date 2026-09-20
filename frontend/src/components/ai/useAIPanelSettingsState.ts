import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type * as React from 'react'
import * as AppGo from '../../../wailsjs/go/wailsapp/App.js'
import { t as translate, type I18nKey } from '../../i18n.ts'
import { getAIGlobalSettings, saveAIGlobalSettings, normalizeAIGlobalSettings, type AIGlobalSettings } from './aiGlobalSettingsBridge.ts'
import { getMCPSettingsState, saveMCPGlobalServer, saveMCPEmbeddedFirecrawlAPIKey, reloadMCPGlobalServers, deleteMCPGlobalServer, restartMCPClientServer, toggleMCPClientServer, toggleMCPClientServerDisabledForPrompts, toggleMCPClientServerToolDisabledForPrompts, updateMCPClientServerTimeout, type MCPServerRuntime } from './mcpClientBridge.ts'
import { clearThemeToolPreviewPackage, setThemeToolPreviewPackage } from '../../utils/theme.ts'
import type { AIConversationSnapshot, McpInfoState } from './aiChatLogic.ts'

type LooseT = (key: I18nKey, vars?: Record<string, unknown>) => string

// 设置/MCP 状态簇：MCP 服务信息与输出压缩设置、全局 AI 设置、设置浮层 UI 态、
// 恶魔/临时会话开关、主题工具预览、提示音播放与删除确认。
// 从 AIConversationTabPanel 原样搬移，闭包依赖经参数同名注入，代码零改动。
export function useAIPanelSettingsState({ t, isWorkspaceTabActive, panelMountedRef, activeConversation, resetGlobalSearchState, resetConversationSearchState }: {
  t: LooseT
  isWorkspaceTabActive: boolean
  panelMountedRef: React.RefObject<boolean>
  activeConversation: AIConversationSnapshot | null
  resetGlobalSearchState: () => void
  resetConversationSearchState: () => void
}) {
  const audioPlayersRef = useRef<Map<string, HTMLAudioElement>>(new Map())
  const [mcpInfo, setMcpInfo] = useState<McpInfoState>({ url: '', transport: 'streamable-http', endpoint: '/mcp', instructions: '', logs: '', tools: [] })
  const [mcpClientServers, setMCPClientServers] = useState<unknown[]>([])
  const [mcpClientGlobalConfigPath, setMCPClientGlobalConfigPath] = useState('')
  const [mcpClientGlobalConfigText, setMCPClientGlobalConfigText] = useState('{\n  "mcpServers": {}\n}')
  const [mcpEmbeddedFirecrawlApiKey, setMCPEmbeddedFirecrawlApiKey] = useState('')
  const [showSettingsPanel, setShowSettingsPanel] = useState(false)
  const [popupDismissVersion, setPopupDismissVersion] = useState(0)
  const [activeSettingsTab, setActiveSettingsTab] = useState('')
  const [tasksDirMigrating, setTasksDirMigrating] = useState(false)
  const [temporarySessionEnabled, setTemporarySessionEnabled] = useState(false)
  const [themeToolPreview, setThemeToolPreview] = useState<unknown>(null)
  const [globalAISettings, setGlobalAISettings] = useState<AIGlobalSettings | null>(null)
  const [terminalOutputLineLimit, setTerminalOutputLineLimit] = useState(500)
  const [terminalOutputCharacterLimit, setTerminalOutputCharacterLimit] = useState(35000)
  const [providerBalanceRefreshSignal, setProviderBalanceRefreshSignal] = useState(0)
  const applyMCPInfo = useCallback((info: unknown) => {
    const rawInfo = info && typeof info === 'object' ? info as Record<string, unknown> : null
    if (!panelMountedRef.current || !rawInfo) {
      return
    }
    setMcpInfo({
      url: typeof rawInfo.url === 'string' ? rawInfo.url : '',
      transport: typeof rawInfo.transport === 'string' ? rawInfo.transport : 'streamable-http',
      endpoint: typeof rawInfo.endpoint === 'string' ? rawInfo.endpoint : '/mcp',
      instructions: typeof rawInfo.instructions === 'string' ? rawInfo.instructions : '',
      logs: typeof rawInfo.logs === 'string' ? rawInfo.logs : '',
      tools: Array.isArray(rawInfo.tools) ? rawInfo.tools : [],
    })
  }, [])
  const applyMCPSettingsState = useCallback((state: unknown) => {
    const rawState = state && typeof state === 'object' ? state as Record<string, unknown> : null
    if (!panelMountedRef.current || !rawState) {
      return
    }
    applyMCPInfo(rawState.service || {})
    const rawClient = rawState.client && typeof rawState.client === 'object' ? rawState.client as Record<string, unknown> : null
    setMCPClientServers(Array.isArray(rawClient?.servers) ? rawClient.servers : [])
    setMCPClientGlobalConfigPath(typeof rawClient?.globalConfigPath === 'string' ? rawClient.globalConfigPath : '')
    setMCPClientGlobalConfigText(typeof rawClient?.globalConfigText === 'string' && rawClient.globalConfigText.trim() ? rawClient.globalConfigText : '{\n  "mcpServers": {}\n}')
    setMCPEmbeddedFirecrawlApiKey(typeof rawClient?.embeddedFirecrawlApiKey === 'string' ? rawClient.embeddedFirecrawlApiKey : '')
  }, [applyMCPInfo])
  const refreshMCPServerInfo = useCallback(async () => {
    try {
      const state = await getMCPSettingsState()
      applyMCPSettingsState(state)
      return state
    } catch {
      return null
    }
  }, [applyMCPSettingsState])
  // 差异更新：只替换列表中被操作的那一台服务器，避免整表全量 refetch 与重渲染。
  const patchMCPClientServer = useCallback((updated: MCPServerRuntime | null | undefined) => {
    if (!panelMountedRef.current || !updated || !updated.name) {
      return
    }
    setMCPClientServers((previous) => Array.isArray(previous)
      ? previous.map((item) => {
          const server = item as MCPServerRuntime
          return server && server.name === updated.name && server.source === updated.source ? updated : item
        })
      : previous)
  }, [])
  const refreshMCPOutputCompressionSettings = useCallback(async () => {
    try {
      const settings = await AppGo.GetMCPOutputCompressionSettings()
      if (!panelMountedRef.current || !settings) {
        return null
      }
      const nextLineLimit = Math.max(10, Math.min(5000, settings.terminalOutputLineLimit || 0))
      const nextCharacterLimit = Math.max(1000, Math.min(500000, settings.terminalOutputCharacterLimit || 0))
      setTerminalOutputLineLimit(nextLineLimit)
      setTerminalOutputCharacterLimit(nextCharacterLimit)
      return settings
    } catch {
      return null
    }
  }, [])
  const showAlert = useCallback(async (message: string) => {
    // message 为动态内容（可能不在翻译表），t() 内部有兜底
    const finalMessage = typeof message === 'string' && message.trim() ? translate(message.trim() as I18nKey) : translate('当前状态不支持还原')
    if (window?.lumeDialog?.alert) {
      await window.lumeDialog.alert(finalMessage, t('提示'))
      return
    }
    window.alert(finalMessage)
  }, [t])
  useEffect(() => {
    if (!isWorkspaceTabActive) {
      return
    }
    if (themeToolPreview) {
      setThemeToolPreviewPackage(themeToolPreview)
    } else {
      clearThemeToolPreviewPackage()
    }
    return () => {
      clearThemeToolPreviewPackage()
    }
  }, [isWorkspaceTabActive, themeToolPreview])
  useEffect(() => {
    if (isWorkspaceTabActive) {
      return
    }
    setShowSettingsPanel(false)
    setPopupDismissVersion((current) => current + 1)
    resetGlobalSearchState()
    resetConversationSearchState()
  }, [isWorkspaceTabActive, resetConversationSearchState, resetGlobalSearchState])
  const normalizedGlobalAISettings = useMemo(() => normalizeAIGlobalSettings(globalAISettings), [globalAISettings])
  const playAISound = useCallback((type: string) => {
    if (normalizedGlobalAISettings.soundEnabled === false) {
      return
    }
    const parsedVolume = Number(normalizedGlobalAISettings.soundVolume)
    const volume = Number.isFinite(parsedVolume) ? Math.max(0, Math.min(1, parsedVolume)) : 0.06
    if (volume <= 0) {
      return
    }
    const soundKey = typeof type === 'string' ? type.trim() : ''
    const audioPathByType: Record<string, string> = {
      completion: '/audio/celebration.wav',
      notification: '/audio/notification.wav',
      progress: '/audio/progress_loop.wav',
    }
    const audioPath = audioPathByType[soundKey]
    if (!audioPath) {
      return
    }
    try {
      let audio = audioPlayersRef.current.get(soundKey)
      if (!(audio instanceof Audio)) {
        audio = new Audio(audioPath)
        audio.preload = 'auto'
        audioPlayersRef.current.set(soundKey, audio)
      }
      audio.pause()
      audio.currentTime = 0
      audio.volume = volume
      void audio.play().catch(() => {})
    } catch {}
  }, [normalizedGlobalAISettings.soundEnabled, normalizedGlobalAISettings.soundVolume])
  useEffect(() => {
    if (!activeConversation && activeSettingsTab === 'backup') {
      setActiveSettingsTab('')
    }
  }, [activeConversation, activeSettingsTab])
  // 代理节点变更时刷新 AI 设置中的代理列表
  useEffect(() => {
    const handler = (event: Event) => {
      const newProxyNodes = (event as CustomEvent).detail
      if (!Array.isArray(newProxyNodes)) return
      setGlobalAISettings((prev) => prev ? { ...prev, proxyNodes: newProxyNodes } : prev)
    }
    window.addEventListener('lumin:proxy-nodes-changed', handler)
    return () => window.removeEventListener('lumin:proxy-nodes-changed', handler)
  }, [])
  useEffect(() => () => {
    audioPlayersRef.current.forEach((audio) => {
      try {
        audio.pause()
        audio.src = ''
      } catch {}
    })
    audioPlayersRef.current.clear()
  }, [])
  useEffect(() => {
    if (!showSettingsPanel) {
      return
    }
    getAIGlobalSettings()
      .then((settings) => {
        setGlobalAISettings(settings)
      })
      .catch(() => {})
  }, [showSettingsPanel])
  useEffect(() => {
    const handleGlobalAISettingsChanged = (event: Event) => {
      const detail = (event as CustomEvent).detail
      if (!detail || typeof detail !== 'object') {
        return
      }
      setGlobalAISettings(normalizeAIGlobalSettings(detail))
    }
    window.addEventListener('ai-global-settings-changed', handleGlobalAISettingsChanged)
    return () => window.removeEventListener('ai-global-settings-changed', handleGlobalAISettingsChanged)
  }, [])
  const handleSaveAIPanelGlobalSettings = useCallback(async (patch: Record<string, unknown>) => {
    const nextSettings = await saveAIGlobalSettings({
      ...normalizedGlobalAISettings,
      ...patch,
    })
    setGlobalAISettings(nextSettings)
    window.dispatchEvent(new CustomEvent('ai-global-settings-changed', { detail: nextSettings }))
    await refreshMCPServerInfo()
    return nextSettings
  }, [normalizedGlobalAISettings, refreshMCPServerInfo])
  const handleSaveMCPGlobalServer = useCallback(async (name: string, configText: string) => {
    await saveMCPGlobalServer(name, configText)
    await refreshMCPServerInfo()
  }, [refreshMCPServerInfo])
  // 内置 Firecrawl API Key: 失焦保存, 后端仅在 Key 变化时重启该 MCP
  const handleSaveMCPEmbeddedFirecrawlApiKey = useCallback(async (apiKey: string) => {
    await saveMCPEmbeddedFirecrawlAPIKey(apiKey)
    await refreshMCPServerInfo()
  }, [refreshMCPServerInfo])
  const handleReloadMCPGlobalServers = useCallback(async () => {
    await reloadMCPGlobalServers()
    await refreshMCPServerInfo()
  }, [refreshMCPServerInfo])
  const handleDeleteMCPGlobalServer = useCallback(async (name: string) => {
    await deleteMCPGlobalServer(name)
    await refreshMCPServerInfo()
  }, [refreshMCPServerInfo])
  const handleRestartMCPClientServer = useCallback(async (name: string, source: string) => {
    await restartMCPClientServer(name, source)
    await refreshMCPServerInfo()
  }, [refreshMCPServerInfo])
  const handleToggleMCPClientServer = useCallback(async (name: string, source: string, disabled: boolean) => {
    const runtime = await toggleMCPClientServer(name, source, disabled)
    patchMCPClientServer(runtime)
  }, [patchMCPClientServer])
  const handleToggleMCPClientServerDisabledForPrompts = useCallback(async (name: string, source: string, disabledForPrompts: boolean) => {
    const runtime = await toggleMCPClientServerDisabledForPrompts(name, source, disabledForPrompts)
    patchMCPClientServer(runtime)
  }, [patchMCPClientServer])
  const handleToggleMCPClientServerToolDisabledForPrompts = useCallback(async (name: string, source: string, toolName: string, disabledForPrompts: boolean) => {
    const runtime = await toggleMCPClientServerToolDisabledForPrompts(name, source, toolName, disabledForPrompts)
    patchMCPClientServer(runtime)
  }, [patchMCPClientServer])
  const handleUpdateMCPClientServerTimeout = useCallback(async (name: string, source: string, timeout: number) => {
    const runtime = await updateMCPClientServerTimeout(name, source, timeout)
    patchMCPClientServer(runtime)
  }, [patchMCPClientServer])

  const saveMCPOutputCompressionSettings = useCallback(async (lineLimit: number, characterLimit: number) => {
    const nextLineLimit = Math.max(10, Math.min(5000, lineLimit || 0))
    const nextCharacterLimit = Math.max(1000, Math.min(500000, characterLimit || 0))
    setTerminalOutputLineLimit(nextLineLimit)
    setTerminalOutputCharacterLimit(nextCharacterLimit)
    await AppGo.SaveMCPOutputCompressionSettings(nextLineLimit, nextCharacterLimit)
  }, [])

  async function requestDeleteConfirmation(message: string) {
    if (!normalizedGlobalAISettings.confirmDelete) {
      return true
    }
    const confirm = window?.lumeDialog?.confirm
    if (typeof confirm !== 'function') {
      return true
    }
    const result = await confirm(message, t('操作确认'))
    return result === true || (typeof result === 'object' && result !== null && result.confirmed === true)
  }

  const handleToggleAiTerminalIsolation = useCallback(async () => {
    await handleSaveAIPanelGlobalSettings({
      terminalIsolation: !normalizedGlobalAISettings.terminalIsolation,
    })
  }, [handleSaveAIPanelGlobalSettings, normalizedGlobalAISettings.terminalIsolation])

  const handleToggleConfirmDelete = useCallback(async () => {
    await handleSaveAIPanelGlobalSettings({
      confirmDelete: !normalizedGlobalAISettings.confirmDelete,
    })
  }, [handleSaveAIPanelGlobalSettings, normalizedGlobalAISettings.confirmDelete])

  const handleToggleSettingsPanel = useCallback(() => {
    setShowSettingsPanel((previous) => {
      const next = !previous
      if (next) {
        setActiveSettingsTab('')
      }
      return next
    })
  }, [])

  const handleTerminalOutputLineLimitChange = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    const value = parseInt(event.target.value, 10) || 0
    saveMCPOutputCompressionSettings(value, terminalOutputCharacterLimit).catch(() => {})
  }, [saveMCPOutputCompressionSettings, terminalOutputCharacterLimit])

  const handleTerminalOutputCharacterLimitChange = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    const value = parseInt(event.target.value, 10) || 0
    saveMCPOutputCompressionSettings(terminalOutputLineLimit, value).catch(() => {})
  }, [saveMCPOutputCompressionSettings, terminalOutputLineLimit])
  return {
    mcpInfo,
    setMcpInfo,
    mcpClientServers,
    mcpClientGlobalConfigPath,
    mcpClientGlobalConfigText,
    mcpEmbeddedFirecrawlApiKey,
    showSettingsPanel,
    setShowSettingsPanel,
    popupDismissVersion,
    setPopupDismissVersion,
    activeSettingsTab,
    setActiveSettingsTab,
    tasksDirMigrating,
    setTasksDirMigrating,
    temporarySessionEnabled,
    setTemporarySessionEnabled,
    themeToolPreview,
    setThemeToolPreview,
    globalAISettings,
    setGlobalAISettings,
    terminalOutputLineLimit,
    terminalOutputCharacterLimit,
    providerBalanceRefreshSignal,
    setProviderBalanceRefreshSignal,
    applyMCPInfo,
    applyMCPSettingsState,
    refreshMCPServerInfo,
    refreshMCPOutputCompressionSettings,
    showAlert,
    playAISound,
    requestDeleteConfirmation,
    normalizedGlobalAISettings,
    handleSaveAIPanelGlobalSettings,
    handleSaveMCPGlobalServer,
    handleSaveMCPEmbeddedFirecrawlApiKey,
    handleReloadMCPGlobalServers,
    handleDeleteMCPGlobalServer,
    handleRestartMCPClientServer,
    handleToggleMCPClientServer,
    handleToggleMCPClientServerDisabledForPrompts,
    handleToggleMCPClientServerToolDisabledForPrompts,
    handleUpdateMCPClientServerTimeout,
    saveMCPOutputCompressionSettings,
    handleToggleAiTerminalIsolation,
    handleToggleConfirmDelete,
    handleToggleSettingsPanel,
    handleTerminalOutputLineLimitChange,
    handleTerminalOutputCharacterLimitChange,
  }
}
