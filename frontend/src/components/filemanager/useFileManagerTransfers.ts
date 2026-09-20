import { useState, useEffect, useCallback, useRef } from 'react';
import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';
import { EventsOn } from '../../../wailsjs/runtime/runtime.js';
import {
  DEFAULT_FILE_MANAGER_DOWNLOAD_DIR,
  FILE_MANAGER_LAYOUT_MODE_SIDEBAR_DUAL,
  DOWNLOAD_CONFLICT_STRATEGY_AUTO_RENAME,
  DOWNLOAD_CONFLICT_STRATEGY_DIFF_OVERWRITE,
  DOWNLOAD_CONFLICT_STRATEGY_FORCE_OVERWRITE,
  DOWNLOAD_CONFLICT_STRATEGY_PROMPT,
  MAX_CHUNK_UPLOAD_RETRIES,
  UPLOAD_ABORT_SENTINEL,
  createLocalItemShell,
  downloadConflictKindLabel,
  fmtDate,
  fmtSize,
  getDownloadConflictSettingsFromStorage,
  isCompressedTransferEnabled,
  isHiddenFile,
  parsePositiveInt,
  readBlobAsBase64,
  runWithLimitSettled,
  uploadChunkWithRetry,
  upsertLocalItem,
} from '../../utils/fileManagerHelpers.tsx';
import {
  getSessionCachedFileManagerPathItems,
  getSessionUploadQueue,
  setSessionFileManagerWorkspace,
  setSessionCachedFileManagerPathItems,
  updateSessionUploadQueue,
} from '../../utils/fileWorkbench.ts';
import type { TransferChunk, TransferQueueItem } from '../../utils/fileWorkbench.ts';
import type { useFileManagerCore } from './useFileManagerCore.ts';
import type { useFileManagerWorkspaceSync } from './useFileManagerWorkspaceSync.ts';
import type { useFileManagerPaneView } from './useFileManagerPaneView.ts';
import type { useFileManagerClipboard } from './useFileManagerClipboard.ts';
import type { useFileManagerEditorState } from './useFileManagerEditorState.ts';
import type { useFileManagerUploadPanel } from './useFileManagerUploadPanel.ts';
import type { useFileManagerDirectoryLoader } from './useFileManagerDirectoryLoader.ts';
import type { FileManagerDownloadConflict, FileManagerFileItem, FileManagerProps } from './fileManagerTypes.ts';
import { buildDownloadConflictOptionsPayload, type FileManagerDownloadConflictSettings } from '../../utils/fileManagerTransfer.ts';

