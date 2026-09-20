import { CheckSquare, Download, Folder, Monitor, Plus, Trash2, X } from 'lucide-react';
import type React from 'react';
import type { config } from '../../../wailsjs/go/models.ts';
import { Z } from '../../constants/zIndex.ts';
import { useTranslation } from '../../i18n.ts';

export interface DashboardBatchOperationBarProps {
  servers: config.Connection[];
  selectedIds: string[];
  onSelectChange: (payload: string | string[] | Array<{ id: string; selected: boolean }>) => void;
  onExitSelectionMode?: () => void;
  onBatchConnect?: (ids: string[]) => void;
  onBatchMoveGroup?: (ids: string[], group: string) => void;
  onBatchExport?: (ids: string[]) => void;
  onBatchDelete?: (ids: string[]) => void;
  moveGroupMenuRef: React.RefObject<HTMLDivElement | null>;
  showMoveGroupDropdown: boolean;
  setShowMoveGroupDropdown: React.Dispatch<React.SetStateAction<boolean>>;
  groupSearchQuery: string;
  setGroupSearchQuery: (query: string) => void;
  filteredGroups: string[];
}

export function DashboardBatchOperationBar({
  servers,
  selectedIds,
  onSelectChange,
  onExitSelectionMode,
  onBatchConnect,
  onBatchMoveGroup,
  onBatchExport,
  onBatchDelete,
  moveGroupMenuRef,
  showMoveGroupDropdown,
  setShowMoveGroupDropdown,
  groupSearchQuery,
  setGroupSearchQuery,
  filteredGroups,
}: DashboardBatchOperationBarProps) {
  const { t } = useTranslation();

  return (
    <div className="batch-operation-bar">
      <div className="selected-info">
        <span className="selected-count-badge">{selectedIds.length}</span>
        <span>{t('已选择服务器')}</span>
      </div>
      <div style={{ flex: 1 }} />
      <div className="w-px self-stretch bg-line-subtle rounded-[var(--radius-sm)] mx-1" />

      <button
        onClick={() => {
          const allSelected = servers.length > 0 && selectedIds.length === servers.length;
          if (allSelected) {
            onSelectChange([]);
          } else {
            onSelectChange(servers.map((s) => s.id));
          }
        }}
        className="btn-batch-action"
        disabled={servers.length === 0}
      >
        <CheckSquare size={14} />
        {servers.length > 0 && selectedIds.length === servers.length ? t('取消全选') : t('全选')}
      </button>

      <button
        onClick={() => {
          if (selectedIds.length > 0) {
            onSelectChange([]);
          } else if (onExitSelectionMode) {
            onExitSelectionMode();
          }
        }}
        className="btn-cancel"
      >
        {selectedIds.length > 0 ? t('取消选择') : t('退出选择')}
      </button>

      {onBatchConnect && (
        <button
          onClick={() => onBatchConnect(selectedIds)}
          className="btn-batch-primary"
          disabled={selectedIds.length === 0}
        >
          <Monitor size={14} />
          {t('批量链接')}
        </button>
      )}

      {onBatchMoveGroup && (
        <div ref={moveGroupMenuRef} style={{ position: 'relative' }}>
          <button
            onClick={() => {
              setShowMoveGroupDropdown((prev) => !prev);
              setGroupSearchQuery('');
            }}
            className="btn-batch-action"
            disabled={selectedIds.length === 0}
          >
            <Folder size={14} />
            {t('移动分组')}
          </button>
          {showMoveGroupDropdown && selectedIds.length > 0 && (
            <div
              className="context-menu absolute bottom-full left-0 mb-2 flex flex-col min-w-[180px] px-2 py-1.5"
              style={{ zIndex: Z.POPUP }}
            >
              <div className="px-1 pt-0.5 pb-1.5 mb-1.5 text-xs text-muted border-b border-line">
                {t('移动到分组')}
              </div>

              <div className="mb-1.5" onClick={(e) => e.stopPropagation()}>
                <input
                  id="dashboard-group-search"
                  name="dashboard-group-search"
                  autoComplete="off"
                  type="text"
                  placeholder={t('搜索或输入新分组...')}
                  value={groupSearchQuery}
                  onChange={(e) => setGroupSearchQuery(e.target.value)}
                  autoFocus
                  className="input-compact w-full h-[26px] text-xs px-1.5 rounded-[var(--radius-sm)] border border-line bg-sunken text-primary"
                />
              </div>

              <div className="max-h-[180px] overflow-y-auto flex flex-col gap-0.5">
                {groupSearchQuery.trim() !== '' && !filteredGroups.includes(groupSearchQuery.trim()) && (
                  <div
                    className="context-menu-item text-accent flex items-center gap-1.5"
                    onClick={() => {
                      onBatchMoveGroup(selectedIds, groupSearchQuery.trim());
                      setShowMoveGroupDropdown(false);
                      setGroupSearchQuery('');
                    }}
                  >
                    <Plus size={11} />
                    <span className="overflow-hidden text-ellipsis whitespace-nowrap">
                      {t('新建并移动')}: "{groupSearchQuery.trim()}"
                    </span>
                  </div>
                )}

                {filteredGroups.map((g) => (
                  <div
                    key={g}
                    onClick={() => {
                      onBatchMoveGroup(selectedIds, g);
                      setShowMoveGroupDropdown(false);
                      setGroupSearchQuery('');
                    }}
                    className="context-menu-item flex items-center gap-1.5"
                  >
                    <Folder size={11} />
                    <span className="overflow-hidden text-ellipsis whitespace-nowrap">{g}</span>
                  </div>
                ))}

                {filteredGroups.length === 0 && groupSearchQuery.trim() === '' && (
                  <div className="py-1.5 px-2 text-xs text-tertiary text-center">
                    {t('暂无分组')}
                  </div>
                )}
              </div>

              <div className="context-menu-divider my-1 rounded-[var(--radius-sm)]" />
              <div
                onClick={() => {
                  onBatchMoveGroup(selectedIds, '');
                  setShowMoveGroupDropdown(false);
                  setGroupSearchQuery('');
                }}
                className="context-menu-item flex items-center gap-1.5"
              >
                <X size={11} />
                <span>{t('移出分组')}</span>
              </div>
            </div>
          )}
        </div>
      )}

      {onBatchExport && (
        <button
          onClick={() => onBatchExport(selectedIds)}
          className="btn-batch-action"
          disabled={selectedIds.length === 0}
        >
          <Download size={14} />
          {t('导出选择')}
        </button>
      )}

      <button
        onClick={async () => {
          if (await window.lumeDialog?.confirm(`${t('确定删除')} ${selectedIds.length} ${t('个服务器')}？`)) {
            onBatchDelete?.(selectedIds);
          }
        }}
        className="btn-delete-batch"
        disabled={selectedIds.length === 0}
      >
        <Trash2 size={14} />
        {t('批量删除')}
      </button>
    </div>
  );
}
