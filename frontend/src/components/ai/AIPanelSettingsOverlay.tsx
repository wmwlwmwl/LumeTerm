import { X } from 'lucide-react';
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Z } from '../../constants/zIndex.ts';
import { useTranslation } from '../../i18n.ts';
import MCPAccessView from './MCPAccessView.tsx';
import MCPServersView from './MCPServersView.tsx';
import AISlashCommandsSettings from './AISlashCommandsSettings.tsx';
import AIConversationBackupSettings from './AIConversationBackupSettings.tsx';
import Tiptop from '../Tiptop.tsx';
import AIPanelBasicSettingsTab from './settings/AIPanelBasicSettingsTab.tsx';
import AIPanelAppearanceSettingsTab from './settings/AIPanelAppearanceSettingsTab.tsx';

/** 全局 AI 设置（宽松结构） */
interface GlobalAISettingsLike {
  approvalButtonOrder?: string;
  commandActionButtonOrder?: string;
  messageNavEnabled?: boolean;
  autoCondenseEnabled?: boolean;
  autoCondenseThresholdRatio?: number;
  aiWorkspaceTabNumbersOnly?: boolean;
  mcpEnabled?: boolean;
  mcpAllowBrowserCalls?: boolean;
  mcpRequireApproval?: boolean;
  mcpActivityVisible?: boolean;
  continueAfterToolRejection?: boolean;
  proxyNodes?: Array<{ id?: string; name?: string; type?: string; host?: string; port?: string | number }>;
  aiRequestProxyId?: string;
  toolResultTokenThreshold?: number;
  slashCommands?: unknown;
  [key: string]: unknown;
}

export interface AIPanelSettingsOverlayProps {
  show: boolean;
  onClose: () => void;
  activeTab: string;
  onChangeTab: (tab: string) => void;
  mcpInfo: { transport?: string; url?: string; tools?: unknown[] };
  configText: string;
  configRows: number;
  globalAISettings: GlobalAISettingsLike;
  onSaveGlobalAISettings?: (settings: Record<string, unknown>) => Promise<unknown> | void;
  aiTerminalIsolation: boolean;
  onToggleAiTerminalIsolation: () => void;
  confirmDelete: boolean;
  onToggleConfirmDelete: () => void;
  activeConversationId: string;
  conversationUpdatedAt: number;
  backupRequestInFlight: boolean;
  onRestoreConversationBackup: (snapshot: unknown) => Promise<unknown> | void;
  autoBackupEnabled: boolean;
  onToggleAutoBackup: () => void;
  autoBackupRetentionCount: number;
  onChangeAutoBackupRetentionCount: (value: number) => void;
  soundEnabled?: boolean;
  soundVolume?: number;
  terminalOutputLineLimit: number;
  onTerminalOutputLineLimitChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
  terminalOutputCharacterLimit: number;
  onTerminalOutputCharacterLimitChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
  mcpClientServers?: unknown[];
  mcpClientGlobalConfigPath?: string;
  mcpClientGlobalConfigText?: string;
  mcpEmbeddedFirecrawlApiKey?: string;
  onSaveMCPEmbeddedFirecrawlApiKey?: (apiKey: string) => Promise<unknown>;
  onSaveMCPGlobalServer?: (name: string, configText: string) => Promise<unknown>;
  onReloadMCPGlobalServers?: () => Promise<unknown>;
  onDeleteMCPGlobalServer?: (name: string) => Promise<unknown>;
  onRestartMCPClientServer?: (name: string, source: string) => Promise<unknown>;
  onToggleMCPClientServer?: (name: string, source: string, enabled: boolean) => Promise<unknown>;
  onToggleMCPClientServerDisabledForPrompts?: (name: string, source: string, disabled: boolean) => Promise<unknown>;
  onToggleMCPClientServerToolDisabledForPrompts?: (name: string, source: string, toolName: string, disabled: boolean) => Promise<unknown>;
  onUpdateMCPClientServerTimeout?: (name: string, source: string, timeout: number) => Promise<unknown>;
  onMigratingChange?: (migrating: boolean) => void;
}

