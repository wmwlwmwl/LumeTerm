import type * as React from 'react';
import { CheckSquare, Clipboard, Copy, ExternalLink, MessageSquarePlus, Search, Trash2 } from 'lucide-react';
import type { Terminal as XTerm } from '@xterm/xterm';
import { Z } from '../../constants/zIndex';
import { formatShortcut } from '../../utils/platform.ts';
import { DEFAULT_TERMINAL_SHORTCUTS } from '../../utils/terminalHelpers.ts';
import { ContextMenu } from '../ui';
import type { I18nKey } from '../../i18n.ts';

type LooseT = (key: I18nKey, vars?: Record<string, unknown>) => string;

// ── 右键上下文菜单（ui/ContextMenu，items 按 source 组装）。
// 从 Terminal.tsx 原样搬移，props 与闭包变量同名。
interface TerminalContextMenuProps {
  contextMenu: { x: number; y: number; source: 'terminal' | 'input'; selText?: string };
  setContextMenu: React.Dispatch<React.SetStateAction<{ x: number; y: number; source: 'terminal' | 'input'; selText?: string } | null>>;
  contextHasSelection: boolean;
  handleMenuAction: (action: string) => void;
  shortcutsRef: React.RefObject<Record<string, string> | null>;
  t: LooseT;
}

export function TerminalContextMenu({
  contextMenu,
  setContextMenu,
  contextHasSelection,
  handleMenuAction,
  shortcutsRef,
  t,
}: TerminalContextMenuProps) {
  return (
    <ContextMenu
      x={contextMenu.x}
      y={contextMenu.y}
      minWidth={190}
      zIndex={Z.MENU}
      onClose={() => setContextMenu(null)}
      items={contextMenu.source === 'input' ? [
        { label: t('剪切'), icon: <Trash2 size={13} />, shortcut: formatShortcut('Ctrl+X'), disabled: !contextHasSelection, onSelect: () => handleMenuAction('cut') },
        { label: t('复制'), icon: <Copy size={13} />, shortcut: formatShortcut('Ctrl+C'), disabled: !contextHasSelection, onSelect: () => handleMenuAction('copy') },
        { label: t('粘贴'), icon: <Clipboard size={13} />, shortcut: formatShortcut('Ctrl+V'), onSelect: () => handleMenuAction('paste') },
        'separator',
        { label: t('全选'), icon: <CheckSquare size={13} />, shortcut: formatShortcut('Ctrl+A'), onSelect: () => handleMenuAction('selectAll') },
      ] : [
        { label: t('复制'), icon: <Copy size={13} />, shortcut: formatShortcut('Ctrl+C'), disabled: !contextHasSelection, onSelect: () => handleMenuAction('copy') },
        { label: t('粘贴'), icon: <Clipboard size={13} />, shortcut: formatShortcut('Ctrl+V'), onSelect: () => handleMenuAction('paste') },
        { label: t('粘贴所选项'), icon: <Clipboard size={13} />, shortcut: formatShortcut(shortcutsRef.current?.pasteSelection || DEFAULT_TERMINAL_SHORTCUTS.pasteSelection), disabled: !contextHasSelection, onSelect: () => handleMenuAction('pasteSelection') },
        'separator',
        { label: t('全选'), icon: <CheckSquare size={13} />, onSelect: () => handleMenuAction('selectAll') },
        { label: t('查找'), icon: <Search size={13} />, shortcut: formatShortcut(shortcutsRef.current?.find || 'Ctrl+F'), onSelect: () => handleMenuAction('find') },
        { label: t('添加到 AI助手'), icon: <MessageSquarePlus size={13} />, disabled: !contextHasSelection, onSelect: () => handleMenuAction('sendToAssistant') },
        { label: t('清空屏幕'), icon: <Trash2 size={13} />, shortcut: formatShortcut('Ctrl+L'), onSelect: () => handleMenuAction('clear') },
      ]}
    />
  );
}

// ── 终端链接菜单：复制 / 打开（对齐安卓） ──
interface TerminalLinkMenuProps {
  linkMenu: { x: number; y: number; url: string };
  setLinkMenu: React.Dispatch<React.SetStateAction<{ x: number; y: number; url: string } | null>>;
  handleLinkMenuAction: (action: string) => void;
  termRef: React.RefObject<XTerm | null>;
  t: LooseT;
}

export function TerminalLinkMenu({
  linkMenu,
  setLinkMenu,
  handleLinkMenuAction,
  termRef,
  t,
}: TerminalLinkMenuProps) {
  return (
    <>
      {/* 透明遮罩：挡住终端拖选，点击空白关闭 */}
      <div
        className="fixed inset-0 bg-transparent cursor-default"
        style={{ zIndex: Z.MENU_BACKDROP }}
        onMouseDown={(e) => {
          e.preventDefault();
          e.stopPropagation();
          try { termRef.current?.clearSelection(); } catch (_) {}
          setLinkMenu(null);
        }}
        onMouseMove={(e) => {
          e.preventDefault();
          e.stopPropagation();
        }}
      />
      <div
        className="fixed bg-raised border border-line rounded-lg shadow-md p-1 min-w-[200px] max-w-[360px] animate-[fadeIn_0.12s_ease]"
        style={{
          left: linkMenu.x,
          top: linkMenu.y,
          zIndex: Z.MENU,
        }}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div
          className="px-3 pt-1.5 pb-1 text-xs text-muted truncate"
          title={linkMenu.url}
        >
          {linkMenu.url}
        </div>
        <div className="h-px my-1 mx-2 bg-line-subtle" />
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            handleLinkMenuAction('copy');
          }}
          className="flex items-center gap-2 w-full h-7 px-3 mx-0 rounded-sm text-sm text-left whitespace-nowrap cursor-pointer outline-none border-none bg-transparent text-secondary transition-colors duration-[80ms] hover:bg-hover hover:text-primary"
        >
          <span className="inline-flex items-center justify-center w-3.5 h-3.5 shrink-0 [&>svg]:w-full [&>svg]:h-full"><Copy size={13} /></span>
          <span className="truncate">{t('复制')}</span>
        </button>
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            handleLinkMenuAction('open');
          }}
          className="flex items-center gap-2 w-full h-7 px-3 mx-0 rounded-sm text-sm text-left whitespace-nowrap cursor-pointer outline-none border-none bg-transparent text-secondary transition-colors duration-[80ms] hover:bg-hover hover:text-primary"
        >
          <span className="inline-flex items-center justify-center w-3.5 h-3.5 shrink-0 [&>svg]:w-full [&>svg]:h-full"><ExternalLink size={13} /></span>
          <span className="truncate">{t('打开')}</span>
        </button>
      </div>
    </>
  );
}

// ── 链接复制 toast ──
export function TerminalLinkToast({ linkToast }: { linkToast: string }) {
  return (
    <div
      className="absolute left-1/2 bottom-14 -translate-x-1/2 bg-[var(--term-context-bg,rgba(20,24,32,0.92))] [border:var(--term-context-border,1px_solid_rgba(255,255,255,0.08))] text-[var(--text-primary,#eaf0f7)] rounded-lg px-3 py-1.5 text-sm pointer-events-none shadow-[var(--term-context-shadow)]"
      style={{ zIndex: Z.POPUP }}
    >
      {linkToast}
    </div>
  );
}
