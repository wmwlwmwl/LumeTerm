import { useState, useRef, useCallback } from 'react';
import type { Terminal as XTerm } from '@xterm/xterm';
import type { FitAddon } from '@xterm/addon-fit';
import type { SearchAddon } from '@xterm/addon-search';
import { useTranslation } from '../i18n.ts';
import { createHighlightState, loadKeywordRulesFromStorage } from '../utils/terminalKeywordHighlight.ts';
import { Z } from '../constants/zIndex';
import type { TerminalProps } from './terminal/terminalTypes.ts';
import { useTerminalTimestamps } from './terminal/useTerminalTimestamps.ts';
import { useTerminalLinkUnderlines } from './terminal/useTerminalLinkUnderlines.ts';
import { useTerminalGutter } from './terminal/useTerminalGutter.ts';
import { useTerminalTheme } from './terminal/useTerminalTheme.ts';
import { useTerminalSettingsEvents } from './terminal/useTerminalSettingsEvents.ts';
import { useTerminalClipboard } from './terminal/useTerminalClipboard.ts';
import { useTerminalSession } from './terminal/useTerminalSession.ts';
import { useTerminalQuickCmd } from './terminal/useTerminalQuickCmd.ts';
import { useTerminalCommandInput } from './terminal/useTerminalCommandInput.ts';
import { useTerminalHistory } from './terminal/useTerminalHistory.ts';
import { useTerminalSearch } from './terminal/useTerminalSearch.ts';
import { useTerminalMenus } from './terminal/useTerminalMenus.ts';
import { TerminalBackground, TerminalStatusBar, TerminalViewport } from './terminal/TerminalSurface.tsx';
import { TerminalSearchBar } from './terminal/TerminalSearchBar.tsx';
import { TerminalQuickCmdBar } from './terminal/TerminalQuickCmdBar.tsx';
import { TerminalInputBar } from './terminal/TerminalInputBar.tsx';
import { TerminalAutocompletePopup } from './terminal/TerminalAutocompletePopup.tsx';
import { TerminalHistoryPopup } from './terminal/TerminalHistoryPopup.tsx';
import { TerminalQuickCmdConfirm } from './terminal/TerminalQuickCmdConfirm.tsx';
import { TerminalContextMenu, TerminalLinkMenu, TerminalLinkToast } from './terminal/TerminalMenus.tsx';

// 启动时从 localStorage 加载自定义关键字规则（模块级，仅执行一次）
loadKeywordRulesFromStorage();

export type { TerminalProps } from './terminal/terminalTypes.ts';

