import { createPortal } from 'react-dom';
import { Copy, Link, X } from 'lucide-react';
import { useLayoutEffect, useRef, useState } from 'react';
import type { config } from '../../../wailsjs/go/models.ts';
import { useOverlayScrollLock } from '../../hooks/useOverlayScrollLock.ts';
import { clampMenuPosition } from '../../utils/menuPosition.ts';
import { buildSSHLink, resolveServerPassword } from '../../utils/serverLink.ts';
import type { TopbarSession } from '../AppTopbar.tsx';
import type { TabContextMenuState } from './overlayTypes.ts';

interface TabContextMenuOverlayProps {
  tabContextMenu: TabContextMenuState;
  sessions: TopbarSession[];
  servers: config.Connection[];
  t: (key: string, vars?: Record<string, unknown>) => string;
  addToast: (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;
  canCopySessionPassword: (sessionId: string) => boolean;
  setTabContextMenu: (menu: TabContextMenuState | null) => void;
  handleCopySessionPassword: (sessionId: string) => Promise<void>;
  forceCloseSession: (sessionId: string) => void;
  closeAllSessions: () => Promise<void>;
}

/** 标签右键菜单 */
export default function TabContextMenuOverlay({
  tabContextMenu,
  sessions,
  servers,
  t,
  addToast,
  canCopySessionPassword,
  setTabContextMenu,
  handleCopySessionPassword,
  forceCloseSession,
  closeAllSessions,
}: TabContextMenuOverlayProps) {
  useOverlayScrollLock(true);

  // 按真实渲染尺寸钳制，避免贴近屏幕底部时溢出
  const panelRef = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ x: tabContextMenu.x, y: tabContextMenu.y });

  useLayoutEffect(() => {
    const el = panelRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    setPos(clampMenuPosition(tabContextMenu.x, tabContextMenu.y, rect.width, rect.height));
  }, [tabContextMenu]);

  const session = sessions.find((item) => item.id === tabContextMenu.sessionId);
  const server = session?.serverId ? servers.find((item) => item.id === session.serverId) : undefined;
  // 本地/串口会话没有对应的服务器配置，不展示复制连接信息
  const showCopySessionPassword = server ? canCopySessionPassword(tabContextMenu.sessionId) : false;

  const copyText = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      addToast(t('已复制'), 'success', 2000);
    } catch {
      addToast(t('复制失败'), 'error', 3000);
    }
  };

  const copySSHLink = async () => {
    if (!server) return;
    const password = await resolveServerPassword(server.id);
    await copyText(buildSSHLink(server, password));
  };

  const content = (
    <div
      ref={panelRef}
      className="tab-context-menu"
      data-tab-context-menu="true"
      style={{ left: pos.x, top: pos.y }}
    >
      {server && (
        <>
          <div
            className="tab-context-menu-item"
            onClick={() => {
              setTabContextMenu(null);
              void copyText(server.host);
            }}
          >
            <Copy size={14} /> {t('复制主机地址')}
          </div>
          {showCopySessionPassword && (
            <div
              className="tab-context-menu-item"
              onClick={() => {
                const sessionId = tabContextMenu.sessionId;
                setTabContextMenu(null);
                void handleCopySessionPassword(sessionId);
              }}
            >
              <Copy size={14} /> {t('复制密码')}
            </div>
          )}
          <div
            className="tab-context-menu-item"
            onClick={() => {
              setTabContextMenu(null);
              void copyText(String(server.port || 22));
            }}
          >
            <Copy size={14} /> {t('复制端口')}
          </div>
          <div
            className="tab-context-menu-item"
            onClick={() => {
              setTabContextMenu(null);
              void copySSHLink();
            }}
          >
            <Link size={14} /> {t('复制 SSH 链接')}
          </div>
          <div className="h-px my-1 bg-line" />
        </>
      )}
      <div
        className="tab-context-menu-item"
        onClick={() => {
          const sessionId = tabContextMenu.sessionId;
          setTabContextMenu(null);
          forceCloseSession(sessionId);
        }}
      >
        <X size={14} /> {t('关闭连接')}
      </div>
      {sessions.length >= 2 && (
        <>
          <div className="h-px my-1 bg-line" />
          <div
            className="tab-context-menu-item"
            onClick={() => {
              setTabContextMenu(null);
              closeAllSessions();
            }}
          >
            <X size={14} /> {t('关闭全部')}
          </div>
        </>
      )}
    </div>
  );

  if (typeof document !== 'undefined' && document.body) {
    return createPortal(content, document.body);
  }
  return content;
}
