import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';
import { warnDev } from '../../utils/devLog';

export interface QuickCommandItem {
  type?: 'group' | 'command';
  name?: string;
  command?: string;
  addCR?: boolean;
  last_modified?: number;
  expanded?: boolean;
  children?: QuickCommandItem[];
  _filteredChildren?: QuickCommandItem[];
  _isFilteredGroup?: boolean;
}

export interface QuickCommandsHandle {
  isDirty: () => boolean;
  showCloseConfirm: () => void;
}

export interface QuickCommandsProps {
  sessionId: string;
  historySessionId?: string;
  addToast?: (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;
  connectedSessions?: Array<{ id: string }>;
  onClose?: () => void;
}

export interface ContextMenuState {
  anchorX: number;
  anchorY: number;
  x: number;
  y: number;
  path: string;
  type: 'group' | 'command';
  index: number;
}

export interface QuickCommandDialogState {
  type: 'add' | 'edit' | 'addGroup' | 'editGroup';
  contextPath?: string;
  parentList?: QuickCommandItem[];
  targetChildren?: QuickCommandItem[];
  groupName?: string;
  parent?: QuickCommandItem[];
  idx?: number;
}

export async function loadCommands(): Promise<QuickCommandItem[]> {
  try {
    const raw = await AppGo.GetQuickCommands();
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) return parsed as QuickCommandItem[];
  } catch (_) {}
  return [];
}

export async function saveCommands(list: QuickCommandItem[]) {
  try {
    await AppGo.SaveQuickCommands(JSON.stringify(list));
    window.dispatchEvent(new CustomEvent('quick-commands-changed'));
  } catch (e) {
    warnDev('[QuickCommands] saveCommands failed:', e);
  }
}

export async function saveCommandsLocal(list: QuickCommandItem[]) {
  try {
    await AppGo.SaveQuickCommandsLocal(JSON.stringify(list));
  } catch (_) {}
}

export function filterTree(items: QuickCommandItem[], keyword: string, parentPath = ''): QuickCommandItem[] {
  if (!keyword) return items;
  const kw = keyword.toLowerCase();
  const result: QuickCommandItem[] = [];
  items.forEach((item, i) => {
    const path = parentPath ? `${parentPath}/${i}` : String(i);
    if (item.type === 'group') {
      const nameMatch = (item.name || '').toLowerCase().includes(kw);
      if (item.children && item.children.length > 0) {
        const matchedChildren = filterTree(item.children, kw, path);
        if (nameMatch || matchedChildren.length > 0) {
          result.push({ ...item, expanded: true, _filteredChildren: matchedChildren, _isFilteredGroup: true });
        }
      } else if (nameMatch) {
        result.push(item);
      }
    } else {
      if ((item.name || '').toLowerCase().includes(kw) ||
          (item.command || '').toLowerCase().includes(kw)) {
        result.push(item);
      }
    }
  });
  return result;
}

export const resolvePath = (
  list: QuickCommandItem[],
  path: string,
): { parent: QuickCommandItem[]; idx: number; item: QuickCommandItem | null | undefined } => {
  const parts = path.split('/').map(Number);
  let cur = list;
  let parent: QuickCommandItem[] = [];
  let idx = -1;
  for (let i = 0; i < parts.length; i++) {
    parent = cur;
    idx = parts[i];
    if (i === parts.length - 1) return { parent, idx, item: cur[idx] };
    cur = cur[idx].children || [];
  }
  return { parent, idx, item: null };
};

export const cloneAlongPath = (list: QuickCommandItem[], path: string) => {
  const parts = path.split('/').map(Number);
  const newList = [...list];
  let cur = newList;
  for (let i = 0; i < parts.length; i++) {
    const idx = parts[i];
    cur[idx] = { ...(cur[idx] || {}) };
    if (i < parts.length - 1) {
      cur[idx].children = [...(cur[idx].children || [])];
      cur = cur[idx].children;
    }
  }
  return newList;
};

export const collectGroups = (list: QuickCommandItem[], basePath = '') => {
  const groups: Array<{ name?: string; path: string; children?: QuickCommandItem[] }> = [];
  if (!Array.isArray(list)) return groups;
  list.forEach((item, i) => {
    const path = basePath ? `${basePath}/${i}` : String(i);
    if (item.type === 'group') {
      groups.push({ name: item.name, path, children: item.children || [] });
      if (item.children) {
        groups.push(...collectGroups(item.children, path));
      }
    }
  });
  return groups;
};

export const inputClass =
  'w-full box-border px-2.5 py-1.5 text-xs rounded-[var(--radius-sm)] bg-sunken border border-line text-primary outline-none font-[inherit] focus:border-focus focus:ring-1 focus:ring-accent/30';
