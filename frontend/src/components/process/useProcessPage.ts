import { useCallback, useEffect, useReducer, useRef, useState, type MouseEvent as ReactMouseEvent } from 'react';
import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';
import { useTranslation } from '../../i18n.ts';
import {
  OVERSCAN,
  ROW_H,
  sortFns,
  type DetailAction,
  type ProcessContextMenu,
  type ProcessInfo,
} from './processTypes.ts';

export interface UseProcessPageOptions {
  sessionId: string;
  addToast: (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;
  active: boolean;
}

export function useProcessPage({ sessionId, addToast, active }: UseProcessPageOptions) {
  const { t } = useTranslation();
  const [processes, setProcesses] = useState<ProcessInfo[] | null>(null);
  const [searchQuery, setSearchQuery] = useState('');
  const [sortKey, setSortKey] = useState('cpu');
  const [sortAsc, setSortAsc] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [selectedPids, setSelectedPids] = useState<Set<string>>(new Set());
  const [killing, setKilling] = useState(false);
  const [contextMenu, setContextMenu] = useState<ProcessContextMenu | null>(null);
  const [detailState, detailDispatch] = useReducer((state: { processes: ProcessInfo[]; activePid: string | null }, action: DetailAction) => {
    switch (action.type) {
      case 'toggle': {
        const idx = state.processes.findIndex((p) => p.pid === action.process.pid);
        if (idx >= 0) {
          if (state.activePid === action.process.pid) {
            const next = state.processes.filter((p) => p.pid !== action.process.pid);
            const ni = next.length ? Math.min(idx, next.length - 1) : -1;
            return { processes: next, activePid: ni >= 0 ? next[ni].pid : null };
          }
          return { ...state, activePid: action.process.pid };
        }
        return { processes: [...state.processes, action.process], activePid: action.process.pid };
      }
      case 'close': {
        const next = state.processes.filter((p) => p.pid !== action.pid);
        return {
          processes: next,
          activePid: state.activePid === action.pid
            ? (next.length ? next[Math.min(state.processes.findIndex((p) => p.pid === action.pid), next.length - 1)].pid : null)
            : state.activePid,
        };
      }
      case 'closeAll':
        return { processes: [], activePid: null };
      default:
        return state;
    }
  }, { processes: [], activePid: null });

  const activeProcess = detailState.processes.find((p) => p.pid === detailState.activePid) || null;
  const [detailHeight, setDetailHeight] = useState(() => {
    const saved = localStorage.getItem('processDetailHeight');
    return saved ? parseFloat(saved) : 200;
  });
  const [envVars, setEnvVars] = useState<string[] | null>(null);
  const [envLoading, setEnvLoading] = useState(false);
  const [showEnv, setShowEnv] = useState(false);
  const [colWidths, setColWidths] = useState<Record<string, number>>(() => {
    const saved = localStorage.getItem('processColWidths');
    if (saved) {
      try {
        return JSON.parse(saved);
      } catch {}
    }
    return { pid: 70, cpu: 70, mem: 70, user: 100, name: 200 };
  });

  const mountedRef = useRef(true);
  const timerRef = useRef<number | null>(null);
  const colDragging = useRef(false);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const tableColumns = `32px ${colWidths.pid}px ${colWidths.cpu}px ${colWidths.mem}px ${colWidths.user}px minmax(${colWidths.name}px, 1fr) minmax(180px, 28%)`;
  const [visibleRange, setVisibleRange] = useState({ start: 0, end: 50 });

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, []);

  const load = useCallback(async () => {
    if (!sessionId) return;
    setLoading(true);
    setError(null);
    try {
      const list = await AppGo.GetFullProcessList(sessionId);
      if (mountedRef.current) {
        setProcesses((list || []) as ProcessInfo[]);
      }
    } catch (e) {
      if (mountedRef.current) {
        setError(e instanceof Error ? e.message : String(e));
        setProcesses([]);
      }
    } finally {
      if (mountedRef.current) setLoading(false);
    }
  }, [sessionId]);

