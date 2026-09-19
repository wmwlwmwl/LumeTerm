import { useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Search, X } from 'lucide-react';
import Tiptop from '../Tiptop.tsx';
import { useOverlayScrollLock } from '../../hooks/useOverlayScrollLock.ts';
import type { config } from '../../../wailsjs/go/models.ts';
import type { TopbarSession } from '../AppTopbar.tsx';
import type { SessionAuthPrompt } from '../../hooks/useSessionConnections.ts';

const PANEL_WIDTH = 300;

interface SessionListOverlayProps {
  sessionListRef: React.RefObject<HTMLDivElement | null>;
  sessionListPos: { x: number; y: number };
  sessionListQuery: string;
  setSessionListQuery: (q: string) => void;
  t: (key: string, vars?: Record<string, unknown>) => string;
  sessions: TopbarSession[];
  /** 完整服务器目录：未连接项点击即连接 */
  servers?: config.Connection[];
  connectServer?: (server: config.Connection) => Promise<void>;
  activeSessionId: string | null;
  sessionAuthPrompts: Record<string, SessionAuthPrompt>;
  handleTabClick: (sessionId: string) => void;
  setShowSessionList: (v: boolean) => void;
  closeSession: (sessionId: string, e?: React.MouseEvent) => Promise<void>;
}

type QuickRow =
  | { kind: 'session'; key: string; s: TopbarSession }
  | { kind: 'server'; key: string; v: config.Connection };

/** 服务器快连面板：搜索已开会话（切换）与全部服务器目录（点击直连），
 *  ↑↓ 选择 / Enter 打开 / Esc 关闭。 */
export default function SessionListOverlay({
  sessionListRef,
  sessionListPos,
  sessionListQuery,
  setSessionListQuery,
  t,
  sessions,
  servers,
  connectServer,
  activeSessionId,
  sessionAuthPrompts,
  handleTabClick,
  setShowSessionList,
  closeSession,
}: SessionListOverlayProps) {
  useOverlayScrollLock(true);
  const [activeIdx, setActiveIdx] = useState(0);
  const bodyRef = useRef<HTMLDivElement | null>(null);

  const close = () => setShowSessionList(false);

  const rows = useMemo<QuickRow[]>(() => {
    const q = (sessionListQuery || '').trim().toLowerCase();
    const hit = (...fields: Array<string | undefined>) => !q || fields.some((f) => (f || '').toLowerCase().includes(q));
    const openedByServerId = new Map(sessions.map((s) => [s.serverId, s]));
    const sessionRows: QuickRow[] = sessions
      .filter((s) => hit(s.serverName, s.host))
      .map((s) => ({ kind: 'session', key: `s-${s.id}`, s }));
    const serverRows: QuickRow[] = (servers || [])
      .filter((v) => !openedByServerId.has(String(v.id)))
      .filter((v) => hit(v.name, v.host, v.group))
      .map((v) => ({ kind: 'server', key: `c-${v.id}`, v }));
    return [...sessionRows, ...serverRows];
  }, [sessions, servers, sessionListQuery]);

  const sessionGroupById = useMemo(() => {
    const m = new Map<string, string>();
    for (const v of servers || []) {
      const g = v.group;
      if (typeof g === 'string' && g) m.set(String(v.id), g);
    }
    return m;
  }, [servers]);

  useEffect(() => {
    setActiveIdx((current) => Math.min(current, Math.max(0, rows.length - 1)));
  }, [rows.length]);

  useEffect(() => {
    bodyRef.current?.querySelector('[data-active="true"]')?.scrollIntoView({ block: 'nearest' });
  }, [activeIdx]);

  const activate = (row: QuickRow | undefined) => {
    if (!row) return;
    close();
    if (row.kind === 'session') {
      handleTabClick(row.s.id);
    } else if (connectServer) {
      void connectServer(row.v);
    }
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setActiveIdx((i) => Math.min(i + 1, rows.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActiveIdx((i) => Math.max(0, i - 1));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      activate(rows[activeIdx]);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      close();
    }
  };

  const content = (
    <div
      ref={sessionListRef}
      className="tab-context-menu max-h-[400px] flex flex-col"
      data-session-list-overlay="true"
      style={{
        left: Math.min(Math.max(8, sessionListPos.x - PANEL_WIDTH), Math.max(8, (window.innerWidth || 1280) - PANEL_WIDTH - 8)),
        top: sessionListPos.y,
        width: PANEL_WIDTH,
      }}
    >
      <div className="relative py-1.5 px-2 border-b border-line">
        <input
          id="app-overlays-session-search"
          name="app-overlays-session-search"
          autoComplete="off"
          type="text"
          value={sessionListQuery}
          onChange={(e) => setSessionListQuery(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder={t('搜索服务器')}
          autoFocus
          className="w-full pt-1 pb-1 pl-[26px] pr-2 text-sm bg-sunken border border-line rounded-sm text-primary outline-none"
        />
        <Search size={13} className="absolute left-[14px] top-1/2 -translate-y-1/2 text-tertiary" />
      </div>
      <div className="overflow-y-auto flex-1 min-h-0" ref={bodyRef}>
        {rows.map((row, index) => {
          const isActiveRow = index === activeIdx;
          if (row.kind === 'session') {
            const s = row.s;
            return (
              <div
                key={row.key}
                data-active={isActiveRow}
                className="tab-context-menu-item"
                style={{
                  fontWeight: activeSessionId === s.id ? 700 : 400,
                  color: activeSessionId === s.id ? 'var(--accent)' : 'var(--text-secondary)',
                  background: isActiveRow ? 'var(--accent-dim)' : undefined,
                }}
                onClick={() => activate(row)}
                onMouseEnter={() => setActiveIdx(index)}
              >
                <span className={`status-dot ${sessionAuthPrompts[s.id] ? 'attention' : s.status === 'connecting' ? 'connecting' : s.status === 'connected' ? 'online' : 'offline'}`} />
                <span className="flex-1 truncate">{s.serverName}</span>
                {sessionGroupById.get(String(s.serverId)) && (
                  <span className="text-xs text-muted shrink-0">{sessionGroupById.get(String(s.serverId))}</span>
                )}
                <Tiptop text={t('关闭')} placement="bottom">
                  <span
                    onClick={(e) => { e.stopPropagation(); void closeSession(s.id, e); }}
                    aria-label={t('关闭')}
                    className="cursor-pointer flex items-center opacity-50 shrink-0"
                  >
                    <X size={13} />
                  </span>
                </Tiptop>
              </div>
            );
          }
          const v = row.v;
          return (
            <div
              key={row.key}
              data-active={isActiveRow}
              className="tab-context-menu-item"
              style={{ color: 'var(--text-secondary)', background: isActiveRow ? 'var(--accent-dim)' : undefined }}
              onClick={() => activate(row)}
              onMouseEnter={() => setActiveIdx(index)}
            >
              <span className="status-dot offline" />
              <span className="flex-1 truncate">{v.name || v.host}</span>
              <span className="text-xs text-muted shrink-0">{v.group}</span>
            </div>
          );
        })}
        {rows.length === 0 && (
          <div className="py-3 px-4 text-sm text-tertiary text-center">{t('无匹配结果')}</div>
        )}
      </div>
    </div>
  );

  if (typeof document !== 'undefined' && document.body) {
    return createPortal(content, document.body);
  }
  return content;
}
