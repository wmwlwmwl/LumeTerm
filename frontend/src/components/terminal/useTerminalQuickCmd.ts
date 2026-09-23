import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type * as React from 'react';
import type { Terminal as XTerm } from '@xterm/xterm';
import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';
import { extractQuickCommandParams, fillQuickCommandParams, normalizeQuickCommandParamHistory, type QuickCommandParamHistory } from '../../utils/quickCommandParams.ts';
import { buildWrappedMultiLineCommand, createGatedLineSender, isInteractivePromptText } from '../../utils/terminalHelpers.ts';
import { warnDev } from '../../utils/devLog';
import { normalizeQuickCommandItems, type FlattenedQuickCommand } from '../../utils/terminalCommandAutocomplete.ts';

// ── 快捷命令条：输入框上方一排按钮，点击后弹确认框再发送（对齐安卓端） ──
// 从 Terminal.tsx 原样搬移，闭包变量同名传入。
export function useTerminalQuickCmd(deps: {
  isConnected: boolean;
  sessionId: string;
  serverId: string;
  multiLineWrapEnabled: boolean;
  prepareScreenScrollbackRef: React.RefObject<(command: string) => void>;
  awaitingPasswordRef: React.RefObject<boolean>;
  awaitingCommandFinishRef: React.RefObject<boolean>;
  termRef: React.RefObject<XTerm | null>;
}) {
  const {
    isConnected, sessionId, serverId, multiLineWrapEnabled,
    prepareScreenScrollbackRef, awaitingPasswordRef, awaitingCommandFinishRef, termRef,
  } = deps;

  const [quickCmdBarVisible, setQuickCmdBarVisible] = useState(
    () => localStorage.getItem('terminalQuickCmdBar') === 'true'
  );
  const [quickCmdBarItems, setQuickCmdBarItems] = useState<FlattenedQuickCommand[]>([]);
  const [quickCmdSearch, setQuickCmdSearch] = useState('');
  const [quickCmdSearchOpen, setQuickCmdSearchOpen] = useState(false);
  // 待确认命令：{ item, values } 或 null（点命令条按钮后弹确认框，对齐安卓端）
  const [pendingQuickCmd, setPendingQuickCmd] = useState<{ item: FlattenedQuickCommand; values: Record<string, string> } | null>(null);
  // 多行直接会话执行的逐行发送链取消句柄(新提交时取消,防陈旧分片后发)
  const cancelPacedPasteRef = useRef<() => void>(() => {});
  // 多行直接会话执行：逐行 + 提示符回归门控发送器(单例，重渲染不重建)
  const gatedLineSenderRef = useRef<((lines: string[]) => () => void) | null>(null);
  const gatedLineSender = (() => {
    if (!gatedLineSenderRef.current) {
      gatedLineSenderRef.current = createGatedLineSender({
        send: (chunk) => {
          AppGo.WriteTerminal(sessionId, chunk).catch((err) => {
            warnDev('WriteTerminal failed:', err);
          });
          return true;
        },
        armCommandFinish: () => { awaitingCommandFinishRef.current = true; },
      });
    }
    return gatedLineSenderRef.current;
  })();
  const [quickCmdHistoryParam, setQuickCmdHistoryParam] = useState<number | null>(null);
  const [quickCmdHistoryPosition, setQuickCmdHistoryPosition] = useState({ left: 0, top: 0 });
  const [quickCmdHistorySearch, setQuickCmdHistorySearch] = useState('');
  const [quickCmdParamHistory, setQuickCmdParamHistory] = useState<QuickCommandParamHistory>({});
  const quickCmdParamHistoryRef = useRef<QuickCommandParamHistory>({});
  useEffect(() => {
    if (quickCmdHistoryParam === null) return;
    const closeHistory = (event: MouseEvent) => {
      if ((event.target as HTMLElement).closest('[data-terminal-quick-cmd-history]')) return;
      setQuickCmdHistoryParam(null);
      setQuickCmdHistorySearch('');
    };
    document.addEventListener('click', closeHistory, true);
    return () => document.removeEventListener('click', closeHistory, true);
  }, [quickCmdHistoryParam]);
  useEffect(() => {
    let cancelled = false;
    AppGo.GetParamHistory().then((raw) => {
      if (cancelled) return;
      try {
        const history = normalizeQuickCommandParamHistory(JSON.parse(raw));
        quickCmdParamHistoryRef.current = history;
        setQuickCmdParamHistory(history);
      } catch (_) {}
    }).catch(() => {});
    return () => { cancelled = true; };
  }, []);

  // 命令条搜索：收起时一并清空关键词，避免留下不可见的过滤条件
  const closeQuickCmdSearch = useCallback(() => {
    setQuickCmdSearchOpen(false);
    setQuickCmdSearch('');
  }, []);

  // 命令条搜索：按名称/命令/分组过滤，大小写不敏感
  const filteredQuickCmdItems = useMemo(() => {
    const kw = quickCmdSearch.trim().toLowerCase();
    if (!kw) return quickCmdBarItems;
    return quickCmdBarItems.filter((item) => (
      item.name.toLowerCase().includes(kw)
      || item.command.toLowerCase().includes(kw)
      || (item.groupPath || '').toLowerCase().includes(kw)
    ));
  }, [quickCmdBarItems, quickCmdSearch]);

  // ── 快捷命令条：点按钮先弹确认框（对齐安卓端 QuickCommandConfirmDialog） ──
  const openQuickCmdConfirm = (item: FlattenedQuickCommand) => {
    if (!item?.command) return;
    const values: Record<string, string> = {};
    const history = quickCmdParamHistoryRef.current[item.command] || {};
    extractQuickCommandParams(item.command).forEach((p) => { values[p.num] = history[p.num]?.[0] || ''; });
    setPendingQuickCmd({ item, values });
  };

  // 确认后发送：addCR 语义对齐安卓端 sendQuickCommand
  const sendQuickCmdConfirmed = () => {
    const pending = pendingQuickCmd;
    if (!pending || !isConnected) return;
    const filled = fillQuickCommandParams(pending.item.command, pending.values);
    const text = filled.replace(/\r\n?/g, '\n').trim();
    if (!text) return;
    const nextParamHistory: QuickCommandParamHistory = {
      ...quickCmdParamHistoryRef.current,
      [pending.item.command]: { ...(quickCmdParamHistoryRef.current[pending.item.command] || {}) },
    };
    Object.entries(pending.values).forEach(([num, value]) => {
      if (!value) return;
      const values = nextParamHistory[pending.item.command][num] || [];
      nextParamHistory[pending.item.command][num] = [value, ...values.filter((entry) => entry !== value)].slice(0, 20);
    });
    const normalizedParamHistory = normalizeQuickCommandParamHistory(nextParamHistory);
    quickCmdParamHistoryRef.current = normalizedParamHistory;
    setQuickCmdParamHistory(normalizedParamHistory);
    AppGo.SaveParamHistory(JSON.stringify(normalizedParamHistory)).catch(() => {});
    setPendingQuickCmd(null);
    const lineCount = text.split('\n').length;
    const payload = pending.item.addCR === false
      ? text
      : (multiLineWrapEnabled && lineCount > 1
        ? buildWrappedMultiLineCommand(text)
        : text + '\r');
    if (pending.item.addCR !== false) {
      prepareScreenScrollbackRef.current(text);
    }
    if (pending.item.addCR !== false && !multiLineWrapEnabled && lineCount > 1) {
      // 多行直接会话执行：逐行 + 提示符回归门控，与远端时序无关
      cancelPacedPasteRef.current?.();
      cancelPacedPasteRef.current = gatedLineSender(text.split('\n'));
    } else {
      AppGo.WriteTerminal(sessionId, payload).catch((err) => {
        warnDev('WriteTerminal failed:', err);
      });
    }
    termRef.current?.scrollToBottom();
    if (text.length > 1 && !/^\d+$/.test(text) && !isInteractivePromptText(text)) {
      window.dispatchEvent(new CustomEvent('ssh-command-history', {
        detail: { sessionId: serverId, command: text, time: new Date().toISOString(), source: 'input' }
      }));
    }
    awaitingPasswordRef.current = false;
    awaitingCommandFinishRef.current = pending.item.addCR !== false;
  };

  // ── 快捷命令条：可见时加载列表，命令增删改后刷新 ──
  useEffect(() => {
    const handleBarToggle = (e: Event) => setQuickCmdBarVisible((e as CustomEvent<unknown>).detail !== false);
    window.addEventListener('quick-cmd-bar-changed', handleBarToggle);
    return () => window.removeEventListener('quick-cmd-bar-changed', handleBarToggle);
  }, []);

  // 确认框：Esc 关闭（挂 document，焦点丢失时也能关）
  useEffect(() => {
    if (!pendingQuickCmd) return undefined;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        setPendingQuickCmd(null);
      }
    };
    document.addEventListener('keydown', onKeyDown, true);
    return () => document.removeEventListener('keydown', onKeyDown, true);
  }, [pendingQuickCmd]);

  useEffect(() => {
    if (!quickCmdBarVisible) {
      setQuickCmdBarItems([]);
      setQuickCmdSearch('');
      setQuickCmdSearchOpen(false);
      return undefined;
    }
    let alive = true;
    const load = () => {
      AppGo.GetQuickCommands()
        .then((raw) => {
          if (alive) setQuickCmdBarItems(normalizeQuickCommandItems(raw));
        })
        .catch(() => {
          if (alive) setQuickCmdBarItems([]);
        });
    };
    load();
    window.addEventListener('quick-commands-changed', load);
    return () => {
      alive = false;
      window.removeEventListener('quick-commands-changed', load);
    };
  }, [quickCmdBarVisible]);

  return {
    quickCmdBarVisible,
    quickCmdBarItems,
    quickCmdSearch,
    setQuickCmdSearch,
    quickCmdSearchOpen,
    setQuickCmdSearchOpen,
    closeQuickCmdSearch,
    filteredQuickCmdItems,
    pendingQuickCmd,
    setPendingQuickCmd,
    quickCmdHistoryParam,
    setQuickCmdHistoryParam,
    quickCmdHistoryPosition,
    setQuickCmdHistoryPosition,
    quickCmdHistorySearch,
    setQuickCmdHistorySearch,
    quickCmdParamHistory,
    setQuickCmdParamHistory,
    quickCmdParamHistoryRef,
    openQuickCmdConfirm,
    sendQuickCmdConfirmed,
  };
}
