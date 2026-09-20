/**
 * 全局对话框 API（GlobalDialog 组件挂载到 window.lumeDialog）
 * 类型以 GlobalDialog.jsx 中的实现为准。
 */

export interface LumeDialogPromptOptions {
  inputType?: 'password' | 'text';
  validate?: (value: string) => string | null | undefined | Promise<string | null | undefined>;
  priority?: string;
  [key: string]: unknown;
}

export interface LumeDialogChoice {
  value: string;
  checked: boolean;
}

export interface LumeDialogApi {
  alert(message: string, title?: string, options?: Record<string, unknown>): Promise<void>;
  confirm(
    message: string,
    title?: string,
    checkboxLabel?: string,
    options?: Record<string, unknown>,
  ): Promise<boolean | { confirmed: boolean; checked: boolean }>;
  prompt(
    message: string,
    defaultValue?: string,
    title?: string,
    checkboxLabel?: string,
    options?: LumeDialogPromptOptions,
  ): Promise<string | null | LumeDialogChoice>;
  choice(
    message: string,
    title: string,
    buttons: unknown[],
    checkboxLabel?: string,
    options?: Record<string, unknown>,
  ): Promise<unknown>;
}

declare global {
  interface Window {
    lumeDialog?: LumeDialogApi;
  }
}

export {};
