const WORKBENCH_STATE_KEY = '__luminFileWorkbenchState';
const UPLOAD_QUEUE_STATE_KEY = '__luminFileUploadQueueState';
const UPLOAD_PANEL_STATE_KEY = '__luminFileUploadPanelState';
const FILE_MANAGER_WORKSPACE_STATE_KEY = '__luminFileManagerWorkspaceState';
const FILE_MANAGER_PATH_CACHE_STATE_KEY = '__luminFileManagerPathCacheState';
const FILE_MANAGER_WORKSPACE_CHANGED_EVENT = 'lumin-file-manager-workspace-changed';
const FILE_MANAGER_SHARED_PINNED_STATE_KEY = '__luminFileManagerSharedPinnedState';
const FILE_MANAGER_SHARED_PINNED_CHANGED_EVENT = 'lumin-file-manager-shared-pinned-changed';

/** 全局 store 宿主（window 或 globalThis） */
type StoreHost = Window & typeof globalThis;

function getRoot(): Window & typeof globalThis {
  if (typeof window !== 'undefined') return window;
  return globalThis as Window & typeof globalThis;
}

function normalizeSessionGroupId(sessionGroupId: unknown): string {
  return String(sessionGroupId || 'default');
}

function workbenchEventName(sessionGroupId: unknown): string {
  return `lumin-file-workbench:${normalizeSessionGroupId(sessionGroupId)}`;
}

function uploadQueueEventName(sessionGroupId: unknown): string {
  return `lumin-file-upload-queue:${normalizeSessionGroupId(sessionGroupId)}`;
}

function uploadPanelEventName(sessionGroupId: unknown, sessionId: unknown): string {
  return `lumin-file-upload-panel:${normalizeSessionGroupId(sessionGroupId)}:${normalizeSessionGroupId(sessionId)}`;
}

function fileManagerWorkspaceEventName(sessionId: unknown): string {
  return `lumin-file-manager-workspace:${normalizeSessionGroupId(sessionId)}`;
}

function fileManagerSharedPinnedEventName(sessionGroupId: unknown): string {
  return `lumin-file-manager-shared-pinned:${normalizeSessionGroupId(sessionGroupId)}`;
}

function ensureStore(host: StoreHost, key: string): Record<string, unknown> {
  const current = (host as unknown as Record<string, unknown>)[key];
  if (!current || typeof current !== 'object') {
    const next: Record<string, unknown> = {};
    (host as unknown as Record<string, unknown>)[key] = next;
    return next;
  }
  return current as Record<string, unknown>;
}

function ensureWorkbenchStore(): Record<string, unknown> {
  return ensureStore(getRoot(), WORKBENCH_STATE_KEY);
}

function ensureUploadQueueStore(): Record<string, unknown> {
  return ensureStore(getRoot(), UPLOAD_QUEUE_STATE_KEY);
}

function ensureUploadPanelStore(): Record<string, unknown> {
  return ensureStore(getRoot(), UPLOAD_PANEL_STATE_KEY);
}

function ensureFileManagerWorkspaceStore(): Record<string, unknown> {
  return ensureStore(getRoot(), FILE_MANAGER_WORKSPACE_STATE_KEY);
}

function ensureFileManagerPathCacheStore(): Record<string, unknown> {
  return ensureStore(getRoot(), FILE_MANAGER_PATH_CACHE_STATE_KEY);
}

function ensureFileManagerSharedPinnedStore(): Record<string, unknown> {
  return ensureStore(getRoot(), FILE_MANAGER_SHARED_PINNED_STATE_KEY);
}

/** 远程文件条目（宽松形状） */
export interface FileManagerPathItem {
  [key: string]: unknown;
  __luminDeletedPlaceholder?: boolean;
}

function cloneFileManagerPathItems(items: unknown): FileManagerPathItem[] {
  return Array.isArray(items)
    ? items
      .filter((item) => item && typeof item === 'object' && !(item as FileManagerPathItem).__luminDeletedPlaceholder)
      .map((item) => ({ ...(item as FileManagerPathItem) }))
    : [];
}

