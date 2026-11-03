import { useEffect, useCallback, useRef } from 'react';
import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';
import { EventsOn } from '../../../wailsjs/runtime/runtime.js';
import {
  FILE_MANAGER_NEW_TAB_PATH_MODE_ROOT,
  FILE_MANAGER_NEW_TAB_PATH_MODE_SESSION_INITIAL_PATH,
  FILE_MANAGER_NEW_TAB_PATH_MODE_TERMINAL_CWD,
  FILE_MANAGER_SYSTEM_TAB_KIND_HOME,
  areFileManagerTabStatesEqual,
  cloneFileManagerItemsForCache,
  computeCompressedOverallProgress,
  createFileManagerTab,
  getFileManagerInitialPathMode,
  getFileManagerSystemTabType,
} from '../../utils/fileManagerHelpers.tsx';
import {
  getSessionFileManagerWorkspace,
  setSessionFileManagerWorkspace,
  updateSessionUploadQueue,
} from '../../utils/fileWorkbench.ts';
import type { TransferQueueItem } from '../../utils/fileWorkbench.ts';
import type { useFileManagerCore } from './useFileManagerCore.ts';
import type { useFileManagerWorkspaceSync } from './useFileManagerWorkspaceSync.ts';
import type { useFileManagerPaneView } from './useFileManagerPaneView.ts';
import type { FileManagerFileItem } from './fileManagerTypes.ts';
import type { FileManagerProps, LoadDirOptions } from './fileManagerTypes.ts';
import type { FileManagerPaneState, FileManagerWorkspaceState } from '../../utils/fileWorkbench.ts';

