import { createPortal } from 'react-dom';
import { Copy, Folder, Link, PenLine, SquarePen, Trash2, X } from 'lucide-react';
import { useLayoutEffect, useRef, useState } from 'react';
import type React from 'react';
import type { config } from '../../../wailsjs/go/models.ts';
import { Z } from '../../constants/zIndex.ts';
import { useTranslation } from '../../i18n.ts';
import { ContextMenu, MenuList, MenuPanel } from '../ui';
import { useOverlayScrollLock } from '../../hooks/useOverlayScrollLock.ts';
import { clampMenuPosition } from '../../utils/menuPosition.ts';
import { buildSSHLink, resolveServerPassword } from '../../utils/serverLink.ts';
import type { MenuItem } from '../ui';
import { MENU_ESTIMATED_WIDTH } from './serverListTypes.ts';

export interface ServerContextMenuProps {
  groupHeaderMenu: { groupName: string; x: number; y: number } | null;
  setGroupHeaderMenu: React.Dispatch<React.SetStateAction<{ groupName: string; x: number; y: number } | null>>;
  handleRenameGroupFromMenu: () => Promise<void>;
  menuServer: config.Connection | null;
  menuPos: { x: number; y: number };
  closeServerMenu: () => void;
  onConnect: (server: config.Connection) => void;
  triggerEdit: (server: config.Connection, source: HTMLElement | null) => void;
  menuSourceRef: React.RefObject<HTMLElement | null>;
  onClone: (server: config.Connection, payload: unknown) => void;
  getEditAnimationPayload: (server: config.Connection, root: HTMLElement | null) => unknown;
  onMoveGroup?: (id: string, group: string) => void;
  submenuToggleRef: React.RefObject<boolean>;
  setGroupMenu: React.Dispatch<React.SetStateAction<boolean>>;
  onDelete: (id: string) => void;
  groupMenu: boolean;
  existingGroups: string[];
  addToast: (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;
}

export function ServerContextMenu({
  groupHeaderMenu,
  setGroupHeaderMenu,
  handleRenameGroupFromMenu,
  menuServer,
  menuPos,
  closeServerMenu,
  onConnect,
  triggerEdit,
  menuSourceRef,
  onClone,
  getEditAnimationPayload,
  onMoveGroup,
  submenuToggleRef,
  setGroupMenu,
  onDelete,
  groupMenu,
  existingGroups,
  addToast,
}: ServerContextMenuProps) {
  const { t } = useTranslation();
  useOverlayScrollLock(!!groupHeaderMenu || !!menuServer);

  // 右键菜单实际高度远超估算值，按真实渲染尺寸重新钳制，避免贴近屏幕底部时溢出
  const panelRef = useRef<HTMLDivElement>(null);
  const [panelPos, setPanelPos] = useState(menuPos);

  useLayoutEffect(() => {
    const el = panelRef.current;
    if (!el) {
      setPanelPos(menuPos);
      return;
    }
    const rect = el.getBoundingClientRect();
    setPanelPos(clampMenuPosition(menuPos.x, menuPos.y, rect.width, rect.height));
  }, [menuPos, menuServer]);

  const copyText = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      addToast(t('已复制'), 'success', 2000);
    } catch {
      addToast(t('复制失败'), 'error', 3000);
    }
  };

  const copyPassword = async (server: config.Connection) => {
    try {
      const password = await resolveServerPassword(server.id);
      if (!password) throw new Error('empty password');
      await copyText(password);
    } catch {
      addToast(t('复制失败'), 'error', 3000);
    }
  };

  const copySSHLink = async (server: config.Connection) => {
    const password = await resolveServerPassword(server.id);
    await copyText(buildSSHLink(server, password));
  };

  const menuServerNode = menuServer ? (
    <>
      <div
        className="fixed inset-0"
        style={{ zIndex: Z.MENU_BACKDROP }}
        onMouseDown={() => closeServerMenu()}
        onContextMenu={(e) => {
          e.preventDefault();
          closeServerMenu();
        }}
      />
      <MenuPanel
        minWidth={MENU_ESTIMATED_WIDTH}
        data-context-menu="true"
        className="fixed overflow-visible animate-[fadeIn_0.12s_ease]"
        style={{ left: panelPos.x, top: panelPos.y, zIndex: Z.MENU }}
      >
        <div className="relative" ref={panelRef}>
          <MenuList
            items={[
              { label: t('连接'), icon: <Link size={14} />, onSelect: () => { onConnect(menuServer); } },
              { label: t('编辑配置'), icon: <SquarePen size={14} />, onSelect: () => { triggerEdit(menuServer, menuSourceRef.current); } },
              { label: t('克隆'), icon: <Copy size={14} />, onSelect: () => { onClone(menuServer, getEditAnimationPayload(menuServer, menuSourceRef.current)); } },
              { label: t('复制 IP'), icon: <Copy size={14} />, onSelect: () => { void copyText(menuServer.host); } },
              { label: t('复制密码'), icon: <Copy size={14} />, onSelect: () => { void copyPassword(menuServer); } },
              { label: t('复制端口'), icon: <Copy size={14} />, onSelect: () => { void copyText(String(menuServer.port || 22)); } },
              { label: t('复制 SSH 链接'), icon: <Link size={14} />, onSelect: () => { void copySSHLink(menuServer); } },
              ...(onMoveGroup ? [{
                label: t('移动到分组'),
                icon: <Folder size={14} />,
                onSelect: () => {
                  submenuToggleRef.current = true;
                  setGroupMenu((prev) => !prev);
                },
              } as MenuItem] : []),
              'separator',
              {
                label: t('删除'),
                icon: <Trash2 size={14} />,
                danger: true,
                onSelect: () => {
                  void (async () => {
                    if (await window.lumeDialog?.confirm(`${t('确定删除服务器')}「${menuServer.name || menuServer.host}」？`)) {
                      onDelete(menuServer.id);
                    }
                  })();
                },
              },
            ] as MenuItem[]}
            onClose={() => {
              if (submenuToggleRef.current) {
                submenuToggleRef.current = false;
                return;
              }
              closeServerMenu();
            }}
          />
          {groupMenu && onMoveGroup && (
            <MenuPanel
              minWidth={MENU_ESTIMATED_WIDTH}
              className="absolute left-full top-0 animate-[fadeIn_0.12s_ease]"
              style={{ zIndex: Z.SUBMENU }}
            >
              <MenuList
                items={[
                  ...existingGroups.filter((g) => g !== (menuServer.group || '')).map((g): MenuItem => ({
                    label: g,
                    icon: <Folder size={13} />,
                    onSelect: () => { onMoveGroup(menuServer.id, g); },
                  })),
                  ...(menuServer.group ? [{
                    label: t('移出分组'),
                    icon: <X size={13} />,
                    onSelect: () => { onMoveGroup(menuServer.id, ''); },
                  } as MenuItem] : []),
                ] as MenuItem[]}
                onClose={closeServerMenu}
              />
            </MenuPanel>
          )}
        </div>
      </MenuPanel>
    </>
  ) : null;

  const menuServerPortal = menuServerNode
    ? (typeof document !== 'undefined' && document.body ? createPortal(menuServerNode, document.body) : menuServerNode)
    : null;

  return (
    <>
      {groupHeaderMenu && (
        <ContextMenu
          x={groupHeaderMenu.x}
          y={groupHeaderMenu.y}
          minWidth={MENU_ESTIMATED_WIDTH}
          items={[
            { label: t('重命名分组'), icon: <PenLine size={14} />, onSelect: () => { void handleRenameGroupFromMenu(); } },
          ]}
          onClose={() => setGroupHeaderMenu(null)}
        />
      )}
      {menuServerPortal}
    </>
  );
}
