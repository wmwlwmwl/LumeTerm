import { useCallback } from 'react';
import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';
import {
  FILE_MANAGER_LAYOUT_MODE_SIDEBAR_DUAL,
  FILE_MANAGER_NEW_TAB_PATH_MODE_INHERIT_CURRENT,
  FILE_MANAGER_NEW_TAB_PATH_MODE_ROOT,
  FILE_MANAGER_NEW_TAB_PATH_MODE_SESSION_INITIAL_PATH,
  FILE_MANAGER_NEW_TAB_PATH_MODE_TERMINAL_CWD,
  createFileManagerTab,
  getFileManagerNewTabPathMode,
  getParentPath,
  getFileManagerTabLabel,
  normalizeFileManagerPaneKey,
} from '../../utils/fileManagerHelpers.tsx';
import type { FileManagerPaneState, FileManagerTabLike, FileManagerWorkspaceState } from '../../utils/fileWorkbench.ts';
import type { useFileManagerCore } from './useFileManagerCore.ts';
import type { useFileManagerWorkspaceSync } from './useFileManagerWorkspaceSync.ts';
import type { useFileManagerPaneView } from './useFileManagerPaneView.ts';
import type { useFileManagerDirectoryLoader } from './useFileManagerDirectoryLoader.ts';
import type { FileManagerProps } from './fileManagerTypes.ts';

