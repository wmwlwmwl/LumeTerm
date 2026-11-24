import React from 'react';
import { ClipboardPaste, X, ChevronUp, ChevronDown, FilePlus, FolderPlus, Upload, ClipboardList, FolderUp, RefreshCw } from 'lucide-react';
import { t as tKey } from '../../i18n.ts';
import Tiptop from '../Tiptop.tsx';
import { Button } from '../ui';
import type { FileManagerController } from './fileManagerController.ts';

// 工具栏：可编辑路径输入、剪贴板粘贴/取消、文件定位器、
// 新建/上传/传输队列/返回上级/刷新操作

// 刷新按钮需要独立的旋转反馈：同路径刷新时列表走 preserveView/trackDiff，不显示整表 loading，
// 而目录读取常常几十毫秒就返回，点击后看起来毫无反应。这里给按钮自身加最短 400ms 的旋转动画。
function RefreshButton({ label, onRefresh }: { label: string; onRefresh: () => void | Promise<unknown> }) {
  const [refreshing, setRefreshing] = React.useState(false);
  const handleClick = async () => {
    if (refreshing) return;
    setRefreshing(true);
    try {
      await Promise.all([Promise.resolve(onRefresh()), new Promise((resolve) => setTimeout(resolve, 400))]);
    } finally {
      setRefreshing(false);
    }
  };
  return (
    <Tiptop text={label} placement="bottom">
      <Button
        variant="ghost"
        aria-label={label}
        disabled={refreshing}
        onClick={handleClick}
        className="file-toolbar-outline-btn !w-[30px] !min-w-[30px] !h-[30px] !min-h-[30px] !p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center"
      >
        <RefreshCw size={14} className={refreshing ? 'animate-spin' : ''} />
      </Button>
    </Tiptop>
  );
}

