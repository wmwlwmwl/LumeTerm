import { useCallback, useMemo } from 'react';
import * as AppGo from '../../wailsjs/go/wailsapp/App.js';
import type { config } from '../../wailsjs/go/models.ts';

/** 服务器表单数据（编辑器中输入，可能缺失部分字段） */
export interface ServerFormData {
  id?: string;
  name?: string;
  host: string;
  port?: string | number;
  username: string;
  group?: string;
  [key: string]: unknown;
}

export interface UseServerCatalogOptions {
  servers: config.Connection[];
  serversRef: React.RefObject<config.Connection[]>;
  searchQuery: string;
  selectedServerIds: string[];
  loadServers: () => Promise<void> | void;
  addToast: (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;
  removeRecentConnection: (serverId: string) => void;
  removeRecentConnections: (serverIds: unknown) => void;
  setServers: React.Dispatch<React.SetStateAction<config.Connection[]>>;
  setServerEditor: React.Dispatch<React.SetStateAction<{ id?: string } | null>>;
  setSelectedServerIds: React.Dispatch<React.SetStateAction<string[]>>;
  setBatchSelectionMode: (value: boolean) => void;
  startSaveFlowAnimation: (saved: config.Connection, data: ServerFormData) => void;
  connectServer: (server: config.Connection) => void;
  t: (key: string, vars?: Record<string, unknown>) => string;
}

export interface UseServerCatalogResult {
  saveServerConfig: (data: ServerFormData) => Promise<config.Connection | null>;
  handleSaveServer: (data: ServerFormData, shouldClearAfterAdd?: boolean) => Promise<config.Connection | null>;
  handleDeleteServer: (id: string) => Promise<void>;
  handleBatchDelete: (ids: string[]) => Promise<void>;
  handleGroupDelete: (groupName: string, ids: string[]) => Promise<void>;
  handleRenameGroup: (groupName: string) => Promise<string | boolean>;
  handleBatchConnect: (ids: string[]) => Promise<void>;
  handleBatchMoveGroup: (ids: string[], group: string) => Promise<void>;
  toggleBatchSelection: (idOrArray: string | Array<string | { id: string; selected: boolean }>) => void;
  filteredServers: config.Connection[];
  allGroups: string[];
  handleMoveGroup: (serverId: string, group: string) => Promise<void>;
}

export default function useServerCatalog({
  servers,
  serversRef,
  searchQuery,
  selectedServerIds,
  loadServers,
  addToast,
  removeRecentConnection,
  removeRecentConnections,
  setServers,
  setServerEditor,
  setSelectedServerIds,
  setBatchSelectionMode,
  startSaveFlowAnimation,
  connectServer,
  t,
}: UseServerCatalogOptions): UseServerCatalogResult {
  const saveServerConfig = useCallback(async (data: ServerFormData) => {
    const duplicate = serversRef.current.some((server) => (
      server.id !== data.id
      && server.host === data.host
      && (server.port || 22) === (Number.parseInt(String(data.port), 10) || 22)
      && server.username === data.username
    ));
    if (duplicate) {
      addToast(t('已存在相同主机、端口和用户名的服务器'), 'error');
      return null;
    }
    const savedServer = await AppGo.SaveConnection(data as unknown as config.Connection, false);
    await loadServers();
    return savedServer;
  }, [addToast, loadServers, serversRef, t]);

  const handleSaveServer = useCallback(async (data: ServerFormData, shouldClearAfterAdd = true) => {
    try {
      const savedServer = await saveServerConfig(data);
      if (!savedServer) return null;
      if (data.id) {
        startSaveFlowAnimation(savedServer, data);
      } else {
        addToast(t('服务器添加成功'), 'success');
        if (shouldClearAfterAdd) setServerEditor(null);
      }
      return savedServer;
    } catch (error) {
      addToast(error instanceof Error ? error : String(error), 'error');
      return null;
    }
  }, [addToast, saveServerConfig, setServerEditor, startSaveFlowAnimation, t]);

  const handleDeleteServer = useCallback(async (id: string) => {
    try {
      await AppGo.DeleteConnection(id);
      setServers((prev) => prev.filter((server) => server.id !== id));
      removeRecentConnection(id);
      setServerEditor((current) => (current?.id === id ? null : current));
      addToast(t('服务器已删除'), 'success');
    } catch {
      addToast(t('删除失败'), 'error');
    }
  }, [addToast, removeRecentConnection, setServerEditor, setServers, t]);

  const handleBatchDelete = useCallback(async (ids: string[]) => {
    try {
      await AppGo.BatchDeleteConnections(ids);
      setServers((prev) => prev.filter((server) => !ids.includes(server.id)));
      removeRecentConnections(ids);
      setSelectedServerIds([]);
      setServerEditor((current) => (current?.id && ids.includes(current.id) ? null : current));
      addToast(t('服务器已删除'), 'success');
    } catch {
      addToast(t('删除失败'), 'error');
    }
  }, [addToast, removeRecentConnections, setSelectedServerIds, setServerEditor, setServers, t]);

  const handleGroupDelete = useCallback(async (groupName: string, ids: string[]) => {
    if (await window.lumeDialog?.confirm(t('确定删除「{group}」分组的 {count} 个服务器？', { group: groupName, count: ids.length }))) {
      await handleBatchDelete(ids);
    }
  }, [handleBatchDelete, t]);

  const handleRenameGroup = useCallback(async (groupName: string) => {
    const next = await window.lumeDialog?.prompt(t('请输入新的分组名称'), groupName, t('重命名分组'), '', {
      validate: async (value: string) => {
        const trimmed = String(value ?? '').trim();
        if (!trimmed) return t('分组名称不能为空');
        if (trimmed === groupName) return null;
        try {
          await AppGo.RenameConnectionGroup(groupName, trimmed);
          return null;
        } catch (error) {
          return String((error as { message?: unknown })?.message || (error instanceof Error ? error.message : error) || t('重命名失败'));
        }
      },
    });
    if (next === null || next === undefined) return false;
    const trimmed = String(next).trim();
    if (!trimmed || trimmed === groupName) return false;
    addToast(t('分组已重命名'), 'success');
    await loadServers();
    return trimmed;
  }, [addToast, loadServers, t]);

  const handleBatchConnect = useCallback(async (ids: string[]) => {
    const targets = servers.filter((server) => ids.includes(server.id));
    for (const server of targets) {
      connectServer(server);
      await new Promise((resolve) => setTimeout(resolve, 150));
    }
    setSelectedServerIds([]);
    setBatchSelectionMode(false);
  }, [connectServer, servers, setBatchSelectionMode, setSelectedServerIds]);

  const handleBatchMoveGroup = useCallback(async (ids: string[], group: string) => {
    try {
      await AppGo.BatchSetConnectionGroup(ids, group);
      addToast(t('已移动到分组') + (group ? `「${group}」` : ''), 'success');
    } catch (error) {
      addToast(String((error as { message?: unknown })?.message || (error instanceof Error ? error.message : error) || t('移动分组')), 'error');
    } finally {
      await loadServers();
      setSelectedServerIds([]);
    }
  }, [addToast, loadServers, setSelectedServerIds, t]);

  const toggleBatchSelection = useCallback((idOrArray: string | Array<string | { id: string; selected: boolean }>) => {
    if (Array.isArray(idOrArray)) {
      if (idOrArray.length === 0) {
        setSelectedServerIds([]);
        return;
      }
      if (idOrArray[0] && typeof idOrArray[0] === 'object' && 'selected' in idOrArray[0]) {
        const next = new Set(selectedServerIds);
        (idOrArray as Array<{ id: string; selected: boolean }>).forEach(({ id, selected }) => (selected ? next.add(id) : next.delete(id)));
        setSelectedServerIds([...next]);
        return;
      }
      setSelectedServerIds(idOrArray as string[]);
      return;
    }
    setSelectedServerIds((prev) => (
      prev.includes(idOrArray) ? prev.filter((id) => id !== idOrArray) : [...prev, idOrArray]
    ));
  }, [selectedServerIds, setSelectedServerIds]);

  const filteredServers = useMemo(() => {
    if (!searchQuery) return servers;
    const query = searchQuery.toLowerCase();
    return servers.filter((server) => [server.name, server.host, server.username, server.group]
      .some((value) => String(value || '').toLowerCase().includes(query)));
  }, [searchQuery, servers]);

  const allGroups = useMemo(() => {
    const groups = new Set(servers.map((server) => server.group).filter((group): group is string => !!group));
    return [...groups].sort((left, right) => left.localeCompare(right));
  }, [servers]);

  const handleMoveGroup = useCallback(async (serverId: string, group: string) => {
    try {
      await AppGo.SetConnectionGroup(serverId, group);
      await loadServers();
      addToast(t('已移动到分组') + (group ? `「${group}」` : ''), 'success');
    } catch (error) {
      addToast(error instanceof Error ? error : String(error), 'error');
    }
  }, [addToast, loadServers, t]);

  return {
    saveServerConfig,
    handleSaveServer,
    handleDeleteServer,
    handleBatchDelete,
    handleGroupDelete,
    handleRenameGroup,
    handleBatchConnect,
    handleBatchMoveGroup,
    toggleBatchSelection,
    filteredServers,
    allGroups,
    handleMoveGroup,
  };
}