// 标签页与面板管理：激活标签/面板、新建标签、拖拽重排、固定/关闭、
// 标签路径更新、标题重命名与标签目录删除
export function useFileManagerTabs(deps: ReturnType<typeof useFileManagerCore> & ReturnType<typeof useFileManagerWorkspaceSync> & ReturnType<typeof useFileManagerPaneView> & ReturnType<typeof useFileManagerDirectoryLoader> & {
  initialPath: string
  addToast?: FileManagerProps['addToast']
}) {
  const {
    sessionId, initialPath, addToast, t,
    fileManagerLayoutMode, fileManagerWorkspace, fileManagerWorkspaceRef,
    activeFileManagerTabIdRef, displayedTabIdRef,
    activePaneKey, normalizePath,
    currentPathRef, currentPathHydratedRef, setCurrentPath,
    setPaneSelectionRestore, getPaneViewState, paneViewStateRef,
    getCachedPathItems, getCachedTabItems, removeCachedTabItems, cacheCurrentTabItems,
    draggingFileManagerTabIdRef, setDraggingFileManagerTabId, setFileManagerTabDropIndicator,
    commitFileManagerWorkspace, publishSharedPinnedTabsFromWorkspace, syncCurrentTabToWorkspace,
    isFileManagerTabLoadSuperseded, openFileManagerPathInNewTabRef,
    setSortField, setSortDir, setSelectedPaths, lastClickedPathRef,
    loadDir, applyAnimatedFileListSnapshot,
    items, fileListRef,
  } = deps;
  const activateFileManagerTab = useCallback(async (tabId: string) => {
    const isDualPane = fileManagerLayoutMode === FILE_MANAGER_LAYOUT_MODE_SIDEBAR_DUAL;
    if (!tabId) {
      return;
    }
    if (!isDualPane && tabId === activeFileManagerTabIdRef.current) {
      return;
    }
    const currentWorkspace = syncCurrentTabToWorkspace({ scrollTop: fileListRef.current?.scrollTop || 0 }) || fileManagerWorkspace;
    const targetTab = currentWorkspace?.tabs?.find((tab) => tab.id === tabId);
    if (!targetTab) {
      return;
    }
    const targetPath = normalizePath(targetTab.path) || '/';
    const currentPanes = (currentWorkspace?.panes && typeof currentWorkspace.panes === 'object'
      ? currentWorkspace.panes
      : {}) as FileManagerWorkspaceState['panes'];
    const currentPane = (currentPanes[activePaneKey] && typeof currentPanes[activePaneKey] === 'object'
      ? currentPanes[activePaneKey]
      : {}) as FileManagerPaneState;
    commitFileManagerWorkspace({
      activePane: activePaneKey,
      activeTabId: tabId,
      panes: {
        ...currentPanes,
        [activePaneKey]: {
          ...currentPane,
          tabId,
          path: targetPath,
          selectedPaths: [],
          scrollTop: 0,
        },
      },
    });
    if (isDualPane) {
      setPaneSelectionRestore(activePaneKey, { selectedPaths: [], lastClickedPath: null });
      getPaneViewState(activePaneKey).pendingRestore = { key: '', offset: 0, scrollTop: 0 };
      const cachedItems = getCachedPathItems(targetPath) || getCachedTabItems(tabId, targetPath);
      if (cachedItems) {
        applyAnimatedFileListSnapshot(targetPath, cachedItems, {
          tabId,
          preserveView: false,
        });
      }
      await loadDir(targetPath, {
        tabId,
        silent: true,
        preserveView: false,
        trackDiff: false,
        showLoading: false,
        preferPathCache: true,
      });
      return;
    }
    setSortField(targetTab.sortField || 'name');
    setSortDir(targetTab.sortDir === 'desc' ? 'desc' : 'asc');
    const nextSelectedPaths = Array.isArray(targetTab.selectedPaths) ? targetTab.selectedPaths : [];
    const cachedItems = getCachedTabItems(tabId, targetPath);
    const restoreSelectionAndScroll = () => {
      setSelectedPaths(nextSelectedPaths);
      lastClickedPathRef.current = nextSelectedPaths[nextSelectedPaths.length - 1] || null;
      requestAnimationFrame(() => {
        if (fileListRef.current) {
          fileListRef.current.scrollTop = Number(targetTab.scrollTop) || 0;
        }
      });
    };

    if (cachedItems) {
      if (targetPath !== currentPathRef.current) {
        setPaneSelectionRestore(activePaneKey, {
          selectedPaths: nextSelectedPaths,
          lastClickedPath: nextSelectedPaths[nextSelectedPaths.length - 1] || null,
        });
        getPaneViewState(activePaneKey).pendingRestore = { key: '', offset: 0, scrollTop: Number(targetTab.scrollTop) || 0 };
      }
      applyAnimatedFileListSnapshot(targetPath, cachedItems, {
        tabId,
        preserveView: targetPath !== currentPathRef.current,
      });
      if (targetPath === currentPathRef.current) {
        restoreSelectionAndScroll();
      }
      await loadDir(targetPath, {
        tabId,
        silent: true,
        preserveView: true,
        trackDiff: true,
        showLoading: false,
        preferPathCache: true,
      });
      return;
    }

    if (targetPath === currentPathRef.current) {
      displayedTabIdRef.current = tabId;
      restoreSelectionAndScroll();
      await loadDir(targetPath, {
        tabId,
        silent: true,
        preserveView: true,
        trackDiff: true,
        showLoading: false,
      });
      return;
    }

    setPaneSelectionRestore(activePaneKey, {
      selectedPaths: nextSelectedPaths,
      lastClickedPath: nextSelectedPaths[nextSelectedPaths.length - 1] || null,
    });
    getPaneViewState(activePaneKey).pendingRestore = { key: '', offset: 0, scrollTop: Number(targetTab.scrollTop) || 0 };
    let resolvedPath = targetPath;
    let ok = await loadDir(targetPath, {
      tabId,
      silent: true,
      preserveView: false,
      trackDiff: false,
      showLoading: false,
      preferPathCache: true,
    });
    if (!ok && isFileManagerTabLoadSuperseded(tabId)) {
      return;
    }
    if (!ok && targetPath !== '/') {
      setPaneSelectionRestore(activePaneKey, { selectedPaths: [], lastClickedPath: null });
      getPaneViewState(activePaneKey).pendingRestore = { key: '', offset: 0, scrollTop: 0 };
      resolvedPath = '/';
      ok = await loadDir('/', {
        tabId,
        silent: true,
        preserveView: false,
        trackDiff: false,
        showLoading: false,
        preferPathCache: true,
      });
      if (ok) {
        setSelectedPaths([]);
      }
    }
    if (ok && resolvedPath !== targetPath && targetTab?.pinned !== true) {
      commitFileManagerWorkspace((current) => ({
        activeTabId: current.activeTabId,
        tabs: (current.tabs || []).map((tab) => (
          tab.id === tabId
            ? { ...tab, path: resolvedPath, selectedPaths: [], scrollTop: 0 }
            : tab
        )),
      }));
    }
  }, [activePaneKey, applyAnimatedFileListSnapshot, commitFileManagerWorkspace, fileManagerLayoutMode, fileManagerWorkspace, getCachedPathItems, getCachedTabItems, loadDir, normalizePath, syncCurrentTabToWorkspace]);

  const activateFileManagerPane = useCallback(async (paneKey: unknown) => {
    const nextPaneKey = paneKey === 'right' ? 'right' : 'left';
    if (nextPaneKey === activePaneKey) {
      return;
    }
    const currentWorkspace = syncCurrentTabToWorkspace({ scrollTop: fileListRef.current?.scrollTop || 0 }) || fileManagerWorkspaceRef.current;
    const targetPane = currentWorkspace?.panes?.[nextPaneKey] || {};
    const targetTabId = String(targetPane.tabId || currentWorkspace?.activeTabId || '').trim();
    const targetPath = normalizePath(targetPane.path) || '/';
    const nextSelectedPaths = Array.isArray(targetPane.selectedPaths) ? targetPane.selectedPaths : [];
    commitFileManagerWorkspace({ activePane: nextPaneKey, activeTabId: targetTabId });
    const targetSortField = typeof targetPane.sortField === 'string' ? targetPane.sortField : 'name';
    const targetSortDir = targetPane.sortDir === 'desc' ? 'desc' : 'asc';
    setSortField(targetSortField);
    setSortDir(targetSortDir);
    requestAnimationFrame(() => {
      fileListRef.current?.focus();
    });
    const cachedItems = getCachedTabItems(targetTabId, targetPath) || getCachedPathItems(targetPath);
    const restoreSelectionAndScroll = () => {
      setSelectedPaths(nextSelectedPaths);
      lastClickedPathRef.current = nextSelectedPaths[nextSelectedPaths.length - 1] || null;
      requestAnimationFrame(() => {
        if (fileListRef.current) {
          fileListRef.current.scrollTop = Number(targetPane.scrollTop) || 0;
        }
      });
    };
    if (cachedItems) {
      if (targetPath !== currentPathRef.current) {
        setPaneSelectionRestore(nextPaneKey, {
          selectedPaths: nextSelectedPaths,
          lastClickedPath: nextSelectedPaths[nextSelectedPaths.length - 1] || null,
        });
        paneViewStateRef.current[normalizeFileManagerPaneKey(nextPaneKey)].pendingRestore = { key: '', offset: 0, scrollTop: Number(targetPane.scrollTop) || 0 };
      }
      applyAnimatedFileListSnapshot(targetPath, cachedItems, {
        tabId: targetTabId,
        preserveView: targetPath !== currentPathRef.current,
      });
      if (targetPath === currentPathRef.current) {
        restoreSelectionAndScroll();
      }
      await loadDir(targetPath, {
        tabId: targetTabId,
        silent: true,
        preserveView: true,
        trackDiff: true,
        showLoading: false,
        preferPathCache: true,
      });
      return;
    }
    if (targetPath === currentPathRef.current) {
      displayedTabIdRef.current = targetTabId;
      restoreSelectionAndScroll();
      await loadDir(targetPath, {
        tabId: targetTabId,
        silent: true,
        preserveView: true,
        trackDiff: true,
        showLoading: false,
      });
      return;
    }
    setPaneSelectionRestore(nextPaneKey, {
      selectedPaths: nextSelectedPaths,
      lastClickedPath: nextSelectedPaths[nextSelectedPaths.length - 1] || null,
    });
    paneViewStateRef.current[normalizeFileManagerPaneKey(nextPaneKey)].pendingRestore = { key: '', offset: 0, scrollTop: Number(targetPane.scrollTop) || 0 };
    await loadDir(targetPath, {
      tabId: targetTabId,
      silent: true,
      preserveView: false,
      trackDiff: false,
      showLoading: false,
      preferPathCache: true,
    });
  }, [activePaneKey, applyAnimatedFileListSnapshot, commitFileManagerWorkspace, getCachedPathItems, getCachedTabItems, loadDir, normalizePath, syncCurrentTabToWorkspace]);

  const resolveNewFileManagerTabPath = useCallback(async () => {
    const mode = getFileManagerNewTabPathMode();
    const activeTabPath = normalizePath(currentPathRef.current);
    const normalizedInitialPath = normalizePath(initialPath);
    if (mode === FILE_MANAGER_NEW_TAB_PATH_MODE_ROOT) {
      return '/';
    }
    if (mode === FILE_MANAGER_NEW_TAB_PATH_MODE_SESSION_INITIAL_PATH) {
      if (normalizedInitialPath) {
        return normalizedInitialPath;
      }
      try {
        const cwd = await AppGo.GetTerminalCwd(sessionId);
        const normalizedCwd = normalizePath(cwd);
        if (normalizedCwd) {
          return normalizedCwd;
        }
      } catch (_) {}
    }
    if (mode === FILE_MANAGER_NEW_TAB_PATH_MODE_TERMINAL_CWD) {
      try {
        const cwd = await AppGo.GetTerminalCwd(sessionId);
        const normalizedCwd = normalizePath(cwd);
        if (normalizedCwd) {
          return normalizedCwd;
        }
      } catch (_) {}
    }
    if (mode === FILE_MANAGER_NEW_TAB_PATH_MODE_INHERIT_CURRENT && activeTabPath) {
      return activeTabPath;
    }
    if (activeTabPath) {
      return activeTabPath;
    }
    return '/';
  }, [initialPath, normalizePath, sessionId]);

  const openFileManagerPathInNewTab = useCallback(async (targetPath: unknown) => {
    const normalizedTargetPath = normalizePath(targetPath) || '/';
    const fallbackCurrentPath = normalizePath(currentPathRef.current) || '/';
    const candidatePaths = Array.from(new Set([normalizedTargetPath, getParentPath(normalizedTargetPath), fallbackCurrentPath, '/']));
    const nextTab = createFileManagerTab(normalizedTargetPath);
    commitFileManagerWorkspace((current) => ({
      activeTabId: nextTab.id,
      tabs: [...(Array.isArray(current?.tabs) ? current.tabs : []), nextTab],
    }));
    // 同步更新 ref：activeFileManagerTabIdRef 依赖 React 重渲染才从 prop 同步，
    // 但下方 loadDir 在 ListDir 返回时即调用 canApplyResult 校验 targetTabId 与
    // activeFileManagerTabIdRef 是否一致；若不在此同步，新标签的首次加载结果会因
    // ref 仍指向旧标签而被判定 tab-mismatch 丢弃（终端 cd 后文件管理器不刷新）。
    activeFileManagerTabIdRef.current = nextTab.id;
    displayedTabIdRef.current = nextTab.id;
    setSortField(nextTab.sortField);
    setSortDir(nextTab.sortDir);
    // 仅在已 hydrate 且当前列表就是目标路径时跳过网络请求；未 hydrate 时必须 ListDir
    if (
      currentPathHydratedRef.current
      && candidatePaths[0] === currentPathRef.current
      && Array.isArray(items)
      && items.length > 0
    ) {
      displayedTabIdRef.current = nextTab.id;
      cacheCurrentTabItems(nextTab.id, candidatePaths[0], items);
      setSelectedPaths([]);
      lastClickedPathRef.current = null;
      requestAnimationFrame(() => {
        if (fileListRef.current) {
          fileListRef.current.scrollTop = 0;
        }
      });
      return;
    }
    setPaneSelectionRestore(activePaneKey, { selectedPaths: [], lastClickedPath: null });
    getPaneViewState(activePaneKey).pendingRestore = { key: '', offset: 0, scrollTop: 0 };
    let resolvedPath = candidatePaths[0];
    for (const candidatePath of candidatePaths) {
      const ok = await loadDir(candidatePath, {
        tabId: nextTab.id,
        silent: true,
        preserveView: false,
        trackDiff: false,
        showLoading: false,
        preferPathCache: true,
      });
      if (!ok && isFileManagerTabLoadSuperseded(nextTab.id)) {
        return;
      }
      if (ok) {
        resolvedPath = candidatePath;
        break;
      }
    }
    if (resolvedPath !== normalizedTargetPath) {
      commitFileManagerWorkspace((current) => ({
        activeTabId: current.activeTabId,
        tabs: (current.tabs || []).map((tab) => (
          tab.id === nextTab.id
            ? { ...tab, path: resolvedPath }
            : tab
        )),
      }));
    }
  }, [cacheCurrentTabItems, commitFileManagerWorkspace, items, loadDir, normalizePath]);
  openFileManagerPathInNewTabRef.current = openFileManagerPathInNewTab;

  const handleCreateFileManagerTab = useCallback(async () => {
    const nextPath = await resolveNewFileManagerTabPath();
    await openFileManagerPathInNewTab(nextPath);
  }, [openFileManagerPathInNewTab, resolveNewFileManagerTabPath]);

  const clearFileManagerTabDragState = useCallback(() => {
    draggingFileManagerTabIdRef.current = '';
    setDraggingFileManagerTabId('');
    setFileManagerTabDropIndicator(null);
  }, []);

  const resolveFileManagerTabDropSide = useCallback((event: React.MouseEvent<HTMLDivElement>, tab: FileManagerTabLike) => {
    if (tab?.systemPinned === true) {
      return 'after';
    }
    const rect = event.currentTarget.getBoundingClientRect();
    return event.clientX - rect.left < rect.width / 2 ? 'before' : 'after';
  }, []);

  const getFileManagerTabDropPreviewText = useCallback((draggedTabId: string, targetTab: FileManagerTabLike, side = 'after') => {
    if (!draggedTabId || !targetTab) {
      return '';
    }
    const currentTabs = Array.isArray(fileManagerWorkspaceRef.current?.tabs) ? fileManagerWorkspaceRef.current.tabs : [];
    const draggedTab = currentTabs.find((tab) => tab.id === draggedTabId);
    if (!draggedTab || draggedTab.id === targetTab.id) {
      return '';
    }
    const nextPinned = targetTab.systemPinned === true || targetTab.pinned === true;
    const positionText = t(
      side === 'before' ? '在 {label} 标签页之前,' : '在 {label} 标签页之后,',
      { label: getFileManagerTabLabel(targetTab.path, t, targetTab.customTitle) },
    );
    const stateText = draggedTab.pinned === nextPinned
      ? t(nextPinned ? '并保持固定' : '并保持未固定')
      : t(nextPinned ? '并进行固定' : '并解除固定');
    return `${positionText}${stateText}`;
  }, [t]);

  const resolveFileManagerTabAppendTarget = useCallback(() => {
    const currentTabs = Array.isArray(fileManagerWorkspaceRef.current?.tabs) ? fileManagerWorkspaceRef.current.tabs : [];
    const movableTabs = currentTabs.filter((tab) => tab && typeof tab === 'object' && tab.systemPinned !== true);
    return movableTabs[movableTabs.length - 1] || currentTabs[currentTabs.length - 1] || null;
  }, []);

  const reorderFileManagerTabs = useCallback((draggedTabId: string, targetTabId: string, side = 'after') => {
    if (!draggedTabId || !targetTabId || draggedTabId === targetTabId) {
      return;
    }
    const nextWorkspace = commitFileManagerWorkspace((current) => {
      const currentTabs = Array.isArray(current?.tabs) ? current.tabs.filter((tab) => tab && typeof tab === 'object') : [];
      const draggedTab = currentTabs.find((tab) => tab.id === draggedTabId);
      const targetTab = currentTabs.find((tab) => tab.id === targetTabId);
      if (!draggedTab || !targetTab || draggedTab.systemPinned === true) {
        return current;
      }
      const systemPinnedTabs = currentTabs.filter((tab) => tab.systemPinned === true);
      const pinnedTabs = currentTabs.filter((tab) => tab.systemPinned !== true && tab.id !== draggedTabId && tab.pinned === true);
      const normalTabs = currentTabs.filter((tab) => tab.systemPinned !== true && tab.id !== draggedTabId && tab.pinned !== true);
      const nextPinned = targetTab.systemPinned === true || targetTab.pinned === true;
      const draggedNextTab = { ...draggedTab, pinned: nextPinned, systemPinned: false };
      const targetGroup = nextPinned ? pinnedTabs : normalTabs;
      let insertIndex = 0;
      if (targetTab.systemPinned === true) {
        insertIndex = 0;
      } else {
        const targetIndex = targetGroup.findIndex((tab) => tab.id === targetTabId);
        insertIndex = targetIndex < 0 ? targetGroup.length : (side === 'before' ? targetIndex : targetIndex + 1);
      }
      targetGroup.splice(insertIndex, 0, draggedNextTab);
      return {
        activeTabId: current?.activeTabId || draggedNextTab.id,
        tabs: [...systemPinnedTabs, ...pinnedTabs, ...normalTabs],
      };
    });
    publishSharedPinnedTabsFromWorkspace(nextWorkspace);
  }, [commitFileManagerWorkspace, publishSharedPinnedTabsFromWorkspace]);

  const handleToggleFileManagerTabPinned = useCallback((tabId: string) => {
    const nextWorkspace = commitFileManagerWorkspace((current) => ({
      activeTabId: current?.activeTabId || '',
      tabs: (current?.tabs || []).map((tab) => (
        tab.id === tabId && tab.systemPinned !== true
          ? { ...tab, pinned: tab.pinned !== true }
          : tab
      )),
    }));
    publishSharedPinnedTabsFromWorkspace(nextWorkspace);
  }, [commitFileManagerWorkspace, publishSharedPinnedTabsFromWorkspace]);

  const handleCloseFileManagerTab = useCallback(async (tabId: string, event: React.MouseEvent | undefined) => {
    event?.stopPropagation();
    const currentWorkspace = syncCurrentTabToWorkspace({ scrollTop: fileListRef.current?.scrollTop || 0 }) || fileManagerWorkspace;
    const currentTabs = Array.isArray(currentWorkspace?.tabs) ? currentWorkspace.tabs : [];
    if (currentTabs.length <= 1) {
      return;
    }
    const targetTab = currentTabs.find((tab) => tab.id === tabId);
    if (!targetTab) {
      return;
    }
    if (targetTab.pinned === true) {
      addToast?.(t('固定标签不能关闭'), 'warning');
      return;
    }
    const closingIndex = currentTabs.findIndex((tab) => tab.id === tabId);
    if (closingIndex < 0) {
      return;
    }
    const nextTabs = currentTabs.filter((tab) => tab.id !== tabId);
    const isClosingActive = tabId === activeFileManagerTabIdRef.current;
    const nextActiveTab = isClosingActive
      ? (nextTabs[closingIndex] || nextTabs[closingIndex - 1] || nextTabs[0] || null)
      : (nextTabs.find((tab) => tab.id === activeFileManagerTabIdRef.current) || nextTabs[0] || null);
    removeCachedTabItems(tabId);
    commitFileManagerWorkspace({
      activeTabId: nextActiveTab?.id || '',
      tabs: nextTabs,
    });
    if (!isClosingActive || !nextActiveTab) {
      return;
    }
    setSortField(nextActiveTab.sortField || 'name');
    setSortDir(nextActiveTab.sortDir === 'desc' ? 'desc' : 'asc');
    const nextSelectedPaths = Array.isArray(nextActiveTab.selectedPaths) ? nextActiveTab.selectedPaths : [];
    const targetPath = normalizePath(nextActiveTab.path) || '/';
    const cachedItems = getCachedTabItems(nextActiveTab.id, targetPath);
    const restoreSelectionAndScroll = () => {
      setSelectedPaths(nextSelectedPaths);
      lastClickedPathRef.current = nextSelectedPaths[nextSelectedPaths.length - 1] || null;
      requestAnimationFrame(() => {
        if (fileListRef.current) {
          fileListRef.current.scrollTop = Number(nextActiveTab.scrollTop) || 0;
        }
      });
    };

    if (cachedItems) {
      if (targetPath !== currentPathRef.current) {
        setPaneSelectionRestore(activePaneKey, {
          selectedPaths: nextSelectedPaths,
          lastClickedPath: nextSelectedPaths[nextSelectedPaths.length - 1] || null,
        });
        getPaneViewState(activePaneKey).pendingRestore = { key: '', offset: 0, scrollTop: Number(nextActiveTab.scrollTop) || 0 };
      }
      applyAnimatedFileListSnapshot(targetPath, cachedItems, {
        tabId: nextActiveTab.id,
        preserveView: targetPath !== currentPathRef.current,
      });
      if (targetPath === currentPathRef.current) {
        restoreSelectionAndScroll();
      }
      await loadDir(targetPath, {
        tabId: nextActiveTab.id,
        silent: true,
        preserveView: true,
        trackDiff: true,
        showLoading: false,
        preferPathCache: true,
      });
      return;
    }

    if (targetPath === currentPathRef.current) {
      displayedTabIdRef.current = nextActiveTab.id;
      restoreSelectionAndScroll();
      await loadDir(targetPath, {
        tabId: nextActiveTab.id,
        silent: true,
        preserveView: true,
        trackDiff: true,
        showLoading: false,
      });
      return;
    }

    setPaneSelectionRestore(activePaneKey, {
      selectedPaths: nextSelectedPaths,
      lastClickedPath: nextSelectedPaths[nextSelectedPaths.length - 1] || null,
    });
    getPaneViewState(activePaneKey).pendingRestore = { key: '', offset: 0, scrollTop: Number(nextActiveTab.scrollTop) || 0 };
    await loadDir(targetPath, {
      tabId: nextActiveTab.id,
      silent: true,
      preserveView: false,
      trackDiff: false,
      showLoading: false,
      preferPathCache: true,
    });
  }, [addToast, applyAnimatedFileListSnapshot, commitFileManagerWorkspace, fileManagerWorkspace, getCachedTabItems, loadDir, normalizePath, removeCachedTabItems, syncCurrentTabToWorkspace, t]);


  const updateFileManagerTabPath = useCallback((tabId: string, nextPath: unknown, options: Record<string, unknown> = {}) => {
    const normalizedNextPath = normalizePath(nextPath) || '/';
    const resetSelection = options.resetSelection === true;
    const clearCache = options.clearCache === true;
    if (clearCache) {
      removeCachedTabItems(tabId);
    }
    commitFileManagerWorkspace((current) => ({
      activeTabId: current.activeTabId,
      tabs: (current.tabs || []).map((tab) => (
        tab.id === tabId
          ? {
              ...tab,
              path: normalizedNextPath,
              selectedPaths: resetSelection ? [] : tab.selectedPaths,
              scrollTop: resetSelection ? 0 : tab.scrollTop,
            }
          : tab
      )),
    }));
    if (tabId === activeFileManagerTabIdRef.current) {
      displayedTabIdRef.current = tabId;
      currentPathHydratedRef.current = true;
      currentPathRef.current = normalizedNextPath;
      setCurrentPath(normalizedNextPath);
      if (resetSelection) {
        setSelectedPaths([]);
        lastClickedPathRef.current = null;
      }
    }
  }, [commitFileManagerWorkspace, normalizePath, removeCachedTabItems]);

  const handleRenameFileManagerTabTitle = useCallback(async (tabId: string) => {
    const targetTab = (fileManagerWorkspaceRef.current?.tabs || []).find((tab) => tab.id === tabId);
    if (!targetTab) {
      return;
    }
    if (targetTab.systemPinned === true) {
      addToast?.(t('初始目录标签不可修改'), 'warning');
      return;
    }
    const currentCustomTitle = String(targetTab.customTitle || '').trim();
    const currentLabel = getFileManagerTabLabel(targetTab.path, t, currentCustomTitle);
    const defaultLabel = getFileManagerTabLabel(targetTab.path, t, '');
    const nextTitle = await window.lumeDialog?.prompt(`${t('标签标题')}: ${currentLabel}`);
    if (nextTitle === null || nextTitle === undefined) {
      return;
    }
    const trimmedTitle = String(nextTitle).trim();
    const resolvedCustomTitle = trimmedTitle && trimmedTitle !== defaultLabel ? trimmedTitle : '';
    if (resolvedCustomTitle === currentCustomTitle) {
      return;
    }
    const nextWorkspace = commitFileManagerWorkspace((current) => ({
      activeTabId: current?.activeTabId || '',
      tabs: (current?.tabs || []).map((tab) => (
        tab.id === tabId
          ? { ...tab, customTitle: resolvedCustomTitle }
          : tab
      )),
    }));
    publishSharedPinnedTabsFromWorkspace(nextWorkspace);
  }, [addToast, commitFileManagerWorkspace, publishSharedPinnedTabsFromWorkspace, t]);

  const handleDeleteTabDirectory = useCallback(async (tabId: string, targetPath: unknown, useShell = false) => {
    const normalizedTargetPath = normalizePath(targetPath) || '/';
    const targetTab = (fileManagerWorkspace?.tabs || []).find((tab) => tab.id === tabId);
    if (targetTab?.systemPinned === true) {
      addToast?.(t('初始目录标签不可修改'), 'warning');
      return;
    }
    if (targetTab?.pinned === true) {
      addToast?.(t('固定标签路径不可变，请先取消固定'), 'warning');
      return;
    }
    const displayName = normalizedTargetPath === '/' ? t('目录根') : (normalizedTargetPath.split('/').filter(Boolean).pop() || normalizedTargetPath);
    const needConfirm = localStorage.getItem('skipFileDeleteConfirm') !== 'true';
    if (needConfirm) {
      const ok = await window.lumeDialog?.confirm(
        useShell
          ? `${t('确定删除')}${displayName}${t('？(rm -rf) 此操作不可撤销')}`
          : `${t('确定删除')}${displayName}${t('？此操作不可撤销')}`
      );
      fileListRef.current?.focus();
      if (!ok) {
        return;
      }
    }
    const parentPath = getParentPath(normalizedTargetPath);
    try {
      await AppGo.DeleteItemShell(sessionId, normalizedTargetPath);
      addToast?.(`${t('已删除')}: ${displayName}`, 'success');
      updateFileManagerTabPath(tabId, parentPath, { resetSelection: true, clearCache: true });
      if (tabId === activeFileManagerTabIdRef.current) {
        await loadDir(parentPath, {
          tabId,
          silent: true,
          preserveView: false,
          trackDiff: false,
          showLoading: false,
        });
      }
    } catch (err) {
      addToast?.(`${t('删除失败')}: ${err}`, 'error');
    }
  }, [addToast, fileManagerWorkspace, loadDir, normalizePath, sessionId, t, updateFileManagerTabPath]);

  return {
    activateFileManagerTab,
    activateFileManagerPane,
    resolveNewFileManagerTabPath,
    openFileManagerPathInNewTab,
    handleCreateFileManagerTab,
    clearFileManagerTabDragState,
    resolveFileManagerTabDropSide,
    getFileManagerTabDropPreviewText,
    resolveFileManagerTabAppendTarget,
    reorderFileManagerTabs,
    handleToggleFileManagerTabPinned,
    handleCloseFileManagerTab,
    updateFileManagerTabPath,
    handleRenameFileManagerTabTitle,
    handleDeleteTabDirectory,
  };
}
