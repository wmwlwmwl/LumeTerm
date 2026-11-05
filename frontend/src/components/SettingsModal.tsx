import { useState, useEffect, useCallback } from 'react';
import { getAvailableLanguages, setLanguage as setGlobalLanguage, t as $t, type LanguageCode } from '../i18n.ts';
import { APP_BUILD_TIME, APP_VERSION } from '../config.ts';
import { formatUpdateError, useUpdateChecker, type UpdateCheckResult } from '../hooks/useUpdateChecker.ts';
import { X } from 'lucide-react';
import { Button, Modal } from './ui';
import AppTab from './settings/AppTab';
import GeneralTab from './settings/GeneralTab';
import NetworkTab from './settings/NetworkTab';
import AppearanceTabPane from './settings/appearance/AppearanceTabPane';
import FileManagerTabPane from './settings/fileManager/FileManagerTabPane';
import RuntimeEnvironmentTab from './settings/RuntimeEnvironmentTab';
import ShortcutsTab from './settings/ShortcutsTab';
import SyncTabPane from './settings/sync/SyncTabPane';
import SettingsSidebar from './settings/SettingsSidebar';
import { useSettingsGeneralState } from './settings/useSettingsGeneralState';
import { useSettingsSearch } from './settings/useSettingsSearch';
import { useSettingsShortcuts } from './settings/useSettingsShortcuts';

const AVAILABLE_LANGUAGES = getAvailableLanguages();

