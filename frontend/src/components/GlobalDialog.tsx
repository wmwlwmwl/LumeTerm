import { useState, useEffect, useRef, useCallback } from 'react';
import { createPortal } from 'react-dom';
import { t } from '../i18n.ts';
import { Z } from '../constants/zIndex';
import { useOverlayScrollLock } from '../hooks/useOverlayScrollLock.ts';
import {
  DIALOG_PRIORITY,
  getDialogPriority,
  insertDialogByPriority,
  type QueuedDialog,
} from './globalDialog/globalDialogTypes.ts';
import { DialogContent } from './globalDialog/DialogContent.tsx';
import { ModalDragStrip } from './ui';

interface GlobalDialogProps {
  suspendDefault?: boolean;
}

export default function GlobalDialog({ suspendDefault = false }: GlobalDialogProps) {
  const [dialogs, setDialogs] = useState<QueuedDialog[]>([]);
  // ponytail: 队列同时存一份 ref。去重判定必须同步进行——同一 tick 内连续调用时
  // state 尚未更新，只比对 state 会漏判；ref 与 state 始终同步写入，二者不会漂移。
  const dialogsRef = useRef<QueuedDialog[]>([]);

  // 入队；命中去重返回 false。调用方据此立即 resolve，避免 Promise 永久挂起。
  const pushDialog = useCallback((dialog: QueuedDialog, isDuplicate: (d: QueuedDialog) => boolean) => {
    if (dialogsRef.current.some(isDuplicate)) return false;
    dialogsRef.current = insertDialogByPriority(dialogsRef.current, dialog);
    setDialogs(dialogsRef.current);
    return true;
  }, []);

  const removeDialog = useCallback((id: number) => {
    dialogsRef.current = dialogsRef.current.filter((dialog) => dialog.id !== id);
    setDialogs(dialogsRef.current);
  }, []);

  useEffect(() => {
    // 注册全局 API
    window.lumeDialog = {
      alert: (message: string, title = t('提示'), options: Record<string, unknown> = {}) => {
        const normalizedMessage = typeof message === 'string' ? message : String(message ?? '');
        return new Promise<void>((resolve) => {
          const queued = pushDialog({
            id: Date.now() + Math.random(),
            type: 'alert',
            priority: getDialogPriority(options),
            title,
            message: normalizedMessage,
            copyable: options?.copyable !== false,
            onClose: () => resolve()
          }, d => d.type === 'alert' && d.priority === getDialogPriority(options) && d.message === normalizedMessage && d.title === title);
          if (!queued) resolve();
        });
      },
      confirm: (message: string, title = t('操作确认'), checkboxLabel = '', options: Record<string, unknown> = {}) => {
        return new Promise<boolean | { confirmed: boolean; checked: boolean }>((resolve) => {
          const queued = pushDialog({
            id: Date.now() + Math.random(),
            type: 'confirm',
            priority: getDialogPriority(options),
            title,
            message,
            checkboxLabel,
            onConfirm: (_, checked) => resolve(checkboxLabel ? { confirmed: true, checked } : true),
            onCancel: () => resolve(checkboxLabel ? { confirmed: false, checked: false } : false)
          }, d => d.type === 'confirm' && d.priority === getDialogPriority(options) && d.message === message);
          // 去重丢弃按「取消」处理：不确认即不执行破坏性操作
          if (!queued) resolve(checkboxLabel ? { confirmed: false, checked: false } : false);
        });
      },
      prompt: (message: string, defaultValue = '', title = t('输入信息'), checkboxLabel = '', options: { inputType?: 'password' | 'text'; validate?: (value: string) => string | null | undefined | Promise<string | null | undefined>; priority?: string; [key: string]: unknown } = {}) => {
        return new Promise<string | null | { value: string; checked: boolean }>((resolve) => {
          const queued = pushDialog({
            id: Date.now() + Math.random(),
            type: 'prompt',
            priority: getDialogPriority(options),
            title,
            message,
            defaultValue,
            checkboxLabel,
            inputType: options?.inputType === 'password' ? 'password' : 'text',
            // validate(value) => null/undefined 通过；返回字符串则保留弹窗并展示错误
            validate: typeof options?.validate === 'function' ? options.validate : null,
            onConfirm: (val, checked) => resolve(checkboxLabel ? { value: val as string, checked } : val as string),
            onCancel: () => resolve(null)
          }, d => d.type === 'prompt' && d.priority === getDialogPriority(options) && d.message === message);
          if (!queued) resolve(null);
        });
      },
      choice: (message: string, title: string, buttons: unknown[], checkboxLabel = '', options: Record<string, unknown> = {}) => {
        return new Promise<unknown>((resolve) => {
          const queued = pushDialog({
            id: Date.now() + Math.random(),
            type: 'choice',
            priority: getDialogPriority(options),
            title,
            message,
            buttons: buttons as QueuedDialog['buttons'],
            checkboxLabel,
            onChoice: (val, checked) => resolve(checkboxLabel ? { value: val, checked } : val),
            onClose: () => resolve(null)
          }, d => d.type === 'choice' && d.priority === getDialogPriority(options) && d.title === title);
          if (!queued) resolve(null);
        });
      }
    };
    return () => {
      delete window.lumeDialog;
    };
  }, [pushDialog]);

  const current = dialogs[0] as QueuedDialog | undefined;
  const currentSuspended = !!current && suspendDefault && current.priority === DIALOG_PRIORITY.default;
  const shouldLock = dialogs.length > 0 && !currentSuspended;
  useOverlayScrollLock(shouldLock);

  if (dialogs.length === 0) return null;

  const activeCurrent = dialogs[0];
  const dialogZIndex = activeCurrent.priority === DIALOG_PRIORITY.system
    ? Z.SYSTEM_DIALOG
    : (activeCurrent.priority === DIALOG_PRIORITY.settings
      ? Z.SETTINGS_DIALOG
      : Z.GLOBAL_DIALOG);

  const overlayNode = (
    <div
      className="modal-overlay"
      data-modal-overlay="true"
      data-global-dialog-active={currentSuspended ? undefined : 'true'}
      style={{ zIndex: dialogZIndex, display: currentSuspended ? 'none' : 'flex', isolation: 'isolate' as const }}
    >
      <ModalDragStrip />
      {dialogs.map((dialog, index) => {
        const dialogActive = index === 0 && !(suspendDefault && dialog.priority === DIALOG_PRIORITY.default);
        const handleClose = () => {
          if (dialog.onClose) dialog.onClose();
          if (dialog.onCancel && dialog.type !== 'alert') dialog.onCancel();
          removeDialog(dialog.id);
        };
        const handleConfirm = (val: unknown, checked: boolean) => {
          if (dialog.onConfirm) dialog.onConfirm(val, checked);
          removeDialog(dialog.id);
        };
        const handleChoice = (val: unknown, checked: boolean) => {
          if (dialog.onChoice) dialog.onChoice(val, checked);
          removeDialog(dialog.id);
        };
        return (
          <div key={dialog.id} style={{ display: dialogActive ? 'contents' : 'none' }}>
            <DialogContent
              current={dialog}
              active={dialogActive}
              onClose={handleClose}
              onConfirm={handleConfirm}
              onChoice={handleChoice}
            />
          </div>
        );
      })}
    </div>
  );

  if (typeof document !== 'undefined' && document.body) {
    return createPortal(overlayNode, document.body);
  }
  return overlayNode;
}