function normalizeFileManagerPathCacheKey(path: unknown): string {
  return String(path || '').trim() || '/';
}

function normalizeFileManagerTabPath(path: unknown): string {
  const trimmed = String(path || '').trim();
  if (!trimmed) return '';
  const normalizedSlashes = trimmed.replace(/\\/g, '/').replace(/\/+/g, '/');
  const parts: string[] = [];
  normalizedSlashes.split('/').forEach((part) => {
    if (!part || part === '.') return;
    if (part === '..') {
      if (parts.length > 0) parts.pop();
      return;
    }
    parts.push(part);
  });
  return parts.length > 0 ? `/${parts.join('/')}` : '/';
}

function getFileManagerSystemPinnedType(tab: FileManagerTabLike | null | undefined): string {
  const rawType = String(tab?.systemPinnedType || '').trim();
  if (rawType === 'cwd') {
    return '';
  }
  if (tab?.systemPinned === true) {
    return 'home';
  }
  return '';
}

/** 文件管理器标签（宽松形状） */
export interface FileManagerTabLike {
  id?: unknown;
  path?: unknown;
  customTitle?: unknown;
  sortField?: unknown;
  sortDir?: unknown;
  selectedPaths?: unknown;
  scrollTop?: unknown;
  pinned?: unknown;
  systemPinned?: unknown;
  systemPinnedType?: unknown;
}

/** 规范化后的文件管理器标签 */
export interface FileManagerTab {
  id: string;
  path: string;
  customTitle: string;
  sortField: string;
  sortDir: 'asc' | 'desc';
  selectedPaths: string[];
  scrollTop: number;
  pinned: boolean;
  systemPinned: boolean;
  systemPinnedType: string;
}

/** 规范化后的文件管理器面板状态 */
export interface FileManagerPaneState {
  tabId: string;
  path: string;
  sortField: string;
  sortDir: 'asc' | 'desc';
  selectedPaths: string[];
  scrollTop: number;
}

/** 规范化后的文件管理器工作区状态 */
export interface FileManagerWorkspaceState {
  activeTabId: string;
  activePane: 'left' | 'right';
  panes: {
    left: FileManagerPaneState;
    right: FileManagerPaneState;
  };
  tabs: FileManagerTab[];
}

function sortFileManagerTabs(tabs: unknown): FileManagerTab[] {
  const homeSystemPinnedTabs: FileManagerTab[] = [];
  const pinnedTabs: FileManagerTab[] = [];
  const normalTabs: FileManagerTab[] = [];
  (Array.isArray(tabs) ? tabs as FileManagerTab[] : []).forEach((tab) => {
    if (!tab || typeof tab !== 'object') return;
    const systemPinnedType = getFileManagerSystemPinnedType(tab);
    if (systemPinnedType === 'home') {
      homeSystemPinnedTabs.push(tab);
      return;
    }
    if (tab.pinned === true) {
      pinnedTabs.push(tab);
      return;
    }
    normalTabs.push(tab);
  });
  return [...homeSystemPinnedTabs, ...pinnedTabs, ...normalTabs];
}

