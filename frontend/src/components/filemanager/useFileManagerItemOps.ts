import { useCallback, useRef } from 'react';
import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';
import {
  getParentPath,
  isMissingUnzipError,
  normalizeChmodMode,
  normalizeIdentityId,
  resolveIdentityCompareKey,
  resolveIdentityInputSpec,
} from '../../utils/fileManagerHelpers.tsx';
import { setSessionCachedFileManagerPathItems } from '../../utils/fileWorkbench.ts';
import type { useFileManagerCore } from './useFileManagerCore.ts';
import type { useFileManagerWorkspaceSync } from './useFileManagerWorkspaceSync.ts';
import type { useFileManagerPaneView } from './useFileManagerPaneView.ts';
import type { useFileManagerClipboard } from './useFileManagerClipboard.ts';
import { warnDev } from '../../utils/devLog';
import type { useFileManagerEditorState } from './useFileManagerEditorState.ts';
import type { useFileManagerDirectoryLoader } from './useFileManagerDirectoryLoader.ts';
import type { useFileManagerTransfers } from './useFileManagerTransfers.ts';
import type { useFileManagerLocator } from './useFileManagerLocator.ts';
import type { FileManagerChmodTarget, FileManagerFileItem, FileManagerProps } from './fileManagerTypes.ts';

type FileManagerChmodBatchItem = {
  item: FileManagerFileItem
  path: string
}

