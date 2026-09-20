import { useCallback, useEffect, useMemo, useState } from 'react'
import type * as React from 'react'
import { getAIProviderState, type AIProviderState } from './aiProviderBridge.ts'
import type { AIProviderLike } from './AIProviderSelector.tsx'
import { AI_CONVERSATION_DIFF_SUCCESS_STATUSES, AI_CONVERSATION_DIFF_TOOL_NAMES, buildAIRequestModelMeta, computeAILastAssistantTurnState, createEmptyPanelState, dropAssistantTurnMessages, extractAIConversationDiffPrimaryPath, normalizeAIMessageStatus, pruneOrphanTurnMessages } from './aiChatLogic.ts'
import type { AIConversationSnapshot, AIMessage, AIPanelProps, ComposerEditState, PanelState, TokenLedger } from './aiChatLogic.ts'
import { cancelAIChat } from './aiChatBridge.ts'
import { deleteAIConversation, deleteTemporaryAIConversation, getAIConversation, getTemporaryAIConversation, listAIConversations, listTemporaryAIConversations as listTemporaryAIConversationsFromDisk, normalizeAIConversationTaskSettings, openAIConversationFolder, saveAIConversation, saveTemporaryAIConversation, subscribeAIConversationChanges, type AIConversationMessageSearchResult } from './aiConversationBridge.ts'
import { getTemporaryAIConversationSummary, listTemporaryAIConversations as listInMemoryTemporaryAIConversations, removeTemporaryAIConversation, seedTemporaryAIConversations, upsertTemporaryAIConversation, TEMPORARY_AI_CONVERSATIONS_CHANGED_EVENT } from './aiTemporaryConversations.ts'
import { upsertConversationSummary, type ConversationSummary } from './aiConversationSummary.ts'
import { getAIGlobalSettings, saveAIGlobalSettings, type AIGlobalSettings } from './aiGlobalSettingsBridge.ts'
import { t as translate, type I18nKey } from '../../i18n.ts'

type LooseT = (key: I18nKey, vars?: Record<string, unknown>) => string