// 目录加载核心：loadDir（缓存优先/stale-while-revalidate/固定标签重定向）、
// 初始路径解析与终端 cwd 跟随、传输进度事件订阅
export function useFileManagerDirectoryLoader(deps: ReturnType<typeof useFileManagerCore> & ReturnType<typeof useFileManagerWorkspaceSync> & ReturnType<typeof useFileManagerPaneView> & {
  initialPath: string
  addToast?: FileManagerProps['addToast']
}) {
  const {
    sessionId, sessionGroupId, isActive, initialPath, addToast, t,
    normalizePath, currentPathRef, currentPathHydratedRef, initializingPathRef, pendingTerminalCwdRef,
    activeFileManagerTabIdRef, displayedTabIdRef, fileManagerWorkspaceRef,
    mountedRef, loadRequestSeqRef, getCachedPathItems,
    setLoading, setItems, setCurrentPath, preserveWorkspacePathRef, fileListRef, abortedUploadIdsRef,
    setPaneSelectionRestore, setSortField, setSortDir,
    activePaneKey, setFileManagerWorkspaceState,
    commitFileManagerWorkspace, syncCurrentTabToWorkspace, isFileManagerTabLoadSuperseded, openFileManagerPathInNewTabRef,
    isDeletedPlaceholderItem, didItemMetadataChange, queueRowEffect,
    setSelectedPaths, lastClickedPathRef,
    queueFileListViewRestore, updateItemsPreservingView, buildItemsWithTrackedDiff, getPaneViewState,
    currentPath, isActiveRef,
  } = deps;
  // 初始化只按「挂载 + sessionId」执行一次；切服务器/终端回来的 isActive 变化只走静默刷新
  const initializedSessionRef = useRef('');
  // 恢复工作区时新 shell 的启动 cwd 锚点：首个 cwd 事件不应覆盖已记住的浏览目录
  const restoredBootstrapCwdRef = useRef('');
  // 拿不到启动 cwd 锚点时的兜底：忽略重挂后的第一个 cwd 事件
  const ignoreFirstCwdFollowRef = useRef(false);
  const loadDir = useCallback(async (path: unknown, options: LoadDirOptions | boolean = {}) => {
    const normalizedPath = normalizePath(path) || '/';
    const resolvedOptions = typeof options === 'boolean' ? { silent: options } : (options || {});
    const targetTabId = String(
      resolvedOptions.tabId
      || activeFileManagerTabIdRef.current
      || displayedTabIdRef.current
      || ''
    ).trim();
    const currentWorkspace = fileManagerWorkspaceRef.current;
    const targetWorkspaceTab = targetTabId && Array.isArray(currentWorkspace?.tabs)
      ? currentWorkspace.tabs.find((tab) => tab.id === targetTabId)
      : null;
    const normalizedTargetWorkspaceTabPath = normalizePath(targetWorkspaceTab?.path) || '/';
    const displayedTabId = String(displayedTabIdRef.current || '').trim();
    const isSwitchingDisplayedTab = !!(targetTabId && displayedTabId && targetTabId !== displayedTabId);
    // 固定标签禁止原地改路径：仅在已 hydrate 后的用户导航时开新标签。
    // 首次初始化若误走此分支，会在 open-new-tab 里因 currentPath 仍是默认 '/' 直接 return，列表永远不加载。
    if (
      currentPathHydratedRef.current
      && targetWorkspaceTab?.pinned === true
      && normalizedPath !== normalizedTargetWorkspaceTabPath
      && !isSwitchingDisplayedTab
      && typeof openFileManagerPathInNewTabRef.current === 'function'
    ) {
      await openFileManagerPathInNewTabRef.current(normalizedPath);
      return true;
    }
    const requestSeq = ++loadRequestSeqRef.current;
    const canApplyResult = () => {
      if (!mountedRef.current) return false;
      if (requestSeq !== loadRequestSeqRef.current) return false;
      if (targetTabId && activeFileManagerTabIdRef.current && targetTabId !== activeFileManagerTabIdRef.current) {
        return false;
      }
      return true;
    };

    const explicitStaleWhileRevalidate = resolvedOptions.staleWhileRevalidate === true;
    const providedStaleItems = explicitStaleWhileRevalidate ? cloneFileManagerItemsForCache(resolvedOptions.staleItems) : null;
    const cachedPathItems = providedStaleItems ? null : getCachedPathItems(normalizedPath);
    // Prefer path-level cache whenever leaving the current path so tab switches don't flash the previous tab's list.
    const pathChanged = normalizedPath !== currentPathRef.current;
    const samePathRefresh = currentPathHydratedRef.current && !pathChanged;
    const staleWhileRevalidate = explicitStaleWhileRevalidate
      || (!!cachedPathItems && pathChanged)
      || (!!cachedPathItems && resolvedOptions.preferPathCache === true);
    const staleItems = providedStaleItems || cachedPathItems;
    const preserveWorkspacePathOnSuccess = resolvedOptions.preserveWorkspacePathOnSuccess === true;

    if (staleWhileRevalidate && staleItems) {
      displayedTabIdRef.current = targetTabId || displayedTabIdRef.current;
      currentPathHydratedRef.current = true;
      currentPathRef.current = normalizedPath;
      setLoading(false);
      setItems(staleItems);
      setCurrentPath(normalizedPath);
    } else if (pathChanged) {
      // No usable cache: clear previous tab/path content immediately so the old list never flashes.
      displayedTabIdRef.current = targetTabId || displayedTabIdRef.current;
      currentPathHydratedRef.current = true;
      currentPathRef.current = normalizedPath;
      setItems([]);
      setCurrentPath(normalizedPath);
    }

    const preserveView = resolvedOptions.preserveView ?? (staleWhileRevalidate ? true : samePathRefresh);
    const trackDiff = (resolvedOptions.trackDiff ?? (staleWhileRevalidate ? true : samePathRefresh)) && samePathRefresh;
    const showLoading = resolvedOptions.showLoading ?? (staleWhileRevalidate ? false : !(preserveView || trackDiff));

    if (showLoading) {
      setLoading(true);
    }
    if (preserveView && !trackDiff) {
      queueFileListViewRestore();
    }

    try {
      const data = await AppGo.ListDir(sessionId, normalizedPath);
      if (!canApplyResult()) {
        return false;
      }
      if (trackDiff) {
        updateItemsPreservingView((current) => buildItemsWithTrackedDiff(current, (data || []) as FileManagerFileItem[], normalizedPath));
      } else {
        setItems((data || []) as FileManagerFileItem[]);
      }
      displayedTabIdRef.current = targetTabId || displayedTabIdRef.current;
      currentPathHydratedRef.current = true;
      currentPathRef.current = normalizedPath;
      preserveWorkspacePathRef.current = preserveWorkspacePathOnSuccess;
      setCurrentPath(normalizedPath);
      if (!preserveView && fileListRef.current) {
        fileListRef.current.scrollTop = 0;
      }
      setLoading(false);
      return true;
    } catch (err) {
      getPaneViewState(activePaneKey).pendingRestore = null;
      if (!canApplyResult()) return false;
      setLoading(false);
      if (!resolvedOptions.silent) {
        const rawMsg = String(err);
        // OpenWrt 缺省无 SFTP 子系统:后端错误串带安装命令,提取后给出可复制提示
        const openwrtInstall = rawMsg.match(/opkg update && opkg install openssh-sftp-server/);
        if (openwrtInstall) {
          const installCmd = openwrtInstall[0];
          addToast?.(
            `${t('检测到 OpenWrt 设备，文件管理器需要 SFTP 子系统，请执行以下命令安装')}：${installCmd}。${t('安装完成后请重新连接会话')}`,
            'warning',
            20000,
            [{
              label: t('复制安装命令'),
              onClick: () => {
                navigator.clipboard.writeText(installCmd);
                addToast?.(t('安装命令已复制'), 'success');
              },
            }],
          );
        } else {
          const msg = rawMsg.toLowerCase().includes('permission denied')
            ? `${t('权限不足')}: SFTP ${t('仍以')} ${sessionId ? t('原用户') : ''} ${t('身份运行，终端内 sudo 不影响文件管理器')}`
            : `${t('读取目录失败')}: ${rawMsg}`;
          addToast?.(`${msg} [${normalizedPath}]`, 'error');
        }
      }
      return false;
    } finally {
      if (mountedRef.current && requestSeq === loadRequestSeqRef.current) {
        setLoading(false);
      }
    }
  }, [sessionId, addToast, normalizePath, t, queueFileListViewRestore, updateItemsPreservingView, isDeletedPlaceholderItem, didItemMetadataChange, queueRowEffect, getCachedPathItems]);

  const applyAnimatedFileListSnapshot = useCallback((path: unknown, nextItems: FileManagerFileItem[], options: Record<string, unknown> = {}) => {
    const normalizedPath = normalizePath(path) || '/';
    const targetTabId = String(
      options.tabId
      || activeFileManagerTabIdRef.current
      || displayedTabIdRef.current
      || ''
    ).trim();
    const preserveView = options.preserveView === true;
    displayedTabIdRef.current = targetTabId || displayedTabIdRef.current;
    currentPathHydratedRef.current = true;
    currentPathRef.current = normalizedPath;
    setLoading(false);
    setItems(Array.isArray(nextItems) ? nextItems : []);
    setCurrentPath(normalizedPath);
    if (!preserveView && fileListRef.current) {
      fileListRef.current.scrollTop = 0;
    }
  }, [normalizePath]);

  const buildNonRememberedInitialPathCandidates = useCallback(async () => {
    const candidates: string[] = [];
    const pushCandidate = (value: unknown) => {
      const normalized = normalizePath(value);
      if (!normalized || candidates.includes(normalized)) {
        return;
      }
      candidates.push(normalized);
    };
    const normalizedInitialPath = normalizePath(initialPath);
    const initialPathMode = getFileManagerInitialPathMode();
    pushCandidate(normalizedInitialPath);
    if (initialPathMode === FILE_MANAGER_NEW_TAB_PATH_MODE_ROOT) {
      pushCandidate('/');
    } else if (initialPathMode === FILE_MANAGER_NEW_TAB_PATH_MODE_SESSION_INITIAL_PATH || initialPathMode === FILE_MANAGER_NEW_TAB_PATH_MODE_TERMINAL_CWD) {
      try {
        const cwd = await AppGo.GetTerminalCwd(sessionId);
        pushCandidate(cwd);
      } catch (_) {}
    }
    pushCandidate('/');
    return candidates;
  }, [initialPath, normalizePath, sessionId]);

  const resolveNonRememberedInitialPath = useCallback(async () => {
    const candidates = await buildNonRememberedInitialPathCandidates();
    for (const candidatePath of candidates) {
      try {
        await AppGo.ListDir(sessionId, candidatePath);
        return candidatePath;
      } catch (_) {}
    }
    return candidates[candidates.length - 1] || '/';
  }, [buildNonRememberedInitialPathCandidates, sessionId]);

  const ensureForcedInitialFileManagerTab = useCallback((workspace: FileManagerWorkspaceState, forcedPath: unknown, _cwdPath = ''): FileManagerWorkspaceState => {
    const normalizedForcedPath = normalizePath(forcedPath) || '/';
    const currentTabs = Array.isArray(workspace?.tabs) ? workspace.tabs.filter((tab) => tab && typeof tab === 'object') : [];
    const currentActiveTabId = typeof workspace?.activeTabId === 'string' ? workspace.activeTabId : '';
    const homeSystemTab = currentTabs.find((tab) => getFileManagerSystemTabType(tab) === FILE_MANAGER_SYSTEM_TAB_KIND_HOME) || null;
    // 首页固定标签路径与首次加载目标保持一致，避免 pane 默认 '/' 与 tab '/root' 分叉后误走开新标签
    const homeTabPath = normalizePath(homeSystemTab?.path) || normalizedForcedPath;
    const baseHomeTab = homeSystemTab || createFileManagerTab(homeTabPath, {
      pinned: true,
      systemPinned: true,
      systemPinnedType: FILE_MANAGER_SYSTEM_TAB_KIND_HOME,
    });
    const baseHomePath = normalizePath(baseHomeTab.path) || '/';
    const nextHomeTab = {
      ...baseHomeTab,
      customTitle: '',
      path: homeTabPath,
      pinned: true,
      systemPinned: true,
      systemPinnedType: FILE_MANAGER_SYSTEM_TAB_KIND_HOME,
      selectedPaths: baseHomePath === homeTabPath ? (Array.isArray(baseHomeTab.selectedPaths) ? baseHomeTab.selectedPaths : []) : [],
      scrollTop: baseHomePath === homeTabPath && Number.isFinite(Number(baseHomeTab.scrollTop)) ? Number(baseHomeTab.scrollTop) : 0,
    };
    const nextTabs = [nextHomeTab];
    currentTabs.forEach((tab) => {
      if (tab.id === nextHomeTab.id || tab.systemPinned === true) {
        return;
      }
      nextTabs.push(tab);
    });
    const nextActiveTabId = nextTabs.some((tab) => tab.id === currentActiveTabId) ? currentActiveTabId : nextHomeTab.id;
    const activeTabPath = normalizePath(nextTabs.find((tab) => tab.id === nextActiveTabId)?.path) || homeTabPath;
    const currentPanes = (workspace?.panes && typeof workspace.panes === 'object'
      ? workspace.panes
      : {}) as FileManagerWorkspaceState['panes'];
    const alignPane = (paneKey: 'left' | 'right', fallbackTabId: string) => {
      const currentPane = (currentPanes?.[paneKey] && typeof currentPanes[paneKey] === 'object'
        ? currentPanes[paneKey]
        : {}) as FileManagerPaneState;
      const paneTabId = String(currentPane.tabId || fallbackTabId || nextActiveTabId || '').trim();
      const matchedTab = nextTabs.find((tab) => tab.id === paneTabId) || nextTabs.find((tab) => tab.id === nextActiveTabId) || nextHomeTab;
      const matchedPath = normalizePath(matchedTab?.path) || activeTabPath;
      return {
        ...currentPane,
        tabId: matchedTab?.id || nextActiveTabId,
        path: matchedPath,
      };
    };
    const nextPanes = {
      left: alignPane('left', nextActiveTabId),
      right: alignPane('right', nextActiveTabId),
    };
    const changed = currentTabs.length !== nextTabs.length
      || nextActiveTabId !== currentActiveTabId
      || currentTabs.some((tab, index) => !areFileManagerTabStatesEqual(tab, nextTabs[index]))
      || nextTabs.some((tab, index) => !areFileManagerTabStatesEqual(tab, currentTabs[index]))
      || normalizePath(currentPanes?.left?.path) !== normalizePath(nextPanes.left.path)
      || normalizePath(currentPanes?.right?.path) !== normalizePath(nextPanes.right.path)
      || String(currentPanes?.left?.tabId || '') !== String(nextPanes.left.tabId || '')
      || String(currentPanes?.right?.tabId || '') !== String(nextPanes.right.tabId || '');
    if (!changed) {
      return workspace;
    }
    return {
      activePane: workspace.activePane,
      activeTabId: nextActiveTabId,
      tabs: nextTabs,
      panes: nextPanes,
    };
  }, [normalizePath]);

  const resolveTerminalCwdFollowTarget = useCallback((cwdPath: unknown) => {
    const normalizedCwdPath = normalizePath(cwdPath);
    if (!normalizedCwdPath) {
      return null;
    }
    const currentWorkspace = fileManagerWorkspaceRef.current;
    const currentTabs = Array.isArray(currentWorkspace?.tabs) ? currentWorkspace.tabs : [];
    const activeTabId = String(activeFileManagerTabIdRef.current || currentWorkspace?.activeTabId || '').trim();
    const activeTab = currentTabs.find((tab) => tab.id === activeTabId) || currentTabs[0] || null;
    if (!activeTab) {
      return null;
    }
    // 激活标签是固定标签且路径与 cwd 不一致时，优先复用已存在的同路径标签（通常是系统 home 标签），
    // 避免命中 loadDir 的“固定标签禁止原地改路径就开新标签”分支而冒出与现成标签重复的冗余标签。
    if (activeTab.pinned === true && (normalizePath(activeTab.path) || '/') !== normalizedCwdPath) {
      const samePathTab = currentTabs.find((tab) => tab && (normalizePath(tab.path) || '/') === normalizedCwdPath);
      if (samePathTab) {
        return {
          path: normalizedCwdPath,
          tabId: String(samePathTab.id || '').trim(),
        };
      }
    }
    return {
      path: normalizedCwdPath,
      tabId: String(activeTab.id || '').trim(),
    };
  }, [normalizePath]);

  const applyTerminalCwdFollow = useCallback(async (cwd: unknown, options: Record<string, unknown> = {}) => {
    // 重连/重挂后新 shell 的启动 cwd（通常是 home）不应覆盖已恢复的浏览目录；
    // 只忽略启动的那一次，之后用户真实 cd 仍正常跟随。
    const normalizedCwd = normalizePath(cwd);
    const bootstrapCwd = normalizePath(restoredBootstrapCwdRef.current);
    if (bootstrapCwd && normalizedCwd === bootstrapCwd) {
      restoredBootstrapCwdRef.current = '';
      pendingTerminalCwdRef.current = '';
      return false;
    }
    if (ignoreFirstCwdFollowRef.current) {
      ignoreFirstCwdFollowRef.current = false;
      pendingTerminalCwdRef.current = '';
      return false;
    }
    const followTarget = resolveTerminalCwdFollowTarget(cwd);
    if (!followTarget?.path || !followTarget?.tabId) {
      pendingTerminalCwdRef.current = '';
      return false;
    }
    if (initializingPathRef.current) {
      pendingTerminalCwdRef.current = followTarget.path;
      return false;
    }
    if (!isActiveRef.current && options.force !== true) {
      pendingTerminalCwdRef.current = followTarget.path;
      return false;
    }
    pendingTerminalCwdRef.current = '';
    const currentActiveTabId = String(activeFileManagerTabIdRef.current || '').trim();
    if (followTarget.tabId !== currentActiveTabId) {
      const currentWorkspace = syncCurrentTabToWorkspace({ scrollTop: fileListRef.current?.scrollTop || 0 }) || fileManagerWorkspaceRef.current;
      const currentPanes = (currentWorkspace?.panes && typeof currentWorkspace.panes === 'object'
        ? currentWorkspace.panes
        : {}) as FileManagerWorkspaceState['panes'];
      const currentPane = (currentPanes[activePaneKey] && typeof currentPanes[activePaneKey] === 'object'
        ? currentPanes[activePaneKey]
        : {}) as FileManagerPaneState;
      const targetTab = Array.isArray(currentWorkspace?.tabs)
        ? currentWorkspace.tabs.find((tab) => tab.id === followTarget.tabId) || null
        : null;
      commitFileManagerWorkspace({
        activePane: activePaneKey,
        activeTabId: followTarget.tabId,
        panes: {
          ...currentPanes,
          [activePaneKey]: {
            ...currentPane,
            tabId: followTarget.tabId,
            path: followTarget.path,
            selectedPaths: [],
            scrollTop: 0,
          },
        },
      });
      activeFileManagerTabIdRef.current = followTarget.tabId;
      displayedTabIdRef.current = followTarget.tabId;
      setSortField(targetTab?.sortField || 'name');
      setSortDir(targetTab?.sortDir === 'desc' ? 'desc' : 'asc');
      setSelectedPaths([]);
      lastClickedPathRef.current = null;
    }
    if (followTarget.path === currentPathRef.current && followTarget.tabId === String(activeFileManagerTabIdRef.current || '').trim()) {
      return true;
    }
    return loadDir(followTarget.path, {
      tabId: followTarget.tabId,
      silent: true,
      preserveView: false,
      trackDiff: false,
      showLoading: options.showLoading === true,
    });
  }, [activePaneKey, commitFileManagerWorkspace, loadDir, resolveTerminalCwdFollowTarget, syncCurrentTabToWorkspace]);

  useEffect(() => {
    if (!isActive || initializedSessionRef.current === sessionId) {
      return undefined;
    }
    initializedSessionRef.current = sessionId;
    const existingWorkspaceSnapshot = getSessionFileManagerWorkspace(sessionId);
    const hasRememberedTabs = Array.isArray(existingWorkspaceSnapshot?.tabs) && existingWorkspaceSnapshot.tabs.length > 0;
    let cancelled = false;
    currentPathHydratedRef.current = false;
    initializingPathRef.current = true;
    const rememberedActiveTabPath = hasRememberedTabs
      ? normalizePath(
        existingWorkspaceSnapshot.tabs.find((tab) => tab.id === existingWorkspaceSnapshot.activeTabId)?.path
        || existingWorkspaceSnapshot.tabs[0]?.path,
      )
      : '';
    const rememberedHomePath = hasRememberedTabs
      ? normalizePath(
        existingWorkspaceSnapshot.tabs.find((tab) => getFileManagerSystemTabType(tab) === FILE_MANAGER_SYSTEM_TAB_KIND_HOME)?.path
        || existingWorkspaceSnapshot.tabs[0]?.path,
      )
      : '';
    // 慢线路下（SFTP 首包可能要等很久）初始化探测阶段也要有反馈；
    // 已恢复目录且已有该目录缓存时跳过，避免缓存可用时闪一下加载态
    if (!rememberedActiveTabPath || !getCachedPathItems(rememberedActiveTabPath)) {
      setLoading(true);
    }
    (async () => {
      try {
        let systemTerminalCwdPath = normalizePath(pendingTerminalCwdRef.current);
        if (!systemTerminalCwdPath) {
          try {
            systemTerminalCwdPath = normalizePath(await AppGo.GetTerminalCwd(sessionId));
          } catch (_) {
            systemTerminalCwdPath = '';
          }
        }
        const forcedInitialPath = hasRememberedTabs
          ? (rememberedHomePath || systemTerminalCwdPath || '/')
          : (systemTerminalCwdPath || await resolveNonRememberedInitialPath());
        if (hasRememberedTabs) {
          // 已恢复工作区：记住新 shell 的启动 cwd，首个 cwd 事件不得覆盖浏览目录
          if (systemTerminalCwdPath) {
            restoredBootstrapCwdRef.current = systemTerminalCwdPath;
          } else {
            ignoreFirstCwdFollowRef.current = true;
          }
        }
        const existingWorkspace = getSessionFileManagerWorkspace(sessionId);
        const repairedWorkspace = ensureForcedInitialFileManagerTab(existingWorkspace, forcedInitialPath, systemTerminalCwdPath || forcedInitialPath);
        const resolvedWorkspace = repairedWorkspace !== existingWorkspace
          ? setSessionFileManagerWorkspace(sessionId, (currentWorkspace) => ensureForcedInitialFileManagerTab(currentWorkspace, forcedInitialPath, systemTerminalCwdPath || forcedInitialPath))
          : repairedWorkspace;
        if (cancelled) {
          return;
        }
        fileManagerWorkspaceRef.current = resolvedWorkspace;
        setFileManagerWorkspaceState(resolvedWorkspace);
        const existingTab = resolvedWorkspace.tabs.find((tab) => tab.id === resolvedWorkspace.activeTabId) || resolvedWorkspace.tabs[0] || null;
        if (!existingTab) {
          return;
        }
        const resolvedInitialPaneKey = resolvedWorkspace.activePane === 'right' ? 'right' : 'left';
        const existingPane = resolvedWorkspace?.panes?.[resolvedInitialPaneKey] || {};
        const nextSortField = typeof existingPane.sortField === 'string' ? existingPane.sortField : (existingTab.sortField || 'name');
        const nextSortDir = existingPane.sortDir === 'desc' ? 'desc' : (existingTab.sortDir === 'desc' ? 'desc' : 'asc');
        const nextSelectedPaths = Array.isArray(existingPane.selectedPaths)
          ? existingPane.selectedPaths
          : (Array.isArray(existingTab.selectedPaths) ? existingTab.selectedPaths : []);
        // 优先 tab 路径：空 workspace 的 pane 默认 '/' 会与首页 tab 的 '/root' 分叉
        const targetPath = normalizePath(existingTab.path || existingPane.path) || '/';
        activeFileManagerTabIdRef.current = existingTab.id;
        displayedTabIdRef.current = existingTab.id;
        setSortField(nextSortField);
        setSortDir(nextSortDir);
        if (targetPath === currentPathRef.current) {
          displayedTabIdRef.current = existingTab.id;
          setSelectedPaths(nextSelectedPaths);
          lastClickedPathRef.current = nextSelectedPaths[nextSelectedPaths.length - 1] || null;
        } else {
          setPaneSelectionRestore(resolvedInitialPaneKey, {
            selectedPaths: nextSelectedPaths,
            lastClickedPath: nextSelectedPaths[nextSelectedPaths.length - 1] || null,
          });
        }
        getPaneViewState(resolvedInitialPaneKey).pendingRestore = {
          key: '',
          offset: 0,
          scrollTop: Number(existingPane.scrollTop ?? existingTab.scrollTop) || 0,
        };
        let ok = await loadDir(targetPath, {
          tabId: existingTab.id,
          silent: true,
          preserveView: false,
          trackDiff: false,
          // 恢复工作区时优先用已重映射的目录缓存首屏渲染，后台再刷新；无缓存才显示 loading
          showLoading: hasRememberedTabs ? undefined : true,
          preferPathCache: hasRememberedTabs,
        });
        if (!ok && isFileManagerTabLoadSuperseded(existingTab.id)) {
          return;
        }
        if (!ok && !cancelled && existingTab.pinned !== true) {
          setPaneSelectionRestore(resolvedInitialPaneKey, { selectedPaths: [], lastClickedPath: null });
          getPaneViewState(resolvedInitialPaneKey).pendingRestore = { key: '', offset: 0, scrollTop: 0 };
          // 原目录读不到时直接回落根目录：不再试探 /root（非 root 用户必然失败）
          await loadDir('/', {
            tabId: existingTab.id,
            silent: true,
            preserveView: false,
            trackDiff: false,
            showLoading: true,
            preserveWorkspacePathOnSuccess: true,
          });
        }
      } finally {
        if (!cancelled) {
          initializingPathRef.current = false;
          // 首屏未真正加载（如工作区无标签直接返回）时收掉初始化加载态，避免常驻转圈
          if (!currentPathHydratedRef.current) {
            setLoading(false);
          }
          const pendingPath = normalizePath(pendingTerminalCwdRef.current);
          if (hasRememberedTabs) {
            // 恢复工作区时不强制跟随初始化期间到达的启动 cwd；若恰好已收到启动事件，
            // 锚点已被消费，清掉避免再误吞一次真实 cd。
            if (pendingPath && normalizePath(restoredBootstrapCwdRef.current) === pendingPath) {
              restoredBootstrapCwdRef.current = '';
            }
            pendingTerminalCwdRef.current = '';
          } else {
            // 初始化期间积压的 cwd 在此应用
            if (pendingPath && pendingPath !== currentPathRef.current) {
              void applyTerminalCwdFollow(pendingPath, { force: true, showLoading: false });
            }
          }
        }
      }
    })();
    return () => {
      cancelled = true;
      initializingPathRef.current = false;
      // 未真正完成首屏加载时不锁定，下一次激活可重试
      if (!currentPathHydratedRef.current) {
        initializedSessionRef.current = '';
      }
    };
  }, [applyTerminalCwdFollow, ensureForcedInitialFileManagerTab, isActive, loadDir, normalizePath, resolveNonRememberedInitialPath, sessionId]);

  // 始终订阅终端 cwd（不依赖 isActive），避免面板隐藏时丢事件
  useEffect(() => {
    if (!sessionId) return undefined;
    const off = EventsOn(`ssh-terminal-cwd-${sessionId}`, (cwd) => {
      void applyTerminalCwdFollow(cwd);
    });
    return () => {
      off?.();
    };
  }, [applyTerminalCwdFollow, sessionId]);

  useEffect(() => {
    const offCompressed = EventsOn(`compressed-upload-progress-${sessionId}`, (payload = {}) => {
      const uploadId = typeof payload.uploadId === 'string' ? payload.uploadId.trim() : '';
      if (!uploadId) return;
      if (abortedUploadIdsRef.current.has(uploadId)) return;
      updateSessionUploadQueue(sessionGroupId, (current) => current.map((item) => {
        if (item.id !== uploadId) return item;
        const nextPhase = payload.phase || item.phase || 'preparing';
        const nextPhaseProgress = Math.max(0, Math.min(100, Number(payload.phaseProgress) || 0));
        const hasBytesDone = payload.bytesDone !== undefined && payload.bytesDone !== null && Number.isFinite(Number(payload.bytesDone));
        const hasBytesTotal = payload.bytesTotal !== undefined && payload.bytesTotal !== null && Number.isFinite(Number(payload.bytesTotal));
        return {
          ...item,
          phase: nextPhase,
          phaseProgress: nextPhaseProgress,
          progress: computeCompressedOverallProgress(nextPhase, nextPhaseProgress, item.progress),
          bytesUploaded: hasBytesDone ? Number(payload.bytesDone) : item.bytesUploaded,
          bytesTotal: hasBytesTotal ? Number(payload.bytesTotal) : item.bytesTotal,
          phaseCurrent: payload.current || '',
          phaseDetail: payload.detail || '',
          updatedAt: Date.now(),
        };
      }));
    });
    return () => {
      offCompressed?.();
    };
  }, [sessionId, sessionGroupId]);

  useEffect(() => {
    const offDownload = EventsOn(`download-transfer-progress-${sessionId}`, (payload = {}) => {
      const downloadId = typeof payload.downloadId === 'string' ? payload.downloadId.trim() : '';
      if (!downloadId) return;
      if (abortedUploadIdsRef.current.has(downloadId)) return;
      updateSessionUploadQueue(sessionGroupId, (current) => current.map((item: TransferQueueItem) => {
        if (item.id !== downloadId) return item;
        const nextStatus = payload.status || item.status || 'uploading';
        const nextPhase = payload.phase || item.phase || '';
        const nextProgress = Math.max(0, Math.min(100, Number.isFinite(Number(payload.progress)) ? Number(payload.progress) : (nextStatus === 'completed' ? 100 : (item.progress || 0))));
        const hasBytesDone = payload.bytesDone !== undefined && payload.bytesDone !== null && Number.isFinite(Number(payload.bytesDone));
        const hasBytesTotal = payload.bytesTotal !== undefined && payload.bytesTotal !== null && Number.isFinite(Number(payload.bytesTotal));
        return {
          ...item,
          direction: 'download',
          mode: payload.mode || item.mode || 'download-file',
          status: nextStatus,
          phase: nextPhase,
          progress: nextProgress,
          bytesUploaded: hasBytesDone ? Number(payload.bytesDone) : item.bytesUploaded,
          bytesTotal: hasBytesTotal ? Number(payload.bytesTotal) : item.bytesTotal,
          phaseCurrent: payload.current || item.phaseCurrent || '',
          phaseDetail: payload.detail || item.phaseDetail || '',
          updatedAt: Date.now(),
        };
      }));
    });
    return () => {
      offDownload?.();
    };
  }, [sessionId, sessionGroupId]);

  // Breadcrumb parts
  const pathParts = currentPath === '/'
    ? [{ label: t('目录根'), path: '/' }]
    : currentPath.split('/').filter(Boolean).reduce((acc, part, i, arr) => {
        const path = '/' + arr.slice(0, i + 1).join('/');
        acc.push({ label: part, path });
        return acc;
      }, [{ label: t('目录根'), path: '/' }]);

  // Navigate into folder
  const navigate = (item: FileManagerFileItem) => {
    if (!item.isDirectory) return;
    const newPath = currentPath === '/'
      ? `/${item.name}`
      : `${currentPath}/${item.name}`;
    void loadDir(newPath, {
      preserveView: false,
      trackDiff: false,
      showLoading: true,
    });
  };

  return {
    loadDir,
    applyAnimatedFileListSnapshot,
    buildNonRememberedInitialPathCandidates,
    resolveNonRememberedInitialPath,
    ensureForcedInitialFileManagerTab,
    resolveTerminalCwdFollowTarget,
    applyTerminalCwdFollow,
    pathParts,
    navigate,
  };
}
