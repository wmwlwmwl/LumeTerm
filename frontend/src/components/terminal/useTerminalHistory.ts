import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type * as React from 'react';
import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';
import { warnDev } from '../../utils/devLog';
import type { QuickCommandsHandle } from '../QuickCommands.tsx';

// 历史指令弹窗逻辑：列表加载/清空/删除、搜索过滤、键盘导航、
// 弹窗定位与外点关闭。从 Terminal.tsx 原样搬移，闭包变量同名传入。
export function useTerminalHistory(deps: {
  showHistory: boolean;
  setShowHistory: React.Dispatch<React.SetStateAction<boolean>>;
  setHistoryPopupPos: React.Dispatch<React.SetStateAction<{ left: number; bottom: number } | null>>;
  historyServerId: string;
  serverId: string;
  showCommands: boolean;
  onQuickCommandsOpenChange?: (open: boolean) => void;
  quickCmdsRef?: React.RefObject<QuickCommandsHandle | null>;
  setCmdInput: React.Dispatch<React.SetStateAction<string>>;
  cmdInputRef: React.RefObject<HTMLTextAreaElement | null>;
}) {
  const {
    showHistory, setShowHistory, setHistoryPopupPos,
    historyServerId, serverId, showCommands,
    onQuickCommandsOpenChange, quickCmdsRef,
    setCmdInput, cmdInputRef,
  } = deps;

  const [historyList, setHistoryList]         = useState<Array<{ id: string; command: string }>>([]);
  const historyListRef                        = useRef<Array<{ id: string; command: string }>>([]);
  useEffect(() => { historyListRef.current = historyList; }, [historyList]);
  const [historyMode, setHistoryMode]         = useState<'server' | 'global'>('server'); // 'server' | 'global'
  const [searchQuery, setSearchQuery]         = useState('');
  const [historySelectedIndex, setHistorySelectedIndex] = useState(-1);
  const historyBtnRef                         = useRef<HTMLButtonElement | null>(null);
  const historySearchInputRef                 = useRef<HTMLInputElement | null>(null);
  const historyScrollRef                      = useRef<HTMLDivElement | null>(null);
  const historyPopupRef                       = useRef<HTMLDivElement | null>(null);

  // ── 点击历史弹窗外关闭（document 捕获阶段 mousedown） ──
  // 必须用 capture：命令按钮 / 底部快捷命令面板会 stopPropagation，
  // 冒泡阶段收不到，历史开着点「命令」或命令面板时就收不起来。
  useEffect(() => {
    if (!showHistory) return;
    const handler = (e: MouseEvent) => {
      const target = e.target;
      if (!(target instanceof Node)) return;
      if (historyPopupRef.current?.contains(target)) return;
      if (historyBtnRef.current?.contains(target)) return;
      // 全局对话框（lumeDialog，如清空确认）打开时，点确认/取消不应收起历史弹窗
      if ((target as Element).closest?.('[data-global-dialog-active="true"]')) return;
      setShowHistory(false);
      setHistoryPopupPos(null);
    };
    document.addEventListener('mousedown', handler, true);
    return () => document.removeEventListener('mousedown', handler, true);
  }, [showHistory]);

  const scrollOnNextUpdate = useRef(false);
  // 加载请求序号：快速切换模式/服务器时丢弃旧结果，避免倒灌
  const historyLoadSeqRef = useRef(0);

  // 弹窗打开/切换模式/收到变更事件时加载历史数据
  const reloadHistoryList = useCallback(() => {
    if (!showHistory) return;
    const seq = ++historyLoadSeqRef.current;
    scrollOnNextUpdate.current = true;
    (async () => {
      try {
        const raw = historyMode === 'global'
          ? await AppGo.GetGlobalCommandHistory()
          : await AppGo.GetCommandHistory(historyServerId);
        if (seq !== historyLoadSeqRef.current) return;
        const entries = JSON.parse(raw);
        const arr = Array.isArray(entries) ? entries : [];
        setHistoryList(arr);
        // 数据为空则无需滚动，直接清空列表
        if (arr.length === 0) scrollOnNextUpdate.current = false;
      } catch {
        if (seq !== historyLoadSeqRef.current) return;
        setHistoryList([]);
        scrollOnNextUpdate.current = false;
      }
    })();
  }, [showHistory, historyMode, historyServerId]);

  useEffect(() => {
    if (!showHistory) return;
    reloadHistoryList();
  }, [showHistory, reloadHistoryList]);

  // 监听清空/变更事件：按作用域刷新弹窗列表
  // - 全局清空/变更：仅当前为全局模式时刷新
  // - 服务器清空/变更：仅当前为服务器模式且目标服务器匹配时刷新
  useEffect(() => {
    if (!showHistory) return;
    const handler = (e: Event) => {
      const d = (e as CustomEvent<{ scope?: string; historyServerId?: string }>).detail;
      const scope = d?.scope || 'server';
      if (scope !== historyMode) return;
      if (scope === 'server' && d?.historyServerId && d.historyServerId !== historyServerId) return;
      reloadHistoryList();
    };
    window.addEventListener('ssh-history-cleared', handler);
    window.addEventListener('ssh-history-changed', handler);
    return () => {
      window.removeEventListener('ssh-history-cleared', handler);
      window.removeEventListener('ssh-history-changed', handler);
    };
  }, [showHistory, historyMode, historyServerId, reloadHistoryList]);

  // 数据渲染后定位到底部，默认选中最新一项
  useEffect(() => {
    if (!showHistory || !scrollOnNextUpdate.current) return;
    // 数据还没加载完（空状态），等待下一次更新
    if (historyList.length === 0) return;
    const el = historyScrollRef.current;
    if (el) requestAnimationFrame(() => { el.scrollTop = el.scrollHeight; });
    scrollOnNextUpdate.current = false;
  }, [historyList, showHistory]);

  const filteredHistory = useMemo(() => {
    if (!searchQuery) return historyList;
    const q = searchQuery.toLowerCase();
    return historyList.filter(item => item.command.toLowerCase().includes(q));
  }, [historyList, searchQuery]);

  // 反转后用于显示：最早的在上边，最新的在底部
  const displayHistory = useMemo(() => [...filteredHistory].reverse(), [filteredHistory]);

  useEffect(() => {
    setHistorySelectedIndex(-1);
  }, [searchQuery, showHistory, historyMode]);

  useEffect(() => {
    if (!showHistory || historySelectedIndex < 0) return;
    const selectedRow = historyScrollRef.current?.querySelector(`[data-history-index="${historySelectedIndex}"]`);
    selectedRow?.scrollIntoView({ block: 'nearest' });
  }, [historySelectedIndex, showHistory, displayHistory]);

  const handleHistorySearchKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      setShowHistory(false);
      setHistoryPopupPos(null);
      requestAnimationFrame(() => cmdInputRef.current?.focus());
      return;
    }
    if (displayHistory.length === 0) return;
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      setHistorySelectedIndex((current) => (
        current < 0 ? 0 : (current + 1) % displayHistory.length
      ));
      return;
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault();
      setHistorySelectedIndex((current) => (
        current < 0 ? displayHistory.length - 1 : (current <= 0 ? displayHistory.length - 1 : current - 1)
      ));
      return;
    }
    if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
      event.preventDefault();
      const selectedItem = historySelectedIndex >= 0
        ? displayHistory[historySelectedIndex]
        : displayHistory[displayHistory.length - 1];
      if (selectedItem) selectHistoryCmd(selectedItem.command);
    }
  };

  const toggleHistory = () => {
    const willShow = !showHistory;
    if (willShow) {
      // 数据加载由 useEffect(showHistory) 负责
      const rect = historyBtnRef.current?.getBoundingClientRect();
      if (rect) {
        setHistoryPopupPos({
          left: Math.max(8, Math.min(rect.right - 480, window.innerWidth - 490)),
          bottom: window.innerHeight - rect.top + 4,
        });
      }
      // 历史弹窗是浮动层，不再收起底部快捷命令面板
    } else {
      setHistoryPopupPos(null);
    }
    setShowHistory(willShow);
  };

  const openHistoryAndFocusSearch = () => {
    if (!showHistory) toggleHistory();
    requestAnimationFrame(() => {
      historySearchInputRef.current?.focus({ preventScroll: true });
      historySearchInputRef.current?.select();
    });
  };

  const toggleCommands = () => {
    const willShow = !showCommands;
    if (willShow) {
      if (showHistory) { setShowHistory(false); setHistoryPopupPos(null); }
      onQuickCommandsOpenChange?.(true);
      return;
    }
    // 关闭面板时检查是否有未保存的修改
    if (quickCmdsRef?.current?.isDirty?.()) {
      quickCmdsRef.current?.showCloseConfirm();
      return; // 让 onClose 回调来关闭
    }
    onQuickCommandsOpenChange?.(false);
  };

  const selectHistoryCmd = (cmd: string) => {
    setCmdInput(cmd);
    setShowHistory(false);
    setHistoryPopupPos(null);
    cmdInputRef.current?.focus();
  };

  const deleteHistoryItem = async (id: string) => {
    const scope = historyMode;
    try {
      const next = historyListRef.current.filter(item => item.id !== id);
      if (scope === 'global') {
        await AppGo.SaveGlobalCommandHistory(JSON.stringify(next));
      } else {
        await AppGo.SaveCommandHistory(historyServerId, JSON.stringify(next));
      }
      setHistoryList(next);
      // 通知历史页 / 自动补全刷新，避免继续显示已删除条目
      window.dispatchEvent(new CustomEvent('ssh-history-changed', {
        detail: { sessionId: serverId, historyServerId, scope }
      }));
    } catch (error) {
      warnDev('[Terminal] 删除历史失败:', error);
    }
  };

  return {
    historyList,
    setHistoryList,
    historyMode,
    setHistoryMode,
    searchQuery,
    setSearchQuery,
    historySelectedIndex,
    historyBtnRef,
    historySearchInputRef,
    historyScrollRef,
    historyPopupRef,
    filteredHistory,
    displayHistory,
    handleHistorySearchKeyDown,
    toggleHistory,
    openHistoryAndFocusSearch,
    toggleCommands,
    selectHistoryCmd,
    deleteHistoryItem,
  };
}