// 会话首页/导航状态簇：会话列表与供应商状态、首页数据刷新、终端标签映射、
// 会话打开/回首页/恢复备份/重命名/删除、供应商切换与 diff 汇总入口。
// 从 AIConversationTabPanel 原样搬移，闭包依赖经参数同名注入，代码零改动。
export function useAIConversationHome({ t, addToast, terminalId, sessionId, workspaceTabId, initialConversationId, isWorkspaceTabActive, sessionTerminals, onGoHomeRequested, onOpenConversationRequested, panelInstanceKey, panelState, activeConversation, pendingConversationId, setPendingConversationId, setPanelState, setComposerEditState, terminalPanelsRef, deletedConversationIdsRef, isReturningHomeRef, conversationLoadRequestRef, panelMountedRef, tokenLedgerRef, rebuildAIConversationTokenLedger, saveConversationSnapshot, clearRestorePreview, resetComposerEditState, setThemeToolPreview, setShowSettingsPanel, setPopupDismissVersion, showAlert, refreshMCPServerInfo, refreshMCPOutputCompressionSettings, globalAISettings, setGlobalAISettings, conversationList: _conversationList, setConversationList, resetGlobalSearchState, resetConversationSearchState, locateConversationMessage, requestDeleteConfirmation }: {
  t: LooseT
  addToast?: AIPanelProps['addToast']
  terminalId: string
  sessionId: string
  workspaceTabId: string
  initialConversationId: string
  isWorkspaceTabActive: boolean
  sessionTerminals: Array<{ id: string; label?: string }>
  onGoHomeRequested?: () => void
  onOpenConversationRequested?: (conversationId: string, messageId?: string) => void | Promise<void>
  panelInstanceKey: string
  panelState: PanelState
  pendingConversationId: string
  setPendingConversationId: React.Dispatch<React.SetStateAction<string>>
  activeConversation: AIConversationSnapshot | null
  setPanelState: (panelKey: string, updater: ((current: PanelState) => PanelState) | Partial<PanelState>) => PanelState
  setComposerEditState: React.Dispatch<React.SetStateAction<ComposerEditState>>
  terminalPanelsRef: React.RefObject<Record<string, PanelState>>
  deletedConversationIdsRef: React.RefObject<Set<string>>
  isReturningHomeRef: React.RefObject<boolean>
  conversationLoadRequestRef: React.RefObject<number>
  panelMountedRef: React.RefObject<boolean>
  tokenLedgerRef: React.RefObject<Map<string, TokenLedger>>
  rebuildAIConversationTokenLedger: (snapshot: AIConversationSnapshot, targetPanelKey?: string) => Promise<number>
  saveConversationSnapshot: (snapshot: AIConversationSnapshot, targetPanelKey?: string, options?: { hydrate?: boolean }) => Promise<AIConversationSnapshot | undefined>
  clearRestorePreview: () => void
  resetComposerEditState: () => void
  setThemeToolPreview: React.Dispatch<React.SetStateAction<unknown>>
  setShowSettingsPanel: React.Dispatch<React.SetStateAction<boolean>>
  setPopupDismissVersion: React.Dispatch<React.SetStateAction<number>>
  showAlert: (message: string) => Promise<void>
  refreshMCPServerInfo: () => Promise<unknown>
  refreshMCPOutputCompressionSettings: () => Promise<unknown>
  globalAISettings: AIGlobalSettings | null
  setGlobalAISettings: React.Dispatch<React.SetStateAction<AIGlobalSettings | null>>
  conversationList: ConversationSummary[]
  setConversationList: React.Dispatch<React.SetStateAction<ConversationSummary[]>>
  resetGlobalSearchState: () => void
  resetConversationSearchState: () => void
  locateConversationMessage: (messageId: string) => void
  requestDeleteConfirmation: (message: string) => Promise<boolean>
}) {
  const [aiProviderState, setAIProviderState] = useState<AIProviderState>({ currentProviderId: '', providers: [] })
  const refreshAIHomeData = useCallback(async () => {
    void getAIGlobalSettings()
      .then((value) => {
        if (!panelMountedRef.current) {
          return
        }
        setGlobalAISettings(value)
      })
      .catch(() => {
        if (!panelMountedRef.current) {
          return
        }
        setGlobalAISettings(null)
      })
    void getAIProviderState()
      .then((value) => {
        if (!panelMountedRef.current) {
          return
        }
        setAIProviderState(value)
      })
      .catch(() => {
        if (!panelMountedRef.current) {
          return
        }
        setAIProviderState({ currentProviderId: '', providers: [] })
      })
    void refreshMCPServerInfo()
    void refreshMCPOutputCompressionSettings()
    try {
      const conversations = await listAIConversations()
      if (!panelMountedRef.current) {
        return
      }
      const temporarySummaries = await listTemporaryAIConversationsFromDisk().catch(() => [])
      seedTemporaryAIConversations(temporarySummaries)
      setConversationList([...temporarySummaries.map((summary) => ({ ...summary, transient: true })), ...(Array.isArray(conversations) ? conversations : [])])
    } catch {
      if (!panelMountedRef.current) {
        return
      }
      const temporarySummaries = await listTemporaryAIConversationsFromDisk().catch(() => [])
      seedTemporaryAIConversations(temporarySummaries)
      setConversationList(temporarySummaries.map((summary) => ({ ...summary, transient: true })))
    }
  }, [refreshMCPOutputCompressionSettings, refreshMCPServerInfo])
  useEffect(() => {
    const syncTemporaryConversations = () => {
      setConversationList((current) => [...listInMemoryTemporaryAIConversations(), ...current.filter((item) => item.transient !== true)])
    }
    window.addEventListener(TEMPORARY_AI_CONVERSATIONS_CHANGED_EVENT, syncTemporaryConversations)
    return () => window.removeEventListener(TEMPORARY_AI_CONVERSATIONS_CHANGED_EVENT, syncTemporaryConversations)
  }, [])
  const terminalLabelMap = useMemo(() => {
    const map = new Map()
    ;(Array.isArray(sessionTerminals) ? sessionTerminals : []).forEach((terminal) => {
      const nextTerminalId = typeof terminal?.id === 'string' ? terminal.id.trim() : ''
      if (!nextTerminalId) {
        return
      }
      const nextLabel = typeof terminal?.label === 'string' && terminal.label.trim() ? terminal.label.trim() : nextTerminalId
      map.set(nextTerminalId, nextLabel)
    })
    return map
  }, [sessionTerminals])
  const enrichAIChatCommandMessage = useCallback((message: AIMessage) => {
    if (!message || typeof message !== 'object' || message.kind !== 'command') {
      return message
    }
    const nextExtra = message.extra && typeof message.extra === 'object' ? { ...message.extra } : {}
    const targetSessionId = typeof nextExtra.targetSessionId === 'string' && nextExtra.targetSessionId.trim()
      ? nextExtra.targetSessionId.trim()
      : ''
    if (targetSessionId) {
      nextExtra.targetLabel = terminalLabelMap.get(targetSessionId) || targetSessionId
    }
    return Object.keys(nextExtra).length > 0
      ? { ...message, extra: nextExtra }
      : message
  }, [terminalLabelMap])
  const selectedAIProvider = useMemo(() => {
    const currentProviderId = typeof aiProviderState?.currentProviderId === 'string' ? aiProviderState.currentProviderId.trim() : ''
    if (!currentProviderId) {
      return null
    }
    return (Array.isArray(aiProviderState?.providers) ? aiProviderState.providers : []).find((item) => item?.id === currentProviderId) || null
  }, [aiProviderState])
  const availableAIProviders = useMemo(
    () => (Array.isArray(aiProviderState?.providers) ? aiProviderState.providers : []),
    [aiProviderState],
  )
  const resolveFirstAvailableProviderId = useCallback((providers: AIProviderLike[] = []) => {
    return typeof providers[0]?.id === 'string' ? providers[0].id.trim() : ''
  }, [])
  const resolveAvailableProviderId = useCallback((providers: AIProviderLike[] = [], preferredProviderId = '') => {
    const normalizedPreferredProviderId = typeof preferredProviderId === 'string' ? preferredProviderId.trim() : ''
    if (normalizedPreferredProviderId && providers.some((item) => item?.id === normalizedPreferredProviderId)) {
      return normalizedPreferredProviderId
    }
    return resolveFirstAvailableProviderId(providers)
  }, [resolveFirstAvailableProviderId])
  const buildConversationWithProviderId = useCallback((snapshot: AIConversationSnapshot, providerId: string) => {
    if (!snapshot || typeof snapshot !== 'object') {
      return snapshot
    }
    const normalizedProviderId = typeof providerId === 'string' ? providerId.trim() : ''
    const rawSettings = snapshot.settings && typeof snapshot.settings === 'object' ? snapshot.settings as Record<string, unknown> : null
    const currentProviderId = typeof rawSettings?.currentProviderId === 'string' ? rawSettings.currentProviderId.trim() : ''
    if (currentProviderId === normalizedProviderId) {
      return snapshot
    }
    return {
      ...snapshot,
      updatedAt: Date.now(),
      settings: normalizeAIConversationTaskSettings({
        ...(rawSettings || {}),
        currentProviderId: normalizedProviderId,
      }),
    }
  }, [])
  const effectiveProviderId = selectedAIProvider?.id || resolveAvailableProviderId(
    availableAIProviders,
    typeof aiProviderState?.currentProviderId === 'string' ? aiProviderState.currentProviderId.trim() : '',
  )
  const resolveAIRequestModelMeta = useCallback((providerId = '', providers: AIProviderLike[] | null = null) => {
    const normalizedProviderId = typeof providerId === 'string' ? providerId.trim() : ''
    const sourceProviders = Array.isArray(providers) ? providers : (Array.isArray(aiProviderState?.providers) ? aiProviderState.providers : [])
    const matchedProvider = normalizedProviderId
      ? sourceProviders.find((item) => item?.id === normalizedProviderId) || null
      : null
    return buildAIRequestModelMeta(matchedProvider)
  }, [aiProviderState])
  useEffect(() => {
    void refreshAIHomeData()
  }, [refreshAIHomeData])
  useEffect(() => subscribeAIConversationChanges((change: unknown) => {
    const rawChange = change && typeof change === 'object' ? change as Record<string, unknown> : null
    if (!rawChange) {
      return
    }
    const summary = rawChange.summary as AIConversationSnapshot | null | undefined
    if (rawChange.type === 'upsert' && summary?.id) {
      deletedConversationIdsRef.current.delete(summary.id)
      setConversationList((current) => upsertConversationSummary(current, summary))
      setPanelState(panelInstanceKey, (current) => (
        current.activeConversationId === summary.id && current.conversation
          ? {
              ...current,
              conversation: {
                ...current.conversation,
                ...summary,
                messages: current.messages,
                apiMessages: current.apiMessages,
              },
            }
          : current
      ))
      return
    }
    const conversationId = typeof rawChange.conversationId === 'string' ? rawChange.conversationId.trim() : ''
    if (rawChange.type !== 'delete' || !conversationId) {
      return
    }
    deletedConversationIdsRef.current.add(conversationId)
    setConversationList((current) => current.filter((item) => item.id !== conversationId))
    const panel = terminalPanelsRef.current[panelInstanceKey]
    if (panel?.activeConversationId !== conversationId) {
      return
    }
    const requestId = panel.activeRequestId
    setPanelState(panelInstanceKey, createEmptyPanelState())
    setThemeToolPreview(null)
    clearRestorePreview()
    resetComposerEditState()
    resetGlobalSearchState()
    resetConversationSearchState()
    if (requestId) {
      void cancelAIChat(requestId)
    }
  }), [clearRestorePreview, panelInstanceKey, resetComposerEditState, resetConversationSearchState, resetGlobalSearchState, setPanelState])
  const conversationDiffItems = useMemo(() => {
    const sourceMessages = Array.isArray(panelState.messages) ? panelState.messages : []
    const collected = sourceMessages.flatMap((message, index) => {
      if (!message || typeof message !== 'object' || message.kind !== 'tool') {
        return []
      }
      const toolName = typeof message.actionLabel === 'string' ? message.actionLabel.trim() : ''
      const status = normalizeAIMessageStatus(message.status)
      const artifactPath = typeof message?.extra?.restoreArtifactPath === 'string' ? message.extra.restoreArtifactPath.trim() : ''
      const hasPreview = message?.extra?.conversationDiffHasPreview === true
      if (!AI_CONVERSATION_DIFF_TOOL_NAMES.has(toolName) || !AI_CONVERSATION_DIFF_SUCCESS_STATUSES.has(status) || !artifactPath || !hasPreview) {
        return []
      }
      const copyContent = typeof message?.extra?.copyContent === 'string' ? message.extra.copyContent : ''
      const summaryText = typeof message.summary === 'string' ? message.summary.trim() : ''
      const primaryPath = typeof message?.extra?.conversationDiffPrimaryPath === 'string' ? message.extra.conversationDiffPrimaryPath.trim() : ''
      const fileCountRaw = Number(message?.extra?.conversationDiffFileCount)
      const fileCount = Number.isFinite(fileCountRaw) && fileCountRaw > 0 ? Math.trunc(fileCountRaw) : 0
      const title = primaryPath
        ? (fileCount > 1
          ? translate('{path} 等 {count} 个文件', { path: primaryPath, count: fileCount })
          : primaryPath)
        : extractAIConversationDiffPrimaryPath(copyContent, summaryText)
      return [{
        id: typeof message.id === 'string' && message.id.trim() ? message.id.trim() : `conversation-diff-${index}`,
        messageId: typeof message.id === 'string' && message.id.trim() ? message.id.trim() : '',
        artifactPath,
        toolName,
        title,
        summary: summaryText,
        status,
        copyContent,
        order: index,
      }]
    })
    return collected
      .reverse()
      .map((item, index) => ({
        ...item,
        order: index + 1,
      }))
  }, [panelState.messages])

  const handleOpenConversationDiff = useCallback(() => {
    if (typeof window === 'undefined' || conversationDiffItems.length === 0) {
      return
    }
    window.dispatchEvent(new CustomEvent('ai-conversation-diff-open', {
      detail: {
        sessionId: sessionId || terminalId || '',
        terminalId: terminalId || '',
        tabId: workspaceTabId,
        items: conversationDiffItems,
      },
    }))
  }, [conversationDiffItems, sessionId, terminalId, workspaceTabId])
  const handleGoHome = useCallback(async () => {
    conversationLoadRequestRef.current += 1
    setPendingConversationId('')
    isReturningHomeRef.current = true
    onGoHomeRequested?.()
    if (typeof window !== 'undefined') {
      if (terminalId) {
        window.dispatchEvent(new CustomEvent('ai-change-review-clear', {
          detail: { sessionId: terminalId, tabId: workspaceTabId },
        }))
      }
      window.dispatchEvent(new CustomEvent('ai-conversation-diff-close', {
        detail: {
          sessionId: sessionId || '',
          terminalId: terminalId || '',
          tabId: workspaceTabId,
        },
      }))
    }
    setThemeToolPreview(null)
    clearRestorePreview()
    setShowSettingsPanel(false)
    setPopupDismissVersion((current) => current + 1)
    resetComposerEditState()
    resetGlobalSearchState()
    resetConversationSearchState()
    const previousPanel = terminalPanelsRef.current[panelInstanceKey]
    const previousRequestId = previousPanel?.activeRequestId || ''
    const previousConversation = previousPanel?.conversation
    const persistCurrentConversation = previousConversation?.transient === true && !deletedConversationIdsRef.current.has(previousConversation.id)
      ? (() => {
          const assistantMessageId = previousPanel?.activeAssistantMessageId || previousRequestId
          const messages = dropAssistantTurnMessages(previousPanel?.messages, assistantMessageId)
          return saveTemporaryAIConversation({
            ...previousConversation,
            updatedAt: Date.now(),
            status: 'idle',
            messages,
            apiMessages: Array.isArray(previousPanel?.apiMessages) ? previousPanel.apiMessages : (Array.isArray(previousConversation.apiMessages) ? previousConversation.apiMessages : []),
          }).then((saved) => { upsertTemporaryAIConversation(saved); return saved })
        })()
      : (previousConversation && !deletedConversationIdsRef.current.has(previousConversation.id)
      ? (() => {
          const assistantMessageId = previousPanel?.activeAssistantMessageId || previousRequestId
          const messages = dropAssistantTurnMessages(previousPanel?.messages, assistantMessageId)
          return saveAIConversation({
            ...previousConversation,
            updatedAt: Date.now(),
            status: 'idle',
            messages,
            apiMessages: Array.isArray(previousPanel?.apiMessages) ? previousPanel.apiMessages : (Array.isArray(previousConversation.apiMessages) ? previousConversation.apiMessages : []),
          }).catch(() => {})
        })()
      : Promise.resolve())
    setPanelState(panelInstanceKey, (current) => ({
      ...current,
      activeConversationId: '',
      conversation: null,
      messages: [],
      apiMessages: [],
      activeRequestId: '',
      activeAssistantMessageId: '',
      activeToolExecution: null,
      toolApprovalMode: '',
      requestPhase: 'idle',
      runtimePhase: 'ready',
      queuedSubmission: null,
      isFlushingQueuedSubmission: false,
      skipNextAutomaticRequest: false,
      resumeAfterCancelRequestId: '',
      recoverableToolStopReason: '',
      contextTokens: 0,
      upstreamInputTokens: 0,
      upstreamOutputTokens: 0,
      isCondensingContext: false,
      collaborationLocked: false,
      collaborationActive: false,
      collaborationMode: '',
      collaborationStreamBuffer: '',
      collaborationAwaitingManualFollowup: false,
      collaborationFollowupRequestId: '',
    }))
    if (previousRequestId) {
      try {
        await cancelAIChat(previousRequestId)
      } catch {}
    }
    await persistCurrentConversation
    await refreshAIHomeData()
  }, [clearRestorePreview, onGoHomeRequested, panelInstanceKey, refreshAIHomeData, resetComposerEditState, sessionId, setPanelState, terminalId, workspaceTabId])
  const handleOpenConversation = useCallback(async (conversationId: string, delegateToWorkspace = true) => {
    const normalizedConversationId = typeof conversationId === 'string' ? conversationId.trim() : ''
    if (!normalizedConversationId) {
      return
    }
    const temporarySummary = getTemporaryAIConversationSummary(normalizedConversationId)
    if (!temporarySummary && delegateToWorkspace && onOpenConversationRequested) {
      await onOpenConversationRequested(conversationId)
      return
    }
    // 切换到别的会话前，先取消本面板的在途请求。否则后端请求会继续跑完（白耗 token），
    // 而它的流事件因为面板 activeRequestId 已被替换而匹配不到面板，被静默丢弃。
    // 重新打开同一个会话不算切换，不能误取消。
    const switchingPanel = terminalPanelsRef.current[panelInstanceKey]
    const switchingRequestId = switchingPanel?.activeRequestId || ''
    const switchingConversationId = switchingPanel?.activeConversationId || ''
    if (switchingRequestId && switchingConversationId !== normalizedConversationId) {
      void cancelAIChat(switchingRequestId).catch(() => {})
    }
    const requestToken = conversationLoadRequestRef.current + 1
    conversationLoadRequestRef.current = requestToken
    setPendingConversationId(normalizedConversationId)
    setThemeToolPreview(null)
    clearRestorePreview()
    resetComposerEditState()
    resetGlobalSearchState()
    resetConversationSearchState()
    try {
      const snapshot = temporarySummary ? await getTemporaryAIConversation(normalizedConversationId) : await getAIConversation(normalizedConversationId)
      if (!panelMountedRef.current || conversationLoadRequestRef.current !== requestToken) {
        return
      }
      const latestProviderState = await getAIProviderState().catch(() => ({
        currentProviderId: typeof aiProviderState?.currentProviderId === 'string' ? aiProviderState.currentProviderId.trim() : '',
        providers: availableAIProviders,
      }))
      if (!panelMountedRef.current || conversationLoadRequestRef.current !== requestToken) {
        return
      }
      const latestProviders = Array.isArray(latestProviderState?.providers) ? latestProviderState.providers : []
      const snapshotSettings = snapshot?.settings && typeof snapshot.settings === 'object' ? snapshot.settings as Record<string, unknown> : null
      const resolvedProviderId = resolveAvailableProviderId(latestProviders, typeof snapshotSettings?.currentProviderId === 'string' ? snapshotSettings.currentProviderId : '')
      // 加载时修补历史快照：早期版本在中断/回首页时只删除 assistant 与 reasoning，
      // 遗留的子卡片（工具/追问等）会在重新分组时变成无归属的残留卡片，这里一次性清掉。
      const healedSnapshot = pruneOrphanTurnMessages(snapshot)
      const nextSnapshot = buildConversationWithProviderId(healedSnapshot, resolvedProviderId)
      setAIProviderState({
        currentProviderId: resolvedProviderId,
        providers: latestProviders,
      })
      setConversationList((prev) => upsertConversationSummary(prev, nextSnapshot))
      setPanelState(panelInstanceKey, {
        activeConversationId: nextSnapshot.id,
        conversation: nextSnapshot,
        messages: nextSnapshot.messages,
        apiMessages: nextSnapshot.apiMessages,
        activeRequestId: '',
        activeAssistantMessageId: '',
        activeToolExecution: null,
        toolApprovalMode: '',
        requestPhase: 'idle',
        runtimePhase: 'ready',
        queuedSubmission: null,
        isFlushingQueuedSubmission: false,
        skipNextAutomaticRequest: false,
        resumeAfterCancelRequestId: '',
        recoverableToolStopReason: '',
        ...computeAILastAssistantTurnState(nextSnapshot.messages),
        contextTokens: 0,
        upstreamInputTokens: nextSnapshot.upstreamInputTokens,
        upstreamOutputTokens: nextSnapshot.upstreamOutputTokens,
        isCondensingContext: false,
        collaborationLocked: false,
        collaborationActive: false,
        collaborationMode: '',
        collaborationStreamBuffer: '',
        collaborationAwaitingManualFollowup: false,
        collaborationFollowupRequestId: '',
      })
      if (nextSnapshot !== snapshot) {
        await saveConversationSnapshot(nextSnapshot, panelInstanceKey)
        return
      }
      void rebuildAIConversationTokenLedger(nextSnapshot, panelInstanceKey)
    } catch {
    } finally {
      if (conversationLoadRequestRef.current === requestToken) {
        setPendingConversationId('')
      }
    }
  }, [aiProviderState, availableAIProviders, buildConversationWithProviderId, onOpenConversationRequested, panelInstanceKey, rebuildAIConversationTokenLedger, resetComposerEditState, resolveAvailableProviderId, saveConversationSnapshot, setPanelState])
  useEffect(() => {
    const normalizedConversationId = initialConversationId.trim()
    if (
      !isWorkspaceTabActive
      || !normalizedConversationId
      || pendingConversationId === normalizedConversationId
      || panelState.activeConversationId === normalizedConversationId
    ) {
      return
    }
    void handleOpenConversation(normalizedConversationId, false)
  }, [handleOpenConversation, initialConversationId, isWorkspaceTabActive, panelState.activeConversationId, pendingConversationId])
  const handleRestoreConversationBackup = useCallback(async (snapshot: unknown) => {
    const rawSnapshot = snapshot && typeof snapshot === 'object' ? snapshot as AIConversationSnapshot : null
    if (!rawSnapshot?.id) {
      return
    }
    setThemeToolPreview(null)
    clearRestorePreview()
    resetComposerEditState()
    resetGlobalSearchState()
    resetConversationSearchState()
    setConversationList((prev) => upsertConversationSummary(prev, rawSnapshot))
    setPanelState(panelInstanceKey, {
      activeConversationId: rawSnapshot.id,
      conversation: rawSnapshot,
      messages: rawSnapshot.messages,
      apiMessages: rawSnapshot.apiMessages,
      activeRequestId: '',
      activeAssistantMessageId: '',
      activeToolExecution: null,
      toolApprovalMode: '',
      requestPhase: 'idle',
      runtimePhase: 'ready',
      queuedSubmission: null,
      isFlushingQueuedSubmission: false,
      skipNextAutomaticRequest: false,
      resumeAfterCancelRequestId: '',
      recoverableToolStopReason: '',
      ...computeAILastAssistantTurnState(rawSnapshot.messages),
      contextTokens: 0,
      upstreamInputTokens: rawSnapshot.upstreamInputTokens,
      upstreamOutputTokens: rawSnapshot.upstreamOutputTokens,
      isCondensingContext: false,
      collaborationLocked: false,
      collaborationActive: false,
      collaborationMode: '',
      collaborationStreamBuffer: '',
      collaborationAwaitingManualFollowup: false,
      collaborationFollowupRequestId: '',
    })
    // 恢复备份: 全量重建账本 (100% 可靠)
    void rebuildAIConversationTokenLedger(rawSnapshot, panelInstanceKey)
  }, [panelInstanceKey, rebuildAIConversationTokenLedger, resetComposerEditState, setPanelState])
  const handleOpenConversationFolder = useCallback(async (conversationId: string) => {
    try {
      await openAIConversationFolder(conversationId)
    } catch (error) {
      const message = error instanceof Error && error.message ? error.message : t('打开任务所在文件夹失败')
      await showAlert(message)
    }
  }, [showAlert, t])
  const handleRenameConversationTitle = useCallback(async (targetConversationId = '') => {
    const normalizedTargetConversationId = typeof targetConversationId === 'string' ? targetConversationId.trim() : ''
    let conversationToRename = activeConversation
    if (!conversationToRename || (normalizedTargetConversationId && conversationToRename.id !== normalizedTargetConversationId)) {
      if (!normalizedTargetConversationId) {
        return
      }
      try {
        conversationToRename = await getAIConversation(normalizedTargetConversationId)
      } catch {
        return
      }
    }
    if (!conversationToRename || conversationToRename.transient === true) {
      return
    }
    const currentTitle = typeof conversationToRename.title === 'string' ? conversationToRename.title.trim() : ''
    const nextTitle = window?.lumeDialog?.prompt
      ? await window.lumeDialog.prompt(
          t('请输入任务标题'),
          currentTitle,
          t('编辑任务标题'),
          '',
          {
            validate: (value) => (String(value || '').trim() ? '' : t('任务标题不能为空')),
          },
        )
      : window.prompt(t('请输入任务标题'), currentTitle)
    if (nextTitle === null || nextTitle === undefined) {
      return
    }
    const trimmedTitle = String(nextTitle).trim()
    if (!trimmedTitle || trimmedTitle === currentTitle) {
      return
    }
    const nextConversation = {
      ...conversationToRename,
      title: trimmedTitle,
      updatedAt: Date.now(),
    }
    if (activeConversation?.id === nextConversation.id) {
      setPanelState(panelInstanceKey, (current) => ({
        ...current,
        conversation: nextConversation,
      }))
    }
    await saveConversationSnapshot(nextConversation, panelInstanceKey)
    addToast?.(t('任务标题已更新'), 'success')
  }, [activeConversation, addToast, panelInstanceKey, saveConversationSnapshot, setPanelState, t])
  const handleSelectGlobalSearchResult = useCallback(async (result: AIConversationMessageSearchResult) => {
    const conversationId = typeof result?.conversationId === 'string' ? result.conversationId.trim() : ''
    const messageId = typeof result?.messageId === 'string' ? result.messageId.trim() : ''
    if (!conversationId || !messageId) {
      return
    }
    if (onOpenConversationRequested) {
      await onOpenConversationRequested(conversationId, messageId)
      return
    }
    if (conversationId !== panelState.activeConversationId) {
      await handleOpenConversation(conversationId)
    } else {
      resetGlobalSearchState()
    }
    window.setTimeout(() => {
      locateConversationMessage(messageId)
    }, 40)
  }, [handleOpenConversation, locateConversationMessage, onOpenConversationRequested, panelState.activeConversationId, resetGlobalSearchState])
  const handleDeleteConversation = useCallback(async (conversationId: string) => {
    const deletingActiveConversation = panelState.activeConversationId === conversationId
    if (deletingActiveConversation) {
      setThemeToolPreview(null)
    }
    clearRestorePreview()
    const confirmed = await requestDeleteConfirmation(t('确定删除这条对话吗？此操作不可撤销。'))
    if (!confirmed) {
      return
    }
    const removedTemporaryConversation = removeTemporaryAIConversation(conversationId)
    if (removedTemporaryConversation) await deleteTemporaryAIConversation(conversationId)
    else await deleteAIConversation(conversationId)
    // 登记已删除 ID：拦截仍在途的并发保存，防止临时会话文件复活
    deletedConversationIdsRef.current.add(conversationId)
    tokenLedgerRef.current.delete(conversationId)
    setComposerEditState((current) => (
      current.mode !== 'new' && deletingActiveConversation
        ? { mode: 'new', targetMessageId: '', targetMessageText: '' }
        : current
    ))
    if (deletingActiveConversation) {
      await handleGoHome()
      return
    }
    const refreshedConversations = await listAIConversations().catch(() => [])
    setConversationList([...listInMemoryTemporaryAIConversations(), ...(Array.isArray(refreshedConversations) ? refreshedConversations : [])])
    const currentActiveConversationId = typeof terminalPanelsRef.current?.[panelInstanceKey]?.activeConversationId === 'string'
      ? terminalPanelsRef.current[panelInstanceKey].activeConversationId.trim()
      : ''
    if (currentActiveConversationId && currentActiveConversationId !== conversationId && refreshedConversations.some((item) => item?.id === currentActiveConversationId)) {
      await handleOpenConversation(currentActiveConversationId)
    }
  }, [clearRestorePreview, handleGoHome, handleOpenConversation, panelInstanceKey, panelState.activeConversationId, requestDeleteConfirmation, t])
  const refreshConversationList = useCallback(async () => {
    const conversations = await listAIConversations().catch(() => [])
    const temporarySummaries = await listTemporaryAIConversationsFromDisk().catch(() => [])
    seedTemporaryAIConversations(temporarySummaries)
    setConversationList([...temporarySummaries.map((summary) => ({ ...summary, transient: true })), ...(Array.isArray(conversations) ? conversations : [])])
  }, [])
  useEffect(() => {
    const handleDeleteWorkspaceTabConversation = (event: Event) => {
      const detail = (event as CustomEvent).detail || {}
      const targetSessionId = typeof detail?.sessionId === 'string' ? detail.sessionId.trim() : ''
      const targetTerminalId = typeof detail?.terminalId === 'string' ? detail.terminalId.trim() : ''
      const targetTabId = typeof detail?.tabId === 'string' ? detail.tabId.trim() : ''
      const targetConversationId = typeof detail?.conversationId === 'string' ? detail.conversationId.trim() : ''
      if (
        !targetConversationId
        || targetTabId !== (workspaceTabId || '').trim()
        || targetSessionId !== (sessionId || '').trim()
        || targetTerminalId !== (terminalId || '').trim()
      ) {
        return
      }
      void handleDeleteConversation(targetConversationId)
    }
    window.addEventListener('ai-workspace-tab-delete-conversation', handleDeleteWorkspaceTabConversation)
    return () => window.removeEventListener('ai-workspace-tab-delete-conversation', handleDeleteWorkspaceTabConversation)
  }, [handleDeleteConversation, sessionId, terminalId, workspaceTabId])

  const handleProviderChange = useCallback(async (providerId: string) => {
    const normalizedProviderId = typeof providerId === 'string' ? providerId.trim() : ''
    const syncLatestProviderState = async () => {
      try {
        const latestProviderState = await getAIProviderState()
        setAIProviderState({
          currentProviderId: normalizedProviderId || latestProviderState.currentProviderId || '',
          providers: Array.isArray(latestProviderState?.providers) ? latestProviderState.providers : [],
        })
      } catch {
        setAIProviderState((current) => ({
          ...current,
          currentProviderId: normalizedProviderId,
        }))
      }
    }

    setAIProviderState((current) => ({
      ...current,
      currentProviderId: normalizedProviderId,
    }))
    if (activeConversation) {
      const nextConversation = {
        ...activeConversation,
        updatedAt: Date.now(),
        settings: {
          ...((activeConversation?.settings as Record<string, unknown> | null) || {}),
          currentProviderId: normalizedProviderId,
        },
      }
      setPanelState(panelInstanceKey, (current) => ({
        ...current,
        conversation: nextConversation,
      }))
      await saveConversationSnapshot(nextConversation, panelInstanceKey)
      await syncLatestProviderState()
      return
    }

    const nextSettings = await saveAIGlobalSettings({
      ...(globalAISettings || {}),
      currentProviderId: normalizedProviderId,
    })
    setGlobalAISettings(nextSettings)
    await syncLatestProviderState()
  }, [activeConversation, globalAISettings, panelInstanceKey, saveConversationSnapshot, setPanelState])
  return {
    aiProviderState,
    setAIProviderState,
    refreshAIHomeData,
    terminalLabelMap,
    enrichAIChatCommandMessage,
    selectedAIProvider,
    availableAIProviders,
    resolveFirstAvailableProviderId,
    resolveAvailableProviderId,
    buildConversationWithProviderId,
    effectiveProviderId,
    resolveAIRequestModelMeta,
    conversationDiffItems,
    handleOpenConversationDiff,
    handleGoHome,
    handleOpenConversation,
    handleRestoreConversationBackup,
    handleOpenConversationFolder,
    handleRenameConversationTitle,
    handleSelectGlobalSearchResult,
    handleDeleteConversation,
    refreshConversationList,
    handleProviderChange,
  }
}