function normalizeFileManagerPaneState(
  state: unknown,
  tabs: FileManagerTab[],
  fallbackTabId = '',
  fallbackPath = '/',
): FileManagerPaneState {
  const source = state && typeof state === 'object' ? state as Record<string, unknown> : {};
  const resolvedTabs = Array.isArray(tabs) ? tabs : [];
  const fallbackTab = resolvedTabs.find((tab) => tab.id === fallbackTabId) || resolvedTabs[0] || null;
  const requestedTabId = typeof source.tabId === 'string' ? source.tabId.trim() : '';
  const matchedTab = resolvedTabs.find((tab) => tab.id === requestedTabId) || fallbackTab;
  return {
    tabId: matchedTab?.id || '',
    path: normalizeFileManagerTabPath(source.path) || normalizeFileManagerTabPath(matchedTab?.path) || normalizeFileManagerTabPath(fallbackPath) || '/',
    sortField: typeof source.sortField === 'string' ? source.sortField : (matchedTab?.sortField || 'name'),
    sortDir: source.sortDir === 'desc' ? 'desc' : (matchedTab?.sortDir === 'desc' ? 'desc' : 'asc'),
    selectedPaths: Array.isArray(source.selectedPaths)
      ? source.selectedPaths.filter((item): item is string => typeof item === 'string')
      : (Array.isArray(matchedTab?.selectedPaths) ? matchedTab.selectedPaths.filter((item): item is string => typeof item === 'string') : []),
    scrollTop: Number.isFinite(Number(source.scrollTop))
      ? Number(source.scrollTop)
      : (Number.isFinite(Number(matchedTab?.scrollTop)) ? Number(matchedTab.scrollTop) : 0),
  };
}

function normalizeFileManagerWorkspaceState(state: unknown): FileManagerWorkspaceState {
  const source = state && typeof state === 'object' ? state as Record<string, unknown> : {};
  const tabs: FileManagerTab[] = Array.isArray(source.tabs)
    ? sortFileManagerTabs(
      source.tabs
        .map((tab) => {
          if (!tab || typeof tab !== 'object') return null;
          const rawTab = tab as FileManagerTabLike;
          const legacySystemPinnedType = String(rawTab.systemPinnedType || '').trim();
          if (legacySystemPinnedType === 'cwd') return null;
          const id = String(rawTab.id || '').trim();
          if (!id) return null;
          return {
            id,
            path: normalizeFileManagerTabPath(rawTab.path),
            customTitle: typeof rawTab.customTitle === 'string' ? rawTab.customTitle.trim() : '',
            sortField: typeof rawTab.sortField === 'string' ? rawTab.sortField : 'name',
            sortDir: rawTab.sortDir === 'desc' ? 'desc' : 'asc',
            selectedPaths: Array.isArray(rawTab.selectedPaths) ? rawTab.selectedPaths.filter((item): item is string => typeof item === 'string') : [],
            scrollTop: Number.isFinite(Number(rawTab.scrollTop)) ? Number(rawTab.scrollTop) : 0,
            pinned: rawTab.pinned === true || rawTab.systemPinned === true,
            systemPinned: rawTab.systemPinned === true,
            systemPinnedType: rawTab.systemPinned === true ? getFileManagerSystemPinnedType(rawTab) : '',
          };
        })
        .filter((tab): tab is FileManagerTab => tab !== null)
    )
    : [];
  const requestedActiveTabId = typeof source.activeTabId === 'string' ? source.activeTabId.trim() : '';
  const defaultTabId = tabs[0]?.id || '';
  const activePane = source.activePane === 'right' ? 'right' : 'left';
  const leftPane = normalizeFileManagerPaneState(
    source.panes && typeof source.panes === 'object' ? (source.panes as Record<string, unknown>).left : null,
    tabs,
    requestedActiveTabId || defaultTabId,
    tabs.find((tab) => tab.id === requestedActiveTabId)?.path || tabs[0]?.path || '/',
  );
  const rightPane = normalizeFileManagerPaneState(
    source.panes && typeof source.panes === 'object' ? (source.panes as Record<string, unknown>).right : null,
    tabs,
    leftPane.tabId || requestedActiveTabId || defaultTabId,
    leftPane.path || '/',
  );
  const activeTabId = activePane === 'right'
    ? (rightPane.tabId || leftPane.tabId || requestedActiveTabId || defaultTabId)
    : (leftPane.tabId || rightPane.tabId || requestedActiveTabId || defaultTabId);
  return {
    activeTabId,
    activePane,
    panes: {
      left: leftPane,
      right: rightPane,
    },
    tabs,
  };
}

