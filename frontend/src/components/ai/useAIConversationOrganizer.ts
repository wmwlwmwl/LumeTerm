import { useCallback, useEffect, useRef, useState } from 'react'
import type * as React from 'react'
import { type I18nKey } from '../../i18n.ts'
import { createAIConversationGroup, loadAIConversationOrganizer, saveAIConversationOrganizer, type AIConversationOrganizerState } from '../../utils/aiConversationOrganizer.ts'
import { createAIConversation, deleteAIConversation, deleteTemporaryAIConversation, getAIConversation, getTemporaryAIConversation, saveAIConversation, saveTemporaryAIConversation } from './aiConversationBridge.ts'
import { upsertConversationSummary, type ConversationSummary } from './aiConversationSummary.ts'
import { getTemporaryAIConversationSummary, removeTemporaryAIConversation, upsertTemporaryAIConversation } from './aiTemporaryConversations.ts'
import type { AIPanelProps } from './aiChatLogic.ts'

type LooseT = (key: I18nKey, vars?: Record<string, unknown>) => string

// 会话整理状态簇：自定义分组（新建/重命名/拖拽重排/删除）、多选批量操作
// （移动分组/归档/删除）、临时会话转正与 F2 重命名快捷键。
// 从 AIConversationTabPanel 原样搬移，闭包依赖经参数同名注入，代码零改动。
export function useAIConversationOrganizer({ t, addToast, showAlert, requestDeleteConfirmation, isWorkspaceTabActive, refreshConversationList, handleOpenConversation, setConversationList }: {
  t: LooseT
  addToast?: AIPanelProps['addToast']
  showAlert: (message: string) => Promise<void>
  requestDeleteConfirmation: (message: string) => Promise<boolean>
  isWorkspaceTabActive: boolean
  refreshConversationList: () => Promise<void>
  handleOpenConversation: (conversationId: string, delegateToWorkspace?: boolean) => Promise<void>
  setConversationList: React.Dispatch<React.SetStateAction<ConversationSummary[]>>
}) {
  const [conversationOrganizer, setConversationOrganizer] = useState<AIConversationOrganizerState>(() => loadAIConversationOrganizer())
  const [conversationFilter, setConversationFilter] = useState('all')
  const [conversationSelectionMode, setConversationSelectionMode] = useState(false)
  const [selectedConversationIds, setSelectedConversationIds] = useState<Set<string>>(() => new Set())
  const [moveToGroupOpen, setMoveToGroupOpen] = useState(false)
  const [editingConversationGroupId, setEditingConversationGroupId] = useState('')
  const [editingConversationGroupName, setEditingConversationGroupName] = useState('')
  const [draggingConversationGroupId, setDraggingConversationGroupId] = useState('')
  const [dragOverConversationGroupId, setDragOverConversationGroupId] = useState('')
  const conversationGroupRenameInputRef = useRef<HTMLInputElement | null>(null)
  const conversationGroupRenameCancelledRef = useRef(false)
  const persistConversationOrganizer = useCallback((updater: (current: AIConversationOrganizerState) => AIConversationOrganizerState) => {
    setConversationOrganizer((current) => saveAIConversationOrganizer(updater(current)))
  }, [])
  const handleMakeConversationPermanent = useCallback(async (conversationId: string) => {
    const temporarySummary = getTemporaryAIConversationSummary(conversationId)
    if (!temporarySummary) return
    let createdConversationId = ''
    try {
      const temporarySnapshot = await getTemporaryAIConversation(conversationId)
      const created = await createAIConversation(temporarySnapshot.title || t('新对话'))
      createdConversationId = created.id
      const permanentSnapshot = await saveAIConversation({
        ...temporarySnapshot,
        id: created.id,
        createdAt: created.createdAt,
        updatedAt: Date.now(),
        transient: false,
        status: 'idle',
      })
      removeTemporaryAIConversation(conversationId)
      persistConversationOrganizer((current) => {
        const assignments = { ...current.assignments }
        if (assignments[conversationId]) assignments[permanentSnapshot.id] = assignments[conversationId]
        delete assignments[conversationId]
        return { ...current, assignments }
      })
      setConversationList((current) => upsertConversationSummary(current.filter((item) => item.id !== conversationId), permanentSnapshot))
      addToast?.(`${t('临时会话')} · ${t('保存')}`, 'success')
      await handleOpenConversation(permanentSnapshot.id)
    } catch {
      if (createdConversationId) await deleteAIConversation(createdConversationId).catch(() => {})
      addToast?.(t('保存失败'), 'error')
    }
  }, [addToast, handleOpenConversation, persistConversationOrganizer, t])
  const handleCreateConversationGroup = useCallback(async () => {
    const name = window?.lumeDialog?.prompt
      ? await window.lumeDialog.prompt(t('请输入分组名称'), '', t('新建分组'))
      : window.prompt(t('请输入分组名称'))
    const normalizedName = typeof name === 'string' ? name.trim() : ''
    if (!normalizedName) return
    const group = createAIConversationGroup(normalizedName)
    persistConversationOrganizer((current) => ({ ...current, groups: [...current.groups, group] }))
    setConversationFilter(group.id)
  }, [persistConversationOrganizer, t])
  const beginRenameConversationGroup = useCallback((groupId: string) => {
    const group = conversationOrganizer.groups.find((item) => item.id === groupId)
    if (!group) return
    setConversationFilter(groupId)
    conversationGroupRenameCancelledRef.current = false
    setEditingConversationGroupId(groupId)
    setEditingConversationGroupName(group.name)
    window.requestAnimationFrame(() => {
      conversationGroupRenameInputRef.current?.focus()
      conversationGroupRenameInputRef.current?.select()
    })
  }, [conversationOrganizer.groups])
  const cancelRenameConversationGroup = useCallback(() => {
    conversationGroupRenameCancelledRef.current = true
    setEditingConversationGroupId('')
    setEditingConversationGroupName('')
  }, [])
  const commitRenameConversationGroup = useCallback(() => {
    if (conversationGroupRenameCancelledRef.current) {
      conversationGroupRenameCancelledRef.current = false
      setEditingConversationGroupId('')
      setEditingConversationGroupName('')
      return
    }
    const groupId = editingConversationGroupId
    const normalizedName = editingConversationGroupName.trim()
    if (groupId && normalizedName) {
      persistConversationOrganizer((current) => ({ ...current, groups: current.groups.map((item) => item.id === groupId ? { ...item, name: normalizedName } : item) }))
    }
    setEditingConversationGroupId('')
    setEditingConversationGroupName('')
  }, [editingConversationGroupId, editingConversationGroupName, persistConversationOrganizer])
  const reorderConversationGroup = useCallback((sourceGroupId: string, targetGroupId: string) => {
    if (!sourceGroupId || !targetGroupId || sourceGroupId === targetGroupId) return
    persistConversationOrganizer((current) => {
      const sourceIndex = current.groups.findIndex((group) => group.id === sourceGroupId)
      const targetIndex = current.groups.findIndex((group) => group.id === targetGroupId)
      if (sourceIndex < 0 || targetIndex < 0) return current
      const groups = [...current.groups]
      const [moved] = groups.splice(sourceIndex, 1)
      groups.splice(targetIndex, 0, moved)
      return { ...current, groups }
    })
  }, [persistConversationOrganizer])
  const showSystemGroupRenameUnsupported = useCallback(() => {
    const message = t('系统分组不支持重命名')
    if (typeof addToast === 'function') {
      addToast(message, 'info', 2200)
      return
    }
    void showAlert(message)
  }, [addToast, showAlert, t])
  useEffect(() => {
    if (!isWorkspaceTabActive) return
    const handleRenameShortcut = (event: KeyboardEvent) => {
      if (event.key !== 'F2' || editingConversationGroupId) return
      if (conversationFilter === 'all' || conversationFilter === 'archived') {
        event.preventDefault()
        showSystemGroupRenameUnsupported()
        return
      }
      if (!conversationOrganizer.groups.some((group) => group.id === conversationFilter)) return
      event.preventDefault()
      beginRenameConversationGroup(conversationFilter)
    }
    window.addEventListener('keydown', handleRenameShortcut)
    return () => window.removeEventListener('keydown', handleRenameShortcut)
  }, [beginRenameConversationGroup, conversationFilter, conversationOrganizer.groups, editingConversationGroupId, isWorkspaceTabActive, showSystemGroupRenameUnsupported])
  useEffect(() => {
    if (conversationFilter === 'ungrouped') setConversationFilter('all')
  }, [conversationFilter])
  const handleDeleteConversationGroup = useCallback(async (groupId: string) => {
    const group = conversationOrganizer.groups.find((item) => item.id === groupId)
    if (!group) return
    const confirmed = await requestDeleteConfirmation(t('删除分组后,其中的会话将移到未分组.是否继续?'))
    if (!confirmed) return
    persistConversationOrganizer((current) => ({
      groups: current.groups.filter((item) => item.id !== groupId),
      assignments: Object.fromEntries(Object.entries(current.assignments).filter(([, assignedGroupId]) => assignedGroupId !== groupId)),
    }))
    if (conversationFilter === groupId) setConversationFilter('ungrouped')
  }, [conversationFilter, conversationOrganizer.groups, persistConversationOrganizer, requestDeleteConfirmation, t])
  const toggleConversationSelection = useCallback((conversationId: string) => {
    setSelectedConversationIds((current) => {
      const next = new Set(current)
      if (next.has(conversationId)) next.delete(conversationId)
      else next.add(conversationId)
      return next
    })
  }, [])
  const clearConversationSelection = useCallback(() => {
    setSelectedConversationIds(new Set())
    setConversationSelectionMode(false)
    setMoveToGroupOpen(false)
  }, [])
  const handleMoveSelectedConversations = useCallback((groupId: string) => {
    const selected = new Set(selectedConversationIds)
    persistConversationOrganizer((current) => {
      const assignments = { ...current.assignments }
      selected.forEach((conversationId) => {
        if (groupId) assignments[conversationId] = groupId
        else delete assignments[conversationId]
      })
      return { ...current, assignments }
    })
    clearConversationSelection()
  }, [clearConversationSelection, persistConversationOrganizer, selectedConversationIds])
  const handleSetSelectedArchived = useCallback(async (archived: boolean) => {
    const ids = Array.from(selectedConversationIds)
    await Promise.all(ids.map(async (conversationId) => {
      try {
    const temporarySummary = getTemporaryAIConversationSummary(conversationId)
        if (temporarySummary) {
          const temporarySnapshot = await getTemporaryAIConversation(conversationId)
          const saved = await saveTemporaryAIConversation({ ...temporarySnapshot, archived, updatedAt: Date.now() })
          upsertTemporaryAIConversation(saved)
          return
        }
        const snapshot = await getAIConversation(conversationId)
        await saveAIConversation({ ...snapshot, archived, updatedAt: Date.now() })
      } catch {
        // Continue processing the remaining selected conversations.
      }
    }))
    clearConversationSelection()
    await refreshConversationList()
  }, [clearConversationSelection, refreshConversationList, selectedConversationIds])
  const handleDeleteSelectedConversations = useCallback(async () => {
    const ids = Array.from(selectedConversationIds)
    if (ids.length === 0) return
    const confirmed = await requestDeleteConfirmation(t('确定删除选中的对话吗？此操作不可撤销。'))
    if (!confirmed) return
    const results = await Promise.allSettled(ids.map(async (conversationId) => removeTemporaryAIConversation(conversationId) ? deleteTemporaryAIConversation(conversationId) : deleteAIConversation(conversationId)))
    persistConversationOrganizer((current) => ({
      ...current,
      assignments: Object.fromEntries(Object.entries(current.assignments).filter(([conversationId]) => !selectedConversationIds.has(conversationId))),
    }))
    clearConversationSelection()
    await refreshConversationList()
    // 容错：部分失败时提示，不影响已成功的删除
    const failedCount = results.filter((result) => result.status === 'rejected').length
    if (failedCount > 0) {
      addToast?.(`${t('部分对话删除失败')}（${failedCount}），其余删除已生效`, 'error')
    }
  }, [addToast, clearConversationSelection, persistConversationOrganizer, refreshConversationList, requestDeleteConfirmation, selectedConversationIds, t])
  return {
    conversationOrganizer,
    setConversationOrganizer,
    conversationFilter,
    setConversationFilter,
    conversationSelectionMode,
    setConversationSelectionMode,
    selectedConversationIds,
    setSelectedConversationIds,
    moveToGroupOpen,
    setMoveToGroupOpen,
    editingConversationGroupId,
    setEditingConversationGroupId,
    editingConversationGroupName,
    setEditingConversationGroupName,
    draggingConversationGroupId,
    setDraggingConversationGroupId,
    dragOverConversationGroupId,
    setDragOverConversationGroupId,
    conversationGroupRenameInputRef,
    conversationGroupRenameCancelledRef,
    persistConversationOrganizer,
    handleMakeConversationPermanent,
    handleCreateConversationGroup,
    beginRenameConversationGroup,
    cancelRenameConversationGroup,
    commitRenameConversationGroup,
    reorderConversationGroup,
    showSystemGroupRenameUnsupported,
    handleDeleteConversationGroup,
    toggleConversationSelection,
    clearConversationSelection,
    handleMoveSelectedConversations,
    handleSetSelectedArchived,
    handleDeleteSelectedConversations,
  }
}
