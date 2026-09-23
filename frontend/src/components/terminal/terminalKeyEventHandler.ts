import type * as React from 'react';
import type { Terminal as XTerm } from '@xterm/xterm';
import { getModKey, buildCombo, isMac } from '../../utils/platform.ts';
import { warnDev } from '../../utils/devLog';
import { createGatedLineSender, DEFAULT_TERMINAL_SHORTCUTS, TERMINAL_SIGNAL_BYTES, normalizeTerminalPasteText, readClipboardText, textEncoder } from '../../utils/terminalHelpers.ts';

// 自定义快捷键处理器工厂：attachCustomKeyEventHandler 回调体从 Terminal.tsx 原样搬移。
// 修饰键策略：macOS 上 ⌘ = UI 动作（复制/粘贴/清屏/查找），物理 ⌃ = 终端控制信号
// （SIGINT/EOF 等，与原生终端一致，⌃V 发送 \x16 literal-next 而非粘贴）；
// Win/Linux 上两者合一为 Ctrl。信号/清屏/选区粘贴同时匹配两种组合键。
export function createTerminalKeyEventHandler(deps: {
  term: XTerm;
  shortcutsRef: React.RefObject<Record<string, string> | null>;
  wsRef: React.RefObject<WebSocket | null>;
  pendingCmdRef: React.RefObject<string>;
  awaitingCommandFinishRef: React.RefObject<boolean>;
  termRef: React.RefObject<XTerm | null>;
  termSearchInputRef: React.RefObject<HTMLInputElement | null>;
  setShowTermSearch: React.Dispatch<React.SetStateAction<boolean>>;
  setTermSearchQuery: React.Dispatch<React.SetStateAction<string>>;
  pasteTerminalSelectionToTerminal: () => void | Promise<void>;
}) {
  const {
    term, shortcutsRef, wsRef, pendingCmdRef, awaitingCommandFinishRef, termRef, termSearchInputRef,
    setShowTermSearch, setTermSearchQuery, pasteTerminalSelectionToTerminal,
  } = deps;
  let lastPasteCancel: (() => void) | null = null;
  const gatedLineSender = createGatedLineSender({
    send: (chunk) => {
      if (wsRef.current?.readyState !== WebSocket.OPEN) return false;
      wsRef.current.send(textEncoder.encode(chunk));
      return true;
    },
    armCommandFinish: () => { awaitingCommandFinishRef.current = true; },
  });

  return (e: KeyboardEvent) => {
    if (e.type !== 'keydown') return true;

    // 1. 获取用户自定义的快捷键配置（从 ref 缓存读取，避免热路径访问 localStorage）
    const customShortcuts: Record<string, string> = shortcutsRef.current || DEFAULT_TERMINAL_SHORTCUTS;

    // 2. 解析当前按下的组合键字符串（macOS 下主快捷键使用 ⌘ Meta，Win/Linux 下使用 Ctrl）
    const pressedStr = buildCombo(e, getModKey(e));
    // 3. 基于物理 Ctrl 键的组合键（用于终端控制信号 SIGINT/EOF 等，跨平台始终绑定物理 Ctrl；
    //    Win/Linux 上与 pressedStr 恒相同，仅 macOS 需要单独构建）
    const physicalCtrlStr = isMac ? buildCombo(e, e.ctrlKey) : pressedStr;

    // ── 自定义复制键（默认 ⌘C 或 Ctrl+C）：智能处理 ────────
    if (pressedStr === customShortcuts.copy) {
      const selection = term.getSelection();
      if (selection) {
        e.preventDefault();
        navigator.clipboard.writeText(selection);
        term.clearSelection();
        return false; // 已复制，阻止 xterm 把按键发给服务器
      }
      // 【关键】如果没有选区，则直接放行 (return true)
      // 这样在 Win/Linux 上按 Ctrl+C 能变成标准的终端中断符 (\x03) 发给服务器
      return true;
    }

    // ── Ctrl+Shift+C：强制系统级复制，作为备用方案 ────────
    if (e.ctrlKey && e.shiftKey && !e.altKey && (e.key === 'c' || e.key === 'C')) {
      e.preventDefault();
      const selection = term.getSelection();
      if (selection) navigator.clipboard.writeText(selection);
      return false;
    }

    // ── 自定义粘贴键 ───────────────────────────
    if (pressedStr === customShortcuts.paste) {
      // 在 macOS 上按下 ⌘V 且使用默认粘贴配置时，放行给系统原生 paste 事件处理，避免触发 WebKit 异步剪贴板 "Paste" 提示气泡。
      // 原生路径由 xterm 的 paste 监听器处理：换行归一 + 按需 bracketed paste 包裹（多行粘贴不会逐行自动执行），
      // 并统一走 term.onData（含 normalizeTerminalPasteText 与本地回显逻辑）。
      if (isMac && e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey && (e.key === 'v' || e.key === 'V') && customShortcuts.paste === 'Ctrl+V') {
        return true;
      }

      // 自定义粘贴组合键无法触发系统原生 paste 事件（只有真实的 ⌘V/Ctrl+V 能），
      // 改走 Wails 运行时剪贴板读取，macOS 下同样不弹 "Paste" 气泡；浏览器 dev 回退 Clipboard API。
      e.preventDefault();
      readClipboardText().then((text) => {
        const payload = normalizeTerminalPasteText(text);
        if (payload && wsRef.current?.readyState === WebSocket.OPEN) {
          pendingCmdRef.current += payload.replace(/[\x00-\x1F\x7F]/g, '');
          if (payload.includes('\r')) {
            lastPasteCancel?.();
            lastPasteCancel = gatedLineSender(payload.split('\r'));
          } else {
            wsRef.current.send(textEncoder.encode(payload));
          }
        }
      }).catch((err) => {
        warnDev('Clipboard read failed:', err);
        termRef.current?.focus();
      });
      return false;
    }

    // ── 自定义清屏键 ───────────────────────────
    if (pressedStr === customShortcuts.clear || physicalCtrlStr === customShortcuts.clear) {
      e.preventDefault();
      term.clear();
      return false;
    }

    // 新建标签页的快捷键放行给外层 App 处理
    if (pressedStr === customShortcuts.newTab) {
      return true;
    }

    // ── 查找终端缓冲区（默认 ⌘F 或 Ctrl+F）。仅匹配主快捷键：物理 ⌃F 在 macOS 上
    // 保持终端语义（readline 前进一个字符 \x06），不打开查找 ────────────────
    const findShortcut = customShortcuts.find || 'Ctrl+F';
    if (pressedStr === findShortcut) {
      e.preventDefault();
      const selection = term.getSelection();
      setShowTermSearch(true);
      if (selection && !selection.includes('\n') && selection.length <= 200) {
        setTermSearchQuery(selection);
      }
      requestAnimationFrame(() => {
        termSearchInputRef.current?.focus();
        termSearchInputRef.current?.select();
      });
      return false;
    }

    // ── 自定义控制信号（向服务器发送对应的控制字符）。
    // 同时匹配物理 Ctrl 与主修饰键组合：跨平台物理 Ctrl 始终可用，
    // macOS 上 ⌘+信号键也生效（保留旧版 ⌘ 映射 Ctrl 的肌肉记忆） ────────────────
    for (const [key, bytes] of Object.entries(TERMINAL_SIGNAL_BYTES)) {
      if (customShortcuts[key] && (physicalCtrlStr === customShortcuts[key] || pressedStr === customShortcuts[key])) {
        e.preventDefault();
        if (wsRef.current?.readyState === WebSocket.OPEN) {
          wsRef.current.send(bytes);
        }
        return false;
      }
    }

    // 已有动作优先，避免重复绑定时一次按键执行两个动作
    if (pressedStr === customShortcuts.pasteSelection || physicalCtrlStr === customShortcuts.pasteSelection) {
      e.preventDefault();
      void pasteTerminalSelectionToTerminal();
      return false;
    }

    // ── 其他标准控制字符全部透传给服务器处理 ────────────────────────
    return true;
  };
}
