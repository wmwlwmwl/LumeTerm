import { useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState, type Ref } from 'react';
import type React from 'react';
import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';
import { useTranslation } from '../../i18n.ts';
import { warnDev } from '../../utils/devLog';
import {
  extractQuickCommandParams,
  fillQuickCommandParams,
  normalizeQuickCommandParamHistory,
  QUICK_COMMAND_PARAM_HISTORY_LIMIT,
  type QuickCommandParamHistory,
} from '../../utils/quickCommandParams.ts';
import {
  cloneAlongPath,
  loadCommands,
  resolvePath,
  saveCommands,
  saveCommandsLocal,
  type ContextMenuState,
  type QuickCommandDialogState,
  type QuickCommandItem,
  type QuickCommandsHandle,
  type QuickCommandsProps,
} from './quickCommandTypes.ts';

export function useQuickCommands(
  props: QuickCommandsProps,
  ref: Ref<QuickCommandsHandle>,
) {
  const { sessionId, historySessionId, addToast, connectedSessions = [], onClose } = props;
  const { t } = useTranslation();

  const [commands, setCommands] = useState<QuickCommandItem[]>([]);
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null);
  const [sendTarget, setSendTarget] = useState<'current' | 'all'>('current');
  const [showCmdBar, setShowCmdBar] = useState(
    () => localStorage.getItem('terminalQuickCmdBar') === 'true',
  );
  const [showCmdEditor, setShowCmdEditor] = useState(false);
  const [cmdEditorText, setCmdEditorText] = useState('');
  const [cmdEditorAddCR, setCmdEditorAddCR] = useState(true);
  const [cmdEditorClearAfterSend, setCmdEditorClearAfterSend] = useState(true);
  const [cmdEditorShowOpts, setCmdEditorShowOpts] = useState(false);

  const [dialog, setDialog] = useState<QuickCommandDialogState | null>(null);
  const [dlgName, setDlgName] = useState('');
  const [dlgCmd, setDlgCmd] = useState('');
  const [dlgAddCR, setDlgAddCR] = useState(true);

  const [paramHistory, setParamHistory] = useState<QuickCommandParamHistory>({});
  const [paramValues, setParamValues] = useState<Record<number, string>>({});
  const [historyDropdown, setHistoryDropdown] = useState<{ cmdKey: string; paramNum: number; left: number; top: number } | null>(null);
  const [historySearch, setHistorySearch] = useState('');
  const [searchText, setSearchText] = useState('');
  const [rootDragOver, setRootDragOver] = useState(false);
  const [dragVersion, setDragVersion] = useState(0);
  const [dirty, setDirty] = useState(false);
  const [confirmUnsaved, setConfirmUnsaved] = useState<{ close?: boolean; pendingPath?: string } | null>(null);
  const [editGroupName, setEditGroupName] = useState('');
  const [editCmdName, setEditCmdName] = useState('');
  const [editCmdText, setEditCmdText] = useState('');

  const dragSourceRef = useRef<string | null>(null);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  useImperativeHandle(ref, () => ({
    isDirty: () => dirty,
    showCloseConfirm: () => setConfirmUnsaved({ close: true }),
  }));

  const handleDragStart = (path: string) => {
    dragSourceRef.current = path;
  };

  const clearDrag = () => {
    dragSourceRef.current = null;
    setRootDragOver(false);
    setDragVersion((v) => v + 1);
  };

  const parsePath = (path: string) => path.split('/').map(Number);
  const hasPathPrefix = (parts: number[], prefix: number[]) => prefix.every((value, index) => parts[index] === value);

  const adjustPathAfterRemoval = (targetParts: number[], srcParts: number[]) => {
    const srcParentParts = srcParts.slice(0, -1);
    const srcIdx = srcParts[srcParts.length - 1];
    if (!hasPathPrefix(targetParts, srcParentParts)) return targetParts;
    const affectedIndex = srcParentParts.length;
    if (targetParts[affectedIndex] > srcIdx) {
      const adjusted = [...targetParts];
      adjusted[affectedIndex] -= 1;
      return adjusted;
    }
    return targetParts;
  };

  const save = async (list: QuickCommandItem[]) => {
    await saveCommands(list);
    const data = await loadCommands();
    if (data.length > 0) setCommands(data);
  };

  const handleDropItem = (targetPath: string, pos: string) => {
    const srcPath = dragSourceRef.current;
    if (!srcPath || srcPath === targetPath) { clearDrag(); return; }
    if (targetPath.startsWith(srcPath + '/')) { clearDrag(); return; }

    const srcParts = parsePath(srcPath);
    const targetParts = parsePath(targetPath);
    const list = structuredClone(commands);
    const src = resolvePath(list, srcPath);
    if (!src.item) { clearDrag(); return; }

    const [moved] = src.parent.splice(src.idx, 1);
    moved.last_modified = Date.now();

    const adjustedTargetPath = adjustPathAfterRemoval(targetParts, srcParts).join('/');
    const tgt = resolvePath(list, adjustedTargetPath);
    if (!tgt.item) { clearDrag(); return; }

    if (pos === 'inside' && tgt.item.type === 'group') {
      if (!tgt.item.children) tgt.item.children = [];
      tgt.item.children.push(moved);
      tgt.item.expanded = true;
    } else {
      const insertIdx = tgt.idx + (pos === 'after' ? 1 : 0);
      tgt.parent.splice(insertIdx, 0, moved);
    }

    save(list);
    setSelectedPath(null);
    clearDrag();
  };

  const handleDropToRoot = () => {
    const srcPath = dragSourceRef.current;
    if (!srcPath) { clearDrag(); return; }
    const list = structuredClone(commands);
    const src = resolvePath(list, srcPath);
    if (!src.item) { clearDrag(); return; }
    const [moved] = src.parent.splice(src.idx, 1);
    moved.last_modified = Date.now();
    list.push(moved);
    save(list);
    setSelectedPath(null);
    clearDrag();
  };

  useEffect(() => {
    let cancelled = false;
    Promise.all([
      loadCommands(),
      (async () => {
        try {
          const raw = await AppGo.GetParamHistory();
          return normalizeQuickCommandParamHistory(JSON.parse(raw));
        } catch (_) {}
        return {};
      })(),
    ]).then(([data, hist]) => {
      if (cancelled) return;
      if (data.length > 0) setCommands(data);
      setParamHistory(hist);
      AppGo.SaveParamHistory(JSON.stringify(hist)).catch(() => {});
    });
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    if (!selectedPath) return;
    const { item } = resolvePath(commands, selectedPath);
    if (!item) return;
    if (item.type === 'group') {
      setEditGroupName(item.name || '');
    } else if (!item.children) {
      setEditCmdName(item.name || '');
      setEditCmdText(item.command || '');
    }
  }, [selectedPath, commands]);

  const commandsRef = useRef(commands);
  const dirtyRef = useRef(dirty);
  commandsRef.current = commands;
  dirtyRef.current = dirty;
  useEffect(() => {
    return () => {
      if (dirtyRef.current && commandsRef.current.length > 0) {
        saveCommands(commandsRef.current);
      }
    };
  }, []);

  useEffect(() => {
    if (!historyDropdown) return;
    const handler = (e: MouseEvent) => {
      if ((e.target as HTMLElement).closest('[data-history-dropdown]')) return;
      setHistoryDropdown(null);
      setHistorySearch('');
    };
    document.addEventListener('click', handler, true);
    return () => document.removeEventListener('click', handler, true);
  }, [historyDropdown]);

  useEffect(() => {
    if (!showCmdEditor && cmdEditorShowOpts) setCmdEditorShowOpts(false);
  }, [showCmdEditor, cmdEditorShowOpts]);

  const commitCmdEdit = useCallback(() => {
    if (!selectedPath || dirty === false) return null;
    const sel = resolvePath(commands, selectedPath);
    if (!sel?.item || sel.item.children) return null;
    if (sel.item.name === editCmdName && sel.item.command === editCmdText) return null;
    const list = cloneAlongPath(commands, selectedPath);
    const r = resolvePath(list, selectedPath);
    r.parent[r.idx].name = editCmdName;
    r.parent[r.idx].command = editCmdText;
    r.parent[r.idx].last_modified = Date.now();
    setCommands(list);
    return list;
  }, [commands, selectedPath, editCmdName, editCmdText, dirty]);

  const handleMove = (path: string, direction: number) => {
    const list = structuredClone(commands);
    const { parent, idx } = resolvePath(list, path);
    const newIdx = idx + direction;
    if (newIdx < 0 || newIdx >= parent.length) return;
    [parent[idx], parent[newIdx]] = [parent[newIdx], parent[idx]];
    parent[idx].last_modified = Date.now();
    parent[newIdx].last_modified = Date.now();
    save(list);
    setSelectedPath(path.replace(/\/\d+$/, `/${newIdx}`));
    setContextMenu(null);
  };

  const handleSelect = (path: string) => {
    commitCmdEdit();
    if (selectedPath && selectedPath !== path && dirty) {
      setConfirmUnsaved({ pendingPath: path });
      return;
    }
    setSelectedPath(path);
    setContextMenu(null);
    const { item } = resolvePath(commands, path);
    if (item?.type === 'group') {
      const list = cloneAlongPath(commands, path);
      const r = resolvePath(list, path);
      if (r.item) r.item.expanded = !r.item.expanded;
      setCommands(list);
      saveCommandsLocal(list);
      setParamValues({});
      setDirty(false);
      return;
    }
    if (item?.command) {
      const params = extractQuickCommandParams(item.command);
      const hist = paramHistory[item.command] || {};
      const initial: Record<number, string> = {};
      params.forEach((p) => { initial[p.num] = (hist[p.num]?.[0]) || ''; });
      setParamValues(initial);
    } else {
      setParamValues({});
    }
    setDirty(false);
  };

  const handleConfirmSave = () => {
    if (!confirmUnsaved) return;
    const isClose = confirmUnsaved.close;
    const path = confirmUnsaved.pendingPath;
    const committed = commitCmdEdit();
    save(committed || commands);
    setDirty(false);
    dirtyRef.current = false;
    setConfirmUnsaved(null);
    if (isClose) {
      onClose?.();
    } else if (path) {
      const { item } = resolvePath(commands, path);
      if (item?.type === 'group') {
        const list = structuredClone(commands);
        const r = resolvePath(list, path);
        if (r.item) r.item.expanded = !r.item.expanded;
        setCommands(list);
        saveCommandsLocal(list);
        setParamValues({});
        return;
      }
      setSelectedPath(path);
      setContextMenu(null);
      if (item?.command) {
        const params = extractQuickCommandParams(item.command);
        const hist = paramHistory[item.command] || {};
        const initial: Record<number, string> = {};
        params.forEach((p) => { initial[p.num] = (hist[p.num]?.[0]) || ''; });
        setParamValues(initial);
      } else {
        setParamValues({});
      }
    }
  };

  const handleConfirmDiscard = async () => {
    if (!confirmUnsaved) return;
    const isClose = confirmUnsaved.close;
    const path = confirmUnsaved.pendingPath;
    setConfirmUnsaved(null);
    setDirty(false);
    dirtyRef.current = false;
    if (isClose) {
      onClose?.();
    } else if (path) {
      const { item: currentItem } = resolvePath(commands, path);
      const data = await loadCommands();
      if (!mountedRef.current) return;
      setCommands(data);
      if (currentItem?.type === 'group') {
        const list = structuredClone(data);
        const r = resolvePath(list, path);
        if (r.item) r.item.expanded = !r.item.expanded;
        saveCommandsLocal(list);
        setParamValues({});
        return;
      }
      setSelectedPath(path);
      setContextMenu(null);
      const { item } = resolvePath(data, path);
      if (item?.command) {
        const params = extractQuickCommandParams(item.command);
        const hist = paramHistory[item.command] || {};
        const initial: Record<number, string> = {};
        params.forEach((p) => { initial[p.num] = (hist[p.num]?.[0]) || ''; });
        setParamValues(initial);
      } else {
        setParamValues({});
      }
    }
  };

  const handleContextMenu = (e: React.MouseEvent, path: string, type: 'group' | 'command', index: number) => {
    setContextMenu({
      anchorX: e.clientX,
      anchorY: e.clientY,
      x: e.clientX,
      y: e.clientY,
      path,
      type,
      index,
    });
    setSelectedPath(path);
  };

  const closeContextMenu = () => setContextMenu(null);

  const sendCommand = (cmd: string, values: Record<number, string>, addCR: boolean) => {
    const filled = fillQuickCommandParams(cmd, values);
    const finalCmd = addCR !== false ? `${filled}\r` : filled;

    if (Object.keys(values).length > 0) {
      const pHist: Record<string, Record<string, string[]>> = { ...paramHistory, [cmd]: { ...(paramHistory[cmd] || {}) } };
      Object.entries(values).forEach(([num, val]) => {
        if (!val) return;
        const arr = pHist[cmd][num] || [];
        const filtered = arr.filter((v) => v !== val);
        filtered.unshift(val);
        pHist[cmd][num] = filtered.slice(0, QUICK_COMMAND_PARAM_HISTORY_LIMIT);
      });
      setParamHistory(pHist);
      AppGo.SaveParamHistory(JSON.stringify(pHist)).catch(() => {});
    }

    const timestamp = new Date().toISOString();
    if (sendTarget === 'all' && connectedSessions.length > 0) {
      connectedSessions.forEach((s) => {
        AppGo.WriteTerminal(s.id, finalCmd).catch((err) => {
          warnDev('WriteTerminal failed:', err);
        });
        window.dispatchEvent(new CustomEvent('ssh-command-history', {
          detail: { sessionId: s.id, command: filled, time: timestamp, source: 'input' },
        }));
      });
      if (addToast) addToast(`${t('已发送到 ')}${connectedSessions.length}${t(' 个会话')}`, 'info', 2000);
    } else {
      AppGo.WriteTerminal(sessionId, finalCmd).catch((err) => {
        warnDev('WriteTerminal failed:', err);
      });
      window.dispatchEvent(new CustomEvent('ssh-command-history', {
        detail: { sessionId: historySessionId || sessionId, command: filled, time: timestamp, source: 'input' },
      }));
      if (addToast) addToast(t('已发送指令到终端'), 'info', 2000);
    }
  };

  const doExecute = (item: QuickCommandItem) => {
    if (!item?.command) return;
    sendCommand(item.command, paramValues, item.addCR !== false);
  };

  const doContextAction = async (action: 'addGroup' | 'addCmd' | 'edit' | 'editGroup' | 'delete' | 'execute') => {
    if (!contextMenu) return;
    const { path, type } = contextMenu;
    closeContextMenu();

    if (action === 'addGroup') {
      setDialog({ type: 'addGroup', contextPath: path, parentList: commands });
      setDlgName('');
      setDlgCmd('');
      setDlgAddCR(true);
      return;
    }

    if (action === 'addCmd') {
      const list = structuredClone(commands);
      const r = resolvePath(list, path);
      let targetChildren = list;
      if (r?.item?.type === 'group') {
        if (!r.item.children) r.item.children = [];
        targetChildren = r.item.children;
      }
      setDialog({ type: 'add', targetChildren, parentList: list, groupName: r?.item?.name || '' });
      setDlgName('');
      setDlgCmd('');
      setDlgAddCR(true);
      return;
    }

    if (action === 'edit' && type === 'command') {
      const { parent, idx } = resolvePath(commands, path);
      const item = parent[idx];
      setDialog({ type: 'edit', parent, idx });
      setDlgName(item.name || '');
      setDlgCmd(item.command || '');
      setDlgAddCR(item.addCR !== false);
      return;
    }

    if (action === 'editGroup' && type === 'group') {
      const { parent, idx } = resolvePath(commands, path);
      setDialog({ type: 'editGroup', contextPath: path });
      setDlgName(parent[idx].name || '');
      setDlgCmd('');
      setDlgAddCR(true);
      return;
    }

    if (action === 'delete') {
      try {
        const list = structuredClone(commands);
        const r = resolvePath(list, path);
        r.parent.splice(r.idx, 1);
        await AppGo.SaveQuickCommands(JSON.stringify(list));
        window.dispatchEvent(new CustomEvent('quick-commands-changed'));
        setCommands(list);
        setSelectedPath(null);
        if (addToast) addToast(t('已删除'), 'success', 1500);
      } catch {
        const data = await loadCommands();
        if (data.length > 0) setCommands(data);
        if (addToast) addToast(t('删除失败'), 'error', 2000);
      }
      return;
    }

    if (action === 'execute') {
      const { item } = resolvePath(commands, path);
      if (item && item.command) doExecute(item);
    }
  };

  const handleDlgSave = () => {
    if (!dialog) return;
    if (!dlgName.trim()) return;
    const isGroup = dialog.type === 'addGroup' || dialog.type === 'editGroup';

    if (isGroup) {
      if (dialog.type === 'addGroup') {
        const list = structuredClone(dialog.parentList || commands);
        const parts = (dialog.contextPath || '').split('/').map(Number);
        if (dialog.contextPath && parts.length === 1 && list[parts[0]]?.type === 'group') {
          list[parts[0]].children = [...(list[parts[0]].children || []), { type: 'group', name: dlgName.trim(), expanded: true, children: [], last_modified: Date.now() }];
        } else if (dialog.contextPath) {
          let cur = list;
          for (let i = 0; i < parts.length - 1; i++) cur = cur[parts[i]].children || [];
          cur.splice(parts[parts.length - 1] + 1, 0, { type: 'group', name: dlgName.trim(), expanded: true, children: [], last_modified: Date.now() });
        } else {
          list.push({ type: 'group', name: dlgName.trim(), expanded: true, children: [], last_modified: Date.now() });
        }
        save(list);
      } else if (dialog.type === 'editGroup') {
        const list = cloneAlongPath(commands, dialog.contextPath || '');
        const r = resolvePath(list, dialog.contextPath || '');
        r.parent[r.idx].name = dlgName.trim();
        r.parent[r.idx].last_modified = Date.now();
        save(list);
      }
      setDialog(null);
      return;
    }

    if (!dlgCmd.trim()) return;
    const newItem: QuickCommandItem = { name: dlgName.trim(), command: dlgCmd.trim(), addCR: dlgAddCR, last_modified: Date.now() };

    if (dialog.type === 'add') {
      dialog.targetChildren?.push(newItem);
      save(dialog.parentList || commands);
    } else if (dialog.type === 'edit') {
      const list = cloneAlongPath(commands, selectedPath || '');
      const r = resolvePath(list, selectedPath || '');
      r.parent[r.idx] = { ...r.parent[r.idx], ...newItem };
      setEditCmdName(newItem.name || '');
      setEditCmdText(newItem.command || '');
      setDirty(false);
      save(list);
    }
    setDialog(null);
  };

  const sendEditorCommand = useCallback(() => {
    const cmd = cmdEditorText.replace(/\r\n?/g, '\n');
    const text = cmd.trim();
    if (!text) return;
    const finalCmd = cmdEditorAddCR ? (`${text}\r`) : text;
    const timestamp = new Date().toISOString();
    if (sendTarget === 'all' && connectedSessions.length > 0) {
      connectedSessions.forEach((s) => {
        AppGo.WriteTerminal(s.id, finalCmd).catch((err) => {
          warnDev('WriteTerminal failed:', err);
        });
        window.dispatchEvent(new CustomEvent('ssh-command-history', {
          detail: { sessionId: s.id, command: text, time: timestamp, source: 'input' },
        }));
      });
      if (addToast) addToast(`${t('已发送到 ')}${connectedSessions.length}${t(' 个会话')}`, 'info', 2000);
    } else {
      AppGo.WriteTerminal(sessionId, finalCmd).catch((err) => {
        warnDev('WriteTerminal failed:', err);
      });
      window.dispatchEvent(new CustomEvent('ssh-command-history', {
        detail: { sessionId: historySessionId || sessionId, command: text, time: timestamp, source: 'input' },
      }));
      if (addToast) addToast(t('已发送'), 'info', 1500);
    }
    if (cmdEditorClearAfterSend) setCmdEditorText('');
  }, [addToast, cmdEditorAddCR, cmdEditorClearAfterSend, cmdEditorText, connectedSessions, sendTarget, sessionId, historySessionId, t]);

  const selectedItem = useMemo(() => {
    if (!selectedPath) return null;
    const { item } = resolvePath(commands, selectedPath);
    return item;
  }, [selectedPath, commands]);

  return {
    t,
    commands,
    setCommands,
    selectedPath,
    setSelectedPath,
    selectedItem,
    contextMenu,
    setContextMenu,
    sendTarget,
    setSendTarget,
    showCmdBar,
    setShowCmdBar,
    showCmdEditor,
    setShowCmdEditor,
    cmdEditorText,
    setCmdEditorText,
    cmdEditorAddCR,
    setCmdEditorAddCR,
    cmdEditorClearAfterSend,
    setCmdEditorClearAfterSend,
    cmdEditorShowOpts,
    setCmdEditorShowOpts,
    dialog,
    setDialog,
    dlgName,
    setDlgName,
    dlgCmd,
    setDlgCmd,
    dlgAddCR,
    setDlgAddCR,
    paramHistory,
    setParamHistory,
    paramValues,
    setParamValues,
    historyDropdown,
    setHistoryDropdown,
    historySearch,
    setHistorySearch,
    searchText,
    setSearchText,
    rootDragOver,
    setRootDragOver,
    dragVersion,
    dirty,
    setDirty,
    confirmUnsaved,
    setConfirmUnsaved,
    editGroupName,
    setEditGroupName,
    editCmdName,
    setEditCmdName,
    editCmdText,
    setEditCmdText,
    handleDragStart,
    clearDrag,
    handleDropItem,
    handleDropToRoot,
    handleMove,
    handleSelect,
    handleConfirmSave,
    handleConfirmDiscard,
    handleContextMenu,
    closeContextMenu,
    doExecute,
    doContextAction,
    handleDlgSave,
    sendEditorCommand,
    save,
  };
}