// 传输引擎：上传（原生路径/浏览器条目分块）、下载（含同名冲突向导）、
// 远端条目移动/复制、中止/恢复目录、撤销栈与传输队列事件
export function useFileManagerTransfers(deps: ReturnType<typeof useFileManagerCore> & ReturnType<typeof useFileManagerWorkspaceSync> & ReturnType<typeof useFileManagerPaneView> & ReturnType<typeof useFileManagerClipboard> & ReturnType<typeof useFileManagerEditorState> & ReturnType<typeof useFileManagerUploadPanel> & ReturnType<typeof useFileManagerDirectoryLoader> & {
  addToast?: FileManagerProps['addToast']
}) {
  const {
    sessionId, sessionGroupId, addToast, t, isActive,
    currentPath, currentPathRef, normalizePath, joinPath,
    queueRowEffect,
    abortedUploadIdsRef, fileManagerAutoRefreshDisabledRef,
    mountedRef, isActiveRef, currentPathHydratedRef, fileListRef,
    fileManagerWorkspaceRef, activePaneKey, fileManagerLayoutMode,
    setFileManagerWorkspaceState, items,
    cacheCurrentTabItems,
    openEditFilesRef, setOpenEditFiles,
    uploadInputRef, uploadFolderInputRef,
    setTransferInfo, openTransferQueueIfNeeded,
    getTransferTaskRunner, getUploadChunkRunner, closeUploadPanel,
    updateClipboard,
    loadDir,
    buildItemsWithTrackedDiff,
    queueRowEffectForMatchingPanes, updateItemsPreservingView,
    setSelectedPaths, lastClickedPathRef,
  } = deps;
  const [operationProgress, setOperationProgress] = useState<{ message: string; current?: number; total?: number } | null>(null);
  // 并发互斥闸门：用 ref 在同步阶段立即生效，避免两个快速事件都读到 stale 的 state 而双双放行
  const operationInProgressRef = useRef(false);

  const getUploadSettings = useCallback(() => ({
    chunkSizeKiB: parsePositiveInt(localStorage.getItem('fileManagerUploadChunkSizeKiB'), 256),
    maxTransferTasks: parsePositiveInt(localStorage.getItem('fileManagerUploadMaxFiles'), 6),
    maxChunksPerFile: parsePositiveInt(localStorage.getItem('fileManagerUploadMaxChunksPerFile'), 8),
    globalInflightLimit: parsePositiveInt(localStorage.getItem('fileManagerUploadGlobalInflightLimit'), 24),
  }), []);
  const getDefaultDownloadDir = useCallback(() => (
    localStorage.getItem('fileManagerDownloadDefaultDir') || DEFAULT_FILE_MANAGER_DOWNLOAD_DIR
  ).trim() || DEFAULT_FILE_MANAGER_DOWNLOAD_DIR, []);
  const getDownloadConflictSettings = useCallback(() => getDownloadConflictSettingsFromStorage(), []);
  const buildDownloadConflictMessage = useCallback((conflict: FileManagerDownloadConflict, fallbackName: unknown) => {
    const relativePath = String(conflict?.relativePath || '').trim() || fallbackName || t('当前文件');
    const localSize = conflict?.localSize === undefined || conflict?.localSize === null ? '-' : fmtSize(Number(conflict.localSize) || 0);
    const remoteSize = conflict?.remoteSize === undefined || conflict?.remoteSize === null ? '-' : fmtSize(Number(conflict.remoteSize) || 0);
    const localModifyTime = conflict?.localModifyTime === undefined || conflict?.localModifyTime === null ? '-' : fmtDate(Number(conflict.localModifyTime));
    const remoteModifyTime = conflict?.remoteModifyTime === undefined || conflict?.remoteModifyTime === null ? '-' : fmtDate(Number(conflict.remoteModifyTime));
    const lines = [
      `${t('冲突项')}: ${relativePath}`,
      `${t('本地路径')}: ${conflict?.localPath || '-'}`,
      `${t('本地类型')}: ${downloadConflictKindLabel(conflict?.localKind, t)}`,
      `${t('远端类型')}: ${downloadConflictKindLabel(conflict?.remoteKind, t)}`,
    ];
    if (conflict?.localKind === 'file' || conflict?.remoteKind === 'file') {
      lines.push(`${t('本地大小')}: ${localSize}`);
      lines.push(`${t('远端大小')}: ${remoteSize}`);
      lines.push(`${t('本地修改时间')}: ${localModifyTime}`);
      lines.push(`${t('远端修改时间')}: ${remoteModifyTime}`);
    }
    lines.push('');
    lines.push(t('请选择本次冲突的处理方式'));
    return lines.join('\n');
  }, [t]);
  const resolvePromptDownloadConflict = useCallback(async (item: FileManagerFileItem, remotePath: unknown, localPath: string, settings: FileManagerDownloadConflictSettings) => {
    const previewDownloadConflicts = window?.go?.wailsapp?.App?.PreviewDownloadConflicts;
    const resolveDownloadLocalPath = window?.go?.wailsapp?.App?.ResolveDownloadLocalPath;
    if (typeof previewDownloadConflicts !== 'function') {
      throw new Error(t('当前环境不支持下载冲突处理'));
    }
    const conflicts = await previewDownloadConflicts(sessionId, String(remotePath || ''), String(localPath || ''), item.isDirectory);
    if (!Array.isArray(conflicts) || conflicts.length === 0) {
      return {
        localPath,
        optionsJSON: buildDownloadConflictOptionsPayload(settings, {
          strategy: DOWNLOAD_CONFLICT_STRATEGY_FORCE_OVERWRITE,
          pathStrategies: {},
        }),
      };
    }
    const buttons = [
      { label: t('差异覆盖'), value: DOWNLOAD_CONFLICT_STRATEGY_DIFF_OVERWRITE, primary: true },
      { label: t('强制覆盖'), value: DOWNLOAD_CONFLICT_STRATEGY_FORCE_OVERWRITE },
      { label: t('自动重命名'), value: DOWNLOAD_CONFLICT_STRATEGY_AUTO_RENAME },
      { label: t('取消'), value: 'cancel', secondary: true },
    ];
    const autoRenameOptionsJSON = buildDownloadConflictOptionsPayload(settings, {
      strategy: DOWNLOAD_CONFLICT_STRATEGY_AUTO_RENAME,
      pathStrategies: {},
    });
    for (const conflict of conflicts) {
      const choice = await window.lumeDialog?.choice(
        buildDownloadConflictMessage(conflict, item.name),
        t('下载同名冲突'),
        buttons,
        t('应用到本次剩余冲突'),
      );
      const choiceValue = choice && typeof choice === 'object' ? (choice as { value?: string; checked?: boolean }).value : undefined;
      const choiceChecked = choice && typeof choice === 'object' ? (choice as { value?: string; checked?: boolean }).checked : false;
      if (!choiceValue || choiceValue === 'cancel') {
        return null;
      }
      if (choiceChecked) {
        if (choiceValue === DOWNLOAD_CONFLICT_STRATEGY_AUTO_RENAME) {
          const renamedPath = typeof resolveDownloadLocalPath === 'function'
            ? await resolveDownloadLocalPath(localPath, item.isDirectory, autoRenameOptionsJSON)
            : localPath;
          return {
            localPath: renamedPath || localPath,
            optionsJSON: autoRenameOptionsJSON,
          };
        }
        return {
          localPath,
          optionsJSON: buildDownloadConflictOptionsPayload(settings, {
            strategy: choiceValue,
            pathStrategies: {},
          }),
        };
      }
      const conflictKey = String(conflict?.key || '.').trim() || '.';
      if (conflictKey === '.' && choiceValue === DOWNLOAD_CONFLICT_STRATEGY_AUTO_RENAME) {
        const renamedPath = typeof resolveDownloadLocalPath === 'function'
          ? await resolveDownloadLocalPath(localPath, item.isDirectory, autoRenameOptionsJSON)
          : localPath;
        return {
          localPath: renamedPath || localPath,
          optionsJSON: autoRenameOptionsJSON,
        };
      }
      settings = {
        ...settings,
        pathStrategies: {
          ...(settings.pathStrategies || {}),
          [conflictKey]: choiceValue,
        },
      };
    }
    return {
      localPath,
      optionsJSON: buildDownloadConflictOptionsPayload(settings, {
        strategy: DOWNLOAD_CONFLICT_STRATEGY_FORCE_OVERWRITE,
        pathStrategies: settings.pathStrategies || {},
      }),
    };
  }, [buildDownloadConflictMessage, sessionId, t]);

  const isUploadAbortable = useCallback((item: TransferQueueItem) => {
    if (!item) return false;
    if (item.direction === 'download') {
      if (item.mode === 'download-compressed') {
        return ['preparing', 'compressing', 'downloading', 'extracting'].includes(item.phase ?? '');
      }
      return item.status === 'queued' || item.status === 'uploading';
    }
    if (item.mode === 'compressed') {
      return ['preparing', 'scanning', 'compressing', 'uploading', 'uploading-file', 'verifying', 'extracting'].includes(item.phase ?? '');
    }
    return item.status === 'queued' || item.status === 'uploading';
  }, []);

  const markUploadAborted = useCallback((queueId: unknown, detail: string = t('已终止')) => {
    if (!queueId) return;
    abortedUploadIdsRef.current.add(String(queueId));
    updateSessionUploadQueue(sessionGroupId, (current) => current.map((item) => (
      item.id === queueId
        ? {
            ...item,
            status: 'failed',
            phase: item.mode === 'compressed' ? 'failed' : item.phase,
            phaseDetail: detail,
            error: detail,
            updatedAt: Date.now(),
          }
        : item
    )));
  }, [sessionGroupId, t]);

  const abortUploadItem = useCallback(async (item: TransferQueueItem, detail: string = t('已终止')) => {
    if (!item) return;
    markUploadAborted(item.id, detail);
    try {
      if (item.direction === 'download') {
        await window?.go?.wailsapp?.App?.AbortDownloadTransfer?.(item.id);
        return;
      }
      if (item.mode === 'compressed') {
        await window?.go?.wailsapp?.App?.AbortCompressedUpload?.(item.id);
        return;
      }
      if (item.taskId && item.fileId) {
        await AppGo.AbortChunkedUploadFile(String(item.taskId), String(item.fileId)).catch(() => {});
      }
    } catch (_) {}
  }, [markUploadAborted, t]);

  const removeUploadItems = useCallback((ids: unknown) => {
    const normalizedIds = new Set(
      Array.from((ids || []) as unknown[])
        .map((id) => String(id || '').trim())
        .filter(Boolean),
    );
    if (normalizedIds.size === 0) {
      return;
    }
    normalizedIds.forEach((id) => abortedUploadIdsRef.current.delete(id));
    let shouldClosePanel = false;
    updateSessionUploadQueue(sessionGroupId, (current) => {
      const next = current.filter((item) => !normalizedIds.has(item.id));
      shouldClosePanel = next.length === 0;
      return next;
    });
    if (shouldClosePanel) {
      closeUploadPanel();
    }
  }, [closeUploadPanel, sessionGroupId]);

  const abortUploadItems = useCallback((items: TransferQueueItem[], detail: string = t('已终止')) => {
    (items || []).forEach((item) => {
      if (item) {
        void abortUploadItem(item, detail);
      }
    });
  }, [abortUploadItem, t]);

  const abortActiveUploadsForSession = useCallback((disconnectedSessionId: unknown, detail: string = t('已终止')) => {
    if (!disconnectedSessionId || disconnectedSessionId !== sessionId) return;
    const queue = getSessionUploadQueue(sessionGroupId)
      .filter((item) => item?.sourceTerminalId === disconnectedSessionId)
      .filter((item) => isUploadAbortable(item));
    queue.forEach((item) => {
      void abortUploadItem(item, detail);
    });
  }, [abortUploadItem, isUploadAbortable, sessionGroupId, sessionId, t]);

  useEffect(() => () => {
    abortActiveUploadsForSession(sessionId, t('已终止'));
  }, [abortActiveUploadsForSession, sessionId, t]);

  const refreshDirectoryAfterTransfer = useCallback(async (targetPath: unknown) => {
    const normalizedTargetPath = normalizePath(targetPath) || '/';
    if (!normalizedTargetPath) {
      return;
    }
    if (normalizedTargetPath === currentPathRef.current) {
      await loadDir(currentPathRef.current, { preserveView: true, showLoading: false });
      return;
    }
    try {
      const nextItems = await AppGo.ListDir(sessionId, normalizedTargetPath);
      const currentWorkspace = fileManagerWorkspaceRef.current;
      const matchingPaneKeys: Array<'left' | 'right'> = [];
      const leftPanePath = normalizePath(currentWorkspace?.panes?.left?.path) || '';
      const rightPanePath = normalizePath(currentWorkspace?.panes?.right?.path) || '';
      if (leftPanePath && leftPanePath === normalizedTargetPath) {
        matchingPaneKeys.push('left');
      }
      if (rightPanePath && rightPanePath === normalizedTargetPath) {
        matchingPaneKeys.push('right');
      }

      const cachedItemsSnapshot = (getSessionCachedFileManagerPathItems(sessionId, normalizedTargetPath) || []) as FileManagerFileItem[];
      const primaryPaneKey = matchingPaneKeys[0] || activePaneKey;
      const mergedItems = buildItemsWithTrackedDiff(
        cachedItemsSnapshot,
        (nextItems || []) as FileManagerFileItem[],
        normalizedTargetPath,
        primaryPaneKey,
      );
      if (matchingPaneKeys.length > 1) {
        matchingPaneKeys.slice(1).forEach((paneKey) => {
          buildItemsWithTrackedDiff(cachedItemsSnapshot, (nextItems || []) as FileManagerFileItem[], normalizedTargetPath, paneKey);
        });
      }

      setSessionCachedFileManagerPathItems(sessionId, normalizedTargetPath, mergedItems);
      const matchingTabIds = Array.isArray(currentWorkspace?.tabs)
        ? currentWorkspace.tabs
          .filter((tab) => (normalizePath(tab?.path) || '/') === normalizedTargetPath)
          .map((tab) => String(tab?.id || '').trim())
          .filter(Boolean)
        : [];
      matchingTabIds.forEach((tabId) => {
        cacheCurrentTabItems(tabId, normalizedTargetPath, mergedItems);
      });
      if (fileManagerLayoutMode === FILE_MANAGER_LAYOUT_MODE_SIDEBAR_DUAL && matchingPaneKeys.length > 0) {
        const nextWorkspace = setSessionFileManagerWorkspace(sessionId, (current) => (
          current
            ? { ...current, panes: { ...(current.panes || {}) } }
            : current
        ));
        fileManagerWorkspaceRef.current = nextWorkspace;
        setFileManagerWorkspaceState(nextWorkspace);
      }
    } catch (_) {}
  }, [activePaneKey, buildItemsWithTrackedDiff, cacheCurrentTabItems, fileManagerLayoutMode, loadDir, normalizePath, sessionId]);

  // 自动刷新：终端命令完成（方案 A）+ 窗口/面板聚焦（方案 C 兜底）时，
  // 防抖刷新当前目录。SFTP 无远程变更通知，只能客户端主动探测；按需刷新避免轰炸服务器。
  useEffect(() => {
    if (!sessionGroupId) return;
    let refreshTimer: ReturnType<typeof setTimeout> | null = null;
    const scheduleRefresh = (delay: number) => {
      if (refreshTimer) clearTimeout(refreshTimer);
      refreshTimer = setTimeout(() => {
        refreshTimer = null;
        // 仅在自动刷新开启、组件挂载、当前会话激活（可见）且目录已 hydrate 时刷新
        if (
          fileManagerAutoRefreshDisabledRef.current
          || !mountedRef.current
          || !isActiveRef.current
          || !currentPathHydratedRef.current
        ) return;
        const targetPath = currentPathRef.current || currentPath;
        if (targetPath) {
          void loadDir(targetPath, { preserveView: true, showLoading: false, silent: true });
        }
      }, delay);
    };
    // A：终端命令完成（提示符回归）。事件携带会话组 id，匹配本会话组才刷新。
    const handleCommandFinished = (e: Event) => {
      const detail = (e as CustomEvent).detail || {}
      if (!detail || detail.sessionId !== sessionGroupId) return;
      scheduleRefresh(800);
    };
    // C：应用窗口重新聚焦（兜底 A 的漏判：自定义提示符/TUI 等）。
    const handleWindowFocus = () => scheduleRefresh(400);
    window.addEventListener('ssh-command-finished', handleCommandFinished);
    window.addEventListener('focus', handleWindowFocus);
    return () => {
      if (refreshTimer) clearTimeout(refreshTimer);
      window.removeEventListener('ssh-command-finished', handleCommandFinished);
      window.removeEventListener('focus', handleWindowFocus);
    };
  }, [loadDir, sessionGroupId, currentPath]);

  // C 补充：isActive 由 false→true（切回文件管理器面板）时立即触发一次刷新。
  const prevIsActiveRef = useRef(isActive);
  useEffect(() => {
    const wasActive = prevIsActiveRef.current;
    prevIsActiveRef.current = isActive;
    if (!isActive || wasActive) return; // 仅在切"进来"时触发
    if (fileManagerAutoRefreshDisabledRef.current || !currentPathHydratedRef.current) return;
    const targetPath = currentPathRef.current || currentPath;
    if (targetPath) {
      void loadDir(targetPath, { preserveView: true, showLoading: false, silent: true });
    }
  }, [isActive, loadDir, currentPath]);

  const fileManagerUndoStackRef = useRef<unknown[]>([]);

  const pushFileManagerUndoEntry = useCallback((entry: unknown) => {
    if (!entry || typeof (entry as { undo?: unknown }).undo !== 'function') {
      return;
    }
    fileManagerUndoStackRef.current = [...fileManagerUndoStackRef.current, entry].slice(-100);
  }, []);

  const handleUndoFileManagerAction = useCallback(async () => {
    if (operationInProgressRef.current) return;
    const undoEntry = fileManagerUndoStackRef.current[fileManagerUndoStackRef.current.length - 1];
    if (!undoEntry) {
      addToast?.(t('没有可撤销的操作'), 'info');
      return;
    }
    fileManagerUndoStackRef.current = fileManagerUndoStackRef.current.slice(0, -1);
    operationInProgressRef.current = true;
    setOperationProgress({ message: t('正在撤销中...') });
    try {
      await (undoEntry as { undo: () => unknown }).undo();
      addToast?.(t('已撤销'), 'success');
    } catch (err) {
      fileManagerUndoStackRef.current = [...fileManagerUndoStackRef.current, undoEntry].slice(-100);
      addToast?.(`${t('撤销失败')}: ${err}`, 'error');
    } finally {
      setOperationProgress(null);
      operationInProgressRef.current = false;
      fileListRef.current?.focus();
    }
  }, [addToast, t]);

  const uploadNativePaths = useCallback(async (paths: unknown) => {
    const localPaths = Array.from((paths || []) as unknown[]).map((path) => String(path || '').trim()).filter(Boolean);
    if (localPaths.length === 0) {
      return;
    }
    const uploadTargetPath = normalizePath(currentPathRef.current || currentPath) || '/';
    openTransferQueueIfNeeded();
    const settings = getUploadSettings();
    const createdAt = Date.now();
    const displayName = localPaths.length === 1
      ? (localPaths[0].split(/[\\/]/).filter(Boolean).pop() || t('文件'))
      : `${localPaths.length} ${t('项')}`;
    const queueId = `native-upload-${createdAt}`;
    const queueItem: TransferQueueItem = {
      id: queueId,
      name: displayName,
      relativePath: displayName,
      remotePath: joinPath(uploadTargetPath, displayName),
      status: 'queued',
      progress: 0,
      bytesUploaded: 0,
      bytesTotal: 0,
      chunkSizeBytes: Math.max(1, settings.chunkSizeKiB * 1024),
      chunksTotal: 0,
      chunksCompleted: 0,
      chunksFailed: 0,
      chunks: [],
      error: '',
      sourceTerminalId: sessionId,
      mode: 'compressed',
      phase: 'preparing',
      phaseProgress: 0,
      phaseCurrent: '',
      phaseDetail: t('准备上传'),
      localPathCount: localPaths.length,
      createdAt,
      updatedAt: createdAt,
    };
    updateSessionUploadQueue(sessionGroupId, (current) => [queueItem, ...current]);
    const patchQueueItem = (patch: Record<string, unknown> | ((item: TransferQueueItem) => TransferQueueItem)) => {
      updateSessionUploadQueue(sessionGroupId, (current) => current.map((item: TransferQueueItem) => (
        item.id === queueId
          ? { ...item, ...(typeof patch === 'function' ? patch(item) : patch) }
          : item
      )));
    };
    const transferTaskRunner = getTransferTaskRunner(settings.maxTransferTasks);
    let uploadSucceeded = false;
    await transferTaskRunner(async () => {
      if (abortedUploadIdsRef.current.has(queueId)) {
        return;
      }
      patchQueueItem({ status: 'uploading', updatedAt: Date.now() });
      try {
        await window?.go?.wailsapp?.App?.UploadLocalPathsCompressed?.(
          sessionId,
          queueId,
          Math.max(1, settings.maxChunksPerFile),
          localPaths,
          uploadTargetPath,
        );
        patchQueueItem({
          status: 'completed',
          phase: 'completed',
          phaseProgress: 100,
          progress: 100,
          error: '',
          phaseDetail: t('已完成'),
          updatedAt: Date.now(),
        });
        uploadSucceeded = true;
        addToast?.(`${t('上传成功')}: ${displayName}`, 'success');
      } catch (err) {
        const isAborted = abortedUploadIdsRef.current.has(queueId) || String(err).toLowerCase().includes('context canceled');
        patchQueueItem({
          status: 'failed',
          phase: 'failed',
          phaseDetail: isAborted ? t('已终止') : String(err),
          error: isAborted ? t('已终止') : String(err),
          updatedAt: Date.now(),
        });
        if (!isAborted) {
          addToast?.(`${t('上传失败')}: ${displayName}: ${err}`, 'error');
        }
      }
    });
    if (uploadSucceeded) {
      await refreshDirectoryAfterTransfer(uploadTargetPath);
    }
  }, [sessionId, sessionGroupId, currentPath, addToast, t, getTransferTaskRunner, getUploadSettings, openTransferQueueIfNeeded, normalizePath, refreshDirectoryAfterTransfer]);

  const uploadEntries = useCallback(async (entries: Array<Record<string, unknown>>) => {
    const uploadEntriesList = entries
      .filter((entry) => entry?.file && entry?.relativePath)
      .map((entry) => ({
        file: entry.file as File,
        relativePath: String(entry.relativePath).replace(/^\/+/, '').replace(/\\/g, '/'),
      }))
      .filter((entry) => entry.relativePath !== '');
    if (uploadEntriesList.length === 0) {
      return;
    }

    const uploadTargetPath = normalizePath(currentPathRef.current || currentPath) || '/';
    openTransferQueueIfNeeded();
    const settings = getUploadSettings();
    const chunkSizeBytes = Math.max(1, settings.chunkSizeKiB * 1024);
    const maxChunksPerFile = Math.max(1, settings.maxChunksPerFile);
    const globalInflightLimit = Math.max(1, settings.globalInflightLimit);
    const transferTaskRunner = getTransferTaskRunner(settings.maxTransferTasks);
    const globalChunkLimiter = getUploadChunkRunner(globalInflightLimit);
    const totalFiles = uploadEntriesList.length;
    const totalBytes = uploadEntriesList.reduce((sum, entry) => sum + entry.file.size, 0);
    const createdAt = Date.now();
    const queueSeed = uploadEntriesList.map((entry, index) => {
      const totalChunks = entry.file.size > 0 ? Math.ceil(entry.file.size / chunkSizeBytes) : 0;
      return {
        id: `upload-${createdAt}-${index}`,
        name: entry.file.name,
        relativePath: entry.relativePath,
        remotePath: joinPath(uploadTargetPath, entry.relativePath),
        status: 'queued',
        progress: 0,
        bytesUploaded: 0,
        bytesTotal: entry.file.size,
        chunkSizeBytes,
        chunksTotal: totalChunks,
        chunksCompleted: 0,
        chunksFailed: 0,
        chunks: Array.from({ length: totalChunks }, (_, chunkIndex) => {
          const start = chunkIndex * chunkSizeBytes;
          const end = Math.min(entry.file.size, start + chunkSizeBytes);
          return {
            index: chunkIndex,
            start,
            end,
            size: end - start,
            status: 'queued',
            attempt: 0,
            error: '',
            updatedAt: createdAt + index,
          };
        }),
        error: '',
        sourceTerminalId: sessionId,
        createdAt: createdAt + index,
        updatedAt: createdAt + index,
      };
    });
    updateSessionUploadQueue(sessionGroupId, (current) => [...queueSeed, ...current]);

    let uploadedBytes = 0;
    let completedFiles = 0;
    const failures: string[] = [];
    const patchQueueItem = (queueId: unknown, patch: Record<string, unknown> | ((item: TransferQueueItem) => TransferQueueItem)) => {
      updateSessionUploadQueue(sessionGroupId, (current) => current.map((item: TransferQueueItem) => (
        item.id === queueId
          ? { ...item, ...(typeof patch === 'function' ? patch(item) : patch) }
          : item
      )));
    };
    const patchQueueChunk = (queueId: unknown, chunkIndex: number, patch: Record<string, unknown> | ((chunk: TransferChunk) => TransferChunk)) => {
      updateSessionUploadQueue(sessionGroupId, (current) => current.map((item: TransferQueueItem) => {
        if (item.id !== queueId) return item;
        const chunks = Array.isArray(item.chunks) ? item.chunks.map((chunk) => (
          chunk.index === chunkIndex ? { ...chunk, ...(typeof patch === 'function' ? patch(chunk) : patch) } : chunk
        )) : [];
        return {
          ...item,
          chunks,
          chunksCompleted: chunks.filter((chunk) => chunk.status === 'completed').length,
          chunksFailed: chunks.filter((chunk) => chunk.status === 'failed').length,
          updatedAt: Date.now(),
        };
      }));
    };
    const updateTransfer = (activeName = '') => {
      const progress = totalBytes > 0
        ? Math.min(100, (uploadedBytes / totalBytes) * 100)
        : (completedFiles / totalFiles) * 100;
      setTransferInfo({
        name: activeName ? `${completedFiles}/${totalFiles} · ${activeName}` : `${completedFiles}/${totalFiles}`,
        progress,
        direction: 'upload',
      });
    };

    try {
      setTransferInfo({ name: `0/${totalFiles}`, progress: 0, direction: 'upload' });
      await Promise.all(uploadEntriesList.map(({ file, relativePath }, fileIndex) => transferTaskRunner(async () => {
        const queueId = queueSeed[fileIndex]?.id;
        if (!queueId || abortedUploadIdsRef.current.has(queueId)) {
          return;
        }
        let taskId = '';
        let fileId = '';
        let fileUploadedBytes = 0;
        try {
          patchQueueItem(queueId, { status: 'uploading', updatedAt: Date.now() });
          taskId = await AppGo.BeginChunkedUploadTask(sessionId, uploadTargetPath, Math.max(1, Math.min(maxChunksPerFile, globalInflightLimit)));
          patchQueueItem(queueId, { taskId, updatedAt: Date.now() });
          const totalChunks = file.size > 0 ? Math.ceil(file.size / chunkSizeBytes) : 0;
          fileId = await AppGo.BeginChunkedUploadFile(taskId, relativePath, file.size, totalChunks);
          patchQueueItem(queueId, { fileId, updatedAt: Date.now() });
          const chunkIndexes = Array.from({ length: totalChunks }, (_, index) => index);
          const chunkResults = await runWithLimitSettled(chunkIndexes, maxChunksPerFile, async (chunkIndex) => {
            const start = chunkIndex * chunkSizeBytes;
            const end = Math.min(file.size, start + chunkSizeBytes);
            const chunkLabel = `${file.name} 分块 ${chunkIndex + 1}/${Math.max(totalChunks, 1)} [${start}-${end})`;
            await globalChunkLimiter(async () => {
              if (abortedUploadIdsRef.current.has(queueId)) {
                throw new Error(UPLOAD_ABORT_SENTINEL);
              }
              patchQueueChunk(queueId, chunkIndex, { status: 'reading', attempt: 0, error: '', updatedAt: Date.now() });
              const content = await readBlobAsBase64(file.slice(start, end));
              await uploadChunkWithRetry(chunkLabel, () => AppGo.UploadChunkBase64(taskId, fileId, chunkIndex, start, content), (attempt, error) => {
                patchQueueChunk(queueId, chunkIndex, {
                  status: error ? 'retrying' : 'uploading',
                  attempt,
                  error: error ? String(error) : '',
                  updatedAt: Date.now(),
                });
              }, () => abortedUploadIdsRef.current.has(queueId));
              patchQueueChunk(queueId, chunkIndex, { status: 'completed', error: '', updatedAt: Date.now() });
              const delta = end - start;
              uploadedBytes += delta;
              fileUploadedBytes += delta;
              patchQueueItem(queueId, {
                status: 'uploading',
                bytesUploaded: fileUploadedBytes,
                progress: file.size > 0 ? Math.min(100, (fileUploadedBytes / file.size) * 100) : 100,
                updatedAt: Date.now(),
              });
              updateTransfer(file.name);
            });
          });
          const failedChunks = chunkResults
            .map((result, index) => ({ result, index }))
            .filter(({ result }) => result.status === 'rejected');
          if (failedChunks.length > 0) {
            failedChunks.forEach(({ result, index }) => {
              patchQueueChunk(queueId, index, {
                status: 'failed',
                attempt: MAX_CHUNK_UPLOAD_RETRIES,
                error: String(result.status === 'rejected' ? result.reason : ''),
                updatedAt: Date.now(),
              });
            });
            throw new Error(failedChunks.map(({ result }) => String(result.status === 'rejected' ? result.reason : '')).slice(0, 3).join('；'));
          }
          await AppGo.CompleteChunkedUploadFile(taskId, fileId);
          completedFiles++;
          patchQueueItem(queueId, {
            status: 'completed',
            bytesUploaded: file.size,
            progress: 100,
            error: '',
            updatedAt: Date.now(),
          });
          updateTransfer(file.name);
        } catch (err) {
          const isAborted = abortedUploadIdsRef.current.has(queueId) || String(err).includes(UPLOAD_ABORT_SENTINEL);
          if (!isAborted) {
            failures.push(`${relativePath}: ${err}`);
          }
          patchQueueItem(queueId, {
            status: 'failed',
            error: isAborted ? t('已终止') : String(err),
            updatedAt: Date.now(),
          });
          if (fileId && taskId) {
            await AppGo.AbortChunkedUploadFile(taskId, fileId).catch(() => {});
          } else if (taskId) {
            await AppGo.AbortChunkedUploadTask(taskId).catch(() => {});
          } else if (isAborted) {
            markUploadAborted(queueId);
          }
        } finally {
          if (taskId) {
            await AppGo.FinishChunkedUploadTask(taskId).catch(() => {});
          }
        }
      })));

      if (failures.length > 0) {
        addToast?.(`${completedFiles > 0 ? t('上传完成') : t('上传失败')}: ${completedFiles}${t('项成功')}, ${failures.length}${t('项失败')} (${failures.slice(0, 3).join(', ')})`, 'error');
      } else if (completedFiles > 0) {
        addToast?.(`${t('上传成功')}: ${completedFiles}${t('项')}`, 'success');
      }
      if (completedFiles > 0) {
        await refreshDirectoryAfterTransfer(uploadTargetPath);
      }
    } catch (err) {
      if (err) addToast?.(`${t('上传失败')}: ${err}`, 'error');
    } finally {
      if (mountedRef.current) setTransferInfo(null);
    }
  }, [sessionId, sessionGroupId, currentPath, getTransferTaskRunner, getUploadChunkRunner, getUploadSettings, addToast, t, markUploadAborted, openTransferQueueIfNeeded, normalizePath, refreshDirectoryAfterTransfer]);

  useEffect(() => {
    const off = EventsOn('ssh-disconnected', (payload) => {
      const data = (payload && typeof payload === 'object')
        ? payload
        : { sessionId: payload, terminalIds: payload ? [payload] : [] };
      const ids = new Set<string>();
      if (data.sessionId) ids.add(data.sessionId);
      if (data.parentSessionId) ids.add(data.parentSessionId);
      if (Array.isArray(data.terminalIds)) {
        const rawTerminalIds = data.terminalIds
        rawTerminalIds.forEach((id: unknown) => id && ids.add(String(id)));
      }
      ids.forEach((id) => abortActiveUploadsForSession(id, t('已终止')));
    });
    return () => {
      off?.();
    };
  }, [abortActiveUploadsForSession, t]);

  useEffect(() => {
    if (!sessionId) return undefined;
    const offSynced = EventsOn('external-edit-synced', (payload = {}) => {
      if (payload.sessionId !== sessionId) return;
      const remotePath = payload.remotePath || '';
      addToast?.(`${t('外部编辑已同步到远程')}${remotePath ? `: ${remotePath}` : ''}`, 'success');
      // Refresh in-memory editor buffer if the same file is open internally.
      if (remotePath && openEditFilesRef.current.some((f) => f.path === remotePath)) {
        AppGo.ReadFile(sessionId, remotePath)
          .then((content) => {
            setOpenEditFiles((prev) => prev.map((f) => (f.path === remotePath ? { ...f, content } : f)));
          })
          .catch(() => {});
      }
    });
    const offError = EventsOn('external-edit-error', (payload = {}) => {
      if (payload.sessionId !== sessionId) return;
      addToast?.(`${t('外部编辑同步失败')}: ${payload.error || ''}`, 'error');
    });
    return () => {
      offSynced?.();
      offError?.();
    };
  }, [sessionId, addToast, t]);

  const handleSelectedFiles = useCallback(async (e: React.ChangeEvent<HTMLInputElement>) => {
    const rawSelectedFiles = Array.from(e.target.files || []);
    const selectedFiles = rawSelectedFiles
      .filter((file) => !isHiddenFile(file.name))
      .map((file) => ({
        file,
        relativePath: file.webkitRelativePath || file.name,
      }));
    e.target.value = '';
    if (selectedFiles.length === 0) {
      return;
    }
    await uploadEntries(selectedFiles);
  }, [uploadEntries]);

  const handleUpload = async () => {
    if (!isCompressedTransferEnabled()) {
      uploadInputRef.current?.click();
      return;
    }
    try {
      const paths = await AppGo.SelectUploadFiles();
      await uploadNativePaths(paths || []);
    } catch (err) {
      if (err) addToast?.(`${t('上传失败')}: ${err}`, 'error');
    }
  };

  const handleUploadFolder = useCallback(async () => {
    if (!isCompressedTransferEnabled()) {
      uploadFolderInputRef.current?.click();
      return;
    }
    try {
      const dirPath = await AppGo.SelectUploadDirectory();
      if (!dirPath) {
        return;
      }

      await uploadNativePaths([dirPath]);
    } catch (err) {
      if (err) addToast?.(`${t('上传失败')}: ${err}`, 'error');
    }
  }, [uploadNativePaths, addToast, t]);

  const transferFileManagerItems = useCallback(async ({
    paths,
    mode,
    sourceDir,
    targetDirPath,
    clearClipboardOnSuccess = false,
  }: { paths: unknown; mode: string; sourceDir: unknown; targetDirPath: unknown; clearClipboardOnSuccess?: boolean }) => {
    if (operationInProgressRef.current) return false;
    const normalizedPaths = Array.isArray(paths)
      ? paths.map((path) => String(path || '').trim()).filter(Boolean)
      : [];
    if (normalizedPaths.length === 0) return false;
    const normalizedSourcePath = normalizePath(sourceDir) || '/';
    const normalizedTargetPath = normalizePath(targetDirPath) || '/';
    const visibleCurrentPath = normalizePath(currentPathRef.current || currentPath) || '/';
    const isCurrentTargetPath = normalizedTargetPath === visibleCurrentPath;
    const isCurrentSourcePath = normalizedSourcePath === visibleCurrentPath;
    if (normalizedSourcePath === normalizedTargetPath && mode === 'cut') {
      addToast?.(t('源目录与目标目录相同，无需移动'), 'warning');
      return false;
    }
    operationInProgressRef.current = true;
    let count = 0;
    let targetItems: FileManagerFileItem[] = isCurrentTargetPath ? items : [];
    try {
      if (!isCurrentTargetPath) {
        targetItems = (await AppGo.ListDir(sessionId, normalizedTargetPath) || []) as FileManagerFileItem[];
      }
    } catch (err) {
      operationInProgressRef.current = false;
      addToast?.(`${t('读取目录失败')}: ${err}`, 'error');
      return false;
    }
    const existing = new Set((Array.isArray(targetItems) ? targetItems : []).map((item) => item.name));
    const localPatchedItems: FileManagerFileItem[] = [];
    const successfulOperations = [];
    let shouldFallbackRefresh = !isCurrentTargetPath;
    const total = normalizedPaths.length;
    setOperationProgress({ message: t('正在粘贴中...'), current: 0, total });
    try {
      for (let i = 0; i < total; i++) {
        const srcPath = normalizedPaths[i];
        const name = srcPath.split('/').pop() || '';
        const sourceItem = normalizedSourcePath === normalizedTargetPath && isCurrentTargetPath
          ? items.find((entry) => entry.name === name)
          : null;
        let destPath = joinPath(normalizedTargetPath, name);
        let destName = name;
        let overwroteExisting = false;
        if (mode === 'copy' && normalizedSourcePath === normalizedTargetPath) {
          const base = name.replace(/(\.[^.]+)$/, '');
          const ext = name !== base ? name.slice(base.length) : '';
          let copyName = `${base}_copy${ext}`;
          let idx = 1;
          while (existing.has(copyName)) {
            idx++;
            copyName = `${base}_copy${idx}${ext}`;
          }
          destName = copyName;
          destPath = joinPath(normalizedTargetPath, copyName);
        } else if (existing.has(name)) {
          if (typeof window.lumeDialog?.confirm !== 'function') {
            addToast?.(`${t('无法确认覆盖操作，已跳过')} ${name}`, 'error');
            continue;
          }
          const ok = await window.lumeDialog.confirm(
            `${t('目标已存在同名项目')} "${name}"${t('，是否覆盖？')}`
          );
          if (!ok) continue;
          overwroteExisting = true;
        }
        setOperationProgress({
          message: `${mode === 'copy' ? t('正在复制') : t('正在移动')} ${name}`,
          current: i + 1,
          total,
        });
        try {
          if (mode === 'copy') {
            await AppGo.CopyItem(sessionId, srcPath, destPath);
          } else {
            await AppGo.MoveItem(sessionId, srcPath, destPath);
          }
          existing.add(destName);
          count++;
          successfulOperations.push({
            mode,
            srcPath,
            destPath,
            sourceDirPath: normalizedSourcePath,
            targetDirPath: normalizedTargetPath,
            overwroteExisting,
          });
          if (mode === 'copy' && sourceItem && isCurrentTargetPath) {
            localPatchedItems.push(createLocalItemShell(destName, sourceItem.isDirectory, {
              ...sourceItem,
              name: destName,
            }));
          } else {
            shouldFallbackRefresh = true;
          }
        } catch (err) {
          addToast?.(`${t('操作失败')}: ${name} - ${err}`, 'error');
        }
      }
    } finally {
      setOperationProgress(null);
      operationInProgressRef.current = false;
    }
    if (count > 0) {
      if (successfulOperations.length > 0 && successfulOperations.every((operation) => operation.overwroteExisting !== true)) {
        const undoOperations = [...successfulOperations];
        pushFileManagerUndoEntry({
          undo: async () => {
            for (let index = undoOperations.length - 1; index >= 0; index--) {
              const operation = undoOperations[index];
              if (operation.mode === 'copy') {
                await AppGo.DeleteItemShell(sessionId, operation.destPath);
              } else {
                await AppGo.MoveItem(sessionId, operation.destPath, operation.srcPath);
              }
            }
            const refreshTargets = new Set();
            undoOperations.forEach((operation) => {
              refreshTargets.add(operation.targetDirPath);
              if (operation.mode === 'cut') {
                refreshTargets.add(operation.sourceDirPath);
              }
            });
            for (const refreshPath of refreshTargets) {
              await refreshDirectoryAfterTransfer(refreshPath);
            }
          },
        });
      }
      addToast?.(`${t('操作完成')}: ${count} ${t('项')}`, 'success');
      if (clearClipboardOnSuccess && mode === 'cut') {
        updateClipboard(null);
      }
      if (!shouldFallbackRefresh && localPatchedItems.length === count) {
        localPatchedItems.forEach((localItem) => {
          const logicalPath = joinPath(normalizedTargetPath, localItem.name);
          queueRowEffectForMatchingPanes(normalizedTargetPath, logicalPath, logicalPath, 'added');
        });
        updateItemsPreservingView((prev) => localPatchedItems.reduce(
          (next, localItem) => upsertLocalItem(next, localItem),
          prev,
        ));
      } else {
        await refreshDirectoryAfterTransfer(normalizedTargetPath);
      }
      if (mode === 'cut' && normalizedSourcePath !== normalizedTargetPath) {
        if (isCurrentSourcePath) {
          setSelectedPaths((currentSelectedPaths) => currentSelectedPaths.filter((path) => !normalizedPaths.includes(path)));
          if (lastClickedPathRef.current && normalizedPaths.includes(lastClickedPathRef.current)) {
            lastClickedPathRef.current = null;
          }
        }
        await refreshDirectoryAfterTransfer(normalizedSourcePath);
      }
      return true;
    }
    return false;
  }, [addToast, currentPath, items, normalizePath, queueRowEffect, refreshDirectoryAfterTransfer, sessionId, t, updateItemsPreservingView]);

  const handleDownload = useCallback(async (item: FileManagerFileItem, options: Record<string, unknown> = {}) => {
    const basePath = typeof options.basePath === 'string' ? options.basePath : currentPath;
    const remotePath = joinPath(basePath, item.name);
    const defaultDownloadDir = getDefaultDownloadDir();
    const askDownloadEveryTime = localStorage.getItem('fileManagerAskDownloadEveryTime') === 'true';
    const resolveDownloadPath = window?.go?.wailsapp?.App?.ResolveDownloadPath;
    const resolveDownloadLocalPath = window?.go?.wailsapp?.App?.ResolveDownloadLocalPath;
    const selectDownloadFilePath = window?.go?.wailsapp?.App?.SelectDownloadFilePath;
    const selectDownloadDirectory = window?.go?.wailsapp?.App?.SelectDownloadDirectory;
    const downloadFileToLocal = window?.go?.wailsapp?.App?.DownloadFileToLocal;
    const downloadDirectoryToLocal = window?.go?.wailsapp?.App?.DownloadDirectoryToLocal;
    const downloadDirectoryCompressed = window?.go?.wailsapp?.App?.DownloadDirectoryCompressed;
    const createdAt = Date.now();
    let queueId = '';

    const patchQueueItem = (id: unknown, patch: Record<string, unknown> | ((queueItem: TransferQueueItem) => TransferQueueItem)) => {
      if (!id) return;
      updateSessionUploadQueue(sessionGroupId, (current) => current.map((queueItem) => (
        queueItem.id === id
          ? { ...queueItem, ...(typeof patch === 'function' ? patch(queueItem) : patch) }
          : queueItem
      )));
    };

    try {
      const conflictSettings = getDownloadConflictSettings();
      const initialPathOptionsJSON = buildDownloadConflictOptionsPayload(conflictSettings, {
        strategy: conflictSettings.strategy === DOWNLOAD_CONFLICT_STRATEGY_PROMPT
          ? DOWNLOAD_CONFLICT_STRATEGY_FORCE_OVERWRITE
          : conflictSettings.strategy,
        pathStrategies: {},
      });
      let localPath = '';

      if (askDownloadEveryTime) {
        if (item.isDirectory) {
          const selectedDir = await selectDownloadDirectory?.(defaultDownloadDir);
          if (!selectedDir) return;
          const separator = selectedDir.includes('\\') ? '\\' : '/';
          const rawLocalPath = `${selectedDir}${selectedDir.endsWith('\\') || selectedDir.endsWith('/') ? '' : separator}${item.name}`;
          localPath = typeof resolveDownloadLocalPath === 'function'
            ? await resolveDownloadLocalPath(rawLocalPath, true, initialPathOptionsJSON)
            : rawLocalPath;
        } else {
          const selectedFilePath = await selectDownloadFilePath?.(remotePath, defaultDownloadDir);
          if (!selectedFilePath) return;
          localPath = typeof resolveDownloadLocalPath === 'function'
            ? await resolveDownloadLocalPath(selectedFilePath, false, initialPathOptionsJSON)
            : selectedFilePath;
        }
      } else {
        if (typeof resolveDownloadPath !== 'function') {
          throw new Error(item.isDirectory ? t('当前环境不支持下载文件夹') : t('下载失败'));
        }
        localPath = await resolveDownloadPath(remotePath, defaultDownloadDir, item.isDirectory, initialPathOptionsJSON);
      }

      if (!localPath) return;

      let optionsJSON = buildDownloadConflictOptionsPayload(conflictSettings, { pathStrategies: {} });
      if (conflictSettings.strategy === DOWNLOAD_CONFLICT_STRATEGY_PROMPT) {
        const resolvedConflict = await resolvePromptDownloadConflict(item, remotePath, localPath, {
          ...conflictSettings,
          pathStrategies: {},
        });
        if (!resolvedConflict) return;
        localPath = resolvedConflict.localPath;
        optionsJSON = resolvedConflict.optionsJSON;
      }

      const compressedEnabled = item.isDirectory && isCompressedTransferEnabled();
      queueId = !item.isDirectory
        ? `download-file-${createdAt}`
        : `${compressedEnabled ? 'download-dir-compressed' : 'download-dir'}-${createdAt}`;
      openTransferQueueIfNeeded();
      updateSessionUploadQueue(sessionGroupId, (current) => [{
        id: queueId,
        name: item.name,
        relativePath: item.name,
        remotePath,
        localPath,
        direction: 'download',
        mode: !item.isDirectory ? 'download-file' : (compressedEnabled ? 'download-compressed' : 'download-directory'),
        status: 'queued',
        progress: 0,
        bytesUploaded: 0,
        bytesTotal: item.isDirectory ? 0 : (item.size || 0),
        phase: compressedEnabled ? 'preparing' : '',
        phaseProgress: 0,
        phaseCurrent: '',
        phaseDetail: compressedEnabled ? t('准备下载') : '',
        error: '',
        sourceTerminalId: sessionId,
        createdAt,
        updatedAt: createdAt,
      }, ...current]);
      const transferTaskRunner = getTransferTaskRunner(getUploadSettings().maxTransferTasks);
      await transferTaskRunner(async () => {
        if (abortedUploadIdsRef.current.has(queueId)) {
          return;
        }
        try {
          patchQueueItem(queueId, { status: 'uploading', updatedAt: Date.now() });
          if (!item.isDirectory) {
            if (typeof downloadFileToLocal !== 'function') {
              throw new Error(t('下载失败'));
            }
            await downloadFileToLocal(sessionId, queueId, remotePath, localPath, optionsJSON);
            patchQueueItem(queueId, {
              status: 'completed',
              progress: 100,
              bytesUploaded: item.size || 0,
              bytesTotal: item.size || 0,
              error: '',
              updatedAt: Date.now(),
            });
            addToast?.(`${t('下载成功')}: ${item.name}`, 'success');
            return;
          }

          if (compressedEnabled) {
            if (typeof downloadDirectoryCompressed !== 'function') {
              throw new Error(t('当前环境不支持下载文件夹'));
            }
            await downloadDirectoryCompressed(sessionId, queueId, remotePath, localPath, optionsJSON);
          } else {
            if (typeof downloadDirectoryToLocal !== 'function') {
              throw new Error(t('当前环境不支持下载文件夹'));
            }
            await downloadDirectoryToLocal(sessionId, queueId, remotePath, localPath, optionsJSON);
          }
          patchQueueItem(queueId, {
            status: 'completed',
            phase: 'completed',
            progress: 100,
            error: '',
            updatedAt: Date.now(),
          });
          addToast?.(`${t('下载成功')}: ${item.name}`, 'success');
        } catch (err) {
          const isAborted = abortedUploadIdsRef.current.has(queueId) || String(err).toLowerCase().includes('context canceled');
          patchQueueItem(queueId, {
            status: 'failed',
            phase: 'failed',
            phaseDetail: isAborted ? t('已终止') : String(err),
            error: isAborted ? t('已终止') : String(err),
            updatedAt: Date.now(),
          });
          if (!isAborted && err) addToast?.(`${t('下载失败')}: ${err}`, 'error');
        }
      });
    } catch (err) {
      const isAborted = abortedUploadIdsRef.current.has(queueId) || String(err).toLowerCase().includes('context canceled');
      patchQueueItem(queueId, {
        status: 'failed',
        phase: 'failed',
        phaseDetail: isAborted ? t('已终止') : String(err),
        error: isAborted ? t('已终止') : String(err),
        updatedAt: Date.now(),
      });
      if (!isAborted && err) addToast?.(`${t('下载失败')}: ${err}`, 'error');
    }
  }, [sessionId, sessionGroupId, currentPath, addToast, t, getDefaultDownloadDir, getDownloadConflictSettings, getTransferTaskRunner, getUploadSettings, resolvePromptDownloadConflict, openTransferQueueIfNeeded]);

  const handleDownloadItems = useCallback(async (items: FileManagerFileItem[], options: Record<string, unknown> = {}) => {
    const normalizedItems = Array.isArray(items)
      ? items.filter((item) => item && typeof item.name === 'string' && item.name.trim())
      : [];
    for (const item of normalizedItems) {
      await handleDownload(item, options);
    }
  }, [handleDownload]);

  return {
    addToast,
    operationProgress, setOperationProgress, operationInProgressRef,
    getUploadSettings, getDefaultDownloadDir, getDownloadConflictSettings,
    buildDownloadConflictMessage, resolvePromptDownloadConflict,
    isUploadAbortable, markUploadAborted, abortUploadItem, removeUploadItems,
    abortUploadItems, abortActiveUploadsForSession,
    refreshDirectoryAfterTransfer,
    pushFileManagerUndoEntry, handleUndoFileManagerAction,
    uploadNativePaths, uploadEntries,
    handleSelectedFiles, handleUpload, handleUploadFolder,
    transferFileManagerItems, handleDownload, handleDownloadItems,
  };
}
