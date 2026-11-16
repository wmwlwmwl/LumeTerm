import { lazy, Suspense } from 'react';
import GlobalContextMenu from './GlobalContextMenu.tsx';
import GlobalDialog from './GlobalDialog.tsx';
import SyncFailureToast from './SyncFailureToast.tsx';
import Toast from './Toast.tsx';
import EditFlyLayer from './overlays/EditFlyLayer.tsx';
import TerminalTabContextMenuOverlay from './overlays/TerminalTabContextMenuOverlay.tsx';
import TabContextMenuOverlay from './overlays/TabContextMenuOverlay.tsx';
import SessionListOverlay from './overlays/SessionListOverlay.tsx';
import type { AppOverlaysProps } from './overlays/overlayTypes.ts';

// 懒加载低频/重型模态弹窗，避免在启动首屏时全量解析其依赖树
const ImportExportDialog = lazy(() => import('./ImportExportDialog.tsx'));
const ExportSelectedDialog = lazy(() => import('./ExportSelectedDialog.tsx'));
const SerialConfigModal = lazy(() => import('./SerialConfigModal.tsx'));
const PortForwardDialog = lazy(() => import('./PortForwardDialog.tsx'));
const SettingsModal = lazy(() => import('./SettingsModal.tsx'));
const CredentialsModal = lazy(() => import('./CredentialsModal.tsx'));
const UpdateModal = lazy(() => import('./UpdateModal.tsx'));

export type { AppOverlaysProps, TabContextMenuState, TerminalTabContextMenuState } from './overlays/overlayTypes.ts';