export interface SettingsModalProps {
  onClose: () => void;
  addToast: (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;
  onRestored?: () => void;
  probePanelPosition: 'left' | 'right';
  onProbePanelPositionChange: (pos: 'left' | 'right') => void;
  forceDarkTheme?: boolean;
  initialTab?: string;
}

export default function SettingsModal({
  onClose,
  addToast,
  onRestored,
  probePanelPosition,
  onProbePanelPositionChange,
  forceDarkTheme = false,
  initialTab = 'general',
}: SettingsModalProps) {
  const CURRENT_VERSION = APP_VERSION;
  const CURRENT_BUILD_TIME = APP_BUILD_TIME;
  const [updateInfo, setUpdateInfo] = useState<UpdateCheckResult | null>(null);

  const { checking: checkingUpdate, downloadProgress, checkUpdate, applyUpdate } = useUpdateChecker({
    onResult: (result) => {
      if (result.hasUpdate) {
        setUpdateInfo({
          hasUpdate: true,
          latestVersion: 'v' + result.latestVersion,
          url: result.url,
          filename: result.filename,
          assetReady: result.assetReady,
          reason: result.reason,
        });
        addToast($t('发现新版本: v') + result.latestVersion, 'success');
        return;
      }
      setUpdateInfo(null);
      // 远端 tag 已更新但本平台安装包尚未上传（如 Windows 仍在打包）
      if (result?.reason === 'asset_pending') {
        addToast($t('新版本安装包尚未就绪，请稍后再试'), 'info');
        return;
      }
      addToast($t('当前已是最新版本'), 'info');
    },
    onError: (err) => {
      const message = err instanceof Error ? err.message : String(err ?? '');
      addToast($t('检查更新失败: ') + message, 'error');
    },
  });

  const handleCheckUpdate = () => { checkUpdate(); };

  const handleApplyUpdate = () => {
    applyUpdate(updateInfo).catch((err) => {
      addToast($t('更新失败: ') + formatUpdateError(err), 'error');
    });
  };

  const [activeTab, setActiveTab] = useState(initialTab || 'general');

  // Recovery password (for cloud backup restore fallback)
  const [recoveryPasswordEditing, setRecoveryPasswordEditing] = useState(false);
  const [recoveryPasswordInput, setRecoveryPasswordInput] = useState('');
  // 恢复失败时的密码兜底
  const [restoreWithPassword, setRestoreWithPassword] = useState(false);
  const [restorePasswordInput, setRestorePasswordInput] = useState('');

  const handleClose = useCallback(() => {
    setRecoveryPasswordInput('');
    setRecoveryPasswordEditing(false);
    setRestorePasswordInput('');
    setRestoreWithPassword(false);
    onClose();
  }, [onClose]);

  // Sync provider selection
  const [syncProvider, setSyncProvider] = useState('webdav');

  // Network/Ping state
  const [pingEnabled, setPingEnabled] = useState(localStorage.getItem('pingEnabled') !== 'false');
  const [probeInterval, setProbeInterval] = useState(parseInt(localStorage.getItem('probeInterval') || '3', 10));
  const [pingInterval, setPingInterval] = useState(parseInt(localStorage.getItem('pingInterval') || '2', 10));
  const [pingMode, setPingMode] = useState(localStorage.getItem('pingMode') || 'auto');

  const [language, setLanguage] = useState(localStorage.getItem('appLanguage') || 'zh-CN');
  // Shortcuts state
  const { shortcuts, listeningKey, setListeningKey, handleResetShortcuts } = useSettingsShortcuts({ handleClose, addToast });

  useEffect(() => {
    if (typeof initialTab === 'string' && initialTab.trim()) {
      setActiveTab(initialTab.trim())
    }
  }, [initialTab])

  const {
    terminalRightClickPasteOnEmpty,
    terminalRightClickPasteMode,
    terminalLeftClickCopyOnSelection,
    terminalLeftClickCopyOnSelectionMode,
    terminalTabDoubleClickActionEnabled,
    terminalTabDoubleClickAction,
    confirmCloseSession,
    confirmCloseAll,
    confirmFileDelete,
    confirmProcessKill,
    confirmGitDiscard,
    confirmGitForcePush,
    confirmGitUntrack,
    confirmTerminalSelectionPaste,
    confirmTerminalMultilinePaste,
    windowCloseAction,
    updateUseProxy,
    rememberWorkspace,
    workspacePersistenceLevel,
    webviewGpuDisabled,
    supportsWebviewGpuDisable,
    handleTerminalRightClickPasteOnEmptyChange,
    handleTerminalRightClickPasteModeChange,
    handleTerminalLeftClickCopyOnSelectionChange,
    handleTerminalLeftClickCopyOnSelectionModeChange,
    handleTerminalTabDoubleClickActionEnabledChange,
    handleTerminalTabDoubleClickActionChange,
    handleToggleConfirmCloseSession,
    handleToggleConfirmCloseAll,
    handleToggleConfirmFileDelete,
    handleToggleConfirmProcessKill,
    handleToggleConfirmGitDiscard,
    handleToggleConfirmGitForcePush,
    handleToggleConfirmGitUntrack,
    handleToggleConfirmTerminalSelectionPaste,
    handleToggleConfirmTerminalMultilinePaste,
    handleWindowCloseActionChange,
    handleToggleUpdateUseProxy,
    handleToggleRememberWorkspace,
    handleWorkspacePersistenceLevelChange,
    handleToggleWebviewGpuDisabled,
  } = useSettingsGeneralState(addToast);

  const {
    settingsSearchQuery,
    setSettingsSearchQuery,
    settingsSearchResults,
    handleSelectSettingsSearchResult,
  } = useSettingsSearch({
    language,
    supportsWebviewGpuDisable,
    activeTab,
    syncProvider,
    setActiveTab,
    setSyncProvider,
  });

  const handleLanguageChange = async (e: React.ChangeEvent<HTMLSelectElement>) => {
    const lang = e.target.value;
    setLanguage(lang);
    // 选项值即语言代码，setGlobalLanguage 期待 LanguageCode
    await setGlobalLanguage(lang as LanguageCode);
  };

  // ── Tab prop wrappers ──
  const handleTogglePingEnabled = () => {
    const next = !pingEnabled;
    setPingEnabled(next);
    localStorage.setItem('pingEnabled', String(next));
    window.dispatchEvent(new Event('pingEnabledChanged'));
  };
  const handleProbeIntervalChange = (s: number) => { setProbeInterval(s); localStorage.setItem('probeInterval', String(s)); window.dispatchEvent(new Event('probeIntervalChanged')); };
  const handlePingIntervalChange = (s: number) => {
    // Banner 模式半开 SSH 成本更高：不允许低于 15s，避免短时间多次登录失败类告警。
    const next = pingMode === 'banner' ? Math.max(15, Number(s) || 15) : s;
    setPingInterval(next);
    localStorage.setItem('pingInterval', String(next));
    window.dispatchEvent(new Event('pingIntervalChanged'));
  };
  const handlePingModeChange = (mode: string) => {
    setPingMode(mode);
    localStorage.setItem('pingMode', mode);
    window.dispatchEvent(new Event('pingModeChanged'));
    // 用户选择强制 Banner 时，自动把延迟检测间隔抬到至少 15s。
    if (mode === 'banner') {
      const current = parseInt(localStorage.getItem('pingInterval') || String(pingInterval) || '2', 10);
      if (!Number.isFinite(current) || current < 15) {
        const next = 15;
        setPingInterval(next);
        localStorage.setItem('pingInterval', String(next));
        window.dispatchEvent(new Event('pingIntervalChanged'));
      }
    }
  };


  return (
    <Modal open onClose={handleClose} hideClose closeOnOverlay={false} size="xl" panelClassName="flex flex-col h-[80vh]" bodyClassName="p-0 gap-0 flex-1 min-h-0">

        {/* Settings Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-line-subtle">
          <div className="text-md font-semibold text-primary">{$t('设置')}</div>
          <Button variant="ghost" size="icon" onClick={handleClose}><X size={16} /></Button>
        </div>

        {/* Settings Body Layout */}
        <div className="flex flex-1 overflow-hidden">

          {/* Settings Sidebar */}
          <SettingsSidebar
            settingsSearchQuery={settingsSearchQuery}
            setSettingsSearchQuery={setSettingsSearchQuery}
            settingsSearchResults={settingsSearchResults}
            activeTab={activeTab}
            setActiveTab={setActiveTab}
            handleSelectSettingsSearchResult={handleSelectSettingsSearchResult}
          />

          {/* Settings Content */}
          {/* 原 <style> 注入的 [data-settings-highlight] 高亮规则，改为作用域工具类（仅搜索跳转目标会带该属性，均在本面板内） */}
          <div
            className="settings-content-pane
              [&_[data-settings-highlight=true]]:outline [&_[data-settings-highlight=true]]:outline-2 [&_[data-settings-highlight=true]]:outline-accent [&_[data-settings-highlight=true]]:shadow-[0_0_0_3px_rgba(var(--accent-rgb),0.18)] [&_[data-settings-highlight=true]]:rounded-md"
          >
            
            {activeTab === 'app' && (
              <AppTab
                CURRENT_VERSION={CURRENT_VERSION}
                BUILD_TIME={CURRENT_BUILD_TIME}
                updateInfo={updateInfo}
                checkingUpdate={checkingUpdate}
                downloadProgress={downloadProgress}
                onCheckUpdate={handleCheckUpdate}
                onApplyUpdate={handleApplyUpdate}
              />
            )}

            {activeTab === 'general' && (
              <GeneralTab
                language={language}
                onLanguageChange={handleLanguageChange}
                availableLanguages={AVAILABLE_LANGUAGES}
                confirmCloseSession={confirmCloseSession}
                onToggleConfirmCloseSession={handleToggleConfirmCloseSession}
                confirmCloseAll={confirmCloseAll}
                onToggleConfirmCloseAll={handleToggleConfirmCloseAll}
                confirmFileDelete={confirmFileDelete}
                onToggleConfirmFileDelete={handleToggleConfirmFileDelete}
                confirmProcessKill={confirmProcessKill}
                onToggleConfirmProcessKill={handleToggleConfirmProcessKill}
                confirmGitDiscard={confirmGitDiscard}
                onToggleConfirmGitDiscard={handleToggleConfirmGitDiscard}
                confirmGitForcePush={confirmGitForcePush}
                onToggleConfirmGitForcePush={handleToggleConfirmGitForcePush}
                confirmGitUntrack={confirmGitUntrack}
                onToggleConfirmGitUntrack={handleToggleConfirmGitUntrack}
                confirmTerminalSelectionPaste={confirmTerminalSelectionPaste}
                onToggleConfirmTerminalSelectionPaste={handleToggleConfirmTerminalSelectionPaste}
                confirmTerminalMultilinePaste={confirmTerminalMultilinePaste}
                onToggleConfirmTerminalMultilinePaste={handleToggleConfirmTerminalMultilinePaste}
                windowCloseAction={windowCloseAction}
                onWindowCloseActionChange={handleWindowCloseActionChange}
                updateUseProxy={updateUseProxy}
                onToggleUpdateUseProxy={handleToggleUpdateUseProxy}
                rememberWorkspace={rememberWorkspace}
                onToggleRememberWorkspace={handleToggleRememberWorkspace}
                workspacePersistenceLevel={workspacePersistenceLevel}
                onWorkspacePersistenceLevelChange={handleWorkspacePersistenceLevelChange}
                supportsWebviewGpuDisable={supportsWebviewGpuDisable}
                webviewGpuDisabled={webviewGpuDisabled}
                onToggleWebviewGpuDisabled={handleToggleWebviewGpuDisabled}
                terminalRightClickPasteOnEmpty={terminalRightClickPasteOnEmpty}
                onTerminalRightClickPasteOnEmptyChange={handleTerminalRightClickPasteOnEmptyChange}
                terminalRightClickPasteMode={terminalRightClickPasteMode}
                onTerminalRightClickPasteModeChange={handleTerminalRightClickPasteModeChange}
                terminalLeftClickCopyOnSelection={terminalLeftClickCopyOnSelection}
                onTerminalLeftClickCopyOnSelectionChange={handleTerminalLeftClickCopyOnSelectionChange}
                terminalLeftClickCopyOnSelectionMode={terminalLeftClickCopyOnSelectionMode}
                onTerminalLeftClickCopyOnSelectionModeChange={handleTerminalLeftClickCopyOnSelectionModeChange}
                terminalTabDoubleClickActionEnabled={terminalTabDoubleClickActionEnabled}
                onTerminalTabDoubleClickActionEnabledChange={handleTerminalTabDoubleClickActionEnabledChange}
                terminalTabDoubleClickAction={terminalTabDoubleClickAction}
                onTerminalTabDoubleClickActionChange={handleTerminalTabDoubleClickActionChange}
              />
            )}

            {activeTab === 'network' && (
              <NetworkTab
                pingEnabled={pingEnabled}
                onTogglePingEnabled={handleTogglePingEnabled}
                pingMode={pingMode}
                onPingModeChange={handlePingModeChange}
                probeInterval={probeInterval}
                onProbeIntervalChange={handleProbeIntervalChange}
                pingInterval={pingInterval}
                onPingIntervalChange={handlePingIntervalChange}
              />
            )}

            <FileManagerTabPane
              activeTab={activeTab}
              addToast={addToast}
            />
            {activeTab === 'runtimeEnvironment' && (
              <RuntimeEnvironmentTab />
            )}
            <AppearanceTabPane
              activeTab={activeTab}
              addToast={addToast}
              forceDarkTheme={forceDarkTheme}
              handleClose={handleClose}
              probePanelPosition={probePanelPosition}
              onProbePanelPositionChange={onProbePanelPositionChange}
            />

            {activeTab === 'shortcuts' && (
              <ShortcutsTab
                shortcuts={shortcuts}
                listeningKey={listeningKey}
                onSetListeningKey={setListeningKey}
                onResetShortcuts={handleResetShortcuts}
              />
            )}

            <SyncTabPane
              activeTab={activeTab}
              addToast={addToast}
              onRestored={onRestored}
              syncProvider={syncProvider}
              setSyncProvider={setSyncProvider}
              recoveryPasswordEditing={recoveryPasswordEditing}
              setRecoveryPasswordEditing={setRecoveryPasswordEditing}
              recoveryPasswordInput={recoveryPasswordInput}
              setRecoveryPasswordInput={setRecoveryPasswordInput}
              restoreWithPassword={restoreWithPassword}
              setRestoreWithPassword={setRestoreWithPassword}
              restorePasswordInput={restorePasswordInput}
              setRestorePasswordInput={setRestorePasswordInput}
            />

          </div>
        </div>

    </Modal>
  );
}