/** 会话工作台 UI 状态 */
export interface WorkbenchState {
  activeTab: string;
  uploadOpen: boolean;
  editorSplitOpen: boolean;
  editorOwnerId: string;
  [key: string]: unknown;
}

export function getSessionWorkbenchState(sessionGroupId: unknown): WorkbenchState {
  const store = ensureWorkbenchStore();
  const key = normalizeSessionGroupId(sessionGroupId);
  return {
    activeTab: 'upload',
    uploadOpen: false,
    editorSplitOpen: false,
    editorOwnerId: '',
    ...(store[key] as Partial<WorkbenchState> | undefined),
  };
}

export function setSessionWorkbenchState(
  sessionGroupId: unknown,
  patch: Partial<WorkbenchState> | ((current: WorkbenchState) => Partial<WorkbenchState>),
): WorkbenchState {
  const root = getRoot();
  const store = ensureWorkbenchStore();
  const key = normalizeSessionGroupId(sessionGroupId);
  const current = getSessionWorkbenchState(key);
  const nextPatch = typeof patch === 'function' ? patch(current) : patch;
  const next: WorkbenchState = { ...current, ...(nextPatch || {}) };
  store[key] = next;
  root.dispatchEvent(new CustomEvent<WorkbenchState>(workbenchEventName(key), { detail: next }));
  return next;
}

export function subscribeSessionWorkbenchState(sessionGroupId: unknown, callback: (state: WorkbenchState) => void): () => void {
  const root = getRoot();
  const key = normalizeSessionGroupId(sessionGroupId);
  const handler = (event: Event) => callback((event as CustomEvent<WorkbenchState>).detail);
  callback(getSessionWorkbenchState(key));
  root.addEventListener(workbenchEventName(key), handler);
  return () => root.removeEventListener(workbenchEventName(key), handler);
}

/** 传输分块（FileManager 上报的宽松结构） */
export interface TransferChunk {
  index: number;
  status: string;
  attempt?: number;
  error?: string;
}

/** 传输队列条目（FileManager 上报的宽松结构） */
export interface TransferQueueItem {
  id: string;
  name?: string;
  direction?: string;
  status: string;
  mode?: string;
  phase?: string;
  phaseDetail?: string;
  phaseCurrent?: string;
  phaseProgress?: number;
  progress?: number;
  error?: string;
  localPath?: string;
  remotePath?: string;
  createdAt?: number;
  bytesUploaded?: number;
  bytesTotal?: number;
  chunkSizeBytes?: number;
  chunksCompleted?: number;
  chunksFailed?: number;
  chunks?: TransferChunk[];
  [key: string]: unknown;
}

export function getSessionUploadQueue(sessionGroupId: unknown): TransferQueueItem[] {
  const store = ensureUploadQueueStore();
  const key = normalizeSessionGroupId(sessionGroupId);
  return Array.isArray(store[key]) ? store[key] as TransferQueueItem[] : [];
}

export function updateSessionUploadQueue(
  sessionGroupId: unknown,
  updater: TransferQueueItem[] | ((current: TransferQueueItem[]) => TransferQueueItem[]),
): TransferQueueItem[] {
  const root = getRoot();
  const store = ensureUploadQueueStore();
  const key = normalizeSessionGroupId(sessionGroupId);
  const current = getSessionUploadQueue(key);
  const next = typeof updater === 'function' ? updater(current) : updater;
  store[key] = Array.isArray(next) ? next : [];
  root.dispatchEvent(new CustomEvent<TransferQueueItem[]>(uploadQueueEventName(key), { detail: store[key] as TransferQueueItem[] }));
  return store[key] as TransferQueueItem[];
}

export function subscribeSessionUploadQueue(sessionGroupId: unknown, callback: (queue: TransferQueueItem[]) => void): () => void {
  const root = getRoot();
  const key = normalizeSessionGroupId(sessionGroupId);
  const handler = (event: Event) => callback((event as CustomEvent<TransferQueueItem[]>).detail);
  callback(getSessionUploadQueue(key));
  root.addEventListener(uploadQueueEventName(key), handler);
  return () => root.removeEventListener(uploadQueueEventName(key), handler);
}

