import { useEffect, useState } from 'react';
import type * as React from 'react';
import { Z } from '../../constants/zIndex';
import { isDarkTerminalSurface, type TerminalTheme } from '../../utils/theme.ts';
import type { I18nKey } from '../../i18n.ts';

type LooseT = (key: I18nKey, vars?: Record<string, unknown>) => string;

// 主题色调层 + 壁纸层（叠在内容上方）。从 Terminal.tsx 原样搬移。
export function TerminalBackground({
  T,
  bgInfo,
}: {
  T: TerminalTheme;
  bgInfo: { image: string; opacity: number; coverTerminal: boolean };
}) {
  return (
    <>
      {/* 主题色调层：xterm 背景已不透明，叠在内容上方才能生效（弹出层 fixed+zIndex 更高，不受影响） */}
      <div
        className="absolute inset-0 pointer-events-none bg-[var(--term-tint,transparent)]"
        // 独立合成层：下方元素悬浮过渡时强制正确重合成，避免半透明层残留旧帧拖影
        style={{ zIndex: Z.STACK, transform: 'translateZ(0)' }}
      />
      {/* 壁纸层：叠在内容上方。混合模式按明暗对称：浅色 multiply（暗部印上浅底），深色 screen（亮部浮上黑底），
          保证两种主题下壁纸都有足够视觉存在感 */}
      {/* 无自定义终端壁纸或全局背景覆盖时不渲染纹理，与全局背景一致：仅用户上传后才显示 */}
      <div
        className="absolute inset-0 pointer-events-none bg-cover bg-center"
        style={{
          zIndex: Z.STACK,
          transform: 'translateZ(0)',
          backgroundImage: bgInfo.coverTerminal || !bgInfo.image ? '' : `url("${bgInfo.image}")`,
          // 封顶 0.9：壁纸层在内容上方（1.2.6 起），100% 会完全盖住终端文字
          opacity: Math.min(Number.isFinite(bgInfo.opacity) ? bgInfo.opacity : 0.15, 0.9),
          mixBlendMode: isDarkTerminalSurface(T) ? 'screen' : 'multiply',
        }}
      />
    </>
  );
}

// Session 状态栏：状态指示灯（连接成功涟漪动画）+ 服务器名 + 连接状态/重连。
export function TerminalStatusBar({
  status,
  serverName,
  sessionId,
  t,
}: {
  status: string;
  serverName: string;
  sessionId: string;
  t: LooseT;
}) {
  const isConnected  = status === 'connected';
  const isConnecting = status === 'connecting';
  const isError      = status === 'error';
  const isClosed     = status === 'closed';
  const statusColor  = isConnected ? 'var(--success)' : isConnecting ? 'var(--warning)' : isError ? 'var(--danger)' : 'var(--text-tertiary)';
  const [justConnected, setJustConnected] = useState(false);

  // 连接成功时触发一次性涟漪动画
  useEffect(() => {
    if (isConnected) {
      setJustConnected(true);
      const timer = setTimeout(() => setJustConnected(false), 1400);
      return () => clearTimeout(timer);
    }
  }, [isConnected]);

  return (
    <div className="term-status-bar">
      {/* 状态指示灯 - 使用全局 CSS 类，连接成功时触发涟漪动画 */}
      <div className={[
        'status-dot',
        'shrink-0',
        isConnected  ? (justConnected ? 'just-connected' : 'online') : '',
        isConnecting ? 'connecting' : '',
        isError      ? 'offline' : '',
        !isConnected && !isConnecting && !isError ? 'offline' : '',
      ].filter(Boolean).join(' ')} />
      <span className="font-medium font-mono text-[var(--term-server-color)]">
        {serverName || 'Terminal'}
      </span>

      {/* 右侧极简状态显示 */}
      <div className="ml-auto flex items-center gap-2.5">
        <span className="text-xs font-mono font-bold" style={{ color: statusColor }}>
          {isConnected  ? t('已连接')
           : isConnecting ? t('连接中...')
           : isError      ? t('错误')
           : t('离线')}
        </span>
        {(isError || isClosed) && (
          <button
            className="term-reconnect-btn"
            onClick={() => {
              window.dispatchEvent(new CustomEvent('ssh-reconnect-trigger', { detail: sessionId }));
            }}
          >
            {t('重新连接')}
          </button>
        )}
      </div>
    </div>
  );
}

// xterm 渲染层 + 时间轴 / 命令块边框 + 常驻链接下划线层。
export function TerminalViewport({
  timestampsVisible,
  commandBlocksVisible,
  alternateBufferActive,
  terminalDefaultMouseCursorEnabled,
  handleTerminalMouseDownCapture,
  handleTerminalMouseUpCapture,
  containerRef,
  gutterRef,
  linkUnderlineLayerRef,
}: {
  timestampsVisible: boolean;
  commandBlocksVisible: boolean;
  alternateBufferActive: boolean;
  terminalDefaultMouseCursorEnabled: boolean;
  handleTerminalMouseDownCapture: (event: React.MouseEvent) => void;
  handleTerminalMouseUpCapture: (event: React.MouseEvent) => void;
  containerRef: React.RefObject<HTMLDivElement | null>;
  gutterRef: React.RefObject<HTMLDivElement | null>;
  linkUnderlineLayerRef: React.RefObject<HTMLDivElement | null>;
}) {
  return (
    <div className="flex-1 min-h-0 flex overflow-hidden">
      <div ref={gutterRef} className="shrink-0 pt-0 overflow-hidden box-border" style={{
        display: (timestampsVisible || commandBlocksVisible) && !alternateBufferActive ? 'block' : 'none',
        // 时间戳约 72px；命令块约 16px；两者同时开约 96px
        // 时间戳列 70 + 命令块 14 + padding ≈ 90；仅时间戳 75；仅命令块 22
        width: timestampsVisible && commandBlocksVisible ? 90 : (timestampsVisible ? 75 : 22),
      }} />
      <div
        className={terminalDefaultMouseCursorEnabled ? 'terminal-output-default-mouse-cursor relative flex-1 min-h-0 overflow-hidden' : 'relative flex-1 min-h-0 overflow-hidden'}
        onMouseDownCapture={handleTerminalMouseDownCapture}
        onMouseUpCapture={handleTerminalMouseUpCapture}
      >
        <div
          ref={containerRef}
          className="h-full min-h-0 p-0 bg-transparent overflow-hidden"
        />
        {/* 常驻链接下划线（pointer-events:none，不挡点击/选区） */}
        <div
          ref={linkUnderlineLayerRef}
          className="absolute inset-0 pointer-events-none overflow-hidden"
          style={{ zIndex: Z.STACK }}
        />
      </div>
    </div>
  );
}
