import SessionWorkspace from '../SessionWorkspace.tsx';
import AppProbePanelHost from './AppProbePanelHost.tsx';
import AppAIPanelHost from './AppAIPanelHost.tsx';
import AppSessionFileManagers from './AppSessionFileManagers.tsx';
import type { useAppOrchestratorResult } from '../../hooks/useAppOrchestrator.ts';
import { isUnsupportedMonitorSession, type SessionLike } from '../../utils/sessionWorkspace.ts';

export interface AppWorkspaceViewProps {
  orchestrator: useAppOrchestratorResult;
}

export default function AppWorkspaceView({ orchestrator }: AppWorkspaceViewProps) {
  const {
    sessionState,
    panelLayout,
    preferences,
    aiReview,
    gitReview,
    animations,
    serverOps,
    quickCommands,
    globalEvents,
    shared,
    serversRef,
    sessionsRef,
    openPortForwardDialog,
  } = orchestrator;

  const activeSession = sessionState.sessions.find((session) => session.id === sessionState.activeSessionId);
  const probePanelVisible = Boolean(
    activeSession
    && !activeSession.isSerial
    && !isUnsupportedMonitorSession(activeSession)
    && (activeSession.status === 'connected' || (activeSession.status === 'closed' && panelLayout.monitoringEnabled[activeSession.id || '']))
  );

  const probePanelNode = (
    <AppProbePanelHost
      sessions={sessionState.sessions}
      activeSessionId={sessionState.activeSessionId}
      monitoringEnabled={panelLayout.monitoringEnabled}
      probeSnapshots={panelLayout.probeSnapshots}
      probePanelCollapsed={panelLayout.probePanelCollapsed}
      setProbeSnapshots={panelLayout.setProbeSnapshots}
      setMonitoringEnabled={panelLayout.setMonitoringEnabled}
      setContentTab={sessionState.setContentTab}
      openPortForwardDialog={(sId: string, initialMapping?: unknown, initialTab?: string) => openPortForwardDialog(sId, initialMapping as string | number | null | undefined, initialTab)}
      addToast={shared.addToast}
    />
  );

  const aiPanelNode = (
    <AppAIPanelHost
      sessions={sessionState.sessions}
      activeSessionId={sessionState.activeSessionId}
      activeTerminalId={sessionState.activeTerminalId}
      aiPanelWidth={panelLayout.aiPanelWidth}
      showAIPanel={panelLayout.showAIPanel}
      isActiveSessionConnected={sessionState.isActiveSessionConnected}
      collapseDragIntent={sessionState.collapseDragIntent}
      probePanelPosition={panelLayout.probePanelPosition}
      getEffectiveTerminals={sessionState.getEffectiveTerminals}
      addToast={shared.addToast}
      setActiveAIWorkspaceTabs={aiReview.setActiveAIWorkspaceTabs}
      sessionsRef={sessionsRef}
      markWorkspaceRestoreNavigationOverride={sessionState.markWorkspaceRestoreNavigationOverride}
      setAIPanelVisibility={panelLayout.setAIPanelVisibility}
      setActiveSessionId={sessionState.setActiveSessionId}
      setActiveTerminalId={sessionState.setActiveTerminalId}
      setContentTab={sessionState.setContentTab}
    />
  );

  const renderSessionFileManagers = (s: SessionLike) => (
    <AppSessionFileManagers
      session={s}
      servers={serversRef.current}
      activeSessionId={sessionState.activeSessionId}
      activeTerminalId={sessionState.activeTerminalId}
      addToast={shared.addToast}
      removeToast={shared.removeToast}
      getEffectiveTerminals={sessionState.getEffectiveTerminals}
    />
  );

  return (
    <SessionWorkspace
      dashboard={{
        allGroups: serverOps.allGroups,
        batchSelectionMode: serverOps.batchSelectionMode,
        clearRecentConnections: preferences.clearRecentConnections,
        connectLocal: sessionState.connectLocal,
        connectSerial: sessionState.connectSerial,
        connectServer: sessionState.connectServer,
        connectedSessions: sessionState.connectedSessions,
        credentials: orchestrator.credentials,
        dashboardHostPageMode: preferences.dashboardHostPageMode,
        editFlyAnimation: animations.editFlyAnimation,
        editFlyShiningFields: animations.editFlyShiningFields,
        filteredServers: serverOps.filteredServers,
        handleBatchConnect: serverOps.handleBatchConnect,
        handleBatchDelete: serverOps.handleBatchDelete,
        handleBatchExport: orchestrator.handleBatchExport,
        handleBatchMoveGroup: serverOps.handleBatchMoveGroup,
        handleDeleteServer: serverOps.handleDeleteServer,
        handleGroupDelete: serverOps.handleGroupDelete,
        handleMoveGroup: serverOps.handleMoveGroup,
        handleOpenImportExport: orchestrator.handleOpenImportExport,
        handleRefreshPing: orchestrator.handleRefreshPing,
        handleRenameGroup: serverOps.handleRenameGroup,
        handleSaveAndConnectServer: serverOps.handleSaveAndConnectServer,
        handleSaveServer: serverOps.handleSaveServer,
        hideSensitive: preferences.hideSensitive,
        isRefreshingPing: orchestrator.isRefreshingPing,
        pingCounts: orchestrator.pingCounts,
        pingEnabled: orchestrator.pingEnabled,
        pings: orchestrator.pings,
        recentConnectionIds: preferences.recentConnectionIds,
        removeRecentConnection: preferences.removeRecentConnection,
        saveFlowHighlights: animations.saveFlowHighlights,
        searchQuery: preferences.searchQuery,
        selectedServerIds: serverOps.selectedServerIds,
        serverEditor: orchestrator.serverEditor,
        serverListViewMode: preferences.serverListViewMode,
        servers: orchestrator.servers,
        setBatchSelectionMode: serverOps.setBatchSelectionMode,
        setDashboardHostPageMode: preferences.setDashboardHostPageMode,
        setHideSensitive: preferences.setHideSensitive,
        setSearchQuery: preferences.setSearchQuery,
        setServerEditor: orchestrator.setServerEditor,
        setServerListViewMode: preferences.setServerListViewMode,
        setShowCredentials: orchestrator.setShowCredentials,
        setShowSerialModal: orchestrator.setShowSerialModal,
        startAddGuideAnimation: animations.startAddGuideAnimation,
        startEditFlyAnimation: animations.startEditFlyAnimation,
        toggleBatchSelection: serverOps.toggleBatchSelection,
      }}
      session={{
        activeSession: sessionState.activeSession,
        activeSessionId: sessionState.activeSessionId,
        activeSessionRootTerminals: sessionState.activeSessionRootTerminals,
        activeTerminalId: sessionState.activeTerminalId,
        connectingServers: sessionState.connectingServers,
        contentTab: sessionState.contentTab,
        getEffectiveTerminals: sessionState.getEffectiveTerminals,
        getSessionPanes: sessionState.getSessionPanes,
        getSessionRootPaneCells: sessionState.getSessionRootPaneCells,
        getSessionWorkspaceTabs: sessionState.getSessionWorkspaceTabs,
        handleCancelConnection: sessionState.handleCancelConnection,
        isActiveSessionConnected: sessionState.isActiveSessionConnected,
        isCreatingTerminal: sessionState.creatingTerminalSessionId !== null,
        isSessionWorkspaceVisible: sessionState.isSessionWorkspaceVisible,
        markWorkspaceRestoreNavigationOverride: sessionState.markWorkspaceRestoreNavigationOverride,
        mountedSessions: sessionState.mountedSessions,
        openNewTerminal: sessionState.openNewTerminal,
        persistWorkspaceSnapshotRef: sessionState.persistWorkspaceSnapshotRef,
        rememberSessionActiveTerminal: sessionState.rememberSessionActiveTerminal,
        resolveHostKeyChoice: sessionState.resolveHostKeyChoice,
        resolvePasswordPrompt: sessionState.resolvePasswordPrompt,
        restoringWorkspaceSessionIds: sessionState.restoringWorkspaceSessionIds,
        sessionAuthPrompts: sessionState.sessionAuthPrompts,
        sessions: sessionState.sessions,
        setActiveTerminalId: sessionState.setActiveTerminalId,
        setContentTab: sessionState.setContentTabLoose,
        setTabContextMenu: sessionState.setTabContextMenu,
        setTerminalTabContextMenu: sessionState.setTerminalTabContextMenu,
        terminalPaneLayouts: sessionState.terminalPaneLayouts,
      }}
      fileManager={{
        bottomSplitHeight: panelLayout.bottomSplitHeight,
        collapseDragIntent: sessionState.collapseDragIntent,
        fileManagerCollapsed: sessionState.fileManagerCollapsed,
        fileManagerDockConfirmTarget: sessionState.fileManagerDockConfirmTarget,
        fileManagerDockDropzones: sessionState.fileManagerDockDropzones,
        fileManagerDockPreview: sessionState.fileManagerDockPreview,
        fileManagerDockTabAnchorRef: sessionState.fileManagerDockTabAnchorRef,
        fileManagerPosition: sessionState.fileManagerPosition,
        leftSplitWidth: panelLayout.leftSplitWidth,
        probePanelCollapsed: panelLayout.probePanelCollapsed,
        probePanelNode,
        probePanelVisible,
        probePanelPosition: panelLayout.probePanelPosition,
        probePanelWidth: panelLayout.probePanelWidth,
        renderSessionFileManagers,
        setFileManagerCollapsedPersistent: sessionState.setFileManagerCollapsedPersistent,
        setProbePanelCollapsedPersistent: panelLayout.setProbePanelCollapsedPersistent,
        shouldIgnoreResizerClick: sessionState.shouldIgnoreResizerClick,
        startDrag: sessionState.startDrag,
      }}
      terminalTabs={{
        closeTerminal: sessionState.closeTerminal,
        closeTerminalGroup: sessionState.closeTerminalGroup,
        closeTerminalPane: sessionState.closeTerminalPane,
        handleTerminalSubTabClickCapture: sessionState.handleTerminalSubTabClickCapture,
        handleTerminalSubTabDockMouseDown: sessionState.handleTerminalSubTabDockMouseDown,
        handleTerminalSubTabMouseDown: sessionState.handleTerminalSubTabMouseDown,
        handleTerminalSubTabScroll: sessionState.handleTerminalSubTabScroll,
        handleTerminalSubTabWheel: sessionState.handleTerminalSubTabWheel,
        shouldIgnoreTerminalDockClick: sessionState.shouldIgnoreTerminalDockClick,
        terminalDockDragPreview: sessionState.terminalDockDragPreview,
        terminalSubTabActionsRef: sessionState.terminalSubTabActionsRef,
        terminalSubTabOverflow: sessionState.terminalSubTabOverflow,
        terminalSubTabScrollRef: sessionState.terminalSubTabScrollRef,
        terminalSubTabScrollStyle: sessionState.terminalSubTabScrollStyle,
        terminalToolbarIconOnly: globalEvents.terminalToolbarIconOnly,
      }}
      ai={{
        gitReview,
        activeChangeReview: aiReview.activeChangeReview,
        activeChangeReviewQueue: aiReview.activeChangeReviewQueue,
        activeConversationDiffPanel: aiReview.activeConversationDiffPanel,
        activeRestorePreviewReview: aiReview.activeRestorePreviewReview,
        activeWorkspaceTerminalKey: aiReview.activeAIWorkspaceTabKey,
        activeAIWorkspaceTabId: aiReview.activeAIWorkspaceTabId,
        aiPanelNode,
        handleApplyConversationDiffRestore: aiReview.handleApplyConversationDiffRestore,
        handleReapplyConversationDiffItem: aiReview.handleReapplyConversationDiffItem,
        handleSelectConversationDiffItem: aiReview.handleSelectConversationDiffItem,
        setAIPanelVisibility: panelLayout.setAIPanelVisibility,
        setConversationDiffPanels: aiReview.setConversationDiffPanels,
        setRestorePreviewReviews: aiReview.setRestorePreviewReviews,
        showAIPanel: panelLayout.showAIPanel,
      }}
      quickCommands={{
        handleQuickCommandsOpenChange: quickCommands.handleQuickCommandsOpenChange,
        quickCmdsRef: quickCommands.quickCmdsRef,
        setShowQuickCommands: quickCommands.setShowQuickCommands,
        showQuickCommands: quickCommands.showQuickCommands,
      }}
      shared={shared}
    />
  );
}
