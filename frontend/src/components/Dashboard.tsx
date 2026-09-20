import { useEffect, useMemo, useRef, useState } from 'react';
import type { config } from '../../wailsjs/go/models.ts';
import type { DashboardHostPageMode, ServerListViewMode } from '../hooks/useDashboardPreferences.ts';
import type { ServerFormData } from '../hooks/useServerCatalog.ts';
import type { PingCounts, ServerPingResult } from '../hooks/useServerPing.ts';
import { useTranslation } from '../i18n.ts';
import { warnDev } from '../utils/devLog';
import AddServerModal from './AddServerModal.tsx';
import { DashboardBatchOperationBar } from './dashboard/DashboardBatchOperationBar.tsx';
import { DashboardHeaderActions } from './dashboard/DashboardHeaderActions.tsx';
import { DashboardRecentTable } from './dashboard/DashboardRecentTable.tsx';
import { DashboardStatusOverview } from './dashboard/DashboardStatusOverview.tsx';
import { type DashboardSessionLike } from './dashboard/dashboardTypes.ts';
import ServerList from './ServerList.tsx';
import type { MenuItem } from './ui';

export interface DashboardProps {
  editorServer: (config.Connection & { authType?: string }) | null;
  editorShiningFields?: Record<string, unknown>;
  saveFlowHighlights: { serverId: string | null; rowPulse: unknown; fields: Record<string, unknown> };
  isEditFlying?: boolean;
  onSaveServer: (data: ServerFormData, shouldClearAfterAdd?: boolean) => Promise<config.Connection | null>;
  onSaveAndConnectServer?: (data: ServerFormData, shouldClearAfterAdd?: boolean) => Promise<config.Connection | null>;
  onCancelEditor: () => void;
  allGroups: string[];
  credentials: config.Credential[];
  onOpenCredentials: () => void;
  searchQuery: string;
  onSearchChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
  hideSensitive: boolean;
  onHideSensitiveToggle: () => void;
  serverListViewMode: ServerListViewMode;
  onViewModeChange: (mode: ServerListViewMode) => void;
  servers: config.Connection[];
  pingEnabled: boolean;
  pingCounts: PingCounts;
  isRefreshingPing: boolean;
  onRefreshPing: () => void;
  filteredServers: config.Connection[];
  pings: Record<string, ServerPingResult>;
  sessions: DashboardSessionLike[];
  activeSessionId: string | null;
  recentConnectionIds?: string[];
  hostPageMode: DashboardHostPageMode;
  onHostPageModeChange?: (mode: DashboardHostPageMode) => void;
  onClearRecentConnections?: () => void;
  onRemoveRecentConnection?: (id: string) => void;
  onConnect: (server: config.Connection) => void;
  onStartAdd: () => void;
  onEdit: (server: config.Connection, payload: unknown) => void;
  onClone: (server: config.Connection, payload: unknown) => void;
  onDelete: (id: string) => void;
  onMoveGroup: (id: string, group: string) => void;
  addToast: (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;
  onOpenImportExport: () => void;
  selectionMode?: boolean;
  selectedIds?: string[];
  onSelectChange: (payload: string | string[] | Array<{ id: string; selected: boolean }>) => void;
  onBatchDelete?: (ids: string[]) => void;
  onBatchConnect?: (ids: string[]) => void;
  onBatchMoveGroup?: (ids: string[], group: string) => void;
  onGroupDelete: (groupName: string, ids: string[]) => void;
  onRenameGroup: (oldName: string) => string | null | Promise<string | null>;
  onSelectionModeToggle: () => void;
  onBatchExport?: (ids: string[]) => void;
  onExitSelectionMode?: () => void;
  onConnectLocal?: (name: string, shellPath: string) => void;
  onConnectSerial?: (config: { port: string; baudRate: number; dataBits: number; stopBits: number; parity: string }) => void;
  setShowSerialModal?: (v: boolean) => void;
}

export default function Dashboard({
  editorServer,
  editorShiningFields,
  saveFlowHighlights,
  isEditFlying: _isEditFlying = false,
  onSaveServer,
  onSaveAndConnectServer,
  onCancelEditor,
  allGroups,
  credentials,
  onOpenCredentials,
  searchQuery,
  onSearchChange,
  hideSensitive,
  onHideSensitiveToggle,
  serverListViewMode,
  onViewModeChange,
  servers,
  pingEnabled,
  pingCounts,
  isRefreshingPing,
  onRefreshPing,
  filteredServers,
  pings,
  sessions,
  activeSessionId,
  recentConnectionIds = [],
  hostPageMode: hostPageModeProp,
  onHostPageModeChange,
  onClearRecentConnections,
  onRemoveRecentConnection,
  onConnect,
  onStartAdd: _onStartAdd,
  onEdit,
  onClone,
  onDelete,
  onMoveGroup,
  addToast,
  onOpenImportExport,
  selectionMode = false,
  selectedIds = [],
  onSelectChange,
  onBatchDelete,
  onBatchConnect,
  onBatchMoveGroup,
  onGroupDelete,
  onRenameGroup,
  onSelectionModeToggle,
  onBatchExport,
  onExitSelectionMode,
  onConnectLocal,
  onConnectSerial: _onConnectSerial,
  setShowSerialModal,
}: DashboardProps) {
  const { t } = useTranslation();

  const [showMoveGroupDropdown, setShowMoveGroupDropdown] = useState(false);
  const [groupSearchQuery, setGroupSearchQuery] = useState('');
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(new Set());
  const [localMenuPos, setLocalMenuPos] = useState<{ x: number; y: number } | null>(null);
  const [localShells, setLocalShells] = useState<string[]>([]);
  const hostPageMode = hostPageModeProp === 'recent' ? 'recent' : 'hosts';
  const moveGroupMenuRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    window.go?.wailsapp?.App?.GetLocalShells?.()
      .then((list) => {
        setLocalShells(list || []);
      })
      .catch((err) => {
        warnDev('Failed to load local shells:', err);
      });
  }, []);

  const switchHostPageMode = (mode: string) => {
    onHostPageModeChange?.(mode === 'recent' ? 'recent' : 'hosts');
  };

  const recentServers = useMemo(() => {
    if (!Array.isArray(recentConnectionIds) || recentConnectionIds.length === 0) return [] as config.Connection[];
    const byId = new Map(servers.map((s) => [s.id, s]));
    return recentConnectionIds.map((id) => byId.get(id)).filter((server): server is config.Connection => !!server);
  }, [recentConnectionIds, servers]);

  const filteredRecentServers = useMemo(() => {
    const query = String(searchQuery || '').trim().toLowerCase();
    if (!query) return recentServers;
    return recentServers.filter((server) => {
      const haystack = [
        server.name,
        server.host,
        server.username,
        server.group,
        String(server.port || ''),
      ].join(' ').toLowerCase();
      return haystack.includes(query);
    });
  }, [recentServers, searchQuery]);

  const handleClearRecent = async () => {
    if (!recentServers.length) return;
    const ok = await window.lumeDialog?.confirm?.(t('确定清空最近连接列表吗？'), t('操作确认'));
    if (!ok) return;
    onClearRecentConnections?.();
  };

  useEffect(() => {
    function handleClickOutside(event: MouseEvent) {
      if (moveGroupMenuRef.current && !moveGroupMenuRef.current.contains(event.target as Node)) {
        setShowMoveGroupDropdown(false);
      }
    }
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  const existingGroups = useMemo(() => {
    const groups = new Set<string>();
    servers.forEach((s) => {
      if (s.group) groups.add(s.group);
    });
    return Array.from(groups).sort();
  }, [servers]);

  const filteredGroups = useMemo(() => {
    const query = groupSearchQuery.trim().toLowerCase();
    if (!query) return existingGroups;
    return existingGroups.filter((g) => g.toLowerCase().includes(query));
  }, [existingGroups, groupSearchQuery]);

  const localMenuItems = useMemo<MenuItem[]>(() => {
    const shellLabel = (sh: string) =>
      sh.startsWith('wsl://')
        ? `WSL - ${sh.slice(6)}`
        : sh === 'powershell.exe'
          ? 'Windows PowerShell'
          : sh === 'pwsh.exe' || sh.endsWith('pwsh.exe')
            ? 'PowerShell 7'
            : sh === 'cmd.exe'
              ? 'Command Prompt'
              : sh;
    return [
      { type: 'header', label: t('选择本地终端或串口') },
      ...localShells.map<MenuItem>((sh) => ({
        label: shellLabel(sh),
        onSelect: () => {
          setLocalMenuPos(null);
          onConnectLocal?.(shellLabel(sh), sh);
        },
      })),
      'separator',
      {
        label: t('串口终端...'),
        onSelect: () => {
          setLocalMenuPos(null);
          setShowSerialModal?.(true);
        },
      },
    ];
  }, [localShells, t, onConnectLocal, setShowSerialModal]);

  const visibleGroupNames = useMemo(() => {
    const groups = new Set<string>();
    filteredServers.forEach((s) => groups.add(s.group || ''));
    return Array.from(groups);
  }, [filteredServers]);
  const hasVisibleGroupHeaders = visibleGroupNames.length > 1 || (visibleGroupNames.length === 1 && visibleGroupNames[0] !== '');

  const allCollapsed = useMemo(() => {
    if (visibleGroupNames.length === 0) return false;
    return visibleGroupNames.every((name) => collapsedGroups.has(name));
  }, [visibleGroupNames, collapsedGroups]);

  const toggleCollapseAllGroups = () => {
    if (allCollapsed) {
      setCollapsedGroups(new Set());
    } else {
      setCollapsedGroups(new Set(visibleGroupNames));
    }
  };

  return (
    <div className="dashboard-container">
      <div className="dashboard-left">
        <AddServerModal
          inline
          server={editorServer}
          shiningFields={editorShiningFields}
          onSave={onSaveServer}
          onSaveAndConnect={onSaveAndConnectServer}
          onClose={onCancelEditor}
          allGroups={allGroups}
          credentials={credentials}
          onOpenCredentials={onOpenCredentials}
        />

        <DashboardStatusOverview
          servers={servers}
          pingEnabled={pingEnabled}
          pingCounts={pingCounts}
          isRefreshingPing={isRefreshingPing}
          onRefreshPing={onRefreshPing}
        />
      </div>

      <div className="dashboard-right">
        <div className="hosts-section-container">
          <DashboardHeaderActions
            hostPageMode={hostPageMode}
            switchHostPageMode={switchHostPageMode}
            recentServersCount={recentServers.length}
            searchQuery={searchQuery}
            onSearchChange={onSearchChange}
            localMenuPos={localMenuPos}
            setLocalMenuPos={setLocalMenuPos}
            localMenuItems={localMenuItems}
            selectionMode={selectionMode}
            onSelectionModeToggle={onSelectionModeToggle}
            serverListViewMode={serverListViewMode}
            onViewModeChange={onViewModeChange}
            hideSensitive={hideSensitive}
            onHideSensitiveToggle={onHideSensitiveToggle}
            hasVisibleGroupHeaders={hasVisibleGroupHeaders}
            allCollapsed={allCollapsed}
            onToggleCollapseAllGroups={toggleCollapseAllGroups}
            onOpenImportExport={onOpenImportExport}
            onClearRecent={handleClearRecent}
            hasRecentServers={recentServers.length > 0}
          />

          {hostPageMode === 'hosts' ? (
            <div className={`hosts-scroll-area ${selectionMode ? 'batch-mode-active' : ''}`}>
              <ServerList
                servers={filteredServers}
                pingEnabled={pingEnabled}
                pings={pings}
                sessions={sessions}
                activeSessionId={activeSessionId}
                saveFlowHighlights={saveFlowHighlights}
                viewMode={serverListViewMode}
                hideSensitive={hideSensitive}
                onConnect={onConnect}
                onEdit={onEdit}
                onClone={onClone}
                onDelete={onDelete}
                onMoveGroup={onMoveGroup}
                addToast={addToast}
                selectionMode={selectionMode}
                selectedIds={selectedIds}
                onSelectChange={onSelectChange}
                onBatchDelete={onBatchDelete}
                onBatchConnect={onBatchConnect}
                onBatchMoveGroup={onBatchMoveGroup}
                onGroupDelete={onGroupDelete}
                onRenameGroup={onRenameGroup}
                onBatchExport={onBatchExport}
                onExitSelectionMode={onExitSelectionMode}
                collapsedGroups={collapsedGroups}
                onCollapsedGroupsChange={setCollapsedGroups}
              />
            </div>
          ) : (
            <div className="hosts-scroll-area">
              <DashboardRecentTable
                recentServers={recentServers}
                filteredRecentServers={filteredRecentServers}
                sessions={sessions}
                activeSessionId={activeSessionId}
                hideSensitive={hideSensitive}
                onConnect={onConnect}
                onRemoveRecentConnection={onRemoveRecentConnection}
              />
            </div>
          )}

          {hostPageMode === 'hosts' && selectionMode && onBatchDelete && (
            <DashboardBatchOperationBar
              servers={servers}
              selectedIds={selectedIds}
              onSelectChange={onSelectChange}
              onExitSelectionMode={onExitSelectionMode}
              onBatchConnect={onBatchConnect}
              onBatchMoveGroup={onBatchMoveGroup}
              onBatchExport={onBatchExport}
              onBatchDelete={onBatchDelete}
              moveGroupMenuRef={moveGroupMenuRef}
              showMoveGroupDropdown={showMoveGroupDropdown}
              setShowMoveGroupDropdown={setShowMoveGroupDropdown}
              groupSearchQuery={groupSearchQuery}
              setGroupSearchQuery={setGroupSearchQuery}
              filteredGroups={filteredGroups}
            />
          )}
        </div>
      </div>
    </div>
  );
}
