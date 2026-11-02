import type React from 'react';
import type { I18nKey } from '../../i18n.ts';
import type { IdentityPresetOption } from '../../utils/fileManagerChmodIdentity.ts';
import type { FileManagerPaneState } from '../../utils/fileWorkbench.ts';

// ============================================================
// FileManager 类型契约（props 见 FileManagerProps；内部数据模型见下）
// ============================================================

// 来自 Go 桥/事件/localStorage 持久化数据的外部形状：字段以守卫读取，
// 均带索引签名（运行时宽松数据，新字段无需改接口）。

/** 工作区持久化文件标签（extractManualPinnedTabsFromWorkspace 输入） */
export interface FileManagerWorkspaceTab {
  pinned?: unknown
  systemPinned?: unknown
  id?: unknown
  path?: unknown
  customTitle?: unknown
}

/** 工作区对象（含 tabs 列表） */
export interface FileManagerWorkspace {
  tabs?: FileManagerWorkspaceTab[]
}

/** 虚拟滚动行条目（paneState.rows 元素） */
export interface FileManagerVirtualRowEntry {
  logicalPath?: unknown
  rowType?: unknown
  item?: unknown
  [key: string]: unknown
}

/** 文件管理面板状态（paneState：虚拟滚动面板形状，见 renderFileManagerVirtualViewport 字面量） */
export interface FileManagerPaneStateLike {
  rows: FileManagerVirtualRow[]
  [key: string]: unknown
}

/** 虚拟滚动可见区间 */

/** 下载冲突（PreviewDownloadConflicts 返回项 / buildDownloadConflictMessage 输入） */
export interface FileManagerDownloadConflict {
  relativePath?: unknown
  localSize?: unknown
  remoteSize?: unknown
  localModifyTime?: unknown
  remoteModifyTime?: unknown
  localPath?: unknown
  remotePath?: unknown
  [key: string]: unknown
}

/** 下载冲突解决设置（buildDownloadConflictOptionsPayload 输入） */

/** 文件项宽松形状（handleOpenSystemEditor/WithEditor 输入） */
export interface FileManagerFileLike {
  name?: unknown
  path?: unknown
  content?: unknown
  size?: unknown
  permission?: unknown
  mode?: unknown
  uid?: unknown
  gid?: unknown
}

/** 双面板拖拽 payload 项 */
export interface FileManagerDualPaneDragItem {
  path?: unknown
  isDirectory?: unknown
  [key: string]: unknown
}

/** 双面板拖拽 payload（confirmDualPaneDirectoryDrag 输入） */
export interface FileManagerDualPaneDragPayload {
  items?: FileManagerDualPaneDragItem[]
  paths?: unknown
  [key: string]: unknown
}

/** 标签拖放指示器（setFileManagerTabDropIndicator 状态） */
export interface FileManagerTabDropIndicator {
  tabId?: unknown
  [key: string]: unknown
}

/** chmod 目标（setChmodTarget 状态，{ item, path, mode, includeSubdirectories, showIncludeSubdirectories }） */
export interface FileManagerChmodTarget {
  item: FileManagerFileItem | null
  path: string
  mode: string
  includeSubdirectories?: boolean
  showIncludeSubdirectories?: boolean
  rememberedMode?: string
  autoApplyLastSettings?: boolean
  ownerCandidates?: IdentityPresetOption[]
  groupCandidates?: IdentityPresetOption[]
  [key: string]: unknown
}

export interface FileManagerProps {
  sessionId: string
  sessionGroupId: string
  addToast?: (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number
  removeToast?: (id: number) => void
  isActive?: boolean
  initialPath?: string
}

// 远端/本地文件条目（ListDir 返回项 + 本地传输占位项的统一形状）
// 形状与 utils/fileManagerItems.ts 的同名接口一致，统一自那边导出
import type { FileManagerFileItem, FileManagerVirtualRow } from '../../utils/fileManagerItems.ts';
export type { FileManagerFileItem, FileManagerVirtualRow };

export type LooseT = (key: I18nKey, vars?: Record<string, unknown>) => string

// 面板选中态恢复（FileManagerPaneState + 最后点击路径）
export interface PaneSelectionRestore extends FileManagerPaneState {
  lastClickedPath?: string | null
}

// syncCurrentTabToWorkspace 的覆盖参数
export interface SyncTabOverrides {
  path?: string
  sortField?: string
  sortDir?: string
  selectedPaths?: string[]
  scrollTop?: number
  reason?: string
}

// 右键菜单状态（contextMenu state）
export interface ContextMenuState {
  pos: { x: number; y: number }
  item: FileManagerFileItem | null
  mode?: string
  itemBasePath?: string
  createBasePath?: string
  showCreateActions?: boolean
  deleteItemCount?: number
  clipboardItemCount?: number
  deleteUsesSelectedPaths?: boolean
  clipboardUsesSelectedPaths?: boolean
  tabId?: string
  tabPath?: string
  tabPinned?: boolean
  tabSystemPinned?: boolean
}

// loadDir 的选项
export interface LoadDirOptions {
  silent?: boolean
  tabId?: string
  staleWhileRevalidate?: boolean
  staleItems?: FileManagerFileItem[]
  preferPathCache?: boolean
  preserveWorkspacePathOnSuccess?: boolean
  preserveView?: boolean
  trackDiff?: boolean
  showLoading?: boolean
}

// 跨实例共享的剪贴板/编辑器状态（挂在 window 上，形状以运行时为准）
declare global {
  interface Window {
    __luminClipboards?: Record<string, unknown>
    __luminEditorStates?: Record<string, unknown>
  }
}

export interface ChmodDialogProps {
  path: string
  permission: string
  mode: string
  rememberedMode?: string
  autoApplyLastSettings?: boolean
  uid: string
  gid: string
  ownerCandidates?: IdentityPresetOption[]
  groupCandidates?: IdentityPresetOption[]
  includeSubdirectories?: boolean
  showIncludeSubdirectories?: boolean
  onSave: (mode: string, includeChildren: boolean, ownerInput: string, groupInput: string) => void
  onClose: () => void
  t: LooseT
}

export interface RenameInputProps {
  initialValue: string
  isDirectory: boolean
  onConfirm: (value: string, refocus: boolean) => void
  onCancel: () => void
  mountedRef?: React.RefObject<HTMLInputElement | null>
}

export interface ContextMenuProps {
  pos: { x: number; y: number }
  item: FileManagerFileItem | null
  mode?: string
  isPinned?: boolean
  isSystemPinned?: boolean
  canTogglePinned?: boolean
  canCloseTab?: boolean
  showCreateActions?: boolean
  deleteItemCount?: number
  clipboardItemCount?: number
  canPaste?: boolean
  clipboardActionArrow?: string
  onClose: () => void
  onDownload: () => void
  onEdit: () => void
  onOpenSystemEditor: () => void
  onOpenWithEditor: () => void
  onRename: () => void
  onDelete: () => void
  onDeleteShell: () => void
  onMkdir: () => void
  onNewFile: () => void
  onCompress: () => void
  onUncompress: () => void
  onChmod: () => void
  onCopyPath: () => void
  onCopyItem: () => void
  onCutItem: () => void
  onPaste: () => void
  onOpenInNewTab: () => void
  onTogglePinned: () => void
  onCloseTab: () => void
  t: LooseT
}