// 终端组件外壳：组合 terminal/ 目录下的 hook 与子组件。
// 各关注点实现见同名文件（会话主链路 / gutter / 主题 / 剪贴板 / 查找 / 菜单 / 输入栏等）。
export default function Terminal({
  sessionId, serverId, historyServerId, status, isActive, serverName,
  connectedSessions: _connectedSessions = [], showCommands = false, onQuickCommandsOpenChange, quickCmdsRef, wsRebuildKey = 0,
}: TerminalProps) {
  const { t } = useTranslation();
  const containerRef   = useRef<HTMLDivElement | null>(null);
  const wrapperRef     = useRef<HTMLDivElement | null>(null);
  const termRef        = useRef<XTerm | null>(null);
  const fitAddonRef    = useRef<FitAddon | null>(null);
  const searchAddonRef = useRef<SearchAddon | null>(null);
  const termSearchInputRef = useRef<HTMLInputElement | null>(null);
  const wsRef          = useRef<WebSocket | null>(null);
  const serverIdRef    = useRef(serverId);
  serverIdRef.current  = serverId;

  // ── 菜单 / 弹层 / 查找等跨 hook 共享状态 ──
  const [contextMenu, setContextMenu]         = useState<{ x: number; y: number; source: 'terminal' | 'input' } | null>(null);
  const [linkMenu, setLinkMenu]               = useState<{ x: number; y: number; url: string } | null>(null); // { x, y, url }
  const [linkToast, setLinkToast]             = useState('');
  const [contextHasSelection, setContextHasSelection] = useState(false);
  const [showHistory, setShowHistory]         = useState(false);
  const [historyPopupPos, setHistoryPopupPos] = useState<{ left: number; bottom: number } | null>(null);
  const [altOpenHistoryEnabled, setAltOpenHistoryEnabled] = useState(localStorage.getItem('altOpenHistory') !== 'false');
  const [showTermSearch, setShowTermSearch]   = useState(false);
  const [termSearchQuery, setTermSearchQuery] = useState('');
  const [termSearchCaseSensitive, setTermSearchCaseSensitive] = useState(false);
  const [termSearchResult, setTermSearchResult] = useState({ resultIndex: -1, resultCount: 0 });
  const pendingCmdRef                         = useRef('');
  const awaitingPasswordRef                   = useRef(false); // 检测到密码提示后，下一行输入不记入命令历史
  const awaitingCommandFinishRef              = useRef(false); // 按回车提交命令后，等待命令完成（提示符回归）

  // 热路径缓存：避免在按键和消息回调中频繁读取 localStorage
  const shortcutsRef = useRef<Record<string, string> | null>(null);
  const localEchoRef = useRef(localStorage.getItem('terminalLocalEcho') === 'true');
  const timestampsEnabledRef = useRef(localStorage.getItem('terminalTimestamps') === 'true');
  const terminalRightClickPasteOnEmptyRef = useRef(localStorage.getItem('terminalRightClickPasteOnEmpty') === 'true');
  const terminalRightClickPasteModeRef = useRef(localStorage.getItem('terminalRightClickPasteMode') === 'always' ? 'always' : 'empty');
  const terminalLeftClickCopyOnSelectionRef = useRef(localStorage.getItem('terminalLeftClickCopyOnSelection') === 'true');
  const terminalLeftClickCopyOnSelectionModeRef = useRef(localStorage.getItem('terminalLeftClickCopyOnSelectionMode') === 'mouseup' ? 'mouseup' : 'click');
  const [timestampsVisible, setTimestampsVisible] = useState(localStorage.getItem('terminalTimestamps') === 'true');
  // 命令块：左侧折叠钮 + 树线，可收起输出
  const commandBlocksEnabledRef = useRef(localStorage.getItem('terminalCommandBlocks') === 'true');
  const [commandBlocksVisible, setCommandBlocksVisible] = useState(localStorage.getItem('terminalCommandBlocks') === 'true');
  const [terminalDefaultMouseCursorEnabled, setTerminalDefaultMouseCursorEnabled] = useState(localStorage.getItem('terminalOutputDefaultMouseCursor') === 'true');
  const keywordHighlightEnabledRef = useRef(localStorage.getItem('terminalKeywordHighlight') === 'true');
  // 关键字高亮：二进制帧流式解码器（每次建连重置，保证 UTF-8 跨帧字符完整）
  const hlDecoderRef = useRef(new TextDecoder());
  // 关键字高亮：per-session 前景色状态。服务端着色区间可能跨帧，
  // 需跨帧跟踪 fgActive 才不会误注入/误清色；每个终端会话独立持有，
  // 多标签/分屏互不污染。建连 / 开关切换时一并重置。
  const hlStateRef = useRef(createHighlightState());
  const [alternateBufferActive, setAlternateBufferActive] = useState(false);
  const alternateBufferActiveRef = useRef(false);
  const screenScrollbackRef = useRef({ pending: false, active: false });
  const prepareScreenScrollbackRef = useRef<(command: string) => void>(() => {});

  // ── 时间戳 ring / 链接下划线 / 命令块 gutter（api 对象向下游 hook 传递） ──
  const tsApi = useTerminalTimestamps({ timestampsEnabledRef });
  const linkApi = useTerminalLinkUnderlines({ termRef, containerRef });
  const { linkUnderlineLayerRef } = linkApi;
  const gutterApi = useTerminalGutter({
    termRef, containerRef, timestampsEnabledRef, commandBlocksEnabledRef, alternateBufferActiveRef,
    commandBlocksVisible, t, ...tsApi,
  });
  const { gutterRef } = gutterApi;

  // ── 主题/壁纸与设置事件监听 ──
  const { T, themeToggle, bgInfo, terminalContainerStyle } = useTerminalTheme({ termRef, wrapperRef });
  useTerminalSettingsEvents({
    ...gutterApi,
    termRef, fitAddonRef, shortcutsRef, localEchoRef, timestampsEnabledRef, commandBlocksEnabledRef,
    terminalRightClickPasteOnEmptyRef, terminalRightClickPasteModeRef,
    terminalLeftClickCopyOnSelectionRef, terminalLeftClickCopyOnSelectionModeRef,
    keywordHighlightEnabledRef, hlDecoderRef, hlStateRef,
    setTimestampsVisible, setCommandBlocksVisible, setTerminalDefaultMouseCursorEnabled, setAltOpenHistoryEnabled,
  });

  // ── 剪贴板 / 选区手势 ──
  const {
    isTerminalPointerDownRef, dispatchSyntheticTerminalMouseUp,
    pasteClipboardToTerminal, pasteTerminalSelectionToTerminal,
    handleTerminalMouseDownCapture, handleTerminalMouseUpCapture,
  } = useTerminalClipboard({
    termRef, containerRef, wsRef, pendingCmdRef, awaitingCommandFinishRef, t,
    terminalRightClickPasteOnEmptyRef, terminalRightClickPasteModeRef,
    terminalLeftClickCopyOnSelectionRef, terminalLeftClickCopyOnSelectionModeRef,
  });

  // ── xterm/WebSocket 会话主链路 ──
  useTerminalSession({
    ...tsApi, ...gutterApi, ...linkApi,
    sessionId, wsRebuildKey, status, isActive, t, T,
    containerRef, termRef, fitAddonRef, searchAddonRef, wsRef, serverIdRef,
    shortcutsRef, localEchoRef, timestampsEnabledRef, commandBlocksEnabledRef,
    alternateBufferActiveRef, setAlternateBufferActive,
    screenScrollbackRef, prepareScreenScrollbackRef,
    awaitingPasswordRef, awaitingCommandFinishRef, pendingCmdRef,
    isTerminalPointerDownRef, dispatchSyntheticTerminalMouseUp,
    keywordHighlightEnabledRef, hlDecoderRef, hlStateRef, termSearchInputRef,
    setShowTermSearch, setTermSearchQuery, setTermSearchResult, setContextMenu, setLinkMenu,
    pasteTerminalSelectionToTerminal,
  });

  const isConnected  = status === 'connected';
  const isClosed     = status === 'closed';
  const isError      = status === 'error';
  const [multiLineWrapEnabled, setMultiLineWrapEnabled] = useState(() => localStorage.getItem('terminalMultiLineWrapEnabled') !== 'false');

  // ── 快捷命令条 ──
  const {
    quickCmdBarVisible, quickCmdBarItems, quickCmdSearch, setQuickCmdSearch, quickCmdSearchOpen, setQuickCmdSearchOpen,
    closeQuickCmdSearch, filteredQuickCmdItems, pendingQuickCmd, setPendingQuickCmd,
    quickCmdHistoryParam, setQuickCmdHistoryParam, quickCmdHistoryPosition, setQuickCmdHistoryPosition,
    quickCmdHistorySearch, setQuickCmdHistorySearch, quickCmdParamHistory, setQuickCmdParamHistory, quickCmdParamHistoryRef,
    openQuickCmdConfirm, sendQuickCmdConfirmed,
  } = useTerminalQuickCmd({
    isConnected, sessionId, serverId, multiLineWrapEnabled,
    prepareScreenScrollbackRef, awaitingPasswordRef, awaitingCommandFinishRef, termRef,
  });

  // ── 命令输入 / 自动补全 ──
  const {
    cmdInput, setCmdInput, cmdInputRef, commandAutocomplete, setCommandAutocomplete,
    commandAutocompletePopupPos, commandAutocompleteListRef, commandAutocompleteFocusedRef,
    commandAutocompleteKeyboardNavigationRef, commandAutocompleteBlurTimerRef,
    cmdInputHintsHidden, toggleCommandInputHints, closeCommandAutocomplete,
    scheduleCommandAutocompleteSuggestions, loadCommandAutocompleteSuggestions, applyCommandAutocompleteItem,
    updateCommandAutocompletePopupPosition, clearCommandAutocompleteBlurTimer,
    syncCommandInputHeight, executeCommand, copyCommand,
  } = useTerminalCommandInput({
    sessionId, serverId, historyServerId, showHistory, showCommands,
    isConnected, isClosed, isError, multiLineWrapEnabled,
    prepareScreenScrollbackRef, awaitingPasswordRef, awaitingCommandFinishRef, termRef,
    openQuickCmdConfirm, setShowHistory, setHistoryPopupPos, t,
  });

  // ── 历史指令弹窗 ──
  const {
    setHistoryList, historyMode, setHistoryMode, searchQuery, setSearchQuery, historySelectedIndex,
    historyBtnRef, historySearchInputRef, historyScrollRef, historyPopupRef,
    filteredHistory, displayHistory, handleHistorySearchKeyDown,
    toggleHistory, openHistoryAndFocusSearch, toggleCommands, selectHistoryCmd, deleteHistoryItem,
  } = useTerminalHistory({
    showHistory, setShowHistory, setHistoryPopupPos, historyServerId, serverId, showCommands,
    onQuickCommandsOpenChange, quickCmdsRef, setCmdInput, cmdInputRef,
  });

  // ── 终端缓冲区查找 ──
  const { openTermSearch, closeTermSearch, findTermNext, findTermPrevious } = useTerminalSearch({
    showTermSearch, termSearchQuery, termSearchCaseSensitive,
    setShowTermSearch, setTermSearchQuery, setTermSearchResult,
    termRef, searchAddonRef, termSearchInputRef, T, themeToggle, isActive, wrapperRef, shortcutsRef,
  });

  // ── 右键 / 链接菜单 ──
  const {
    handleContextMenu, closeContextMenu,
    handleLinkMenuAction, handleMenuAction,
  } = useTerminalMenus({
    termRef, cmdInputRef, setCmdInput, contextMenu, setContextMenu, contextHasSelection, setContextHasSelection,
    linkMenu, setLinkMenu, setLinkToast, serverIdRef, sessionId,
    pasteClipboardToTerminal, pasteTerminalSelectionToTerminal, openTermSearch,
    syncCommandInputHeight, scheduleCommandAutocompleteSuggestions, closeCommandAutocomplete,
    commandAutocompleteFocusedRef, terminalRightClickPasteOnEmptyRef, terminalRightClickPasteModeRef, t,
  });

  const cmdTrimmed = cmdInput.trim();

  const toggleMultiLineWrap = useCallback(() => {
    setMultiLineWrapEnabled((previous) => {
      const next = !previous
      localStorage.setItem('terminalMultiLineWrapEnabled', next ? 'true' : 'false')
      return next
    })
    requestAnimationFrame(() => {
      cmdInputRef.current?.focus()
      syncCommandInputHeight()
    })
  }, [syncCommandInputHeight])

  return (
    <div
      ref={wrapperRef}
      onContextMenu={handleContextMenu}
      onClick={closeContextMenu}
      style={terminalContainerStyle}
      // 主题底色 + 色调层；壁纸半透明叠在上面
      className="relative h-full flex flex-col overflow-hidden bg-[var(--term-container-bg)]"
    >
      <TerminalBackground T={T} bgInfo={bgInfo} />

      {/* 内容层（置于背景之上) */}
      <div className="relative flex flex-col flex-1 min-h-0 h-full overflow-hidden" style={{ zIndex: Z.CONTENT }}>
      <TerminalStatusBar status={status} serverName={serverName} sessionId={sessionId} t={t} />

      {/* ── 终端内容查找栏 ── */}
      {showTermSearch && (
        <TerminalSearchBar
          termSearchInputRef={termSearchInputRef} termSearchQuery={termSearchQuery} setTermSearchQuery={setTermSearchQuery}
          termSearchResult={termSearchResult} termSearchCaseSensitive={termSearchCaseSensitive}
          setTermSearchCaseSensitive={setTermSearchCaseSensitive} closeTermSearch={closeTermSearch}
          findTermNext={findTermNext} findTermPrevious={findTermPrevious} t={t}
        />
      )}

      {/* ── xterm 渲染层 + 时间轴 / 命令块边框 ── */}
      <TerminalViewport
        timestampsVisible={timestampsVisible} commandBlocksVisible={commandBlocksVisible} alternateBufferActive={alternateBufferActive}
        terminalDefaultMouseCursorEnabled={terminalDefaultMouseCursorEnabled} containerRef={containerRef}
        handleTerminalMouseDownCapture={handleTerminalMouseDownCapture} handleTerminalMouseUpCapture={handleTerminalMouseUpCapture}
        gutterRef={gutterRef} linkUnderlineLayerRef={linkUnderlineLayerRef}
      />

      {/* ── 快捷命令条（输入框上方，横向滚动，点击后弹确认框） ── */}
      {quickCmdBarVisible && (
        <TerminalQuickCmdBar
          quickCmdBarItems={quickCmdBarItems} filteredQuickCmdItems={filteredQuickCmdItems}
          quickCmdSearch={quickCmdSearch} setQuickCmdSearch={setQuickCmdSearch}
          quickCmdSearchOpen={quickCmdSearchOpen} setQuickCmdSearchOpen={setQuickCmdSearchOpen}
          closeQuickCmdSearch={closeQuickCmdSearch} openQuickCmdConfirm={openQuickCmdConfirm} isConnected={isConnected} t={t}
        />
      )}

      {/* ── 底部命令输入栏 ── */}
      <TerminalInputBar
        cmdInput={cmdInput} setCmdInput={setCmdInput} cmdInputRef={cmdInputRef} cmdInputHintsHidden={cmdInputHintsHidden}
        commandAutocomplete={commandAutocomplete} setCommandAutocomplete={setCommandAutocomplete}
        commandAutocompleteFocusedRef={commandAutocompleteFocusedRef}
        commandAutocompleteKeyboardNavigationRef={commandAutocompleteKeyboardNavigationRef}
        commandAutocompleteBlurTimerRef={commandAutocompleteBlurTimerRef}
        scheduleCommandAutocompleteSuggestions={scheduleCommandAutocompleteSuggestions}
        clearCommandAutocompleteBlurTimer={clearCommandAutocompleteBlurTimer}
        updateCommandAutocompletePopupPosition={updateCommandAutocompletePopupPosition}
        closeCommandAutocomplete={closeCommandAutocomplete}
        loadCommandAutocompleteSuggestions={loadCommandAutocompleteSuggestions}
        applyCommandAutocompleteItem={applyCommandAutocompleteItem} toggleCommandInputHints={toggleCommandInputHints}
        altOpenHistoryEnabled={altOpenHistoryEnabled} openHistoryAndFocusSearch={openHistoryAndFocusSearch}
        setShowHistory={setShowHistory}
        showHistory={showHistory} showCommands={showCommands} historyBtnRef={historyBtnRef}
        toggleHistory={toggleHistory} toggleCommands={toggleCommands} executeCommand={executeCommand}
        cmdTrimmed={cmdTrimmed} isConnected={isConnected} copyCommand={copyCommand}
        multiLineWrapEnabled={multiLineWrapEnabled} toggleMultiLineWrap={toggleMultiLineWrap} t={t}
      />
      </div>

      {(commandAutocomplete.open || commandAutocomplete.loading) && !showHistory && !showCommands && commandAutocompletePopupPos && (
        <TerminalAutocompletePopup
          commandAutocomplete={commandAutocomplete} setCommandAutocomplete={setCommandAutocomplete}
          commandAutocompletePopupPos={commandAutocompletePopupPos} commandAutocompleteListRef={commandAutocompleteListRef}
          applyCommandAutocompleteItem={applyCommandAutocompleteItem} t={t}
        />
      )}

      {/* ── 历史指令弹窗（fixed 定位，不受 overflow:hidden 裁剪） ── */}
      {showHistory && historyPopupPos && (
        <TerminalHistoryPopup
          historyPopupPos={historyPopupPos} historyPopupRef={historyPopupRef} historyScrollRef={historyScrollRef}
          historySearchInputRef={historySearchInputRef} altOpenHistoryEnabled={altOpenHistoryEnabled}
          setAltOpenHistoryEnabled={setAltOpenHistoryEnabled} historyMode={historyMode} setHistoryMode={setHistoryMode}
          setHistoryList={setHistoryList} setShowHistory={setShowHistory} setHistoryPopupPos={setHistoryPopupPos}
          filteredHistory={filteredHistory} displayHistory={displayHistory} searchQuery={searchQuery}
          setSearchQuery={setSearchQuery} historySelectedIndex={historySelectedIndex}
          handleHistorySearchKeyDown={handleHistorySearchKeyDown} selectHistoryCmd={selectHistoryCmd}
          executeCommand={executeCommand} deleteHistoryItem={deleteHistoryItem}
          serverId={serverId} historyServerId={historyServerId} t={t}
        />
      )}

      {/* ── 快捷命令二次确认框（ui/Modal；z 层降到 Z.DIALOG） ── */}
      {pendingQuickCmd && (
        <TerminalQuickCmdConfirm
          pendingQuickCmd={pendingQuickCmd} setPendingQuickCmd={setPendingQuickCmd}
          sendQuickCmdConfirmed={sendQuickCmdConfirmed} isConnected={isConnected}
          quickCmdHistoryParam={quickCmdHistoryParam} setQuickCmdHistoryParam={setQuickCmdHistoryParam}
          quickCmdHistoryPosition={quickCmdHistoryPosition} setQuickCmdHistoryPosition={setQuickCmdHistoryPosition}
          quickCmdHistorySearch={quickCmdHistorySearch} setQuickCmdHistorySearch={setQuickCmdHistorySearch}
          quickCmdParamHistory={quickCmdParamHistory} setQuickCmdParamHistory={setQuickCmdParamHistory}
          quickCmdParamHistoryRef={quickCmdParamHistoryRef} t={t}
        />
      )}

      {/* ── 右键上下文菜单（ui/ContextMenu，items 按 source 组装） ── */}
      {contextMenu && (
        <TerminalContextMenu
          contextMenu={contextMenu} setContextMenu={setContextMenu} contextHasSelection={contextHasSelection}
          handleMenuAction={handleMenuAction} shortcutsRef={shortcutsRef} t={t}
        />
      )}

      {/* ── 终端链接菜单：复制 / 打开（对齐安卓） ── */}
      {linkMenu && (
        <TerminalLinkMenu
          linkMenu={linkMenu} setLinkMenu={setLinkMenu} handleLinkMenuAction={handleLinkMenuAction}
          termRef={termRef} t={t}
        />
      )}

      {linkToast && (
        <TerminalLinkToast linkToast={linkToast} />
      )}
    </div>
  );
}