export default function AIPanelSettingsOverlay({
  show,
  onClose,
  activeTab,
  onChangeTab,
  mcpInfo,
  configText,
  configRows,
  globalAISettings,
  onSaveGlobalAISettings,
  aiTerminalIsolation,
  onToggleAiTerminalIsolation,
  confirmDelete,
  onToggleConfirmDelete,
  activeConversationId,
  conversationUpdatedAt,
  backupRequestInFlight,
  onRestoreConversationBackup,
  autoBackupEnabled,
  onToggleAutoBackup,
  autoBackupRetentionCount,
  onChangeAutoBackupRetentionCount,
  soundEnabled,
  soundVolume,
  terminalOutputLineLimit,
  onTerminalOutputLineLimitChange,
  terminalOutputCharacterLimit: _terminalOutputCharacterLimit,
  onTerminalOutputCharacterLimitChange: _onTerminalOutputCharacterLimitChange,
  mcpClientServers = [],
  mcpClientGlobalConfigPath = '',
  mcpClientGlobalConfigText = '',
  mcpEmbeddedFirecrawlApiKey = '',
  onSaveMCPEmbeddedFirecrawlApiKey,
  onSaveMCPGlobalServer,
  onReloadMCPGlobalServers,
  onDeleteMCPGlobalServer,
  onRestartMCPClientServer,
  onToggleMCPClientServer,
  onToggleMCPClientServerDisabledForPrompts,
  onToggleMCPClientServerToolDisabledForPrompts,
  onUpdateMCPClientServerTimeout,
  onMigratingChange,
}: AIPanelSettingsOverlayProps) {
  const { t } = useTranslation();
  const overlayRef = useRef<HTMLDivElement | null>(null);
  const tabListRef = useRef<HTMLDivElement | null>(null);
  const [overlayBounds, setOverlayBounds] = useState<{ top: number; left: number; width: number; height: number } | null>(null);

  const [tasksDir, setTasksDir] = useState('');
  const [isCustomTasksDir, setIsCustomTasksDir] = useState(false);
  const [tasksDirMigrating, setTasksDirMigrating] = useState(false);

  const refreshTasksDir = useCallback(async () => {
    try {
      const [dir, isCustom] = await Promise.all([
        window?.go?.wailsapp?.App?.GetTasksDir?.(),
        window?.go?.wailsapp?.App?.IsCustomTasksDir?.(),
      ]);
      if (typeof dir === 'string') setTasksDir(dir);
      if (typeof isCustom === 'boolean') setIsCustomTasksDir(isCustom);
    } catch { /* ignore */ }
  }, []);

  useEffect(() => { void refreshTasksDir(); }, [refreshTasksDir]);

  useEffect(() => { onMigratingChange?.(tasksDirMigrating); }, [tasksDirMigrating, onMigratingChange]);

  const handleChangeTasksDir = async () => {
    if (tasksDirMigrating) return;
    try {
      const selected = await window?.go?.wailsapp?.App?.SelectTasksDirectory?.();
      if (!selected) return;
      setTasksDirMigrating(true);
      await window?.go?.wailsapp?.App?.MigrateAITasksDir?.(selected);
      await refreshTasksDir();
      window.lumeDialog?.alert?.(t('AI 对话数据已迁移到新目录。'), t('提示'), { priority: 'settings' });
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e || '');
      if (msg.trim()) window.lumeDialog?.alert?.(msg, t('错误'), { priority: 'settings' });
      await refreshTasksDir();
    } finally {
      setTasksDirMigrating(false);
    }
  };

  // AI 对话完整日志(ai.log)在文件管理器中定位,便于用户直接把文件提供出来排查。
  const handleRevealAIDebugLog = async () => {
    try {
      const bridge = window?.go?.wailsapp?.AIBindings || window?.go?.wailsapp?.App;
      if (!bridge?.RevealAIDebugLog) {
        window.lumeDialog?.alert?.(t('定位 AI 日志能力未就绪'), t('错误'), { priority: 'settings' });
        return;
      }
      await bridge.RevealAIDebugLog();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e || '');
      if (msg.trim()) window.lumeDialog?.alert?.(msg, t('错误'), { priority: 'settings' });
    }
  };

  const handleResetTasksDir = async () => {
    if (tasksDirMigrating) return;
    try {
      const ok = await window.lumeDialog?.confirm?.(
        t('恢复为默认目录？数据将自动迁移到默认目录。')
      );
      if (!ok) return;
      setTasksDirMigrating(true);
      await window?.go?.wailsapp?.App?.ResetTasksDir?.();
      await refreshTasksDir();
      window.lumeDialog?.alert?.(t('AI 对话数据已迁移到默认目录。'), t('提示'), { priority: 'settings' });
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e || '');
      if (msg.trim()) window.lumeDialog?.alert?.(msg, t('错误'), { priority: 'settings' });
      await refreshTasksDir();
    } finally {
      setTasksDirMigrating(false);
    }
  };

  useLayoutEffect(() => {
    if (!show || activeTab) return undefined;
    const firstTabKey = (tabListRef.current?.querySelector('[data-ai-settings-tab-key]') as HTMLElement | null | undefined)?.dataset?.aiSettingsTabKey || '';
    if (firstTabKey) {
      onChangeTab(firstTabKey);
    }
    return undefined;
  }, [activeTab, onChangeTab, show]);

  useEffect(() => {
    if (!show) return undefined;

    const updateOverlayBounds = () => {
      const root = overlayRef.current?.closest('[data-ai-panel-root="true"]');
      const chatStage = root?.querySelector('[data-ai-chat-stage="true"]');
      const composer = root?.querySelector('[data-ai-composer-root="true"]');

      if (!root || (!chatStage && !composer)) {
        setOverlayBounds(null);
        return;
      }

      const rootRect = root.getBoundingClientRect();
      const chatRect = chatStage?.getBoundingClientRect();
      const composerRect = composer?.getBoundingClientRect();

      const top = Math.min(chatRect?.top ?? rootRect.top, composerRect?.top ?? rootRect.top);
      const left = Math.min(chatRect?.left ?? rootRect.left, composerRect?.left ?? rootRect.left);
      const right = Math.max(chatRect?.right ?? rootRect.right, composerRect?.right ?? rootRect.right);
      const bottom = Math.max(chatRect?.bottom ?? rootRect.bottom, composerRect?.bottom ?? rootRect.bottom);

      setOverlayBounds({
        top: top - rootRect.top,
        left: left - rootRect.left,
        width: right - left,
        height: bottom - top,
      });
    };

    updateOverlayBounds();

    const rootEl = overlayRef.current?.closest('[data-ai-panel-root="true"]');
    const resizeObserver = rootEl ? new ResizeObserver(updateOverlayBounds) : null;
    if (resizeObserver && rootEl) {
      resizeObserver.observe(rootEl);
    }

    window.addEventListener('resize', updateOverlayBounds);
    window.addEventListener('scroll', updateOverlayBounds, true);

    return () => {
      resizeObserver?.disconnect();
      window.removeEventListener('resize', updateOverlayBounds);
      window.removeEventListener('scroll', updateOverlayBounds, true);
    };
  }, [show]);

  if (!show) {
    return null;
  }

  const approvalButtonOrder = globalAISettings?.approvalButtonOrder || 'reject-approve';
  const commandActionButtonOrder = globalAISettings?.commandActionButtonOrder || 'terminate-continue';
  const messageNavEnabled = globalAISettings?.messageNavEnabled !== false;
  const autoCondenseEnabled = globalAISettings?.autoCondenseEnabled === true;
  const autoCondenseThresholdRatio = globalAISettings?.autoCondenseThresholdRatio ?? 0.8;
  const aiWorkspaceTabNumbersOnly = globalAISettings?.aiWorkspaceTabNumbersOnly === true;
  const mcpEnabled = globalAISettings?.mcpEnabled !== false;
  const mcpAllowBrowserCalls = Boolean(globalAISettings?.mcpAllowBrowserCalls);
  const mcpRequireApproval = Boolean(globalAISettings?.mcpRequireApproval);
  const mcpActivityVisible = Boolean(globalAISettings?.mcpActivityVisible);
  const continueAfterToolRejection = globalAISettings?.continueAfterToolRejection !== false;
  const proxyNodes = Array.isArray(globalAISettings?.proxyNodes) ? globalAISettings.proxyNodes : [];
  const aiRequestProxyId = typeof globalAISettings?.aiRequestProxyId === 'string' ? globalAISettings.aiRequestProxyId : '';
  const toolResultTokenThreshold = Number.isFinite(Number(globalAISettings?.toolResultTokenThreshold))
    ? Math.max(1, Math.trunc(Number(globalAISettings.toolResultTokenThreshold)))
    : 350000;

  return (
    <div
      ref={overlayRef}
      className="absolute max-w-full max-h-full bg-scrim/90 backdrop-blur-[4px] flex items-stretch justify-center overflow-hidden"
      style={{
        zIndex: Z.POPOVER,
        top: overlayBounds?.top ?? 0,
        left: overlayBounds?.left ?? 0,
        width: overlayBounds?.width ?? '100%',
        height: overlayBounds?.height ?? '100%',
      }}>
      <div className="w-full h-full bg-overlay border border-line rounded-none shadow-xl overflow-hidden flex flex-col" onClick={(e) => e.stopPropagation()}>
        <div className="h-[50px] px-4 flex items-center justify-between border-b border-line shrink-0">
          <div className="text-md font-bold text-primary">{t('设置')}</div>
          <Tiptop text={t('关闭设置面板')}>
            <button
              type="button"
              onClick={onClose}
              disabled={tasksDirMigrating}
              aria-label={t('关闭设置面板')}
              className={`w-[30px] h-[30px] inline-flex items-center justify-center rounded-lg text-secondary bg-transparent border border-transparent transition-colors duration-[80ms] ${tasksDirMigrating ? 'opacity-40 cursor-not-allowed' : 'cursor-pointer'}`}
            >
              <X size={16} />
            </button>
          </Tiptop>
        </div>
        <div className="flex-1 min-h-0 flex overflow-hidden">
          <div ref={tabListRef} className="w-fit border-r border-line bg-canvas p-0 gap-0 flex flex-col shrink-0">
            {(
              [
                ['ai', t('基本')],
                ['mcp', t('MCP集成')],
                ['mcp-servers', t('MCP服务器')],
                ['slash-commands', t('斜杠命令')],
                ['appearance', t('外观')],
              ] as Array<[string, string]>
            ).map(([tabKey, tabLabel]) => (
              <button
                key={tabKey}
                type="button"
                data-ai-settings-tab-key={tabKey}
                onClick={() => onChangeTab(tabKey)}
                className={`flex items-center justify-start min-h-[52px] px-2.5 text-left text-base whitespace-nowrap w-full border-0 border-l-2 rounded-none transition-colors duration-[80ms] cursor-pointer ${
                  activeTab === tabKey
                    ? 'font-semibold text-primary bg-[rgba(var(--accent-rgb),0.10)] border-l-accent'
                    : 'font-medium text-secondary bg-transparent border-l-transparent'
                }`}
              >
                <span>{tabLabel}</span>
              </button>
            ))}
            {activeConversationId ? (
              <button
                type="button"
                onClick={() => onChangeTab('backup')}
                className={`flex items-center justify-start min-h-[52px] px-2.5 text-left text-base whitespace-nowrap w-full border-0 border-l-2 rounded-none transition-colors duration-[80ms] cursor-pointer ${
                  activeTab === 'backup'
                    ? 'font-semibold text-primary bg-[rgba(var(--accent-rgb),0.10)] border-l-accent'
                    : 'font-medium text-secondary bg-transparent border-l-transparent'
                }`}
              >
                <span>{t('自动备份')}</span>
              </button>
            ) : null}
          </div>
          <div className="flex-1 min-w-0 overflow-y-auto px-5 py-[18px] flex flex-col gap-3.5">
            {activeTab === 'mcp' && (
              <MCPAccessView
                mcpInfo={mcpInfo as Parameters<typeof MCPAccessView>[0]['mcpInfo']}
                configText={configText}
                configRows={configRows}
                title={t('接入方式')}
                titleSize={18}
                showTools={true}
                mcpEnabled={mcpEnabled}
                mcpAllowBrowserCalls={mcpAllowBrowserCalls}
                mcpRequireApproval={mcpRequireApproval}
                mcpActivityVisible={mcpActivityVisible}
                onToggleMcpEnabled={() => onSaveGlobalAISettings?.({ mcpEnabled: !mcpEnabled })}
                onToggleMcpAllowBrowserCalls={() => onSaveGlobalAISettings?.({ mcpAllowBrowserCalls: !mcpAllowBrowserCalls })}
                onToggleMcpRequireApproval={() => onSaveGlobalAISettings?.(mcpRequireApproval
                  ? { mcpRequireApproval: false }
                  : { mcpRequireApproval: true, mcpActivityVisible: true })}
                onToggleMcpActivityVisible={() => onSaveGlobalAISettings?.({ mcpActivityVisible: !mcpActivityVisible })}
              />
            )}
            {activeTab === 'mcp-servers' ? (
              <MCPServersView
                servers={mcpClientServers as Parameters<typeof MCPServersView>[0]['servers']}
                globalConfigPath={mcpClientGlobalConfigPath}
                globalConfigText={mcpClientGlobalConfigText}
                embeddedFirecrawlApiKey={mcpEmbeddedFirecrawlApiKey}
                onSaveEmbeddedFirecrawlApiKey={onSaveMCPEmbeddedFirecrawlApiKey}
                onSaveServer={onSaveMCPGlobalServer}
                onReloadServers={onReloadMCPGlobalServers}
                onDeleteServer={onDeleteMCPGlobalServer}
                onRestartServer={onRestartMCPClientServer}
                onToggleServer={onToggleMCPClientServer}
                onToggleServerDisabledForPrompts={onToggleMCPClientServerDisabledForPrompts}
                onToggleToolDisabledForPrompts={onToggleMCPClientServerToolDisabledForPrompts}
                onUpdateServerTimeout={onUpdateMCPClientServerTimeout}
              />
            ) : null}
            {activeTab === 'ai' ? (
              <AIPanelBasicSettingsTab
                aiTerminalIsolation={aiTerminalIsolation}
                onToggleAiTerminalIsolation={onToggleAiTerminalIsolation}
                confirmDelete={confirmDelete}
                onToggleConfirmDelete={onToggleConfirmDelete}
                continueAfterToolRejection={continueAfterToolRejection}
                soundEnabled={soundEnabled}
                soundVolume={soundVolume}
                terminalOutputLineLimit={terminalOutputLineLimit}
                onTerminalOutputLineLimitChange={onTerminalOutputLineLimitChange}
                toolResultTokenThreshold={toolResultTokenThreshold}
                autoCondenseEnabled={autoCondenseEnabled}
                autoCondenseThresholdRatio={autoCondenseThresholdRatio}
                aiRequestProxyId={aiRequestProxyId}
                proxyNodes={proxyNodes}
                tasksDir={tasksDir}
                isCustomTasksDir={isCustomTasksDir}
                tasksDirMigrating={tasksDirMigrating}
                handleChangeTasksDir={handleChangeTasksDir}
                handleResetTasksDir={handleResetTasksDir}
                handleRevealAIDebugLog={handleRevealAIDebugLog}
                aiDebugLogEnabled={globalAISettings?.aiDebugLogEnabled !== false}
                onSaveGlobalAISettings={onSaveGlobalAISettings}
              />
            ) : null}
            {activeTab === 'slash-commands' ? (
              <AISlashCommandsSettings
                slashCommands={globalAISettings?.slashCommands}
                onSaveGlobalAISettings={onSaveGlobalAISettings as Parameters<typeof AISlashCommandsSettings>[0]['onSaveGlobalAISettings']}
              />
            ) : null}
            {activeTab === 'appearance' ? (
              <AIPanelAppearanceSettingsTab
                approvalButtonOrder={approvalButtonOrder}
                commandActionButtonOrder={commandActionButtonOrder}
                messageNavEnabled={messageNavEnabled}
                aiWorkspaceTabNumbersOnly={aiWorkspaceTabNumbersOnly}
                onSaveGlobalAISettings={onSaveGlobalAISettings}
              />
            ) : null}
            {activeTab === 'backup' && activeConversationId ? (
              <AIConversationBackupSettings
                active={activeTab === 'backup'}
                conversationId={activeConversationId}
                conversationUpdatedAt={conversationUpdatedAt}
                requestInFlight={backupRequestInFlight}
                onRestoreSnapshot={onRestoreConversationBackup}
                autoBackupEnabled={autoBackupEnabled}
                onToggleAutoBackup={onToggleAutoBackup}
                autoBackupRetentionCount={autoBackupRetentionCount}
                onChangeAutoBackupRetentionCount={onChangeAutoBackupRetentionCount}
              />
            ) : null}
          </div>
        </div>
      </div>
    </div>
  );
}
