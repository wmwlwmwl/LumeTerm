import { useEffect, useRef, useState } from 'react';
import type * as React from 'react';
import { Clipboard, Clock, Play, Zap } from 'lucide-react';
import Tiptop from '../Tiptop.tsx';
import { cn } from '../../utils/cn.ts';
import type { CommandAutocompleteState } from '../../utils/terminalCommandAutocomplete.ts';
import type { AutocompleteItem } from '../../utils/terminalCommandAutocomplete.ts';
import type { I18nKey } from '../../i18n.ts';

type LooseT = (key: I18nKey, vars?: Record<string, unknown>) => string;

// 底部命令输入栏：命令 textarea（含快捷键提示浮层/自动补全键盘导航）、
// 历史/命令/执行/复制/多行包裹按钮。从 Terminal.tsx 原样搬移，props 与闭包变量同名。
interface TerminalInputBarProps {
  cmdInput: string
  setCmdInput: React.Dispatch<React.SetStateAction<string>>
  cmdInputRef: React.RefObject<HTMLTextAreaElement | null>
  cmdInputHintsHidden: boolean
  commandAutocomplete: CommandAutocompleteState
  setCommandAutocomplete: React.Dispatch<React.SetStateAction<CommandAutocompleteState>>
  commandAutocompleteFocusedRef: React.RefObject<boolean>
  commandAutocompleteKeyboardNavigationRef: React.RefObject<boolean>
  scheduleCommandAutocompleteSuggestions: (nextValue: string) => void
  clearCommandAutocompleteBlurTimer: () => void
  commandAutocompleteBlurTimerRef: React.RefObject<ReturnType<typeof setTimeout> | null>
  updateCommandAutocompletePopupPosition: () => void
  closeCommandAutocomplete: () => void
  loadCommandAutocompleteSuggestions: (nextValue: string) => Promise<AutocompleteItem[]>
  applyCommandAutocompleteItem: (item: AutocompleteItem) => void
  toggleCommandInputHints: () => void | Promise<void>
  altOpenHistoryEnabled: boolean
  openHistoryAndFocusSearch: () => void
  setShowHistory: React.Dispatch<React.SetStateAction<boolean>>
  showHistory: boolean
  showCommands: boolean
  historyBtnRef: React.RefObject<HTMLButtonElement | null>
  toggleHistory: () => void
  toggleCommands: () => void
  executeCommand: (directCmd?: string) => void
  cmdTrimmed: string
  isConnected: boolean
  copyCommand: () => void
  multiLineWrapEnabled: boolean
  toggleMultiLineWrap: () => void
  t: LooseT
}

