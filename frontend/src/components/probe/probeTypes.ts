import { t } from '../../i18n.ts';

export const HISTORY_SIZE = 30;
export const PROBE_FETCH_TIMEOUT_MS = 50000;

export interface ProbeHist {
  cpu: number[];
  up: number[];
  down: number[];
}

export interface ProbeInfo {
  os?: string;
  timezone?: string;
  cpuModel?: string;
  ip?: string;
  uptime?: string;
  load1?: number;
  load5?: number;
  load15?: number;
  cpuUsage?: number;
  cpuCores?: number[];
  memUsed?: number;
  memTotal?: number;
  memCache?: number;
  memFree?: number;
  swapTotal?: number;
  swapUsed?: number;
  diskDevice?: string;
  diskTotal?: number;
  diskUsed?: number;
  diskPercent?: number;
  diskReadSpeed?: number;
  diskWriteSpeed?: number;
  diskPartitions?: Array<{ mount?: string; size?: string; avail?: string; usedPct?: number }>;
  netUp?: number;
  netDown?: number;
  netUpTotal?: number;
  netDownTotal?: number;
  networkInterfaces?: Array<{ name?: string; uploadSpeed?: number; downloadSpeed?: number }>;
  processes?: Array<{ pid?: number | string; cpu?: number; mem?: number; cmd?: string }>;
}

export interface ProbeSnapshot {
  info: ProbeInfo | null;
  hist: ProbeHist;
}

