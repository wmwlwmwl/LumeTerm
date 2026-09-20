import { memo, useCallback, useEffect, useRef, useState } from 'react';
import {
  Check,
  ChevronDown,
  ChevronRight,
  Clipboard,
  GitBranch,
  GitCommit,
  Plus,
  RefreshCw,
  Send,
  Terminal,
  Trash2,
  Upload,
  X,
} from 'lucide-react';
import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';
import { t as strictTranslate } from '../../i18n.ts';
import { openGlobalContextMenu, type GlobalContextMenuItem } from '../../utils/contextMenu.ts';
const translate = strictTranslate as unknown as (key: string, vars?: Record<string, unknown>) => string;
import Tiptop from '../Tiptop.tsx';

type GitFile = {
  status: string;
  file: string;
  paths?: string[];
  rowKey?: string;
  deletedPlaceholder?: boolean;
};

type GitLog = {
  hash: string;
  shortHash: string;
  author: string;
  date: string;
  subject: string;
  files: GitFile[];
  rowKey?: string;
  deletedPlaceholder?: boolean;
};

type GitRowEffect = {
  effect: 'added' | 'changed' | 'removed';
  startedAt: number;
  durationMs: number;
};

type GitRowDrawer = 'staged' | 'unstaged' | 'logs';

type GitRowCleanup = {
  drawer: GitRowDrawer;
  rowKey: string;
  removePlaceholder: boolean;
};

type GitVisualDiff = {
  stagedRows: GitFile[];
  unstagedRows: GitFile[];
  logRows: GitLog[];
  rowEffects: Record<string, GitRowEffect>;
  cleanup: GitRowCleanup[];
};

type GitRepoState = {
  loading: boolean;
  busy: boolean;
  loaded: boolean;
  isRepository: boolean | null;
  error: string;
  branchName: string;
  hasRemote: boolean;
  upstreamName: string;
  remoteSyncStatus: string;
  files: GitFile[];
  logs: GitLog[];
  stagedRows: GitFile[];
  unstagedRows: GitFile[];
  logRows: GitLog[];
  rowEffects: Record<string, GitRowEffect>;
  logsLoadFailed: boolean;
  commitMessage: string;
  stagedExpanded: boolean;
  unstagedExpanded: boolean;
  logsExpanded: boolean;
  expandedLogHash: string;
  selectedStaged: string[];
  selectedUnstaged: string[];
};

type GitResult = {
  success?: boolean;
  output?: string;
  error?: string;
  exitCode?: number;
  timedOut?: boolean;
};

type GitTerminalCandidate = {
  sessionId: string;
  busy: boolean;
  cwd: string;
  current: boolean;
  recommended: boolean;
};

type GitFileContentCacheEntry = {
  mtime: number;
  size: number;
  content: string;
};

type GitCommitActionMode = 'staged' | 'autostage' | 'disabled';

const REPOSITORY_STORAGE_PREFIX = 'lumin.git.repository-paths.';
const GIT_AUTO_REFRESH_STORAGE_PREFIX = 'lumin.git.auto-refresh.';
const GIT_ROW_EFFECT_DURATION_MS = 1200;
const CONFIRM_KEYS = {
  discard: 'skipGitDiscardConfirm',
  forcePush: 'skipGitForcePushConfirm',
  untrack: 'skipGitUntrackConfirm',
} as const;

function createEmptyRepoState(): GitRepoState {
  return {
    loading: false,
    busy: false,
    loaded: false,
    isRepository: null,
    error: '',
    branchName: '',
    hasRemote: false,
    upstreamName: '',
    remoteSyncStatus: 'no-remote',
    files: [],
    logs: [],
    stagedRows: [],
    unstagedRows: [],
    logRows: [],
    rowEffects: {},
    logsLoadFailed: false,
    commitMessage: '',
    stagedExpanded: true,
    unstagedExpanded: true,
    logsExpanded: true,
    expandedLogHash: '',
    selectedStaged: [],
    selectedUnstaged: [],
  };
}

function normalizeRepositoryPaths(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return [];
  }
  const seen = new Set<string>();
  return value
    .map((item) => typeof item === 'string' ? item.trim() : '')
    .filter((item) => {
      if (!item || seen.has(item)) {
        return false;
      }
      seen.add(item);
      return true;
    });
}

function readRepositoryPaths(serverId: string): string[] {
  if (!serverId || typeof window === 'undefined') {
    return [];
  }
  try {
    return normalizeRepositoryPaths(JSON.parse(window.localStorage.getItem(`${REPOSITORY_STORAGE_PREFIX}${serverId}`) || '[]'));
  } catch {
    return [];
  }
}

function getGitAutoRefreshStorageKey(serverId: string, repoPath: string): string {
  return `${GIT_AUTO_REFRESH_STORAGE_PREFIX}${encodeURIComponent(serverId)}.${encodeURIComponent(repoPath)}`;
}

function readGitAutoRefreshInterval(serverId: string, repoPath: string): string {
  if (typeof window === 'undefined') {
    return '0';
  }
  const value = Number(window.localStorage.getItem(getGitAutoRefreshStorageKey(serverId, repoPath)) || 0);
  return Number.isFinite(value) && value > 0 ? String(Math.floor(value)) : '0';
}

function writeGitAutoRefreshInterval(serverId: string, repoPath: string, value: string): void {
  if (typeof window === 'undefined') {
    return;
  }
  const next = Number(value);
  if (!Number.isFinite(next) || next <= 0) {
    window.localStorage.removeItem(getGitAutoRefreshStorageKey(serverId, repoPath));
    return;
  }
  window.localStorage.setItem(getGitAutoRefreshStorageKey(serverId, repoPath), String(Math.floor(next)));
}

