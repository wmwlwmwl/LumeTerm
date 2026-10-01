import { useEffect } from 'react';
import type * as React from 'react';
import type { Terminal as XTerm } from '@xterm/xterm';
import type { FitAddon } from '@xterm/addon-fit';
import { getResolvedProgramFontPreferences } from '../../utils/programFonts.ts';
import { setKeywordRules, createHighlightState, type KeywordRule } from '../../utils/terminalKeywordHighlight.ts';

// 设置项变更事件监听：快捷键 / 本地回显 / 时间戳 / 命令块 / 字体 / 鼠标手势 /
// 关键字高亮等开关变化时同步 ref 缓存与本地状态。从 Terminal.tsx 原样搬移。
export function useTerminalSettingsEvents(deps: {
  termRef: React.RefObject<XTerm | null>;
  fitAddonRef: React.RefObject<FitAddon | null>;
  gutterRef: React.RefObject<HTMLDivElement | null>;
  shortcutsRef: React.RefObject<Record<string, string> | null>;
  localEchoRef: React.RefObject<boolean>;
  timestampsEnabledRef: React.RefObject<boolean>;
  commandBlocksEnabledRef: React.RefObject<boolean>;
  terminalRightClickPasteOnEmptyRef: React.RefObject<boolean>;
  terminalRightClickPasteModeRef: React.RefObject<string>;
  terminalLeftClickCopyOnSelectionRef: React.RefObject<boolean>;
  terminalLeftClickCopyOnSelectionModeRef: React.RefObject<string>;
  keywordHighlightEnabledRef: React.RefObject<boolean>;
  hlDecoderRef: React.RefObject<TextDecoder>;
  hlStateRef: React.RefObject<ReturnType<typeof createHighlightState>>;
  setTimestampsVisible: React.Dispatch<React.SetStateAction<boolean>>;
  setCommandBlocksVisible: React.Dispatch<React.SetStateAction<boolean>>;
  setTerminalDefaultMouseCursorEnabled: React.Dispatch<React.SetStateAction<boolean>>;
  setAltOpenHistoryEnabled: React.Dispatch<React.SetStateAction<boolean>>;
  setAltOpenHistoryScope: React.Dispatch<React.SetStateAction<'global' | 'input'>>;
  cbExpandAllCollapsed: (term: XTerm | null) => boolean;
  cbClear: () => void;
  scheduleGutterSync: () => void;
}) {
  const {
    termRef, fitAddonRef, gutterRef,
    shortcutsRef, localEchoRef, timestampsEnabledRef, commandBlocksEnabledRef,
    terminalRightClickPasteOnEmptyRef, terminalRightClickPasteModeRef,
    terminalLeftClickCopyOnSelectionRef, terminalLeftClickCopyOnSelectionModeRef,
    keywordHighlightEnabledRef, hlDecoderRef, hlStateRef,
    setTimestampsVisible, setCommandBlocksVisible, setTerminalDefaultMouseCursorEnabled,
    setAltOpenHistoryEnabled, setAltOpenHistoryScope,
    cbExpandAllCollapsed, cbClear, scheduleGutterSync,
  } = deps;

  // 监听快捷键 / 本地回显 / 字体变更，同步更新 ref 缓存（保持设置即时生效）
  useEffect(() => {
    const handleShortcutsChange = (e: Event) => {
      shortcutsRef.current = (e as CustomEvent<Record<string, string>>).detail;
    };
    const handleLocalEchoChange = (e: Event) => {
      localEchoRef.current = (e as CustomEvent<unknown>).detail !== false;
    };
    const handleTimestampsChange = (e: Event) => {
      const detail = (e as CustomEvent<unknown>).detail;
      timestampsEnabledRef.current = detail !== false;
      setTimestampsVisible(detail !== false);
      if (!timestampsEnabledRef.current && !commandBlocksEnabledRef.current) {
        if (gutterRef.current) gutterRef.current.innerHTML = '';
      } else {
        scheduleGutterSync();
      }
    };
    const handleCommandBlocksChange = (e: Event) => {
      const enabled = (e as CustomEvent<unknown>).detail !== false;
      commandBlocksEnabledRef.current = enabled;
      setCommandBlocksVisible(enabled);
      if (!enabled) {
        // ponytail: 关开关前先展开，否则 buffer 里还留着 ⋯ N lines，但 savedOutput 被清掉，再开无法展开
        const term = termRef.current;
        if (term) {
          cbExpandAllCollapsed(term);
        }
        cbClear();
      }
      if (!timestampsEnabledRef.current && !enabled) {
        if (gutterRef.current) gutterRef.current.innerHTML = '';
      } else {
        // 下一帧再 sync，等 display/width 样式生效
        requestAnimationFrame(() => scheduleGutterSync());
      }
    };
    const handleProgramFontSettingsChange = (e: Event) => {
      const detail = (e as CustomEvent<{ terminalFontFamily?: string }>).detail;
      const nextFontFamily = typeof detail?.terminalFontFamily === 'string' && detail.terminalFontFamily.trim()
        ? detail.terminalFontFamily
        : getResolvedProgramFontPreferences().terminalFontFamily;
      if (termRef.current) {
        termRef.current.options.fontFamily = nextFontFamily;
        if (fitAddonRef.current) {
          try { fitAddonRef.current.fit(); } catch (_) {}
        }
        scheduleGutterSync();
      }
    };
    const handleTerminalOutputDefaultMouseCursorChange = (e: Event) => {
      setTerminalDefaultMouseCursorEnabled((e as CustomEvent<unknown>).detail === true);
    };
    const handleTerminalRightClickPasteOnEmptyChange = (e: Event) => {
      terminalRightClickPasteOnEmptyRef.current = (e as CustomEvent<unknown>).detail === true;
    };
    const handleTerminalRightClickPasteModeChange = (e: Event) => {
      terminalRightClickPasteModeRef.current = (e as CustomEvent<string>).detail === 'always' ? 'always' : 'empty';
    };
    const handleTerminalLeftClickCopyOnSelectionChange = (e: Event) => {
      terminalLeftClickCopyOnSelectionRef.current = (e as CustomEvent<unknown>).detail === true;
    };
    const handleTerminalLeftClickCopyOnSelectionModeChange = (e: Event) => {
      terminalLeftClickCopyOnSelectionModeRef.current = (e as CustomEvent<string>).detail === 'mouseup' ? 'mouseup' : 'click';
    };
    const handleKeywordHighlightChange = (e: Event) => {
      keywordHighlightEnabledRef.current = (e as CustomEvent<unknown>).detail === true;
      // 开关切换时重置流式解码器，清除挂起的不完整字节，避免重新开启后污染输出
      hlDecoderRef.current = new TextDecoder();
      // 一并重置前景色状态：关闭前可能停在 fgActive=true，重开时避免误判
      hlStateRef.current = createHighlightState();
    };
    const handleKeywordRulesChange = (e: Event) => {
      const detail = (e as CustomEvent<KeywordRule[]>).detail;
      if (Array.isArray(detail)) setKeywordRules(detail);
    };
    const handleAltOpenHistoryChange = (e: Event) => {
      setAltOpenHistoryEnabled((e as CustomEvent<unknown>).detail !== false);
    };
    const handleAltOpenHistoryScopeChange = (e: Event) => {
      setAltOpenHistoryScope((e as CustomEvent<unknown>).detail === 'global' ? 'global' : 'input');
    };
    window.addEventListener('alt-open-history-changed', handleAltOpenHistoryChange);
    window.addEventListener('alt-open-history-scope-changed', handleAltOpenHistoryScopeChange);
    window.addEventListener('app-shortcuts-changed', handleShortcutsChange);
    window.addEventListener('terminal-local-echo-changed', handleLocalEchoChange);
    window.addEventListener('terminal-timestamps-changed', handleTimestampsChange);
    window.addEventListener('terminal-command-blocks-changed', handleCommandBlocksChange);
    window.addEventListener('terminal-output-default-mouse-cursor-changed', handleTerminalOutputDefaultMouseCursorChange);
    window.addEventListener('terminal-right-click-paste-on-empty-changed', handleTerminalRightClickPasteOnEmptyChange);
    window.addEventListener('terminal-right-click-paste-mode-changed', handleTerminalRightClickPasteModeChange);
    window.addEventListener('terminal-left-click-copy-on-selection-changed', handleTerminalLeftClickCopyOnSelectionChange);
    window.addEventListener('terminal-left-click-copy-on-selection-mode-changed', handleTerminalLeftClickCopyOnSelectionModeChange);
    window.addEventListener('terminal-keyword-highlight-changed', handleKeywordHighlightChange);
    window.addEventListener('terminal-keyword-rules-changed', handleKeywordRulesChange);
    window.addEventListener('program-font-settings-changed', handleProgramFontSettingsChange);
    return () => {
      window.removeEventListener('alt-open-history-changed', handleAltOpenHistoryChange);
      window.removeEventListener('alt-open-history-scope-changed', handleAltOpenHistoryScopeChange);
      window.removeEventListener('app-shortcuts-changed', handleShortcutsChange);
      window.removeEventListener('terminal-local-echo-changed', handleLocalEchoChange);
      window.removeEventListener('terminal-timestamps-changed', handleTimestampsChange);
      window.removeEventListener('terminal-command-blocks-changed', handleCommandBlocksChange);
      window.removeEventListener('terminal-output-default-mouse-cursor-changed', handleTerminalOutputDefaultMouseCursorChange);
      window.removeEventListener('terminal-right-click-paste-on-empty-changed', handleTerminalRightClickPasteOnEmptyChange);
      window.removeEventListener('terminal-right-click-paste-mode-changed', handleTerminalRightClickPasteModeChange);
      window.removeEventListener('terminal-left-click-copy-on-selection-changed', handleTerminalLeftClickCopyOnSelectionChange);
      window.removeEventListener('terminal-left-click-copy-on-selection-mode-changed', handleTerminalLeftClickCopyOnSelectionModeChange);
      window.removeEventListener('terminal-keyword-highlight-changed', handleKeywordHighlightChange);
      window.removeEventListener('terminal-keyword-rules-changed', handleKeywordRulesChange);
      window.removeEventListener('program-font-settings-changed', handleProgramFontSettingsChange);
    };
  }, []);
}