export interface ProbePanelProps {
  sessionId: string;
  serverId?: string;
  isConnected?: boolean;
  host: string;
  addToast: (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;
  enabled: boolean;
  active: boolean;
  snapshot?: ProbeSnapshot;
  onSnapshot: (snapshot: ProbeSnapshot) => void;
  onEnable: () => void;
  onShowAllProcesses: () => void;
  onShowNetworkDetails: () => void;
  onOpenPortForward: () => void;
}

export interface DragHandleProps {
  draggable: boolean;
  dragReady: boolean;
  dragging: boolean;
  onPointerDown: (event: React.PointerEvent<HTMLElement>) => void;
  onPointerUp: () => void;
  onPointerCancel: () => void;
  onDragStart: (event: React.DragEvent<HTMLElement>) => void;
  onDragEnd: () => void;
}

export interface SparklineSeries {
  data: number[];
  color: string;
  fill?: boolean;
}

export const clampPct = (value: number) => Math.min(Math.max(Number(value) || 0, 0), 100);
export const pctColor = (pct: number, warn = 60, danger = 85) => (pct >= danger ? 'var(--danger)' : (pct >= warn ? 'var(--warning)' : 'var(--success)'));
export const createEmptyHist = (): ProbeHist => ({ cpu: Array(HISTORY_SIZE).fill(0), up: Array(HISTORY_SIZE).fill(0), down: Array(HISTORY_SIZE).fill(0) });

export type ProbeSidebarTab = 'monitor' | 'extensions';

export interface ProbeSidebarState {
  version: 1;
  activeTab: ProbeSidebarTab;
  activeExtensionTab: string;
}

export const PROBE_SIDEBAR_STATE_PREFIX = 'lumin.probe.sidebar.';
export const PROBE_CARD_ORDER_KEY = 'probePanelCardOrder';
export const PROBE_CARD_ORDER_CHANGED_EVENT = 'probeCardOrderChanged';
export const DEFAULT_PROBE_CARD_ORDER = ['overview', 'portforward', 'cpu', 'memory', 'network', 'disk', 'process'];
export const PROBE_HIDE_IP_KEY = 'probeHideIP';
export const PROBE_HIDE_IP_CHANGED_EVENT = 'probeHideIPChanged';

export const normalizeProbeCardOrder = (value: unknown): string[] => {
  const source = Array.isArray(value) ? value : [];
  const seen = new Set<string>();
  const next: string[] = [];
  source.forEach((item) => {
    if (typeof item === 'string' && DEFAULT_PROBE_CARD_ORDER.includes(item) && !seen.has(item)) {
      seen.add(item);
      next.push(item);
    }
  });
  DEFAULT_PROBE_CARD_ORDER.forEach((item) => {
    if (!seen.has(item)) next.push(item);
  });
  return next;
};

export const readProbeCardOrder = (): string[] => {
  try {
    return normalizeProbeCardOrder(JSON.parse(localStorage.getItem(PROBE_CARD_ORDER_KEY) || '[]'));
  } catch (_) {
    return [...DEFAULT_PROBE_CARD_ORDER];
  }
};

export const persistProbeCardOrder = (order: string[]) => {
  const next = normalizeProbeCardOrder(order);
  localStorage.setItem(PROBE_CARD_ORDER_KEY, JSON.stringify(next));
  window.dispatchEvent(new CustomEvent(PROBE_CARD_ORDER_CHANGED_EVENT, { detail: next }));
};

export const reorderProbeCard = (order: string[], activeId: string, targetId: string, position: 'before' | 'after') => {
  if (!activeId || !targetId || activeId === targetId) return order;
  const next = order.filter((item) => item !== activeId);
  const targetIndex = next.indexOf(targetId);
  if (targetIndex === -1) return order;
  next.splice(position === 'after' ? targetIndex + 1 : targetIndex, 0, activeId);
  return next;
};

export const normalizeProbeSidebarState = (value: unknown): ProbeSidebarState => {
  const source = value && typeof value === 'object' ? value as Record<string, unknown> : {};
  return {
    version: 1,
    activeTab: source.activeTab === 'extensions' ? 'extensions' : 'monitor',
    activeExtensionTab: typeof source.activeExtensionTab === 'string' && source.activeExtensionTab.trim()
      ? source.activeExtensionTab.trim()
      : 'git',
  };
};

export const readProbeSidebarState = (serverId: string): ProbeSidebarState => {
  if (!serverId || typeof window === 'undefined') {
    return normalizeProbeSidebarState(null);
  }
  try {
    return normalizeProbeSidebarState(JSON.parse(window.localStorage.getItem(`${PROBE_SIDEBAR_STATE_PREFIX}${serverId}`) || 'null'));
  } catch {
    return normalizeProbeSidebarState(null);
  }
};

export const persistProbeSidebarState = (serverId: string, value: unknown): ProbeSidebarState => {
  const next = normalizeProbeSidebarState(value);
  if (serverId && typeof window !== 'undefined') {
    try {
      window.localStorage.setItem(`${PROBE_SIDEBAR_STATE_PREFIX}${serverId}`, JSON.stringify(next));
    } catch {}
  }
  return next;
};

export const readProbeHideIP = () => localStorage.getItem(PROBE_HIDE_IP_KEY) === 'true';

export const persistProbeHideIP = (hide: boolean) => {
  localStorage.setItem(PROBE_HIDE_IP_KEY, String(hide));
  window.dispatchEvent(new CustomEvent(PROBE_HIDE_IP_CHANGED_EVENT, { detail: hide }));
};

export function isInternalIP(ip: string): boolean {
  if (!ip) return true;
  const addr = ip.trim();
  if (addr.includes(':')) {
    const lower = addr.toLowerCase();
    if (lower === '::1' || lower === '::') return true;
    const head = parseInt(lower.split(':')[0] || '0', 16);
    if (Number.isNaN(head)) return true;
    if (head >= 0xfe80 && head <= 0xfebf) return true;
    if (head >= 0xfc00 && head <= 0xfdff) return true;
    return false;
  }
  const parts = addr.split('.');
  if (parts.length !== 4) return true;
  const octets = parts.map((part) => parseInt(part, 10));
  if (octets.some((n) => Number.isNaN(n) || n < 0 || n > 255)) return true;
  if (octets[0] === 10) return true;
  if (octets[0] === 127) return true;
  if (octets[0] === 172 && octets[1] >= 16 && octets[1] <= 31) return true;
  if (octets[0] === 192 && octets[1] === 168) return true;
  if (octets[0] === 169 && octets[1] === 254) return true;
  return false;
}

export const maskAddress = (addr: string) => {
  if (!addr) return '';
  if (addr.includes(':')) return '****:****:****:****';
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(addr)) return '***.***.***.***';
  return '*'.repeat(Math.min(addr.length, 16));
};

export function translateProbeError(error: string): string {
  if (!error || !error.startsWith('PROBE_')) return error;
  const sep = error.indexOf('|');
  const code = sep === -1 ? error : error.substring(0, sep);
  const detail = sep === -1 ? '' : error.substring(sep + 1);
  switch (code) {
    case 'PROBE_CLIENT_UNAVAILABLE':
      return t('SSH 客户端不可用');
    case 'PROBE_DEPLOY_GIVEUP':
      return t('探针部署失败 {count} 次，已放弃', { count: detail });
    case 'PROBE_DEPLOY_TIMEOUT':
      return t('探针脚本部署超时（{duration}）', { duration: detail });
    case 'PROBE_DEPLOY_IO':
      return detail ? t('探针脚本部署失败：{detail}', { detail }) : t('探针脚本部署失败');
    case 'PROBE_EXEC_FAILED':
      return detail ? t('探针脚本执行失败：{detail}', { detail }) : t('探针脚本执行失败');
    case 'PROBE_FETCH_TIMEOUT':
      return t('探针数据获取超时');
    default:
      return error;
  }
}
