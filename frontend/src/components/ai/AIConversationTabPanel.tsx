import { useEffect, useMemo, useState } from 'react'
import { ArrowDown, ArrowUp, Columns2, Database, Loader2, Search } from 'lucide-react'
import { useTranslation, getLanguage } from '../../i18n.ts'
import { Z } from '../../constants/zIndex'
import Tiptop from '../Tiptop.tsx'
import AIPanelHeader, { formatAIContextTokens } from './AIPanelHeader.tsx'
import { normalizeAIConversationTaskSettings } from './aiConversationBridge.ts'
import { useAIChatStreamEvents } from './useAIChatStreamEvents.ts'
import { useAIPanelCoreState } from './useAIPanelCoreState.ts'
import { useAIPanelSettingsState } from './useAIPanelSettingsState.ts'
import { useAIGlobalSearch } from './useAIGlobalSearch.ts'
import { useAIConversationSearch } from './useAIConversationSearch.ts'
import { useAIConversationOrganizer } from './useAIConversationOrganizer.ts'
import { useAIConversationHome } from './useAIConversationHome.ts'
import { useAIAutoApprovalSettings } from './useAIAutoApprovalSettings.ts'
import { useAIChatRequests } from './useAIChatRequests.ts'
import { useAIChatActions } from './useAIChatActions.ts'
import { renderAIHomeView } from './AIHomeView.tsx'
import { renderAIConversationStage } from './AIConversationStage.tsx'
import { renderAIComposerSection, renderAISettingsOverlaySection } from './AIConversationPanelSections.tsx'
import { AIWorkspaceTabProvider } from './aiWorkspaceTabContext.ts'
import { subscribeAIWorkspaceTabGroups } from '../../utils/aiWorkspaceTabs.ts'
import type { AIPanelProps } from './aiChatLogic.ts'
import type { ConversationSummary } from './aiConversationSummary.ts'
// ============================================================
// AIConversationTabPanel：单个工作区标签页的对话面板（外壳见 ../../AIPanel.tsx）。
// ============================================================
// ============================================================