  useEffect(() => {
    if (!active) return;
    let stopped = false;

    const scheduleNext = () => {
      if (timerRef.current) clearTimeout(timerRef.current);
      const interval = parseInt(localStorage.getItem('probeInterval') || '3', 10);
      timerRef.current = setTimeout(async () => {
        await load();
        if (!stopped) scheduleNext();
      }, Math.max(interval, 1) * 1000);
    };

    const run = async () => {
      await load();
      if (!stopped) scheduleNext();
    };

    run();
    const onIntervalChange = () => scheduleNext();
    window.addEventListener('probeIntervalChanged', onIntervalChange);
    return () => {
      stopped = true;
      if (timerRef.current) clearTimeout(timerRef.current);
      window.removeEventListener('probeIntervalChanged', onIntervalChange);
    };
  }, [load, active]);

  useEffect(() => {
    if (!activeProcess) {
      setEnvVars(null);
      setShowEnv(false);
      return;
    }
    setEnvLoading(true);
    setEnvVars(null);
    setShowEnv(false);
    AppGo.GetProcessEnv(sessionId, activeProcess.pid)
      .then((vars) => {
        if (mountedRef.current) {
          setEnvVars(vars || []);
          setEnvLoading(false);
        }
      })
      .catch(() => {
        if (mountedRef.current) {
          setEnvVars([]);
          setEnvLoading(false);
        }
      });
  }, [activeProcess, sessionId]);

  const sorted = !processes ? [] : [...processes].sort((a, b) => {
    const fn = sortFns[sortKey] || sortFns.cpu;
    return sortAsc ? fn(a, b) : fn(b, a);
  });

  const filtered = searchQuery
    ? sorted.filter((p) => (
        String(p.pid).includes(searchQuery)
        || (p.name || '').toLowerCase().includes(searchQuery.toLowerCase())
        || (p.user || '').toLowerCase().includes(searchQuery.toLowerCase())
        || (p.cmd || '').toLowerCase().includes(searchQuery.toLowerCase())
      ))
    : sorted;

  const handleSort = (key: string) => {
    if (colDragging.current) {
      colDragging.current = false;
      return;
    }
    if (key === sortKey) setSortAsc((v) => !v);
    else {
      setSortKey(key);
      setSortAsc(false);
    }
  };

  const toggleSelect = (pid: string) => {
    setSelectedPids((prev) => {
      const next = new Set(prev);
      if (next.has(pid)) next.delete(pid);
      else next.add(pid);
      return next;
    });
  };

  const selectAll = () => {
    if (selectedPids.size === filtered.length) {
      setSelectedPids(new Set());
    } else {
      setSelectedPids(new Set(filtered.map((p) => p.pid)));
    }
  };

  const confirmKill = async (count: number) => {
    if (localStorage.getItem('skipProcessKillConfirm') === 'true') return true;
    const result = await window.lumeDialog?.confirm(
      t('确定要终止选中的 ') + count + t(' 个进程吗？'),
      t('操作确认'),
      t('不再询问'),
    );
    if (!result || typeof result !== 'object' || !result.confirmed) return false;
    if (result.checked) localStorage.setItem('skipProcessKillConfirm', 'true');
    return true;
  };

  const killSelected = async () => {
    if (selectedPids.size === 0) return;
    if (!(await confirmKill(selectedPids.size))) return;

    setKilling(true);
    let killed = 0;
    for (const pid of selectedPids) {
      try {
        await AppGo.KillProcess(sessionId, pid);
        killed++;
      } catch (_) {}
    }
    setKilling(false);
    if (killed > 0) {
      addToast?.(t('已终止 ') + killed + t(' 个进程'), 'success');
      setSelectedPids(new Set());
      void load();
    } else {
      addToast?.(t('无法终止进程，请检查权限'), 'error');
    }
  };

  const killOne = async (p: ProcessInfo | undefined) => {
    if (!p) return;
    if (!(await confirmKill(1))) return;
    setKilling(true);
    try {
      await AppGo.KillProcess(sessionId, p.pid);
      addToast?.(t('已终止 ') + 1 + t(' 个进程'), 'success');
      setSelectedPids((prev) => {
        if (!prev.has(p.pid)) return prev;
        const next = new Set(prev);
        next.delete(p.pid);
        return next;
      });
      void load();
    } catch (_) {
      addToast?.(t('无法终止进程，请检查权限'), 'error');
    } finally {
      setKilling(false);
    }
  };

