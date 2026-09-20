import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type React from 'react';
import { EditorState, EditorView, type Extension } from '@uiw/react-codemirror';
import { useTranslation } from '../../i18n.ts';
import { useEditorWordWrap } from '../../utils/editorWordWrap.ts';
import {
  editorActiveLineTheme,
  getLanguage,
  gotoLineKeymap,
} from './fileEditorLanguages.ts';
import {
  readPreferredExternalApp,
  type FileEditorProps,
} from './fileEditorTypes.ts';

export function useFileEditor(props: FileEditorProps) {
  const {
    files,
    activePath,
    onSave,
    onCloseFile,
    onCloseAll,
    mode = 'modal',
    splitPosition = 'right',
    isActive = true,
  } = props;

  const { t, lang: i18nLang } = useTranslation();
  const wordWrapEnabled = useEditorWordWrap();

  const [editedContents, setEditedContents] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [minimized, setMinimized] = useState(false);
  const [preferredExternalApp, setPreferredExternalApp] = useState(() => readPreferredExternalApp());

  useEffect(() => {
    const refreshPreferred = () => setPreferredExternalApp(readPreferredExternalApp());
    window.addEventListener('storage', refreshPreferred);
    window.addEventListener('focus', refreshPreferred);
    return () => {
      window.removeEventListener('storage', refreshPreferred);
      window.removeEventListener('focus', refreshPreferred);
    };
  }, []);

  // 打开文件时自动恢复：仅在激活文件变化时恢复（依赖里不含 minimized，
  // 否则点击最小化的瞬间就会立刻被恢复，导致无法最小化）
  useEffect(() => {
    if (minimized && activePath) setMinimized(false);
  }, [activePath]);

  const [contextMenu, setContextMenu] = useState<{ x: number; y: number; hasSelection: boolean } | null>(null);

  const activeFile = files.find((f) => f.path === activePath) || files[0];

  const [popupPos, setPopupPos] = useState<{ x: number; y: number; w: number; h: number }>(() => {
    const saved = localStorage.getItem('fileEditorPopupPos');
    if (saved) {
      try {
        const parsed = JSON.parse(saved);
        if (parsed && typeof parsed.x === 'number' && typeof parsed.y === 'number' && typeof parsed.w === 'number' && typeof parsed.h === 'number') {
          return parsed as { x: number; y: number; w: number; h: number };
        }
      } catch (_) { /* 忽略损坏的持久化位置 */ }
    }
    return { x: window.innerWidth - 660, y: 60, w: 620, h: 500 };
  });

  const popupPosRef = useRef(popupPos);
  const isDraggingRef = useRef(false);
  const dragStartRef = useRef({ x: 0, y: 0, px: 0, py: 0 });

  useEffect(() => {
    popupPosRef.current = popupPos;
  }, [popupPos]);

  const currentContent = activeFile
    ? (editedContents[activeFile.path] !== undefined ? editedContents[activeFile.path] : activeFile.content)
    : '';

  const isModified = activeFile ? currentContent !== activeFile.content : false;

  const byteSize = useMemo(() => new Blob([currentContent]).size, [currentContent]);

  const handleChange = useCallback((value: string) => {
    if (!activeFile) return;
    setEditedContents((prev) => ({ ...prev, [activeFile.path]: value }));
  }, [activeFile]);

  const handleContextMenu = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    const sel = window.getSelection()?.toString() || '';
    setContextMenu({ x: e.clientX, y: e.clientY, hasSelection: sel.length > 0 });
  };

  const handleMenuAction = (action: 'copy' | 'paste' | 'cut' | 'selectAll') => {
    setContextMenu(null);
    switch (action) {
      case 'copy':
        document.execCommand('copy');
        break;
      case 'paste':
        navigator.clipboard.readText().then((text) => {
          document.execCommand('insertText', false, text);
        }).catch(() => {});
        break;
      case 'cut':
        document.execCommand('cut');
        break;
      case 'selectAll':
        document.execCommand('selectAll');
        break;
    }
  };

  const handleSave = useCallback(async () => {
    if (!activeFile || !isModified) return;
    setSaving(true);
    try {
      await onSave(activeFile.path, currentContent);
      setEditedContents((prev) => {
        const next = { ...prev };
        delete next[activeFile.path];
        return next;
      });
    } finally {
      setSaving(false);
    }
  }, [activeFile, isModified, currentContent, onSave]);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault();
        if (isModified && !saving) handleSave();
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [isModified, saving, handleSave]);

  const closeFileWithConfirm = async (path: string) => {
    const f = files.find((x) => x.path === path);
    const edited = editedContents[path];
    if (f && edited !== undefined && edited !== f.content) {
      const ok = await window.lumeDialog?.confirm(t('文件有未保存的修改，确定关闭？'));
      if (!ok) return;
    }
    setEditedContents((prev) => {
      const next = { ...prev };
      delete next[path];
      return next;
    });
    onCloseFile(path);
  };

  const handleCloseAllEditors = async () => {
    const hasModified = files.some((f) => {
      const edited = editedContents[f.path];
      return edited !== undefined && edited !== f.content;
    });
    if (hasModified && !(await window.lumeDialog?.confirm(t('有文件未保存，确定全部关闭？')))) return;
    onCloseAll();
  };

  const startPopupDrag = (e: React.MouseEvent) => {
    if (e.button !== 0) return;
    isDraggingRef.current = true;
    dragStartRef.current = { x: e.clientX, y: e.clientY, px: popupPosRef.current.x, py: popupPosRef.current.y };
    document.body.style.userSelect = 'none';

    const onMove = (ev: MouseEvent) => {
      if (!isDraggingRef.current) return;
      const dx = ev.clientX - dragStartRef.current.x;
      const dy = ev.clientY - dragStartRef.current.y;
      const next = {
        ...popupPosRef.current,
        x: Math.max(0, Math.min(window.innerWidth - 200, dragStartRef.current.px + dx)),
        y: Math.max(40, Math.min(window.innerHeight - 100, dragStartRef.current.py + dy)),
      };
      setPopupPos(next);
    };

    const onUp = () => {
      isDraggingRef.current = false;
      document.body.style.userSelect = '';
      localStorage.setItem('fileEditorPopupPos', JSON.stringify(popupPosRef.current));
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };

    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  };

  const startPopupResize = (dir: string) => (e: React.MouseEvent) => {
    e.stopPropagation();
    if (e.button !== 0) return;
    isDraggingRef.current = true;
    const startX = e.clientX;
    const startY = e.clientY;
    const start = { ...popupPosRef.current };
    const MIN_W = 320;
    const MIN_H = 200;
    document.body.style.userSelect = 'none';

    const onMove = (ev: MouseEvent) => {
      const dx = ev.clientX - startX;
      const dy = ev.clientY - startY;
      let { x, y, w, h } = start;
      if (dir.includes('e')) w = Math.max(MIN_W, Math.min(window.innerWidth - start.x, start.w + dx));
      if (dir.includes('s')) h = Math.max(MIN_H, Math.min(window.innerHeight - start.y, start.h + dy));
      if (dir.includes('w')) {
        w = Math.max(MIN_W, Math.min(start.x + start.w, start.w - dx));
        x = start.x + start.w - w;
      }
      if (dir.includes('n')) {
        h = Math.max(MIN_H, Math.min(start.y + start.h - 40, start.h - dy));
        y = start.y + start.h - h;
      }
      setPopupPos({ x, y, w, h });
    };

    const onUp = () => {
      isDraggingRef.current = false;
      document.body.style.userSelect = '';
      localStorage.setItem('fileEditorPopupPos', JSON.stringify(popupPosRef.current));
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };

    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  };

  const lang = useMemo(
    () => (activeFile ? getLanguage(activeFile.path || activeFile.name) : null),
    [activeFile],
  );

  const editorPhrases = useMemo(() => EditorState.phrases.of({
    'Go to line': t('跳转到行'),
    'go': t('跳转'),
    'Find': t('查找'),
    'Replace': t('替换'),
    'next': t('下一个'),
    'previous': t('上一个'),
    'all': t('全部'),
    'match case': t('区分大小写'),
    'regexp': t('正则'),
    'by word': t('按词'),
    'replace': t('替换'),
    'replace all': t('全部替换'),
    'replaced $ matches': t('替换了 $ 处'),
    'replaced match on line $': t('在第 $ 行替换了匹配'),
    'current match': t('当前匹配'),
    'on line': t('第'),
    'close': t('关闭'),
  }), [i18nLang, t]);

  const extensions = useMemo(() => {
    const exts: Extension[] = [gotoLineKeymap, editorActiveLineTheme, editorPhrases];
    if (wordWrapEnabled) exts.push(EditorView.lineWrapping);
    if (lang) exts.push(lang);
    return exts;
  }, [lang, editorPhrases, wordWrapEnabled]);

  const ext = activeFile ? (activeFile.name.split('.').pop() || '').toLowerCase() : '';

  useEffect(() => {
    const host = document.getElementById('editor-split-host');
    const container = document.getElementById('session-editor-container');
    if (!host || !container) return;
    if (!isActive || mode !== 'split') return;

    const resizer = document.getElementById('editor-split-resizer');
    const mainContent = document.getElementById('editor-main-content');
    if (resizer) {
      resizer.style.display = '';
      resizer.classList.remove('hotzone-left', 'hotzone-right');
      resizer.classList.add(splitPosition === 'left' ? 'hotzone-left' : 'hotzone-right');
    }
    if (splitPosition === 'left') {
      host.style.order = '0';
      if (resizer) resizer.style.order = '1';
      if (mainContent) mainContent.style.order = '2';
    } else {
      if (mainContent) mainContent.style.order = '0';
      if (resizer) resizer.style.order = '1';
      host.style.order = '2';
    }

    if (splitPosition === 'bottom') {
      container.style.flexDirection = 'column';
      host.style.width = '100%';
      host.style.height = '50%';
      host.style.minWidth = '0px';
      host.style.maxWidth = 'none';
      host.style.minHeight = '200px';
      host.style.maxHeight = '70%';
      host.style.borderTop = '1px solid var(--border)';
      host.style.borderLeft = 'none';
      host.style.borderRight = 'none';
      host.style.order = '2';
    } else {
      container.style.flexDirection = 'row';
      host.style.width = '50%';
      host.style.height = '100%';
      host.style.minWidth = '320px';
      host.style.maxWidth = '70%';
      host.style.minHeight = '0px';
      host.style.maxHeight = 'none';
      host.style.borderTop = 'none';
      host.style.borderLeft = splitPosition === 'right' ? '1px solid var(--border)' : 'none';
      host.style.borderRight = splitPosition === 'left' ? '1px solid var(--border)' : 'none';
      host.style.order = splitPosition === 'left' ? '0' : '2';
    }

    // editor-split-host 由本组件独占：卸载/失活时无条件归零（传输队列为
    // 独立浮动面板，不再依赖本 host），避免切终端时 host 宽度残留导致
    // 编辑器“不见了”或切回后无法恢复
    return () => {
      const nextResizer = document.getElementById('editor-split-resizer');
      const nextMainContent = document.getElementById('editor-main-content');
      if (nextResizer) nextResizer.style.display = 'none';
      if (nextMainContent) nextMainContent.style.order = '1';
      container.style.flexDirection = 'row';
      host.style.width = '0px';
      host.style.height = '100%';
      host.style.minWidth = '0px';
      host.style.maxWidth = '0px';
      host.style.minHeight = '0px';
      host.style.maxHeight = '0px';
      host.style.borderLeft = 'none';
      host.style.borderRight = 'none';
      host.style.borderTop = 'none';
      host.style.order = '2';
    };
  }, [mode, splitPosition, isActive]);

  return {
    t,
    editedContents,
    saving,
    minimized,
    setMinimized,
    preferredExternalApp,
    setPreferredExternalApp,
    contextMenu,
    setContextMenu,
    activeFile,
    currentContent,
    isModified,
    byteSize,
    handleChange,
    handleContextMenu,
    handleMenuAction,
    handleSave,
    closeFileWithConfirm,
    handleCloseAllEditors,
    popupPos,
    startPopupDrag,
    startPopupResize,
    lang,
    extensions,
    ext,
  };
}
