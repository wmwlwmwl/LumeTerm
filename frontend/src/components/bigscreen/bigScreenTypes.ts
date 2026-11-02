import { createEmptyHist, type ProbeHist, type ProbeInfo } from '../probe/probeTypes.ts';

/** 大屏历史曲线点数（比探针面板更长，适合挂墙展示） */
export const BIG_SCREEN_HISTORY = 60;
/** 单次采集超时：需大于后端 probeTotalBudget（70s），与 ProbePanel 保持一致 */
export const BIG_SCREEN_FETCH_TIMEOUT_MS = 90000;
/** 选中列表持久化键 */
export const BIG_SCREEN_SELECTED_KEY = 'bigScreenSelectedServers';
/** 见过的服务器持久化键（用于仅对"新连接"自动上屏，让手动取消的选择得以保留） */
export const BIG_SCREEN_SEEN_KEY = 'bigScreenSeenServers';

/** 大屏上的一台服务器（来自已连接会话 + 服务器配置） */
export interface BigScreenTarget {
  sessionId: string;
  serverId: string;
  name: string;
  host: string;
  username: string;
  isLocal: boolean;
}

/** 静态信息（OS / 时区 / CPU 型号 / 探测 IP），每个会话只取一次 */
export interface BigScreenStatic {
  os?: string;
  timezone?: string;
  cpuModel?: string;
  ip?: string;
}

/** 单台服务器的大屏数据点 */
export interface BigScreenPoint {
  info: ProbeInfo | null;
  hist: ProbeHist;
  loading: boolean;
  error: string;
  errorCount: number;
  updatedAt: number;
}

export const createEmptyPoint = (): BigScreenPoint => ({
  info: null,
  hist: createEmptyHist(),
  loading: true,
  error: '',
  errorCount: 0,
  updatedAt: 0,
});

export const readStringSet = (key: string): Set<string> => {
  try {
    const raw = JSON.parse(localStorage.getItem(key) || '[]');
    if (!Array.isArray(raw)) return new Set();
    return new Set(raw.filter((item): item is string => typeof item === 'string'));
  } catch (_) {
    return new Set();
  }
};

export const writeStringSet = (key: string, value: Set<string>) => {
  localStorage.setItem(key, JSON.stringify([...value]));
};

/** 按配置的探针间隔轮询（与大屏/探针面板保持一致） */
export const getBigScreenInterval = (): number => {
  const v = parseInt(localStorage.getItem('probeInterval') || '3', 10);
  return v >= 1 ? v : 5;
};
