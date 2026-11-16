import { Monitor } from 'lucide-react';
import { useTranslation } from '../i18n.ts';
import { ServerCardItem } from './serverList/ServerCardItem.tsx';
import { ServerContextMenu } from './serverList/ServerContextMenu.tsx';
import { ServerGroupHeader } from './serverList/ServerGroupHeader.tsx';
import type { ServerListProps } from './serverList/serverListTypes.ts';
import { ServerTableItem } from './serverList/ServerTableItem.tsx';
import { useServerList } from './serverList/useServerList.ts';

export default function ServerList(props: ServerListProps) {
  const {
    servers,
    pingEnabled,
    pings,
    viewMode = 'grid',
    hideSensitive = false,
    onConnect,
    onClone,
    onDelete,
    onMoveGroup,
    selectionMode = false,
    onSelectChange,
    onGroupDelete,
  } = props;

  const { t } = useTranslation();

  const {
    menuServer,
    menuPos,
    groupHeaderMenu,
    setGroupHeaderMenu,
    groupMenu,
    setGroupMenu,
    menuSourceRef,
    submenuToggleRef,
    closeServerMenu,
    tryConnect,
    pointerSelectHandlers,
    connectedSessionMap,
    mask,
    getEditAnimationPayload,
    triggerEdit,
    handleContextMenu,
    isActive,
    hasSession,
    getSaveFlowTokens,
    selectedSet,
    allGroupServerIds,
    handleServerClick,
    handleShiftClick,
    handleGroupToggleSelect,
    isGroupSelected,
    isGroupPartiallySelected,
    existingGroups,
    toggleGroup,
    openGroupHeaderMenu,
    handleRenameGroupFromMenu,
    moveGroup,
    flatItems,
  } = useServerList(props);

  if (servers.length === 0) {
    return (
      <div className="empty-state mt-5">
        <div className="empty-state-icon flex items-center justify-center"><Monitor size={48} strokeWidth={1.5} /></div>
        <div className="empty-state-text">
          {t('暂无服务器')}
        </div>
      </div>
    );
  }

  const commonItemProps = {
    pingEnabled,
    pings,
    connectedSessionMap,
    isActive,
    hasSession,
    getSaveFlowTokens,
    selectedSet,
    selectionMode,
    handleShiftClick,
    handleServerClick,
    tryConnect,
    pointerSelectHandlers,
    handleContextMenu,
    onSelectChange,
    hideSensitive,
    mask,
    triggerEdit,
  };

  const groupHeaderProps = {
    selectionMode,
    isGroupSelected,
    isGroupPartiallySelected,
    handleGroupToggleSelect,
    toggleGroup,
    openGroupHeaderMenu,
    onGroupDelete,
    allGroupServerIds,
    moveGroup,
  };

  return (
    <>
      {viewMode === 'grid' ? (
        <div className="server-grid">
          {flatItems.map((item, idx) =>
            item.type === 'header' ? (
              <ServerGroupHeader
                key={`__group_${item.groupName || 'ungrouped'}`}
                groupName={item.groupName}
                count={item.count}
                collapsed={item.collapsed}
                {...groupHeaderProps}
              />
            ) : (
              <ServerCardItem
                key={item.server.id}
                server={item.server}
                flatIdx={idx}
                {...commonItemProps}
              />
            ),
          )}
        </div>
      ) : (
        <div className="server-table-container">
          <table className="server-table">
            <thead>
              <tr>
                {selectionMode && <th className="w-9"></th>}
                <th>{t('系统')}</th>
                <th>{t('别名')}</th>
                <th>{t('主机地址')}</th>
                <th>{t('用户名')}</th>
                <th>{t('状态')}</th>
                <th>{t('操作')}</th>
              </tr>
            </thead>
            <tbody>
              {flatItems.map((item, idx) =>
                item.type === 'header' ? (
                  <ServerGroupHeader
                    key={`__group_${item.groupName || 'ungrouped'}`}
                    groupName={item.groupName}
                    count={item.count}
                    collapsed={item.collapsed}
                    isTableView
                    {...groupHeaderProps}
                  />
                ) : (
                  <ServerTableItem
                    key={item.server.id}
                    server={item.server}
                    flatIdx={idx}
                    {...commonItemProps}
                  />
                ),
              )}
            </tbody>
          </table>
        </div>
      )}

      <ServerContextMenu
        groupHeaderMenu={groupHeaderMenu}
        setGroupHeaderMenu={setGroupHeaderMenu}
        handleRenameGroupFromMenu={handleRenameGroupFromMenu}
        menuServer={menuServer}
        menuPos={menuPos}
        closeServerMenu={closeServerMenu}
        onConnect={onConnect}
        triggerEdit={triggerEdit}
        menuSourceRef={menuSourceRef}
        onClone={onClone}
        getEditAnimationPayload={getEditAnimationPayload}
        onMoveGroup={onMoveGroup}
        submenuToggleRef={submenuToggleRef}
        setGroupMenu={setGroupMenu}
        onDelete={onDelete}
        groupMenu={groupMenu}
        existingGroups={existingGroups}
        addToast={props.addToast}
      />
    </>
  );
}
