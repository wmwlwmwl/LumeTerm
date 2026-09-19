import { useEffect, useRef, useCallback } from 'react';
import type * as React from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import type { IBufferRange, IMarker, ITerminalInitOnlyOptions, ITerminalOptions } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { SearchAddon } from '@xterm/addon-search';
import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';
import { clampMenuPosition } from '../../utils/menuPosition.ts';
import { getSolidTerminalBackground, type TerminalTheme } from '../../utils/theme.ts';
import { warnDev } from '../../utils/devLog';
import { getResolvedProgramFontPreferences } from '../../utils/programFonts.ts';
import { highlightKeywords, createHighlightState } from '../../utils/terminalKeywordHighlight.ts';
import type { I18nKey } from '../../i18n.ts';
import {
  DEFAULT_TERMINAL_SHORTCUTS,
  extractCommandFromBufferLine,
  formatTerminalTimestamp,
  getTerminalBufferSnapshotText,
  isInteractivePromptText,
  normalizeTerminalPasteText,
  startsInteractiveScreen,
  textDecoder,
  textEncoder,
} from '../../utils/terminalHelpers.ts';
import { createTerminalLocalEchoFilter } from './terminalLocalEchoFilter.ts';
import { createTerminalKeyEventHandler } from './terminalKeyEventHandler.ts';

type LooseT = (key: I18nKey, vars?: Record<string, unknown>) => string;

