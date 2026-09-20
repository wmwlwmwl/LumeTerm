import { useCallback, useEffect, useRef } from 'react';
import type * as React from 'react';
import type { Terminal as XTerm } from '@xterm/xterm';
import type { IBufferRange } from '@xterm/xterm';
import type { I18nKey } from '../../i18n.ts';
import { normalizeTerminalPasteText, readClipboardText, textEncoder } from '../../utils/terminalHelpers.ts';
import { warnDev } from '../../utils/devLog';

type LooseT = (key: I18nKey, vars?: Record<string, unknown>) => string;

// 终端剪贴板与选区手势：复制/粘贴（含选区粘贴二次确认）、左键划选复制、
// macOS WKWebView 丢失 mouseup 的合成事件防呆。从 Terminal.tsx 原样搬移。
export function useTerminalClipboard(deps: {
  termRef: React.RefObject<XTerm | null>;
  containerRef: React.RefObject<HTMLDivElement | null>;
  wsRef: React.RefObject<WebSocket | null>;
  pendingCmdRef: React.RefObject<string>;
  terminalRightClickPasteOnEmptyRef: React.RefObject<boolean>;
  terminalRightClickPasteModeRef: React.RefObject<string>;
  terminalLeftClickCopyOnSelectionRef: React.RefObject<boolean>;
  terminalLeftClickCopyOnSelectionModeRef: React.RefObject<string>;
  t: LooseT;
}) {
  const {
    termRef, containerRef, wsRef, pendingCmdRef,
    terminalLeftClickCopyOnSelectionRef, terminalLeftClickCopyOnSelectionModeRef,
    t,
  } = deps;
  const terminalMouseDownSelectionRef = useRef<{ mode: 'mouseup' | 'click'; startClientX: number; startClientY: number; text?: string } | null>(null);
  const isTerminalPointerDownRef = useRef(false);
  // macOS WKWebView / 系统手势可能吞掉 mouseup，导致 xterm 拖选状态机卡死（此后划动指针 = 持续划选）。
  // 主动向 document 派发合成 mouseup 闭合状态机：xterm 的选区收尾监听挂在 document 上且不校验 isTrusted；
  // 事件坐标仅 altClickMovesCursor 分支使用（合成事件 altKey=false 不会进入），无指针信息时传 0 即可。
  const dispatchSyntheticTerminalMouseUp = useCallback((clientX = 0, clientY = 0) => {
    document.dispatchEvent(new MouseEvent('mouseup', {
      bubbles: true,
      cancelable: true,
      view: window,
      clientX,
      clientY,
      buttons: 0,
      button: 0,
    }));
  }, []);

  const getTerminalBufferCellPositionFromMouseEvent = useCallback((event: React.MouseEvent, isSelection = false) => {
    const term = termRef.current;
    const container = containerRef.current;
    if (!term?.buffer?.active || !container || typeof window === 'undefined') {
      return null;
    }
    const screen = container.querySelector('.xterm-screen');
    if (!screen) {
      return null;
    }
    const rect = screen.getBoundingClientRect();
    if (!rect.width || !rect.height || !term.cols || !term.rows) {
      return null;
    }
    const style = window.getComputedStyle(screen);
    const leftPadding = parseInt(style.getPropertyValue('padding-left'), 10) || 0;
    const topPadding = parseInt(style.getPropertyValue('padding-top'), 10) || 0;
    const cellWidth = rect.width / term.cols;
    const cellHeight = rect.height / term.rows;
    if (!Number.isFinite(cellWidth) || !Number.isFinite(cellHeight) || cellWidth <= 0 || cellHeight <= 0) {
      return null;
    }
    const relativeX = event.clientX - rect.left - leftPadding;
    const relativeY = event.clientY - rect.top - topPadding;
    let x = Math.ceil((relativeX + (isSelection ? cellWidth / 2 : 0)) / cellWidth);
    let viewportRow = Math.ceil(relativeY / cellHeight);
    x = Math.min(Math.max(x, 1), term.cols + (isSelection ? 1 : 0)) - 1;
    viewportRow = Math.min(Math.max(viewportRow, 1), term.rows) - 1;
    return {
      x,
      y: term.buffer.active.viewportY + viewportRow,
    };
  }, []);

  const isTerminalBufferCellWithinRange = useCallback((position: { x: number; y: number } | null, range: IBufferRange | null | undefined) => {
    if (!position || !range?.start || !range?.end) {
      return false;
    }
    return (position.y > range.start.y && position.y < range.end.y)
      || (range.start.y === range.end.y && position.y === range.start.y && position.x >= range.start.x && position.x < range.end.x)
      || (range.start.y < range.end.y && position.y === range.end.y && position.x < range.end.x)
      || (range.start.y < range.end.y && position.y === range.start.y && position.x >= range.start.x);
  }, []);

  const copyTerminalSelectionText = useCallback((text: string) => {
    if (!text) {
      return;
    }
    navigator.clipboard.writeText(text).then(() => {
      termRef.current?.focus();
    }).catch((err) => {
      warnDev('Failed to write clipboard:', err);
      termRef.current?.focus();
    });
  }, []);

  const pasteClipboardToTerminal = useCallback(() => {
    // 走 Wails 运行时读取剪贴板：右键/菜单粘贴没有 keydown 可放行成原生 paste 事件，
    // macOS 下 navigator.clipboard.readText() 会弹 "Paste" 提示气泡（issue #263）
    readClipboardText().then((text) => {
      const payload = normalizeTerminalPasteText(text);
      if (payload && wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
        pendingCmdRef.current += payload.replace(/[\x00-\x1F\x7F]/g, '');
        wsRef.current.send(textEncoder.encode(payload));
      }
      termRef.current?.focus();
    }).catch((err) => {
      warnDev('Failed to read clipboard:', err);
      termRef.current?.focus();
    });
  }, []);

  const pasteTerminalSelectionToTerminal = useCallback(async () => {
    const term = termRef.current;
    const selectedText = term?.getSelection?.() || '';
    if (!selectedText || !term) {
      term?.focus();
      return;
    }

    const lineCount = selectedText.replace(/\r\n?/g, '\n').split('\n').length;
    if (lineCount > 3 && localStorage.getItem('skipTerminalSelectionPasteConfirm') !== 'true') {
      const result = await window.lumeDialog?.confirm(
        t('所选内容超过3行，是否继续粘贴？'),
        t('确认粘贴'),
        t('不再询问')
      );
      const confirmed = typeof result === 'object' ? result.confirmed : result === true;
      if (!confirmed) {
        term.focus();
        return;
      }
      if (typeof result === 'object' && result.checked) {
        localStorage.setItem('skipTerminalSelectionPasteConfirm', 'true');
      }
    }

    const payload = normalizeTerminalPasteText(selectedText);
    if (payload && wsRef.current?.readyState === WebSocket.OPEN) {
      pendingCmdRef.current += payload.replace(/[\x00-\x1F\x7F]/g, '');
      wsRef.current.send(textEncoder.encode(payload));
      term.clearSelection();
    }
    term.focus();
  }, [t]);

  const handleTerminalMouseDownCapture = useCallback((event: React.MouseEvent) => {
    if (event.button === 0) {
      isTerminalPointerDownRef.current = true;
    }
    if (event.button !== 0 || !terminalLeftClickCopyOnSelectionRef.current) {
      terminalMouseDownSelectionRef.current = null;
      return;
    }
    const mode = terminalLeftClickCopyOnSelectionModeRef.current === 'mouseup' ? 'mouseup' : 'click';
    if (mode === 'mouseup') {
      terminalMouseDownSelectionRef.current = {
        mode,
        startClientX: event.clientX,
        startClientY: event.clientY,
      };
      return;
    }
    const term = termRef.current;
    const text = term?.getSelection?.() || '';
    const range = term?.getSelectionPosition?.();
    const position = getTerminalBufferCellPositionFromMouseEvent(event, true);
    if (!text || !range || !position || !isTerminalBufferCellWithinRange(position, range)) {
      terminalMouseDownSelectionRef.current = null;
      return;
    }
    terminalMouseDownSelectionRef.current = {
      mode,
      text,
      startClientX: event.clientX,
      startClientY: event.clientY,
    };
  }, [getTerminalBufferCellPositionFromMouseEvent, isTerminalBufferCellWithinRange]);

  const handleTerminalMouseUpCapture = useCallback((event: React.MouseEvent) => {
    if (event.button === 0) {
      isTerminalPointerDownRef.current = false;
    }
    const snapshot = terminalMouseDownSelectionRef.current;
    terminalMouseDownSelectionRef.current = null;
    if (event.button !== 0 || !terminalLeftClickCopyOnSelectionRef.current || !snapshot) {
      return;
    }
    const deltaX = Math.abs(event.clientX - snapshot.startClientX);
    const deltaY = Math.abs(event.clientY - snapshot.startClientY);
    if (snapshot.mode === 'mouseup') {
      if (deltaX <= 4 && deltaY <= 4) {
        return;
      }
      requestAnimationFrame(() => {
        const text = termRef.current?.getSelection?.() || '';
        if (!text) {
          return;
        }
        copyTerminalSelectionText(text);
      });
      return;
    }
    if (deltaX > 4 || deltaY > 4) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    copyTerminalSelectionText(snapshot.text ?? '');
  }, [copyTerminalSelectionText]);

  useEffect(() => {
    const handleWindowMouseMove = (event: MouseEvent) => {
      // macOS WKWebView / 触控板手势丢失 mouseup 防呆：
      // 当物理上没有按键处于按下状态（buttons === 0 或未按左键），但终端仍记录着按下状态时，
      // 说明上一次 mousedown 对应的 mouseup 已丢失。主动派发合成 mouseup 闭合 xterm 拖拽选区状态机。
      if (isTerminalPointerDownRef.current && (event.buttons === 0 || !(event.buttons & 1))) {
        isTerminalPointerDownRef.current = false;
        dispatchSyntheticTerminalMouseUp(event.clientX, event.clientY);
      }
    };

    const handleWindowMouseUp = (event: MouseEvent) => {
      if (event.button === 0) {
        isTerminalPointerDownRef.current = false;
      }
      const snapshot = terminalMouseDownSelectionRef.current;
      if (event.button !== 0 || !terminalLeftClickCopyOnSelectionRef.current || !snapshot || snapshot.mode !== 'mouseup') {
        return;
      }
      terminalMouseDownSelectionRef.current = null;
      const deltaX = Math.abs(event.clientX - snapshot.startClientX);
      const deltaY = Math.abs(event.clientY - snapshot.startClientY);
      if (deltaX <= 4 && deltaY <= 4) {
        return;
      }
      requestAnimationFrame(() => {
        const text = termRef.current?.getSelection?.() || '';
        if (!text) {
          return;
        }
        copyTerminalSelectionText(text);
      });
    };

    const handleWindowPointerCancel = () => {
      if (isTerminalPointerDownRef.current) {
        isTerminalPointerDownRef.current = false;
        dispatchSyntheticTerminalMouseUp();
      }
    };

    const handleWindowBlur = () => {
      // 失焦常意味着按键最终在 WebView 之外释放（mouseup 不会送达），xterm 仍卡在拖选态。
      // 若只清 isTerminalPointerDownRef，检测基准被清空后这个卡死态将永远无法被发现，划动又会拖选；
      // 因此失焦时同样派发合成 mouseup 闭合它。快照需先清，避免合成 mouseup 冒泡触发 mouseup 模式的复制。
      const wasPointerDown = isTerminalPointerDownRef.current;
      isTerminalPointerDownRef.current = false;
      terminalMouseDownSelectionRef.current = null;
      if (wasPointerDown) {
        dispatchSyntheticTerminalMouseUp();
      }
    };

    window.addEventListener('mousemove', handleWindowMouseMove, { capture: true, passive: true });
    window.addEventListener('mouseup', handleWindowMouseUp);
    window.addEventListener('pointercancel', handleWindowPointerCancel);
    window.addEventListener('dragend', handleWindowPointerCancel);
    window.addEventListener('blur', handleWindowBlur);
    return () => {
      window.removeEventListener('mousemove', handleWindowMouseMove, true);
      window.removeEventListener('mouseup', handleWindowMouseUp);
      window.removeEventListener('pointercancel', handleWindowPointerCancel);
      window.removeEventListener('dragend', handleWindowPointerCancel);
      window.removeEventListener('blur', handleWindowBlur);
    };
  }, [copyTerminalSelectionText]);

  return {
    isTerminalPointerDownRef,
    dispatchSyntheticTerminalMouseUp,
    pasteClipboardToTerminal,
    pasteTerminalSelectionToTerminal,
    handleTerminalMouseDownCapture,
    handleTerminalMouseUpCapture,
  };
}
