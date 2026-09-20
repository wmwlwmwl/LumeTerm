import { createPortal } from 'react-dom';
import { Copy, Folder, Link, PenLine, SquarePen, Trash2, X } from 'lucide-react';
import type React from 'react';
import type { config } from '../../../wailsjs/go/models.ts';
import { Z } from '../../constants/zIndex.ts';
import { useTranslation } from '../../i18n.ts';
import { ContextMenu, MenuList, MenuPanel } from '../ui';
import { useOverlayScrollLock } from '../../hooks/useOverlayScrollLock.ts';
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
}: ServerContextMenuProps) {
  const { t } = useTranslation();
  useOverlayScrollLock(!!groupHeaderMenu || !!menuServer);

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
        style={{ left: menuPos.x, top: menuPos.y, zIndex: Z.MENU }}
      >
        <div className="relative">
          <MenuList
            items={[
              { label: t('连接'), icon: <Link size={14} />, onSelect: () => { onConnect(menuServer); } },
              { label: t('编辑配置'), icon: <SquarePen size={14} />, onSelect: () => { triggerEdit(menuServer, menuSourceRef.current); } },
              { label: t('克隆'), icon: <Copy size={14} />, onSelect: () => { onClone(menuServer, getEditAnimationPayload(menuServer, menuSourceRef.current)); } },
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
