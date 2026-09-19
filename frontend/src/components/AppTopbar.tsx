import { useRef } from 'react';
import { House, Minus, Square, X, Bot, Settings, RefreshCw, Rocket, Sun, Moon, ChevronDown, MonitorUp } from 'lucide-react';
import Tiptop from './Tiptop.tsx';

import { WindowMinimise } from '../../wailsjs/runtime/runtime.js';
import { Z } from '../constants/zIndex.ts';
import { cn } from '../utils/cn.ts';
import useTabStripWheelScroll from '../hooks/useTabStripWheelScroll.ts';
import type { SessionAuthPrompt, SshChannelUsage } from '../hooks/useSessionConnections.ts';
import type { SessionLike } from '../utils/sessionWorkspace.ts';

/** 顶栏标签页的会话形状（来自 useSessionConnections 的宽松会话） */
export interface TopbarSession extends SessionLike {
  id: string;
  serverName?: string;
  serverId?: string;
  host?: string;
  status: string;
}

export interface AppTopbarProps {
  t: (key: string, vars?: Record<string, unknown>) => string;
  handleTopbarDoubleClick: () => void;
  markWorkspaceRestoreNavigationOverride: () => void;
  setActiveSessionId: (id: string | null) => void;
  setActiveTerminalId: (id: string | null) => void;
  setShowSettings: (v: boolean) => void;
  logoImg: string;
  showTopbarRefreshedLogo: boolean;
  topbarLogoTransitionImg: string;
  sessions: TopbarSession[];
  tabScrollRef: React.RefObject<HTMLDivElement | null>;
  tabListRef: React.RefObject<HTMLDivElement | null>;
  activeSessionId: string | null;
  handleTabClick: (sessionId: string) => void;
  closeSession: (sessionId: string, e?: React.MouseEvent) => Promise<void>;
  setTabContextMenu: (menu: { sessionId: string; serverName: string; x: number; y: number } | null) => void;
  sessionAuthPrompts: Record<string, SessionAuthPrompt>;
  sshChannelUsage: Record<string, SshChannelUsage>;
  tabsOverflow: boolean;
  tabActionsRef: React.RefObject<HTMLDivElement | null>;
    sessionListBtnRef: React.RefObject<HTMLButtonElement | null>;
    showSessionList: boolean;
    toggleSessionList: () => void;
  closeAllSessions: () => Promise<void>;
  showThemeQuickEntry: boolean;
  showBigScreenQuickEntry: boolean;
  showAIQuickEntry: boolean;
  resolvedQuickThemeMode: 'light' | 'dark';
  handleQuickThemeToggle: () => void;
  isActiveSessionConnected: boolean;
  showAIPanel: boolean;
  setAIPanelVisibility: (v: boolean) => void;
  startupUpdateInfo: { version: string } | null;
  showUpdateBubble: boolean;
  isUpdateModalVisible: boolean;
  setShowUpdateBubble: (v: boolean) => void;
  setIsUpdateModalVisible: (v: boolean) => void;
  setSettingsInitialTab: (tab: string) => void;
  handleToggleMaximise: () => void;
  handleCloseWindow: () => Promise<void>;
  reconnectSession: (session: SessionLike) => Promise<unknown>;
  onOpenBigScreen: () => void;
}