export function TerminalInputBar({
  cmdInput,
  setCmdInput,
  cmdInputRef,
  cmdInputHintsHidden,
  commandAutocomplete,
  setCommandAutocomplete,
  commandAutocompleteFocusedRef,
  commandAutocompleteKeyboardNavigationRef,
  scheduleCommandAutocompleteSuggestions,
  clearCommandAutocompleteBlurTimer,
  commandAutocompleteBlurTimerRef,
  updateCommandAutocompletePopupPosition,
  closeCommandAutocomplete,
  loadCommandAutocompleteSuggestions,
  applyCommandAutocompleteItem,
  toggleCommandInputHints,
  altOpenHistoryEnabled,
  openHistoryAndFocusSearch,
  setShowHistory,
  showHistory,
  showCommands,
  historyBtnRef,
  toggleHistory,
  toggleCommands,
  executeCommand,
  cmdTrimmed,
  isConnected,
  copyCommand,
  multiLineWrapEnabled,
  toggleMultiLineWrap,
  t,
}: TerminalInputBarProps) {
  const [cmdInputWidth, setCmdInputWidth] = useState<number>(600);

  useEffect(() => {
    const el = cmdInputRef.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        if (entry.contentRect && entry.contentRect.width > 0) {
          setCmdInputWidth(entry.contentRect.width);
        }
      }
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const commandsBtnRef = useRef<HTMLButtonElement | null>(null);

  return (
    <div className="term-input-bar">
      {/* 命令输入框 */}
      <Tiptop
        text={!cmdInputHintsHidden && !cmdInput && !commandAutocomplete.open ? (
          <div className="flex flex-col gap-[5px] px-1 py-0.5 text-xs leading-[1.5] text-left min-w-[190px]">
            <div className="flex items-center gap-[5px] font-semibold text-primary border-b border-line-subtle pb-1 mb-0.5">
              <span>{t('命令输入快捷键')}</span>
            </div>
            <div className="flex items-center justify-between gap-3">
              <span className="text-secondary">{t('执行命令')}</span>
              <kbd className="bg-overlay border border-line rounded-xs px-[5px] py-px text-[10px] font-mono">Enter</kbd>
            </div>
            <div className="flex items-center justify-between gap-3">
              <span className="text-secondary">{t('换行多行输入')}</span>
              <kbd className="bg-overlay border border-line rounded-xs px-[5px] py-px text-[10px] font-mono">Shift + Enter</kbd>
            </div>
            <div className="flex items-center justify-between gap-3">
              <span className="text-secondary">{t('快捷命令列表')}</span>
              <kbd className="bg-overlay border border-line rounded-xs px-[5px] py-px text-[10px] font-mono">/</kbd>
            </div>
            {altOpenHistoryEnabled && (
              <div className="flex items-center justify-between gap-3">
                <span className="text-secondary">{t('搜索历史指令')}</span>
                <kbd className="bg-overlay border border-line rounded-xs px-[5px] py-px text-[10px] font-mono">Alt</kbd>
              </div>
            )}
            <div className="flex items-center justify-between gap-3">
              <span className="text-secondary">{t('补全候选项')}</span>
              <kbd className="bg-overlay border border-line rounded-xs px-[5px] py-px text-[10px] font-mono">Tab</kbd>
            </div>
            <div className="flex items-center justify-between gap-3">
              <span className="text-secondary">{t('关闭此提示')}</span>
              <kbd className="bg-overlay border border-line rounded-xs px-[5px] py-px text-[10px] font-mono">F1</kbd>
            </div>
          </div>
        ) : undefined}
        placement="top"
        className="flex"
        style={{ flex: 1, minWidth: 0 }}
      >
        <textarea
          ref={cmdInputRef}
          className="input term-command-input w-full text-sm py-[7px] px-[11px] h-9 min-h-9 leading-5 bg-[var(--term-input-bg)] text-[var(--term-input-color)]"
          name="terminalCommand"
          value={cmdInput}
          rows={1}
          spellCheck={false}
          autoComplete="off"
          onChange={e => {
            const nextValue = e.target.value;
            setCmdInput(nextValue);
            if (commandAutocompleteFocusedRef.current) {
              scheduleCommandAutocompleteSuggestions(nextValue);
            }
          }}
          onFocus={() => {
            commandAutocompleteFocusedRef.current = true;
            clearCommandAutocompleteBlurTimer();
            updateCommandAutocompletePopupPosition();
            if (cmdInput.trim()) {
              scheduleCommandAutocompleteSuggestions(cmdInput);
            }
          }}
          onBlur={() => {
            commandAutocompleteFocusedRef.current = false;
            clearCommandAutocompleteBlurTimer();
            commandAutocompleteBlurTimerRef.current = setTimeout(() => {
              closeCommandAutocomplete();
            }, 120);
          }}
          onScroll={() => {
            if (commandAutocomplete.open || commandAutocomplete.loading) {
              updateCommandAutocompletePopupPosition();
            }
          }}
          onSelect={() => {
            if (commandAutocomplete.open || commandAutocomplete.loading) {
              updateCommandAutocompletePopupPosition();
            }
            if (commandAutocompleteFocusedRef.current && cmdInput.trim()) {
              scheduleCommandAutocompleteSuggestions(cmdInput);
            }
          }}
          onKeyDown={async (e) => {
            if (e.key === 'F1' && !e.ctrlKey && !e.shiftKey && !e.altKey && !e.metaKey) {
              e.preventDefault();
              e.stopPropagation();
              void toggleCommandInputHints();
              return;
            }

            if (e.key === 'Alt' && !e.ctrlKey && !e.shiftKey && !e.metaKey && !e.repeat) {
              if (!altOpenHistoryEnabled) return;
              e.preventDefault();
              e.stopPropagation();
              closeCommandAutocomplete();
              openHistoryAndFocusSearch();
              return;
            }

            if (commandAutocomplete.open && e.key === 'ArrowDown') {
              e.preventDefault();
              if (commandAutocomplete.items.length === 0) {
                return;
              }
              commandAutocompleteKeyboardNavigationRef.current = true;
              setCommandAutocomplete((previous) => ({
                ...previous,
                selectedIndex: previous.selectedIndex < 0
                  ? 0
                  : (previous.selectedIndex + 1) % previous.items.length,
              }));
              return;
            }

            if (commandAutocomplete.open && e.key === 'ArrowUp') {
              e.preventDefault();
              if (commandAutocomplete.items.length === 0) {
                return;
              }
              commandAutocompleteKeyboardNavigationRef.current = true;
              setCommandAutocomplete((previous) => ({
                ...previous,
                selectedIndex: previous.selectedIndex < 0
                  ? previous.items.length - 1
                  : (previous.selectedIndex - 1 + previous.items.length) % previous.items.length,
              }));
              return;
            }

            if (e.key === 'Tab' && cmdInput.trim()) {
              e.preventDefault();
              let items = commandAutocomplete.items;
              if (items.length === 0) {
                items = await loadCommandAutocompleteSuggestions(cmdInput);
              }
              const selectedIndex = commandAutocomplete.selectedIndex >= 0 ? commandAutocomplete.selectedIndex : 0;
              const selectedItem = items[selectedIndex] || items[0];
              if (selectedItem) {
                applyCommandAutocompleteItem(selectedItem);
              }
              return;
            }

            if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) {
              requestAnimationFrame(() => {
                if (commandAutocompleteFocusedRef.current && cmdInputRef.current) {
                  updateCommandAutocompletePopupPosition();
                  void loadCommandAutocompleteSuggestions(cmdInputRef.current.value);
                }
              });
            }

            if (e.key === 'Escape') {
              if (commandAutocomplete.open) {
                e.preventDefault();
                closeCommandAutocomplete();
                return;
              }
              setShowHistory(false);
            }

            if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
              if (e.shiftKey) {
                return;
              }
              e.preventDefault();
              closeCommandAutocomplete();
              executeCommand();
            }
          }}
          placeholder={(() => {
            if (cmdInputWidth >= 520) {
              return altOpenHistoryEnabled
                ? `${t('输入命令')} (/ ${t('快捷命令')}) · Shift+Enter ${t('换行')} · Alt → ${t('历史指令')}`
                : `${t('输入命令')} (/ ${t('快捷命令')}) · Shift+Enter ${t('换行')}`;
            }
            if (cmdInputWidth >= 360) {
              return `${t('输入命令')} (/ ${t('快捷命令')}) · Shift+Enter ${t('换行')}`;
            }
            if (cmdInputWidth >= 240) {
              return `${t('输入命令')} (/ ${t('快捷命令')})`;
            }
            return `${t('输入命令')}...`;
          })()}
          style={{
            fontFamily: 'var(--font-terminal)',
            borderColor: cmdInput ? 'var(--border-focus)' : 'var(--term-btn-border)',
          }}
        />
      </Tiptop>

      {/* 历史按钮 */}
      <Tiptop text={t('历史指令')}>
        <button
          ref={historyBtnRef}
          type="button"
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            toggleHistory();
          }}
          aria-label={t('历史指令')}
          className={`term-btn${showHistory ? ' active' : ''}`}
        >
          <Clock size={13} />
          <span>{t('历史')}</span>
        </button>
      </Tiptop>

      {/* 快捷命令按钮 */}
      <Tiptop text={t('快捷命令')}>
        <button
          ref={commandsBtnRef}
          type="button"
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            toggleCommands();
          }}
          aria-label={t('快捷命令')}
          className={`term-btn${showCommands ? ' active' : ''}`}
        >
          <span className="inline-flex items-center"><Zap size={13} /></span>
          <span>{t('命令')}</span>
        </button>
      </Tiptop>

      {/* 执行按钮（绿色） */}
      <Tiptop text={t('执行')}>
        <button
          onClick={() => executeCommand()}
          disabled={!cmdTrimmed || !isConnected}
          aria-label={t('执行')}
          className={`term-btn-icon success${(cmdTrimmed && isConnected) ? ' enabled' : ''}`}
        >
          <Play size={13} />
        </button>
      </Tiptop>

      {/* 复制按钮（蓝色） */}
      <Tiptop text={t('复制')}>
        <button
          onClick={copyCommand}
          disabled={!cmdTrimmed}
          aria-label={t('复制')}
          className={`term-btn-icon accent${cmdTrimmed ? ' enabled' : ''}`}
        >
          <Clipboard size={13} />
        </button>
      </Tiptop>

      <Tiptop
        text={
          multiLineWrapEnabled ? (
            <div className="flex flex-col gap-0.5 text-center">
              <div className="font-semibold text-primary">{t('独立子 Shell 执行 (已开启)')}</div>
              <div className="text-secondary text-xs">{t('多行/复杂命令在隔离子进程中执行，不污染终端会话')}</div>
            </div>
          ) : (
            <div className="flex flex-col gap-0.5 text-center">
              <div className="font-semibold text-primary">{t('直接在当前会话执行 (默认)')}</div>
              <div className="text-secondary text-xs">{t('命令直接发送至当前终端会话，定义的变量与环境在会话中持续生效')}</div>
            </div>
          )
        }
      >
        <button
          onClick={toggleMultiLineWrap}
          aria-label={multiLineWrapEnabled ? t('独立子 Shell 执行 (已开启)') : t('直接在当前会话执行 (默认)')}
          className={cn('term-btn-icon', multiLineWrapEnabled ? 'active enabled border-accent text-accent bg-accent-dim font-bold' : 'enabled')}
        >
          <span className="inline-flex items-center justify-center w-3.5 font-mono text-xs font-bold">
            &gt;_
          </span>
        </button>
      </Tiptop>
    </div>
  );
}
