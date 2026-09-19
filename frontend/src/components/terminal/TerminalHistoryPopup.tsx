import type * as React from 'react';
import { Clipboard, Play, X } from 'lucide-react';
import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';
import { Z } from '../../constants/zIndex';
import { ToggleSwitch } from '../settings/SharedComponents.tsx';
import Tiptop from '../Tiptop.tsx';
import { warnDev } from '../../utils/devLog';
import type { I18nKey } from '../../i18n.ts';

type LooseT = (key: I18nKey, vars?: Record<string, unknown>) => string;

interface HistoryItem {
  id: string
  command: string
}

// 历史指令弹窗（fixed 定位，不受 overflow:hidden 裁剪）。
// 从 Terminal.tsx 原样搬移，props 与闭包变量同名。
interface TerminalHistoryPopupProps {
  historyPopupPos: { left: number; bottom: number }
  historyPopupRef: React.RefObject<HTMLDivElement | null>
  historyScrollRef: React.RefObject<HTMLDivElement | null>
  historySearchInputRef: React.RefObject<HTMLInputElement | null>
  altOpenHistoryEnabled: boolean
  setAltOpenHistoryEnabled: React.Dispatch<React.SetStateAction<boolean>>
  historyMode: 'server' | 'global'
  setHistoryMode: React.Dispatch<React.SetStateAction<'server' | 'global'>>
  setHistoryList: React.Dispatch<React.SetStateAction<HistoryItem[]>>
  setShowHistory: React.Dispatch<React.SetStateAction<boolean>>
  setHistoryPopupPos: React.Dispatch<React.SetStateAction<{ left: number; bottom: number } | null>>
  filteredHistory: HistoryItem[]
  displayHistory: HistoryItem[]
  searchQuery: string
  setSearchQuery: React.Dispatch<React.SetStateAction<string>>
  historySelectedIndex: number
  handleHistorySearchKeyDown: (event: React.KeyboardEvent<HTMLInputElement>) => void
  selectHistoryCmd: (cmd: string) => void
  executeCommand: (directCmd?: string) => void
  deleteHistoryItem: (id: string) => void | Promise<void>
  serverId: string
  historyServerId: string
  t: LooseT
}