/** 会话上传面板 UI 状态 */
export interface UploadPanelState {
  uploadOpen: boolean;
  [key: string]: unknown;
}

export function getSessionUploadPanelState(sessionGroupId: unknown, sessionId: unknown): UploadPanelState {
  const store = ensureUploadPanelStore();
  const groupKey = normalizeSessionGroupId(sessionGroupId);
  const terminalKey = normalizeSessionGroupId(sessionId);
  const groupStore = store[groupKey];
  return {
    uploadOpen: false,
    ...(groupStore && typeof groupStore === 'object' ? (groupStore as Record<string, unknown>)[terminalKey] as Partial<UploadPanelState> | undefined : {}),
  };
}

export function setSessionUploadPanelState(
  sessionGroupId: unknown,
  sessionId: unknown,
  patch: Partial<UploadPanelState> | ((current: UploadPanelState) => Partial<UploadPanelState>),
): UploadPanelState {
  const root = getRoot();
  const store = ensureUploadPanelStore();
  const groupKey = normalizeSessionGroupId(sessionGroupId);
  const terminalKey = normalizeSessionGroupId(sessionId);
  if (!store[groupKey] || typeof store[groupKey] !== 'object') {
    store[groupKey] = {};
  }
  const current = getSessionUploadPanelState(groupKey, terminalKey);
  const nextPatch = typeof patch === 'function' ? patch(current) : patch;
  const next: UploadPanelState = { ...current, ...(nextPatch || {}) };
  (store[groupKey] as Record<string, unknown>)[terminalKey] = next;
  root.dispatchEvent(new CustomEvent<UploadPanelState>(uploadPanelEventName(groupKey, terminalKey), { detail: next }));
  return next;
}

export function subscribeSessionUploadPanelState(
  sessionGroupId: unknown,
  sessionId: unknown,
  callback: (state: UploadPanelState) => void,
): () => void {
  const root = getRoot();
  const groupKey = normalizeSessionGroupId(sessionGroupId);
  const terminalKey = normalizeSessionGroupId(sessionId);
  const eventName = uploadPanelEventName(groupKey, terminalKey);
  const handler = (event: Event) => callback((event as CustomEvent<UploadPanelState>).detail);
  callback(getSessionUploadPanelState(groupKey, terminalKey));
  root.addEventListener(eventName, handler);
  return () => root.removeEventListener(eventName, handler);
}

function getSessionFileManagerPathCache(sessionGroupId: unknown): Record<string, unknown> {
  const store = ensureFileManagerPathCacheStore();
  const key = normalizeSessionGroupId(sessionGroupId);
  const current = store[key];
  return current && typeof current === 'object' ? current as Record<string, unknown> : {};
}

export function getSessionCachedFileManagerPathItems(sessionGroupId: unknown, path: unknown): FileManagerPathItem[] | null {
  const cache = getSessionFileManagerPathCache(sessionGroupId);
  const items = cache[normalizeFileManagerPathCacheKey(path)];
  return Array.isArray(items) ? cloneFileManagerPathItems(items) : null;
}

export function setSessionCachedFileManagerPathItems(
  sessionGroupId: unknown,
  path: unknown,
  items: unknown,
): Record<string, unknown> {
  const store = ensureFileManagerPathCacheStore();
  const key = normalizeSessionGroupId(sessionGroupId);
  const pathKey = normalizeFileManagerPathCacheKey(path);
  const current = getSessionFileManagerPathCache(key);
  const next = {
    ...current,
    [pathKey]: cloneFileManagerPathItems(items),
  };
  store[key] = next;
  return next;
}

export function getSessionFileManagerWorkspace(sessionId: unknown): FileManagerWorkspaceState {
  const store = ensureFileManagerWorkspaceStore();
  const key = normalizeSessionGroupId(sessionId);
  return normalizeFileManagerWorkspaceState(store[key]);
}

