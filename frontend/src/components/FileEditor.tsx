import { createPortal } from 'react-dom';
import CodeMirror from '@uiw/react-codemirror';
import { oneDark } from '@codemirror/theme-one-dark';
import { SquarePen } from 'lucide-react';
import { Z } from '../constants/zIndex.ts';
import { useOverlayScrollLock } from '../hooks/useOverlayScrollLock.ts';
import { formatShortcut } from '../utils/platform.ts';
import { ContextMenu, Modal } from './ui';
import { BASIC_SETUP } from './fileEditor/fileEditorLanguages.ts';
import { FileEditorTabs } from './fileEditor/FileEditorTabs.tsx';
import { FileEditorToolbar } from './fileEditor/FileEditorToolbar.tsx';
import {
  POPUP_RESIZE_HANDLES,
  type FileEditorFile,
  type FileEditorProps,
} from './fileEditor/fileEditorTypes.ts';
import { useFileEditor } from './fileEditor/useFileEditor.ts';

export type { FileEditorFile, FileEditorProps };

export default function FileEditor(props: FileEditorProps) {
  const {
    files,
    onCloseAll,
    onActivate,
    mode = 'modal',
    onModeChange,
    splitPosition = 'right',
    onSplitPositionChange,
    isActive = true,
    onOpenSystemEditor,
    onOpenWithEditor,
    externalOpening = false,
  } = props;

  const {
    t,
    editedContents,
    saving,
    minimized,
    setMinimized,
    preferredExternalApp,
    setPreferredExternalApp,
    contextMenu,
    setContextMenu,
    activeFile,
    currentContent,
    isModified,
    byteSize,
    handleChange,
    handleContextMenu,
    handleMenuAction,
    handleSave,
    closeFileWithConfirm,
    handleCloseAllEditors,
    popupPos,
    startPopupDrag,
    startPopupResize,
    lang,
    extensions,
    ext,
  } = useFileEditor(props);

  useOverlayScrollLock(mode === 'popup' && isActive && !minimized);

  const editorContent = (
    <>
      <FileEditorToolbar
        mode={mode}
        startPopupDrag={startPopupDrag}
        activeFile={activeFile}
        isModified={isModified}
        ext={ext}
        splitPosition={splitPosition}
        onSplitPositionChange={onSplitPositionChange}
        onModeChange={onModeChange}
        externalOpening={externalOpening}
        onOpenSystemEditor={onOpenSystemEditor}
        onOpenWithEditor={onOpenWithEditor}
        currentContent={currentContent}
        preferredExternalApp={preferredExternalApp}
        setPreferredExternalApp={setPreferredExternalApp}
        saving={saving}
        handleSave={handleSave}
        setMinimized={setMinimized}
        handleCloseAllEditors={handleCloseAllEditors}
        filesCount={files.length}
      />

      <FileEditorTabs
        files={files}
        activeFile={activeFile}
        editedContents={editedContents}
        onActivate={onActivate}
        closeFileWithConfirm={closeFileWithConfirm}
      />

      <div className="px-4 pt-1 pb-2 text-xs text-tertiary font-mono border-b border-line overflow-x-auto whitespace-nowrap shrink-0">
        {activeFile ? activeFile.path : ''}
      </div>

      <div className="flex-1 overflow-auto min-h-0">
        {activeFile && (
          <CodeMirror
            key={activeFile.path}
            value={currentContent}
            height="100%"
            minHeight="200px"
            theme={oneDark}
            extensions={extensions}
            onChange={handleChange}
            className="text-sm"
            style={{ height: '100%' }}
            basicSetup={BASIC_SETUP}
          />
        )}
      </div>

      <div className="flex items-center justify-between px-4 py-1.5 border-t border-line text-xs text-tertiary font-mono">
        <span>{currentContent.split('\n').length}{t('行')} · {byteSize}{t('字节')}</span>
        <span>UTF-8 · {lang ? ext.toUpperCase() : t('文本')}</span>
      </div>

      {contextMenu && createPortal(
        <ContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          zIndex={Z.FLOATING_EDITOR_MENU}
          minWidth={160}
          onClose={() => setContextMenu(null)}
          items={[
            { label: t('复制'), shortcut: formatShortcut('Ctrl+C'), disabled: !contextMenu.hasSelection, onSelect: () => handleMenuAction('copy') },
            { label: t('粘贴'), shortcut: formatShortcut('Ctrl+V'), onSelect: () => handleMenuAction('paste') },
            { label: t('剪切'), shortcut: formatShortcut('Ctrl+X'), disabled: !contextMenu.hasSelection, onSelect: () => handleMenuAction('cut') },
            { label: t('全选'), shortcut: formatShortcut('Ctrl+A'), onSelect: () => handleMenuAction('selectAll') },
          ]}
        />,
        document.body,
      )}
    </>
  );

  if (minimized) {
    if (typeof document === 'undefined') return null;
    return createPortal(
      <div
        onClick={() => setMinimized(false)}
        className="fixed bottom-4 right-4 flex items-center gap-2 px-4 py-2 bg-overlay border border-line rounded-[var(--radius-md)] shadow-md cursor-pointer select-none pointer-events-auto animate-[fadeIn_0.12s_ease]"
        style={{ zIndex: Z.FLOATING_EDITOR }}
      >
        <SquarePen size={14} className="shrink-0" />
        <span className="font-mono text-base max-w-[200px] truncate">
          {activeFile ? activeFile.name : t('编辑器')}
        </span>
        {files.length > 1 && (
          <span className="text-xs text-tertiary bg-sunken px-1.5 py-px rounded-[var(--radius-sm)]">
            {files.length}
          </span>
        )}
        {isModified && <span className="text-xs text-warning">{t('未保存')}</span>}
      </div>,
      document.body,
    );
  }

  if (mode === 'popup') {
    if (!isActive || typeof document === 'undefined') return null;
    return createPortal(
      <div
        data-modal-overlay="true"
        className="fixed flex flex-col bg-raised border border-line rounded-[var(--radius-md)] shadow-md overflow-hidden pointer-events-auto"
        style={{
          left: popupPos.x,
          top: popupPos.y,
          width: popupPos.w,
          height: popupPos.h,
          zIndex: Z.FLOATING_EDITOR,
        }}
        onContextMenu={handleContextMenu}
      >
        {editorContent}
        {POPUP_RESIZE_HANDLES.map((h) => (
          <div
            key={h.dir}
            onMouseDown={startPopupResize(h.dir)}
            className="absolute"
            style={{ zIndex: Z.STACK, cursor: h.cursor, ...h.pos }}
          />
        ))}
      </div>,
      document.body,
    );
  }

  if (mode === 'split') {
    if (!isActive) return null;
    const host = document.getElementById('editor-split-host');
    if (!host) return null;
    // 分栏 host 只放编辑器；上传队列为独立浮动面板，不再以工作台标签混入
    return createPortal(
      <div className="w-full h-full flex flex-col" onContextMenu={handleContextMenu}>
        {editorContent}
      </div>,
      host,
    );
  }

  if (typeof document === 'undefined') return null;
  if (!isActive) return null;
  return createPortal(
    <Modal
      open
      onClose={onCloseAll}
      hideClose
      closeOnOverlay={false}
      closeOnEscape={false}
      panelClassName="flex flex-col"
      bodyClassName="p-0 gap-0 flex-1 min-h-0"
      panelStyle={{
        height: 'calc(100vh - 40px)',
        maxHeight: 'calc(100vh - 40px)',
        maxWidth: '100vw',
        marginTop: 40,
      }}
      overlayProps={{ onContextMenu: handleContextMenu }}
    >
        {editorContent}
    </Modal>,
    document.body,
  );
}
