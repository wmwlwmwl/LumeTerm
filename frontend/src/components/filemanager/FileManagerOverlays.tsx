import React, { Suspense } from 'react';
import { createPortal } from 'react-dom';
import { Upload, RefreshCw } from 'lucide-react';
import { Z } from '../../constants/zIndex.ts';
import FileUploadQueuePanel from '../FileUploadQueuePanel.tsx';
import ContextMenu from './ContextMenu.tsx';
import ChmodDialog from './ChmodDialog.tsx';
import type { FileManagerController } from './fileManagerController.ts';
import type { FileManagerFileItem } from './fileManagerTypes.ts';

const FileEditor = React.lazy(() => import('../FileEditor.tsx'));

// 覆盖层：拖拽遮罩与拖拽提示、右键菜单门户、传输队列面板门户、
// 文件编辑器（modal/popup/split）、chmod 对话框与操作进度遮罩
export function renderFileManagerOverlays(fm: FileManagerController) {
  const {
    t, isActive, addToast,
    isDragOver, fileManagerDragTip,
    contextMenu, clipboard, isDualPaneLayout, activePaneKey,
    fileManagerWorkspace, hideFileManagerTabCloseButton,
    closeContextMenu, handleToggleFileManagerTabPinned, handleCloseFileManagerTab,
    handleCopyPath, currentPath, selectedPathsRef, joinPath, sortedItems,
    getInactiveFileManagerPaneState, transferFileManagerItems,
    handleClipboardCopy, handleClipboardCut, handlePaste, handleDownload, handleDownloadItems,
    openFileManagerPathInNewTab, handleEdit,
    handleOpenSystemEditor, handleOpenWithEditor,
    handleRenameFileManagerTabTitle, startRename, openChmodTarget, handleChmod, handleChmodItems,
    operationInProgressRef, handleDeleteItems, handleDeleteTabDirectory,
    handleDelete, handleDeleteShell, handleMkdir, handleNewFile,
    handleCompress, handleCompressItems, handleUncompress,
    uploadQueueItems, uploadPanelState, uploadPanelClosing, setUploadPanelOpen,
    isUploadAbortable, abortUploadItem, abortUploadItems, removeUploadItems,
    openEditFiles, activeEditPath, handleSaveFile, closeEditFile, closeAllEditFiles,
    activateEditFile, editorMode, handleEditorModeChange,
    editorSplitPosition, handleEditorSplitPositionChange, externalOpening,
    chmodTarget, handleChmodSave, setChmodTarget,
    operationProgress,
  } = fm;

  const resolveContextMenuActionItems = (): FileManagerFileItem[] => {
    if (!contextMenu?.item) {
      return [];
    }
    if (!contextMenu.clipboardUsesSelectedPaths || !Array.isArray(selectedPathsRef.current) || selectedPathsRef.current.length <= 1) {
      return [contextMenu.item];
    }
    const basePath = contextMenu.itemBasePath || currentPath;
    const selectedItems = selectedPathsRef.current
      .map((selectedPath) => sortedItems.find((candidate) => joinPath(basePath, candidate.name) === selectedPath))
      .filter((candidate): candidate is FileManagerFileItem => Boolean(candidate));
    return selectedItems.length === selectedPathsRef.current.length ? selectedItems : [contextMenu.item];
  };
  const chmodTargetCount = chmodTarget && Array.isArray(chmodTarget.items) ? chmodTarget.items.length : 1;

  return (
    <>
      {/* Context Menu */}
      {isDragOver && (
        <div className="drag-overlay">
          <div className="drag-overlay-text"><Upload size={14} /> {t('释放以上传文件/文件夹')}</div>
        </div>
      )}

      {fileManagerDragTip && typeof document !== 'undefined' && createPortal(
        <div
          className="tiptop-bubble tiptop-bubble-bottom"
          style={{
            position: 'fixed',
            left: fileManagerDragTip.x,
            top: fileManagerDragTip.y,
            transform: 'none',
            opacity: 1,
            visibility: 'visible',
            pointerEvents: 'none',
            zIndex: Z.MENU + 2,
            whiteSpace: 'nowrap',
          }}
        >
          {fileManagerDragTip.text}
        </div>,
        document.body
      )}

      {/* Context Menu */}
      {contextMenu && createPortal(
        <ContextMenu
          pos={contextMenu.pos}
          item={contextMenu.item}
          mode={contextMenu.mode || 'item'}
          isPinned={contextMenu.mode === 'tab' && contextMenu.tabPinned === true}
          isSystemPinned={contextMenu.mode === 'tab' && contextMenu.tabSystemPinned === true}
          canTogglePinned={contextMenu.mode === 'tab' && contextMenu.tabSystemPinned !== true}
          canCloseTab={contextMenu.mode === 'tab' && contextMenu.tabPinned !== true && fileManagerWorkspace.tabs.length > 1 && !hideFileManagerTabCloseButton}
          showCreateActions={Boolean(contextMenu.showCreateActions)}
          deleteItemCount={Number.isFinite(Number(contextMenu.deleteItemCount)) ? Number(contextMenu.deleteItemCount) : 1}
          clipboardItemCount={Number.isFinite(Number(contextMenu.clipboardItemCount)) ? Number(contextMenu.clipboardItemCount) : 1}
          canPaste={Boolean(clipboard && Array.isArray(clipboard.paths) && clipboard.paths.length > 0)}
          clipboardActionArrow={isDualPaneLayout ? (activePaneKey === 'right' ? '<-' : '->') : ''}
          t={t}
          onClose={closeContextMenu}
          onTogglePinned={() => {
            if (contextMenu.mode === 'tab') {
              handleToggleFileManagerTabPinned(String(contextMenu.tabId || ''));
            }
            closeContextMenu();
          }}
          onCloseTab={() => {
            if (contextMenu.mode === 'tab') {
              void handleCloseFileManagerTab(String(contextMenu.tabId || ''), undefined);
            }
            closeContextMenu();
          }}
          onCopyPath={() => {
            if (contextMenu.item) {
              handleCopyPath(
                contextMenu.item,
                contextMenu.itemBasePath || currentPath,
                contextMenu.clipboardUsesSelectedPaths ? [...selectedPathsRef.current] : [],
              );
            }
            closeContextMenu();
          }}
          onCopyItem={() => {
            if (contextMenu.item) {
              const clipboardPaths = contextMenu.clipboardUsesSelectedPaths
                ? [...selectedPathsRef.current]
                : [joinPath(contextMenu.itemBasePath || currentPath, contextMenu.item.name)];
              if (isDualPaneLayout) {
                const inactivePane = getInactiveFileManagerPaneState();
                void transferFileManagerItems({
                  paths: clipboardPaths,
                  mode: 'copy',
                  sourceDir: contextMenu.itemBasePath || currentPath,
                  targetDirPath: inactivePane.path,
                });
              } else {
                handleClipboardCopy(clipboardPaths, contextMenu.itemBasePath || currentPath);
              }
            }
            closeContextMenu();
          }}
          onCutItem={() => {
            if (contextMenu.item) {
              const clipboardPaths = contextMenu.clipboardUsesSelectedPaths
                ? [...selectedPathsRef.current]
                : [joinPath(contextMenu.itemBasePath || currentPath, contextMenu.item.name)];
              if (isDualPaneLayout) {
                const inactivePane = getInactiveFileManagerPaneState();
                void transferFileManagerItems({
                  paths: clipboardPaths,
                  mode: 'cut',
                  sourceDir: contextMenu.itemBasePath || currentPath,
                  targetDirPath: inactivePane.path,
                });
              } else {
                handleClipboardCut(clipboardPaths, contextMenu.itemBasePath || currentPath);
              }
            }
            closeContextMenu();
          }}
          onPaste={() => {
            const pasteTargetPath = contextMenu.item && contextMenu.item.isDirectory
              ? joinPath(contextMenu.itemBasePath || currentPath, contextMenu.item.name)
              : (contextMenu.createBasePath || contextMenu.itemBasePath || currentPath);
            void handlePaste(pasteTargetPath);
            closeContextMenu();
          }}
          onDownload={() => {
            const actionItems = resolveContextMenuActionItems();
            if (actionItems.length > 1) {
              void handleDownloadItems(actionItems, { basePath: contextMenu?.itemBasePath || currentPath });
            } else if (actionItems[0]) {
              void handleDownload(actionItems[0], { basePath: contextMenu?.itemBasePath || currentPath });
            }
            closeContextMenu();
          }}
          onOpenInNewTab={() => {
            const nextTabPath = contextMenu.mode === 'tab'
              ? contextMenu.tabPath
              : (contextMenu.item ? joinPath(contextMenu.itemBasePath || currentPath, contextMenu.item.name) : '');
            if (nextTabPath) {
              void openFileManagerPathInNewTab(nextTabPath);
            }
            closeContextMenu();
          }}
          onEdit={() => {
            if (contextMenu.item) {
              void handleEdit(contextMenu.item);
            }
            closeContextMenu();
          }}
          onOpenSystemEditor={() => {
            if (contextMenu.item && !contextMenu.item.isDirectory) {
              const remotePath = joinPath(contextMenu.itemBasePath || currentPath, contextMenu.item.name);
              void handleOpenSystemEditor({ path: remotePath, name: contextMenu.item.name }, '');
            }
            closeContextMenu();
          }}
          onOpenWithEditor={() => {
            if (contextMenu.item && !contextMenu.item.isDirectory) {
              const remotePath = joinPath(contextMenu.itemBasePath || currentPath, contextMenu.item.name);
              void handleOpenWithEditor({ path: remotePath, name: contextMenu.item.name }, '', false);
            }
            closeContextMenu();
          }}
          onRename={() => {
            if (contextMenu.mode === 'tab') {
              void handleRenameFileManagerTabTitle(String(contextMenu.tabId || ''));
            } else if (contextMenu.item) {
              startRename(contextMenu.item);
            }
            closeContextMenu();
          }}
          onChmod={() => {
            if (contextMenu.mode === 'tab' && contextMenu.item) {
              void openChmodTarget(contextMenu.tabPath, contextMenu.item);
            } else {
              const actionItems = resolveContextMenuActionItems();
              if (actionItems.length > 1) {
                void handleChmodItems(actionItems, contextMenu.itemBasePath || currentPath);
              } else if (actionItems[0]) {
                void handleChmod(actionItems[0], contextMenu.itemBasePath || currentPath);
              }
            }
            closeContextMenu();
          }}
          onDelete={() => {
            if (operationInProgressRef.current) {
              addToast?.(t('有操作正在进行，请稍候'), 'warning');
            } else if (contextMenu.deleteUsesSelectedPaths) {
              void handleDeleteItems();
            } else if (contextMenu.mode === 'tab') {
              void handleDeleteTabDirectory(String(contextMenu.tabId || ''), contextMenu.tabPath, false);
            } else if (contextMenu.item) {
              void handleDelete(contextMenu.item);
            }
            closeContextMenu();
          }}
          onDeleteShell={() => {
            if (operationInProgressRef.current) {
              addToast?.(t('有操作正在进行，请稍候'), 'warning');
            } else if (contextMenu.mode === 'tab') {
              void handleDeleteTabDirectory(String(contextMenu.tabId || ''), contextMenu.tabPath, true);
            } else if (contextMenu.item) {
              void handleDeleteShell(contextMenu.item);
            }
            closeContextMenu();
          }}
          onMkdir={() => { void handleMkdir(contextMenu.createBasePath || currentPath); closeContextMenu(); }}
          onNewFile={() => { void handleNewFile(contextMenu.createBasePath || currentPath); closeContextMenu(); }}
          onCompress={() => {
            const actionItems = resolveContextMenuActionItems();
            if (actionItems.length > 1) {
              void handleCompressItems(actionItems, { basePath: contextMenu?.itemBasePath || currentPath });
            } else if (actionItems[0]) {
              void handleCompress(actionItems[0], { basePath: contextMenu?.itemBasePath || currentPath });
            }
            closeContextMenu();
          }}
          onUncompress={() => { if (contextMenu.item) { void handleUncompress(contextMenu.item); } closeContextMenu(); }}
        />,
        document.body
      )}

      {/* 传输队列独立浮动面板（右下角固定定位，不依赖编辑器分栏 host） */}
      {(uploadPanelState.uploadOpen || uploadPanelClosing) && typeof document !== 'undefined' && createPortal(
        <div
          className="fixed bottom-3 right-3 flex flex-col overflow-hidden rounded-[var(--radius-md)] border border-line bg-raised shadow-md"
          style={{ width: 420, maxWidth: 'calc(100vw - 24px)', height: 'clamp(240px, 60vh, 560px)', zIndex: Z.FLOATING_EDITOR }}
        >
          <FileUploadQueuePanel
            items={uploadQueueItems}
            closing={uploadPanelClosing}
            onClose={() => setUploadPanelOpen(false)}
            isAbortable={isUploadAbortable}
            onAbortItem={(item) => { void abortUploadItem(item, t('已终止')); }}
            onAbortItems={(items) => abortUploadItems(items, t('已终止'))}
            onRemoveItems={removeUploadItems}
          />
        </div>,
        document.body
      )}

      {/* File Editor (modal/popup/split 均由 FileEditor 内部决定渲染方式) */}
      {openEditFiles.length > 0 && (
        <Suspense fallback={<div className="flex items-center justify-center h-full text-tertiary">{t('加载中...')}</div>}>
          <FileEditor
            files={openEditFiles}
            activePath={activeEditPath || undefined}
            onSave={handleSaveFile}
            onCloseFile={closeEditFile}
            onCloseAll={closeAllEditFiles}
            onActivate={activateEditFile}
            mode={editorMode as 'split' | 'modal' | 'popup'}
            onModeChange={handleEditorModeChange}
            splitPosition={editorSplitPosition as 'bottom' | 'left' | 'right'}
            onSplitPositionChange={handleEditorSplitPositionChange}
            isActive={isActive}
            onOpenSystemEditor={handleOpenSystemEditor}
            onOpenWithEditor={handleOpenWithEditor}
            externalOpening={externalOpening}
          />
        </Suspense>
      )}

      {/* Chmod Dialog */}
      {chmodTarget && (
        <ChmodDialog
          path={chmodTargetCount > 1 ? `${chmodTarget.path} (${chmodTargetCount}${t('项')})` : chmodTarget.path}
          permission={(chmodTarget.item as FileManagerFileItem).permission ?? ''}
          mode={chmodTarget.mode}
          rememberedMode={chmodTarget.rememberedMode}
          autoApplyLastSettings={chmodTarget.autoApplyLastSettings}
          uid={(chmodTarget.item as FileManagerFileItem).uid ?? ''}
          gid={(chmodTarget.item as FileManagerFileItem).gid ?? ''}
          metadataLoading={chmodTarget.metadataLoading === true}
          ownerCandidates={chmodTarget.ownerCandidates}
          groupCandidates={chmodTarget.groupCandidates}
          includeSubdirectories={chmodTarget.includeSubdirectories}
          showIncludeSubdirectories={chmodTarget.showIncludeSubdirectories}
          onSave={handleChmodSave}
          onClose={() => setChmodTarget(null)}
          t={t}
        />
      )}

      {/* Operation Progress Overlay */}
      {operationProgress && (
        <div className="file-operation-overlay">
          <div className="file-operation-card">
            <div className="file-operation-title">
              {operationProgress.message}
            </div>
            {operationProgress.total && operationProgress.total > 0 ? (
              <>
                <div className="file-operation-progress-container">
                  <div
                    className="file-operation-progress-bar"
                    style={{ width: `${((operationProgress.current || 0) / operationProgress.total) * 100}%` }}
                  />
                </div>
                <div className="file-operation-details">
                  <span>{Math.round(((operationProgress.current || 0) / operationProgress.total) * 100)}%</span>
                  <span>{operationProgress.current || 0} / {operationProgress.total}</span>
                </div>
              </>
            ) : (
              <div className="file-operation-spinner">
                <RefreshCw className="animate-[spin_1s_linear_infinite]" size={20} />
              </div>
            )}
          </div>
        </div>
      )}
    </>
  );
}