export function setSessionFileManagerWorkspace(
  sessionId: unknown,
  patch: Partial<FileManagerWorkspaceState> | ((current: FileManagerWorkspaceState) => Partial<FileManagerWorkspaceState>),
): FileManagerWorkspaceState {
  const root = getRoot();
  const store = ensureFileManagerWorkspaceStore();
  const key = normalizeSessionGroupId(sessionId);
  const current = getSessionFileManagerWorkspace(key);
  const nextPatch = typeof patch === 'function' ? patch(current) : patch;
  const nextSource = nextPatch && typeof nextPatch === 'object'
    ? {...current,
      ...nextPatch,
      panes: {
        ...(current.panes && typeof current.panes === 'object' ? current.panes : {}),
        ...(nextPatch.panes && typeof nextPatch.panes === 'object' ? nextPatch.panes : {}),
      },
    }
    : current;
  const next = normalizeFileManagerWorkspaceState(nextSource);
  store[key] = next;
  root.dispatchEvent(new CustomEvent<FileManagerWorkspaceState>(fileManagerWorkspaceEventName(key), { detail: next }));
  root.dispatchEvent(new CustomEvent(FILE_MANAGER_WORKSPACE_CHANGED_EVENT, {
    detail: { sessionId: key, workspace: next },
  }));
  return next;
}

export function subscribeSessionFileManagerWorkspace(
  sessionId: unknown,
  callback: (state: FileManagerWorkspaceState) => void,
): () => void {
  const root = getRoot();
  const key = normalizeSessionGroupId(sessionId);
  const handler = (event: Event) => callback((event as CustomEvent<FileManagerWorkspaceState>).detail);
  callback(getSessionFileManagerWorkspace(key));
  root.addEventListener(fileManagerWorkspaceEventName(key), handler);
  return () => root.removeEventListener(fileManagerWorkspaceEventName(key), handler);
}

/** 共享置顶标签（跨文件管理器实例） */
export interface SharedPinnedTab {
  id: string;
  path: string;
  customTitle: string;
}

function normalizeSharedPinnedTab(tab: unknown): SharedPinnedTab | null {
  if (!tab || typeof tab !== 'object') return null;
  const rawTab = tab as FileManagerTabLike;
  const id = String(rawTab.id || '').trim();
  if (!id) return null;
  return {
    id,
    path: normalizeFileManagerTabPath(rawTab.path) || '/',
    customTitle: typeof rawTab.customTitle === 'string' ? rawTab.customTitle.trim() : '',
  };
}

function normalizeSharedPinnedTabs(tabs: unknown): SharedPinnedTab[] {
  const seenIds = new Set<string>();
  const seenPaths = new Set<string>();
  return (Array.isArray(tabs) ? tabs : [])
    .map(normalizeSharedPinnedTab)
    .filter((tab): tab is SharedPinnedTab => {
      if (!tab) return false;
      if (seenIds.has(tab.id)) return false;
      if (tab.path && seenPaths.has(tab.path)) return false;
      seenIds.add(tab.id);
      if (tab.path) seenPaths.add(tab.path);
      return true;
    });
}

export function getSessionSharedPinnedTabs(sessionGroupId: unknown): SharedPinnedTab[] {
  const store = ensureFileManagerSharedPinnedStore();
  const key = normalizeSessionGroupId(sessionGroupId);
  return normalizeSharedPinnedTabs(store[key]);
}

export function setSessionSharedPinnedTabs(
  sessionGroupId: unknown,
  updater: SharedPinnedTab[] | ((current: SharedPinnedTab[]) => SharedPinnedTab[]),
): SharedPinnedTab[] {
  const root = getRoot();
  const store = ensureFileManagerSharedPinnedStore();
  const key = normalizeSessionGroupId(sessionGroupId);
  const current = getSessionSharedPinnedTabs(key);
  const nextRaw = typeof updater === 'function' ? updater(current) : updater;
  const next = normalizeSharedPinnedTabs(nextRaw);
  store[key] = next;
  root.dispatchEvent(new CustomEvent<SharedPinnedTab[]>(fileManagerSharedPinnedEventName(key), { detail: next }));
  root.dispatchEvent(new CustomEvent(FILE_MANAGER_SHARED_PINNED_CHANGED_EVENT, {
    detail: { sessionGroupId: key, tabs: next },
  }));
  return next;
}

