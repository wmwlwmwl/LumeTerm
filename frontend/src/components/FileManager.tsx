import { useTranslation } from '../i18n.ts';
import { useFileManagerCore } from './filemanager/useFileManagerCore.ts';
import { useFileManagerTabScroll } from './filemanager/useFileManagerTabScroll.ts';
import { useFileManagerWorkspaceSync } from './filemanager/useFileManagerWorkspaceSync.ts';
import { useFileManagerPaneView } from './filemanager/useFileManagerPaneView.ts';
import { useFileManagerClipboard } from './filemanager/useFileManagerClipboard.ts';
import { useFileManagerEditorState } from './filemanager/useFileManagerEditorState.ts';
import { useFileManagerUploadPanel } from './filemanager/useFileManagerUploadPanel.ts';
import { useFileManagerDirectoryLoader } from './filemanager/useFileManagerDirectoryLoader.ts';
import { useFileManagerTransfers } from './filemanager/useFileManagerTransfers.ts';
import { useFileManagerLocator } from './filemanager/useFileManagerLocator.ts';
import { useFileManagerEditors } from './filemanager/useFileManagerEditors.ts';
import { useFileManagerTabs } from './filemanager/useFileManagerTabs.ts';
import { useFileManagerItemOps } from './filemanager/useFileManagerItemOps.ts';
import { useFileManagerScrollSync } from './filemanager/useFileManagerScrollSync.ts';
import { useFileManagerDragDrop } from './filemanager/useFileManagerDragDrop.ts';
import { useFileManagerPaneScroller } from './filemanager/useFileManagerPaneScroller.ts';
import { renderFileManagerToolbar } from './filemanager/FileManagerToolbar.tsx';
import { renderFileManagerTabBar } from './filemanager/FileManagerTabBar.tsx';
import { renderFileManagerContent } from './filemanager/FileManagerContent.tsx';
import { renderFileManagerOverlays } from './filemanager/FileManagerOverlays.tsx';
import type { FileManagerController } from './filemanager/fileManagerController.ts';
import type { FileManagerProps } from './filemanager/fileManagerTypes.ts';
import { FILE_LIST_NAME_MIN_WIDTH, FILE_LIST_ACTIONS_COLUMN_WIDTH } from '../utils/fileManagerFormat.ts';

// FileManager 编排层：按声明顺序组合各领域 hook（保证 effect 执行顺序与
// 拆分前一致），并把控制器对象分发给各渲染段（Toolbar/TabBar/Content/Overlays）。
export default function FileManager({ sessionId, sessionGroupId = sessionId, addToast, removeToast, isActive = true, initialPath = '' }: FileManagerProps) {
  const { t } = useTranslation();
  const core = useFileManagerCore({ sessionId, sessionGroupId, isActive, t });
  const tabScroll = useFileManagerTabScroll(core);
  const wsSync = useFileManagerWorkspaceSync({ ...core, ...tabScroll });
  const paneView = useFileManagerPaneView({ ...core, ...wsSync });
  const fmClipboard = useFileManagerClipboard(core);
  const fmEditors = useFileManagerEditorState(core);
  const uploadPanel = useFileManagerUploadPanel({ ...core, ...fmEditors });
  const loader = useFileManagerDirectoryLoader({ ...core, ...wsSync, ...paneView, ...uploadPanel, initialPath, addToast });
  const transfers = useFileManagerTransfers({ ...core, ...wsSync, ...paneView, ...fmClipboard, ...fmEditors, ...uploadPanel, ...loader, addToast });
  const locator = useFileManagerLocator({ ...core, ...wsSync, ...paneView });
  const editors = useFileManagerEditors({ ...core, ...fmEditors, ...transfers, addToast });
  const tabs = useFileManagerTabs({ ...core, ...wsSync, ...paneView, ...loader, ...transfers, initialPath, addToast });
  const itemOps = useFileManagerItemOps({ ...core, ...wsSync, ...paneView, ...fmClipboard, ...fmEditors, ...loader, ...transfers, ...locator, addToast, removeToast });
  const scrollSync = useFileManagerScrollSync({ ...core, ...wsSync, ...paneView, ...uploadPanel, ...transfers, ...itemOps });
  const dragDrop = useFileManagerDragDrop({ ...core, ...wsSync, ...uploadPanel, ...transfers });
  const paneScroller = useFileManagerPaneScroller({ ...core, ...wsSync, ...paneView, ...locator });
  const fm: FileManagerController = {
    ...core, ...tabScroll, ...wsSync, ...paneView, ...fmClipboard, ...fmEditors, ...uploadPanel,
    ...loader, ...transfers, ...locator, ...editors, ...tabs, ...itemOps, ...scrollSync, ...dragDrop, ...paneScroller,
  };
  const {
    fileManagerRootRef, isDualPaneLayout, contextMenu, setContextMenu, currentPath,
    handleDragEnter, handleDragOver, handleDragLeave, handleDrop,
    fileListColumnWidths, uploadInputRef, uploadFolderInputRef, handleSelectedFiles,
  } = fm;

  return (
    <div
      ref={fileManagerRootRef}
      className={`file-manager${isDualPaneLayout ? ' file-manager-dual' : ''}${contextMenu ? ' file-manager-context-menu-open' : ''}`}
      style={{
        position: 'relative',
        '--wails-drop-target': 'drop',
        '--file-col-name-min': `${FILE_LIST_NAME_MIN_WIDTH}px`,
        '--file-col-size': `${fileListColumnWidths.size}px`,
        '--file-col-permission': `${fileListColumnWidths.permission}px`,
        '--file-col-modified': `${fileListColumnWidths.modified}px`,
        '--file-col-actions': `${fileListColumnWidths.actions ?? FILE_LIST_ACTIONS_COLUMN_WIDTH}px`,
        '--file-list-min-width': fileListColumnWidths.minWidth,
      } as React.CSSProperties}
      onContextMenu={(e) => {
        e.preventDefault();
        setContextMenu({
          pos: { x: e.clientX, y: e.clientY },
          item: null,
          mode: 'blank',
          createBasePath: currentPath,
          showCreateActions: true,
        });
      }}
      onDragEnter={handleDragEnter}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <input
        id="fm-upload-file-input"
        name="fm-upload-file-input"
        ref={uploadInputRef}
        type="file"
        multiple
        className="hidden"
        autoComplete="off"
        onChange={(e) => { void handleSelectedFiles(e); }}
      />
      <input
        id="fm-upload-folder-input"
        name="fm-upload-folder-input"
        ref={uploadFolderInputRef}
        type="file"
        multiple
        {...({ webkitdirectory: '', directory: '' } as React.InputHTMLAttributes<HTMLInputElement>)}
        className="hidden"
        autoComplete="off"
        onChange={(e) => { void handleSelectedFiles(e); }}
      />
      {renderFileManagerToolbar(fm)}
      {renderFileManagerTabBar(fm)}
      {renderFileManagerContent(fm)}
      {renderFileManagerOverlays(fm)}
    </div>
  );
}