function normalizeGitStatusPath(value: string): string {
  return String(value || '')
    .trim()
    .replace(/^"(.*)"$/, '$1')
    .replace(/\\"/g, '"')
    .replace(/\\\\/g, '\\');
}

function getGitFilePaths(file: GitFile): string[] {
  const paths = Array.isArray(file.paths) && file.paths.length > 0
    ? file.paths
    : [file.file];
  return Array.from(new Set(paths.map(normalizeGitStatusPath).filter(Boolean)));
}

function parseStatusOutput(output: string): GitFile[] {
  return String(output || '')
    .split(/\r?\n/)
    .filter((line) => line.trim())
    .map((line) => {
      const rawFile = line.slice(3).trim();
      const paths = rawFile.includes(' -> ')
        ? rawFile.split(' -> ').map(normalizeGitStatusPath).filter(Boolean)
        : [normalizeGitStatusPath(rawFile)].filter(Boolean);
      return {
        status: line.slice(0, 2),
        file: paths.join(' -> '),
        paths,
      };
    })
    .filter((item) => item.file);
}

function getGitFileIdentity(file: GitFile): string {
  return getGitFilePaths(file).join(' -> ') || String(file.file || '');
}

function getGitFileRowKey(drawer: Exclude<GitRowDrawer, 'logs'>, file: GitFile): string {
  return `${drawer}:${getGitFileIdentity(file)}`;
}

function getGitLogRowKey(log: GitLog): string {
  return `logs:${String(log.hash || '').trim() || `${log.subject}:${log.date}`}`;
}

function isGitDeletedPlaceholder(item: GitFile | GitLog): boolean {
  return item.deletedPlaceholder === true;
}

function areGitFilesEqual(previous: GitFile, next: GitFile): boolean {
  return previous.status === next.status
    && previous.file === next.file
    && JSON.stringify(previous.paths || []) === JSON.stringify(next.paths || []);
}

function areGitLogsEqual(previous: GitLog, next: GitLog): boolean {
  return previous.hash === next.hash
    && previous.shortHash === next.shortHash
    && previous.author === next.author
    && previous.date === next.date
    && previous.subject === next.subject
    && JSON.stringify(previous.files || []) === JSON.stringify(next.files || []);
}

function buildGitFileDrawerRows(drawer: Exclude<GitRowDrawer, 'logs'>, previousRows: GitFile[], nextItems: GitFile[], now: number) {
  const previousItems = (Array.isArray(previousRows) ? previousRows : []).filter((item) => !isGitDeletedPlaceholder(item));
  const previousPlaceholders = (Array.isArray(previousRows) ? previousRows : []).filter((item) => isGitDeletedPlaceholder(item));
  const normalizedItems: GitFile[] = (Array.isArray(nextItems) ? nextItems : []).map((item) => ({
    ...item,
    rowKey: getGitFileRowKey(drawer, item),
    deletedPlaceholder: false,
  }));
  const previousByIdentity = new Map(previousItems.map((item) => [getGitFileIdentity(item), item]));
  const nextIdentities = new Set(normalizedItems.map(getGitFileIdentity));
  const rowEffects: Record<string, GitRowEffect> = {};
  const cleanup: GitRowCleanup[] = [];
  normalizedItems.forEach((item) => {
    const rowKey = item.rowKey || getGitFileRowKey(drawer, item);
    const previous = previousByIdentity.get(getGitFileIdentity(item));
    if (!previous) {
      rowEffects[rowKey] = { effect: 'added', startedAt: now, durationMs: GIT_ROW_EFFECT_DURATION_MS };
      cleanup.push({ drawer, rowKey, removePlaceholder: false });
    } else if (!areGitFilesEqual(previous, item)) {
      rowEffects[rowKey] = { effect: 'changed', startedAt: now, durationMs: GIT_ROW_EFFECT_DURATION_MS };
      cleanup.push({ drawer, rowKey, removePlaceholder: false });
    }
  });
  previousItems.forEach((item) => {
    const identity = getGitFileIdentity(item);
    if (nextIdentities.has(identity)) {
      return;
    }
    const rowKey = `${drawer}:removed:${identity}`;
    const existingPlaceholder = previousPlaceholders.find((entry) => entry.rowKey === rowKey);
    normalizedItems.push(existingPlaceholder || {
      ...item,
      rowKey,
      deletedPlaceholder: true,
    });
    if (!existingPlaceholder) {
      rowEffects[rowKey] = { effect: 'removed', startedAt: now, durationMs: GIT_ROW_EFFECT_DURATION_MS };
      cleanup.push({ drawer, rowKey, removePlaceholder: true });
    }
  });
  return { rows: normalizedItems, rowEffects, cleanup };
}

function buildGitLogDrawerRows(previousRows: GitLog[], nextLogs: GitLog[], now: number) {
  const previousItems = (Array.isArray(previousRows) ? previousRows : []).filter((item) => !isGitDeletedPlaceholder(item));
  const previousPlaceholders = (Array.isArray(previousRows) ? previousRows : []).filter((item) => isGitDeletedPlaceholder(item));
  const normalizedItems: GitLog[] = (Array.isArray(nextLogs) ? nextLogs : []).map((item) => ({
    ...item,
    rowKey: getGitLogRowKey(item),
    deletedPlaceholder: false,
  }));
  const previousByHash = new Map(previousItems.map((item) => [item.hash, item]));
  const nextHashes = new Set(normalizedItems.map((item) => item.hash));
  const rowEffects: Record<string, GitRowEffect> = {};
  const cleanup: GitRowCleanup[] = [];
  normalizedItems.forEach((item) => {
    const rowKey = item.rowKey || getGitLogRowKey(item);
    const previous = previousByHash.get(item.hash);
    if (!previous) {
      rowEffects[rowKey] = { effect: 'added', startedAt: now, durationMs: GIT_ROW_EFFECT_DURATION_MS };
      cleanup.push({ drawer: 'logs', rowKey, removePlaceholder: false });
    } else if (!areGitLogsEqual(previous, item)) {
      rowEffects[rowKey] = { effect: 'changed', startedAt: now, durationMs: GIT_ROW_EFFECT_DURATION_MS };
      cleanup.push({ drawer: 'logs', rowKey, removePlaceholder: false });
    }
  });
  previousItems.forEach((item) => {
    if (nextHashes.has(item.hash)) {
      return;
    }
    const rowKey = `logs:removed:${item.hash}`;
    const existingPlaceholder = previousPlaceholders.find((entry) => entry.rowKey === rowKey);
    normalizedItems.push(existingPlaceholder || {
      ...item,
      rowKey,
      deletedPlaceholder: true,
    });
    if (!existingPlaceholder) {
      rowEffects[rowKey] = { effect: 'removed', startedAt: now, durationMs: GIT_ROW_EFFECT_DURATION_MS };
      cleanup.push({ drawer: 'logs', rowKey, removePlaceholder: true });
    }
  });
  return { rows: normalizedItems, rowEffects, cleanup };
}

function buildGitInitialVisualRows(nextFiles: GitFile[], nextLogs: GitLog[]): GitVisualDiff {
  return {
    stagedRows: nextFiles.filter(isStagedFile).map((item) => ({ ...item, rowKey: getGitFileRowKey('staged', item) })),
    unstagedRows: nextFiles.filter(isUnstagedFile).map((item) => ({ ...item, rowKey: getGitFileRowKey('unstaged', item) })),
    logRows: nextLogs.map((item) => ({ ...item, rowKey: getGitLogRowKey(item) })),
    rowEffects: {},
    cleanup: [],
  };
}

function buildGitVisualDiff(previousState: GitRepoState, nextFiles: GitFile[], nextLogs: GitLog[], now: number): GitVisualDiff {
  const stagedDiff = buildGitFileDrawerRows('staged', previousState.stagedRows, nextFiles.filter(isStagedFile), now);
  const unstagedDiff = buildGitFileDrawerRows('unstaged', previousState.unstagedRows, nextFiles.filter(isUnstagedFile), now);
  const logsDiff = buildGitLogDrawerRows(previousState.logRows, nextLogs, now);
  return {
    stagedRows: stagedDiff.rows,
    unstagedRows: unstagedDiff.rows,
    logRows: logsDiff.rows,
    rowEffects: {
      ...previousState.rowEffects,
      ...stagedDiff.rowEffects,
      ...unstagedDiff.rowEffects,
      ...logsDiff.rowEffects,
    },
    cleanup: [...stagedDiff.cleanup, ...unstagedDiff.cleanup, ...logsDiff.cleanup],
  };
}

function parseGitConfigTemplateOutput(output: unknown): { origin: string; template: string } {
  const line = String(output || '').trim().split(/\r?\n/).filter(Boolean).pop() || '';
  const separator = line.indexOf('\t');
  if (separator < 0) {
    return { origin: '', template: normalizeGitStatusPath(line) };
  }
  return {
    origin: normalizeGitStatusPath(line.slice(0, separator).replace(/^file:/, '')),
    template: normalizeGitStatusPath(line.slice(separator + 1)),
  };
}

function normalizeRemoteGitPath(value: string): string {
  const normalized = String(value || '').trim().replace(/\\/g, '/');
  if (!normalized) {
    return '';
  }
  const parts: string[] = [];
  normalized.split('/').forEach((part) => {
    if (!part || part === '.') {
      return;
    }
    if (part === '..') {
      if (parts.length > 0) {
        parts.pop();
      }
      return;
    }
    parts.push(part);
  });
  return `/${parts.join('/')}`;
}

function resolveRemoteGitTemplatePath(template: string, origin: string, repoPath: string): string {
  const normalizedTemplate = normalizeGitStatusPath(template);
  if (!normalizedTemplate) {
    return '';
  }
  if (normalizedTemplate.startsWith('/')) {
    return normalizeRemoteGitPath(normalizedTemplate);
  }
  const normalizedOrigin = normalizeRemoteGitPath(origin);
  const originDir = normalizedOrigin.includes('/') ? normalizedOrigin.slice(0, normalizedOrigin.lastIndexOf('/')) : '';
  const originIsRepositoryConfig = normalizedOrigin.endsWith('/.git/config') || normalizedOrigin.endsWith('/.git/config.worktree');
  return normalizeRemoteGitPath(`${originIsRepositoryConfig ? repoPath : (originDir || repoPath)}/${normalizedTemplate}`);
}

function getGitRowAnimationStyle(effect: GitRowEffect | undefined) {
  if (!effect) {
    return undefined;
  }
  const elapsed = Math.max(0, Date.now() - effect.startedAt);
  return {
    animationDuration: `${effect.durationMs}ms`,
    animationDelay: `-${Math.min(elapsed, Math.max(0, effect.durationMs - 16))}ms`,
  };
}

function parseGitLogs(output: string): GitLog[] {
  const logs: GitLog[] = [];
  let current: GitLog | null = null;
  for (const rawLine of String(output || '').split(/\r?\n/)) {
    const line = rawLine.trimEnd();
    if (!line.trim()) {
      continue;
    }
    const header = line.match(/^([0-9a-f]{40})\x1f([0-9a-f]+)\x1f(.*?)\x1f(.*?)\x1f(.*)$/);
    if (header) {
      if (current) {
        logs.push(current);
      }
      current = {
        hash: header[1],
        shortHash: header[2],
        author: header[3],
        date: header[4],
        subject: header[5],
        files: [],
      };
      continue;
    }
    if (!current || !/^[MADRCU?]/.test(line)) {
      continue;
    }
    const parts = line.split('\t');
    const status = (parts[0] || '').trim();
    const file = (parts[parts.length - 1] || '').trim();
    if (status && file) {
      current.files.push({ status, file });
    }
  }
  if (current) {
    logs.push(current);
  }
  return logs;
}

function getPrimaryStatus(status: string): string {
  const first = status?.[0] && status[0] !== ' ' ? status[0] : '';
  const second = status?.[1] && status[1] !== ' ' ? status[1] : '';
  return first === '?' || second === '?' ? '?' : first || second;
}

function getStatusClass(status: string): string {
  const primary = getPrimaryStatus(status);
  if (primary === '?') {
    return 'text-warning';
  }
  if (primary === 'D' || primary === 'U') {
    return 'text-danger';
  }
  if (primary === 'A' || primary === 'R' || primary === 'C') {
    return 'text-success';
  }
  return primary === 'M' ? 'text-warning' : 'text-tertiary';
}

function isStagedFile(file: GitFile): boolean {
  return Boolean(file.status?.[0] && file.status[0] !== ' ' && file.status[0] !== '?');
}

function isUnstagedFile(file: GitFile): boolean {
  return file.status === '??' || Boolean(file.status?.[1] && file.status[1] !== ' ');
}

function getGitCommitActionMode(state: GitRepoState, push: boolean): GitCommitActionMode {
  if (push && (!state.hasRemote || !state.upstreamName)) {
    return 'disabled';
  }
  if (state.files.some(isStagedFile)) {
    return 'staged';
  }
  if (state.files.some(isUnstagedFile)) {
    return 'autostage';
  }
  return 'disabled';
}

function getGitRemoteSyncTip(state: GitRepoState): string {
  const upstream = state.upstreamName ? ` (${state.upstreamName})` : '';
  if (state.remoteSyncStatus === 'synced') {
    return `${translate('本地与远端一致')}${upstream}`;
  }
  if (state.remoteSyncStatus === 'diverged') {
    return `${translate('本地与远端不一致')}${upstream}`;
  }
  if (state.remoteSyncStatus === 'no-upstream') {
    return translate('当前分支未关联远端分支');
  }
  return translate('当前仓库不存在远端');
}

function getCandidateLabel(candidate: GitTerminalCandidate): string {
  if (candidate.current) {
    return translate('当前终端');
  }
  return `${translate('终端')} ${candidate.sessionId.slice(-8)}`;
}

function joinRemoteRepositoryPath(repositoryPath: string, filePath: string): string {
  return `${repositoryPath.replace(/\/+$/, '')}/${filePath.replace(/^\/+/, '')}`;
}

function quoteGitCommandArgument(value: string): string {
  return `'${String(value || '').replace(/'/g, `'\\''`)}'`;
}

function isGitRepositoryError(error: unknown): boolean {
  const message = String(error || '').toLowerCase();
  return message.includes('not a git repository') || message.includes('not a git directory');
}

function buildGitDiffCommand(repoPath: string, type: 'staged' | 'unstaged' | 'commit', filePaths: string[], hash = ''): string {
  const args = type === 'commit'
    ? ['show', '--format=fuller', '--patch', '--find-renames', '--find-copies', '--binary', hash]
    : type === 'staged'
      ? ['diff', '--cached', '--patch', '--find-renames', '--find-copies', '--binary']
      : ['diff', '--patch', '--find-renames', '--find-copies', '--binary'];
  if (filePaths.length > 0) {
    args.push('--', ...filePaths);
  }
  return `git -C ${quoteGitCommandArgument(repoPath)} -c core.quotePath=false ${args.map(quoteGitCommandArgument).join(' ')}`;
}

function GitRepositoryPanel({
  serverId,
  sessionId,
  isConnected,
  activeSubTab = 'git',
  onActiveSubTabChange,
}: {
  serverId: string;
  sessionId: string;
  isConnected: boolean;
  activeSubTab?: string;
  onActiveSubTabChange?: (subTab: string) => void;
}) {
  const normalizedServerId = String(serverId || sessionId || '').trim();
  const reviewTerminalId = String(sessionId || '').trim();
  const [repositoryPaths, setRepositoryPaths] = useState(() => readRepositoryPaths(normalizedServerId));
  const [repositoryInput, setRepositoryInput] = useState('');
  const [repoStates, setRepoStates] = useState<Record<string, GitRepoState>>({});
  const [autoRefreshIntervals, setAutoRefreshIntervals] = useState<Record<string, string>>(() => Object.fromEntries(repositoryPaths.map((repoPath) => [repoPath, readGitAutoRefreshInterval(normalizedServerId, repoPath)])));
  const repoStatesRef = useRef<Record<string, GitRepoState>>({});
  repoStatesRef.current = repoStates;
  const gitRowEffectTimersRef = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map());
  const [terminalCandidates, setTerminalCandidates] = useState<GitTerminalCandidate[]>([]);
  const [selectedTerminalId, setSelectedTerminalId] = useState(sessionId);
  const [terminalPickerOpen, setTerminalPickerOpen] = useState(false);
  const [terminalLoading, setTerminalLoading] = useState(false);
  const [diffLoadingKey, setDiffLoadingKey] = useState('');
  const diffLoadingKeyRef = useRef('');
  diffLoadingKeyRef.current = diffLoadingKey;
  const [interactiveBusy, setInteractiveBusy] = useState(false);
  const interactiveBusyRef = useRef(false);
  const diffFileCacheRef = useRef<Map<string, GitFileContentCacheEntry>>(new Map());
  const gitDrawerScrollerRefs = useRef<Record<string, HTMLDivElement | null>>({});
  const gitDrawerPendingAnchorsRef = useRef<Record<string, { rowKey: string; offset: number; scrollTop: number }>>({});
  const [selectionAnchors, setSelectionAnchors] = useState({ staged: -1, unstaged: -1 });
  const [copiedRepositoryPath, setCopiedRepositoryPath] = useState('');
  const copiedRepositoryPathTimerRef = useRef(0);

  useEffect(() => () => {
    if (copiedRepositoryPathTimerRef.current) {
      window.clearTimeout(copiedRepositoryPathTimerRef.current);
    }
  }, []);

  const persistRepositoryPaths = useCallback((paths: string[]) => {
    const nextPaths = normalizeRepositoryPaths(paths);
    setRepositoryPaths(nextPaths);
    if (typeof window !== 'undefined' && normalizedServerId) {
      window.localStorage.setItem(`${REPOSITORY_STORAGE_PREFIX}${normalizedServerId}`, JSON.stringify(nextPaths));
    }
  }, [normalizedServerId]);

  useEffect(() => {
    const nextPaths = readRepositoryPaths(normalizedServerId);
    setRepositoryPaths(nextPaths);
    setRepoStates({});
    setTerminalCandidates([]);
    setTerminalPickerOpen(false);
    setSelectionAnchors({ staged: -1, unstaged: -1 });
    return () => {
      diffFileCacheRef.current.clear();
    };
  }, [normalizedServerId, sessionId]);

  useEffect(() => {
    if (isConnected) {
      return;
    }
    diffFileCacheRef.current.clear();
    setRepoStates((previous) => Object.fromEntries(Object.entries(previous).map(([repoPath, state]) => [
      repoPath,
      { ...state, loading: false, loaded: false },
    ])));
  }, [isConnected]);

  const updateRepoState = useCallback((repoPath: string, patch: Partial<GitRepoState>) => {
    setRepoStates((previous) => ({
      ...previous,
      [repoPath]: {
        ...createEmptyRepoState(),
        ...(previous[repoPath] || {}),
        ...patch,
      },
    }));
  }, []);

  const invokeGit = useCallback(async (targetSessionId: string, repoPath: string, args: string[], interactive: boolean): Promise<GitResult> => {
    const result = await AppGo.ExecuteGitCommand(targetSessionId, repoPath, args, interactive);
    return (result || {}) as GitResult;
  }, []);

  const readGitCommitMessage = useCallback(async (repoPath: string) => {
    for (const scope of ['local', 'global']) {
      const configResult = await invokeGit(sessionId, repoPath, ['config', `--${scope}`, '--show-origin', '--path', '--get', 'commit.template'], false);
      if (configResult.success !== true) {
        continue;
      }
      const { origin, template } = parseGitConfigTemplateOutput(configResult.output);
      const templatePath = resolveRemoteGitTemplatePath(template, origin, repoPath);
      if (!templatePath) {
        continue;
      }
      try {
        const content = String(await AppGo.ReadFile(sessionId, templatePath) || '').trim();
        if (content) {
          return content;
        }
      } catch {}
    }
    return '';
  }, [invokeGit, sessionId]);

  const clearGitRowEffectTimer = useCallback((repoPath: string, rowKey: string) => {
    const timerKey = `${repoPath}::${rowKey}`;
    const timer = gitRowEffectTimersRef.current.get(timerKey);
    if (timer) {
      window.clearTimeout(timer);
      gitRowEffectTimersRef.current.delete(timerKey);
    }
  }, []);

  const cleanupGitRowEffect = useCallback((repoPath: string, cleanup: GitRowCleanup) => {
    setRepoStates((previous) => {
      const state = previous[repoPath];
      if (!state) {
        return previous;
      }
      const nextEffects = { ...state.rowEffects };
      delete nextEffects[cleanup.rowKey];
      const nextState: GitRepoState = {
        ...state,
        rowEffects: nextEffects,
      };
      if (cleanup.removePlaceholder) {
        if (cleanup.drawer === 'staged') {
          nextState.stagedRows = state.stagedRows.filter((item) => item.rowKey !== cleanup.rowKey);
        } else if (cleanup.drawer === 'unstaged') {
          nextState.unstagedRows = state.unstagedRows.filter((item) => item.rowKey !== cleanup.rowKey);
        } else {
          nextState.logRows = state.logRows.filter((item) => item.rowKey !== cleanup.rowKey);
        }
      }
      return { ...previous, [repoPath]: nextState };
    });
    gitRowEffectTimersRef.current.delete(`${repoPath}::${cleanup.rowKey}`);
  }, []);

  const scheduleGitVisualDiffCleanup = useCallback((repoPath: string, visualDiff: GitVisualDiff) => {
    visualDiff.cleanup.forEach((cleanup) => {
      clearGitRowEffectTimer(repoPath, cleanup.rowKey);
      const timerKey = `${repoPath}::${cleanup.rowKey}`;
      const timer = window.setTimeout(() => cleanupGitRowEffect(repoPath, cleanup), GIT_ROW_EFFECT_DURATION_MS);
      gitRowEffectTimersRef.current.set(timerKey, timer);
    });
  }, [clearGitRowEffectTimer, cleanupGitRowEffect]);

  useEffect(() => () => {
    gitRowEffectTimersRef.current.forEach((timer) => window.clearTimeout(timer));
    gitRowEffectTimersRef.current.clear();
  }, []);

  const captureGitDrawerAnchors = useCallback((repoPath: string) => {
    (['staged', 'unstaged', 'logs'] as GitRowDrawer[]).forEach((drawer) => {
      const key = `${repoPath}::${drawer}`;
      const listElement = gitDrawerScrollerRefs.current[key];
      if (!listElement) {
        return;
      }
      const viewportTop = listElement.getBoundingClientRect().top;
      const rows = Array.from(listElement.querySelectorAll<HTMLElement>('[data-git-row-key]'));
      const anchorRow = rows.find((row) => row.getBoundingClientRect().bottom > viewportTop + 1);
      const anchorRect = anchorRow?.getBoundingClientRect();
      gitDrawerPendingAnchorsRef.current[key] = {
        rowKey: anchorRow?.dataset.gitRowKey || '',
        offset: anchorRect ? anchorRect.top - viewportTop : 0,
        scrollTop: listElement.scrollTop,
      };
    });
  }, []);

  const restoreGitDrawerAnchors = useCallback((repoPath: string) => {
    window.requestAnimationFrame(() => {
      (['staged', 'unstaged', 'logs'] as GitRowDrawer[]).forEach((drawer) => {
        const key = `${repoPath}::${drawer}`;
        const pendingAnchor = gitDrawerPendingAnchorsRef.current[key];
        const listElement = gitDrawerScrollerRefs.current[key];
        if (!pendingAnchor || !listElement) {
          return;
        }
        const viewportTop = listElement.getBoundingClientRect().top;
        const rows = Array.from(listElement.querySelectorAll<HTMLElement>('[data-git-row-key]'));
        const anchorRow = rows.find((row) => row.dataset.gitRowKey === pendingAnchor.rowKey);
        if (anchorRow) {
          const delta = anchorRow.getBoundingClientRect().top - viewportTop - pendingAnchor.offset;
          if (delta !== 0) {
            listElement.scrollTop += delta;
          }
        } else {
          listElement.scrollTop = pendingAnchor.scrollTop;
        }
        delete gitDrawerPendingAnchorsRef.current[key];
      });
    });
  }, []);

  const loadTerminalCandidates = useCallback(async () => {
    if (!sessionId) {
      return [];
    }
    setTerminalLoading(true);
    try {
      const rawCandidates = await AppGo.ListGitTerminalCandidates(sessionId);
      const candidates = Array.isArray(rawCandidates)
        ? rawCandidates
          .filter((item): item is Record<string, unknown> => !!item && typeof item === 'object')
          .map((item) => ({
            sessionId: String(item.sessionId || '').trim(),
            busy: item.busy === true,
            cwd: String(item.cwd || '').trim(),
            current: item.current === true,
            recommended: item.recommended === true,
          }))
          .filter((item) => item.sessionId)
        : [];
      setTerminalCandidates(candidates);
      const recommended = candidates.find((item) => item.recommended && !item.busy)
        || candidates.find((item) => item.current && !item.busy)
        || candidates.find((item) => !item.busy);
      if (recommended) {
        setSelectedTerminalId((current) => {
          const selected = candidates.find((item) => item.sessionId === current);
          return selected && !selected.busy ? selected.sessionId : recommended.sessionId;
        });
      }
      return candidates;
    } catch {
      setTerminalCandidates([]);
      return [];
    } finally {
      setTerminalLoading(false);
    }
  }, [sessionId]);

  const resolveInteractiveTerminal = useCallback(async () => {
    const candidates = await loadTerminalCandidates();
    const selected = candidates.find((item) => item.sessionId === selectedTerminalId);
    if (selected && !selected.busy) {
      return selected.sessionId;
    }
    return candidates.find((item) => item.recommended && !item.busy)?.sessionId
      || candidates.find((item) => !item.busy)?.sessionId
      || '';
  }, [loadTerminalCandidates, selectedTerminalId]);

  const loadRepository = useCallback(async (repoPath: string, interactiveRefresh = false, refreshCommitMessage = false) => {
    captureGitDrawerAnchors(repoPath);
    updateRepoState(repoPath, { loading: true, logsLoadFailed: false, error: '' });
    try {
      if (interactiveRefresh) {
        const targetSessionId = await resolveInteractiveTerminal();
        if (!targetSessionId) {
          throw new Error(translate('没有可用的终端'));
        }
        const refreshResult = await invokeGit(targetSessionId, repoPath, ['status', '--short', '--untracked-files=all'], true);
        if (refreshResult.success !== true) {
          throw new Error(refreshResult.error || translate('刷新 Git 仓库失败'));
        }
      }
      const statusResult = await invokeGit(sessionId, repoPath, ['status', '--short', '--untracked-files=all'], false);
      if (statusResult.success !== true) {
        if (isGitRepositoryError(statusResult.error)) {
          updateRepoState(repoPath, {
            loading: false,
            loaded: true,
            isRepository: false,
            error: '',
            files: [],
            logs: [],
            stagedRows: [],
            unstagedRows: [],
            logRows: [],
            rowEffects: {},
            logsLoadFailed: false,
          });
          return;
        }
        throw new Error(statusResult.error || translate('加载 Git 状态失败'));
      }
      const branchResult = await invokeGit(sessionId, repoPath, ['branch', '--show-current'], false);
      const remoteResult = await invokeGit(sessionId, repoPath, ['remote'], false);
      const upstreamResult = await invokeGit(sessionId, repoPath, ['rev-parse', '--abbrev-ref', '--symbolic-full-name', '@{u}'], false);
      const headResult = await invokeGit(sessionId, repoPath, ['rev-parse', 'HEAD'], false);
      const upstreamHeadResult = await invokeGit(sessionId, repoPath, ['rev-parse', '@{u}'], false);
      let logsResult = await invokeGit(sessionId, repoPath, ['log', '--name-status', '--pretty=format:%H%x1f%h%x1f%an%x1f%ad%x1f%s', '--date=format:%Y-%m-%d %H:%M', '-n', '200'], false);
      if (logsResult.success !== true) {
        await new Promise<void>((resolve) => window.setTimeout(resolve, 160));
        logsResult = await invokeGit(sessionId, repoPath, ['log', '--name-status', '--pretty=format:%H%x1f%h%x1f%an%x1f%ad%x1f%s', '--date=format:%Y-%m-%d %H:%M', '-n', '200'], false);
      }
      const nextFiles = parseStatusOutput(String(statusResult.output || ''));
      const nextLogs = logsResult.success === true ? parseGitLogs(String(logsResult.output || '')) : [];
      const previousState = repoStatesRef.current[repoPath] || createEmptyRepoState();
      const visualDiff = previousState.loaded && previousState.isRepository === true
        ? buildGitVisualDiff(previousState, nextFiles, nextLogs, Date.now())
        : buildGitInitialVisualRows(nextFiles, nextLogs);
      const branchName = String(branchResult.output || '').trim();
      const upstreamName = upstreamResult.success === true ? String(upstreamResult.output || '').trim() : '';
      const headHash = headResult.success === true ? String(headResult.output || '').trim() : '';
      const upstreamHeadHash = upstreamHeadResult.success === true ? String(upstreamHeadResult.output || '').trim() : '';
      const hasRemote = Boolean(String(remoteResult.output || '').trim());
      const remoteSyncStatus = !hasRemote
        ? 'no-remote'
        : !upstreamName
          ? 'no-upstream'
          : headHash && upstreamHeadHash && headHash === upstreamHeadHash
            ? 'synced'
            : 'diverged';
      const commitMessage = refreshCommitMessage
        ? await readGitCommitMessage(repoPath)
        : previousState.commitMessage;
      updateRepoState(repoPath, {
        loading: false,
        loaded: true,
        isRepository: true,
        error: '',
        branchName,
        hasRemote,
        upstreamName,
        remoteSyncStatus,
        files: nextFiles,
        logs: nextLogs,
        stagedRows: visualDiff.stagedRows,
        unstagedRows: visualDiff.unstagedRows,
        logRows: visualDiff.logRows,
        rowEffects: visualDiff.rowEffects,
        commitMessage,
        logsLoadFailed: logsResult.success !== true,
      });
      restoreGitDrawerAnchors(repoPath);
      scheduleGitVisualDiffCleanup(repoPath, visualDiff);
    } catch (error) {
      updateRepoState(repoPath, {
        loading: false,
        loaded: true,
        error: error instanceof Error ? error.message : String(error || translate('加载失败')),
      });
    }
  }, [captureGitDrawerAnchors, invokeGit, readGitCommitMessage, resolveInteractiveTerminal, restoreGitDrawerAnchors, scheduleGitVisualDiffCleanup, sessionId, updateRepoState]);

  useEffect(() => {
    if (!isConnected) {
      return;
    }
    repositoryPaths.forEach((repoPath) => {
      if (!repoStates[repoPath]?.loaded && !repoStates[repoPath]?.loading) {
        void loadRepository(repoPath, false, true);
      }
    });
  }, [isConnected, loadRepository, repoStates, repositoryPaths]);

  const refreshGitCommitMessage = useCallback(async (repoPath: string) => {
    const commitMessage = await readGitCommitMessage(repoPath);
    if (repoStatesRef.current[repoPath]) {
      updateRepoState(repoPath, { commitMessage });
    }
    return commitMessage;
  }, [readGitCommitMessage, updateRepoState]);

  const executeQuietSequence = useCallback(async (repoPath: string, commands: string[][]) => {
    const state = repoStates[repoPath] || createEmptyRepoState();
    if (state.busy || interactiveBusyRef.current || commands.length === 0) {
      return false;
    }
    interactiveBusyRef.current = true;
    setInteractiveBusy(true);
    updateRepoState(repoPath, { busy: true, error: '' });
    try {
      for (const args of commands) {
        const result = await invokeGit(sessionId, repoPath, args, false);
        if (result.success !== true) {
          throw new Error(result.error || translate('操作失败'));
        }
      }
      await loadRepository(repoPath);
      return true;
    } catch (error) {
      updateRepoState(repoPath, {
        busy: false,
        error: error instanceof Error ? error.message : String(error || translate('操作失败')),
      });
      return false;
    } finally {
      updateRepoState(repoPath, { busy: false });
      interactiveBusyRef.current = false;
      setInteractiveBusy(false);
    }
  }, [invokeGit, loadRepository, repoStates, sessionId, updateRepoState]);

  const executeInteractiveSequence = useCallback(async (repoPath: string, commands: string[][]) => {
    const state = repoStates[repoPath] || createEmptyRepoState();
    if (state.busy || interactiveBusyRef.current || commands.length === 0) {
      return false;
    }
    interactiveBusyRef.current = true;
    setInteractiveBusy(true);
    const targetSessionId = await resolveInteractiveTerminal();
    if (!targetSessionId) {
      updateRepoState(repoPath, { error: translate('没有可用的终端') });
      interactiveBusyRef.current = false;
      setInteractiveBusy(false);
      return false;
    }
    updateRepoState(repoPath, { busy: true, error: '' });
    try {
      for (const args of commands) {
        const result = await invokeGit(targetSessionId, repoPath, args, true);
        if (result.success !== true) {
          throw new Error(result.error || translate('操作失败'));
        }
      }
      await loadRepository(repoPath);
      return true;
    } catch (error) {
      updateRepoState(repoPath, {
        busy: false,
        error: error instanceof Error ? error.message : String(error || translate('操作失败')),
      });
      return false;
    } finally {
      updateRepoState(repoPath, { busy: false });
      interactiveBusyRef.current = false;
      setInteractiveBusy(false);
    }
  }, [invokeGit, loadRepository, repoStates, resolveInteractiveTerminal, updateRepoState]);

  const confirmDangerousAction = useCallback(async (key: keyof typeof CONFIRM_KEYS, message: string) => {
    if (typeof window !== 'undefined' && window.localStorage.getItem(CONFIRM_KEYS[key]) === 'true') {
      return true;
    }
    if (typeof window === 'undefined' || typeof window.lumeDialog?.confirm !== 'function') {
      return window.confirm(message);
    }
    const result = await window.lumeDialog.confirm(message, translate('操作确认'), translate('不再询问'));
    const confirmed = result === true || (typeof result === 'object' && result !== null && result.confirmed === true);
    if (confirmed && typeof result === 'object' && result.checked) {
      window.localStorage.setItem(CONFIRM_KEYS[key], 'true');
    }
    return confirmed;
  }, []);

  const handleAddRepository = useCallback(() => {
    const nextPath = repositoryInput.trim();
    if (!nextPath || repositoryPaths.includes(nextPath)) {
      return;
    }
    persistRepositoryPaths([...repositoryPaths, nextPath]);
    setAutoRefreshIntervals((current) => ({
      ...current,
      [nextPath]: readGitAutoRefreshInterval(normalizedServerId, nextPath),
    }));
    setRepositoryInput('');
    void loadRepository(nextPath, false, true);
  }, [loadRepository, normalizedServerId, persistRepositoryPaths, repositoryInput, repositoryPaths]);

  const handleRemoveRepository = useCallback((repoPath: string) => {
    persistRepositoryPaths(repositoryPaths.filter((item) => item !== repoPath));
    setRepoStates((previous) => {
      const next = { ...previous };
      delete next[repoPath];
      return next;
    });
  }, [persistRepositoryPaths, repositoryPaths]);

  const handleFileSelection = useCallback((
    repoPath: string,
    type: 'staged' | 'unstaged',
    items: GitFile[],
    index: number,
    event: { ctrlKey: boolean; metaKey: boolean; shiftKey: boolean },
  ) => {
    const target = items[index];
    if (!target) {
      return;
    }
    const state = repoStates[repoPath] || createEmptyRepoState();
    const selectedKey = type === 'staged' ? 'selectedStaged' : 'selectedUnstaged';
    const previous = state[selectedKey];
    const useMultiSelect = event.ctrlKey || event.metaKey;
    const anchor = selectionAnchors[type] >= 0 ? selectionAnchors[type] : index;
    let nextSelection: string[];
    if (event.shiftKey) {
      const start = Math.min(anchor, index);
      const end = Math.max(anchor, index);
      const range = items.slice(start, end + 1).map((item) => item.file);
      nextSelection = useMultiSelect
        ? Array.from(new Set([...previous, ...range]))
        : range;
    } else if (useMultiSelect) {
      nextSelection = previous.includes(target.file)
        ? previous.filter((item) => item !== target.file)
        : [...previous, target.file];
    } else {
      nextSelection = [target.file];
    }
    updateRepoState(repoPath, { [selectedKey]: nextSelection });
    setSelectionAnchors((current) => ({
      ...current,
      [type]: event.shiftKey ? anchor : index,
    }));
  }, [repoStates, selectionAnchors, updateRepoState]);

  const getContextSelectedItems = useCallback((repoPath: string, type: 'staged' | 'unstaged', items: GitFile[], index: number) => {
    const target = items[index];
    if (!target) {
      return [];
    }
    const state = repoStates[repoPath] || createEmptyRepoState();
    const selectedKey = type === 'staged' ? 'selectedStaged' : 'selectedUnstaged';
    const selected = state[selectedKey];
    const selectedItems = selected.includes(target.file)
      ? items.filter((item) => selected.includes(item.file))
      : [target];
    if (!selected.includes(target.file)) {
      updateRepoState(repoPath, { [selectedKey]: [target.file] });
      setSelectionAnchors((current) => ({ ...current, [type]: index }));
    }
    return selectedItems;
  }, [repoStates, updateRepoState]);

  const getActionFiles = useCallback((repoPath: string, type: 'staged' | 'unstaged', items: GitFile[], ignoreSelection = false) => {
    if (ignoreSelection) {
      return Array.from(new Set(items.flatMap(getGitFilePaths)));
    }
    const state = repoStates[repoPath] || createEmptyRepoState();
    const selected = type === 'staged' ? state.selectedStaged : state.selectedUnstaged;
    const selectedItems = selected.length > 0
      ? items.filter((item) => selected.includes(item.file))
      : items;
    return Array.from(new Set(selectedItems.flatMap(getGitFilePaths)));
  }, [repoStates]);

  const handleStage = useCallback(async (repoPath: string, items: GitFile[], ignoreSelection = false) => {
    const files = getActionFiles(repoPath, 'unstaged', items, ignoreSelection);
    if (files.length > 0) {
      await executeQuietSequence(repoPath, [['add', '--', ...files]]);
    }
  }, [executeQuietSequence, getActionFiles]);

  const handleUnstage = useCallback(async (repoPath: string, items: GitFile[], ignoreSelection = false) => {
    const files = getActionFiles(repoPath, 'staged', items, ignoreSelection);
    if (files.length > 0) {
      await executeQuietSequence(repoPath, [['reset', 'HEAD', '--', ...files]]);
    }
  }, [executeQuietSequence, getActionFiles]);

  const handleDiscard = useCallback(async (repoPath: string, items: GitFile[], ignoreSelection = false) => {
    const files = getActionFiles(repoPath, 'unstaged', items, ignoreSelection);
    if (files.length === 0 || !(await confirmDangerousAction('discard', translate('确认放弃所选 Git 更改？此操作不可撤销。')))) {
      return;
    }
    const targetItems = items.filter((item) => getGitFilePaths(item).some((path) => files.includes(path)));
    const trackedFiles = targetItems.filter((item) => item.status !== '??').flatMap(getGitFilePaths);
    const untrackedFiles = targetItems.filter((item) => item.status === '??').flatMap(getGitFilePaths);
    const commands: string[][] = [];
    if (trackedFiles.length > 0) {
      commands.push(['checkout', '--', ...trackedFiles]);
    }
    if (untrackedFiles.length > 0) {
      commands.push(['clean', '-fd', '--', ...untrackedFiles]);
    }
    await executeQuietSequence(repoPath, commands);
  }, [confirmDangerousAction, executeQuietSequence, getActionFiles]);

  const handleUntrack = useCallback(async (repoPath: string, items: GitFile[], ignoreSelection = false) => {
    const files = getActionFiles(repoPath, 'unstaged', items, ignoreSelection).filter((file) => items.some((item) => item.status !== '??' && getGitFilePaths(item).includes(file)));
    if (files.length === 0 || !(await confirmDangerousAction('untrack', translate('确认取消跟踪所选文件？本地文件不会被删除。')))) {
      return;
    }
    await executeQuietSequence(repoPath, [['rm', '--cached', '--', ...files]]);
  }, [confirmDangerousAction, executeQuietSequence, getActionFiles]);

  const handleIgnoreFiles = useCallback(async (repoPath: string, items: GitFile[]) => {
    const filePaths = Array.from(new Set(items.flatMap(getGitFilePaths)));
    if (filePaths.length === 0 || interactiveBusyRef.current) {
      return;
    }
    interactiveBusyRef.current = true;
    setInteractiveBusy(true);
    updateRepoState(repoPath, { busy: true, error: '' });
    try {
      await AppGo.AddGitIgnoreEntries(sessionId, repoPath, filePaths);
      updateRepoState(repoPath, { selectedUnstaged: [] });
      await loadRepository(repoPath);
    } catch (error) {
      updateRepoState(repoPath, {
        error: error instanceof Error ? error.message : translate('操作失败'),
      });
    } finally {
      updateRepoState(repoPath, { busy: false });
      interactiveBusyRef.current = false;
      setInteractiveBusy(false);
    }
  }, [loadRepository, sessionId, updateRepoState]);

  const handleCommit = useCallback(async (repoPath: string, amend = false, push = false) => {
    const state = repoStates[repoPath] || createEmptyRepoState();
    const mode = getGitCommitActionMode(state, push);
    if (mode === 'disabled') {
      return;
    }
    const message = state.commitMessage.trim();
    if (!message) {
      await window.lumeDialog?.alert?.(translate('请输入提交消息'));
      return;
    }
    const commands: string[][] = [];
    if (mode === 'autostage') {
      const unstagedFiles = Array.from(new Set(state.files.filter(isUnstagedFile).flatMap(getGitFilePaths)));
      if (unstagedFiles.length > 0) {
        commands.push(['add', '--', ...unstagedFiles]);
      }
    }
    commands.push(amend ? ['commit', '--amend', '-m', message] : ['commit', '-m', message]);
    if (push) {
      commands.push(['push']);
    }
    const success = await executeInteractiveSequence(repoPath, commands);
    if (success) {
      await refreshGitCommitMessage(repoPath);
    }
  }, [executeInteractiveSequence, refreshGitCommitMessage, repoStates]);

  const handleToggleAmendMessage = useCallback(async (repoPath: string) => {
    const state = repoStates[repoPath] || createEmptyRepoState();
    if (state.busy || interactiveBusyRef.current) {
      return;
    }
    interactiveBusyRef.current = true;
    setInteractiveBusy(true);
    updateRepoState(repoPath, { busy: true, error: '' });
    try {
      const result = await invokeGit(sessionId, repoPath, ['log', '-1', '--pretty=%B'], false);
      if (result.success !== true) {
        throw new Error(result.error || translate('操作失败'));
      }
      const lastMessage = String(result.output || '').trim();
      updateRepoState(repoPath, {
        commitMessage: state.commitMessage.trim() === lastMessage ? '' : lastMessage,
      });
    } catch (error) {
      updateRepoState(repoPath, {
        error: error instanceof Error ? error.message : translate('操作失败'),
      });
    } finally {
      updateRepoState(repoPath, { busy: false });
      interactiveBusyRef.current = false;
      setInteractiveBusy(false);
    }
  }, [invokeGit, repoStates, sessionId, updateRepoState]);

  const handleInitializeRepository = useCallback(async (repoPath: string) => {
    await executeQuietSequence(repoPath, [['init']]);
  }, [executeQuietSequence]);

  const handleCreatePatchBranch = useCallback(async (repoPath: string) => {
    const state = repoStates[repoPath] || createEmptyRepoState();
    if (state.branchName === 'patch') {
      return;
    }
    const existingBranch = await invokeGit(sessionId, repoPath, ['rev-parse', '--verify', 'refs/heads/patch'], false);
    if (existingBranch.success === true) {
      updateRepoState(repoPath, { error: translate('patch 分支已存在') });
      return;
    }
    await executeInteractiveSequence(repoPath, [['checkout', '-b', 'patch']]);
  }, [executeInteractiveSequence, invokeGit, repoStates, sessionId, updateRepoState]);

  const handleForcePush = useCallback(async (repoPath: string) => {
    if (await confirmDangerousAction('forcePush', translate('确认强制推送当前分支？远端历史可能被覆盖。'))) {
      await executeInteractiveSequence(repoPath, [['push', '--force']]);
    }
  }, [confirmDangerousAction, executeInteractiveSequence]);

  const handleRefresh = useCallback(async (repoPath: string) => {
    if (interactiveBusyRef.current || diffLoadingKey) {
      return;
    }
    interactiveBusyRef.current = true;
    setInteractiveBusy(true);
    try {
      await loadRepository(repoPath);
    } finally {
      interactiveBusyRef.current = false;
      setInteractiveBusy(false);
    }
  }, [diffLoadingKey, loadRepository]);

  const handleGitAutoRefreshIntervalChange = useCallback((repoPath: string, value: string) => {
    const normalizedValue = String(value || '').replace(/[^\d]/g, '');
    setAutoRefreshIntervals((current) => ({ ...current, [repoPath]: normalizedValue || '0' }));
    writeGitAutoRefreshInterval(normalizedServerId, repoPath, normalizedValue || '0');
  }, [normalizedServerId]);

  useEffect(() => {
    if (!isConnected) {
      return undefined;
    }
    let cancelled = false;
    const timers = new Set<ReturnType<typeof setTimeout>>();
    const schedule = (repoPath: string) => {
      const intervalSeconds = Number(autoRefreshIntervals[repoPath] || 0);
      if (!Number.isFinite(intervalSeconds) || intervalSeconds <= 0) {
        return;
      }
      const timer = window.setTimeout(async () => {
        timers.delete(timer);
        if (cancelled) {
          return;
        }
        if (
          !interactiveBusyRef.current
          && !diffLoadingKeyRef.current
          && !repoStatesRef.current[repoPath]?.busy
          && repoStatesRef.current[repoPath]?.loaded
        ) {
          await loadRepository(repoPath);
        }
        if (!cancelled) {
          schedule(repoPath);
        }
      }, Math.max(1, Math.floor(intervalSeconds)) * 1000);
      timers.add(timer);
    };
    repositoryPaths.forEach(schedule);
    return () => {
      cancelled = true;
      timers.forEach((timer) => window.clearTimeout(timer));
      timers.clear();
    };
  }, [autoRefreshIntervals, isConnected, loadRepository, repositoryPaths]);

  const readLatestGitFileContent = useCallback(async (repoPath: string, filePath: string, status: string) => {
    if (getPrimaryStatus(status) === 'D') {
      return '';
    }
    const cacheKey = `${repoPath}::${filePath}`;
    let mtime = Number.NaN;
    let size = Number.NaN;
    try {
      const metadata = await AppGo.GetGitFileModTime(reviewTerminalId, repoPath, filePath) as { mtime?: unknown; size?: unknown };
      mtime = Number(metadata?.mtime);
      size = Number(metadata?.size);
      const cached = diffFileCacheRef.current.get(cacheKey);
      if (cached && cached.mtime === mtime && cached.size === size) {
        return cached.content;
      }
    } catch {}
    const content = String(await AppGo.ReadFile(reviewTerminalId, joinRemoteRepositoryPath(repoPath, filePath)));
    if (Number.isFinite(mtime) && Number.isFinite(size)) {
      diffFileCacheRef.current.set(cacheKey, { mtime, size, content });
    }
    return content;
  }, [reviewTerminalId]);

  const handleOpenDiff = useCallback(async (repoPath: string, file: GitFile, staged: boolean) => {
    if (diffLoadingKey || interactiveBusyRef.current) {
      return;
    }
    const filePaths = getGitFilePaths(file);
    const targetPath = filePaths[filePaths.length - 1] || '';
    if (!targetPath) {
      return;
    }
    const loadingKey = `${repoPath}::${file.file}`;
    setDiffLoadingKey(loadingKey);
    try {
      const latestContent = await readLatestGitFileContent(repoPath, targetPath, file.status);
      const isNewFile = file.status === '??' || (staged && file.status?.[0] === 'A');
      const baseArgs = isNewFile
        ? null
        : staged
          ? ['show', `HEAD:${targetPath}`]
          : ['show', `:${targetPath}`];
      let beforeContent = '';
      if (baseArgs) {
        const baseResult = await invokeGit(reviewTerminalId, repoPath, baseArgs, false);
        if (baseResult.success !== true) {
          throw new Error(baseResult.error || translate('差异预览失败'));
        }
        beforeContent = String(baseResult.output || '');
      }
      const reviewId = `git-review-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
      window.dispatchEvent(new CustomEvent('git-change-review-required', {
        detail: {
          review: {
            reviewId,
            requestId: reviewId,
            sessionId: reviewTerminalId,
            source: 'git',
            path: targetPath,
            toolName: 'Git',
            isNewFile,
            blocks: [{
              before: beforeContent,
              after: String(latestContent || ''),
              startLine: 1,
              matchedStartLine: 1,
            }],
          },
          terminalId: reviewTerminalId,
        },
      }));
      updateRepoState(repoPath, { error: '' });
    } catch (error) {
      updateRepoState(repoPath, {
        error: error instanceof Error ? error.message : translate('差异预览失败'),
      });
    } finally {
      setDiffLoadingKey('');
    }
  }, [diffLoadingKey, invokeGit, interactiveBusy, readLatestGitFileContent, reviewTerminalId, updateRepoState]);

  const handleCopy = useCallback(async (value: string) => {
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      await window.lumeDialog?.alert?.(translate('复制失败'));
    }
  }, []);

  const handleCopyRepositoryPath = useCallback(async (repoPath: string) => {
    try {
      await navigator.clipboard.writeText(repoPath);
      setCopiedRepositoryPath(repoPath);
      if (copiedRepositoryPathTimerRef.current) {
        window.clearTimeout(copiedRepositoryPathTimerRef.current);
      }
      copiedRepositoryPathTimerRef.current = window.setTimeout(() => {
        copiedRepositoryPathTimerRef.current = 0;
        setCopiedRepositoryPath((current) => current === repoPath ? '' : current);
      }, 2000);
    } catch {}
  }, []);

  const handleCopyGitDiff = useCallback(async (repoPath: string, type: 'staged' | 'unstaged' | 'commit', items: GitFile[], hash = '') => {
    const filePaths = Array.from(new Set(items.flatMap(getGitFilePaths)));
    const args = type === 'commit'
      ? ['show', '--format=fuller', '--patch', '--find-renames', '--find-copies', '--binary', hash, ...(filePaths.length > 0 ? ['--', ...filePaths] : [])]
      : type === 'staged'
        ? ['diff', '--cached', '--patch', '--find-renames', '--find-copies', '--binary', ...(filePaths.length > 0 ? ['--', ...filePaths] : [])]
        : ['diff', '--patch', '--find-renames', '--find-copies', '--binary', ...(filePaths.length > 0 ? ['--', ...filePaths] : [])];
    const result = await invokeGit(sessionId, repoPath, args, false);
    if (result.success !== true) {
      updateRepoState(repoPath, { error: result.error || translate('操作失败') });
      return;
    }
    await handleCopy(String(result.output || ''));
  }, [handleCopy, invokeGit, sessionId, updateRepoState]);

  const openGitFileContextMenu = useCallback((
    event: { preventDefault: () => void; stopPropagation: () => void; clientX: number; clientY: number },
    repoPath: string,
    type: 'staged' | 'unstaged',
    items: GitFile[],
    index: number,
  ) => {
    event.preventDefault();
    event.stopPropagation();
    const selectedItems = getContextSelectedItems(repoPath, type, items, index);
    if (selectedItems.length === 0) {
      return;
    }
    const menuItems: GlobalContextMenuItem[] = type === 'staged'
      ? [
          { key: 'unstage', label: translate('取消暂存'), onSelect: () => { void handleUnstage(repoPath, selectedItems); } },
          { type: 'divider', key: 'divider' },
          { key: 'copy-diff', label: translate('复制完整 Git Diff'), onSelect: () => { void handleCopyGitDiff(repoPath, 'staged', selectedItems); } },
          { key: 'copy-command', label: translate('复制完整 Git Diff(命令)'), onSelect: () => { void handleCopy(buildGitDiffCommand(repoPath, 'staged', Array.from(new Set(selectedItems.flatMap(getGitFilePaths))))); } },
        ]
      : [
          { key: 'stage', label: translate('暂存更改'), onSelect: () => { void handleStage(repoPath, selectedItems); } },
          { key: 'discard', label: translate('放弃更改'), danger: true, onSelect: () => { void handleDiscard(repoPath, selectedItems); } },
          { type: 'divider', key: 'divider-actions' },
          { key: 'ignore', label: translate('添加到忽略项'), onSelect: () => { void handleIgnoreFiles(repoPath, selectedItems); } },
          { key: 'untrack', label: translate('不再跟踪此文件'), danger: true, onSelect: () => { void handleUntrack(repoPath, selectedItems); } },
          { type: 'divider', key: 'divider-copy' },
          { key: 'copy-diff', label: translate('复制完整 Git Diff'), onSelect: () => { void handleCopyGitDiff(repoPath, 'unstaged', selectedItems); } },
          { key: 'copy-command', label: translate('复制完整 Git Diff(命令)'), onSelect: () => { void handleCopy(buildGitDiffCommand(repoPath, 'unstaged', Array.from(new Set(selectedItems.flatMap(getGitFilePaths))))); } },
        ];
    openGlobalContextMenu({
      x: event.clientX,
      y: event.clientY,
      items: menuItems,
    });
  }, [getContextSelectedItems, handleCopy, handleCopyGitDiff, handleDiscard, handleIgnoreFiles, handleStage, handleUnstage, handleUntrack]);

  const openGitDrawerContextMenu = useCallback((
    event: { preventDefault: () => void; stopPropagation: () => void; clientX: number; clientY: number },
    repoPath: string,
    type: 'staged' | 'unstaged',
    items: GitFile[],
  ) => {
    event.preventDefault();
    event.stopPropagation();
    const filePaths = getActionFiles(repoPath, type, items);
    openGlobalContextMenu({
      x: event.clientX,
      y: event.clientY,
      items: [
        { key: 'copy-diff', label: translate('复制完整 Git Diff'), onSelect: () => { void handleCopyGitDiff(repoPath, type, items.filter((item) => filePaths.some((path) => getGitFilePaths(item).includes(path)))); } },
        { key: 'copy-command', label: translate('复制完整 Git Diff(命令)'), onSelect: () => { void handleCopy(buildGitDiffCommand(repoPath, type, filePaths)); } },
      ],
    });
  }, [getActionFiles, handleCopy, handleCopyGitDiff]);

  const openGitLogContextMenu = useCallback((
    event: { preventDefault: () => void; stopPropagation: () => void; clientX: number; clientY: number },
    repoPath: string,
    log: GitLog,
  ) => {
    const filePaths = Array.from(new Set(log.files.flatMap(getGitFilePaths)));
    event.preventDefault();
    event.stopPropagation();
    openGlobalContextMenu({
      x: event.clientX,
      y: event.clientY,
      items: [
        { key: 'copy-diff', label: translate('复制完整 Git Diff'), onSelect: () => { void handleCopyGitDiff(repoPath, 'commit', log.files, log.hash); } },
        { key: 'copy-command', label: translate('复制完整 Git Diff(命令)'), onSelect: () => { void handleCopy(buildGitDiffCommand(repoPath, 'commit', filePaths, log.hash)); } },
        { type: 'divider', key: 'divider-copy' },
        { key: 'copy-hash', label: translate('复制哈希'), onSelect: () => { void handleCopy(log.hash); } },
        { key: 'copy-message', label: translate('复制提交消息'), onSelect: () => { void handleCopy(log.subject); } },
      ],
    });
  }, [handleCopy, handleCopyGitDiff]);

  const openGitRepositoryContextMenu = useCallback((
    event: { preventDefault: () => void; stopPropagation: () => void; clientX: number; clientY: number },
    repoPath: string,
  ) => {
    event.preventDefault();
    event.stopPropagation();
    const name = repoPath.split('/').filter(Boolean).pop() || repoPath;
    openGlobalContextMenu({
      x: event.clientX,
      y: event.clientY,
      items: [
        { key: 'copy-name', label: translate('复制仓库名'), onSelect: () => { void handleCopy(name); } },
        { key: 'copy-path', label: translate('复制仓库路径'), onSelect: () => { void handleCopyRepositoryPath(repoPath); } },
      ],
    });
  }, [handleCopy, handleCopyRepositoryPath]);

  const renderFileList = (repoPath: string, type: 'staged' | 'unstaged', items: GitFile[]) => {
    const state = repoStates[repoPath] || createEmptyRepoState();
    const selected = type === 'staged' ? state.selectedStaged : state.selectedUnstaged;
    const controlsDisabled = Boolean(diffLoadingKey || interactiveBusy);
    if (items.length === 0) {
      return <div className="px-2 py-3 text-center text-xs text-tertiary">{translate('无内容')}</div>;
    }
    return items.map((file, index) => {
      const deletedPlaceholder = file.deletedPlaceholder === true;
      const rowEffect = state.rowEffects[file.rowKey || ''];
      const rowInteractive = !controlsDisabled && !deletedPlaceholder;
      return (
        <div
          key={file.rowKey || `${type}-${file.status}-${file.file}`}
          className={`git-repository-file-row group flex min-h-[36px] min-w-0 items-center gap-2 border-b border-line-subtle px-2 py-1.5 text-xs ${selected.includes(file.file) ? 'bg-accent-dim' : 'hover:bg-hover'}${deletedPlaceholder ? ' git-repository-deleted-placeholder' : ''}${rowEffect ? ` git-repository-visual-effect git-repository-visual-effect-${rowEffect.effect}` : ''}`}
          style={getGitRowAnimationStyle(rowEffect)}
          onClick={(event) => {
            if (rowInteractive) {
              handleFileSelection(repoPath, type, items, index, event);
            }
          }}
          onContextMenu={(event) => {
            if (rowInteractive) {
              openGitFileContextMenu(event, repoPath, type, items, index);
            }
          }}
          onDoubleClick={() => {
            if (rowInteractive) {
              void handleOpenDiff(repoPath, file, type === 'staged');
            }
          }}
          data-git-row-key={file.rowKey || `${type}:${file.file}`}
        >
          <span className={`w-4 shrink-0 text-center font-mono font-bold ${getStatusClass(file.status)}`}>{getPrimaryStatus(file.status)}</span>
          <Tiptop text={file.file} className="min-w-0 flex-1">
            <span className="block min-w-0 truncate text-primary">{file.file}</span>
          </Tiptop>
          {!deletedPlaceholder ? (
            <Tiptop text={translate('打开差异')}>
              <button
                type="button"
                aria-label={translate('打开差异')}
                disabled={!rowInteractive}
                className="hidden h-6 w-6 shrink-0 items-center justify-center rounded text-muted hover:bg-hover hover:text-primary group-hover:inline-flex disabled:cursor-not-allowed disabled:opacity-45"
                onClick={(event) => {
                  event.stopPropagation();
                  void handleOpenDiff(repoPath, file, type === 'staged');
                }}
              >
                {diffLoadingKey === `${repoPath}::${file.file}` ? <RefreshCw size={13} className="animate-spin" /> : <GitCommit size={13} />}
              </button>
            </Tiptop>
          ) : null}
          {type === 'unstaged' && !deletedPlaceholder ? (
            <>
              <Tiptop text={translate('放弃更改')}>
                <button
                  type="button"
                  aria-label={translate('放弃更改')}
                  disabled={!rowInteractive}
                  className="hidden h-6 w-6 shrink-0 items-center justify-center rounded text-danger hover:bg-hover group-hover:inline-flex disabled:cursor-not-allowed disabled:opacity-45"
                  onClick={(event) => {
                    event.stopPropagation();
                    void handleDiscard(repoPath, [file], true);
                  }}
                >
                  <X size={13} />
                </button>
              </Tiptop>
              <Tiptop text={translate('暂存更改')}>
                <button
                  type="button"
                  aria-label={translate('暂存更改')}
                  disabled={!rowInteractive}
                  className="hidden h-6 w-6 shrink-0 items-center justify-center rounded text-success hover:bg-hover group-hover:inline-flex disabled:cursor-not-allowed disabled:opacity-45"
                  onClick={(event) => {
                    event.stopPropagation();
                    void handleStage(repoPath, [file], true);
                  }}
                >
                  <Check size={13} />
                </button>
              </Tiptop>
              <Tiptop text={translate('取消跟踪')}>
                <button
                  type="button"
                  aria-label={translate('取消跟踪')}
                  disabled={!rowInteractive}
                  className="hidden h-6 w-6 shrink-0 items-center justify-center rounded text-warning hover:bg-hover group-hover:inline-flex disabled:cursor-not-allowed disabled:opacity-45"
                  onClick={(event) => {
                    event.stopPropagation();
                    void handleUntrack(repoPath, [file], true);
                  }}
                >
                  <Trash2 size={13} />
                </button>
              </Tiptop>
            </>
          ) : null}
        </div>
      );
    });
  };

  const renderRepository = (repoPath: string) => {
    const state = repoStates[repoPath] || createEmptyRepoState();
    const staged = state.stagedRows;
    const unstaged = state.unstagedRows;
    const stagedItems = state.files.filter(isStagedFile);
    const unstagedItems = state.files.filter(isUnstagedFile);
    const selectedCandidate = terminalCandidates.find((item) => item.sessionId === selectedTerminalId);
    const controlsDisabled = Boolean(diffLoadingKey || interactiveBusy);
    const commitMode = getGitCommitActionMode(state, false);
    const commitPushMode = getGitCommitActionMode(state, true);
    return (
      <div key={repoPath} className="git-repository-card">
        <div className="git-repository-header" onContextMenu={(event) => openGitRepositoryContextMenu(event, repoPath)}>
          <div className="git-repository-header-main">
            <GitBranch size={14} className="shrink-0 text-accent" />
            <div className="min-w-0 flex-1">
              <div className="flex min-w-0 items-center gap-2">
                <span className="min-w-0 truncate text-sm font-bold text-primary">{repoPath.split('/').filter(Boolean).pop() || repoPath}</span>
                {state.branchName ? <span className="git-repository-branch">{state.branchName}</span> : null}
              </div>
              <Tiptop text={copiedRepositoryPath === repoPath ? translate('已复制!') : repoPath} className="min-w-0">
                <button type="button" className="git-repository-path-button" onClick={() => void handleCopyRepositoryPath(repoPath)}>
                  {copiedRepositoryPath === repoPath ? translate('已复制!') : repoPath}
                </button>
              </Tiptop>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-1">
            {state.branchName && state.branchName !== 'patch' ? (
              <Tiptop text={translate('创建 patch 分支')}>
                <button type="button" aria-label={translate('创建 patch 分支')} className="git-repository-icon-button text-accent" disabled={controlsDisabled || state.busy} onClick={() => void handleCreatePatchBranch(repoPath)}><GitBranch size={14} /></button>
              </Tiptop>
            ) : null}
            <Tiptop text={translate('强制推送')}>
              <button type="button" aria-label={translate('强制推送')} className="git-repository-icon-button text-warning" disabled={controlsDisabled || state.busy || !state.hasRemote} onClick={() => void handleForcePush(repoPath)}><Upload size={14} /></button>
            </Tiptop>
            <Tiptop text={translate('刷新')}>
              <button type="button" aria-label={translate('刷新')} className="git-repository-icon-button" disabled={controlsDisabled || state.busy} onClick={() => void handleRefresh(repoPath)}><RefreshCw size={14} className={state.loading ? 'animate-spin' : ''} /></button>
            </Tiptop>
            <Tiptop text={translate('移除仓库')}>
              <button type="button" aria-label={translate('移除仓库')} className="git-repository-icon-button text-danger" disabled={controlsDisabled || state.busy} onClick={() => handleRemoveRepository(repoPath)}><Trash2 size={14} /></button>
            </Tiptop>
          </div>
        </div>
        {state.error ? <div className="git-repository-error">{state.error}</div> : null}
        {state.loading && !state.loaded ? (
          <div className="git-repository-loading"><RefreshCw size={15} className="animate-spin" />{translate('加载中...')}</div>
        ) : state.isRepository === false ? (
          <div className="git-repository-uninitialized">
            <button type="button" disabled={controlsDisabled || state.busy} onClick={() => void handleInitializeRepository(repoPath)}>
              <GitBranch size={13} />
              {translate('初始化本地仓库')}
            </button>
          </div>
        ) : (
          <>
            <div className="git-repository-terminal-row">
              <Terminal size={13} className="shrink-0 text-tertiary" />
              <span className="truncate text-xs text-secondary">{translate('操作终端')}: {selectedCandidate ? getCandidateLabel(selectedCandidate) : translate('当前终端')}</span>
              <label className="git-repository-auto-refresh">
                <span>{translate('自动刷新')}</span>
                <input
                  type="number"
                  min="0"
                  step="1"
                  inputMode="numeric"
                  value={autoRefreshIntervals[repoPath] || '0'}
                  aria-label={translate('自动刷新')}
                  disabled={controlsDisabled || state.busy}
                  onChange={(event) => handleGitAutoRefreshIntervalChange(repoPath, event.target.value)}
                />
                <span>s</span>
              </label>
              <div className="relative ml-auto">
                <Tiptop text={translate('指派终端')}>
                  <button type="button" aria-label={translate('指派终端')} className="git-repository-terminal-button" disabled={controlsDisabled} onClick={() => { setTerminalPickerOpen((open) => !open); void loadTerminalCandidates(); }}><ChevronDown size={13} /></button>
                </Tiptop>
                {terminalPickerOpen ? (
                  <div className="git-repository-terminal-menu">
                    {terminalLoading ? <div className="git-repository-terminal-empty">{translate('正在加载终端...')}</div> : null}
                    {!terminalLoading && terminalCandidates.map((candidate) => (
                      <button
                        key={candidate.sessionId}
                        type="button"
                        className={`git-repository-terminal-option${candidate.sessionId === selectedTerminalId ? ' active' : ''}`}
                        disabled={candidate.busy}
                        onClick={() => {
                          setSelectedTerminalId(candidate.sessionId);
                          setTerminalPickerOpen(false);
                        }}
                      >
                        <Terminal size={12} />
                        <span className="min-w-0 flex-1 truncate">{getCandidateLabel(candidate)}</span>
                        {candidate.recommended ? <span className="git-repository-terminal-badge">{translate('推荐')}</span> : null}
                        <span className={candidate.busy ? 'text-warning' : 'text-success'}>{candidate.busy ? translate('忙碌') : translate('空闲')}</span>
                      </button>
                    ))}
                    {!terminalLoading && terminalCandidates.length === 0 ? <div className="git-repository-terminal-empty">{translate('没有可用的终端')}</div> : null}
                  </div>
                ) : null}
              </div>
            </div>
            <textarea
              className="git-repository-commit-message"
              value={state.commitMessage}
              onChange={(event) => updateRepoState(repoPath, { commitMessage: event.target.value })}
              placeholder={translate('请输入提交消息')}
              disabled={controlsDisabled || state.busy}
            />
            <div className="git-repository-commit-actions">
              <button type="button" className={`git-repository-commit-${commitMode}`} disabled={controlsDisabled || state.busy || commitMode === 'disabled'} onClick={() => void handleCommit(repoPath)}><GitCommit size={13} />{translate('提交')}</button>
              <button
                type="button"
                className={`git-repository-commit-${commitMode}`}
                disabled={controlsDisabled || state.busy || commitMode === 'disabled'}
                onClick={() => void handleCommit(repoPath, true)}
                onContextMenu={(event) => {
                  event.preventDefault();
                  void handleToggleAmendMessage(repoPath);
                }}
              >
                <GitCommit size={13} />{translate('提交(修改)')}
              </button>
              <button type="button" className={`git-repository-commit-${commitPushMode}`} disabled={controlsDisabled || state.busy || commitPushMode === 'disabled'} onClick={() => void handleCommit(repoPath, false, true)}><Send size={13} />{translate('提交并推送')}</button>
            </div>
            <div className="git-repository-drawer">
              <button type="button" className="git-repository-drawer-header" disabled={controlsDisabled} onClick={() => updateRepoState(repoPath, { stagedExpanded: !state.stagedExpanded })} onContextMenu={(event) => openGitDrawerContextMenu(event, repoPath, 'staged', stagedItems)}>
                {state.stagedExpanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
                <span>{translate('已暂存')}</span>
                <span className="git-repository-count">{stagedItems.length}</span>
                <span className="ml-auto" />
                <Tiptop text={translate('取消暂存所有')}>
                  <span role="button" tabIndex={0} className="git-repository-drawer-action" onClick={(event) => { event.stopPropagation(); if (!controlsDisabled) void handleUnstage(repoPath, stagedItems, true); }}><X size={13} /></span>
                </Tiptop>
              </button>
              {state.stagedExpanded ? <div className="git-repository-file-list" ref={(element) => { gitDrawerScrollerRefs.current[`${repoPath}::staged`] = element; }}>{renderFileList(repoPath, 'staged', staged)}</div> : null}
            </div>
            <div className="git-repository-drawer">
              <button type="button" className="git-repository-drawer-header" disabled={controlsDisabled} onClick={() => updateRepoState(repoPath, { unstagedExpanded: !state.unstagedExpanded })} onContextMenu={(event) => openGitDrawerContextMenu(event, repoPath, 'unstaged', unstagedItems)}>
                {state.unstagedExpanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
                <span>{translate('未暂存')}</span>
                <span className="git-repository-count">{unstagedItems.length}</span>
                <span className="ml-auto" />
                <Tiptop text={translate('还原所有')}>
                  <span role="button" tabIndex={0} className="git-repository-drawer-action text-danger" onClick={(event) => { event.stopPropagation(); if (!controlsDisabled) void handleDiscard(repoPath, unstagedItems, true); }}><X size={13} /></span>
                </Tiptop>
                <Tiptop text={translate('暂存所有')}>
                  <span role="button" tabIndex={0} className="git-repository-drawer-action text-success" onClick={(event) => { event.stopPropagation(); if (!controlsDisabled) void handleStage(repoPath, unstagedItems, true); }}><Check size={13} /></span>
                </Tiptop>
              </button>
              {state.unstagedExpanded ? <div className="git-repository-file-list" ref={(element) => { gitDrawerScrollerRefs.current[`${repoPath}::unstaged`] = element; }}>{renderFileList(repoPath, 'unstaged', unstaged)}</div> : null}
            </div>
            <div className={`git-repository-drawer git-repository-log-sync-${state.remoteSyncStatus}`}>
              <button type="button" className="git-repository-drawer-header" disabled={controlsDisabled} onClick={() => updateRepoState(repoPath, { logsExpanded: !state.logsExpanded })}>
                {state.logsExpanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
                <span>{translate('提交记录')}</span>
                <span className="git-repository-count">{state.logs.length}</span>
                <Tiptop text={getGitRemoteSyncTip(state)}>
                  <span className={`git-repository-log-sync-text git-repository-log-sync-text-${state.remoteSyncStatus}`}>
                    {state.remoteSyncStatus === 'synced'
                      ? translate('本地与远端一致')
                      : state.remoteSyncStatus === 'diverged'
                        ? translate('本地与远端不一致')
                        : state.remoteSyncStatus === 'no-upstream'
                          ? translate('未关联上游')
                          : translate('无远端')}
                  </span>
                </Tiptop>
              </button>
              {state.logsExpanded ? (
                <div className="git-repository-log-list" ref={(element) => { gitDrawerScrollerRefs.current[`${repoPath}::logs`] = element; }}>
                  {state.loading && state.logRows.length === 0 ? (
                    <div className="git-repository-loading"><RefreshCw size={13} className="animate-spin" />{translate('加载中...')}</div>
                  ) : state.logsLoadFailed ? (
                    <div className="px-2 py-3 text-center text-xs text-danger">{translate('加载提交记录失败')}</div>
                  ) : state.logRows.length === 0 ? (
                    <div className="px-2 py-3 text-center text-xs text-tertiary">{translate('暂无历史')}</div>
                  ) : state.logRows.map((log) => (
                    <div
                      key={log.rowKey || log.hash}
                      data-git-row-key={log.rowKey || `logs:${log.hash}`}
                      className={`git-repository-log-item${log.deletedPlaceholder ? ' git-repository-deleted-placeholder' : ''}${state.rowEffects[log.rowKey || ''] ? ` git-repository-visual-effect git-repository-visual-effect-${state.rowEffects[log.rowKey || ''].effect}` : ''}`}
                      style={getGitRowAnimationStyle(state.rowEffects[log.rowKey || ''])}
                    >
                      <button type="button" className="git-repository-log-main" disabled={controlsDisabled} onClick={() => updateRepoState(repoPath, { expandedLogHash: state.expandedLogHash === log.hash ? '' : log.hash })} onContextMenu={(event) => openGitLogContextMenu(event, repoPath, log)}>
                        <Tiptop text={translate('复制哈希')}>
                          <span
                            role="button"
                            tabIndex={0}
                            className="font-mono text-accent cursor-copy"
                            onClick={(event) => {
                              event.stopPropagation();
                              if (!controlsDisabled) void handleCopy(log.hash);
                            }}
                            onKeyDown={(event) => {
                              if (event.key === 'Enter' || event.key === ' ') {
                                event.preventDefault();
                                event.stopPropagation();
                                if (!controlsDisabled) void handleCopy(log.hash);
                              }
                            }}
                          >
                            {log.shortHash}
                          </span>
                        </Tiptop>
                        <Tiptop text={translate('复制提交消息')} className="min-w-0 flex-1">
                          <span
                            role="button"
                            tabIndex={0}
                            className="block min-w-0 truncate text-primary cursor-copy"
                            onClick={(event) => {
                              event.stopPropagation();
                              if (!controlsDisabled) void handleCopy(log.subject);
                            }}
                            onKeyDown={(event) => {
                              if (event.key === 'Enter' || event.key === ' ') {
                                event.preventDefault();
                                event.stopPropagation();
                                if (!controlsDisabled) void handleCopy(log.subject);
                              }
                            }}
                          >
                            {log.subject}
                          </span>
                        </Tiptop>
                        <Tiptop text={translate('复制')}>
                          <span role="button" tabIndex={0} className="git-repository-copy-button" onClick={(event) => { event.stopPropagation(); if (!controlsDisabled) void handleCopy(log.hash); }}><Clipboard size={12} /></span>
                        </Tiptop>
                      </button>
                      <div className="truncate px-2 pb-1 text-[10px] text-tertiary">{log.author} · {log.date}</div>
                      {state.expandedLogHash === log.hash ? (
                        <div className="git-repository-log-files">
                          {log.files.map((file) => <div key={`${log.hash}-${file.file}`}><span className={getStatusClass(file.status)}>{getPrimaryStatus(file.status)}</span><span className="truncate">{file.file}</span></div>)}
                        </div>
                      ) : null}
                    </div>
                  ))}
                </div>
              ) : null}
            </div>
          </>
        )}
      </div>
    );
  };

  return (
    <div className="probe-extension-panel">
      <div className="probe-extension-subtabs" role="tablist" aria-label={translate('扩展')}>
        <button type="button" role="tab" aria-selected={activeSubTab === 'git'} className={`probe-extension-subtab${activeSubTab === 'git' ? ' active' : ''}`} onClick={() => onActiveSubTabChange?.('git')}>
          <GitBranch size={13} />
          {translate('Git仓库')}
        </button>
      </div>
      <div className="git-repository-content">
        <div className="git-repository-manager">
          <div className="git-repository-input-row">
            <input
              id="git-repository-path-input"
              value={repositoryInput}
              onChange={(event) => setRepositoryInput(event.target.value)}
              disabled={Boolean(diffLoadingKey || interactiveBusy)}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  event.preventDefault();
                  handleAddRepository();
                }
              }}
              placeholder={translate('输入远程 Git 仓库绝对路径')}
            />
            <Tiptop text={translate('刷新')}>
              <button type="button" aria-label={translate('刷新')} className="git-repository-input-action" disabled={Boolean(diffLoadingKey || interactiveBusy)} onClick={() => { void (async () => { for (const repoPath of repositoryPaths) await handleRefresh(repoPath); })(); }}>
                <RefreshCw size={14} />
              </button>
            </Tiptop>
            <Tiptop text={translate('添加 Git 仓库')}>
              <button type="button" aria-label={translate('添加 Git 仓库')} className="git-repository-input-action git-repository-input-add" disabled={Boolean(diffLoadingKey || interactiveBusy)} onClick={handleAddRepository}>
                <Plus size={14} />
              </button>
            </Tiptop>
          </div>
          {repositoryPaths.length === 0 ? <div className="git-repository-empty">{translate('仓库列表为空')}</div> : null}
        </div>
        <div className="git-repository-list">
          {repositoryPaths.map(renderRepository)}
        </div>
      </div>
    </div>
  );
}

export default memo(GitRepositoryPanel, (previous, next) => (
  previous.serverId === next.serverId
  && previous.sessionId === next.sessionId
  && previous.isConnected === next.isConnected
  && previous.activeSubTab === next.activeSubTab
));