export function subscribeSessionSharedPinnedTabs(
  sessionGroupId: unknown,
  callback: (tabs: SharedPinnedTab[]) => void,
): () => void {
  const root = getRoot();
  const key = normalizeSessionGroupId(sessionGroupId);
  const handler = (event: Event) => callback((event as CustomEvent<SharedPinnedTab[]>).detail);
  callback(getSessionSharedPinnedTabs(key));
  root.addEventListener(fileManagerSharedPinnedEventName(key), handler);
  return () => root.removeEventListener(fileManagerSharedPinnedEventName(key), handler);
}

export function getAllSessionFileManagerWorkspaces(): Record<string, FileManagerWorkspaceState> {
  const store = ensureFileManagerWorkspaceStore();
  return Object.fromEntries(
    Object.entries(store).map(([key, value]) => [key, normalizeFileManagerWorkspaceState(value)]),
  );
}

export function replaceAllSessionFileManagerWorkspaces(nextState: unknown): Record<string, FileManagerWorkspaceState> {
  const root = getRoot();
  const currentStore = ensureFileManagerWorkspaceStore();
  const previousKeys = Object.keys(currentStore);
  const normalized: Record<string, FileManagerWorkspaceState> = {};
  Object.entries(nextState && typeof nextState === 'object' ? nextState as Record<string, unknown> : {}).forEach(([key, value]) => {
    normalized[normalizeSessionGroupId(key)] = normalizeFileManagerWorkspaceState(value);
  });
  (root as unknown as Record<string, unknown>)[FILE_MANAGER_WORKSPACE_STATE_KEY] = normalized;
  const changedKeys = new Set([...previousKeys, ...Object.keys(normalized)]);
  changedKeys.forEach((key) => {
    root.dispatchEvent(new CustomEvent<FileManagerWorkspaceState>(fileManagerWorkspaceEventName(key), {
      detail: getSessionFileManagerWorkspace(key),
    }));
  });
  root.dispatchEvent(new CustomEvent(FILE_MANAGER_WORKSPACE_CHANGED_EVENT, {
    detail: { sessionIds: Array.from(changedKeys), workspaces: normalized },
  }));
  return normalized;
}

export function remapSessionFileManagerPathCaches(idMap: Record<string, string> | null | undefined): void {
  const sourceMap = idMap && typeof idMap === 'object' ? idMap : {};
  const store = ensureFileManagerPathCacheStore();
  const keys = Object.keys(store);
  const remapped: Record<string, unknown> = {};
  keys.forEach((sessionId) => {
    remapped[normalizeSessionGroupId(sourceMap[sessionId] || sessionId)] = store[sessionId];
  });
  keys.forEach((key) => {
    delete store[key];
  });
  Object.assign(store, remapped);
}

export function remapSessionFileManagerWorkspaces(idMap: Record<string, string> | null | undefined): Record<string, FileManagerWorkspaceState> {
  const sourceMap = idMap && typeof idMap === 'object' ? idMap : {};
  remapSessionFileManagerPathCaches(sourceMap);
  const current = getAllSessionFileManagerWorkspaces();
  const remapped: Record<string, FileManagerWorkspaceState> = {};
  Object.entries(current).forEach(([sessionId, state]) => {
    const mappedId = normalizeSessionGroupId(sourceMap[sessionId] || sessionId);
    remapped[mappedId] = normalizeFileManagerWorkspaceState(state);
  });
  return replaceAllSessionFileManagerWorkspaces(remapped);
}
