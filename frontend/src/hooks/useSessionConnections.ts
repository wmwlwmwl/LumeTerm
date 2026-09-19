import { useCallback, useEffect, useRef } from 'react';
import { EventsOn, WindowHide } from '../../wailsjs/runtime/runtime.js';
import * as AppGo from '../../wailsjs/go/wailsapp/App.js';
import { warnDev } from '../utils/devLog';
import type { config } from '../../wailsjs/go/models.ts';
import type { SessionLike, WorkspaceContentTab } from '../utils/sessionWorkspace.ts';
import type { TerminalPaneLayout } from '../utils/terminalPaneLayout.ts';
import type { FileManagerWorkspaceState } from '../utils/fileWorkbench.ts';
import type { SnapshotOverrides, WorkspaceSessionSnapshot } from './useWorkspacePersistence.ts';
import type { AIWorkspaceTabGroup } from '../utils/aiWorkspaceTabs.ts';

/** 连接中的服务器卡片 */
export interface ConnectingServer {
  server: { id: string; name?: string; host: string; port?: number };
  sessionId: string;
  startTime: number;
  status?: string;
  message?: string;
}

/** 会话认证提示（主机密钥 / 密码重输） */
export interface SessionAuthPrompt {
  kind: 'hostkey' | 'password';
  token: number;
  title: string;
  message: string;
  danger?: boolean;
  connId?: string;
  checkboxLabel?: string;
}

/** SSH 通道占用统计 */
export interface SshChannelUsage {
  terminals: number;
  sharedSftp: number;
  uploadPool: number;
  total: number;
  maxSessions: number;
}

