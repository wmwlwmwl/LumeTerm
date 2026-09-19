import { useEffect } from 'react';
import type * as React from 'react';
import type { Terminal as XTerm } from '@xterm/xterm';
import { readClipboardText } from '../../utils/terminalHelpers.ts';
import { warnDev } from '../../utils/devLog';
import type { I18nKey } from '../../i18n.ts';

type LooseT = (key: I18nKey, vars?: Record<string, unknown>) => string;

// 右键上下文菜单 / 链接菜单 / 链接复制 toast：菜单开合、剪贴板/选区动作、
// 输入框编辑动作与外点关闭。从 Terminal.tsx 原样搬移，闭包变量同名传入。
export function useTerminalMenus(deps: {
  termRef: React.RefObject<XTerm | null>;
  cmdInputRef: React.RefObject<HTMLTextAreaElement | null>;
  setCmdInput: React.Dispatch<React.SetStateAction<string>>;
  contextMenu: { x: number; y: number; source: 'terminal' | 'input' } | null;
  setContextMenu: React.Dispatch<React.SetStateAction<{ x: number; y: number; source: 'terminal' | 'input' } | null>>;
  contextHasSelection: boolean;
  setContextHasSelection: React.Dispatch<React.SetStateAction<boolean>>;
  linkMenu: { x: number; y: number; url: string } | null;
  setLinkMenu: React.Dispatch<React.SetStateAction<{ x: number; y: number; url: string } | null>>;
  setLinkToast: React.Dispatch<React.SetStateAction<string>>;
  serverIdRef: React.RefObject<string>;
  sessionId: string;
  pasteClipboardToTerminal: () => void;
  pasteTerminalSelectionToTerminal: () => void | Promise<void>;
  openTermSearch: (seedText?: string) => void;
  syncCommandInputHeight: () => void;
  scheduleCommandAutocompleteSuggestions: (nextValue: string) => void;
  closeCommandAutocomplete: () => void;
  commandAutocompleteFocusedRef: React.RefObject<boolean>;
  terminalRightClickPasteOnEmptyRef: React.RefObject<boolean>;
  terminalRightClickPasteModeRef: React.RefObject<string>;
  t: LooseT;
}) {
  const {
    termRef, cmdInputRef, setCmdInput,
    contextMenu, setContextMenu, setContextHasSelection,
    linkMenu, setLinkMenu, setLinkToast,
    serverIdRef, sessionId,
    pasteClipboardToTerminal, pasteTerminalSelectionToTerminal, openTermSearch,
    syncCommandInputHeight, scheduleCommandAutocompleteSuggestions, closeCommandAutocomplete,
    commandAutocompleteFocusedRef,
    terminalRightClickPasteOnEmptyRef, terminalRightClickPasteModeRef,
    t,
  } = deps;

  const handleContextMenu = (e: React.MouseEvent) => {
    const target = e.target as Element | null;
    if (!target?.closest?.('.xterm-helper-textarea')
      && target?.closest?.('.term-command-input, input, textarea, button, select, [contenteditable="true"]')) {
      return;
    }
    e.preventDefault();
    setLinkMenu(null);
    const hasSelection = !!(termRef.current && termRef.current.getSelection());
    const rightClickPasteMode = terminalRightClickPasteModeRef.current === 'always' ? 'always' : 'empty';
    if (terminalRightClickPasteOnEmptyRef.current && (rightClickPasteMode === 'always' || !hasSelection)) {
      pasteClipboardToTerminal();
      return;
    }
    setContextHasSelection(hasSelection);
    setContextMenu({ x: e.clientX, y: e.clientY, source: 'terminal' });
  };

  const handleInputContextMenu = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setLinkMenu(null);
    const input = cmdInputRef.current;
    const hasSelection = !!input && (input.selectionStart ?? 0) !== (input.selectionEnd ?? 0);
    setContextHasSelection(hasSelection);
    setContextMenu({ x: e.clientX, y: e.clientY, source: 'input' });
  };

  const closeContextMenu = () => {
    if (contextMenu) setContextMenu(null);
  };

  const closeLinkMenu = () => {
    if (linkMenu) setLinkMenu(null);
  };

  const openExternalUrl = (url: string) => {
    if (!url) return;
    if (typeof window.runtime?.BrowserOpenURL === 'function') {
      window.runtime.BrowserOpenURL(url);
      return;
    }
    window.open(url, '_blank', 'noopener,noreferrer');
  };

  const handleLinkMenuAction = (action: string) => {
    const url = linkMenu?.url || '';
    closeLinkMenu();
    if (!url) return;
    if (action === 'copy') {
      navigator.clipboard.writeText(url).then(() => {
        setLinkToast(t('链接已复制'));
        setTimeout(() => setLinkToast(''), 1500);
      }).catch(() => {});
      termRef.current?.focus();
      return;
    }
    if (action === 'open') {
      openExternalUrl(url);
      termRef.current?.focus();
    }
  };

  // 点击外部关闭右键菜单 / 链接菜单（[role="menu"] 为 ui/ContextMenu 菜单项容器）
  useEffect(() => {
    if (!contextMenu && !linkMenu) return;
    const handler = (e: MouseEvent) => {
      if ((e.target as Element | null)?.closest?.('.context-menu, [role="menu"]')) return;
      setContextMenu(null);
      setLinkMenu(null);
    };
    document.addEventListener('mousedown', handler);
    return () => {
      document.removeEventListener('mousedown', handler);
    };
  }, [contextMenu, linkMenu]);

  const handleMenuAction = (action: string) => {
    const contextSource = contextMenu?.source || 'terminal';
    closeContextMenu();

    if (contextSource === 'input') {
      const input = cmdInputRef.current;
      if (!input) return;
      const value = input.value || '';
      const selectionStart = input.selectionStart ?? 0;
      const selectionEnd = input.selectionEnd ?? selectionStart;
      const selectedText = selectionEnd > selectionStart ? value.slice(selectionStart, selectionEnd) : '';

      switch (action) {
        case 'cut': {
          if (!selectedText) {
            input.focus();
            return;
          }
          navigator.clipboard.writeText(selectedText).catch(() => {});
          const nextValue = `${value.slice(0, selectionStart)}${value.slice(selectionEnd)}`;
          setCmdInput(nextValue);
          requestAnimationFrame(() => {
            if (!cmdInputRef.current) return;
            cmdInputRef.current.focus();
            cmdInputRef.current.setSelectionRange(selectionStart, selectionStart);
            syncCommandInputHeight();
            if (nextValue.trim()) {
              commandAutocompleteFocusedRef.current = true;
              scheduleCommandAutocompleteSuggestions(nextValue);
            } else {
              closeCommandAutocomplete();
            }
          });
          break;
        }
        case 'copy':
          if (selectedText) {
            navigator.clipboard.writeText(selectedText).catch(() => {});
          }
          input.focus();
          break;
        case 'paste':
          readClipboardText().then((text) => {
            const insertText = String(text || '');
            const nextValue = `${value.slice(0, selectionStart)}${insertText}${value.slice(selectionEnd)}`;
            const nextCaret = selectionStart + insertText.length;
            setCmdInput(nextValue);
            requestAnimationFrame(() => {
              if (!cmdInputRef.current) return;
              cmdInputRef.current.focus();
              cmdInputRef.current.setSelectionRange(nextCaret, nextCaret);
              syncCommandInputHeight();
              if (nextValue.trim()) {
                commandAutocompleteFocusedRef.current = true;
                scheduleCommandAutocompleteSuggestions(nextValue);
              } else {
                closeCommandAutocomplete();
              }
            });
          }).catch((err) => {
            warnDev('Failed to read clipboard:', err);
            input.focus();
          });
          break;
        case 'selectAll':
          requestAnimationFrame(() => {
            cmdInputRef.current?.focus();
            cmdInputRef.current?.select();
          });
          break;
        default:
          input.focus();
          break;
      }
      return;
    }

    if (!termRef.current) return;
    switch (action) {
      case 'copy': {
        const selectedText = termRef.current.getSelection();
        if (selectedText) {
          navigator.clipboard.writeText(selectedText);
          termRef.current.clearSelection();
        }
        termRef.current.focus();
        break;
      }
      case 'paste':
        pasteClipboardToTerminal();
        break;
      case 'pasteSelection':
        void pasteTerminalSelectionToTerminal();
        break;
      case 'sendToAssistant': {
        const selectedText = termRef.current.getSelection();
        if (selectedText) {
          window.dispatchEvent(new CustomEvent('ai-terminal-send-to-assistant', {
            detail: {
              sessionId: serverIdRef.current,
              terminalId: sessionId,
              text: selectedText,
            },
          }));
          termRef.current.clearSelection();
        }
        termRef.current.focus();
        break;
      }
      case 'clear':
        termRef.current.clear();
        termRef.current.focus();
        break;
      case 'selectAll':
        termRef.current.selectAll();
        termRef.current.focus();
        break;
      case 'find': {
        const selectedText = termRef.current.getSelection();
        openTermSearch(selectedText || undefined);
        break;
      }
      default:
        termRef.current.focus();
        break;
    }
  };

  return {
    handleContextMenu,
    handleInputContextMenu,
    closeContextMenu,
    closeLinkMenu,
    handleLinkMenuAction,
    handleMenuAction,
  };
}