// 条目操作：复制/剪切路径、删除（单项/批量/标签目录）、文件列表键盘快捷键、
// 粘贴、新建文件/文件夹、压缩/解压、重命名、右键菜单与 chmod 保存
export function useFileManagerItemOps(deps: ReturnType<typeof useFileManagerCore> & ReturnType<typeof useFileManagerWorkspaceSync> & ReturnType<typeof useFileManagerPaneView> & ReturnType<typeof useFileManagerClipboard> & ReturnType<typeof useFileManagerEditorState> & ReturnType<typeof useFileManagerDirectoryLoader> & ReturnType<typeof useFileManagerTransfers> & ReturnType<typeof useFileManagerLocator> & {
  addToast?: FileManagerProps['addToast']
}) {
  const {
    sessionId, addToast, t,
    currentPath, currentPathRef, joinPath, normalizePath,
    updateClipboard,
    selectedPaths, lastClickedPathRef, setSelectedPaths,
    sortedItems, clipboard,
    transferFileManagerItems, refreshDirectoryAfterTransfer,
    pushFileManagerUndoEntry, handleUndoFileManagerAction,
    operationInProgressRef, setOperationProgress,
    updateItemsPreservingView, captureFileListViewAnchor, clearActiveRowEffect,
    paneEffectStateRef, queueRowEffectForMatchingPanes, confirmCreatedItem, isDeletedPlaceholderItem,
    renamingItem, renamingInputMountedRef, setRenamingItem,
    chmodTarget, setChmodTarget, contextMenu, setContextMenu,
    fileManagerSmartUncompressConflictStrategy,
    fileListTypeaheadQuery,
    clearFileListTypeahead, handleFileListTypeaheadKey,
    loadDir,
    fileListRef,
  } = deps;

  // Download file via Wails native file dialog
  const handleCopyPath = (item: FileManagerFileItem, basePath = currentPath, selectedItemPaths: unknown[] = []) => {
    const normalizedSelectedPaths = Array.isArray(selectedItemPaths)
      ? selectedItemPaths.map((path) => String(path || '').trim()).filter(Boolean)
      : [];
    let paths = normalizedSelectedPaths;
    if (paths.length === 0) {
      let fullPath = joinPath(basePath, item.name);
      if (item.isDirectory && !fullPath.endsWith('/')) fullPath += '/';
      paths = [fullPath];
    }
    const clipboardText = paths.join('\n');
    navigator.clipboard?.writeText(clipboardText).then(() => {
      addToast?.(`${t('已复制')}: ${paths.length > 1 ? `${paths.length}${t('项')}` : clipboardText}`, 'success');
    }).catch(() => {
      addToast?.(t('复制失败'), 'error');
    });
  };

  const handleClipboardCopy = useCallback((paths: unknown, sourceDir = currentPathRef.current || currentPath) => {
    const normalizedPaths = Array.isArray(paths)
      ? paths.map((path) => String(path || '').trim()).filter(Boolean)
      : [];
    if (normalizedPaths.length === 0) {
      return;
    }
    updateClipboard({
      paths: normalizedPaths,
      mode: 'copy',
      srcDir: normalizePath(sourceDir) || '/',
    });
    addToast?.(t('已复制'), 'info');
  }, [addToast, currentPath, normalizePath, t]);

  const handleClipboardCut = useCallback((paths: unknown, sourceDir = currentPathRef.current || currentPath) => {
    const normalizedPaths = Array.isArray(paths)
      ? paths.map((path) => String(path || '').trim()).filter(Boolean)
      : [];
    if (normalizedPaths.length === 0) {
      return;
    }
    updateClipboard({
      paths: normalizedPaths,
      mode: 'cut',
      srcDir: normalizePath(sourceDir) || '/',
    });
    addToast?.(t('已剪切'), 'info');
  }, [addToast, currentPath, normalizePath, t]);

  // Delete
  const handleDelete = async (item: FileManagerFileItem) => {
    if (operationInProgressRef.current) return;
    operationInProgressRef.current = true;
    const remotePath = joinPath(currentPath, item.name);
    const needConfirm = localStorage.getItem('skipFileDeleteConfirm') !== 'true';
    if (needConfirm) {
      const ok = await window.luminDialog?.confirm(`${t('确定删除')}${item.name}${t('？此操作不可撤销')}`);
      fileListRef.current?.focus();
      if (!ok) { operationInProgressRef.current = false; return; }
    }
    try {
      setOperationProgress({ message: `${t('正在删除')} ${item.name}` });
      await AppGo.DeleteItemShell(sessionId, remotePath);
      setSelectedPaths(prev => prev.filter(p => p !== remotePath));
      if (lastClickedPathRef.current === remotePath) {
        lastClickedPathRef.current = null;
      }
      updateItemsPreservingView((prev) => prev.filter((entry) => entry.name !== item.name));
    } catch (err) {
      addToast?.(`${t('删除失败')}: ${err}`, 'error');
    } finally {
      setOperationProgress(null);
      operationInProgressRef.current = false;
      fileListRef.current?.focus();
    }
  };

  // Delete via rm -rf
  const handleDeleteShell = async (item: FileManagerFileItem) => {
    if (operationInProgressRef.current) return;
    operationInProgressRef.current = true;
    const remotePath = joinPath(currentPath, item.name);
    const needConfirm = localStorage.getItem('skipFileDeleteConfirm') !== 'true';
    if (needConfirm) {
      const ok = await window.luminDialog?.confirm(`${t('确定删除')}${item.name}${t('？(rm -rf) 此操作不可撤销')}`);
      fileListRef.current?.focus();
      if (!ok) { operationInProgressRef.current = false; return; }
    }
    try {
      setOperationProgress({ message: `${t('正在删除')} ${item.name}` });
      await AppGo.DeleteItemShell(sessionId, remotePath);
      setSelectedPaths(prev => prev.filter(p => p !== remotePath));
      if (lastClickedPathRef.current === remotePath) {
        lastClickedPathRef.current = null;
      }
      updateItemsPreservingView((prev) => prev.filter((entry) => entry.name !== item.name));
    } catch (err) {
      addToast?.(`${t('删除失败')}: ${err}`, 'error');
    } finally {
      setOperationProgress(null);
      operationInProgressRef.current = false;
      fileListRef.current?.focus();
    }
  };

  // Delete multiple selected items
  const handleDeleteItems = async () => {
    if (operationInProgressRef.current) return;
    if (selectedPaths.length === 0) return;
    operationInProgressRef.current = true;
    const needConfirm = localStorage.getItem('skipFileDeleteConfirm') !== 'true';
    if (needConfirm) {
      const ok = await window.luminDialog?.confirm(`${t('确定删除所选')} (${selectedPaths.length}${t('项')})${t('？此操作不可撤销')}`);
      fileListRef.current?.focus();
      if (!ok) { operationInProgressRef.current = false; return; }
    }
    const total = selectedPaths.length;
    let removedPaths: string[] = [];
    setOperationProgress({ message: t('正在删除中...'), current: 0, total });
    try {
      await AppGo.BatchDeleteItemShell(sessionId, selectedPaths);
      removedPaths = [...selectedPaths];
    } catch (err) {
      warnDev('batch delete failed:', err);
      addToast?.(`${t('删除失败')}: ${err instanceof Error ? err.message : String(err || '')}`, 'error');
    } finally {
      setOperationProgress(null);
      operationInProgressRef.current = false;
    }
    if (removedPaths.length === 0) {
      fileListRef.current?.focus();
      return;
    }
    addToast?.(`${t('已删除')} ${removedPaths.length} ${t('项')}`, 'success');
    const removedSet = new Set(removedPaths);
    if (lastClickedPathRef.current && removedSet.has(lastClickedPathRef.current)) {
      lastClickedPathRef.current = null;
    }
    setSelectedPaths([]);
    updateItemsPreservingView((prev) => prev.filter((entry) => !removedSet.has(joinPath(currentPath, entry.name))));
    fileListRef.current?.focus();
  };

  // Keyboard shortcuts for file list
  const handleFileListKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (operationInProgressRef.current) return;
    // 重命名残留兜底：若上一次重命名的 input 已被虚拟化滚出视口而卸载（React 不会
    // 触发 onBlur，renamingItem 会残留），这里先把它提交掉，再继续本次按键。
    // 仅当 input 仍挂在 DOM 时，才认为用户正在编辑、需挡住快捷键。
    if (renamingItem) {
      const el = renamingInputMountedRef.current;
      const stillInDom = el && document.body.contains(el);
      if (stillInDom) return;
      confirmRename(el ? el.value : '');
    }
    const eventTarget = e.target;
    const isEditableTarget = eventTarget instanceof HTMLElement
      && (
        eventTarget.tagName === 'INPUT'
        || eventTarget.tagName === 'TEXTAREA'
        || eventTarget.isContentEditable
      );
    if (isEditableTarget) return;
    if (fileListRef.current && document.activeElement !== fileListRef.current) return;
    if (e.target !== e.currentTarget) return;
    if (e.defaultPrevented) return;
    const isCtrl = e.ctrlKey || e.metaKey;
    if (isCtrl && !e.shiftKey && e.key.toLowerCase() === 'z') {
      e.preventDefault();
      void handleUndoFileManagerAction();
      return;
    }
    if (e.key === 'Delete' || e.key === 'Del') {
      e.preventDefault();
      void handleDeleteItems();
      return;
    }
    if (e.key === 'Backspace') {
      if (currentPath === '/') return;
      e.preventDefault();
      const parent = currentPath.substring(0, currentPath.lastIndexOf('/')) || '/';
      void loadDir(parent, {
        preserveView: false,
        trackDiff: false,
        showLoading: true,
      });
      return;
    }
    if (e.key === 'F2') {
      if (selectedPaths.length !== 1) return;
      e.preventDefault();
      const targetPath = selectedPaths[0];
      const targetItem = sortedItems.find((item) => (
        !isDeletedPlaceholderItem(item) && joinPath(currentPath, item.name) === targetPath
      ));
      if (!targetItem) return;
      startRename(targetItem);
      return;
    }
    if (isCtrl && e.key === 'a') {
      e.preventDefault();
      setSelectedPaths(sortedItems.filter((item) => !isDeletedPlaceholderItem(item)).map(i => joinPath(currentPath, i.name)));
      return;
    }
    if (isCtrl && e.key === 'c') {
      e.preventDefault();
      if (selectedPaths.length === 0) return;
      handleClipboardCopy(selectedPaths, currentPath);
      return;
    }
    if (isCtrl && e.key === 'x') {
      e.preventDefault();
      if (selectedPaths.length === 0) return;
      handleClipboardCut(selectedPaths, currentPath);
      return;
    }
    if (isCtrl && e.key === 'v') {
      e.preventDefault();
      if (!clipboard) return;
      void handlePaste();
      return;
    }
    if (fileListTypeaheadQuery && e.key === 'Escape') {
      e.preventDefault();
      clearFileListTypeahead();
      return;
    }
    const isPlainEnglishLetter = !isCtrl && !e.altKey && /^[a-zA-Z]$/.test(String(e.key || ''));
    if (isPlainEnglishLetter) {
      e.preventDefault();
      handleFileListTypeaheadKey(e.key);
    }
  };

  const handlePaste = useCallback(async (targetDirPath = currentPathRef.current || currentPath) => {
    if (!clipboard || clipboard.paths.length === 0) return false;
    return transferFileManagerItems({
      paths: clipboard.paths,
      mode: clipboard.mode,
      sourceDir: clipboard.srcDir,
      targetDirPath,
      clearClipboardOnSuccess: true,
    });
  }, [clipboard, currentPath, transferFileManagerItems]);
  const handleMkdir = async (targetDirPath = currentPath) => {
    const normalizedTargetDirPath = normalizePath(typeof targetDirPath === 'string' ? targetDirPath : (currentPathRef.current || currentPath)) || '/';
    const promptResult = await window.luminDialog?.prompt(t('新文件夹名称:'));
    const name = typeof promptResult === 'string' ? promptResult : '';
    if (!name) return;
    const remotePath = joinPath(normalizedTargetDirPath, name);
    try {
      await AppGo.Mkdir(sessionId, remotePath);
      const { matchedItem, listedItems } = await confirmCreatedItem(normalizedTargetDirPath, name, true);
      const currentVisiblePath = normalizePath(currentPathRef.current) || '/';
      if (normalizedTargetDirPath === currentVisiblePath) {
        queueRowEffectForMatchingPanes(normalizedTargetDirPath, remotePath, remotePath, 'added');
        updateItemsPreservingView((listedItems || []) as FileManagerFileItem[]);
      } else {
        setSessionCachedFileManagerPathItems(sessionId, normalizedTargetDirPath, listedItems);
      }
      pushFileManagerUndoEntry({
        undo: async () => {
          await AppGo.DeleteItemShell(sessionId, remotePath);
          await refreshDirectoryAfterTransfer(normalizedTargetDirPath);
        },
      });
      addToast?.(
        normalizedTargetDirPath === currentVisiblePath
          ? `${t('文件夹创建成功')}: ${matchedItem.name}`
          : `${t('文件夹创建成功')}: ${matchedItem.name} [${normalizedTargetDirPath}]`,
        'success',
      );
    } catch (err) {
      addToast?.(`${t('创建失败')}: ${err}`, 'error');
    }
  };

  const handleNewFile = async (targetDirPath = currentPath) => {
    const normalizedTargetDirPath = normalizePath(typeof targetDirPath === 'string' ? targetDirPath : (currentPathRef.current || currentPath)) || '/';
    const promptResult = await window.luminDialog?.prompt(t('新文件名称:'));
    const name = typeof promptResult === 'string' ? promptResult : '';
    if (!name) return;
    const remotePath = joinPath(normalizedTargetDirPath, name);
    try {
      await AppGo.WriteFile(sessionId, remotePath, '');
      const { matchedItem, listedItems } = await confirmCreatedItem(normalizedTargetDirPath, name, false);
      const currentVisiblePath = normalizePath(currentPathRef.current) || '/';
      if (normalizedTargetDirPath === currentVisiblePath) {
        queueRowEffectForMatchingPanes(normalizedTargetDirPath, remotePath, remotePath, 'added');
        updateItemsPreservingView((listedItems || []) as FileManagerFileItem[]);
      } else {
        setSessionCachedFileManagerPathItems(sessionId, normalizedTargetDirPath, listedItems);
      }
      pushFileManagerUndoEntry({
        undo: async () => {
          await AppGo.DeleteItemShell(sessionId, remotePath);
          await refreshDirectoryAfterTransfer(normalizedTargetDirPath);
        },
      });
      addToast?.(
        normalizedTargetDirPath === currentVisiblePath
          ? `${t('文件创建成功')}: ${matchedItem.name}`
          : `${t('文件创建成功')}: ${matchedItem.name} [${normalizedTargetDirPath}]`,
        'success',
      );
    } catch (err) {
      addToast?.(`${t('创建失败')}: ${err}`, 'error');
    }
  };


  // Compress
  const handleCompress = async (item: FileManagerFileItem, options: Record<string, unknown> = {}) => {
    const basePath = typeof options.basePath === 'string' ? options.basePath : currentPath;
    const remotePath = joinPath(basePath, item.name);
    try {
      addToast?.(`${t('正在压缩')} ${item.name}...`, 'info');
      await AppGo.CompressItem(sessionId, remotePath);
      addToast?.(t('压缩成功'), 'success');
      if (basePath === currentPathRef.current) {
        await loadDir(currentPathRef.current, { preserveView: true, showLoading: false });
      }
    } catch (err) {
      addToast?.(`${t('压缩失败')}: ${err}`, 'error');
    }
  };

  const handleCompressItems = async (items: FileManagerFileItem[], options: Record<string, unknown> = {}) => {
    const normalizedItems = Array.isArray(items)
      ? items.filter((item) => item && typeof item.name === 'string' && item.name.trim())
      : [];
    for (const item of normalizedItems) {
      await handleCompress(item, options);
    }
  };

  // Uncompress
  const handleUncompress = async (item: FileManagerFileItem, options: Record<string, unknown> = {}) => {
    const basePath = typeof options.basePath === 'string' ? options.basePath : currentPath;
    const remotePath = joinPath(basePath, item.name);
    const isZip = String(item.name || '').toLowerCase().endsWith('.zip');
    const autoInstallAttempted = typeof options === 'object' && options !== null && options.__autoInstallUnzipAttempted === true;
    try {
      const previewSmartUncompressItem = window?.go?.wailsapp?.App?.PreviewSmartUncompressItem;
      const uncompressItemWithStrategy = window?.go?.wailsapp?.App?.UncompressItemWithStrategy;
      let requestedStrategy = fileManagerSmartUncompressConflictStrategy;
      if (requestedStrategy === 'prompt' && typeof previewSmartUncompressItem === 'function') {
        const preview = await previewSmartUncompressItem(sessionId, remotePath);
        if (preview?.mode === 'folder' && preview?.targetExists === true) {
          const targetName = String(preview?.targetName || item.name || '').trim() || t('文件夹');
          const targetKind = preview?.targetKind === 'file' ? t('文件') : t('文件夹');
          const choice = await window.luminDialog?.choice?.(
            `${t('准备解压到“{name}”', { name: targetName })}\n${t('但当前目录里已经有同名{kind}', { kind: targetKind })}\n\n${t('请选择这次怎么处理')}`,
            t('智能解压遇到同名'),
            [
              { label: t('覆盖'), value: 'overwrite', primary: true },
              { label: t('自动重命名'), value: 'auto_rename' },
              { label: t('取消'), value: 'cancel', secondary: true },
            ]
          );
          const choiceObj = choice && typeof choice === 'object' ? (choice as { value?: unknown }) : null;
          const selectedValue = choiceObj ? choiceObj.value : choice;
          if (!selectedValue || selectedValue === 'cancel') {
            return;
          }
          requestedStrategy = selectedValue === 'overwrite' ? 'overwrite' : 'auto_rename';
        }
      }
      addToast?.(`${t('正在解压')} ${item.name}...`, 'info');
      if (typeof uncompressItemWithStrategy === 'function') {
        await uncompressItemWithStrategy(sessionId, remotePath, requestedStrategy);
      } else {
        await AppGo.UncompressItem(sessionId, remotePath);
      }
      addToast?.(t('解压成功'), 'success');
      if (basePath === currentPathRef.current) {
        await loadDir(currentPathRef.current, { preserveView: true, showLoading: false });
      }
    } catch (err) {
      if (isZip && !autoInstallAttempted && isMissingUnzipError(err)) {
        const installUnzip = window?.go?.wailsapp?.App?.InstallUnzip;
        if (typeof installUnzip === 'function') {
          try {
            await installUnzip(sessionId);
            const nextOptions = typeof options === 'string'
              ? { basePath: options, __autoInstallUnzipAttempted: true }
              : { ...options, __autoInstallUnzipAttempted: true };
            await handleUncompress(item, nextOptions);
            return;
          } catch (installErr) {
            addToast?.(`${t('解压失败')}: ${installErr}`, 'error');
            return;
          }
        }
      }
      addToast?.(`${t('解压失败')}: ${err}`, 'error');
    }
  };

  // Rename
  const startRename = (item: FileManagerFileItem) => {
    // 切换到另一个条目的重命名时，先提交当前编辑中的值（input 卸载不会触发 onBlur）
    if (renamingItem && renamingItem.name !== item.name) {
      const el = renamingInputMountedRef.current;
      void confirmRename(el ? el.value : '');
    }
    setRenamingItem(item);
  };

  const renameCommittingRef = useRef<FileManagerFileItem | null>(null);

  const confirmRename = async (nextValue: unknown, refocus = false) => {
    const targetItem = renamingItem;
    // 同一 item 的提交正在进行中时忽略重复触发（残留清理可能被连续点击多次调用）
    if (!targetItem || renameCommittingRef.current === targetItem) return;
    const nextName = String(nextValue ?? '').trim();
    if (!nextName || nextName === targetItem.name) {
      // 函数式更新 + 身份比较：避免异步窗口内清掉刚启动的新重命名
      setRenamingItem((current: FileManagerFileItem | null) => (current === targetItem ? null : current));
      if (refocus) fileListRef.current?.focus();
      return;
    }
    const oldPath = joinPath(currentPath, targetItem.name);
    const newPath = joinPath(currentPath, nextName);
    renameCommittingRef.current = targetItem;
    try {
      await AppGo.RenameItem(sessionId, oldPath, newPath);
      pushFileManagerUndoEntry({
        undo: async () => {
          await AppGo.RenameItem(sessionId, newPath, oldPath);
          await refreshDirectoryAfterTransfer(currentPathRef.current || currentPath);
        },
      });
      addToast?.(t('重命名成功'), 'success');
      const anchor = captureFileListViewAnchor();
      if (anchor?.key === oldPath) {
        anchor.key = newPath;
      }
      setSelectedPaths((prev) => prev.map((path) => (path === oldPath ? newPath : path)));
      if (lastClickedPathRef.current === oldPath) {
        lastClickedPathRef.current = newPath;
      }
      Object.values(paneEffectStateRef.current).forEach((paneEffectState) => {
        paneEffectState.pendingVisualEffects.delete(oldPath);
      });
      clearActiveRowEffect(oldPath);
      queueRowEffectForMatchingPanes(currentPathRef.current || currentPath, newPath, newPath, 'changed');
      updateItemsPreservingView((prev) => prev.map((entry) => (
        entry.name === targetItem.name
          ? { ...entry, name: nextName, modifyTime: Date.now() }
          : entry
      )), anchor);
    } catch (err) {
      addToast?.(`${t('重命名失败')}: ${err}`, 'error');
    } finally {
      if (renameCommittingRef.current === targetItem) renameCommittingRef.current = null;
      setRenamingItem((current: FileManagerFileItem | null) => (current === targetItem ? null : current));
      if (refocus) fileListRef.current?.focus();
    }
  };

  const closeContextMenu = () => setContextMenu(null);
  const contextMenuTargetPath = contextMenu?.mode === 'item' && contextMenu?.item
    ? joinPath(contextMenu.itemBasePath || currentPath, contextMenu.item.name)
    : '';

  const openChmodTarget = useCallback(async (itemPath: unknown, item: FileManagerFileItem, batchItems: FileManagerChmodBatchItem[] = []) => {
    let rememberedMode = '';
    let rememberedIncludeSubdirectories = false;
    let rememberedAutoApplyLastSettings = false;
    try {
      const settings = await AppGo.GetChmodDialogSettings();
      rememberedMode = normalizeChmodMode(settings?.mode);
      rememberedIncludeSubdirectories = settings?.includeSubdirectories === true;
      rememberedAutoApplyLastSettings = settings?.autoApplyLastSettings === true;
    } catch (_) {}

    const primaryPath = String(itemPath ?? '');
    const getPathOwnership = window?.go?.wailsapp?.App?.GetPathOwnership;
    const resolveItemOwnership = async (targetPath: string, targetItem: FileManagerFileItem) => {
      let resolvedTargetItem = targetItem;
      const needsMetadata = !targetItem?.permission
        || !targetItem?.mode
        || !targetItem?.uid
        || targetItem?.uid === '-'
        || !targetItem?.gid
        || targetItem?.gid === '-';
      if (needsMetadata && typeof getPathOwnership === 'function') {
        try {
          const ownership = await getPathOwnership(sessionId, targetPath);
          if (ownership && typeof ownership === 'object') {
            const ownershipData = ownership as unknown as Record<string, unknown>;
            resolvedTargetItem = {
              ...targetItem,
              permission: typeof ownershipData.permission === 'string' ? ownershipData.permission : String(targetItem?.permission || ''),
              mode: typeof ownershipData.mode === 'string' ? ownershipData.mode : String(targetItem?.mode || ''),
              uid: typeof ownershipData.uid === 'string' ? ownershipData.uid : String(targetItem?.uid || '-'),
              gid: typeof ownershipData.gid === 'string' ? ownershipData.gid : String(targetItem?.gid || '-'),
            };
          }
        } catch (error) {
          warnDev('GetPathOwnership failed:', error);
        }
      }
      return resolvedTargetItem;
    };

    const resolvedTargets: FileManagerChmodBatchItem[] = [{
      item: await resolveItemOwnership(primaryPath, item),
      path: primaryPath,
    }];
    const seenPaths = new Set([primaryPath]);
    for (const target of Array.isArray(batchItems) ? batchItems : []) {
      const targetPath = String(target?.path || '');
      if (!targetPath || seenPaths.has(targetPath) || !target?.item) {
        continue;
      }
      seenPaths.add(targetPath);
      resolvedTargets.push({
        item: await resolveItemOwnership(targetPath, target.item),
        path: targetPath,
      });
    }

    const resolvedItem = resolvedTargets[0].item;
    const actualMode = normalizeChmodMode(resolvedItem?.mode);
    setChmodTarget({
      item: resolvedItem,
      path: primaryPath,
      mode: actualMode || '',
      rememberedMode,
      autoApplyLastSettings: rememberedAutoApplyLastSettings,
      ownerCandidates: [],
      groupCandidates: [],
      includeSubdirectories: rememberedIncludeSubdirectories,
      showIncludeSubdirectories: resolvedTargets.some((target) => target.item.isDirectory),
      items: resolvedTargets,
    });
    const listOwnershipCandidates = window?.go?.wailsapp?.App?.ListOwnershipCandidates;
    if (typeof listOwnershipCandidates !== 'function') {
      return;
    }
    try {
      const nextCandidates = await listOwnershipCandidates(sessionId);
      setChmodTarget((current: FileManagerChmodTarget | null) => {
        if (!current || current.path !== primaryPath) {
          return current;
        }
        return {
          ...current,
          ownerCandidates: Array.isArray(nextCandidates?.users) ? nextCandidates.users : [],
          groupCandidates: Array.isArray(nextCandidates?.groups) ? nextCandidates.groups : [],
        };
      });
    } catch (error) {
      warnDev('ListOwnershipCandidates failed:', error);
    }
  }, [sessionId]);

  // Chmod
  const handleChmod = async (item: FileManagerFileItem, basePath = currentPath) => {
    const itemPath = joinPath(basePath, item.name);
    await openChmodTarget(itemPath, item);
  };

  const handleChmodItems = async (items: FileManagerFileItem[], basePath = currentPath) => {
    const normalizedItems = Array.isArray(items)
      ? items.filter((item) => item && typeof item.name === 'string' && item.name.trim())
      : [];
    if (normalizedItems.length === 0) {
      return;
    }
    const targets = normalizedItems.map((item) => ({
      item,
      path: joinPath(basePath, item.name),
    }));
    await openChmodTarget(targets[0].path, targets[0].item, targets.slice(1));
  };

  const handleChmodSave = async (modeStr: unknown, includeSubdirectories: unknown, ownerValue: unknown, groupValue: unknown) => {
    if (!chmodTarget) return;
    const storedTargets = Array.isArray(chmodTarget.items)
      ? chmodTarget.items as FileManagerChmodBatchItem[]
      : [];
    const targetItems = storedTargets.length > 0
      ? storedTargets
      : (chmodTarget.item ? [{ item: chmodTarget.item, path: chmodTarget.path }] : []);
    if (targetItems.length === 0) {
      return;
    }

    const normalizedMode = normalizeChmodMode(modeStr) || '644';
    const primaryItem = targetItems[0].item;
    const currentMode = normalizeChmodMode(primaryItem.mode) || normalizeChmodMode(chmodTarget.mode) || normalizedMode;
    const modeChanged = normalizedMode !== currentMode;
    const isBatch = targetItems.length > 1;
    const rememberedIncludeSubdirectories = Boolean(includeSubdirectories);
    const ownerCandidates = Array.isArray(chmodTarget.ownerCandidates) ? chmodTarget.ownerCandidates : [];
    const groupCandidates = Array.isArray(chmodTarget.groupCandidates) ? chmodTarget.groupCandidates : [];
    const currentOwnerId = normalizeIdentityId(primaryItem.uid);
    const currentGroupId = normalizeIdentityId(primaryItem.gid);
    const ownerChanged = resolveIdentityCompareKey(ownerValue, ownerCandidates, currentOwnerId) !== (currentOwnerId ? `id:${currentOwnerId}` : '');
    const groupChanged = resolveIdentityCompareKey(groupValue, groupCandidates, currentGroupId) !== (currentGroupId ? `id:${currentGroupId}` : '');
    const ownerSpec = ownerChanged ? resolveIdentityInputSpec(ownerValue, ownerCandidates, currentOwnerId) : '';
    const groupSpec = groupChanged ? resolveIdentityInputSpec(groupValue, groupCandidates, currentGroupId) : '';

    const requireChownFile = () => {
      const chownFile = window?.go?.wailsapp?.App?.ChownFile;
      if (typeof chownFile !== 'function') {
        throw new Error(t('应用不可用'));
      }
      return chownFile;
    };

    if (!modeChanged && !ownerChanged && !groupChanged && !isBatch) {
      setChmodTarget(null);
      return;
    }

    try {
      try {
        await AppGo.SaveChmodDialogSettings(normalizedMode, rememberedIncludeSubdirectories);
      } catch (saveErr) {
        warnDev('SaveChmodDialogSettings failed:', saveErr);
      }

      const appliedTargets: Array<{
        path: string
        currentMode: string
        currentOwnerId: string
        currentGroupId: string
        recursive: boolean
        ownerApplied: boolean
        modeApplied: boolean
      }> = [];
      const failures: string[] = [];

      for (const target of targetItems) {
        const targetCurrentMode = normalizeChmodMode(target.item.mode) || normalizedMode;
        const targetRecursive = Boolean(target.item.isDirectory && rememberedIncludeSubdirectories);
        const applied = {
          path: target.path,
          currentMode: targetCurrentMode,
          currentOwnerId: normalizeIdentityId(target.item.uid),
          currentGroupId: normalizeIdentityId(target.item.gid),
          recursive: targetRecursive,
          ownerApplied: false,
          modeApplied: false,
        };
        try {
          if (ownerChanged || groupChanged) {
            await requireChownFile()(sessionId, target.path, ownerSpec, groupSpec, targetRecursive);
            applied.ownerApplied = true;
          }
          if (isBatch || modeChanged) {
            await AppGo.ChmodFile(sessionId, target.path, normalizedMode, targetRecursive);
            applied.modeApplied = true;
          }
        } catch (err) {
          if (applied.ownerApplied || applied.modeApplied) {
            appliedTargets.push(applied);
          }
          failures.push(`${target.path}: ${err}`);
          continue;
        }
        if (applied.ownerApplied || applied.modeApplied) {
          appliedTargets.push(applied);
        }
      }

      if (appliedTargets.length === 0) {
        addToast?.(`${t('权限修改失败')}: ${failures.slice(0, 3).join('；') || t('操作失败')}`, 'error');
        return;
      }

      const undoTargets = [...appliedTargets];
      pushFileManagerUndoEntry({
        undo: async () => {
          for (let index = undoTargets.length - 1; index >= 0; index -= 1) {
            const target = undoTargets[index];
            if (target.ownerApplied) {
              await requireChownFile()(
                sessionId,
                target.path,
                ownerChanged ? target.currentOwnerId : '',
                groupChanged ? target.currentGroupId : '',
                target.recursive,
              );
            }
            if (target.modeApplied) {
              await AppGo.ChmodFile(sessionId, target.path, target.currentMode, target.recursive);
            }
          }
          const refreshTargets = new Set(undoTargets.map((target) => getParentPath(target.path)));
          for (const refreshPath of refreshTargets) {
            await refreshDirectoryAfterTransfer(refreshPath);
          }
        },
      });

      addToast?.(
        isBatch ? `${t('权限修改成功')}: ${appliedTargets.length}${t('项')}` : t('权限修改成功'),
        'success',
      );
      if (failures.length > 0) {
        addToast?.(`${t('权限修改失败')}: ${failures.slice(0, 3).join('；')}`, 'error');
      }
      setChmodTarget(null);
      const refreshTargets = new Set(appliedTargets.map((target) => getParentPath(target.path)));
      for (const refreshPath of refreshTargets) {
        await refreshDirectoryAfterTransfer(refreshPath);
      }
    } catch (err) {
      addToast?.(`${t('权限修改失败')}: ${err}`, 'error');
    }
  };

  return {
    handleCopyPath, handleClipboardCopy, handleClipboardCut,
    handleDelete, handleDeleteShell, handleDeleteItems,
    handleFileListKeyDown,
    handlePaste, handleMkdir, handleNewFile,
    handleCompress, handleCompressItems, handleUncompress,
    startRename, renameCommittingRef, confirmRename,
    closeContextMenu, contextMenuTargetPath,
    openChmodTarget, handleChmod, handleChmodItems, handleChmodSave,
  };
}