export interface UseSessionConnectionsDeps {
  activeSessionIdRef: React.RefObject<string | null>;
  activeTerminalIdRef: React.RefObject<string | null>;
  addToast: (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;
  authPromptTokenRef: React.RefObject<number>;
  awaitDisconnectTerminals: (ids: string[]) => Promise<unknown>;
  buildTerminalCloneCwdCommand: (cwd: string) => string;
  cancelledConnectionsRef: React.RefObject<Set<string>>;
  clearSessionAuthPrompt: (sessionId: string) => void;
  cloneSessionFileManagerWorkspaceState: (workspace: unknown) => FileManagerWorkspaceState | null;
  connectingServersRef: React.RefObject<ConnectingServer[]>;
  contentTabRef: React.RefObject<string>;
  creatingTerminalRef: React.RefObject<string | null>;
  credentials: config.Credential[];
  disconnectSessionConnection: (sessionId: string, terminalIds?: string[]) => Promise<unknown>;
  disconnectSessionTerminals: (ids: string[]) => Promise<unknown>;
  fileManagerPosition: string;
  getAllSessionFileManagerWorkspaces: () => Record<string, FileManagerWorkspaceState>;
  getAllAIWorkspaceTabGroups: () => Record<string, AIWorkspaceTabGroup>;
  getSessionFileManagerWorkspace: (terminalId: string) => FileManagerWorkspaceState;
  isRecoveryPasswordError: (error: unknown) => boolean;
  isUnsupportedMonitorSession: (session: SessionLike | null | undefined) => boolean;
  lastContentTabRef: React.RefObject<Record<string, string>>;
  lastTerminalRef: React.RefObject<Record<string, string>>;
  loadServerWorkspaceSessionSnapshot: (serverId: string) => Promise<WorkspaceSessionSnapshot | null>;
  markWorkspaceRestoreNavigationOverride: () => void;
  mountedRef: React.RefObject<boolean>;
  normalizeWorkspaceContentTab: (value: unknown) => WorkspaceContentTab;
  persistServerWorkspaceSessionSnapshot: (session: SessionLike, overrides?: SnapshotOverrides) => void;
  persistWorkspaceSnapshotRef: React.RefObject<((overrides?: Record<string, unknown>) => void) | null>;
  recordRecentConnection: (serverId: string) => void;
  registerServerDisconnect: (serverId: string, promise: Promise<unknown>) => void;
  remapSessionFileManagerWorkspaceMap: (workspaces: Record<string, unknown> | null | undefined, idMap: Record<string, string> | null | undefined) => Record<string, unknown>;
  remapSessionFileManagerWorkspaces: (idMap: Record<string, string> | null | undefined) => Record<string, FileManagerWorkspaceState>;
  remapAIWorkspaceTabGroups: (idMap: Record<string, string> | null | undefined) => Record<string, AIWorkspaceTabGroup>;
  remapSessionWorkspaceLayouts: (
    layouts: Record<string, TerminalPaneLayout> | null | undefined,
    idMap: Record<string, string> | null | undefined,
    targetSessionId: string,
  ) => Record<string, TerminalPaneLayout>;
  remapTerminalPaneLayouts: (
    layouts: Record<string, TerminalPaneLayout> | null | undefined,
    idMap: Record<string, string>,
    sessionId: string,
  ) => Record<string, TerminalPaneLayout>;
  rememberSessionActiveTerminal: (sessionId: string, terminalId: string, label: string) => void;
  rememberWorkspace: boolean;
  rememberWorkspaceLoaded: boolean;
  removeChangeReviewsByRequestId: (requestId: string) => void;
  replaceAllSessionFileManagerWorkspaces: (nextState: unknown) => Record<string, FileManagerWorkspaceState>;
  replaceAllAIWorkspaceTabGroups: (nextState: unknown) => Record<string, AIWorkspaceTabGroup>;
  clearAIWorkspaceTabGroup: (terminalId: string) => void;
  resolveSessionRootTerminalId: (
    session: SessionLike,
    fallbackTerminalId: string | null | undefined,
    layouts?: Record<string, TerminalPaneLayout>,
    label?: string,
  ) => string | null;
  restoringWorkspaceRef: React.RefObject<boolean>;
  restoringWorkspaceSessionIds: Set<string>;
  serversLoaded: boolean;
  serversRef: React.RefObject<config.Connection[]>;
  sessionsRef: React.RefObject<SessionLike[]>;
  setActiveSessionId: (id: string | null) => void;
  setActiveTerminalId: (id: string | null) => void;
  setConnectingServers: React.Dispatch<React.SetStateAction<ConnectingServer[]>>;
  setContentTab: (tab: string) => void;
  setCreatingTerminalSessionId: (id: string | null) => void;
  setCredentials: React.Dispatch<React.SetStateAction<config.Credential[]>>;
  setMonitoringEnabled: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  setMountedSessions: React.Dispatch<React.SetStateAction<Set<string>>>;
  setRestoringWorkspaceSessionIds: React.Dispatch<React.SetStateAction<Set<string>>>;
  setServers: React.Dispatch<React.SetStateAction<config.Connection[]>>;
  setServersLoaded: (loaded: boolean) => void;
  setSessionAuthPrompts: React.Dispatch<React.SetStateAction<Record<string, SessionAuthPrompt>>>;
  setSessionFileManagerWorkspace: (terminalId: string, state: FileManagerWorkspaceState) => FileManagerWorkspaceState;
  setSessions: React.Dispatch<React.SetStateAction<SessionLike[]>>;
  setSettingsInitialTab: (tab: string) => void;
  setShowSettings: (show: boolean) => void;
  setSshChannelUsage: React.Dispatch<React.SetStateAction<Record<string, SshChannelUsage>>>;
  setSyncFailed: React.Dispatch<React.SetStateAction<unknown>>;
  setTabContextMenu: (menu: unknown) => void;
  setTerminalPaneLayouts: React.Dispatch<React.SetStateAction<Record<string, TerminalPaneLayout>>>;
  setTerminalSubTabOverflow: (overflow: boolean) => void;
  setTerminalTabContextMenu: (menu: unknown) => void;
  setWorkspaceRestoreReady: (ready: boolean) => void;
  sortTerminalPaneCells: (cells: unknown) => TerminalPaneLayout['panes'][number]['cells'];
  syncFailed: unknown;
  syncWithRecoveryPassword: <TResult>(options: {
    sync?: () => Promise<TResult>;
    initialError?: unknown;
    retry: (password: string) => Promise<TResult>;
    prompt: (title: string, placeholder: string, message: string, okLabel?: string, options?: Record<string, unknown>) => Promise<string | null>;
    t: (key: string, vars?: Record<string, unknown>) => string;
  }) => Promise<{ result: TResult | null; cancelled: boolean }>;
  t: (key: string, vars?: Record<string, unknown>) => string;
  terminalPaneLayoutsRef: React.RefObject<Record<string, TerminalPaneLayout>>;
  terminalSubTabScrollBySessionRef: React.RefObject<Record<string, number>>;
  terminalSubTabScrollRef: React.RefObject<HTMLElement | null>;
  terminalSubTabScrollTargetRef: React.RefObject<number>;
  updateSessionStatus: (sessionId: string, status: string) => void;
  waitForServerDisconnect: (serverId: string) => Promise<unknown>;
  workspacePersistenceLevel: 'program' | 'session';
  workspaceRestoreNavigationOverrideRef: React.RefObject<boolean>;
  workspaceRestoreStartedRef: React.RefObject<boolean>;
}

interface ReconnectSessionResult {
  oldToNew: Record<string, string>;
  newTerminals: Array<{ id: string; label: string }>;
}

export interface UseSessionConnectionsResult {
  handleConnectError: (sessionId: string, err: unknown) => void;
  postConnectSetup: (sessionId: string, serverId: string) => Promise<void>;
  loadServers: () => Promise<void>;
  handleCancelConnection: (sessionId: string) => void;
  resolveSessionContentTab: (sessionId: string) => string;
  switchToNextSession: (currentSessionId: string) => void;
  handleTabClick: (sessionId: string) => void;
  canCopySessionPassword: (sessionId: string) => boolean;
  handleCopySessionPassword: (sessionId: string) => Promise<void>;
  reconnectSession: (
    session: SessionLike,
    requestingTerminalId?: string,
    options?: { deferState?: boolean },
  ) => Promise<ReconnectSessionResult | null>;
  resolveHostKeyChoice: (sessionId: string, chosen: number) => Promise<void>;
  resolvePasswordPrompt: (sessionId: string, connId: string, result: { value: string; persist: boolean } | null) => Promise<void>;
  handleCloseWindow: () => Promise<void>;
  connectServer: (server: config.Connection) => Promise<void>;
  connectLocal: (name: string, shellPath: string) => void;
  connectSerial: (config: { port: string; baudRate: number; dataBits: number; stopBits: number; parity: string }) => void;
  forceCloseSession: (sessionId: string) => void;
  closeSession: (sessionId: string, e?: React.MouseEvent) => Promise<void>;
  closeAllSessions: () => Promise<void>;
  openNewTerminal: (sessionId: string, options?: {
    sourceTerminalId?: string;
    cloneFileManagerWorkspace?: boolean;
    cloneCwd?: boolean;
  }) => Promise<void>;
  handleRenameTerminalTab: (sessionId: string, terminalId: string) => Promise<void>;
  closeTerminal: (sessionId: string, terminalId: string, e?: React.MouseEvent) => void;
}

/** 恢复的工作区快照（JSON.parse 后的宽松形状） */
interface RestoredSnapshotSession {
  id?: unknown;
  serverId?: unknown;
  serverName?: unknown;
  host?: unknown;
  activeTerminalId?: unknown;
  activeTerminalLabel?: unknown;
  terminals?: Array<{ id?: unknown; label?: unknown }>;
  workspaceTabs?: Array<{ terminalIds?: unknown }>;
  aiTabWorkspaces?: Record<string, unknown>;
}

function normalizeAIWorkspaceReconnectTerminals(
  terminals: unknown,
  preferredRootTerminalId: unknown,
  fallbackTerminalId: string,
  fallbackLabel: string,
  aiTabWorkspaces: Record<string, unknown> | null | undefined = null,
): Array<{ id: string; label: string }> {
  const seenIds = new Set<string>()
  const normalizedTerminals = (Array.isArray(terminals) ? terminals : []).flatMap((terminal, index) => {
    const item = terminal && typeof terminal === 'object' ? terminal as Record<string, unknown> : {}
    const id = typeof item.id === 'string' ? item.id.trim() : ''
    if (!id || seenIds.has(id)) {
      return []
    }
    seenIds.add(id)
    return [{
      id,
      label: typeof item.label === 'string' && item.label.trim()
        ? item.label.trim()
        : `${fallbackLabel}${index + 1}`,
    }]
  })
  Object.keys(aiTabWorkspaces || {}).forEach((terminalId) => {
    const id = terminalId.trim()
    if (!id || seenIds.has(id)) {
      return
    }
    seenIds.add(id)
    normalizedTerminals.push({
      id,
      label: `${fallbackLabel}${normalizedTerminals.length + 1}`,
    })
  })
  const normalizedPreferredRootTerminalId = typeof preferredRootTerminalId === 'string'
    ? preferredRootTerminalId.trim()
    : ''
  const rootTerminal = normalizedTerminals.find((terminal) => terminal.id === normalizedPreferredRootTerminalId)
    || normalizedTerminals[0]
    || {
      id: fallbackTerminalId,
      label: fallbackLabel,
    }
  return [
    rootTerminal,
    ...normalizedTerminals.filter((terminal) => terminal.id !== rootTerminal.id),
  ]
}

function remapAIWorkspaceTabSnapshotGroups(
  workspaces: Record<string, unknown> | null | undefined,
  idMap: Record<string, string>,
): Record<string, unknown> {
  return Object.fromEntries(
    Object.entries(workspaces || {}).flatMap(([terminalId, group]) => {
      const mappedTerminalId = idMap[terminalId]
      return mappedTerminalId ? [[mappedTerminalId, group]] : []
    }),
  )
}

export default function useSessionConnections(deps: UseSessionConnectionsDeps): UseSessionConnectionsResult {
  const {
    activeSessionIdRef, activeTerminalIdRef, addToast, authPromptTokenRef, awaitDisconnectTerminals,
    buildTerminalCloneCwdCommand, cancelledConnectionsRef, clearSessionAuthPrompt,
    cloneSessionFileManagerWorkspaceState, connectingServersRef, contentTabRef, creatingTerminalRef,
    credentials, disconnectSessionConnection, disconnectSessionTerminals, fileManagerPosition,
    getAllSessionFileManagerWorkspaces, getAllAIWorkspaceTabGroups, getSessionFileManagerWorkspace, isRecoveryPasswordError,
    isUnsupportedMonitorSession, lastContentTabRef, lastTerminalRef, loadServerWorkspaceSessionSnapshot,
    markWorkspaceRestoreNavigationOverride, mountedRef, normalizeWorkspaceContentTab,
    persistServerWorkspaceSessionSnapshot, persistWorkspaceSnapshotRef, recordRecentConnection,
    registerServerDisconnect, remapSessionFileManagerWorkspaceMap, remapSessionFileManagerWorkspaces, remapAIWorkspaceTabGroups,
    remapSessionWorkspaceLayouts, remapTerminalPaneLayouts, rememberSessionActiveTerminal,
    rememberWorkspace, rememberWorkspaceLoaded, removeChangeReviewsByRequestId,
    replaceAllSessionFileManagerWorkspaces, replaceAllAIWorkspaceTabGroups, clearAIWorkspaceTabGroup, resolveSessionRootTerminalId, restoringWorkspaceRef,
    restoringWorkspaceSessionIds,
    serversLoaded, serversRef, sessionsRef, setActiveSessionId, setActiveTerminalId,
    setConnectingServers, setContentTab, setCreatingTerminalSessionId, setCredentials,
    setMonitoringEnabled, setMountedSessions, setRestoringWorkspaceSessionIds, setServers,
    setServersLoaded, setSessionAuthPrompts, setSessionFileManagerWorkspace, setSessions,
    setSettingsInitialTab, setShowSettings, setSshChannelUsage, setSyncFailed, setTabContextMenu,
    setTerminalPaneLayouts, setTerminalSubTabOverflow, setTerminalTabContextMenu,
    setWorkspaceRestoreReady, sortTerminalPaneCells, syncFailed, syncWithRecoveryPassword, t,
    terminalPaneLayoutsRef, terminalSubTabScrollBySessionRef, terminalSubTabScrollRef,
    terminalSubTabScrollTargetRef, updateSessionStatus, waitForServerDisconnect,
    workspacePersistenceLevel, workspaceRestoreNavigationOverrideRef, workspaceRestoreStartedRef,
  } = deps;

  // ponytail: 防双击/重复点击同一服务器：连接进行中时忽略后续 connectServer 触发。
  // 双击卡片会连续触发两次 onConnect，而 connectServer 开头有 await，两次调用
  // 都能穿过 existing/closedSession 检查，各自 ConnectSSH → 服务器上开出两个
  // /bin/bash（两个终端通道），通道占用虚高。
  const connectingServerIdsRef = useRef<Set<string>>(new Set());

  const handleConnectError = useCallback((sessionId: string, err: unknown) => {
    // 如果用户已取消该连接，不再弹错误提示
    if (cancelledConnectionsRef.current.has(sessionId)) {
      cancelledConnectionsRef.current.delete(sessionId);
      return;
    }
    const errMsg = String(err);
    const isHostKeyChange = errMsg.includes('主机密钥已变更');
    const isAuthFailed = errMsg.includes('认证失败');
    setSessions((prev) =>
      prev.map((s) => (s.id === sessionId ? { ...s, status: (isHostKeyChange || isAuthFailed) ? 'connecting' : 'error' } : s))
    );
    if (!isHostKeyChange && !isAuthFailed) {
      setConnectingServers((prev) => prev.filter((s) => s.sessionId !== sessionId));
      addToast(`${t('连接失败')}: ${err}`, 'error', 5000);
    }
  }, [addToast, t]);

  // ── 连接成功后通用设置：查询 OS 信息、启用监控、持久化 OS ──
  const postConnectSetup = useCallback(async (sessionId: string, serverId: string) => {
    let staticInfo: Record<string, unknown> | null = null;
    try {
      // 获取静态信息（OS/主机名/时区）
      staticInfo = await AppGo.GetServerStaticInfo(sessionId) as Record<string, unknown> | null;
      if (staticInfo) {
        setSessions((prev) => prev.map((s) => s.id === sessionId ? { ...s, osInfo: staticInfo } : s));
      }
      // 启用监控（PowerShell/CMD 无 probe 后端，跳过以避免无效轮询与误导标记）
      const sess = sessionsRef.current.find((s) => s.id === sessionId);
      if (!isUnsupportedMonitorSession(sess)) {
        setMonitoringEnabled((prev) => ({ ...prev, [sessionId]: true }));
      }
    } catch (_) {
    } finally {
      if (serverId) {
        recordRecentConnection(serverId);
        setServers(prevServers => {
          const currentServer = prevServers.find(s => s.id === serverId);
          if (!currentServer) return prevServers;
          const detectedOs = staticInfo?.os || '';
          // 连接成功后统一同步；OS 检测失败时保留已有值，避免清空。
          AppGo.SetConnectionOS(serverId, String(detectedOs || currentServer.os || '')).catch((err) => { warnDev(err); });
          if (detectedOs && currentServer.os !== detectedOs) {
            return prevServers.map(s => s.id === serverId ? { ...s, os: String(detectedOs) } : s);
          }
          return prevServers;
        });
      }
    }
  }, [recordRecentConnection]);

  // ── Load servers ───────────────────────────────────────────
  const loadServers = useCallback(async () => {
    try {
      const data = await AppGo.GetConnectionsMasked();
      setServers(data || []);
    } catch (_) {
      addToast(t('加载服务器配置失败'), 'error');
    }
    try {
      const creds = await AppGo.GetCredentials();
      setCredentials(creds || []);
    } catch (_) { }
    setServersLoaded(true);
  }, [addToast]);

  useEffect(() => { loadServers(); }, [loadServers]);

  // ── 取消连接 ──────────────────────────────────────────────
  const handleCancelConnection = useCallback((sessionId: string) => {
    if (!sessionId) return;
    const session = sessionsRef.current.find((item) => item.id === sessionId);
    const termIds = Array.from(new Set([
      sessionId,
      ...(session?.terminals || []).map((terminal) => terminal.id as string),
    ].filter(Boolean)));
    const disconnectPromise = disconnectSessionConnection(sessionId, termIds);
    if (session?.serverId) {
      registerServerDisconnect(String(session.serverId), disconnectPromise);
    }
    setSessions(prev => prev.filter(s => s.id !== sessionId));
    setActiveSessionId(null);
    setActiveTerminalId(null);
    setConnectingServers((prev) => prev.filter((s) => s.sessionId !== sessionId));
    termIds.forEach((terminalId) => clearAIWorkspaceTabGroup(terminalId));
    clearSessionAuthPrompt(sessionId);
  }, [clearAIWorkspaceTabGroup, clearSessionAuthPrompt, disconnectSessionConnection, registerServerDisconnect]);

  // ── 切换到下一个可用 session ──────────────────────────────
  const resolveSessionContentTab = useCallback((sessionId: string) => {
    const tab = normalizeWorkspaceContentTab(lastContentTabRef.current[sessionId] || 'terminal');
    // 文件管理器已停靠时，files 页签不可用，回落终端
    if (tab === 'files' && fileManagerPosition !== 'tab') return 'terminal';
    // 串口会话不支持文件管理/进程/网络（无 SFTP/probe），回落终端
    const sess = sessionsRef.current.find((s) => s.id === sessionId);
    if (sess?.isSerial && (tab === 'files' || tab === 'process' || tab === 'network')) return 'terminal';
    // PowerShell/CMD 无 probe 后端，进程/网络监控不可用（文件管理仍可用），回落终端
    if (isUnsupportedMonitorSession(sess) && (tab === 'process' || tab === 'network')) return 'terminal';
    return tab;
  }, [fileManagerPosition]);

  const switchToNextSession = useCallback((currentSessionId: string) => {
    const remaining = sessionsRef.current.filter(s => s.id !== currentSessionId);
    if (remaining.length > 0) {
      const nextSession = remaining[remaining.length - 1];
      setActiveSessionId(nextSession.id!);
      const nextTermId = resolveSessionRootTerminalId(
        nextSession,
        lastTerminalRef.current[nextSession.id!] || String(nextSession.activeTerminalId || ''),
        terminalPaneLayoutsRef.current,
        String(nextSession.activeTerminalLabel || ''),
      );
      setActiveTerminalId(nextTermId);
      if (nextTermId) {
        rememberSessionActiveTerminal(nextSession.id!, nextTermId, String(nextSession.activeTerminalLabel || ''));
      }
      setContentTab(resolveSessionContentTab(nextSession.id!));
    } else {
      setActiveSessionId(null);
      setActiveTerminalId(null);
    }
  }, [rememberSessionActiveTerminal, resolveSessionContentTab, resolveSessionRootTerminalId]);

  // ponytail: 提取 tab 点击处理，避免每次渲染创建 N 个闭包
  const handleTabClick = useCallback((sessionId: string) => {
    markWorkspaceRestoreNavigationOverride();
    setTabContextMenu(null);
    setTerminalTabContextMenu(null);
    setActiveSessionId(sessionId);
    const sess = sessionsRef.current.find(x => x.id === sessionId);
    const preferredId = lastTerminalRef.current[sessionId] || String(sess?.activeTerminalId || '') || null;
    const preferredLabel = String(sess?.activeTerminalLabel || '');
    const nextTerminalId = sess ? resolveSessionRootTerminalId(sess, preferredId, terminalPaneLayoutsRef.current, preferredLabel) : null;
    setActiveTerminalId(nextTerminalId);
    if (nextTerminalId) {
      rememberSessionActiveTerminal(sessionId, nextTerminalId, preferredLabel);
    }
    setContentTab(resolveSessionContentTab(sessionId));
    persistWorkspaceSnapshotRef.current?.({
      activeSessionId: sessionId,
      activeTerminalId: nextTerminalId,
    });
  }, [markWorkspaceRestoreNavigationOverride, rememberSessionActiveTerminal, resolveSessionContentTab, resolveSessionRootTerminalId]);

  const canCopySessionPassword = useCallback((sessionId: string) => {
    const session = sessionsRef.current.find((item) => item.id === sessionId);
    if (!session?.serverId) {
      return false;
    }
    const server = serversRef.current.find((item) => item.id === session.serverId);
    if (!server) {
      return false;
    }
    if (server.credentialId) {
      const credential = credentials.find((item) => item.id === server.credentialId);
      return credential?.authMethod === 'password';
    }
    return server.authMethod === 'password';
  }, [credentials]);

  const handleCopySessionPassword = useCallback(async (sessionId: string) => {
    const session = sessionsRef.current.find((item) => item.id === sessionId);
    if (!session?.serverId) {
      addToast(t('复制失败'), 'error', 3000);
      return;
    }
    try {
      const password = await AppGo.GetConnectionPassword(String(session.serverId));
      if (!password) {
        throw new Error('empty password');
      }
      await navigator.clipboard.writeText(password);
      addToast(t('已复制'), 'success', 2000);
    } catch {
      addToast(t('复制失败'), 'error', 3000);
    }
  }, [addToast, t]);

  // ── 重连会话核心逻辑 ────────────────────────────────────────
  const reconnectSession = useCallback(async (
    session: SessionLike,
    requestingTerminalId?: string,
    options: { deferState?: boolean; quiet?: boolean } = {},
  ): Promise<ReconnectSessionResult | null> => {
    const deferState = options?.deferState === true;
    // quiet:仅抑制失败 toast(自动重连的中间尝试使用),不影响状态更新
    const quiet = options?.quiet === true;
    const suppressToast = deferState || quiet;
    updateSessionStatus(session.id!, 'connecting');

    if (session.isLocal) {
      const serverObj = { id: String(session.serverId), name: String(session.serverName), host: 'localhost' };
      setConnectingServers((prev) => [...prev, { server: serverObj, sessionId: session.id!, startTime: Date.now() }]);
      try {
        await window.go.wailsapp.App.ConnectLocal(session.id!, String(session.serverName), String(session.shellPath || ''), '');
        // 本地/串口复用同一 sessionId 重连：自增 wsRebuildKey 让 Terminal 重建 WebSocket
        if (!deferState) {
          setSessions((prev) =>
            prev.map((s) => (s.id === session.id ? { ...s, status: 'connected', wsRebuildKey: ((s.wsRebuildKey as number) || 0) + 1 } : s))
          );
        }
        setConnectingServers((prev) => prev.filter((s) => s.sessionId !== session.id));
        return { oldToNew: { [session.id!]: session.id! }, newTerminals: session.terminals as Array<{ id: string; label: string }> };
      } catch (err) {
        setSessions((prev) =>
          prev.map((s) => (s.id === session.id ? { ...s, status: 'error' } : s))
        );
        setConnectingServers((prev) => prev.filter((s) => s.sessionId !== session.id));
        if (!suppressToast) {
          addToast(`${t('重新连接失败')}: ${String(err)}`, 'error', 5000);
        }
        return null;
      }
    }

    if (session.isSerial) {
      const serverObj = {
        id: String(session.serverId),
        name: String(session.serverName),
        host: String((session.serialConfig as { port?: unknown } | null)?.port || ''),
      };
      setConnectingServers((prev) => [...prev, { server: serverObj, sessionId: session.id!, startTime: Date.now() }]);
      try {
        const config = session.serialConfig as { port: string; baudRate: number; dataBits: number; stopBits: number; parity: string };
        await window.go.wailsapp.App.ConnectSerial(
          session.id!,
          String(session.serverName),
          config.port,
          config.baudRate,
          config.dataBits,
          config.stopBits,
          config.parity
        );
        // 本地/串口复用同一 sessionId 重连：自增 wsRebuildKey 让 Terminal 重建 WebSocket
        if (!deferState) {
          setSessions((prev) =>
            prev.map((s) => (s.id === session.id ? { ...s, status: 'connected', wsRebuildKey: ((s.wsRebuildKey as number) || 0) + 1 } : s))
          );
        }
        setConnectingServers((prev) => prev.filter((s) => s.sessionId !== session.id));
        return { oldToNew: { [session.id!]: session.id! }, newTerminals: session.terminals as Array<{ id: string; label: string }> };
      } catch (err) {
        setSessions((prev) =>
          prev.map((s) => (s.id === session.id ? { ...s, status: 'error' } : s))
        );
        setConnectingServers((prev) => prev.filter((s) => s.sessionId !== session.id));
        if (!suppressToast) {
          addToast(`${t('重新连接失败')}: ${String(err)}`, 'error', 5000);
        }
        return null;
      }
    }

    const serverObj = serversRef.current.find((sv) => sv.id === session.serverId);
    if (serverObj) {
      setConnectingServers((prev) => [...prev, { server: serverObj, sessionId: session.id!, startTime: Date.now() }]);
    }
    try {
      // 先拆旧 SSH（保留前端 terminals 列表用于恢复），避免脏 connTerminals / 重复登记
      const priorTerminals = session.terminals?.length
        ? session.terminals
        : [{ id: session.id! }];
      const disconnectIds = new Set([session.id!, ...priorTerminals.map((term) => term.id as string).filter(Boolean)]);
      await awaitDisconnectTerminals([...disconnectIds]);

      await AppGo.ConnectSSH(session.id!, String(session.serverId));

      const hasSavedTerminals = Array.isArray(session.terminals) && session.terminals.length > 0;
      const savedTerminals = hasSavedTerminals ? session.terminals! : [{ id: session.id!, label: `${t('终端')}1` }];
      const rootTerminal = savedTerminals.find(term => term.id === session.id) || savedTerminals[0] || { id: session.id!, label: `${t('终端')}1` };
      const subTerminals = savedTerminals.filter(term => term.id !== rootTerminal.id);
      const oldToNew: Record<string, string> = { [rootTerminal.id!]: session.id!, [session.id!]: session.id! };
      for (const sub of subTerminals) {
        try {
          const newTermId = await AppGo.OpenTerminal(session.id!);
          oldToNew[sub.id!] = newTermId;
        } catch { }
      }
      const newTerminals = savedTerminals
        .map(term => ({
          id: oldToNew[term.id!],
          label: String(term.label || `${t('终端')}1`),
        }))
        .filter(term => !!term.id);

      if (!deferState && Object.keys(oldToNew).length > 0) {
        remapSessionFileManagerWorkspaces(oldToNew);
        remapAIWorkspaceTabGroups(oldToNew);
        const remappedLayouts = remapTerminalPaneLayouts(terminalPaneLayoutsRef.current, oldToNew, session.id!);
        terminalPaneLayoutsRef.current = remappedLayouts;
        setTerminalPaneLayouts(remappedLayouts);
        if (lastTerminalRef.current[session.id!] && oldToNew[lastTerminalRef.current[session.id!]]) {
          lastTerminalRef.current[session.id!] = oldToNew[lastTerminalRef.current[session.id!]];
        }
      }

      if (!deferState) {
        setSessions((prev) =>
          prev.map((s) => (s.id === session.id ? { ...s, status: 'connected', terminals: newTerminals } : s))
        );
      }
      setConnectingServers((prev) => prev.filter((s) => s.sessionId !== session.id));

      if (requestingTerminalId && oldToNew[requestingTerminalId]) {
        setActiveTerminalId(oldToNew[requestingTerminalId]);
      }

      await postConnectSetup(session.id!, String(session.serverId));
      return { oldToNew, newTerminals };
    } catch (err) {
      const errMsg = String(err);
      const isHostKeyChange = errMsg.includes('主机密钥已变更');
      setSessions((prev) =>
        prev.map((s) => (s.id === session.id ? { ...s, status: isHostKeyChange ? 'connecting' : 'error' } : s))
      );
      if (!isHostKeyChange) {
        setConnectingServers((prev) => prev.filter((s) => s.sessionId !== session.id));
        if (!suppressToast) {
          addToast(`${t('重新连接失败')}: ${String(err)}`, 'error', 5000);
        }
      }
      return null;
    }
  }, [addToast, awaitDisconnectTerminals, t, postConnectSetup]);

  // ── 断线自动重连(按服务器开关,默认关;指数退避,最多 10 次)────
  const AUTO_RECONNECT_MAX_ATTEMPTS = 10;
  const autoReconnectTimersRef = useRef<Map<string, { timer: number; attempt: number }>>(new Map());

  // 该会话对应的服务器是否开启了断线自动重连(本地/串口无 transport 断连语义,恒不启用)
  const isSessionAutoReconnectEnabled = useCallback((session: SessionLike) => {
    if (session.isLocal || session.isSerial) {
      return false;
    }
    const server = serversRef.current.find((sv) => sv.id === session.serverId);
    return !!server?.autoReconnect;
  }, [serversRef]);

  // 卸载时清掉所有挂起的重连定时器
  useEffect(() => () => {
    autoReconnectTimersRef.current.forEach((state) => window.clearTimeout(state.timer));
    autoReconnectTimersRef.current.clear();
  }, []);

  const cancelAutoReconnect = useCallback((sessionId: string) => {
    const state = autoReconnectTimersRef.current.get(sessionId);
    if (state) {
      window.clearTimeout(state.timer);
      autoReconnectTimersRef.current.delete(sessionId);
    }
  }, []);

  const scheduleAutoReconnect = useCallback((sessionId: string) => {
    const session = sessionsRef.current.find((s) => s.id === sessionId);
    if (!session || !isSessionAutoReconnectEnabled(session)) {
      return;
    }
    cancelAutoReconnect(sessionId);
    const state = { timer: 0, attempt: 0 };
    autoReconnectTimersRef.current.set(sessionId, state);
    const runAttempt = () => {
      if (!autoReconnectTimersRef.current.has(sessionId)) {
        return;
      }
      const current = sessionsRef.current.find((s) => s.id === sessionId);
      // 会话已删除、已被用户手动重连、正在等待主机密钥确认(connecting)、
      // 或服务器已关闭自动重连开关时终止
      if (!current || (current.status !== 'closed' && current.status !== 'error') || !isSessionAutoReconnectEnabled(current)) {
        autoReconnectTimersRef.current.delete(sessionId);
        return;
      }
      state.attempt += 1;
      const isFinalAttempt = state.attempt >= AUTO_RECONNECT_MAX_ATTEMPTS;
      void reconnectSession(current, undefined, { quiet: !isFinalAttempt }).then((result) => {
        // 期间被取消/替换则丢弃结果
        if (autoReconnectTimersRef.current.get(sessionId) !== state) {
          return;
        }
        if (result) {
          autoReconnectTimersRef.current.delete(sessionId);
          addToast(t('SSH 已自动重新连接'), 'success', 3000);
          return;
        }
        if (isFinalAttempt) {
          autoReconnectTimersRef.current.delete(sessionId);
          addToast(t('自动重连失败,已达最大重试次数,请手动重连'), 'error', 6000);
          return;
        }
        // 指数退避:2s 起步,封顶 30s
        const delay = Math.min(2000 * 2 ** (state.attempt - 1), 30000);
        state.timer = window.setTimeout(runAttempt, delay);
      });
    };
    state.timer = window.setTimeout(runAttempt, 2000);
  }, [addToast, cancelAutoReconnect, isSessionAutoReconnectEnabled, reconnectSession, sessionsRef, t]);

  useEffect(() => {
    if (!serversLoaded || !rememberWorkspaceLoaded || workspaceRestoreStartedRef.current) {
      return;
    }
    workspaceRestoreStartedRef.current = true;
    workspaceRestoreNavigationOverrideRef.current = false;
    if (!rememberWorkspace) {
      setWorkspaceRestoreReady(true);
      return;
    }
    (async () => {
      const raw = await window?.go?.wailsapp?.App?.GetWorkspaceState?.();
      if (typeof raw !== 'string' || !raw.trim()) {
        return;
      }
      let snapshot: {
        activeSessionId?: unknown;
        activeTerminalId?: unknown;
        sessions?: RestoredSnapshotSession[];
        terminalPaneLayouts?: Record<string, TerminalPaneLayout>;
        fileManagerWorkspaces?: Record<string, unknown>;
        aiTabWorkspaces?: Record<string, unknown>;
      };
      try {
        snapshot = JSON.parse(raw) as typeof snapshot;
      } catch {
        return;
      }
      const savedSessions = (snapshot.sessions || [])
        .filter((session) => session?.id && session?.serverId && serversRef.current.some((server) => server.id === session.serverId))
        .map((session) => {
          const terminalById = new Map((session.terminals || []).map((term) => [term.id, term]));
          const workspaceTerminalIds = (session.workspaceTabs || []).flatMap((tab) => Array.isArray(tab.terminalIds) ? tab.terminalIds : []);
          const baseTerminalIds = [...workspaceTerminalIds, ...terminalById.keys()];
          const orderedTerminalIds = Array.from(new Set(baseTerminalIds.length > 0 ? baseTerminalIds : [session.id]));
          const terminals = orderedTerminalIds.map((terminalId, index) => {
            const terminal = terminalById.get(terminalId);
            return {
              id: String(terminalId),
              label: String(terminal?.label || `${t('终端')}${index + 1}`),
            };
          });
          const savedActiveTermId = typeof session.activeTerminalId === 'string' ? session.activeTerminalId.trim() : '';
          const savedActiveTermLabel = typeof session.activeTerminalLabel === 'string' ? session.activeTerminalLabel.trim() : '';
          // 当前激活会话若未带 per-session 字段，回退全局 activeTerminalId
          const fallbackActiveTermId = session.id === snapshot.activeSessionId
            ? (typeof snapshot.activeTerminalId === 'string' ? snapshot.activeTerminalId.trim() : '')
            : '';
          return {
            id: String(session.id),
            serverId: String(session.serverId),
            serverName: String(session.serverName || session.host),
            host: String(session.host || ''),
            status: 'connecting',
            activeTerminalId: savedActiveTermId || fallbackActiveTermId || null,
            activeTerminalLabel: savedActiveTermLabel || null,
            terminals,
          } as SessionLike;
        });
      if (savedSessions.length === 0) {
        return;
      }
      const savedLayouts = Object.fromEntries(
        Object.entries(snapshot.terminalPaneLayouts || {})
          .filter(([, layout]) => savedSessions.some((session) => session.id === layout?.sessionId))
          .map(([layoutId, layout]) => [
            layoutId,
            {
              ...layout,
              sessionId: layout.sessionId,
              rootTerminalId: layout.rootTerminalId || layoutId,
              panes: (layout.panes || []).map((pane) => ({
                ...pane,
                cells: sortTerminalPaneCells(pane.cells),
              })),
            },
          ])
      ) as Record<string, TerminalPaneLayout>;
      const savedTerminalIds = new Set(savedSessions.flatMap((session) => (session.terminals || []).map((terminal) => String(terminal.id))));
      const savedFileManagerWorkspaces = Object.fromEntries(
        Object.entries(snapshot.fileManagerWorkspaces || {})
          .filter(([terminalId]) => savedTerminalIds.has(terminalId))
      );
      const savedAITabWorkspaces = Object.fromEntries(
        Object.entries(snapshot.aiTabWorkspaces || {})
          .filter(([terminalId]) => savedTerminalIds.has(terminalId))
      );
      const initialActiveSessionId = savedSessions.some((session) => session.id === snapshot.activeSessionId)
        ? snapshot.activeSessionId as string
        : savedSessions[0].id!;
      replaceAllSessionFileManagerWorkspaces(savedFileManagerWorkspaces);
      replaceAllAIWorkspaceTabGroups(savedAITabWorkspaces);
      restoringWorkspaceRef.current = true;
      setRestoringWorkspaceSessionIds(new Set(savedSessions.map((session) => session.id!)));
      setSessions(savedSessions);
      sessionsRef.current = savedSessions;
      setTerminalPaneLayouts(savedLayouts);
      terminalPaneLayoutsRef.current = savedLayouts;
      setMountedSessions(new Set(initialActiveSessionId ? [initialActiveSessionId] : []));
      setActiveSessionId(initialActiveSessionId);
      setActiveTerminalId(String(snapshot.activeTerminalId || initialActiveSessionId));
      setContentTab('terminal');

      const idMap: Record<string, string> = {};
      let restoredLayouts = savedLayouts;
      for (const savedSession of savedSessions) {
        const result = await reconnectSession(
          { ...savedSession, status: 'closed', terminals: savedSession.terminals },
          undefined,
          { deferState: true },
        );
        setRestoringWorkspaceSessionIds((prev) => {
          if (!prev.has(savedSession.id!)) {
            return prev;
          }
          const next = new Set(prev);
          next.delete(savedSession.id!);
          return next;
        });
        if (result?.oldToNew) {
          Object.assign(idMap, result.oldToNew);
          remapSessionFileManagerWorkspaces(result.oldToNew);
          remapAIWorkspaceTabGroups(result.oldToNew);
          restoredLayouts = remapTerminalPaneLayouts(restoredLayouts, result.oldToNew, savedSession.id!);
          const restoredSession = { ...savedSession, status: 'connected', terminals: result.newTerminals };
          const restoredSessionLayouts = Object.fromEntries(
            Object.entries(restoredLayouts).filter(([, layout]) => layout?.sessionId === savedSession.id)
          );
          // 每个会话各自恢复上次选中的终端（不仅当前激活会话）
          // 优先按旧 id 映射；失败再用标签名（终端3）兜底
          const rawPreferredId = savedSession.activeTerminalId
            || (savedSession.id === initialActiveSessionId ? snapshot.activeTerminalId : null);
          const preferredTermId = (rawPreferredId && idMap[String(rawPreferredId)]) || rawPreferredId || null;
          const preferredLabel = savedSession.activeTerminalLabel || '';
          const resolvedTermId = resolveSessionRootTerminalId(
            restoredSession,
            preferredTermId as string | null,
            { ...terminalPaneLayoutsRef.current, ...restoredSessionLayouts },
            String(preferredLabel),
          );
          const resolvedLabel = restoredSession.terminals?.find((term) => term.id === resolvedTermId)?.label
            || preferredLabel
            || '';
          const sessionWithActive = resolvedTermId
            ? { ...restoredSession, activeTerminalId: resolvedTermId, activeTerminalLabel: String(resolvedLabel) }
            : restoredSession;
          if (resolvedTermId) {
            lastTerminalRef.current[sessionWithActive.id!] = resolvedTermId;
          }
          // ponytail: 用函数式更新而非整体覆盖，避免恢复期间用户新建/关闭的 session 被丢失或复活
          sessionsRef.current = sessionsRef.current.map((session) => (
            session.id === savedSession.id ? sessionWithActive : session
          ));
          setSessions((prev) => prev.map((session) => (
            session.id === savedSession.id ? sessionWithActive : session
          )));
          terminalPaneLayoutsRef.current = { ...terminalPaneLayoutsRef.current, ...restoredSessionLayouts };
          setTerminalPaneLayouts((prev) => ({ ...prev, ...restoredSessionLayouts }));
        }
      }

      if (workspaceRestoreNavigationOverrideRef.current) {
        return;
      }
      // ponytail: 收尾时从当前 sessions 找，避免用户已关闭的 session 被复活为 active 导致空白
      const finalSession = sessionsRef.current.find((session) => session.id === initialActiveSessionId) || sessionsRef.current[0];
      if (!finalSession) {
        setActiveSessionId(null);
        setActiveTerminalId(null);
        return;
      }
      const preferredTerminalId = String(finalSession.activeTerminalId || '')
        || lastTerminalRef.current[finalSession.id!]
        || idMap[String(snapshot.activeTerminalId)]
        || snapshot.activeTerminalId;
      const resolvedTerminalId = resolveSessionRootTerminalId(
        finalSession,
        preferredTerminalId as string | null,
        terminalPaneLayoutsRef.current,
        String(finalSession.activeTerminalLabel || ''),
      );
      if (resolvedTerminalId) {
        lastTerminalRef.current[finalSession.id!] = resolvedTerminalId;
        const resolvedLabel = finalSession.terminals?.find((term) => term.id === resolvedTerminalId)?.label || '';
        sessionsRef.current = sessionsRef.current.map((session) => (
          session.id === finalSession.id
            ? { ...session, activeTerminalId: resolvedTerminalId, activeTerminalLabel: String(resolvedLabel) }
            : session
        ));
        setSessions((prev) => prev.map((session) => (
          session.id === finalSession.id
            ? { ...session, activeTerminalId: resolvedTerminalId, activeTerminalLabel: String(resolvedLabel) }
            : session
        )));
      }
      setActiveSessionId(finalSession.id!);
      setActiveTerminalId(resolvedTerminalId);
      setContentTab('terminal');
    })().finally(() => {
      restoringWorkspaceRef.current = false;
      setRestoringWorkspaceSessionIds(new Set());
      setWorkspaceRestoreReady(true);
    });
  }, [rememberWorkspace, rememberWorkspaceLoaded, reconnectSession, resolveSessionRootTerminalId, serversLoaded, t]);

  // ── 监听 SSH 断开事件（整机意外断 vs 单终端结束）────────────────
  useEffect(() => {
    const unbind = EventsOn('ssh-disconnected', (payload: unknown) => {
      // 兼容旧版纯 string sessionId
      const raw = payload as Record<string, unknown> | null;
      const data = (payload && typeof payload === 'object')
        ? raw!
        : { sessionId: payload, parentSessionId: payload, connectionClosed: true, reason: 'transport' };
      const sessionId = typeof data.sessionId === 'string' ? data.sessionId : '';
      const parentSessionId = typeof data.parentSessionId === 'string' && data.parentSessionId ? data.parentSessionId : sessionId;
      const connectionClosed = data.connectionClosed !== false && data.connectionClosed !== 'false';
      const reason = typeof data.reason === 'string' ? data.reason : '';
      const rawTerminalIds = Array.isArray(data.terminalIds) ? data.terminalIds : [];
      const endedTerminalIds = rawTerminalIds.length
        ? rawTerminalIds.map(String)
        : (sessionId ? [sessionId] : []);

      const sessionList = sessionsRef.current;
      const matchedSession = sessionList.find((item) => item.id === parentSessionId || item.id === sessionId)
        || sessionList.find((item) => item.terminals?.some((terminal) => terminal.id === sessionId || terminal.id === parentSessionId || endedTerminalIds.includes(terminal.id!)))
        || null;
      if (!matchedSession) {
        return;
      }
      const parentId = matchedSession.id!;

      // ponytail: 决定性诊断——断线瞬间记录「掉的是不是当前前台/选中标签」。
      // 配合后端 [disconnect] 里的 remoteAddr，可判定掉线是否与前台选中强相关。
      try {
        const rt = (window as unknown as { runtime?: { Log?: (m: string) => void } }).runtime;
        if (rt && rt.Log) {
          const isForeground = parentId === activeSessionIdRef.current;
          rt.Log(`[diag-disconnect] parentSessionId=${parentId} activeSessionId=${activeSessionIdRef.current} isForeground=${isForeground} reason=${reason} connectionClosed=${connectionClosed}`);
        }
      } catch (_) { /* runtime 不可用时静默忽略 */ }

      const transportDead = reason === 'transport' || reason === 'keepalive';
      if (connectionClosed || transportDead) {
        setSessions((prev) => prev.map((s) => (s.id === parentId ? { ...s, status: 'closed' } : s)));
        // 仅传输/保活导致的整机断开视为「意外」；最后一终端正常 exit 只标 closed，不误报
        if (transportDead) {
          addToast(t('SSH 连接已意外断开'), 'error', 4000);
          // 设置开启时自动重连该会话(指数退避)
          scheduleAutoReconnect(parentId);
        }
        return;
      }

      // 单终端 channel 结束：只移除该终端；若已无终端再标 closed
      endedTerminalIds.forEach((terminalId) => clearAIWorkspaceTabGroup(terminalId));
      setSessions((prev) => prev.map((s) => {
        if (s.id !== parentId) return s;
        const nextTerminals = (s.terminals || []).filter((term) => !endedTerminalIds.includes(term.id!));
        if (nextTerminals.length === 0) {
          return { ...s, status: 'closed', terminals: [{ id: s.id!, label: `${t('终端')}1` }] };
        }
        // 根终端 id 常等于 session.id；若根 shell 结束但子终端还在，保留子终端
        const stillHasRoot = nextTerminals.some((term) => term.id === s.id);
        const terminals = stillHasRoot
          ? nextTerminals
          : [{ id: s.id!, label: String(nextTerminals[0]?.label || `${t('终端')}1`) }, ...nextTerminals.filter((term) => term.id !== s.id)];
        return { ...s, status: 'connected', terminals };
      }));
    });
    return () => {
      if (unbind) unbind();
    };
  }, [addToast, clearAIWorkspaceTabGroup, scheduleAutoReconnect, t]);

  // ── 外部 AI 通过 MCP 代为重连成功:后端已复用原会话 id 重新拨号,前端收编状态 ──
  useEffect(() => {
    const unbind = EventsOn('ssh-mcp-reconnected', (payload: unknown) => {
      const data = (payload && typeof payload === 'object' ? payload : {}) as {
        sessionId?: unknown;
        oldToNew?: unknown;
        failedTerminals?: unknown;
      };
      const parentId = typeof data.sessionId === 'string' ? data.sessionId : '';
      const oldToNew = (data.oldToNew && typeof data.oldToNew === 'object' ? data.oldToNew : {}) as Record<string, string>;
      const failedTerminals = Array.isArray(data.failedTerminals) ? data.failedTerminals.map(String) : [];
      if (!parentId) {
        return;
      }
      const session = sessionsRef.current.find((s) => s.id === parentId);
      if (!session) {
        return;
      }
      const savedTerminals = session.terminals?.length
        ? session.terminals
        : [{ id: parentId, label: `${t('终端')}1` }];
      // 重连成功但未能重开的子终端(failedTerminals)不进入新列表,
      // 并清理其 AI 工作区与文件管理器工作区,避免残留无通道的死终端。
      failedTerminals.forEach((terminalId) => clearAIWorkspaceTabGroup(terminalId));
      const newTerminals = savedTerminals
        .map((term, index) => ({
          id: oldToNew[term.id!],
          label: String(term.label || `${t('终端')}${index + 1}`),
        }))
        .filter((term) => !!term.id);
      // 文件管理器工作区:重映射后跳过失败终端(旧 id/new id 都删),其余原样保留
      const nextFileManagerWorkspaces: Record<string, unknown> = {};
      Object.entries(getAllSessionFileManagerWorkspaces() || {}).forEach(([key, state]) => {
        const mapped = oldToNew[key] || key;
        if (failedTerminals.includes(key) || failedTerminals.includes(mapped)) {
          return;
        }
        nextFileManagerWorkspaces[mapped] = state;
      });
      replaceAllSessionFileManagerWorkspaces(nextFileManagerWorkspaces);
      remapAIWorkspaceTabGroups(oldToNew);
      const remappedLayouts = remapTerminalPaneLayouts(terminalPaneLayoutsRef.current, oldToNew, parentId);
      terminalPaneLayoutsRef.current = remappedLayouts;
      setTerminalPaneLayouts(remappedLayouts);
      setSessions((prev) => prev.map((s) => (s.id === parentId ? { ...s, status: 'connected', terminals: newTerminals } : s)));
      void postConnectSetup(parentId, String(session.serverId));
      addToast(t('外部 AI 已重新连接该服务器'), 'info', 4000);
    });
    return () => {
      if (unbind) unbind();
    };
  }, [addToast, clearAIWorkspaceTabGroup, getAllSessionFileManagerWorkspaces, postConnectSetup, remapAIWorkspaceTabGroups, remapTerminalPaneLayouts, replaceAllSessionFileManagerWorkspaces, t]);

  // ── 外部 AI 连续重连失败达到阈值:提醒用户手动处理 ──────────────
  useEffect(() => {
    const unbind = EventsOn('mcp-reconnect-failed', (payload: unknown) => {
      const data = (payload && typeof payload === 'object' ? payload : {}) as { attempts?: unknown };
      const attempts = typeof data.attempts === 'number' ? data.attempts : 0;
      addToast(`${t('外部 AI 自动重连服务器失败,请手动处理')}${attempts > 0 ? ` (${attempts})` : ''}`, 'error', 8000);
    });
    return () => {
      if (unbind) unbind();
    };
  }, [addToast, t]);

  // ── 主机密钥确认：用户在会话卡片上做出选择后 ──────────────────
  // chosen: 0=取消, 1=仅本次接受, 2=接受并保存
  const resolveHostKeyChoice = useCallback(async (sessionId: string, chosen: number) => {
    clearSessionAuthPrompt(sessionId);
    try {
      await AppGo.AcceptHostKeyChange(sessionId, chosen);
      if (chosen >= 1) {
        setSessions((prev) =>
          prev.map((s) =>
            s.id === sessionId ? { ...s, status: 'connected' } : s
          )
        );
        setConnectingServers((prev) => prev.filter((s) => s.sessionId !== sessionId));
        addToast(
          chosen === 2 ? t('主机密钥已保存，连接成功') : t('本次已接受，连接成功'),
          'success'
        );

        const matched = sessionsRef.current.find((s) => s.id === sessionId);
        await postConnectSetup(sessionId, String(matched?.serverId || ''));
      } else {
        updateSessionStatus(sessionId, 'error');
        setConnectingServers((prev) => prev.filter((s) => s.sessionId !== sessionId));
        addToast(t('用户取消连接'), 'warning', 3000);
      }
    } catch (err) {
      // 取消分支后端固定返回「用户取消了主机密钥验证」，属预期结果，不作失败提示
      if (chosen >= 1) {
        addToast(`${t('连接失败')}: ${String(err)}`, 'error', 5000);
      } else {
        addToast(t('用户取消连接'), 'warning', 3000);
      }
      updateSessionStatus(sessionId, 'error');
      setConnectingServers((prev) => prev.filter((s) => s.sessionId !== sessionId));
    }
  }, [addToast, clearSessionAuthPrompt, postConnectSetup, t, updateSessionStatus]);

  // ── 监听主机密钥变更事件 ────────────────────────────────────
  // 只写入该会话的待确认状态，由会话面板内的 SessionAuthCard 呈现，
  // 批量连接时 N 台主机就有 N 张卡片，各自独立。
  useEffect(() => {
    const unbind = EventsOn('ssh-host-key-changed', (data: Record<string, unknown>) => {
      const {
        sessionId, host, port, newFingerprint, oldFingerprints, isNew
      } = data;

      const oldFpList = (Array.isArray(oldFingerprints) ? oldFingerprints : []).map(String).join('\n');
      const message = isNew
        ? [
          t('首次连接到此主机，请确认密钥指纹：'),
          ``,
          `${t('主机:')} ${host}:${port}`,
          ``,
          t('密钥指纹:'),
          `${newFingerprint}`,
          ``,
          t('如果指纹与服务器管理员提供的匹配，点击"接受并保存"。'),
        ].join('\n')
        : [
          t('远程主机密钥已变更，可能存在中间人攻击！'),
          ``,
          `${t('主机:')} ${host}:${port}`,
          ``,
          t('新密钥指纹:'),
          `${newFingerprint}`,
          ``,
          t('旧密钥指纹:'),
          `${oldFpList}`,
          ``,
          t('如果确认这是预期的变更（如服务器重装），点击"接受并保存"。'),
        ].join('\n');

      setSessionAuthPrompts((prev) => ({
        ...prev,
        [String(sessionId)]: {
          kind: 'hostkey',
          token: ++authPromptTokenRef.current,
          title: isNew ? t('主机密钥确认') : t('主机密钥已变更'),
          message,
          danger: !isNew, // 密钥变更（疑似中间人）默认焦点落在「取消」
        },
      }));
    });
    return () => {
      if (unbind) unbind();
    };
  }, [t]);

  // ── 认证失败：用户在会话卡片上重输密码后 ──────────────────
  // result: null=取消 | { value, persist }
  const resolvePasswordPrompt = useCallback(async (
    sessionId: string,
    connId: string,
    result: { value: string; persist: boolean } | null,
  ) => {
    clearSessionAuthPrompt(sessionId);
    if (result === null) {
      // 用户取消
      updateSessionStatus(sessionId, 'error');
      setConnectingServers((prev) => prev.filter((s) => s.sessionId !== sessionId));
      addToast(t('用户取消连接'), 'warning', 3000);
      return;
    }

    const { value: newPassword, persist } = result;
    if (!newPassword) {
      updateSessionStatus(sessionId, 'error');
      setConnectingServers((prev) => prev.filter((s) => s.sessionId !== sessionId));
      return;
    }

    updateSessionStatus(sessionId, 'connecting');
    setConnectingServers((prev) => {
      if (prev.some((item) => item.sessionId === sessionId)) return prev;
      const matchedServer = serversRef.current.find((server) => String(server.id) === connId);
      const matchedSession = sessionsRef.current.find((session) => session.id === sessionId);
      // server 字段显式声明为 Connection 兼容形状，避免 || 链推导出 {} 类型导致 TS 报错
      const fallbackServer: Pick<config.Connection, 'id' | 'name' | 'host'> = {
        id: connId,
        name: String(matchedSession?.serverName || matchedSession?.host || connId),
        host: String(matchedSession?.host || ''),
      };
      return [...prev, {
        server: matchedServer || fallbackServer,
        sessionId,
        startTime: Date.now(),
      }];
    });

    try {
      await AppGo.ReconnectWithPassword(sessionId, connId, newPassword, persist);
      setSessions((prev) =>
        prev.map((s) => (s.id === sessionId ? { ...s, status: 'connected' } : s))
      );
      setConnectingServers((prev) => prev.filter((s) => s.sessionId !== sessionId));
      addToast(persist ? t('密码已保存，连接成功') : t('连接成功'), 'success', 3000);

      await postConnectSetup(sessionId, connId);
    } catch (retryErr) {
      handleConnectError(sessionId, retryErr);
    }
  }, [addToast, clearSessionAuthPrompt, handleConnectError, postConnectSetup, t, updateSessionStatus]);

  // ── 监听认证失败事件（密码错误等） ──────────────────────────
  // 只写入该会话的待确认状态，由会话面板内的 SessionAuthCard 呈现
  useEffect(() => {
    const unbind = EventsOn('ssh-auth-failed', (data: Record<string, unknown>) => {
      const { sessionId, connId, host, port, username, error } = data;
      const usesCredential = serversRef.current.some(s => s.id === connId && s.credentialId);

      const message = [
        t('认证失败，请输入正确的密码重试：'),
        ``,
        `${t('主机:')} ${host}:${port}`,
        `${t('用户')}: ${username}`,
        ``,
        `${t('错误')}: ${error}`,
      ].join('\n');

      setSessionAuthPrompts((prev) => ({
        ...prev,
        [String(sessionId)]: {
          kind: 'password',
          token: ++authPromptTokenRef.current,
          title: t('认证失败'),
          message,
          connId: String(connId),
          checkboxLabel: usesCredential ? t('更新凭据密码') : t('记住密码'),
        },
      }));
    });
    return () => {
      if (unbind) unbind();
    };
  }, [t]);

  // ── 关闭窗口通用处理 ──────────────────────────────────────────
  const handleCloseWindow = useCallback(async () => {
    if (syncFailed) {
      const choice = await window.luminDialog?.choice?.(
        t('云端同步未完成，确定退出吗？'),
        t('同步未完成'),
        [
          { label: t('仍然退出'), value: 'quit', primary: true },
          { label: t('重试同步'), value: 'retry', secondary: true },
          { label: t('取消'), value: 'cancel', secondary: true },
        ],
        '',
        { priority: 'system' },
      );
      if (choice === 'quit') {
        AppGo.DoQuit();
      } else if (choice === 'retry') {
        const err = await AppGo.RetrySync();
        if (!err) {
          setSyncFailed(null);
          addToast(t('同步成功'), 'success', 3000);
        }
      }
      return;
    }
    const savedAction = localStorage.getItem('windowCloseAction');
    if (savedAction === 'quit') { AppGo.DoQuit(); return; }
    if (savedAction === 'tray') { AppGo.AckClose(); WindowHide(); return; }
    const result = await window.luminDialog?.choice?.(
      t('请选择操作'),
      t('关闭窗口'),
      [
        { label: t('退出'), value: 'quit', shortcut: 'q', primary: true },
        { label: t('系统托盘'), value: 'tray', shortcut: 't', secondary: true },
        { label: t('取消'), value: 'cancel', shortcut: 'c', secondary: true },
      ],
      t('记住选择'),
      { priority: 'system' },
    );
    if (!result) return;
    const { value, checked } = result as { value?: string; checked?: boolean };
    if (checked && (value === 'quit' || value === 'tray')) {
      localStorage.setItem('windowCloseAction', value);
    }
    if (value === 'quit') {
      AppGo.DoQuit();
    } else if (value === 'tray') {
      AppGo.AckClose();
      WindowHide();
    } else if (value === 'cancel') {
      AppGo.AckClose();
    }
  }, [t, syncFailed, addToast]);

  // ── 监听关闭窗口请求，弹出选择对话框 ──────────────────────────
  useEffect(() => {
    const unbind = EventsOn('close-request', handleCloseWindow);
    return () => { if (unbind) unbind(); };
  }, [handleCloseWindow]);

  useEffect(() => {
    const handleOpenRuntimeEnvironmentSettings = (event: Event) => {
      const detail = (event as CustomEvent<{ tab?: unknown; toast?: unknown; duration?: unknown; type?: unknown }>).detail || {};
      const nextTab = typeof detail.tab === 'string' && detail.tab.trim()
        ? detail.tab.trim()
        : 'runtimeEnvironment';
      setSettingsInitialTab(nextTab);
      setShowSettings(true);
      const toastMessage = typeof detail.toast === 'string' ? detail.toast.trim() : '';
      if (toastMessage) {
        const toastDuration = Number.isFinite(Number(detail.duration)) ? Number(detail.duration) : 6000;
        const toastType = typeof detail.type === 'string' && detail.type.trim() ? detail.type.trim() : 'warning';
        addToast(toastMessage, toastType, toastDuration);
      }
    };

    window.addEventListener('open-runtime-environment-settings', handleOpenRuntimeEnvironmentSettings);
    return () => window.removeEventListener('open-runtime-environment-settings', handleOpenRuntimeEnvironmentSettings);
  }, [addToast]);

  // ── 监听云端同步失败事件 ──────────────────────────────────
  useEffect(() => {
    let active = true;
    const unbind = EventsOn('sync-failed', async (data: unknown) => {
      if (!isRecoveryPasswordError(data)) {
        if (active) setSyncFailed(data);
        return;
      }
      if (active) setSyncFailed(null);
      try {
        const { cancelled } = await syncWithRecoveryPassword({
          initialError: data,
          retry: (password) => AppGo.SyncWithRecoveryPassword(password),
          prompt: ((...args: unknown[]) => window.luminDialog!.prompt!(...args as [string, string?, string?, string?, Record<string, unknown>?])) as unknown as (
            title: string, placeholder: string, message: string, okLabel?: string, options?: Record<string, unknown>,
          ) => Promise<string | null>,
          t,
        });
        if (active && !cancelled) addToast(t('同步成功'), 'success', 3000);
      } catch (err) {
        if (!active) return;
        if (isRecoveryPasswordError(err)) {
          addToast(t('恢复密码连续三次错误，同步已取消'), 'error', 4000);
        } else {
          setSyncFailed({ ...(data as object), category: 'sync', error: String((err as { message?: unknown })?.message ?? err) });
        }
      }
    });
    return () => {
      active = false;
      if (unbind) unbind();
    };
  }, [addToast, t]);

  // ── 监听 SSH 通道占用事件 ─────────────────────────────────
  useEffect(() => {
    const unbind = EventsOn('ssh-channel-usage', (payload: unknown) => {
      const data = payload && typeof payload === 'object' ? payload as Record<string, unknown> : null;
      if (!data) return;
      const sessionIds = Array.isArray(data.sessionIds) ? data.sessionIds.map(String).filter(Boolean) : [];
      if (sessionIds.length === 0) return;
      const usage: SshChannelUsage = {
        terminals: Number(data.terminals) || 0,
        sharedSftp: Number(data.sharedSftp) || 0,
        uploadPool: Number(data.uploadPool) || 0,
        total: Number(data.total) || 0,
        maxSessions: Number(data.maxSessions) || 10,
      };
      setSshChannelUsage((prev) => {
        const next = { ...prev };
        sessionIds.forEach((id) => { next[id] = usage; });
        return next;
      });
    });
    return () => { if (unbind) unbind(); };
  }, []);

  // ── 监听 SSH 连接状态事件 ─────────────────────────────────
  useEffect(() => {
    const unbind = EventsOn('ssh-status', (data: Record<string, unknown>) => {
      const sessionId = typeof data?.sessionId === 'string' ? data.sessionId : '';
      if (!sessionId) return;
      const status = typeof data?.status === 'string' ? data.status : '';
      if (status === 'post-auth-slow') {
        const message = t('SSH 已认证，但打开终端通道响应较慢，服务器可能正在恢复或负载较高。');
        setConnectingServers((prev) => prev.map((item) => (
          item.sessionId === sessionId ? { ...item, status, message } : item
        )));
      } else if (status === 'sftp-unavailable' && data?.openwrt === true && typeof data.installCmd === 'string') {
        // OpenWrt/Dropbear 缺省无 SFTP 子系统:给出可复制的安装命令,
        // 避免只显示 cryptic 的 "unexpected EOF"。
        const installCmd = data.installCmd;
        addToast(
          `${t('检测到 OpenWrt 设备，文件管理器需要 SFTP 子系统，请执行以下命令安装')}：${installCmd}。${t('安装完成后请重新连接会话')}`,
          'warning',
          20000,
          [{
            label: t('复制安装命令'),
            onClick: () => {
              navigator.clipboard.writeText(installCmd);
              addToast(t('安装命令已复制'), 'success');
            },
          }],
        );
      }
    });
    return () => { if (unbind) unbind(); };
  }, [addToast, t]);

  // ── 监听同步状态事件 ──────────────────────────────────────
  useEffect(() => {
    const unbind = EventsOn('sync-status', (data: Record<string, unknown>) => {
      if (data.action === 'merge' || data.action === 'download') {
        const msg = data.localChanged
          ? t('同步完成') + `：${t('云端')} ${data.remoteCount} → ${t('合并')} ${data.mergedCount}` + (data.uploaded ? `，${t('已上传')}` : '')
          : t('同步完成') + `：${t('数据一致，无需变更')}`;
        addToast(msg, 'info', 4000);
        // merge/download 意味着本地数据已变更，刷新列表
        if (data.localChanged) loadServers();
      } else if (data.action === 'upload') {
        addToast(t('本地数据已同步到云端'), 'info', 4000);
      } else if (data.action === 'skip' && data.reason === 'tombstone_conflict_needs_manual_sync') {
        addToast(t('已跳过自动同步：删除记录将影响目标云，请手动合并同步并确认。'), 'warning', 8000);
      }
    });
    return () => { if (unbind) unbind(); };
  }, [addToast, t, loadServers]);

  useEffect(() => {
    const unbind = EventsOn('ai-chat-stream', (payload: Record<string, unknown>) => {
      if (!payload || typeof payload !== 'object') {
        return;
      }
      if (
        payload.kind === 'tool_approval_resolved'
        || payload.kind === 'tool_rejected'
        || payload.kind === 'error'
        || payload.kind === 'cancelled'
      ) {
        removeChangeReviewsByRequestId(String(payload.requestId));
      }
    });
    return () => {
      if (unbind) unbind();
    };
  }, [removeChangeReviewsByRequestId]);

  // ── 监听终端触发的重连请求 ──────────────────────────────────
  useEffect(() => {
    const handleReconnectTrigger = (e: Event) => {
      const sessId = (e as CustomEvent<string>).detail;
      // 通过 sessionsRef 读取最新 sessions，避免每次 sessions 变化都重注册监听器
      const sessions = sessionsRef.current;
      // 先按 sessionId 查找
      let sess = sessions.find((s) => s.id === sessId);
      // 如果是子终端 ID，找到父会话
      if (!sess) {
        const parent = sessions.find(s => s.terminals?.some(t => t.id === sessId));
        if (parent) sess = parent;
      }
      if (sess) {
        // 用户手动重连优先,取消该会话挂起的自动重连
        cancelAutoReconnect(sess.id!);
        reconnectSession(sess, sessId);
      }
    };
    window.addEventListener('ssh-reconnect-trigger', handleReconnectTrigger);
    return () => window.removeEventListener('ssh-reconnect-trigger', handleReconnectTrigger);
  }, [cancelAutoReconnect, reconnectSession]);

  // ── Connect to server ──────────────────────────────────────
  const connectServerInner = useCallback(async (server: config.Connection) => {
    markWorkspaceRestoreNavigationOverride();
    // 用户主动点连即记入最近，已连接仅切换焦点时也置顶
    recordRecentConnection(server?.id);
    await waitForServerDisconnect(server?.id);
    const existing = sessionsRef.current.find((s) => s.serverId === server.id && s.status !== 'closed' && s.status !== 'error');
    if (existing) {
      setActiveSessionId(existing.id!);
      setActiveTerminalId(resolveSessionRootTerminalId(existing, lastTerminalRef.current[existing.id!]));
      setContentTab(resolveSessionContentTab(existing.id!));
      return;
    }

    const sessionSnapshot = rememberWorkspace && workspacePersistenceLevel === 'session'
      ? await loadServerWorkspaceSessionSnapshot(server.id)
      : null;
    const closedSession = sessionsRef.current.find((s) => s.serverId === server.id && (s.status === 'closed' || s.status === 'error'));
    if (closedSession) {
      if (restoringWorkspaceRef.current && restoringWorkspaceSessionIds.has(closedSession.id!)) {
        setActiveSessionId(closedSession.id!);
        setActiveTerminalId(resolveSessionRootTerminalId(closedSession, lastTerminalRef.current[closedSession.id!]));
        setContentTab(resolveSessionContentTab(closedSession.id!));
        return;
      }
      const restoreTerminals = normalizeAIWorkspaceReconnectTerminals(
        sessionSnapshot?.terminals || closedSession.terminals,
        sessionSnapshot?.sessionId || closedSession.id,
        closedSession.id!,
        `${t('终端')}1`,
        sessionSnapshot?.aiTabWorkspaces,
      );
      setActiveSessionId(closedSession.id!);
      setContentTab(resolveSessionContentTab(closedSession.id!));
      const result = await reconnectSession({
        ...closedSession,
        terminals: restoreTerminals,
      });
      if (!result) {
        return;
      }
      if (sessionSnapshot) {
        const currentAITabWorkspaces = { ...getAllAIWorkspaceTabGroups() };
        Object.keys(sessionSnapshot.aiTabWorkspaces || {}).forEach((terminalId) => {
          delete currentAITabWorkspaces[terminalId];
        });
        replaceAllAIWorkspaceTabGroups({
          ...currentAITabWorkspaces,
          ...remapAIWorkspaceTabSnapshotGroups(sessionSnapshot.aiTabWorkspaces, result.oldToNew),
        });
      }
      const previousActiveTerminalId = String(sessionSnapshot?.activeTerminalId || closedSession.activeTerminalId || '');
      const nextActiveTerminalId = result.oldToNew[previousActiveTerminalId] || result.newTerminals[0]?.id || closedSession.id!;
      const nextActiveTerminalLabel = result.newTerminals.find((terminal) => terminal.id === nextActiveTerminalId)?.label || '';
      lastTerminalRef.current[closedSession.id!] = nextActiveTerminalId;
      rememberSessionActiveTerminal(closedSession.id!, nextActiveTerminalId, nextActiveTerminalLabel);
      setActiveTerminalId(nextActiveTerminalId);
      return;
    }
    const sessionId = `session_${Date.now()}`;
    const restoredTerminals = sessionSnapshot
      ? normalizeAIWorkspaceReconnectTerminals(
          sessionSnapshot.terminals,
          sessionSnapshot.sessionId,
          sessionId,
          `${t('终端')}1`,
          sessionSnapshot.aiTabWorkspaces,
        )
      : [{ id: sessionId, label: `${t('终端')}1` }];
    const newSession: SessionLike = {
      id: sessionId,
      serverId: server.id,
      serverName: server.name || server.host,
      host: server.host,
      status: 'connecting',
      terminals: restoredTerminals,
    };

    const nextSessions = [...sessionsRef.current, newSession];
    sessionsRef.current = nextSessions;
    setSessions(nextSessions);
    setActiveSessionId(sessionId);
    setActiveTerminalId(sessionId);
    setContentTab('terminal');
    setConnectingServers((prev) => [...prev, { server, sessionId, startTime: Date.now() }]);

    try {
      if (sessionSnapshot) {
        const result = await reconnectSession(newSession, undefined, { deferState: true });
        if (!result) {
          return;
        }
        const restoredSession = { ...newSession, status: 'connected', terminals: result.newTerminals };
        const restoredLayouts = remapSessionWorkspaceLayouts(sessionSnapshot.terminalPaneLayouts || {}, result.oldToNew, sessionId);
        const mergedLayouts = { ...terminalPaneLayoutsRef.current, ...restoredLayouts };
        const currentWorkspaces = { ...getAllSessionFileManagerWorkspaces() };
        const remappedSnapshotWorkspaces = remapSessionFileManagerWorkspaceMap(sessionSnapshot.fileManagerWorkspaces || {}, result.oldToNew);
        const currentAITabWorkspaces = { ...getAllAIWorkspaceTabGroups() };
        const remappedSnapshotAITabWorkspaces = remapAIWorkspaceTabSnapshotGroups(
          sessionSnapshot.aiTabWorkspaces,
          result.oldToNew,
        );
        Object.keys(sessionSnapshot.fileManagerWorkspaces || {}).forEach((terminalId) => {
          delete currentWorkspaces[terminalId];
        });
        replaceAllSessionFileManagerWorkspaces({
          ...currentWorkspaces,
          ...remappedSnapshotWorkspaces,
        });
        Object.keys(sessionSnapshot.aiTabWorkspaces || {}).forEach((terminalId) => {
          delete currentAITabWorkspaces[terminalId];
        });
        replaceAllAIWorkspaceTabGroups({
          ...currentAITabWorkspaces,
          ...remappedSnapshotAITabWorkspaces,
        });
        sessionsRef.current = sessionsRef.current.map((item) => (
          item.id === sessionId ? restoredSession : item
        ));
        setSessions((prev) => prev.map((item) => (
          item.id === sessionId ? restoredSession : item
        )));
        terminalPaneLayoutsRef.current = mergedLayouts;
        setTerminalPaneLayouts((prev) => ({ ...prev, ...restoredLayouts }));
        const preferredTerminalId = result.oldToNew[String(sessionSnapshot.activeTerminalId)] || result.newTerminals[0]?.id || sessionId;
        const nextActiveTerminalId = resolveSessionRootTerminalId(restoredSession, preferredTerminalId, mergedLayouts) || result.newTerminals[0]?.id || sessionId;
        const snapshotContentTab = normalizeWorkspaceContentTab(sessionSnapshot.contentTab || 'terminal');
        const nextContentTab = fileManagerPosition === 'tab'
          ? snapshotContentTab
          : (snapshotContentTab === 'files' ? 'terminal' : snapshotContentTab);
        lastTerminalRef.current[sessionId] = nextActiveTerminalId;
        setActiveTerminalId(nextActiveTerminalId);
        setContentTab(nextContentTab);
        lastContentTabRef.current[sessionId] = nextContentTab;
        persistWorkspaceSnapshotRef.current?.({
          sessions: sessionsRef.current,
          activeSessionId: sessionId,
          activeTerminalId: nextActiveTerminalId,
          terminalPaneLayouts: mergedLayouts,
        });
        return;
      }

      await AppGo.ConnectSSH(sessionId, server.id);
      setSessions((prev) =>
        prev.map((s) => (s.id === sessionId ? { ...s, status: 'connected' } : s))
      );
      setConnectingServers((prev) => prev.filter((s) => s.sessionId !== sessionId));
      await postConnectSetup(sessionId, server.id);
    } catch (err) {
      handleConnectError(sessionId, err);
    }
  }, [fileManagerPosition, handleConnectError, loadServerWorkspaceSessionSnapshot, markWorkspaceRestoreNavigationOverride, postConnectSetup, reconnectSession, recordRecentConnection, rememberWorkspace, resolveSessionContentTab, resolveSessionRootTerminalId, restoringWorkspaceSessionIds, t, waitForServerDisconnect, workspacePersistenceLevel]);

  // ponytail: 防双击/重复点击。双击服务器卡片会连续触发两次 connectServer，
  // 而 connectServerInner 开头有 await（waitForServerDisconnect），两次调用
  // 都能穿过 existing/closedSession 检查，各自 ConnectSSH → 服务器上开出
  // 两个 /bin/bash（两个终端通道），通道占用虚高且标签页重复。
  // 同一服务器连接进行中时直接忽略后续触发。
  const connectServer = useCallback(async (server: config.Connection) => {
    const serverId = String(server?.id || '');
    if (!serverId) {
      return;
    }
    if (connectingServerIdsRef.current.has(serverId)) {
      return;
    }
    connectingServerIdsRef.current.add(serverId);
    try {
      await connectServerInner(server);
    } finally {
      connectingServerIdsRef.current.delete(serverId);
    }
  }, [connectServerInner]);

  const connectLocal = useCallback((name: string, shellPath: string) => {
    markWorkspaceRestoreNavigationOverride();
    const sessionId = `session_${Date.now()}`;
    const newSession: SessionLike = {
      id: sessionId,
      serverId: `local_${shellPath}`,
      serverName: name,
      host: 'localhost',
      status: 'connecting',
      terminals: [{ id: sessionId, label: name }],
      isLocal: true,
      shellPath: shellPath,
      wsRebuildKey: 0,
    };
    const nextSessions = [...sessionsRef.current, newSession];
    sessionsRef.current = nextSessions;
    setSessions(nextSessions);
    setActiveSessionId(sessionId);
    setActiveTerminalId(sessionId);
    setContentTab('terminal');
    setConnectingServers((prev) => [...prev, { server: { id: String(newSession.serverId), name, host: 'localhost' }, sessionId, startTime: Date.now() }]);

    window.go.wailsapp.App.ConnectLocal(sessionId, name, shellPath, '')
      .then(() => {
        setSessions((prev) =>
          prev.map((s) => (s.id === sessionId ? { ...s, status: 'connected' } : s))
        );
        setConnectingServers((prev) => prev.filter((s) => s.sessionId !== sessionId));
        // 与 SSH 连接保持一致：连接成功后查询静态信息并自动启用系统监控。
        // postConnectSetup 内部对 serverId 相关调用有兜底，本地 serverId 无副作用。
        void postConnectSetup(sessionId, String(newSession.serverId));
      })
      .catch((err) => {
        handleConnectError(sessionId, err);
      });
  }, [handleConnectError, markWorkspaceRestoreNavigationOverride, postConnectSetup]);

  const connectSerial = useCallback((config: { port: string; baudRate: number; dataBits: number; stopBits: number; parity: string }) => {
    markWorkspaceRestoreNavigationOverride();
    const sessionId = `session_${Date.now()}`;
    const displayName = `${config.port}@${config.baudRate}`;
    const newSession: SessionLike = {
      id: sessionId,
      serverId: `serial_${config.port}`,
      serverName: displayName,
      host: config.port,
      status: 'connecting',
      terminals: [{ id: sessionId, label: displayName }],
      isSerial: true,
      serialConfig: config,
      wsRebuildKey: 0,
    };
    const nextSessions = [...sessionsRef.current, newSession];
    sessionsRef.current = nextSessions;
    setSessions(nextSessions);
    setActiveSessionId(sessionId);
    setActiveTerminalId(sessionId);
    setContentTab('terminal');
    setConnectingServers((prev) => [...prev, { server: { id: String(newSession.serverId), name: displayName, host: config.port }, sessionId, startTime: Date.now() }]);

    window.go.wailsapp.App.ConnectSerial(
      sessionId,
      displayName,
      config.port,
      config.baudRate,
      config.dataBits,
      config.stopBits,
      config.parity
    )
      .then(() => {
        setSessions((prev) =>
          prev.map((s) => (s.id === sessionId ? { ...s, status: 'connected' } : s))
        );
        setConnectingServers((prev) => prev.filter((s) => s.sessionId !== sessionId));
      })
      .catch((err) => {
        handleConnectError(sessionId, err);
      });
  }, [handleConnectError, markWorkspaceRestoreNavigationOverride]);

  // ── Close session ──────────────────────────────────────────
  // ponytail: 内部关闭逻辑，不带确认弹窗，供 closeSession 和右键菜单共用
  const forceCloseSession = useCallback((sessionId: string) => {
    const session = sessionsRef.current.find(s => s.id === sessionId);
    if (session) {
      persistServerWorkspaceSessionSnapshot(session, {
        session,
        terminalPaneLayouts: terminalPaneLayoutsRef.current,
        activeTerminalId: activeSessionIdRef.current === sessionId ? activeTerminalIdRef.current : lastTerminalRef.current[sessionId],
        contentTab: normalizeWorkspaceContentTab(activeSessionIdRef.current === sessionId ? contentTabRef.current : (lastContentTabRef.current[sessionId] || 'terminal')),
      });
    }
    const termIds = Array.from(new Set([
      sessionId,
      ...(session?.terminals || []).map((terminal) => terminal.id as string),
    ].filter(Boolean)));
    const disconnectPromise = disconnectSessionConnection(sessionId, termIds);
    if (session?.serverId) {
      registerServerDisconnect(String(session.serverId), disconnectPromise);
    }
    setSessions((prev) => {
      const next = prev.filter((s) => s.id !== sessionId);
      if (next.length === 0) {
        window?.go?.wailsapp?.App?.ClearWorkspaceState?.().catch(() => { });
      }
      return next;
    });
    setTerminalPaneLayouts((prev) => {
      const next = { ...prev };
      Object.entries(next).forEach(([layoutId, layout]) => {
        if (layout?.sessionId === sessionId) {
          delete next[layoutId];
        }
      });
      return next;
    });
    delete terminalSubTabScrollBySessionRef.current[sessionId];
    if (activeSessionIdRef.current === sessionId) {
      switchToNextSession(sessionId);
    }
    if (connectingServersRef.current.some((s) => s.sessionId === sessionId)) {
      setConnectingServers((prev) => prev.filter((s) => s.sessionId !== sessionId));
    }
    termIds.forEach((terminalId) => clearAIWorkspaceTabGroup(terminalId));
    clearSessionAuthPrompt(sessionId);
  }, [clearAIWorkspaceTabGroup, clearSessionAuthPrompt, disconnectSessionConnection, persistServerWorkspaceSessionSnapshot, registerServerDisconnect, switchToNextSession]);

  const closeSession = useCallback(async (sessionId: string, e?: React.MouseEvent) => {
    e?.stopPropagation();
    if (localStorage.getItem('skipCloseSessionConfirm') === 'true') {
      forceCloseSession(sessionId);
      return;
    }
    const session = sessionsRef.current.find(s => s.id === sessionId);
    const name = session?.serverName || session?.name || session?.host || sessionId;
    const result = await window.luminDialog?.confirm(`${t('确定关闭连接')}「${name}」？`, t('操作确认'), t('不再询问'));
    if (!result || typeof result !== 'object') return;
    if (!result.confirmed) return;
    if (result.checked) localStorage.setItem('skipCloseSessionConfirm', 'true');
    forceCloseSession(sessionId);
  }, [forceCloseSession, t]);

  // ponytail: 批量关闭 — 一次性断开所有终端再清空 state，避免逐个 forceClose 反复触发 switchToNextSession
  const closeAllSessions = useCallback(async () => {
    const all = sessionsRef.current;
    if (all.length === 0) return;
    const skip = localStorage.getItem('skipCloseAllConfirm') === 'true';
    if (!skip) {
      const result = await window.luminDialog?.confirm(`${t('确定关闭全部')} ${all.length} ${t('个连接')}？`, t('操作确认'), t('不再询问'));
      if (!result || typeof result !== 'object') return;
      if (!result.confirmed) return;
      if (result.checked) localStorage.setItem('skipCloseAllConfirm', 'true');
    }
    all.forEach((session) => {
      persistServerWorkspaceSessionSnapshot(session, {
        session,
        terminalPaneLayouts: terminalPaneLayoutsRef.current,
        activeTerminalId: activeSessionIdRef.current === session.id ? activeTerminalIdRef.current : lastTerminalRef.current[session.id!],
        contentTab: normalizeWorkspaceContentTab(activeSessionIdRef.current === session.id ? contentTabRef.current : (lastContentTabRef.current[session.id!] || 'terminal')),
      });
    });
    const disconnectPromise = Promise.allSettled(all.map((session) => disconnectSessionConnection(
      session.id!,
      (session.terminals || []).map((terminal) => terminal.id as string),
    )));
    all
      .map((session) => session?.serverId)
      .filter(Boolean)
      .forEach((serverId) => registerServerDisconnect(String(serverId), disconnectPromise));
    window?.go?.wailsapp?.App?.ClearWorkspaceState?.().catch(() => { });
    setSessions([]);
    setTerminalPaneLayouts({});
    terminalSubTabScrollBySessionRef.current = {};
    setActiveSessionId(null);
    setActiveTerminalId(null);
    setConnectingServers([]);
    all.flatMap((session) => (session.terminals || []).map((terminal) => terminal.id as string)).forEach((terminalId) => clearAIWorkspaceTabGroup(terminalId));
    setSessionAuthPrompts({});
  }, [clearAIWorkspaceTabGroup, disconnectSessionConnection, persistServerWorkspaceSessionSnapshot, registerServerDisconnect, t]);

  // ── 在当前服务器上新建终端标签 ──────────────────────────────
  const openNewTerminal = useCallback(async (sessionId: string, options: {
    sourceTerminalId?: string;
    cloneFileManagerWorkspace?: boolean;
    cloneCwd?: boolean;
  } = {}) => {
    markWorkspaceRestoreNavigationOverride();
    if (creatingTerminalRef.current) return;

    const session = sessionsRef.current.find(s => s.id === sessionId);
    if (!session || session.status !== 'connected') return;

    creatingTerminalRef.current = sessionId;
    setCreatingTerminalSessionId(sessionId);

    const baseTermId = session.terminals?.[0]?.id as string || sessionId;
    const sourceTerminalId = typeof options?.sourceTerminalId === 'string' && options.sourceTerminalId.trim()
      ? options.sourceTerminalId.trim()
      : baseTermId;
    const cloneFileManagerWorkspace = options?.cloneFileManagerWorkspace === true;
    const cloneCwd = options?.cloneCwd === true;
    const sourceWorkspace = cloneFileManagerWorkspace
      ? cloneSessionFileManagerWorkspaceState(getSessionFileManagerWorkspace(sourceTerminalId))
      : null;
    const sourceCwdPromise = cloneCwd
      ? Promise.resolve(AppGo.GetTerminalCwd(sourceTerminalId))
        .then((value) => String(value || '').trim())
        .catch(() => '')
      : Promise.resolve('');

    // 新 tab 命名为 "<serverName> <序号>"。序号取所有现有标签中最大的「编号后缀」+1,
    // 目的是无论历史标签是哪种格式都不重名：
    //   - 本地终端根标签 == serverName（如 "PowerShell 7"），后续 tab 为 "PowerShell 7 2/3…"；
    //   - SSH 根标签是 "终端N"（与 serverName 不同），旧逻辑用 ^serverName\s+\d+$ 一个都匹配不上，
    //     导致新 tab 永远叫 "serverName 2" 甚至和已有的 "终端2" 重名。
    // 编号后缀的判定以 serverName 为参照前缀：去掉前缀后剩余部分是纯数字才算编号
    // （避免把 serverName 自带的数字，如 "PowerShell 7" 的 7，误当成编号）；
    // 与 serverName 完全无关的标签（历史 "终端N" 或用户改的 "test2"）则直接取其尾部数字，
    // 取全局最大值，保证新 tab 不与任何已有标签重名。
    const baseName = String(session.serverName || t('终端'));
    let maxNum = 0; // 无任何带编号 tab 时，首个新 tab 落在 2
    (session.terminals || []).forEach(term => {
      const label = String(term?.label || '').trim();
      if (!label) return;
      let n: number | null = null;
      if (label === baseName) {
        n = null; // 根标签，无编号后缀
      } else if (label.startsWith(baseName)) {
        // 形如 "<baseName> 2" / "<baseName>2" / "<baseName>-2"，去掉前缀后剩纯数字才算编号
        const rest = label.slice(baseName.length);
        const m = rest.match(/^\s*(\d+)$/);
        n = m ? parseInt(m[1], 10) : null;
      } else {
        // 与 serverName 无关的标签（历史 "终端N" 或用户改名），直接取尾部数字
        const m = label.match(/(\d+)\s*$/);
        n = m ? parseInt(m[1], 10) : null;
      }
      if (n != null) maxNum = Math.max(maxNum, n);
    });
    const termLabel = `${baseName} ${Math.max(1, maxNum) + 1}`;

    try {
      const newTermId = await AppGo.OpenTerminal(baseTermId);
      const nextSessions = sessionsRef.current.map((s) => (
        s.id === sessionId
          ? {
            ...s,
            terminals: [...(s.terminals || []), { id: newTermId, label: termLabel }],
            activeTerminalId: newTermId,
            activeTerminalLabel: termLabel,
          }
          : s
      ));
      sessionsRef.current = nextSessions;
      terminalSubTabScrollBySessionRef.current[sessionId] = Number.MAX_SAFE_INTEGER;
      setSessions(nextSessions);
      setActiveTerminalId(newTermId);
      setContentTab('terminal');
      lastTerminalRef.current[sessionId] = newTermId;
      if (sourceWorkspace) {
        setSessionFileManagerWorkspace(newTermId, sourceWorkspace);
      }
      void sourceCwdPromise.then((sourceCwd) => {
        const command = buildTerminalCloneCwdCommand(sourceCwd);
        if (!command) {
          return;
        }
        window.setTimeout(() => {
          try {
            AppGo.WriteTerminal(newTermId, command);
          } catch { }
        }, 80);
      });
      persistWorkspaceSnapshotRef.current?.({
        sessions: nextSessions,
        activeSessionId: sessionId,
        activeTerminalId: newTermId,
      });
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          const el = terminalSubTabScrollRef.current;
          if (!el) return;
          const maxLeft = Math.max(0, el.scrollWidth - el.clientWidth);
          const nextLeft = maxLeft;
          terminalSubTabScrollBySessionRef.current[sessionId] = nextLeft;
          terminalSubTabScrollTargetRef.current = nextLeft;
          el.scrollLeft = nextLeft;
          setTerminalSubTabOverflow(maxLeft > 1);
        });
      });
    } catch (err) {
      addToast(`${t('新建终端失败')}: ${String(err)}`, 'error', 5000);
    } finally {
      creatingTerminalRef.current = null;
      if (mountedRef.current) setCreatingTerminalSessionId(null);
    }
  }, [addToast, markWorkspaceRestoreNavigationOverride, t]);

  const handleRenameTerminalTab = useCallback(async (sessionId: string, terminalId: string) => {
    const session = sessionsRef.current.find((item) => item.id === sessionId);
    const currentTerminals = Array.isArray(session?.terminals) && session.terminals.length > 0
      ? session.terminals
      : (session ? [{ id: session.id!, label: `${t('终端')}1` }] : []);
    const targetTerminal = currentTerminals.find((item) => item.id === terminalId);
    if (!session || !targetTerminal) {
      return;
    }
    const currentLabel = String(targetTerminal.label || '').trim() || t('终端');
    const nextLabel = await window.luminDialog?.prompt(`${t('标签标题')}: ${currentLabel}`);
    if (nextLabel === null || nextLabel === undefined) {
      return;
    }
    const trimmedLabel = typeof nextLabel === 'object' ? String(nextLabel.value || '').trim() : String(nextLabel).trim();
    if (!trimmedLabel || trimmedLabel === currentLabel) {
      return;
    }
    const nextSessions = sessionsRef.current.map((item) => (
      item.id === sessionId
        ? {
          ...item,
          terminals: (Array.isArray(item.terminals) && item.terminals.length > 0 ? item.terminals : currentTerminals).map((term) => (
            term.id === terminalId
              ? { ...term, label: trimmedLabel }
              : term
          )),
        }
        : item
    ));
    sessionsRef.current = nextSessions;
    setSessions(nextSessions);
    persistWorkspaceSnapshotRef.current?.({
      sessions: nextSessions,
      activeSessionId: activeSessionIdRef.current,
      activeTerminalId: activeTerminalIdRef.current,
      terminalPaneLayouts: terminalPaneLayoutsRef.current,
    });
  }, [t]);

  // ── 关闭单个终端标签 ──────────────────────────────────────
  const closeTerminal = useCallback((sessionId: string, terminalId: string, e?: React.MouseEvent) => {
    e?.stopPropagation();
    const session = sessionsRef.current.find(s => s.id === sessionId);
    if (!session?.terminals) return;

    const remaining = (session.terminals || []).filter(t => t.id !== terminalId);
    if (remaining.length === 0) {
      persistServerWorkspaceSessionSnapshot(session, {
        session,
        terminalPaneLayouts: terminalPaneLayoutsRef.current,
        activeTerminalId: activeSessionIdRef.current === sessionId ? activeTerminalIdRef.current : lastTerminalRef.current[sessionId],
        contentTab: normalizeWorkspaceContentTab(activeSessionIdRef.current === sessionId ? contentTabRef.current : (lastContentTabRef.current[sessionId] || 'terminal')),
      });
    }
    clearAIWorkspaceTabGroup(terminalId);
    const disconnectPromise = disconnectSessionTerminals([terminalId]);
    if (remaining.length === 0 && session?.serverId) {
      registerServerDisconnect(String(session.serverId), disconnectPromise);
    }

    setSessions((prev) => {
      const next = prev.map((s) => {
        if (s.id !== sessionId) return s;
        if (remaining.length === 0) return null;
        return { ...s, terminals: remaining };
      }).filter((s): s is SessionLike => s !== null);
      if (next.length === 0) {
        window?.go?.wailsapp?.App?.ClearWorkspaceState?.().catch(() => { });
      }
      return next;
    });

    if (remaining.length === 0) {
      setMountedSessions(prev => {
        if (!prev.has(sessionId)) return prev;
        const next = new Set(prev);
        next.delete(sessionId);
        return next;
      });
      if (activeSessionIdRef.current === sessionId) {
        switchToNextSession(sessionId);
      }
      return;
    }

    if (activeSessionIdRef.current === sessionId && activeTerminalIdRef.current === terminalId) {
      setActiveTerminalId(resolveSessionRootTerminalId({ ...session, terminals: remaining }, lastTerminalRef.current[sessionId]));
    }
  }, [clearAIWorkspaceTabGroup, disconnectSessionTerminals, persistServerWorkspaceSessionSnapshot, registerServerDisconnect, resolveSessionRootTerminalId, switchToNextSession]);

  return {
    handleConnectError, postConnectSetup, loadServers, handleCancelConnection,
    resolveSessionContentTab, switchToNextSession, handleTabClick,
    canCopySessionPassword, handleCopySessionPassword, reconnectSession,
    resolveHostKeyChoice, resolvePasswordPrompt, handleCloseWindow,
    connectServer, connectLocal, connectSerial, forceCloseSession,
    closeSession, closeAllSessions, openNewTerminal, handleRenameTerminalTab, closeTerminal,
  };
}
