import { useEffect, useCallback, useState, useRef } from 'react';
import {
  FILE_MANAGER_LAYOUT_MODE_SIDEBAR_DUAL,
  extractManualPinnedTabsFromWorkspace,
  mergeSharedPinnedTabsIntoWorkspaceTabs,
} from '../../utils/fileManagerHelpers.tsx';
import {
  setSessionFileManagerWorkspace,
  setSessionSharedPinnedTabs,
  subscribeSessionSharedPinnedTabs,
} from '../../utils/fileWorkbench.ts';
import type { FileManagerPaneState, FileManagerTabLike, FileManagerWorkspaceState } from '../../utils/fileWorkbench.ts';
import type { useFileManagerCore } from './useFileManagerCore.ts';
import type { useFileManagerTabScroll } from './useFileManagerTabScroll.ts';
import type { ContextMenuState, SyncTabOverrides } from './fileManagerTypes.ts';

// 工作区提交与同步：workspace 持久化提交、共享固定标签发布/合并、
// 当前标签状态回写（syncCurrentTabToWorkspace）与选中态恢复数据
export function useFileManagerWorkspaceSync(deps: ReturnType<typeof useFileManagerCore> & ReturnType<typeof useFileManagerTabScroll>) {
  const {
    sessionId, sessionGroupId, isActive,
    setFileManagerWorkspaceState, fileManagerWorkspaceRef,
    cwdSystemTabHighlightTimerRef, setCwdSystemTabHighlight,
    sortField, sortDir, draggingFileManagerTabIdRef, draggingFileManagerTabId,
    currentPathHydratedRef, preserveWorkspacePathRef,
    activePaneKey, fileManagerLayoutMode, fileListRef, mountedRef,
    getPaneSelectionRestore, setPaneSelectionRestore,
    getCachedTabItems, normalizePath, setLoading, setItems, setCurrentPath,
    activeFileManagerTabIdRef, displayedTabIdRef,
    fileManagerSharedPinnedTabsEnabled, fileManagerSharedPinnedTabsEnabledRef, applyingSharedPinnedTabsRef,
    cacheCurrentTabItems, cachePathItems, currentPathRef, currentPath,
    items,
  } = deps;
  const commitFileManagerWorkspace = useCallback((updater: Partial<FileManagerWorkspaceState> | ((current: FileManagerWorkspaceState) => Partial<FileManagerWorkspaceState>)) => {
    const next = setSessionFileManagerWorkspace(sessionId, updater);
    fileManagerWorkspaceRef.current = next;
    setFileManagerWorkspaceState(next);
    return next;
  }, [sessionId]);
  const publishSharedPinnedTabsFromWorkspace = useCallback((workspace: FileManagerWorkspaceState) => {
    if (!fileManagerSharedPinnedTabsEnabledRef.current || !sessionGroupId || applyingSharedPinnedTabsRef.current) {
      return;
    }
    setSessionSharedPinnedTabs(sessionGroupId, extractManualPinnedTabsFromWorkspace(workspace));
  }, [sessionGroupId]);
  useEffect(() => {
    if (!fileManagerSharedPinnedTabsEnabled || !sessionGroupId) {
      return undefined;
    }
    const localManualPinned = extractManualPinnedTabsFromWorkspace(fileManagerWorkspaceRef.current);
    if (localManualPinned.length > 0) {
      setSessionSharedPinnedTabs(sessionGroupId, (currentShared) => {
        const seenPaths = new Set(currentShared.map((tab) => tab.path).filter(Boolean));
        const appended = localManualPinned.filter((tab) => tab.path && !seenPaths.has(tab.path));
        if (appended.length === 0) {
          return currentShared;
        }
        return [...currentShared, ...appended];
      });
    }
    return subscribeSessionSharedPinnedTabs(sessionGroupId, (sharedTabs) => {
      applyingSharedPinnedTabsRef.current = true;
      try {
        commitFileManagerWorkspace((current) => ({
          ...current,
          tabs: mergeSharedPinnedTabsIntoWorkspaceTabs(current?.tabs, sharedTabs),
        }));
      } finally {
        applyingSharedPinnedTabsRef.current = false;
      }
    });
  }, [commitFileManagerWorkspace, fileManagerSharedPinnedTabsEnabled, sessionGroupId]);
  const triggerCwdSystemTabHighlight = useCallback((tabId: unknown) => {
    const normalizedTabId = String(tabId || '').trim();
    if (!normalizedTabId) {
      return;
    }
    if (cwdSystemTabHighlightTimerRef.current) {
      window.clearTimeout(cwdSystemTabHighlightTimerRef.current);
      cwdSystemTabHighlightTimerRef.current = 0;
    }
    setCwdSystemTabHighlight((current) => ({
      tabId: normalizedTabId,
      token: current.token + 1,
    }));
    cwdSystemTabHighlightTimerRef.current = window.setTimeout(() => {
      cwdSystemTabHighlightTimerRef.current = 0;
      setCwdSystemTabHighlight((current) => (
        current.tabId === normalizedTabId
          ? { ...current, tabId: '' }
          : current
      ));
    }, 1500);
  }, []);
  useEffect(() => { mountedRef.current = true; return () => { mountedRef.current = false; }; }, []);
  const openFileManagerPathInNewTabRef = useRef<unknown>(null);
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null); // { pos, item }
  const [selectedPaths, setSelectedPaths] = useState<string[]>([]);
  const lastClickedPathRef = useRef<string | null>(null);
  const sortFieldRef = useRef(sortField);
  const sortDirRef = useRef(sortDir);
  const selectedPathsRef = useRef(selectedPaths);
  useEffect(() => { sortFieldRef.current = sortField; }, [sortField]);
  useEffect(() => { sortDirRef.current = sortDir; }, [sortDir]);
  useEffect(() => { selectedPathsRef.current = selectedPaths; }, [selectedPaths]);
  useEffect(() => { draggingFileManagerTabIdRef.current = draggingFileManagerTabId; }, [draggingFileManagerTabId]);
  useEffect(() => {
    const pendingRestore = getPaneSelectionRestore(activePaneKey);
    if (pendingRestore) {
      const nextSelectedPaths = Array.isArray(pendingRestore.selectedPaths) ? pendingRestore.selectedPaths : [];
      setSelectedPaths(nextSelectedPaths);
      lastClickedPathRef.current = pendingRestore.lastClickedPath || nextSelectedPaths[nextSelectedPaths.length - 1] || null;
      setPaneSelectionRestore(activePaneKey, null);
    } else {
      setSelectedPaths([]);
      lastClickedPathRef.current = null;
    }
  }, [activePaneKey, currentPath, getPaneSelectionRestore, setPaneSelectionRestore]);
  useEffect(() => {
    if (!currentPathHydratedRef.current) return;
    const displayedTabId = displayedTabIdRef.current || '';
    if (displayedTabId) {
      cacheCurrentTabItems(displayedTabId, currentPathRef.current || currentPath, items);
    }
    cachePathItems(currentPath || currentPathRef.current || '/', items);
  }, [cacheCurrentTabItems, cachePathItems, currentPath, items]);
  const syncCurrentTabToWorkspace = useCallback((overrides: SyncTabOverrides = {}) => {
    const displayedTabId = String(displayedTabIdRef.current || '').trim();
    const activeTabId = String(activeFileManagerTabIdRef.current || '').trim();
    const workspaceTabId = displayedTabId || activeTabId;
    if (!sessionId) {
      return null;
    }
    const hasExplicitPath = Object.prototype.hasOwnProperty.call(overrides, 'path');
    return commitFileManagerWorkspace((currentWorkspace) => {
      const currentPanes = (currentWorkspace?.panes && typeof currentWorkspace.panes === 'object'
        ? currentWorkspace.panes
        : {}) as FileManagerWorkspaceState['panes'];
      const currentPane = (currentPanes[activePaneKey] && typeof currentPanes[activePaneKey] === 'object'
        ? currentPanes[activePaneKey]
        : {}) as FileManagerPaneState;
      const nextPath = hasExplicitPath
        ? (overrides.path ?? currentPathRef.current)
        : currentPathRef.current;
      const normalizedNextPath = normalizePath(nextPath) || '/';
      const nextPane = {
        ...currentPane,
        tabId: workspaceTabId || String(currentPane.tabId || currentWorkspace?.activeTabId || '').trim(),
        path: normalizedNextPath,
        sortField: overrides.sortField ?? sortFieldRef.current,
        sortDir: (overrides.sortDir ?? sortDirRef.current) === 'desc' ? 'desc' : 'asc',
        selectedPaths: Array.isArray(overrides.selectedPaths) ? overrides.selectedPaths : selectedPathsRef.current,
        scrollTop: Number.isFinite(Number(overrides.scrollTop)) ? Number(overrides.scrollTop) : (fileListRef.current?.scrollTop || 0),
      };
      const baseWorkspace = {
        ...currentWorkspace,
        activePane: activePaneKey,
        activeTabId: String(nextPane.tabId || currentWorkspace?.activeTabId || '').trim(),
        panes: {
          ...currentPanes,
          [activePaneKey]: nextPane,
        },
      };
      if (fileManagerLayoutMode === FILE_MANAGER_LAYOUT_MODE_SIDEBAR_DUAL || !workspaceTabId || !Array.isArray(currentWorkspace?.tabs) || currentWorkspace.tabs.length === 0) {
        return baseWorkspace;
      }
      return {
        ...baseWorkspace,
        tabs: currentWorkspace.tabs.map((tab) => {
          if (tab.id !== workspaceTabId) {
            return tab;
          }
          const normalizedTabPath = normalizePath(tab.path) || '/';
          return {
            ...tab,
            path: tab.pinned === true && normalizedNextPath !== normalizedTabPath ? tab.path : normalizedNextPath,
            sortField: overrides.sortField ?? sortFieldRef.current,
            sortDir: (overrides.sortDir ?? sortDirRef.current) === 'desc' ? 'desc' : 'asc',
            selectedPaths: Array.isArray(overrides.selectedPaths) ? overrides.selectedPaths : selectedPathsRef.current,
            scrollTop: Number.isFinite(Number(overrides.scrollTop)) ? Number(overrides.scrollTop) : (fileListRef.current?.scrollTop || 0),
          };
        }),
      };
    });
  }, [activePaneKey, commitFileManagerWorkspace, fileManagerLayoutMode, normalizePath, sessionId]);
  const restoreTabItemsFromCache = useCallback((tab: FileManagerTabLike, path: unknown) => {
    const resolvedTabId = String(tab?.id || '').trim();
    const resolvedPath = normalizePath(path ?? tab?.path) || '/';
    const cachedItems = getCachedTabItems(resolvedTabId, resolvedPath);
    if (!cachedItems) {
      return false;
    }
    displayedTabIdRef.current = resolvedTabId;
    currentPathHydratedRef.current = true;
    currentPathRef.current = resolvedPath;
    setLoading(false);
    setItems(cachedItems);
    setCurrentPath(resolvedPath);
    return true;
  }, [getCachedTabItems, normalizePath]);
  const isFileManagerTabLoadSuperseded = useCallback((tabId: unknown) => {
    const normalizedTabId = String(tabId || '').trim();
    const activeTabId = String(activeFileManagerTabIdRef.current || '').trim();
    return Boolean(normalizedTabId && activeTabId && normalizedTabId !== activeTabId);
  }, []);
  useEffect(() => {
    if (!isActive || !currentPathHydratedRef.current) return;
    syncCurrentTabToWorkspace({
      ...(preserveWorkspacePathRef.current ? {} : { path: currentPath }),
      scrollTop: fileListRef.current?.scrollTop || 0,
      reason: 'currentPath-effect',
    });
  }, [currentPath, isActive, syncCurrentTabToWorkspace]);
  useEffect(() => {
    if (!isActive || !activeFileManagerTabIdRef.current || !currentPathHydratedRef.current) return;
    syncCurrentTabToWorkspace({ sortField, sortDir, reason: 'sort-effect' });
  }, [isActive, sortField, sortDir, syncCurrentTabToWorkspace]);
  useEffect(() => {
    if (!isActive || !activeFileManagerTabIdRef.current || !currentPathHydratedRef.current) return;
    syncCurrentTabToWorkspace({ selectedPaths, reason: 'selectedPaths-effect' });
  }, [isActive, selectedPaths, syncCurrentTabToWorkspace]);
  return {
    commitFileManagerWorkspace,
    publishSharedPinnedTabsFromWorkspace,
    triggerCwdSystemTabHighlight,
    openFileManagerPathInNewTabRef,
    contextMenu, setContextMenu,
    selectedPaths, setSelectedPaths,
    lastClickedPathRef, sortFieldRef, sortDirRef, selectedPathsRef,
    syncCurrentTabToWorkspace,
    restoreTabItemsFromCache,
    isFileManagerTabLoadSuperseded,
  };
}