export function TerminalHistoryPopup({
  historyPopupPos,
  historyPopupRef,
  historyScrollRef,
  historySearchInputRef,
  altOpenHistoryEnabled,
  setAltOpenHistoryEnabled,
  historyMode,
  setHistoryMode,
  setHistoryList,
  setShowHistory,
  setHistoryPopupPos,
  filteredHistory,
  displayHistory,
  searchQuery,
  setSearchQuery,
  historySelectedIndex,
  handleHistorySearchKeyDown,
  selectHistoryCmd,
  executeCommand,
  deleteHistoryItem,
  serverId,
  historyServerId,
  t,
}: TerminalHistoryPopupProps) {
  return (
    <div ref={historyPopupRef} className="term-popup flex flex-col box-border w-[480px] max-w-[calc(100vw-16px)] max-h-[280px] text-sm" style={{
        left: historyPopupPos.left,
        bottom: historyPopupPos.bottom,
        zIndex: Z.POPUP,
        fontFamily: 'var(--font-terminal)',
      }}>
          {/* 弹窗头部（标题 + 操作按钮） */}
          <div className="flex items-center justify-between px-2.5 py-2 border-b border-[var(--term-separator)] shrink-0">
            <span className="text-[var(--term-status-color)] text-xs">{t('历史命令')}</span>
            <div className="flex items-center gap-1.5">
              <div className="flex items-center gap-[5px]">
                <span className="text-[var(--term-muted)] text-xs">{t('Alt 打开历史指令')}</span>
                <ToggleSwitch checked={altOpenHistoryEnabled} onChange={() => {
                  const enabled = !altOpenHistoryEnabled;
                  setAltOpenHistoryEnabled(enabled);
                  localStorage.setItem('altOpenHistory', String(enabled));
                  window.dispatchEvent(new CustomEvent('alt-open-history-changed', { detail: enabled }));
                }} />
              </div>
              <button
                onClick={async () => {
                  const scope = historyMode;
                  // 二次确认，与历史页清空行为一致；按作用域给出不同提示
                  const msg = scope === 'global'
                    ? t('确定要清空全部服务器的历史指令吗？')
                    : t('确定要清空该服务器的历史指令吗？');
                  const result = await window.luminDialog?.confirm(msg);
                  const confirmed = typeof result === 'object' ? result?.confirmed : result === true;
                  if (!confirmed) return;
                  try {
                    if (scope === 'global') {
                      await AppGo.SaveGlobalCommandHistory('[]');
                    } else {
                      await AppGo.SaveCommandHistory(historyServerId, '[]');
                    }
                    setHistoryList([]);
                    // 通知历史页 / 自动补全按作用域刷新（全局清空不触碰服务器历史）
                    window.dispatchEvent(new CustomEvent('ssh-history-cleared', {
                      detail: { sessionId: serverId, historyServerId, scope }
                    }));
                  } catch (error) {
                    warnDev('[Terminal] 清空历史失败:', error);
                  }
                }}
                className="inline-flex items-center justify-center gap-1 border border-line bg-raised text-danger rounded-[var(--radius-sm)] px-2 py-[2px] text-xs cursor-pointer select-none transition-colors duration-[80ms] hover:bg-hover"
              >
                {t('清空列表')}
              </button>
              <button
                onClick={() => { setShowHistory(false); setHistoryPopupPos(null); }}
                aria-label={t('关闭')}
                className="inline-flex items-center justify-center gap-1 border border-line bg-raised text-danger rounded-[var(--radius-sm)] px-2 py-[3px] cursor-pointer select-none transition-colors duration-[80ms] hover:bg-hover"
              >
                <X size={12} />
              </button>
            </div>
          </div>

          {/* 历史列表（可滚动） */}
          <div ref={historyScrollRef} className="flex-1 overflow-y-auto min-h-0">
          {filteredHistory.length === 0 ? (
            <div className="p-5 text-center text-[var(--term-muted)] text-sm">
              {searchQuery ? t('无匹配结果') : t('暂无历史记录')}
            </div>
          ) : displayHistory.map((item, index) => {
            const isSelected = historySelectedIndex >= 0 && historySelectedIndex === index;
            return (
              <div
                key={item.id}
                data-history-index={index}
                role="option"
                aria-selected={isSelected}
                onClick={() => selectHistoryCmd(item.command)}
                className={`flex items-center justify-between px-2.5 py-1.5 cursor-pointer border-b border-[var(--term-separator)] transition-colors duration-[80ms] ${
                  isSelected
                    ? 'bg-accent-dim text-accent font-medium'
                    : 'hover:bg-hover text-[var(--term-input-color)]'
                }`}
              >
                <span
                  className="flex-1 min-w-0 truncate pr-2"
                  title={item.command}
                >
                  {item.command}
                </span>
                <div className="flex items-center gap-[3px] shrink-0">
                  {/* 执行（绿色） */}
                  <Tiptop text={t('执行')}>
                    <button
                      type="button"
                      onClick={(e) => { e.stopPropagation(); executeCommand(item.command); }}
                      aria-label={t('执行')}
                      className="inline-flex items-center justify-center w-6 h-6 border border-line bg-raised rounded-[var(--radius-sm)] text-secondary cursor-pointer transition-colors duration-[80ms] hover:text-primary"
                    >
                      <Play size={12} />
                    </button>
                  </Tiptop>
                  {/* 复制（蓝色） */}
                  <Tiptop text={t('复制')}>
                    <button
                      type="button"
                      onClick={(e) => { e.stopPropagation(); navigator.clipboard.writeText(item.command).catch(() => {}); }}
                      aria-label={t('复制')}
                      className="inline-flex items-center justify-center w-6 h-6 border border-line bg-raised rounded-[var(--radius-sm)] text-secondary cursor-pointer transition-colors duration-[80ms] hover:text-primary"
                    >
                      <Clipboard size={12} />
                    </button>
                  </Tiptop>
                  {/* 删除（红色） */}
                  <Tiptop text={t('删除')}>
                    <button
                      type="button"
                      onClick={(e) => { e.stopPropagation(); deleteHistoryItem(item.id); }}
                      aria-label={t('删除')}
                      className="inline-flex items-center justify-center w-6 h-6 border border-line bg-[rgba(255,123,114,0.15)] rounded-[var(--radius-sm)] text-danger cursor-pointer transition-colors duration-[80ms] hover:bg-danger-dim"
                    >
                      <X size={12} />
                    </button>
                  </Tiptop>
                </div>
              </div>
            );
          })}
          </div>

          {/* 搜索 + 模式切换 */}
          <div className="flex gap-2 items-center px-3 py-2 border-t border-[var(--term-separator)] shrink-0 bg-canvas">
            <input
              ref={historySearchInputRef}
              name="terminal-history-search"
              autoComplete="off"
              aria-label={t('搜索命令历史')}
              value={searchQuery}
              onChange={e => setSearchQuery(e.target.value)}
              onKeyDown={handleHistorySearchKeyDown}
              placeholder={t('搜索命令...')}
              className="flex-1 h-8.5 px-2.5 bg-sunken border border-line rounded-[var(--radius-sm)] text-xs text-primary outline-none focus:border-focus focus:ring-1 focus:ring-accent/30"
            />
            <div className="inline-flex h-8.5 items-center gap-0.5 rounded-[var(--radius-sm)] border border-line-subtle bg-sunken p-0.5 shrink-0">
              <button
                type="button"
                className={`h-7 px-3 rounded-[6px] text-xs font-medium transition-colors cursor-pointer ${
                  historyMode === 'server'
                    ? 'bg-accent text-white font-semibold shadow-xs'
                    : 'border border-transparent text-secondary hover:text-primary hover:bg-hover/60'
                }`}
                onClick={() => setHistoryMode('server')}
              >
                {t('当前服务器')}
              </button>
              <button
                type="button"
                className={`h-7 px-3 rounded-[6px] text-xs font-medium transition-colors cursor-pointer ${
                  historyMode === 'global'
                    ? 'bg-accent text-white font-semibold shadow-xs'
                    : 'border border-transparent text-secondary hover:text-primary hover:bg-hover/60'
                }`}
                onClick={() => setHistoryMode('global')}
              >
                {t('全部服务器')}
              </button>
            </div>
          </div>
        </div>
  );
}