// ── 初始化 xterm + WebSocket 终端通道 ────────────────────────────────
// xterm.js 通过 AttachAddon + WebSocket 直接连到本地 Go WebSocket 服务器
// 完全绕开 Wails IPC跨进程通信，走 TCP loopback 延迟极低
// （主体从 Terminal.tsx 原样搬移；回显过滤与自定义快捷键分别见 terminalLocalEchoFilter / terminalKeyEventHandler）
export function useTerminalSession(deps: {
  sessionId: string;
  wsRebuildKey: number;
  status: string;
  isActive: boolean;
  t: LooseT;
  T: TerminalTheme;
  containerRef: React.RefObject<HTMLDivElement | null>;
  termRef: React.RefObject<XTerm | null>;
  fitAddonRef: React.RefObject<FitAddon | null>;
  searchAddonRef: React.RefObject<SearchAddon | null>;
  wsRef: React.RefObject<WebSocket | null>;
  serverIdRef: React.RefObject<string>;
  shortcutsRef: React.RefObject<Record<string, string> | null>;
  localEchoRef: React.RefObject<boolean>;
  timestampsEnabledRef: React.RefObject<boolean>;
  commandBlocksEnabledRef: React.RefObject<boolean>;
  alternateBufferActiveRef: React.RefObject<boolean>;
  setAlternateBufferActive: React.Dispatch<React.SetStateAction<boolean>>;
  screenScrollbackRef: React.RefObject<{ pending: boolean; active: boolean }>;
  prepareScreenScrollbackRef: React.RefObject<(command: string) => void>;
  awaitingPasswordRef: React.RefObject<boolean>;
  awaitingCommandFinishRef: React.RefObject<boolean>;
  pendingCmdRef: React.RefObject<string>;
  isTerminalPointerDownRef: React.RefObject<boolean>;
  dispatchSyntheticTerminalMouseUp: (clientX?: number, clientY?: number) => void;
  keywordHighlightEnabledRef: React.RefObject<boolean>;
  hlDecoderRef: React.RefObject<TextDecoder>;
  hlStateRef: React.RefObject<ReturnType<typeof createHighlightState>>;
  termSearchInputRef: React.RefObject<HTMLInputElement | null>;
  setShowTermSearch: React.Dispatch<React.SetStateAction<boolean>>;
  setTermSearchQuery: React.Dispatch<React.SetStateAction<string>>;
  setTermSearchResult: React.Dispatch<React.SetStateAction<{ resultIndex: number; resultCount: number }>>;
  setContextMenu: React.Dispatch<React.SetStateAction<{ x: number; y: number; source: 'terminal' | 'input' } | null>>;
  setLinkMenu: React.Dispatch<React.SetStateAction<{ x: number; y: number; url: string } | null>>;
  isShellPromptLine: (text: string) => boolean;
  isCollapseSummaryLine: (text: string) => boolean;
  cbRewriteLockRef: React.RefObject<boolean>;
  tsSet: (marker: IMarker | undefined, val: string) => void;
  tsClearLine: (line: number) => void;
  tsClear: () => void;
  cbClear: () => void;
  gutterRef: React.RefObject<HTMLDivElement | null>;
  gutterSyncRAFRef: React.RefObject<number | null>;
  linkUnderlineLayerRef: React.RefObject<HTMLDivElement | null>;
  linkUnderlineSyncRAFRef: React.RefObject<number | null>;
  getViewportLinkCache: (term: XTerm) => Map<number, Array<{ text: string; range: IBufferRange }>>;
  scheduleGutterSync: () => void;
  scheduleLinkUnderlineSync: () => void;
  handleClearScreen: () => void;
  pasteTerminalSelectionToTerminal: () => void | Promise<void>;
}) {
  const {
    sessionId, wsRebuildKey, status, isActive, t, T,
    containerRef, termRef, fitAddonRef, searchAddonRef, wsRef, serverIdRef,
    shortcutsRef, localEchoRef, timestampsEnabledRef, commandBlocksEnabledRef,
    alternateBufferActiveRef, setAlternateBufferActive,
    screenScrollbackRef, prepareScreenScrollbackRef,
    awaitingPasswordRef, awaitingCommandFinishRef, pendingCmdRef,
    isTerminalPointerDownRef, dispatchSyntheticTerminalMouseUp,
    keywordHighlightEnabledRef, hlDecoderRef, hlStateRef,
    termSearchInputRef, setShowTermSearch, setTermSearchQuery, setTermSearchResult,
    setContextMenu, setLinkMenu,
    isShellPromptLine, isCollapseSummaryLine, cbRewriteLockRef,
    tsSet, tsClearLine, tsClear, cbClear,
    gutterRef, gutterSyncRAFRef, linkUnderlineLayerRef, linkUnderlineSyncRAFRef,
    getViewportLinkCache, scheduleGutterSync, scheduleLinkUnderlineSync,
    handleClearScreen, pasteTerminalSelectionToTerminal,
  } = deps;

  const connectWebSocketRef = useRef<(() => void) | null>(null);
  const smartWriteRef = useRef<((data: string | Uint8Array) => void) | null>(null);
  const statusRef = useRef(status);
  useEffect(() => { statusRef.current = status; }, [status]);

  const userPinnedRef = useRef(false);
  const lastSentPTYSizeRef = useRef<{ cols: number; rows: number }>({ cols: 0, rows: 0 });
  const ptyResizeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const scheduleDebouncedPTYResize = useCallback((cols: number, rows: number, immediate = false) => {
    const MIN_COLS = 20;
    const MIN_ROWS = 2;
    const clampedCols = Math.max(MIN_COLS, cols);
    const clampedRows = Math.max(MIN_ROWS, rows);
    // 必须先取消挂起的 resize 再去重：快速拖动窗口回到 earlier size 时，
    // 去重早退若不清定时器，过期的更大尺寸会在 80ms 后仍发给 PTY，
    // 造成 shell 折行列数与终端实际列数长期错位
    if (ptyResizeTimerRef.current) {
      clearTimeout(ptyResizeTimerRef.current);
      ptyResizeTimerRef.current = null;
    }
    if (lastSentPTYSizeRef.current.cols === clampedCols && lastSentPTYSizeRef.current.rows === clampedRows) {
      return;
    }
    const send = () => {
      lastSentPTYSizeRef.current = { cols: clampedCols, rows: clampedRows };
      AppGo.ResizeTerminal(sessionId, clampedCols, clampedRows);
    };
    if (immediate) {
      send();
    } else {
      ptyResizeTimerRef.current = setTimeout(send, 80);
    }
  }, [sessionId]);

  useEffect(() => {
    if (!containerRef.current) return;

    containerRef.current.innerHTML = '';

    const fontSize = parseInt(localStorage.getItem('terminalFontSize') || '13', 10);

    const term = new XTerm({
      // background 用不透明的容器底色：nano/vim 的反转显示（SGR 7）需要真实的背景色参与
      // 互换，透明底会退化成黑字黑底；壁纸/色调层改为叠在内容上方保持观感
      theme:            { ...T.xterm, background: getSolidTerminalBackground(T) },
      fontFamily:       getResolvedProgramFontPreferences().terminalFontFamily,
      fontSize:         fontSize,
      fontWeight:       500,
      fontWeightBold:   700,
      lineHeight:       1.22,
      letterSpacing:    0.3,
      // 关自动反差：搜索高亮底上白字会被压成黑字
      minimumContrastRatio: 0,
      cursorBlink:      true,
      cursorStyle:      'bar',
      cursorWidth:      1,
      scrollback:       5000,
      reflowCursorLine: true,
      // SearchAddon 高亮装饰依赖 proposed API
      allowProposedApi: true,
      fastScrollModifier: 'alt',
      macOptionIsMeta:  true,
      padding:          8,
      windowOptions: {
        setWinSizeChars: true
      }
      // xterm 5 类型未声明 padding 选项（运行期仍生效），按构造参数类型断言
    } as ITerminalOptions & ITerminalInitOnlyOptions & { padding?: number });

    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    const searchAddon = new SearchAddon({ highlightLimit: 1000 });
    term.loadAddon(searchAddon);
    searchAddonRef.current = searchAddon;
    const searchResultsDisposable = searchAddon.onDidChangeResults((result) => {
      setTermSearchResult({
        resultIndex: typeof result?.resultIndex === 'number' ? result.resultIndex : -1,
        resultCount: typeof result?.resultCount === 'number' ? result.resultCount : 0,
      });
    });
    // 点击/手型用 provider；常驻下划线用覆盖层。可见区扫描走 getViewportLinkCache
    const linkProviderDisposable = term.registerLinkProvider({
      provideLinks(bufferLineNumber, callback) {
        const found = getViewportLinkCache(term).get(bufferLineNumber) || [];
        if (!found.length) {
          callback(undefined);
          return;
        }
        callback(found.map(({ text, range }) => ({
          text,
          range,
          decorations: { underline: false, pointerCursor: true },
          activate(event, uri) {
            event?.preventDefault?.();
            event?.stopPropagation?.();
            try { term.clearSelection(); } catch (_) {}
            requestAnimationFrame(() => { try { term.clearSelection(); } catch (_) {} });
            const x = event?.clientX ?? 0;
            const y = event?.clientY ?? 0;
            setContextMenu(null);
            setLinkMenu({ ...clampMenuPosition(x, y, 200, 96), url: uri });
          },
        })));
      },
    });
    term.open(containerRef.current);
    screenScrollbackRef.current.pending = false;
    screenScrollbackRef.current.active = false;
    const syncTuiState = (screenActive = screenScrollbackRef.current.active) => {
      const active = term.buffer.active.type === 'alternate' || screenActive;
      alternateBufferActiveRef.current = active;
      setAlternateBufferActive(active);
      if (active) {
        if (gutterSyncRAFRef.current !== null) {
          cancelAnimationFrame(gutterSyncRAFRef.current);
          gutterSyncRAFRef.current = null;
        }
        if (gutterRef.current) gutterRef.current.innerHTML = '';
        if (linkUnderlineLayerRef.current) linkUnderlineLayerRef.current.innerHTML = '';
      } else {
        scheduleGutterSync();
        scheduleLinkUnderlineSync();
      }
    };
    prepareScreenScrollbackRef.current = (command) => {
      if (screenScrollbackRef.current.active) return;
      screenScrollbackRef.current.pending = startsInteractiveScreen(command);
    };
    const screenAltModeSetDisposable = term.parser.registerCsiHandler({ prefix: '?', final: 'h' }, (params) => {
      const mode = params.length === 1 && typeof params[0] === 'number' ? params[0] : 0;
      if ((!screenScrollbackRef.current.pending && !screenScrollbackRef.current.active) || (mode !== 47 && mode !== 1047 && mode !== 1049)) return false;
      screenScrollbackRef.current.pending = false;
      screenScrollbackRef.current.active = true;
      syncTuiState(true);
      return true;
    });
    const screenAltModeResetDisposable = term.parser.registerCsiHandler({ prefix: '?', final: 'l' }, (params) => {
      const mode = params.length === 1 && typeof params[0] === 'number' ? params[0] : 0;
      if (!screenScrollbackRef.current.active || (mode !== 47 && mode !== 1047 && mode !== 1049)) return false;
      screenScrollbackRef.current.active = false;
      screenScrollbackRef.current.pending = false;
      syncTuiState(false);
      // normal buffer 已承载 screen 历史，不能再执行 1049l 的旧光标恢复，否则长日志会把提示符拉回已裁剪位置。
      return true;
    });
    try { fitAddon.fit(); } catch (_) {}
    const terminalInput = containerRef.current.querySelector('.xterm-helper-textarea') as HTMLTextAreaElement | null;
    if (terminalInput) {
      terminalInput.name = 'terminalInput';
      terminalInput.autocomplete = 'off';
    }
    alternateBufferActiveRef.current = false;
    setAlternateBufferActive(false);

    // ── 智能写入：用户手动滚动上时保持位置 ─────────────────────────
    userPinnedRef.current = false;
    const onTermScroll = () => {
      const buf = term.buffer.active;
      // 滚到底部时解除锁定
      if (buf.viewportY >= buf.baseY) {
        userPinnedRef.current = false;
      }
      scheduleGutterSync();
      scheduleLinkUnderlineSync();
    };
    const scrollDisposable = term.onScroll(onTermScroll);
    // 直接监听 xterm 视口 DOM scroll 事件作为更可靠的备选
    const vpEl = containerRef.current.querySelector('.xterm-viewport');
    if (vpEl) {
      vpEl.addEventListener('scroll', onTermScroll, { passive: true });
    }

    // ── 每行时间戳 / 命令块：marker 跟随 xterm scrollback 裁剪 ──
    const lineFeedDisposable = term.onLineFeed(() => {
      if (alternateBufferActiveRef.current || term.buffer.active.type !== 'normal') return;
      if (!timestampsEnabledRef.current && !commandBlocksEnabledRef.current) return;

      const buf = term.buffer.active;
      const cursorLine = buf.baseY + buf.cursorY;
      // 往回跳过 isWrapped 包裹行，记到逻辑行首行
      let pos = cursorLine - 1;
      while (pos > 0) {
        const line = buf.getLine(pos);
        if (line && line.isWrapped) { pos--; } else { break; }
      }
      if (pos < 0) return;

      // 收起/展开改写 buffer 时不要打新时间戳；摘要行不打。
      // 回车完成的行（含「空提示符出现」/「执行命令」）都用当前时刻覆盖旧戳。
      if (timestampsEnabledRef.current && !cbRewriteLockRef.current) {
        const posText = buf.getLine(pos)?.translateToString(true) || '';
        if (!isCollapseSummaryLine(posText)) {
          tsClearLine(pos);
          tsSet(term.registerMarker(pos - cursorLine), formatTerminalTimestamp());
        }
      }
      // 命令块由 gutter sync 扫描提示符决定，lineFeed 只需刷新
      if (commandBlocksEnabledRef.current && !cbRewriteLockRef.current) {
        scheduleGutterSync();
      }
    });
    const writeParsedDisposable = term.onWriteParsed(() => {
      scheduleGutterSync();
      scheduleLinkUnderlineSync();
      // 命令完成检测：仅当处于"等待命令完成"状态且不在 TUI（备用屏）时，
      // 检查光标所在行是否已回归 shell 提示符。命中则派发事件并退出等待态。
      // 提示符识别复用 isShellPromptLine；只取末行避免全量扫描。
      if (!awaitingCommandFinishRef.current || alternateBufferActiveRef.current) return;
      const buf = term.buffer.active;
      if (!buf) return;
      const lastLine = buf.getLine(buf.baseY + buf.cursorY);
      const text = lastLine ? lastLine.translateToString(true) : '';
      if (isShellPromptLine(text)) {
        awaitingCommandFinishRef.current = false;
        window.dispatchEvent(new CustomEvent('ssh-command-finished', {
          detail: { sessionId: serverIdRef.current }
        }));
      }
    });
    const bufferChangeDisposable = term.buffer.onBufferChange((buffer) => {
      syncTuiState(buffer.type === 'alternate' || screenScrollbackRef.current.active);
    });
    const wheelHandler = (e: WheelEvent) => {
      // 触控板双指滚动打断选区状态防呆：
      // 当双指滚动开始时，若由于单指先行接触残留了 isTerminalPointerDownRef 状态，
      // 且当前无物理按键按下，主动释放状态并闭合选区，防止滚动后单指滑动意外划选文字
      if (isTerminalPointerDownRef.current && (e.buttons === 0 || !(e.buttons & 1))) {
        isTerminalPointerDownRef.current = false;
        dispatchSyntheticTerminalMouseUp(e.clientX, e.clientY);
      }
      // 无论向上还是向下滚动，都检查当前位置并更新锁定状态
      requestAnimationFrame(() => {
        const buf = term.buffer.active;
        userPinnedRef.current = buf.viewportY < buf.baseY;
      });
    };
    containerRef.current?.addEventListener('wheel', wheelHandler, { passive: true });

    const isClearScreenData = (d: string | Uint8Array) => {
      if (!d) return false;
      if (typeof d === 'string') return d.includes('\x1b[2J') || d.includes('\x1b[3J');
      // Binary: scan for \x1b[2J (clear) or \x1b[3J (clear scrollback)
      if (!d.includes(0x1b)) return false;
      for (let i = 0; i <= d.length - 4; i++) {
        if (d[i] === 0x1b && d[i+1] === 0x5b && (d[i+2] === 0x32 || d[i+2] === 0x33) && d[i+3] === 0x4a) {
          return true;
        }
      }
      return false;
    };
    const smartWrite = (data: string | Uint8Array) => {
      if (isClearScreenData(data)) handleClearScreen();
      // 关键字高亮：高亮开启时 onmessage 已统一解码为字符串（incomingText），这里只需处理字符串；
      // 关闭时数据为原始 string/Uint8Array，直接透传不高亮。
      let writeData = data;
      if (keywordHighlightEnabledRef.current && typeof data === 'string') {
        writeData = highlightKeywords(data, hlStateRef.current);
      }
      if (userPinnedRef.current) {
        // xterm.js 在用户不在底部时已经会保持滚动位置。
        // 之前用 scrollToLine(savedY) 在异步回调中执行，会在用户向下滚动后
        // 把视图拉回旧位置，导致用户无法追上最新输出。
        // 现在仅在 xterm.js 自动滚动打断时才恢复（用相对偏移检测）。
        const buf = term.buffer.active;
        const offset = buf.baseY - buf.viewportY;
        term.write(writeData, () => {
          const newBuf = term.buffer.active;
          // 只有当 offset 变小（说明 xterm 自动滚动了）才恢复
          if (newBuf.baseY - newBuf.viewportY < offset) {
            const newY = newBuf.baseY - offset;
            if (newY >= 0) term.scrollToLine(newY);
          }
        });
      } else {
        term.write(writeData);
      }
    };
    smartWriteRef.current = smartWrite;

    // ── DOM 渲染器（WebGL 在 CJK/宽字符支持差，使用默认 DOM 渲染确保中文正常显示）──

    termRef.current    = term;
    fitAddonRef.current = fitAddon;
    window.__luminTerminalSnapshots = window.__luminTerminalSnapshots || {};
    window.__luminTerminalSnapshots[sessionId] = () => getTerminalBufferSnapshotText(termRef.current || term);

    const fitTimer = setTimeout(() => {
      try { fitAddon.fit(); } catch (_) {}
    }, 100);

    // ── 自定义快捷键 ──────────────────────────────────────────────

    // 初始化快捷键缓存（移出按键热路径，仅在首次或变更时读取）
    if (shortcutsRef.current === null) {
      let defaults: Record<string, string>;
      try {
        const saved = localStorage.getItem('appShortcuts');
        defaults = saved
          ? { ...DEFAULT_TERMINAL_SHORTCUTS, ...JSON.parse(saved) }
          : { ...DEFAULT_TERMINAL_SHORTCUTS };
      } catch (_) {
        defaults = { ...DEFAULT_TERMINAL_SHORTCUTS };
      }
      shortcutsRef.current = defaults;
    }

    // 快捷键处理逻辑见 terminalKeyEventHandler.ts（原回调体原样搬移）
    term.attachCustomKeyEventHandler(createTerminalKeyEventHandler({
      term,
      shortcutsRef,
      wsRef,
      pendingCmdRef,
      termRef,
      termSearchInputRef,
      setShowTermSearch,
      setTermSearchQuery,
      pasteTerminalSelectionToTerminal,
    }));

    // ── WebSocket 连接 & Predictive Local Echo ─────────────────────
    let ws: WebSocket | null = null;
    let wsConnecting = false;
    let cancelled = false;
    // 本地回显预测过滤（预测队列 / 流式解码 / 残留转义序列）见 terminalLocalEchoFilter.ts
    const echoFilter = createTerminalLocalEchoFilter();
    // 重置高亮流式解码器，避免上一次连接的残留字节污染本次输出
    hlDecoderRef.current = new TextDecoder();
    // 同步重置前景色状态：上次连接可能在颜色区间未闭合时断开（fgActive=true），
    // 不清掉会让新连接开局误判为「前景色已激活」而哑火高亮
    hlStateRef.current = createHighlightState();

    const connectWebSocket = () => {
      if (cancelled || wsConnecting || statusRef.current !== 'connected' || wsRef.current) return;
      wsConnecting = true;
      // 并行获取端口与鉴权 token，后端要求连接时通过 ?token=xxx 携带，防止本机恶意进程注入命令
      Promise.all([AppGo.GetWsPort(), AppGo.GetWsToken()]).then(([port, token]) => {
        if (cancelled || statusRef.current !== 'connected' || wsRef.current || !port || !termRef.current) return;
        const tokenQuery = token ? `?token=${encodeURIComponent(token)}` : '';
        const currentWs = new WebSocket(`ws://127.0.0.1:${port}/ws/${sessionId}${tokenQuery}`);
        ws = currentWs;
        currentWs.binaryType = 'arraybuffer';
        wsRef.current = currentWs;
        currentWs.onclose = () => {
          if (wsRef.current === currentWs) wsRef.current = null;
        };

      currentWs.onopen = () => {
        // 补发一次初始尺寸：终端首次 fit 发生在 onResize 订阅之前，那次
        // 尺寸变化事件被错过，本地 PTY 可能长期停留在出生尺寸；这里主动
        // 同步一次，同时给 SIGWINCH 会重绘提示符的 shell（bash/zsh）兜底自愈机会。
        if (termRef.current) {
          scheduleDebouncedPTYResize(termRef.current.cols, termRef.current.rows, true);
        }
      };

      currentWs.onmessage = (ev) => {
        if (!termRef.current) return;
        // 在原始数据上检测清屏序列（不依赖后续文本处理路径）
        const rawBytes = typeof ev.data === 'string' ? null : new Uint8Array(ev.data);
        // 统一解码：高亮开启时整个连接只用一个流式解码器（hlDecoderRef），
        // 避免「快速路径 / 回显过滤路径」各自持有一个解码器导致跨帧 UTF-8 失步损坏
        const incomingText = (keywordHighlightEnabledRef.current && rawBytes)
          ? hlDecoderRef.current.decode(rawBytes, { stream: true })
          : null;
        if (timestampsEnabledRef.current) {
          if (typeof ev.data === 'string' && (ev.data.includes('\x1b[2J') || ev.data.includes('\x1b[3J'))) {
            handleClearScreen();
          } else if (rawBytes && rawBytes.includes(0x1b)) {
            for (let i = 0; i <= rawBytes.length - 4; i++) {
              if (rawBytes[i] === 0x1b && rawBytes[i+1] === 0x5b && (rawBytes[i+2] === 0x32 || rawBytes[i+2] === 0x33) && rawBytes[i+3] === 0x4a) {
                handleClearScreen();
                break;
              }
            }
          }
        }

          // 检测密码提示，标记下一行输入为密码（不记入命令历史）
        if (!awaitingPasswordRef.current) {
          const probeText = incomingText ?? (typeof ev.data === 'string' ? ev.data : textDecoder.decode(ev.data));
          // ponytail: 只在最后一行像密码/验证码提示时触发（关键词 + 行尾冒号），
          // 避免 "admin password: xxx" 之类信息性输出误判，导致下一条普通命令被跳过。
          // 行尾冒号是强约束，关键词可适度放宽：覆盖 OTP/MFA/Token 等验证码提示
          const lastLine = (probeText.split(/\r?\n/).pop() || '').trim();
          if (/(password|passwd|passphrase|密码|verification|otp|token|2fa|mfa|auth.*code)/i.test(lastLine) && /[:：]\s*$/.test(lastLine)) {
            awaitingPasswordRef.current = true;
          }
        }

        const shouldFilterIncomingText = echoFilter.shouldFilterIncomingText(localEchoRef.current)

        if (!shouldFilterIncomingText) {
          echoFilter.noteFastPath();
          smartWrite(incomingText ?? (typeof ev.data === 'string' ? ev.data : new Uint8Array(ev.data)));
          return;
        }

        const text = incomingText ?? (typeof ev.data === 'string' ? ev.data : echoFilter.decodeStream(new Uint8Array(ev.data)));
        // 写回经过滤的文本（整帧不完整时 filterIncoming 返回 null，跳过写入）
        const newText = echoFilter.filterIncoming(text);
        if (newText === null) {
          return;
        }
        smartWrite(newText);
      };

      currentWs.onerror = (e) => warnDev('[Terminal] WebSocket error', e);
      }).finally(() => {
        wsConnecting = false;
      });
    };
    connectWebSocketRef.current = connectWebSocket;
    connectWebSocket();

    // ── 历史指令记录 + 输入直觉 + Local Echo ────────────────────────
    let localInputLength = 0; // 用于保护提示符，防止退格越界
    let pendingCmdReliable = true;

    term.onData((data) => {
      if ((statusRef.current === 'closed' || statusRef.current === 'error') && (data.includes('\r') || data.includes('\n'))) {
        window.dispatchEvent(new CustomEvent('ssh-reconnect-trigger', { detail: sessionId }));
        return;
      }

      // 粘贴等多字符输入：把 \\r\\n / \\n 收成单个 \\r，保证 bash 的 \\ 续行不断
      let out = data;
      if (out.length > 1 && /[\r\n]/.test(out)) {
        out = normalizeTerminalPasteText(out);
      }

      userPinnedRef.current = false;

      if (wsRef.current?.readyState === WebSocket.OPEN) {
        wsRef.current.send(textEncoder.encode(out));
      }

      // ── 命令记录：优先读取终端可见缓冲区的当前行（屏幕上实际渲染、
      // 真正被 shell 执行的命令），可正确捕捉 Tab 补全 / 方向键调历史 /
      // Ctrl+R 等 shell 编辑结果；pendingCmdRef 仅在缓冲读取失败时作兜底。
      if (out.includes('\r') || out.includes('\n')) {
        if (term.buffer.active.type === 'alternate') {
          pendingCmdRef.current = '';
          return;
        }
        // 多行粘贴：只把最后一行之前的可见内容并入历史，避免把整段 paste 拆烂
        const lines = out.split(/\r/).filter((line, i, arr) => i < arr.length - 1 || line.length > 0);
        if (lines.length > 1) {
          for (const line of lines) {
            const piece = line.replace(/[\x00-\x1F\x7F]/g, '');
            if (piece) pendingCmdRef.current += (pendingCmdRef.current ? ' ' : '') + piece;
          }
        } else {
          const nlIdx = out.search(/[\r\n]/);
          if (nlIdx > 0) {
            pendingCmdRef.current += out.slice(0, nlIdx).replace(/[\x00-\x1F\x7F]/g, '');
          }
        }
        let cmd = '';
        const buf = term.buffer.active;
        let promptFilteredThisTurn = false;
        if (buf) {
          const bufLine = buf.getLine(buf.baseY + buf.cursorY);
          const text = bufLine ? bufLine.translateToString(true) : '';
          cmd = extractCommandFromBufferLine(text);
          // 含控制字符（C0 0x00-0x1F / DEL / C1 0x80-0x9F，多为 ANSI 序列残留）
          // 或交互脚本提示：视为无效，回退到逐字符累加。注意保留合法 Unicode
          // （如提示符 ❯、中文路径、emoji 参数），不再按"非 ASCII"一刀切丢弃。
          const hasControl = /[\x00-\x1F\x7F-\x9F]/.test(cmd);
          const isPrompt = isInteractivePromptText(cmd);
          if (hasControl || isPrompt) {
            promptFilteredThisTurn = true;
            cmd = '';
          }
        }
        const pending = pendingCmdRef.current.trim();
        if (!promptFilteredThisTurn) {
          if (!cmd) {
            cmd = pending;
          } else if (pendingCmdReliable && pending) {
            const c = cmd.toLowerCase(), p = pending.toLowerCase();
            if (!c.startsWith(p) && !p.startsWith(c)) cmd = '';
          }
        }
        if (!awaitingPasswordRef.current) {
          prepareScreenScrollbackRef.current(cmd);
        }
        if (!awaitingPasswordRef.current && cmd.length > 1 && !/^\d+$/.test(cmd)) {
          window.dispatchEvent(new CustomEvent('ssh-command-history', {
            detail: { sessionId: serverIdRef.current, command: cmd, time: new Date().toISOString(), source: 'input' }
          }));
        }
        // 非密码输入且提交了实际命令：进入"等待命令完成"状态，
        // 待提示符回归（onWriteParsed 检测）时派发 ssh-command-finished 事件，
        // 供文件管理器自动刷新当前目录。
        awaitingCommandFinishRef.current = !awaitingPasswordRef.current && cmd.length > 0;
        awaitingPasswordRef.current = false;
        pendingCmdRef.current = '';
        pendingCmdReliable = true;
      } else if (out === '\x7F' || out === '\b') {
        pendingCmdRef.current = pendingCmdRef.current.slice(0, -1);
      } else if (!/[\x00-\x1F\x7F]/.test(out)) {
        pendingCmdRef.current += out;
      } else if (out === '\x03' || out === '\x04') {
        pendingCmdRef.current = '';
        pendingCmdReliable = true;
        if (!screenScrollbackRef.current.active) screenScrollbackRef.current.pending = false;
        awaitingPasswordRef.current = false; // Ctrl+C/D 取消当前输入，重置密码等待状态，避免下一条普通命令被误跳过
      } else {
        pendingCmdReliable = false;
      }

      // Local Echo 逻辑 (恢复默认开启)
      if (localEchoRef.current) {
        // 如果输入中不包含控制字符（如方向键、Esc、退格等），则视作常规可见输入（支持多字符连击或粘贴）
        if (!/[\x00-\x1F\x7F]/.test(out)) {
          // 由于 JavaScript 中部分多字节字符的 length 表现，这里按照字符串常规长度累加是安全的。
          // 因为退格也是按字符来删的。
          localInputLength += out.length;
          for (let i = 0; i < out.length; i++) {
            echoFilter.pushPendingEcho(out[i]);
          }
          term.write(out);
        } else if (out === '\x7F') { // Backspace
          // 仅当我们确信这是用户刚刚输入的字符时，才在本地执行退格预测。
          // 否则（localInputLength <= 0），将退格完全交还给服务器，保护提示符不被删除。
          if (localInputLength > 0) {
            localInputLength--;
            echoFilter.pushPendingEcho(out);
            term.write('\b \b'); // 本地立即执行退格效果
          }
        } else if (out === '\r' || out === '\n' || out === '\r\n' || (out.length > 1 && /[\r\n]/.test(out))) {
          localInputLength = 0;
        } else {
          // 遇到方向键、Ctrl快捷键（如 Ctrl+C/D/Z）等控制符，
          // 立刻清零预测输入长度，安全退回到服务器渲染模式
          localInputLength = 0;
        }
      }

    });

    const resizeDisposable = term.onResize(({ cols, rows }) => {
      scheduleDebouncedPTYResize(cols, rows, false);
      scheduleGutterSync();
      scheduleLinkUnderlineSync();
    });
    // 首帧同步常驻下划线
    scheduleLinkUnderlineSync();

    return () => {
      cancelled = true;
      if (connectWebSocketRef.current === connectWebSocket) connectWebSocketRef.current = null;
      scrollDisposable.dispose();
      lineFeedDisposable.dispose();
      writeParsedDisposable.dispose();
      bufferChangeDisposable.dispose();
      screenAltModeSetDisposable.dispose();
      screenAltModeResetDisposable.dispose();
      resizeDisposable.dispose();
      try { linkProviderDisposable.dispose(); } catch (_) {}
      try { searchResultsDisposable.dispose(); } catch (_) {}
      try { searchAddon.dispose(); } catch (_) {}
      if (gutterSyncRAFRef.current !== null) {
        cancelAnimationFrame(gutterSyncRAFRef.current);
        gutterSyncRAFRef.current = null;
      }
      if (linkUnderlineSyncRAFRef.current !== null) {
        cancelAnimationFrame(linkUnderlineSyncRAFRef.current);
        linkUnderlineSyncRAFRef.current = null;
      }
      if (linkUnderlineLayerRef.current) linkUnderlineLayerRef.current.innerHTML = '';
      clearTimeout(fitTimer);
      // 取消挂起的防抖 PTY resize，避免拆卸后仍向会话发送过期尺寸
      if (ptyResizeTimerRef.current) {
        clearTimeout(ptyResizeTimerRef.current);
        ptyResizeTimerRef.current = null;
      }
      if (vpEl) vpEl.removeEventListener('scroll', onTermScroll);
      // 移除 wheel 监听器，避免内存泄漏
      containerRef.current?.removeEventListener('wheel', wheelHandler);
      if (ws) { try { ws.close(); } catch (_) {} }
      if (wsRef.current === ws) wsRef.current = null;
      tsClear(); // 清理时间戳
      cbClear(); // 清理命令块边框
      if (window.__luminTerminalSnapshots?.[sessionId]) {
        delete window.__luminTerminalSnapshots[sessionId];
      }
      smartWriteRef.current = null;
      screenScrollbackRef.current.pending = false;
      screenScrollbackRef.current.active = false;
      prepareScreenScrollbackRef.current = () => {};
      alternateBufferActiveRef.current = false;
      termRef.current     = null;
      fitAddonRef.current = null;
      searchAddonRef.current = null;
      // xterm Viewport 构造里 setTimeout(syncScrollArea) 无句柄；StrictMode 先 dispose 再触发会读空 renderer.dimensions
      // 延后 dispose，让该 setTimeout 先跑完（同队列 FIFO）
      const termToDispose = term;
      setTimeout(() => {
        try { termToDispose.dispose(); } catch (_) {}
      }, 0);
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, wsRebuildKey]);

  // ── 监听字体大小修改事件 ──────────────────────────────────────
  useEffect(() => {
    const handleFontSizeChange = (e: Event) => {
      if (termRef.current) {
        termRef.current.options.fontSize = (e as CustomEvent<number>).detail;
        if (fitAddonRef.current) {
          try { fitAddonRef.current.fit(); } catch (_) {}
        }
        scheduleGutterSync();
      }
    };
    window.addEventListener('terminal-font-size-changed', handleFontSizeChange);
    return () => window.removeEventListener('terminal-font-size-changed', handleFontSizeChange);
  }, []);

  // SSH/本地/串口断开时终端组件会保活以保留输出；单独关闭 WS，释放
  // 浏览器连接和 Go ReadMessage goroutine。重连后只重建 WS，不重建 xterm。
  useEffect(() => {
    if (status === 'closed' || status === 'error') {
      screenScrollbackRef.current.pending = false;
      screenScrollbackRef.current.active = false;
      const actualAlternate = termRef.current?.buffer.active.type === 'alternate';
      alternateBufferActiveRef.current = actualAlternate;
      setAlternateBufferActive(actualAlternate);
      const ws = wsRef.current;
      wsRef.current = null;
      if (ws) {
        try { ws.close(); } catch (_) {}
      }
      return;
    }
    if (status === 'connected') {
      connectWebSocketRef.current?.();
    }
  }, [status]);

  // ── 状态变化提示 ─────────────────────────────────────────────────
  useEffect(() => {
    if (!termRef.current) return;
    const sw = smartWriteRef.current;
    const writeMsg = (msg: string) => {
      if (sw) sw(msg);
      else termRef.current?.write(msg);
    };
    if (status === 'error') {
      writeMsg('\r\n\x1b[31m✗ ' + t('连接失败') + '\x1b[0m\r\n');
    } else if (status === 'closed') {
      writeMsg('\r\n\x1b[33m⚠ ' + t('已断开') + '\x1b[0m\r\n');
    }
  }, [status]);

  const safeFit = useCallback((immediate = false) => {
    if (!termRef.current || !fitAddonRef.current || !containerRef.current) return;
    const rect = containerRef.current.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) return;
    try {
      const term = termRef.current;
      const dims = fitAddonRef.current.proposeDimensions();
      if (!dims || isNaN(dims.cols) || isNaN(dims.rows)) return;
      const MIN_COLS = 20;
      const MIN_ROWS = 2;
      const cols = Math.max(MIN_COLS, dims.cols);
      const rows = Math.max(MIN_ROWS, dims.rows);
      if (term.cols !== cols || term.rows !== rows) {
        term.resize(cols, rows);
      }
      scheduleDebouncedPTYResize(cols, rows, immediate);

      // xterm 5 在后台（display:none）时 IntersectionObserver 会将 RenderService 设为 paused，
      // 导致尺寸虽然更新但 renderer canvas 仍停留在旧高度，Viewport 因计算出 scrollHeight > height 而误显示滚动条。
      // 此处主动解除 pause、冲刷任务队列、同步尺寸并触发 Viewport 重新计算，彻底消除切标签后误显滚动条的问题。
      try {
        const core = (term as any)._core;
        if (core?._renderService) {
          if (core._renderService._isPaused) {
            core._renderService._isPaused = false;
            core._renderService._pausedResizeTask?.flush();
          }
          core._renderService.handleResize?.(cols, rows);
          core._renderService.refreshRows?.(0, rows - 1);
        }
        const buf = term.buffer.active;
        const targetLine = userPinnedRef.current ? Math.min(buf.viewportY, buf.baseY) : buf.baseY;
        if (!userPinnedRef.current) {
          term.scrollToBottom();
        }
        if (core?._viewport) {
          core._viewport._latestYDisp = targetLine;
          core._viewport.scrollToLine?.(targetLine, true);
          core._viewport.queueSync?.(targetLine);
        }
      } catch (_) {}

      // 安排下一帧（RAF）再次确认底部同步，防止 xterm 内部异步 refresh 回调覆盖底部位置
      requestAnimationFrame(() => {
        try {
          const t = termRef.current;
          if (!t) return;
          const b = t.buffer.active;
          const c = (t as any)._core;
          if (!userPinnedRef.current) {
            t.scrollToBottom();
            if (c?._viewport) {
              c._viewport._latestYDisp = b.baseY;
              c._viewport.scrollToLine?.(b.baseY, true);
              c._viewport.queueSync?.(b.baseY);
            }
          }
        } catch (_) {}
      });
    } catch (e) {
      warnDev('[Terminal] safeFit error:', e);
    }
  }, [scheduleDebouncedPTYResize]);

  // ── 监听容器与窗口大小变化以及侧边栏切换进行自适应 ─────────────────
  useEffect(() => {
    if (!isActive || !containerRef.current) return;

    let resizeTimer: ReturnType<typeof setTimeout> | null = null;
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        if (entry.contentRect.width > 0 && entry.contentRect.height > 0) {
          if (resizeTimer) clearTimeout(resizeTimer);
          resizeTimer = setTimeout(() => {
            safeFit(false);
          }, 30);
        }
      }
    });

    observer.observe(containerRef.current);

    let windowResizeTimer: ReturnType<typeof setTimeout> | null = null;
    const handleWindowResize = () => {
      if (windowResizeTimer) clearTimeout(windowResizeTimer);
      windowResizeTimer = setTimeout(() => {
        safeFit(false);
      }, 30);
    };
    window.addEventListener('resize', handleWindowResize);

    const handleAIPanelChange = () => {
      // 侧边栏/AI面板展开或收起时，采用多阶段自适应确保尺寸与底部锚定完全就绪
      safeFit(true);
      requestAnimationFrame(() => {
        safeFit(true);
        requestAnimationFrame(() => {
          safeFit(true);
        });
      });
      setTimeout(() => safeFit(true), 60);
      setTimeout(() => safeFit(true), 150);
    };
    window.addEventListener('ai-panel-visibility-changed', handleAIPanelChange);

    return () => {
      if (resizeTimer) clearTimeout(resizeTimer);
      if (windowResizeTimer) clearTimeout(windowResizeTimer);
      observer.disconnect();
      window.removeEventListener('resize', handleWindowResize);
      window.removeEventListener('ai-panel-visibility-changed', handleAIPanelChange);
    };
  }, [isActive, safeFit]);

  // ── 终端切换回来时，多阶段重新自适应 ──────────────────────────────
  useEffect(() => {
    if (!isActive) return;

    // 多阶段重适应：立即执行、RAF 执行、double-RAF 以及 60ms/150ms/300ms 延迟，
    // 覆盖标签切换后容器 display:flex 恢复、子标签栏渲染以及 CSS 布局完全就绪的各个阶段
    safeFit(true);
    const raf1 = requestAnimationFrame(() => {
      safeFit(true);
      const raf2 = requestAnimationFrame(() => {
        safeFit(true);
      });
      return () => cancelAnimationFrame(raf2);
    });
    const t1 = setTimeout(() => safeFit(true), 60);
    const t2 = setTimeout(() => safeFit(true), 150);
    const t3 = setTimeout(() => safeFit(true), 300);

    return () => {
      cancelAnimationFrame(raf1);
      clearTimeout(t1);
      clearTimeout(t2);
      clearTimeout(t3);
    };
  }, [isActive, safeFit]);
}