  const copyText = (text: string | undefined, okMsg: string) => {
    const value = String(text || '');
    if (!value) {
      addToast?.(t('复制失败'), 'error');
      return;
    }
    navigator.clipboard?.writeText(value).then(() => {
      addToast?.(okMsg || `${t('已复制')}: ${value}`, 'success');
    }).catch(() => {
      addToast?.(t('复制失败'), 'error');
    });
  };

  const copyEnv = async (p: ProcessInfo) => {
    if (!p) return;
    try {
      const vars = await AppGo.GetProcessEnv(sessionId, p.pid);
      if (!vars?.length) {
        addToast?.(t('无环境变量'), 'error');
        return;
      }
      await navigator.clipboard.writeText(vars.join('\n'));
      addToast?.(`${t('已复制')}: ${t('环境变量')} (${vars.length})`, 'success');
    } catch (_) {
      addToast?.(t('复制失败'), 'error');
    }
  };

  const handleRowClick = (p: ProcessInfo) => {
    detailDispatch({ type: 'toggle', process: p });
    setSelectedPids(new Set());
  };

  const handleRowContextMenu = (e: ReactMouseEvent, p: ProcessInfo) => {
    e.preventDefault();
    e.stopPropagation();
    const known = (activeProcess?.pid === p.pid && envVars !== null)
      ? envVars.length > 0
      : null;
    setContextMenu({ x: e.clientX, y: e.clientY, process: p, hasEnv: known });
    if (known !== null) return;
    const pid = p.pid;
    AppGo.GetProcessEnv(sessionId, pid)
      .then((vars) => {
        if (!mountedRef.current) return;
        setContextMenu((prev) => {
          if (!prev || prev.process?.pid !== pid) return prev;
          return { ...prev, hasEnv: Array.isArray(vars) && vars.length > 0 };
        });
      })
      .catch(() => {
        if (!mountedRef.current) return;
        setContextMenu((prev) => {
          if (!prev || prev.process?.pid !== pid) return prev;
          return { ...prev, hasEnv: false };
        });
      });
  };

  const startDetailDrag = useCallback((e: ReactMouseEvent) => {
    e.preventDefault();
    const startY = e.clientY;
    const startH = detailHeight;
    const onMove = (ev: MouseEvent) => {
      const dh = Math.max(100, Math.min(600, startH - (ev.clientY - startY)));
      setDetailHeight(dh);
      localStorage.setItem('processDetailHeight', String(dh));
    };
    const onUp = () => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };
    document.body.style.cursor = 'row-resize';
    document.body.style.userSelect = 'none';
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  }, [detailHeight]);

  const startColResize = useCallback((colKey: string, e: ReactMouseEvent) => {
    e.preventDefault();
    const startX = e.clientX;
    const startW = colWidths[colKey];
    colDragging.current = false;
    const onMove = (ev: MouseEvent) => {
      colDragging.current = true;
      const w = Math.max(40, Math.min(500, startW + (ev.clientX - startX)));
      const next = { ...colWidths, [colKey]: w };
      setColWidths(next);
      localStorage.setItem('processColWidths', JSON.stringify(next));
    };
    const onUp = () => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    };
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  }, [colWidths]);

  const handleScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const start = Math.max(0, Math.floor(el.scrollTop / ROW_H) - OVERSCAN);
    const end = Math.min(filtered.length, start + Math.ceil(el.clientHeight / ROW_H) + OVERSCAN * 2);
    setVisibleRange((prev) => (prev.start === start && prev.end === end ? prev : { start, end }));
  }, [filtered.length]);

  useEffect(() => {
    setVisibleRange({ start: 0, end: 50 });
    if (scrollRef.current) scrollRef.current.scrollTop = 0;
  }, [sortKey, sortAsc, searchQuery]);

  return {
    processes,
    filtered,
    loading,
    error,
    searchQuery,
    setSearchQuery,
    sortKey,
    sortAsc,
    selectedPids,
    killing,
    contextMenu,
    setContextMenu,
    detailState,
    detailDispatch,
    activeProcess,
    detailHeight,
    envVars,
    envLoading,
    showEnv,
    setShowEnv,
    tableColumns,
    visibleRange,
    scrollRef,
    load,
    handleSort,
    toggleSelect,
    selectAll,
    killSelected,
    killOne,
    copyText,
    copyEnv,
    handleRowClick,
    handleRowContextMenu,
    startDetailDrag,
    startColResize,
    handleScroll,
  };
}