export default function AppTopbar({
  t, handleTopbarDoubleClick, markWorkspaceRestoreNavigationOverride,
  setActiveSessionId, setActiveTerminalId, setShowSettings,
  logoImg, showTopbarRefreshedLogo, topbarLogoTransitionImg,
  sessions, tabScrollRef, tabListRef, activeSessionId, handleTabClick,
  closeSession, setTabContextMenu, sessionAuthPrompts, sshChannelUsage,
  tabActionsRef, sessionListBtnRef, showSessionList, toggleSessionList,
   closeAllSessions, showThemeQuickEntry, showBigScreenQuickEntry, showAIQuickEntry,
  resolvedQuickThemeMode, handleQuickThemeToggle, isActiveSessionConnected,
  showAIPanel, setAIPanelVisibility, startupUpdateInfo, showUpdateBubble,
  isUpdateModalVisible, setShowUpdateBubble,
  setIsUpdateModalVisible, setSettingsInitialTab, handleToggleMaximise,
  handleCloseWindow, reconnectSession, onOpenBigScreen,
}: AppTopbarProps) {
  const topbarRef = useRef<HTMLDivElement | null>(null);
  // 顶栏会话标签条：普通滚轮直接横向滚动（与其他标签条统一，不依赖 Shift）
  useTabStripWheelScroll(tabScrollRef, sessions.length > 0);

  return (
    <>
      {/* ── Topbar ───────────────────────────────────────── */}
      <div
        className="topbar"
        ref={topbarRef}
        onMouseDown={(e) => {
          // detail>1 为双击的第二次按下；阻止浏览器默认划词（否则 WebView2 会弹 AI 搜索条）
          if (e.detail > 1) e.preventDefault();
        }}
        onDoubleClick={handleTopbarDoubleClick}
      >
        <div className="topbar-content">
          <div className="topbar-logo" onClick={() => { markWorkspaceRestoreNavigationOverride(); setActiveSessionId(null); setActiveTerminalId(null); setShowSettings(false); }}>
            <div className="relative w-5 h-5 rounded-xs overflow-hidden shrink-0">
              <img
                src={logoImg}
                alt="Lumin SSH"
                className="absolute inset-0 w-full h-full object-cover [transition:opacity_0.6s_ease,transform_0.7s_cubic-bezier(0.22,1,0.36,1),filter_0.6s_ease]"
                style={{
                  opacity: showTopbarRefreshedLogo ? 0 : 1,
                  transform: showTopbarRefreshedLogo ? 'scale(0.9) rotate(-8deg)' : 'scale(1) rotate(0deg)',
                  filter: showTopbarRefreshedLogo ? 'blur(8px)' : 'blur(0px)',
                }}
              />
              <img
                src={topbarLogoTransitionImg}
                alt="Lumin Theme Logo"
                className="absolute inset-0 w-full h-full object-cover [transition:opacity_0.6s_ease,transform_0.7s_cubic-bezier(0.22,1,0.36,1),filter_0.6s_ease]"
                style={{
                  opacity: showTopbarRefreshedLogo ? 1 : 0,
                  transform: showTopbarRefreshedLogo ? 'scale(1) rotate(0deg)' : 'scale(1.12) rotate(8deg)',
                  filter: showTopbarRefreshedLogo ? 'blur(0px)' : 'blur(10px)',
                }}
              />
            </div>
            <div className="topbar-title">Lumin</div>
          </div>

          {sessions.length > 0 && (
            <div className="tab-bar">
              <Tiptop text={t('返回主页')} placement="bottom">
                <button
                  type="button"
                  className={cn('tab-item tab-home-item no-drag shrink-0', activeSessionId === null ? 'active' : '')}
                  onClick={() => { markWorkspaceRestoreNavigationOverride(); setActiveSessionId(null); setActiveTerminalId(null); }}
                  aria-label={t('返回主页')}
                >
                  <House size={14} />
                </button>
              </Tiptop>
              <div className="tab-scroll" ref={tabScrollRef}>
                <div ref={tabListRef} className="tab-list">
              <Tiptop text={t('搜索服务器')} placement="bottom">
                <button
                  ref={sessionListBtnRef}
                  type="button"
                  className={cn(
                    'tab-search-item no-drag shrink-0',
                    showSessionList && 'active',
                  )}
                  onClick={(e) => { e.stopPropagation(); toggleSessionList(); }}
                  aria-label={t('搜索服务器')}
                  aria-haspopup="dialog"
                  aria-expanded={showSessionList}
                >
                  <ChevronDown size={14} />
                </button>
              </Tiptop>
                  {sessions.map((s) => (
                    <div
                      key={s.id}
                      className={`tab-item no-drag ${activeSessionId === s.id ? 'active' : ''}`}
                      onClick={() => handleTabClick(s.id)}
                      onDoubleClick={(e) => { void closeSession(s.id, e); }}
                      onMouseDown={(e) => {
                        if (e.button !== 1) return;
                        e.preventDefault();
                        e.stopPropagation();
                        void closeSession(s.id, e);
                      }}
                      onContextMenu={(e) => {
                        e.preventDefault();
                        const rect = e.currentTarget.getBoundingClientRect();
                        setTabContextMenu({
                          sessionId: s.id,
                          serverName: s.serverName || s.host || '',
                          x: rect.left,
                          y: rect.bottom + 4,
                        });
                      }}
                    >
                      <span
                        className={`status-dot ${sessionAuthPrompts[s.id] ? 'attention' : s.status === 'connecting' ? 'connecting' : s.status === 'connected' ? 'online' : 'offline'}`}
                        role={sessionAuthPrompts[s.id] ? 'img' : undefined}
                        aria-label={sessionAuthPrompts[s.id] ? sessionAuthPrompts[s.id].title : undefined}
                        title={sessionAuthPrompts[s.id] ? sessionAuthPrompts[s.id].title : undefined}
                      />
                      {(() => {
                        const usage = sshChannelUsage[s.id];
                        if (!usage || usage.total <= 0 || s.status !== 'connected') return null;
                        const maxSessions = usage.maxSessions > 0 ? usage.maxSessions : 10;
                        const nearLimit = usage.total >= Math.max(1, maxSessions - 2);
                        return (
                          <Tiptop
                            placement="bottom"
                            minTop={() => (topbarRef.current?.getBoundingClientRect().bottom ?? 40) + 6}
                            text={(
                              <>
                                <div>{t('服务器连接通道占用')}</div>
                                <div className="mt-0.5 opacity-[0.82] text-xs">{t('终端 {count} 个', { count: usage.terminals })}</div>
                                <div className="opacity-[0.82] text-xs">{t('共享文件通道 {count} 个', { count: usage.sharedSftp })}</div>
                                <div className="opacity-[0.82] text-xs">{t('上传通道 {count} 个', { count: usage.uploadPool })}</div>
                                <div className="mt-0.5 text-xs">{t('合计 {total} / 上限 {max}', { total: usage.total, max: maxSessions })}</div>
                                <div className="mt-0.5 opacity-70 text-xs">{t('接近服务器通道上限后将无法建立新的终端或传输')}</div>
                              </>
                            )}
                          >
                            <span
                              className={cn(
                                'no-drag min-w-[15px] h-[15px] px-1 rounded-full text-[10px] font-bold leading-[15px] text-center shrink-0 cursor-default border',
                                nearLimit
                                  ? 'bg-warning-dim text-warning border-warning'
                                  : 'bg-sunken text-tertiary border-line',
                              )}
                            >
                              {usage.total}
                            </span>
                          </Tiptop>
                        );
                      })()}
                      <span className="max-w-[120px] truncate">
                        {s.serverName}
                      </span>
                      {(s.status === 'closed' || s.status === 'error') && (
                        <Tiptop text={t('重新连接')} placement="bottom">
                          <span
                            className="tab-reconnect no-drag cursor-pointer"
                            onClick={(e) => {
                              e.stopPropagation();
                              reconnectSession(s);
                            }}
                            onDoubleClick={(e) => e.stopPropagation()}
                            aria-label={t('重新连接')}
                          >
                            <RefreshCw size={12} />
                          </span>
                        </Tiptop>
                      )}
                      <span
                        className="tab-close no-drag"
                        onClick={(e) => closeSession(s.id, e)}
                        onDoubleClick={(e) => e.stopPropagation()}
                      >
                        <X size={12} />
                      </span>
                    </div>
                  ))}
                </div>
              </div>
              <div ref={tabActionsRef} className="tab-actions">
                {sessions.length >= 2 && (
                  <Tiptop text={t('关闭全部')} placement="bottom">
                    <button
                      type="button"
                      className="no-drag w-[26px] h-[26px] p-0 rounded-sm inline-flex items-center justify-center bg-transparent border-0 cursor-pointer text-tertiary hover:text-danger hover:bg-danger-dim transition-colors duration-[80ms]"
                      onClick={closeAllSessions}
                      aria-label={t('关闭全部')}
                    >
                      <X size={13} />
                    </button>
                  </Tiptop>
                )}
              </div>
            </div>
          )}
          {sessions.length === 0 && <div className="flex-1" />}

          <div className="window-controls">
            {sessions.length > 0 && showBigScreenQuickEntry && (
              <Tiptop text={t('数据大屏')} placement="bottom">
                <button
                  type="button"
                  className="no-drag w-7 h-7 p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center shrink-0 text-secondary transition-colors duration-[80ms] hover:bg-hover hover:text-primary"
                  onClick={onOpenBigScreen}
                  aria-label={t('数据大屏')}
                >
                  <MonitorUp size={16} />
                </button>
              </Tiptop>
            )}
            {showThemeQuickEntry && (
              <Tiptop text={resolvedQuickThemeMode === 'light' ? t('深色') : t('浅色')} placement="bottom">
                <button
                  type="button"
                  className="no-drag w-7 h-7 p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center shrink-0 text-secondary transition-colors duration-[80ms] hover:bg-hover hover:text-primary"
                  onClick={handleQuickThemeToggle}
                  aria-label={resolvedQuickThemeMode === 'light' ? t('深色') : t('浅色')}
                >
                  {resolvedQuickThemeMode === 'light' ? <Sun size={15} /> : <Moon size={15} />}
                </button>
              </Tiptop>
            )}
            {showAIQuickEntry && activeSessionId !== null && isActiveSessionConnected && sessions.length > 0 && (
              <Tiptop text={showAIPanel ? t('收起 AI 助手面板') : t('打开 AI 助手面板')} placement="bottom">
                <button
                  type="button"
                  className={cn(
                    'no-drag w-7 h-7 p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center shrink-0 transition-colors duration-[80ms] hover:bg-hover',
                    showAIPanel ? 'text-accent hover:text-accent' : 'text-secondary hover:text-primary',
                  )}
                  onClick={() => setAIPanelVisibility(!showAIPanel)}
                  aria-label={showAIPanel ? t('收起 AI 助手面板') : t('打开 AI 助手面板')}
                >
                  <Bot size={16} />
                </button>
              </Tiptop>
            )}
            {startupUpdateInfo && (
              <Tiptop text={`${t('发现新版本')} ${startupUpdateInfo.version}`} placement="bottom">
                <div className="update-entry no-drag relative flex items-center">
                  <div
                    className={`update-bubble${showUpdateBubble ? ' visible' : ''} top-[calc(100%+10px)] -right-1 pointer-events-none`}
                    style={{
                      position: 'absolute',
                      opacity: showUpdateBubble ? 1 : 0,
                      transform: `translateY(${showUpdateBubble ? '0' : '-8px'}) scale(${showUpdateBubble ? '1' : '0.94'})`,
                      zIndex: Z.POPOVER,
                    }}
                  >
                    <span className="update-bubble-pulse" />
                    <span className="update-bubble-dot" />
                    <div className="update-bubble-content">
                      <span className="update-bubble-pill">{t('发现新版本')}</span>
                      <span className="update-bubble-text">{startupUpdateInfo.version}</span>
                    </div>
                  </div>
                  <button
                    type="button"
                    aria-pressed={isUpdateModalVisible}
                    className={cn(
                      'no-drag w-7 h-7 p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center shrink-0 transition-colors duration-[80ms] hover:bg-hover relative overflow-visible',
                      isUpdateModalVisible ? 'text-accent hover:text-accent' : 'text-secondary hover:text-primary',
                    )}
                    onClick={() => {
                      setShowUpdateBubble(false);
                      setIsUpdateModalVisible(true);
                    }}
                    aria-label={`${t('发现新版本')} ${startupUpdateInfo.version}`}
                  >
                    <Rocket size={16} />
                    <span className="update-entry-badge absolute top-1.5 right-1.5 w-1.5 h-1.5 rounded-full bg-danger shadow-[0_0_0_2px_var(--surface-base)]" />
                  </button>
                </div>
              </Tiptop>
            )}
            <Tiptop text={t('设置')} placement="bottom">
              <button
                type="button"
                className="no-drag w-7 h-7 p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center shrink-0 text-secondary transition-colors duration-[80ms] hover:bg-hover hover:text-primary"
                onClick={() => {
                  setSettingsInitialTab('general');
                  setShowSettings(true);
                }}
                aria-label={t('设置')}
              >
                <Settings size={16} />
              </button>
            </Tiptop>
            <div className="window-divider" />
            <Tiptop text={t('最小化')} placement="bottom">
              <button
                type="button"
                className="no-drag w-7 h-7 p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center shrink-0 text-secondary transition-colors duration-[80ms] hover:bg-hover hover:text-primary"
                onClick={WindowMinimise}
                aria-label={t('最小化')}
              >
                <Minus size={14} />
              </button>
            </Tiptop>
            <Tiptop text={t('最大化')} placement="bottom">
              <button
                type="button"
                className="no-drag w-7 h-7 p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center shrink-0 text-secondary transition-colors duration-[80ms] hover:bg-hover hover:text-primary"
                onClick={handleToggleMaximise}
                aria-label={t('最大化')}
              >
                <Square size={14} />
              </button>
            </Tiptop>
            <Tiptop text={t('关闭')} placement="bottom">
              <button
                type="button"
                className="no-drag w-7 h-7 p-0 rounded-[var(--radius-sm)] inline-flex items-center justify-center shrink-0 text-secondary transition-colors duration-[80ms] hover:bg-danger hover:text-white"
                aria-label={t('关闭')}
                onClick={handleCloseWindow}
              >
                <X size={14} />
              </button>
            </Tiptop>
          </div>
        </div>
      </div>
    </>
  );
}
