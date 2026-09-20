import { t as $t } from '../../i18n.ts';
import type { LumeDialogChoice, LumeDialogPromptOptions } from '../../types/lumeDialog.js';

/** 设置弹窗统一使用 settings 优先级，避免被后开的终端会话弹窗压在下面 */
const SETTINGS_DIALOG_OPTIONS = { priority: 'settings' };

export const settingsConfirm = (message: string, title = $t('操作确认'), checkboxLabel = ''): Promise<boolean | { confirmed: boolean; checked: boolean }> | undefined => (
  window.lumeDialog?.confirm?.(message, title, checkboxLabel, SETTINGS_DIALOG_OPTIONS)
);
export const settingsChoice = (message: string, title: string, buttons: unknown[], checkboxLabel = ''): Promise<unknown> | undefined => (
  window.lumeDialog?.choice?.(message, title, buttons, checkboxLabel, SETTINGS_DIALOG_OPTIONS)
);
export const settingsPrompt = (message: string, defaultValue = '', title = $t('输入信息'), checkboxLabel = '', options: LumeDialogPromptOptions = {}): Promise<string | null | LumeDialogChoice> | undefined => (
  window.lumeDialog?.prompt?.(message, defaultValue, title, checkboxLabel, { ...options, ...SETTINGS_DIALOG_OPTIONS } as LumeDialogPromptOptions)
);
