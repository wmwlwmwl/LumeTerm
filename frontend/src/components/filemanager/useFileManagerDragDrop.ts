import { useCallback } from 'react';
import {
  FILE_MANAGER_INTERNAL_DRAG_MIME,
  FILE_MANAGER_LAYOUT_MODE_SIDEBAR_DUAL,
  isCompressedTransferEnabled,
  isHiddenFile,
  traverseEntry,
} from '../../utils/fileManagerHelpers.tsx';
import type { useFileManagerCore } from './useFileManagerCore.ts';
import type { useFileManagerWorkspaceSync } from './useFileManagerWorkspaceSync.ts';
import type { useFileManagerUploadPanel } from './useFileManagerUploadPanel.ts';
import type { useFileManagerTransfers } from './useFileManagerTransfers.ts';
import type { FileManagerDualPaneDragItem, FileManagerDualPaneDragPayload, FileManagerPaneStateLike } from './fileManagerTypes.ts';

// 拖放传输：浏览器拖入上传（含 webkit 条目遍历）、双面板内部拖拽 payload、
// 拖拽跟随提示与复制/移动模式解析
export function useFileManagerDragDrop(deps: ReturnType<typeof useFileManagerCore> & ReturnType<typeof useFileManagerWorkspaceSync> & ReturnType<typeof useFileManagerUploadPanel> & ReturnType<typeof useFileManagerTransfers>) {
  const {
    sessionId, t,
    currentPath, currentPathRef, normalizePath, joinPath,
    selectedPathsRef, items,
    fileManagerLayoutMode, fileManagerDualPaneDragTransferEnabled,
    fileManagerDualPaneDragPromptOnDirectory, fileManagerDualPaneDragInvertModifier,
    activePaneKey,
    internalFileManagerDragPayloadRef,
    setFileManagerPaneDropTarget, setFileManagerDragTip,
    setIsDragOver, dragCounterRef,
    nativeDropHandledUntilRef,
    uploadEntries, transferFileManagerItems,
    fileListRef,
  } = deps;
  const isFileTransferDragEvent = useCallback((event: React.DragEvent) => {
    const types = Array.from(event?.dataTransfer?.types || []);
    if (types.includes('Files')) {
      return true;
    }
    const items = Array.from(event?.dataTransfer?.items || []);
    return items.some((item) => item?.kind === 'file');
  }, []);

  const handleDragEnter = (e: React.DragEvent) => {
    if (!isFileTransferDragEvent(e)) {
      return;
    }
    e.preventDefault();
    if (!isCompressedTransferEnabled()) {
      e.stopPropagation();
    }
    dragCounterRef.current++;
    setIsDragOver(true);
  };

  const handleDragOver = (e: React.DragEvent) => {
    if (!isFileTransferDragEvent(e)) {
      return;
    }
    e.preventDefault();
    if (!isCompressedTransferEnabled()) {
      e.stopPropagation();
    }
  };

  const handleDragLeave = (e: React.DragEvent) => {
    if (!isFileTransferDragEvent(e)) {
      return;
    }
    e.preventDefault();
    if (!isCompressedTransferEnabled()) {
      e.stopPropagation();
    }
    dragCounterRef.current--;
    if (dragCounterRef.current <= 0) {
      dragCounterRef.current = 0;
      setIsDragOver(false);
    }
  };

  const handleDrop = async (e: React.DragEvent) => {
    if (!isFileTransferDragEvent(e)) {
      return;
    }
    e.preventDefault();
    if (!isCompressedTransferEnabled()) {
      e.stopPropagation();
    }
    setIsDragOver(false);
    dragCounterRef.current = 0;

    const droppedItems = Array.from(e.dataTransfer.items || []);
    const droppedFiles = Array.from(e.dataTransfer.files || []);
    if (droppedItems.length === 0 && droppedFiles.length === 0) return;

    const entryMap = new Map<string, { file: File; relativePath: string }>();
    const addEntry = (file: File, relativePath: unknown) => {
      if (!file || isHiddenFile(file.name)) return;
      const normalizedPath = String(relativePath || file.webkitRelativePath || file.name)
        .replace(/^\/+/, '')
        .replace(/\\/g, '/');
      if (!normalizedPath) return;
      const key = `${normalizedPath}|${file.size}|${file.lastModified}`;
      if (!entryMap.has(key)) {
        entryMap.set(key, { file, relativePath: normalizedPath });
      }
    };

    for (const item of droppedItems) {
      const entry = item.webkitGetAsEntry && item.webkitGetAsEntry();
      if (entry) {
        const files = await traverseEntry(entry);
        files.forEach((file) => addEntry(file, (file as File & { _fullPath?: string })._fullPath || file.webkitRelativePath || file.name));
        continue;
      }
      let file: File | null;
      try { file = item.getAsFile(); } catch (_) { file = null; }
      if (file) addEntry(file, file.webkitRelativePath || file.name);
    }

    droppedFiles.forEach((file) => addEntry(file, file.webkitRelativePath || file.name));

    if (isCompressedTransferEnabled()) {
      // 等原生 OnFileDrop 抢先处理；超时再走浏览器 File/Blob 兜底
      await new Promise((resolve) => setTimeout(resolve, 3000));
      if (Date.now() < nativeDropHandledUntilRef.current) {
        return;
      }
    }
    await uploadEntries(Array.from(entryMap.values()));
  };

  const buildFileManagerDragPayload = useCallback((itemPath: unknown) => {
    if (!itemPath) {
      return null;
    }
    const sourceDirPath = normalizePath(currentPathRef.current || currentPath) || '/';
    const currentSelectedPaths = Array.isArray(selectedPathsRef.current) ? selectedPathsRef.current : [];
    const dragPaths: string[] = currentSelectedPaths.length > 1 && currentSelectedPaths.includes(String(itemPath || ''))
      ? currentSelectedPaths
      : [String(itemPath || '')];
    const payloadItems = dragPaths.map((path) => {
      const matchedItem = items.find((entry) => joinPath(sourceDirPath, entry.name) === path);
      return {
        path,
        isDirectory: matchedItem?.isDirectory === true,
      };
    });
    return {
      sessionId,
      sourceDir: sourceDirPath,
      paths: dragPaths,
      items: payloadItems,
    };
  }, [currentPath, items, normalizePath, sessionId]);

  const parseFileManagerDragPayload = useCallback((event: React.DragEvent) => {
    const rawPayload = event?.dataTransfer?.getData(FILE_MANAGER_INTERNAL_DRAG_MIME);
    if (!rawPayload) {
      return null;
    }
    try {
      const parsedPayload = JSON.parse(rawPayload);
      const sourceDir = normalizePath(parsedPayload?.sourceDir) || '/';
      const rawPaths = parsedPayload?.paths
      const paths = Array.isArray(rawPaths)
        ? rawPaths.map((path: unknown) => String(path || '').trim()).filter(Boolean)
        : [];
      if (!paths.length) {
        return null;
      }
      const rawItems = parsedPayload?.items
      const payloadItems = Array.isArray(rawItems)
        ? rawItems.map((item: FileManagerDualPaneDragItem, index) => ({
          path: paths[index] || String(item?.path || '').trim(),
          isDirectory: item?.isDirectory === true,
        }))
        : paths.map((path) => ({ path, isDirectory: false }));
      return {
        sessionId: String(parsedPayload?.sessionId || '').trim(),
        sourceDir,
        paths,
        items: payloadItems,
      };
    } catch {
      return null;
    }
  }, [normalizePath]);

  const confirmDualPaneDirectoryDrag = useCallback(async (mode: string, paneState: FileManagerPaneStateLike, payload: FileManagerDualPaneDragPayload) => {
    if (!fileManagerDualPaneDragPromptOnDirectory) {
      return true;
    }
    if (!Array.isArray(payload?.items) || !payload.items.some((item: FileManagerDualPaneDragItem) => item?.isDirectory === true)) {
      return true;
    }
    const actionLabel = mode === 'cut' ? t('移动') : t('复制');
    const normalizedTargetDirPath = normalizePath(paneState?.path) || '/';
    const normalizedPaths = Array.isArray(payload?.paths)
      ? payload.paths.map((path: unknown) => normalizePath(path)).filter(Boolean)
      : [];
    const primarySourcePath = normalizedPaths[0] || normalizePath(payload?.sourceDir) || '/';
    const sourceName = primarySourcePath.split('/').filter(Boolean).pop() || '';
    const targetPath = sourceName
      ? joinPath(normalizedTargetDirPath, sourceName)
      : normalizedTargetDirPath;
    const message = `${t('确认{action}', { action: actionLabel })}\n${primarySourcePath}\n${t('到')}\n${targetPath}`;
    const confirm = await window.lumeDialog?.confirm?.(message);
    return confirm !== false;
  }, [fileManagerDualPaneDragPromptOnDirectory, joinPath, normalizePath, t]);

  const resolveDualPaneDragTransferMode = useCallback((ctrlKey = false) => (
    fileManagerDualPaneDragInvertModifier
      ? (ctrlKey ? 'copy' : 'cut')
      : (ctrlKey ? 'cut' : 'copy')
  ), [fileManagerDualPaneDragInvertModifier]);

  const hideFileManagerDragTip = useCallback(() => {
    setFileManagerDragTip(null);
  }, []);

  const updateFileManagerDragTip = useCallback((clientX: number, clientY: number, targetPath: unknown, ctrlKey = false) => {
    const mode = resolveDualPaneDragTransferMode(ctrlKey);
    const actionLabel = mode === 'cut' ? t('移动') : t('复制');
    const normalizedTargetPath = normalizePath(targetPath) || '/';
    setFileManagerDragTip({
      x: clientX + 16,
      y: clientY + 20,
      text: t('{action}至:{path}', { action: actionLabel, path: normalizedTargetPath }),
    });
  }, [normalizePath, resolveDualPaneDragTransferMode, t]);

  const handleDualPaneTransferDragOver = useCallback((event: React.DragEvent, paneState: FileManagerPaneStateLike) => {
    if (fileManagerLayoutMode !== FILE_MANAGER_LAYOUT_MODE_SIDEBAR_DUAL || !fileManagerDualPaneDragTransferEnabled) {
      return;
    }
    if (paneState.key === activePaneKey) {
      return;
    }
    const types = Array.from(event?.dataTransfer?.types || []);
    if (!types.includes(FILE_MANAGER_INTERNAL_DRAG_MIME)) {
      return;
    }
    const payload = internalFileManagerDragPayloadRef.current;
    if (!payload || payload.sessionId !== sessionId) {
      return;
    }
    const mode = resolveDualPaneDragTransferMode(event.ctrlKey);
    event.preventDefault();
    event.stopPropagation();
    event.dataTransfer.dropEffect = mode === 'cut' ? 'move' : 'copy';
    updateFileManagerDragTip(event.clientX, event.clientY, paneState.path, event.ctrlKey);
    setFileManagerPaneDropTarget(String(paneState.key ?? ''));
  }, [activePaneKey, fileManagerDualPaneDragTransferEnabled, fileManagerLayoutMode, resolveDualPaneDragTransferMode, sessionId, updateFileManagerDragTip]);

  const handleDualPaneTransferDrop = useCallback(async (event: React.DragEvent, paneState: FileManagerPaneStateLike) => {
    setFileManagerPaneDropTarget('');
    hideFileManagerDragTip();
    if (fileManagerLayoutMode !== FILE_MANAGER_LAYOUT_MODE_SIDEBAR_DUAL || !fileManagerDualPaneDragTransferEnabled) {
      return;
    }
    const payload = parseFileManagerDragPayload(event) || internalFileManagerDragPayloadRef.current;
    internalFileManagerDragPayloadRef.current = null;
    if (!payload || payload.sessionId !== sessionId || paneState.key === activePaneKey) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    const mode = resolveDualPaneDragTransferMode(event.ctrlKey);
    const confirmed = await confirmDualPaneDirectoryDrag(mode, paneState, payload);
    if (!confirmed) {
      return;
    }
    await transferFileManagerItems({
      paths: payload.paths,
      mode,
      sourceDir: payload.sourceDir,
      targetDirPath: paneState.path,
    });
    fileListRef.current?.focus();
  }, [activePaneKey, confirmDualPaneDirectoryDrag, fileManagerDualPaneDragTransferEnabled, fileManagerLayoutMode, hideFileManagerDragTip, parseFileManagerDragPayload, resolveDualPaneDragTransferMode, sessionId, transferFileManagerItems]);

  return {
    isFileTransferDragEvent,
    handleDragEnter, handleDragOver, handleDragLeave, handleDrop,
    buildFileManagerDragPayload, parseFileManagerDragPayload,
    confirmDualPaneDirectoryDrag, resolveDualPaneDragTransferMode,
    hideFileManagerDragTip, updateFileManagerDragTip,
    handleDualPaneTransferDragOver, handleDualPaneTransferDrop,
  };
}