export function renderFileManagerToolbar(fm: FileManagerController) {
  const {
    t, sessionId, addToast,
    editingPath, setEditingPath, currentPath, normalizePath, loadDir,
    clipboard, operationInProgressRef, handlePaste, updateClipboard,
    fileLocatorInputRef, clearFileListTypeahead, setFileLocatorQuery,
    navigateFileLocatorMatch, setFileLocatorActiveIndex, setFileLocatorActiveRowKey,
    fileListRef, fileLocatorQuery, fileLocatorMatches, fileLocatorActiveIndex,
    handleNewFile, handleMkdir, handleUpload, handleUploadFolder,
    uploadPanelState, toggleUploadPanel, activeUploadCount,
  } = fm;
  return (
    <>
      {/* Toolbar */}
      <div className="file-toolbar">
        {/* Editable path input */}
        <input
          className="path-input flex-1 min-w-0"
          type="text"
          name="directoryPath"
          aria-label={t('当前目录路径')}
          value={editingPath !== null ? editingPath : currentPath}
          onChange={(e) => setEditingPath(e.target.value)}
          onFocus={() => setEditingPath(currentPath)}
          onBlur={async () => {
            if (editingPath !== null) {
              const p = editingPath.trim();
              const normalizedTargetPath = normalizePath(p);
              if (normalizedTargetPath && normalizedTargetPath !== currentPath) {
                const resolveDirectoryPath = window?.go?.wailsapp?.App?.ResolveDirectoryPath;
                let resolvedDirectoryPath = normalizedTargetPath;
                if (typeof resolveDirectoryPath === 'function') {
                  try {
                    resolvedDirectoryPath = normalizePath(await resolveDirectoryPath(sessionId, normalizedTargetPath)) || normalizedTargetPath;
                  } catch (_) {}
                }
                if (resolvedDirectoryPath) {
                  void loadDir(resolvedDirectoryPath, {
                    preserveView: false,
                    trackDiff: false,
                    showLoading: true,
                  });
                }
              }
              setEditingPath(null);
            }
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              (e.target as HTMLInputElement).blur();
            } else if (e.key === 'Escape') {
              setEditingPath(null);
              (e.target as HTMLInputElement).blur();
            }
          }}
        />

        {clipboard && (
          <>
            <Tiptop text={t('粘贴')} placement="bottom">
              {/* file-toolbar-outline-btn 系列类保留（file-manager 专属样式，含 has-count/clipboard 配色） */}
              <Button
                variant="ghost"
                className={`file-toolbar-outline-btn has-count ${clipboard.mode === 'cut' ? 'clipboard-cut' : 'clipboard-copy'}`}
                aria-label={t('粘贴')}
                onClick={() => {
                  if (operationInProgressRef.current) {
                    addToast?.(t('有操作正在进行，请稍候'), 'warning');
                  } else {
                    void handlePaste();
                  }
                }}
              >
                <ClipboardPaste size={14} />
                <span className={`clipboard-count-badge ${clipboard.mode === 'cut' ? 'clipboard-cut' : 'clipboard-copy'}`}>{clipboard.paths.length}</span>
              </Button>
            </Tiptop>
            <Tiptop text={t('取消')} placement="bottom">
              <Button
                variant="ghost"
                className="file-toolbar-outline-btn !w-[30px] !min-w-[30px] !h-[30px] !min-h-[30px] !p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center"
                aria-label={t('取消')}
                onClick={() => updateClipboard(null)}
              >
                <X size={14} />
              </Button>
            </Tiptop>
          </>
        )}

        <div className="file-toolbar-locator">
          <div className="file-locator-input-wrap">
            <input
              ref={fileLocatorInputRef}
              className="file-locator-input"
              type="text"
              name="fileLocator"
              value={fileLocatorQuery}
              onFocus={() => {
                clearFileListTypeahead();
              }}
              onChange={(e) => {
                clearFileListTypeahead();
                setFileLocatorQuery(e.target.value);
              }}
              onKeyDown={(e) => {
                if (e.key === 'ArrowDown') {
                  e.preventDefault();
                  navigateFileLocatorMatch(1);
                  return;
                }
                if (e.key === 'ArrowUp') {
                  e.preventDefault();
                  navigateFileLocatorMatch(-1);
                  return;
                }
                if (e.key === 'Enter') {
                  e.preventDefault();
                  navigateFileLocatorMatch(e.shiftKey ? -1 : 1);
                  return;
                }
                if (e.key === 'Escape') {
                  setFileLocatorQuery('');
                  setFileLocatorActiveIndex(0);
                  setFileLocatorActiveRowKey('');
                  fileListRef.current?.focus();
                }
              }}
              placeholder={t('定位文件')}
              aria-label={t('定位文件')}
              spellCheck={false}
            />
            {fileLocatorQuery.trim() ? (
              <button
                type="button"
                className="file-locator-clear-btn"
                aria-label={t('清空输入')}
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => {
                  setFileLocatorQuery('');
                  setFileLocatorActiveIndex(0);
                  setFileLocatorActiveRowKey('');
                  fileLocatorInputRef.current?.focus();
                }}
              >
                <X size={12} />
              </button>
            ) : null}
          </div>
          {fileLocatorQuery.trim() ? (
            <span className="file-locator-status">
              {fileLocatorMatches.length > 0 ? `${fileLocatorActiveIndex + 1}/${fileLocatorMatches.length}` : '0'}
            </span>
          ) : null}
          {fileLocatorQuery.trim() ? (
            <>
              <Tiptop text={t('上一个命中')} placement="bottom">
                <Button
                  variant="ghost"
                  className="file-toolbar-outline-btn !w-[30px] !min-w-[30px] !h-[30px] !min-h-[30px] !p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center"
                  aria-label={t('上一个命中')}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => navigateFileLocatorMatch(-1)}
                  disabled={fileLocatorMatches.length === 0}
                >
                  <ChevronUp size={14} />
                </Button>
              </Tiptop>
              <Tiptop text={t('下一个命中')} placement="bottom">
                <Button
                  variant="ghost"
                  className="file-toolbar-outline-btn !w-[30px] !min-w-[30px] !h-[30px] !min-h-[30px] !p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center"
                  aria-label={t('下一个命中')}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => navigateFileLocatorMatch(1)}
                  disabled={fileLocatorMatches.length === 0}
                >
                  <ChevronDown size={14} />
                </Button>
              </Tiptop>
            </>
          ) : null}
        </div>

        <div className="file-toolbar-actions">
          <Tiptop text={t('新建文件')} placement="bottom">
            <Button
              variant="ghost"
              className="file-toolbar-outline-btn !w-[30px] !min-w-[30px] !h-[30px] !min-h-[30px] !p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center"
              aria-label={t('新建文件')}
              onClick={() => { void handleNewFile(); }}
            >
              <FilePlus size={14} />
            </Button>
          </Tiptop>
          <Tiptop text={t('新建文件夹')} placement="bottom">
            <Button
              variant="ghost"
              className="file-toolbar-outline-btn !w-[30px] !min-w-[30px] !h-[30px] !min-h-[30px] !p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center"
              aria-label={t('新建文件夹')}
              onClick={() => { void handleMkdir(); }}
            >
              <FolderPlus size={14} />
            </Button>
          </Tiptop>
          <Tiptop text={t('上传文件或右键上传文件夹')} placement="bottom">
            <Button
              variant="ghost"
              className="file-toolbar-outline-btn !w-[30px] !min-w-[30px] !h-[30px] !min-h-[30px] !p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center"
              aria-label={t('上传文件或右键上传文件夹')}
              onClick={handleUpload}
              onContextMenu={(e) => {
                e.preventDefault();
                e.stopPropagation();
                void handleUploadFolder();
              }}
            >
              <Upload size={14} />
            </Button>
          </Tiptop>
          <Tiptop text={t('传输队列')} placement="bottom">
            <Button
              variant="ghost"
              aria-pressed={uploadPanelState.uploadOpen}
              aria-label={t('传输队列')}
              onClick={toggleUploadPanel}
              className="file-toolbar-outline-btn relative !w-[30px] !min-w-[30px] !h-[30px] !min-h-[30px] !p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center"
            >
              <ClipboardList size={14} />
              {activeUploadCount > 0 && (
                <span
                  className="absolute -top-1 -right-1 min-w-[15px] h-[15px] px-1 rounded-full bg-accent text-white text-[10px] font-bold leading-[15px] text-center"
                >
                  {activeUploadCount > 99 ? '99+' : activeUploadCount}
                </span>
              )}
            </Button>
          </Tiptop>
          {currentPath !== '/' && (
            <Tiptop text={tKey('返回上级')} placement="bottom">
              <Button
                variant="ghost"
                aria-label={tKey('返回上级')}
                onClick={() => {
                  const parent = currentPath.substring(0, currentPath.lastIndexOf('/')) || '/';
                  void loadDir(parent, {
                    preserveView: false,
                    trackDiff: false,
                    showLoading: true,
                  });
                }}
                className="file-toolbar-outline-btn !w-[30px] !min-w-[30px] !h-[30px] !min-h-[30px] !p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center"
              >
                <FolderUp size={14} />
              </Button>
            </Tiptop>
          )}
          <RefreshButton label={t('刷新')} onRefresh={() => loadDir(currentPath)} />
        </div>
      </div>
    </>
  );
}