export function AIConversationTabPanel({ width, side, terminalId = 'global', sessionId = '', sessionTerminals = [], workspaceTabId = '', isHomeView = false, isWorkspaceTabActive = true, showComposer = true, initialConversationId = '', tabBar = null, onGoHomeRequested, onOpenConversationRequested, onWorkspaceTabDisplaySettingsChange, onWorkspaceTabStateChange, addToast }: AIPanelProps) {
  const { t } = useTranslation()
  const [conversationList, setConversationList] = useState<ConversationSummary[]>([])
  useEffect(() => subscribeAIWorkspaceTabGroups(() => setConversationList((current) => [...current])), [])
  const {
    panelInstanceKey, terminalPanelsRef, deletedConversationIdsRef, isReturningHomeRef,
    conversationLoadRequestRef, panelMountedRef, tokenLedgerRef, sendPerfMetricsRef,
    pendingConversationId, setPendingConversationId,
    composerInputValue, setComposerInputValue, composerImages, setComposerImages,
    composerEditState, setComposerEditState, resetComposerEditState,
    conversationScrollSignal, requestConversationSmoothScrollToBottom, clearRestorePreview,
    panelState, activeConversation, normalizedInitialConversationId, isConversationLoading,
    activeConversationRelationType, activeConversationArchived, isThemeTuningConversation,
    runtimePhase, isStreaming, isAwaitingToolApproval, isToolRunning,
    isAwaitingCommandAction, isAwaitingTerminalAssignment, isQueueBlocked,
    setPanelState, truncateConversationAfterMessage, rebuildAIConversationTokenLedger,
    saveConversationSnapshot,
  } = useAIPanelCoreState({ terminalId, sessionId, workspaceTabId, initialConversationId, isWorkspaceTabActive, onWorkspaceTabStateChange, setConversationList })

  const {
    globalSearchOpen, globalSearchQuery, setGlobalSearchQuery, globalSearchLoading,
    globalSearchResults, globalSearchInputRef, resetGlobalSearchState, normalizedGlobalSearchQuery, handleOpenGlobalSearch,
  } = useAIGlobalSearch({ panelMountedRef })

  const {
    conversationSearchOpen, conversationSearchQuery, setConversationSearchQuery,
    conversationSearchIndex, conversationSearchInputRef,
    resetConversationSearchState, conversationSearchResults,
    locateConversationMessage, handleOpenConversationSearch, handleCycleConversationSearchResult,
  } = useAIConversationSearch({ sessionId, terminalId, workspaceTabId, panelState, activeConversation })

  const {
    mcpInfo, mcpClientServers, mcpClientGlobalConfigPath, mcpClientGlobalConfigText, mcpEmbeddedFirecrawlApiKey,
    showSettingsPanel, setShowSettingsPanel, popupDismissVersion, setPopupDismissVersion,
    activeSettingsTab, setActiveSettingsTab, tasksDirMigrating, setTasksDirMigrating,
    temporarySessionEnabled, setTemporarySessionEnabled, themeToolPreview: _themeToolPreview, setThemeToolPreview,
    globalAISettings, setGlobalAISettings, terminalOutputLineLimit, terminalOutputCharacterLimit,
    providerBalanceRefreshSignal, setProviderBalanceRefreshSignal, refreshMCPServerInfo,
    refreshMCPOutputCompressionSettings, showAlert, playAISound, requestDeleteConfirmation,
    normalizedGlobalAISettings, handleSaveAIPanelGlobalSettings, handleSaveMCPGlobalServer, handleSaveMCPEmbeddedFirecrawlApiKey,
    handleReloadMCPGlobalServers, handleDeleteMCPGlobalServer, handleRestartMCPClientServer,
    handleToggleMCPClientServer, handleToggleMCPClientServerDisabledForPrompts, handleToggleMCPClientServerToolDisabledForPrompts,
    handleUpdateMCPClientServerTimeout, handleToggleAiTerminalIsolation,
    handleToggleConfirmDelete, handleToggleSettingsPanel, handleTerminalOutputLineLimitChange,
    handleTerminalOutputCharacterLimitChange,
  } = useAIPanelSettingsState({ t, isWorkspaceTabActive, panelMountedRef, activeConversation, resetGlobalSearchState, resetConversationSearchState })
  useEffect(() => {
    if (!globalAISettings) {
      return
    }
    onWorkspaceTabDisplaySettingsChange?.(normalizedGlobalAISettings.aiWorkspaceTabNumbersOnly === true)
  }, [globalAISettings, normalizedGlobalAISettings.aiWorkspaceTabNumbersOnly, onWorkspaceTabDisplaySettingsChange])

  const {
    aiProviderState, setAIProviderState,
    terminalLabelMap, enrichAIChatCommandMessage,
    availableAIProviders,
    resolveAvailableProviderId, buildConversationWithProviderId,
    resolveAIRequestModelMeta, effectiveProviderId,
    handleOpenConversationDiff, handleGoHome,
    handleOpenConversation, handleRestoreConversationBackup, handleOpenConversationFolder,
    handleRenameConversationTitle, handleSelectGlobalSearchResult, handleDeleteConversation,
    refreshConversationList, handleProviderChange,
  } = useAIConversationHome({
    t, addToast, terminalId, sessionId, workspaceTabId, initialConversationId, isWorkspaceTabActive, sessionTerminals,
    onGoHomeRequested, onOpenConversationRequested, panelInstanceKey, panelState, activeConversation,
    pendingConversationId, setPendingConversationId, setPanelState, setComposerEditState, terminalPanelsRef,
    deletedConversationIdsRef, isReturningHomeRef, conversationLoadRequestRef, panelMountedRef, tokenLedgerRef,
    rebuildAIConversationTokenLedger, saveConversationSnapshot, clearRestorePreview, resetComposerEditState,
    setThemeToolPreview, setShowSettingsPanel, setPopupDismissVersion, showAlert, refreshMCPServerInfo,
    refreshMCPOutputCompressionSettings, globalAISettings, setGlobalAISettings, conversationList, setConversationList,
    resetGlobalSearchState, resetConversationSearchState, locateConversationMessage, requestDeleteConfirmation,
  })

  const effectiveAutoApprovalSettings = useMemo(() => {
    if (!activeConversation) {
      return normalizedGlobalAISettings
    }
    const normalizedTaskSettings = normalizeAIConversationTaskSettings(activeConversation.settings)
    return {
      ...normalizedTaskSettings,
      allowedCommands: normalizedGlobalAISettings.allowedCommands,
      deniedCommands: normalizedGlobalAISettings.deniedCommands,
    }
  }, [activeConversation, normalizedGlobalAISettings])
  const effectiveAutoApprovalEnabled = effectiveAutoApprovalSettings.autoApprovalEnabled
  const shouldPersistProviderSelection = !activeConversation
  const approvalButtonOrder = normalizedGlobalAISettings.approvalButtonOrder
  const commandActionButtonOrder = normalizedGlobalAISettings.commandActionButtonOrder
  const messageNavEnabled = normalizedGlobalAISettings.messageNavEnabled !== false
  const shouldLockAssistantCollaboration = Boolean(effectiveAutoApprovalSettings.alwaysAllowFollowupQuestions)
  const collaborationLocked = Boolean(panelState.collaborationLocked) && Boolean(activeConversation)
  const collaborationActive = Boolean(panelState.collaborationActive)
  const isSummarySubtaskCollaborationActive = collaborationActive && panelState.collaborationMode === 'summary_subtask'
  const isArchivedAgentConversation = activeConversationArchived && activeConversationRelationType === 'agent'
  const canQuickCondenseConversation = Boolean(activeConversation) && runtimePhase === 'ready' && !panelState.isCondensingContext && !isArchivedAgentConversation
  const canSummaryCondenseConversation = Boolean(activeConversation) && runtimePhase === 'ready' && !panelState.isCondensingContext
  const composerInteractionLocked = isConversationLoading || (isArchivedAgentConversation && !isSummarySubtaskCollaborationActive)
  const composerInteractionLockedLabel = isConversationLoading
    ? t('加载中...')
    : t('当前子代理任务已归档,仅可摘要压缩创建新的子阶段任务')
  const collaborationFollowupInteractionLocked = collaborationLocked && collaborationActive && panelState.collaborationMode === 'followup'
  const showAssistantCollaborationActiveImage = !isConversationLoading && collaborationActive && Boolean(activeConversation)
  const toolResumeAvailable = Boolean(activeConversation)
    && !isArchivedAgentConversation
    && panelState.requestPhase === 'idle'
    && runtimePhase === 'ready'
    && !panelState.queuedSubmission
    && !panelState.isFlushingQueuedSubmission
    && !collaborationActive
    && !panelState.isCondensingContext
    && (!panelState.lastTurnBusinessMessageKind || (panelState.lastTurnBusinessMessageKind !== 'completion' && panelState.lastTurnBusinessMessageKind !== 'followup'))

  const {
    conversationOrganizer, conversationFilter, setConversationFilter,
    conversationSelectionMode, setConversationSelectionMode, selectedConversationIds,
    moveToGroupOpen, setMoveToGroupOpen, editingConversationGroupId,
    editingConversationGroupName, setEditingConversationGroupName, draggingConversationGroupId,
    dragOverConversationGroupId, setDraggingConversationGroupId, setDragOverConversationGroupId,
    conversationGroupRenameInputRef,
    handleMakeConversationPermanent, handleCreateConversationGroup, beginRenameConversationGroup,
    cancelRenameConversationGroup, commitRenameConversationGroup, reorderConversationGroup,
    showSystemGroupRenameUnsupported, handleDeleteConversationGroup, toggleConversationSelection,
    clearConversationSelection, handleMoveSelectedConversations, handleSetSelectedArchived,
    handleDeleteSelectedConversations,
  } = useAIConversationOrganizer({
    t, addToast, showAlert, requestDeleteConfirmation, isWorkspaceTabActive,
    refreshConversationList, handleOpenConversation, setConversationList,
  })

  const {
    handlePatchAutoApprovalSettings, handleCollaborationExtraPromptChange,
    handleCollaborationPromptPresetsChange,
  } = useAIAutoApprovalSettings({ activeConversation, globalAISettings, normalizedGlobalAISettings, panelState, panelInstanceKey, saveConversationSnapshot, setPanelState, setGlobalAISettings, setComposerInputValue })

  const {
    handleSendMessage, handleConversationUserMessage, handleComposerSendMessage,
    handleRetryUserMessage, handleRetryAssistantMessage, handleEditUserMessage, handleDeleteMessage,
    handleCondenseContext, runAIConversationSummarySubtaskFlow,
    handleCondenseContextFullSummary, resumeAIChatFromConversation,
  } = useAIChatRequests({
    t, terminalId, sessionId, workspaceTabId, isWorkspaceTabActive, activeConversation,
    panelState, panelInstanceKey, terminalPanelsRef, sendPerfMetricsRef, setPanelState,
    setConversationList, setAIProviderState, setGlobalAISettings, setComposerEditState,
    setComposerInputValue, setComposerImages, resetComposerEditState,
    requestConversationSmoothScrollToBottom, clearRestorePreview, truncateConversationAfterMessage,
    saveConversationSnapshot, rebuildAIConversationTokenLedger, showAlert, requestDeleteConfirmation,
    resolveAvailableProviderId, buildConversationWithProviderId, resolveAIRequestModelMeta,
    setThemeToolPreview, globalAISettings, normalizedGlobalAISettings, aiProviderState,
    availableAIProviders, composerEditState, composerImages, temporarySessionEnabled,
    isQueueBlocked, isArchivedAgentConversation, runtimePhase, effectiveProviderId,
    effectiveAutoApprovalEnabled, shouldLockAssistantCollaboration,
    collaborationFollowupInteractionLocked, terminalOutputLineLimit, terminalOutputCharacterLimit,
  })

  useAIChatStreamEvents({
    terminalId,
    sessionId,
    workspaceTabId,
    panelInstanceKey,
    terminalPanelsRef,
    shouldLockAssistantCollaboration,
    activeConversation,
    panelState,
    setPanelState,
    enrichAIChatCommandMessage,
    playAISound,
    rebuildAIConversationTokenLedger,
    saveConversationSnapshot,
    resumeAIChatFromConversation,
    runAIConversationSummarySubtaskFlow,
    setThemeToolPreview,
    setConversationList,
    setComposerInputValue,
    setComposerImages,
    setProviderBalanceRefreshSignal,
  })


  const {
    handleCancelMessage, handleStopAndResumeMessage, handleResumeTask, handleApproveTools,
    handleRejectTools, handleContinueTool, handleTerminateTool, handlePreviewRestore,
    handlePreviewDiff, handleApplyRestore, handleRestoreToHere, handleReapplyRestore, handleListCommandTerminalCandidates,
    handleAssignToolTerminal, handleToggleSkipNextAutomaticRequest, handleInterruptCollaboration,
    handleCancelQueuedSubmission,
  } = useAIChatActions({
    addToast, terminalId, workspaceTabId, activeConversation, panelState, panelInstanceKey,
    terminalPanelsRef, panelMountedRef, setPanelState, showAlert, clearRestorePreview,
    terminalLabelMap, isQueueBlocked, handleSendMessage, handleRetryAssistantMessage,
    resumeAIChatFromConversation, normalizedGlobalAISettings,
  })


  // ponytail: mcpInfo.transport 是 MCP 协议层名称（streamable-http），
  // 客户端配置文件（如 ~/.claude.json）期望的 type 值为 "http"，这里做映射。
  // 仅 streamable-http 需要转换，其他值（如 sse、stdio）保持原样。
  const mcpConfigType = mcpInfo.transport === 'streamable-http' ? 'http' : (mcpInfo.transport || 'http')
  const configText = `"lumeterm": {
  "type": "${mcpConfigType}",
  "url": "${mcpInfo.url || ''}",
  "oauth": false,
  "alwaysAllow": [],
  "disabled": false,
  "timeout": 0,
  "disabledForPrompts": false
}`
  const configRows = Math.max(configText.split('\n').length, 1)
  const normalizedUpstreamInputTokens = Number.isFinite(Number(panelState.upstreamInputTokens)) && Number(panelState.upstreamInputTokens) > 0 ? Math.trunc(Number(panelState.upstreamInputTokens)) : 0
  const normalizedUpstreamOutputTokens = Number.isFinite(Number(panelState.upstreamOutputTokens)) && Number(panelState.upstreamOutputTokens) > 0 ? Math.trunc(Number(panelState.upstreamOutputTokens)) : 0
  const hasUpstreamTokenUsage = normalizedUpstreamInputTokens > 0 || normalizedUpstreamOutputTokens > 0

  const renderedConversationList = useMemo(() => renderAIHomeView({ t, conversationList, conversationOrganizer, conversationFilter, setConversationFilter, conversationSelectionMode, setConversationSelectionMode, selectedConversationIds, moveToGroupOpen, setMoveToGroupOpen, editingConversationGroupId, editingConversationGroupName, setEditingConversationGroupName, draggingConversationGroupId, dragOverConversationGroupId, setDraggingConversationGroupId, setDragOverConversationGroupId, panelState, globalSearchOpen, globalSearchQuery, setGlobalSearchQuery, normalizedGlobalSearchQuery, globalSearchLoading, globalSearchResults, globalSearchInputRef, conversationGroupRenameInputRef, resetGlobalSearchState, handleOpenGlobalSearch, handleSelectGlobalSearchResult, toggleConversationSelection, clearConversationSelection, handleOpenConversation, handleMakeConversationPermanent, handleOpenConversationFolder, handleRenameConversationTitle, handleDeleteConversation, handleCreateConversationGroup, beginRenameConversationGroup, cancelRenameConversationGroup, commitRenameConversationGroup, reorderConversationGroup, showSystemGroupRenameUnsupported, handleDeleteConversationGroup, handleMoveSelectedConversations, handleSetSelectedArchived, handleDeleteSelectedConversations }), [beginRenameConversationGroup, cancelRenameConversationGroup, clearConversationSelection, commitRenameConversationGroup, conversationFilter, conversationList, conversationOrganizer, conversationSelectionMode, dragOverConversationGroupId, draggingConversationGroupId, editingConversationGroupId, editingConversationGroupName, getLanguage, globalSearchLoading, globalSearchOpen, globalSearchQuery, globalSearchResults, handleCreateConversationGroup, handleDeleteConversation, handleDeleteConversationGroup, handleDeleteSelectedConversations, handleMakeConversationPermanent, handleMoveSelectedConversations, handleOpenConversation, handleOpenConversationFolder, handleOpenGlobalSearch, handleSelectGlobalSearchResult, handleSetSelectedArchived, moveToGroupOpen, normalizedGlobalSearchQuery, panelState.activeConversationId, reorderConversationGroup, resetGlobalSearchState, selectedConversationIds, showSystemGroupRenameUnsupported, t, toggleConversationSelection])

  return (
    <AIWorkspaceTabProvider value={{ sessionId: sessionId || '', terminalId: terminalId || '', tabId: workspaceTabId || '' }}>
      <div
        data-ai-panel-root="true"
        style={{
          width: width || '100%',
          minWidth: 0,
          maxWidth: '100%',
          height: '100%',
          minHeight: 0,
          background: 'var(--surface-raised)',
          flexShrink: 1,
          borderRight: side === 'right' ? '1px solid var(--border)' : 'none',
          borderLeft: side === 'left' ? '1px solid var(--border)' : 'none',
          display: 'flex',
          flexDirection: 'column',
          boxSizing: 'border-box',
          overflow: 'hidden',
          position: 'relative',
          fontFamily: 'var(--font-ai-panel)',
        }}
      >
      {tasksDirMigrating ? (
        <div className="absolute inset-0 bg-scrim/85 backdrop-blur-[3px] flex flex-col items-center justify-center gap-3" style={{ zIndex: Z.SETTINGS }}>
          <Loader2 size={36} className="animate-[spin_1s_linear_infinite] text-accent" />
          <div className="text-md font-semibold text-primary">{t('正在迁移对话数据...')}</div>
          <div className="text-sm text-tertiary">{t('迁移期间请勿使用 AI 对话')}</div>
        </div>
      ) : null}
      <AIPanelHeader
        showSettingsPanel={showSettingsPanel}
        onToggleSettings={handleToggleSettingsPanel}
        onGoHome={handleGoHome}
        showContextTokens={Boolean(activeConversation) && !isConversationLoading}
        contextTokens={panelState.contextTokens}
        apiMessageCount={Array.isArray(panelState.apiMessages) ? panelState.apiMessages.length : 0}
        isCondensingContext={Boolean(panelState.isCondensingContext)}
        canCondenseContext={canQuickCondenseConversation || canSummaryCondenseConversation}
        canQuickCondenseContext={canQuickCondenseConversation}
        canSummaryCondenseContext={canSummaryCondenseConversation}
        onCondenseContext={handleCondenseContext}
        onCondenseContextFullSummary={handleCondenseContextFullSummary}
        fullSummaryCondenseAvailable={true}
      />
      {isWorkspaceTabActive ? tabBar : null}
      {activeConversation && !isConversationLoading ? (
        <div className="h-[26px] shrink-0 grid grid-cols-[minmax(0,1fr)_auto_auto_minmax(0,1fr)] items-center gap-2 px-2 border-b border-line bg-raised leading-none">
          <div />
          <div className="justify-self-center flex items-center gap-2">
            {(() => {
              const recentAssistantMessages = Array.isArray(panelState.messages)
                ? panelState.messages.filter((message) => message.kind === 'assistant').slice(-8)
                : []
              return Array.from({ length: 8 }, (_, index) => {
                const message = recentAssistantMessages[index - (8 - recentAssistantMessages.length)]
                const cacheReadTokens = Number(message?.extra?.cacheReadTokens)
                const hasCacheReadTokens = Boolean(message) && Number.isFinite(cacheReadTokens)
                const isCacheHit = hasCacheReadTokens && cacheReadTokens >= 4000
                const statusKey = !hasCacheReadTokens ? 'pending' : (isCacheHit ? 'hit' : 'miss')
                const colorClassName = !hasCacheReadTokens
                  ? 'text-tertiary'
                  : isCacheHit
                    ? 'text-success'
                    : 'text-danger'
                const animationClassName = statusKey === 'hit'
                  ? 'animate-[ai-cache-status-hit-pop_2000ms_cubic-bezier(0.22,1,0.36,1)_both]'
                  : statusKey === 'miss'
                    ? 'animate-[ai-cache-status-miss-pop_2400ms_cubic-bezier(0.34,1.1,0.32,1)_both]'
                    : ''
                const label = !hasCacheReadTokens
                  ? t('暂无 API 请求')
                  : isCacheHit
                    ? t('缓存命中')
                    : t('缓存未命中')
                // key 绑定消息与状态：状态首次确定时重挂载播放一次，列表滑动不重播
                const statusNodeKey = message ? `${message.id}:${statusKey}` : `ai-cache-status-placeholder-${index}`
                return (
                  <Tiptop key={statusNodeKey} text={label} placement="top">
                    <span className={`inline-flex items-center leading-none ${colorClassName} ${animationClassName}`} aria-label={label}>
                      <Database size={16} className="block" fill="currentColor" fillOpacity={0.3} strokeWidth={2} />
                    </span>
                  </Tiptop>
                )
              })
            })()}
          </div>
          {hasUpstreamTokenUsage ? (
            <Tiptop text={t('上游实际用量,输入/输出 Token')} placement="bottom">
              <span
                aria-label={t('上游实际用量,输入/输出 Token')}
                className="inline-flex items-center justify-center gap-1.5 w-fit min-w-0 h-5 px-2 rounded-[var(--radius-sm)] border border-line bg-transparent text-secondary text-xs font-bold whitespace-nowrap leading-none tabular-nums cursor-default select-none"
              >
                <span className="inline-flex items-center gap-0.5">
                  <ArrowUp size={11} />
                  <span>{formatAIContextTokens(normalizedUpstreamInputTokens)}</span>
                </span>
                <span className="inline-flex items-center gap-0.5">
                  <ArrowDown size={11} />
                  <span>{formatAIContextTokens(normalizedUpstreamOutputTokens)}</span>
                </span>
              </span>
            </Tiptop>
          ) : <div />}
          <div className="justify-self-end flex items-center gap-1">
            <Tiptop text={t('当前对话搜索')} placement="bottom">
              <button
                type="button"
                aria-label={t('当前对话搜索')}
                onClick={handleOpenConversationSearch}
                className={`inline-flex items-center justify-center w-[20px] h-[20px] rounded-md border cursor-pointer transition-colors duration-[80ms] ${
                  conversationSearchOpen
                    ? 'text-accent bg-accent-dim border-accent-border'
                    : 'text-secondary bg-transparent border-transparent'
                }`}
              >
                <Search size={14} className="block" />
              </button>
            </Tiptop>
            <Tiptop text={t('当前对话文件变更')} placement="bottom">
              <button
                type="button"
                aria-label={t('当前对话文件变更')}
                onClick={handleOpenConversationDiff}
                className="inline-flex items-center justify-center w-[20px] h-[20px] rounded-md border border-transparent bg-transparent text-secondary cursor-pointer transition-colors duration-[80ms]"
              >
                <Columns2 size={14} className="block" />
              </button>
            </Tiptop>
          </div>
        </div>
      ) : null}
      <div className="flex-1 min-h-0 flex flex-col overflow-hidden">
        {renderAIConversationStage({ t, side, sessionId, terminalId, workspaceTabId, isHomeView, activeConversation, isThemeTuningConversation, isConversationLoading, normalizedInitialConversationId, conversationSearchOpen, conversationSearchQuery, setConversationSearchQuery, conversationSearchInputRef, resetConversationSearchState, handleCycleConversationSearchResult, conversationSearchResults, conversationSearchIndex, panelState, handleConversationUserMessage, handleRetryUserMessage, handleRetryAssistantMessage, handleEditUserMessage, handleDeleteMessage, handlePreviewRestore, handlePreviewDiff, handleApplyRestore, handleRestoreToHere, handleReapplyRestore, collaborationFollowupInteractionLocked, messageNavEnabled, conversationScrollSignal, sendPerfMetricsRef, composerEditState, showAssistantCollaborationActiveImage, renderedConversationList, handleGoHome })}
        {renderAIComposerSection({ t, terminalId, showComposer, panelState, activeConversation, isStreaming, isQueueBlocked, isAwaitingToolApproval, isToolRunning, isAwaitingCommandAction, isAwaitingTerminalAssignment, collaborationLocked, collaborationActive, toolResumeAvailable, shouldPersistProviderSelection, composerInteractionLocked, composerInteractionLockedLabel, effectiveProviderId, effectiveAutoApprovalSettings, providerBalanceRefreshSignal, approvalButtonOrder, commandActionButtonOrder, composerEditState, composerInputValue, setComposerInputValue, composerImages, setComposerImages, temporarySessionEnabled, setTemporarySessionEnabled, normalizedGlobalAISettings, popupDismissVersion, handleComposerSendMessage, handleCancelMessage, handleStopAndResumeMessage, handleProviderChange, handleResumeTask, handleListCommandTerminalCandidates, handleAssignToolTerminal, handleCancelQueuedSubmission, handleToggleSkipNextAutomaticRequest, handlePatchAutoApprovalSettings, handleCollaborationExtraPromptChange, handleCollaborationPromptPresetsChange, handleInterruptCollaboration, handleApproveTools, handleRejectTools, handleContinueTool, handleTerminateTool, resetComposerEditState })}
      </div>
      {renderAISettingsOverlaySection({ showSettingsPanel, setShowSettingsPanel, activeSettingsTab, setActiveSettingsTab, mcpInfo, configText, configRows, normalizedGlobalAISettings, activeConversation, panelState, runtimePhase, terminalOutputLineLimit, terminalOutputCharacterLimit, mcpClientServers, mcpClientGlobalConfigPath, mcpClientGlobalConfigText, mcpEmbeddedFirecrawlApiKey, handleSaveAIPanelGlobalSettings, handleToggleAiTerminalIsolation, handleToggleConfirmDelete, handleRestoreConversationBackup, handleTerminalOutputLineLimitChange, handleTerminalOutputCharacterLimitChange, handleSaveMCPGlobalServer, handleSaveMCPEmbeddedFirecrawlApiKey, handleReloadMCPGlobalServers, handleDeleteMCPGlobalServer, handleRestartMCPClientServer, handleToggleMCPClientServer, handleToggleMCPClientServerDisabledForPrompts, handleToggleMCPClientServerToolDisabledForPrompts, handleUpdateMCPClientServerTimeout, setTasksDirMigrating })}
      </div>
    </AIWorkspaceTabProvider>
  )
}