export default function AppOverlays({ dialogs, importExport, notifications, menus, animation, shared }: AppOverlaysProps) {
  const {
    activeSessionId,
    addToast,
    canCopySessionPassword,
    canMoveTerminalToDockTarget,
    closeAllSessions,
    closePortForwardDialog,
    closeSession,
    closeTerminal,
    closeTerminalGroup,
    connectSerial,
    downloadProgress,
    editFlyAnimation,
    editorModeBanner,
    exportSelectedIds,
    forceCloseSession,
    handleApplyStartupUpdate,
    handleCopySessionPassword,
    handleDownloadTemplate,
    handleExport,
    handleExportSelected,
    handleImport,
    handleRenameTerminalTab,
    handleTabClick,
    handleToastAction,
    hasRecoveryPassword,
    ieBusy,
    isTerminalDockTargetOccupied,
    isUpdateModalVisible,
    loadServers,
    moveTerminalToDockTarget,
    portForwardDialogSessionId,
    portForwardInitialMapping,
    portForwardInitialTab,
    probePanelPosition,
    removeToast,
    sessionAuthPrompts,
    sessionListPos,
    sessionListQuery,
    sessionListRef,
    servers,
    connectServer,
    sessions,
    setExportSelectedIds,
    setIsUpdateModalVisible,
    setProbePanelPosition,
    setSessionListQuery,
    setSettingsInitialTab,
    setShowCredentials,
    setShowExportSelectedDialog,
    setShowImportExportDialog,
    setShowSerialModal,
    setShowSessionList,
    setShowSettings,
    setSyncFailed,
    setTabContextMenu,
    setTerminalTabContextMenu,
    settingsInitialTab,
    showCredentials,
    showExportSelectedDialog,
    showImportExportDialog,
    showPortForwardDialog,
    showSerialModal,
    showSessionList,
    showSettings,
    startupUpdateInfo,
    syncFailed,
    t,
    tabContextMenu,
    terminalTabContextMenu,
    toasts,
  } = { ...dialogs, ...importExport, ...notifications, ...menus, ...animation, ...shared };
  return (<>
      {showImportExportDialog && (
        <Suspense fallback={null}>
          <ImportExportDialog
            onClose={() => setShowImportExportDialog(false)}
            onExport={handleExport}
            onImport={handleImport}
            onDownloadTemplate={handleDownloadTemplate}
            hasRecoveryPassword={hasRecoveryPassword}
            busy={ieBusy}
          />
        </Suspense>
      )}

      {showExportSelectedDialog && (
        <Suspense fallback={null}>
          <ExportSelectedDialog
            onClose={() => {
              setShowExportSelectedDialog(false);
              setExportSelectedIds([]);
            }}
            onExport={handleExportSelected}
            hasRecoveryPassword={hasRecoveryPassword}
            busy={ieBusy}
            selectedCount={exportSelectedIds.length}
          />
        </Suspense>
      )}

      {showSerialModal && (
        <Suspense fallback={null}>
          <SerialConfigModal
            onClose={() => setShowSerialModal(false)}
            onConnect={(config) => {
              setShowSerialModal(false);
              connectSerial(config);
            }}
          />
        </Suspense>
      )}

      {showPortForwardDialog && portForwardDialogSessionId && (
        <Suspense fallback={null}>
          <PortForwardDialog
            sessionId={portForwardDialogSessionId}
            initialMapping={portForwardInitialMapping}
            initialTab={portForwardInitialTab}
            onClose={closePortForwardDialog}
          />
        </Suspense>
      )}

      {showSettings && (
        <Suspense fallback={null}>
          <SettingsModal
            initialTab={settingsInitialTab}
            onClose={() => { setShowSettings(false); loadServers(); }}
            addToast={addToast}
            onRestored={loadServers}
            probePanelPosition={probePanelPosition}
            onProbePanelPositionChange={setProbePanelPosition}
          />
        </Suspense>
      )}

      {showCredentials && (
        <Suspense fallback={null}>
          <CredentialsModal
            onClose={() => { setShowCredentials(false); loadServers(); }}
            onChange={loadServers}
            addToast={addToast}
          />
        </Suspense>
      )}

      {editFlyAnimation && (
        <EditFlyLayer editFlyAnimation={editFlyAnimation} />
      )}

      {editorModeBanner && (
        <div className="editor-mode-banner" key={editorModeBanner.id} aria-live="polite">
          {editorModeBanner.text}
        </div>
      )}

      {/* ── Toasts ────────────────────────────────────────── */}
      <Toast toasts={toasts} onClose={removeToast} onAction={handleToastAction} closeLabel={t('关闭')} />
      <GlobalDialog suspendDefault={showSettings} />


      {/* ── 自动更新弹窗 ──────────────────────────────── */}
      {isUpdateModalVisible && (
        <Suspense fallback={null}>
          <UpdateModal
            visible={isUpdateModalVisible}
            updateInfo={startupUpdateInfo}
            downloadProgress={downloadProgress}
            t={t}
            onClose={() => setIsUpdateModalVisible(false)}
            onUpdate={handleApplyStartupUpdate}
          />
        </Suspense>
      )}

      <SyncFailureToast
        syncFailed={syncFailed}
        setSyncFailed={setSyncFailed}
        setSettingsInitialTab={setSettingsInitialTab}
        setShowSettings={setShowSettings}
        addToast={addToast}
        t={t}
      />

      <GlobalContextMenu />

      {/* ── 终端子标签右键菜单 ── */}
      {terminalTabContextMenu && (
        <TerminalTabContextMenuOverlay
          terminalTabContextMenu={terminalTabContextMenu}
          sessions={sessions}
          t={t}
          isTerminalDockTargetOccupied={isTerminalDockTargetOccupied}
          canMoveTerminalToDockTarget={canMoveTerminalToDockTarget}
          moveTerminalToDockTarget={moveTerminalToDockTarget}
          setTerminalTabContextMenu={setTerminalTabContextMenu}
          handleRenameTerminalTab={handleRenameTerminalTab}
          closeTerminalGroup={closeTerminalGroup}
          closeTerminal={closeTerminal}
        />
      )}

      {/* ── 标签右键菜单 ── */}
      {tabContextMenu && (
        <TabContextMenuOverlay
          tabContextMenu={tabContextMenu}
          sessions={sessions}
          servers={servers}
          t={t}
          addToast={addToast}
          canCopySessionPassword={canCopySessionPassword}
          setTabContextMenu={setTabContextMenu}
          handleCopySessionPassword={handleCopySessionPassword}
          forceCloseSession={forceCloseSession}
          closeAllSessions={closeAllSessions}
        />
      )}
      {/* ── 服务器列表下拉 ── */}
      {showSessionList && (
        <SessionListOverlay
          sessionListRef={sessionListRef}
          sessionListPos={sessionListPos}
          sessionListQuery={sessionListQuery}
          setSessionListQuery={setSessionListQuery}
          t={t}
          sessions={sessions}
          servers={servers}
          connectServer={connectServer}
          activeSessionId={activeSessionId}
          sessionAuthPrompts={sessionAuthPrompts}
          handleTabClick={handleTabClick}
          setShowSessionList={setShowSessionList}
          closeSession={closeSession}
        />
      )}
  </>);
}
