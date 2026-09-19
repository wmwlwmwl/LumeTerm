import { ChevronLeft, ChevronRight } from 'lucide-react';
import type React from 'react';
import Tiptop from '../Tiptop.tsx';
import type { PanelResizeDirection } from '../../hooks/useWorkspacePanelDocking.ts';

export interface WorkspaceSidePanesProps {
  position: 'left' | 'right';
  aiPanelNode: React.ReactNode;
  probePanelNode: React.ReactNode;
  probePanelVisible: boolean;
  probePanelPosition: 'left' | 'right';
  probePanelCollapsed: boolean;
  probePanelWidth: number;
  showAIPanel: boolean;
  isActiveSessionConnected: boolean;
  collapseDragIntent: unknown;
  setAIPanelVisibility: (v: boolean) => void;
  setProbePanelCollapsedPersistent: (next: boolean) => void;
  startDrag: (event: React.MouseEvent<HTMLElement> | MouseEvent, direction: PanelResizeDirection) => void;
  shouldIgnoreResizerClick: () => boolean;
  t: (key: string, vars?: Record<string, unknown>) => string;
}

export default function WorkspaceSidePanes({
  position,
  aiPanelNode,
  probePanelNode,
  probePanelVisible,
  probePanelPosition,
  probePanelCollapsed,
  probePanelWidth,
  showAIPanel,
  isActiveSessionConnected,
  collapseDragIntent,
  setAIPanelVisibility,
  setProbePanelCollapsedPersistent,
  startDrag,
  shouldIgnoreResizerClick,
  t,
}: WorkspaceSidePanesProps) {
  if (position === 'left') {
    return (
      <>
        {aiPanelNode && probePanelPosition === 'right' && (
          <>
            {aiPanelNode}
            {isActiveSessionConnected && (showAIPanel ? (
              <Tiptop text={t('收起 AI 助手面板')} placement="bottom" className="flex">
                <div
                  className={`split-resizer-v hotzone-left${collapseDragIntent === 'ai' ? ' armed' : ''}`}
                  onMouseDown={(e) => startDrag(e, 'ai')}
                  onClick={() => {
                    if (shouldIgnoreResizerClick()) return;
                    setAIPanelVisibility(false);
                  }}
                  aria-label={t('收起 AI 助手面板')}
                />
              </Tiptop>
            ) : (
              <Tiptop text={t('打开 AI 助手面板')} placement="bottom">
                <button
                  type="button"
                  className="panel-collapse-strip panel-collapse-strip-vertical panel-collapse-strip-left no-drag"
                  onClick={() => setAIPanelVisibility(true)}
                  aria-label={t('打开 AI 助手面板')}
                >
                  <ChevronRight size={14} />
                </button>
              </Tiptop>
            ))}
          </>
        )}
        {probePanelNode && probePanelPosition === 'left' && (
          <>
            <div
              className="probe-panel-wrapper probe-panel-wrapper-left relative border-r border-line"
              style={{
                display: probePanelVisible && !probePanelCollapsed ? 'block' : 'none',
                width: probePanelWidth,
                minWidth: probePanelWidth,
                background: 'var(--surface-base)',
                borderLeft: 'none',
              }}
            >
              {collapseDragIntent === 'probe' && (
                <div className="panel-collapse-armed-zone panel-collapse-armed-zone-vertical panel-collapse-armed-zone-right">
                  <ChevronLeft size={14} />
                </div>
              )}
              {probePanelNode}
            </div>
            {probePanelVisible && (probePanelCollapsed ? (
              <Tiptop text={t('展开监控面板')} placement="bottom">
                <button
                  type="button"
                  className="panel-collapse-strip panel-collapse-strip-vertical panel-collapse-strip-left no-drag"
                  onClick={() => setProbePanelCollapsedPersistent(false)}
                  aria-label={t('展开监控面板')}
                >
                  <ChevronRight size={14} />
                </button>
              </Tiptop>
            ) : (
              <Tiptop text={t('收起监控面板')} placement="bottom" className="flex">
                <div
                  className={`split-resizer-v hotzone-left probe-resizer${collapseDragIntent === 'probe' ? ' armed' : ''}`}
                  onMouseDown={(e) => startDrag(e, 'probe')}
                  onClick={() => {
                    if (shouldIgnoreResizerClick()) return;
                    setProbePanelCollapsedPersistent(true);
                  }}
                  aria-label={t('收起监控面板')}
                />
              </Tiptop>
            ))}
          </>
        )}
      </>
    );
  }

  // position === 'right'
  return (
    <>
      {probePanelNode && probePanelPosition === 'right' && (
        <>
          {probePanelVisible && (probePanelCollapsed ? (
            <Tiptop text={t('展开监控面板')} placement="bottom">
              <button
                type="button"
                className="panel-collapse-strip panel-collapse-strip-vertical panel-collapse-strip-right no-drag"
                onClick={() => setProbePanelCollapsedPersistent(false)}
                aria-label={t('展开监控面板')}
              >
                <ChevronLeft size={14} />
              </button>
            </Tiptop>
          ) : (
            <Tiptop text={t('收起监控面板')} placement="bottom" className="flex">
              <div
                className={`split-resizer-v hotzone-right probe-resizer${collapseDragIntent === 'probe' ? ' armed' : ''}`}
                onMouseDown={(e) => startDrag(e, 'probe')}
                onClick={() => {
                  if (shouldIgnoreResizerClick()) return;
                  setProbePanelCollapsedPersistent(true);
                }}
                aria-label={t('收起监控面板')}
              />
            </Tiptop>
          ))}
          <div
            className="probe-panel-wrapper relative"
            style={{
              display: probePanelVisible && !probePanelCollapsed ? 'block' : 'none',
              width: probePanelWidth,
              minWidth: probePanelWidth,
              background: 'var(--surface-base)',
            }}
          >
            {collapseDragIntent === 'probe' && (
              <div className="panel-collapse-armed-zone panel-collapse-armed-zone-vertical panel-collapse-armed-zone-left">
                <ChevronRight size={14} />
              </div>
            )}
            {probePanelNode}
          </div>
        </>
      )}
      {aiPanelNode && probePanelPosition === 'left' && (
        <>
          {isActiveSessionConnected && (showAIPanel ? (
            <Tiptop text={t('收起 AI 助手面板')} placement="bottom" className="flex">
              <div
                className={`split-resizer-v hotzone-right${collapseDragIntent === 'ai' ? ' armed' : ''}`}
                onMouseDown={(e) => startDrag(e, 'ai')}
                onClick={() => {
                  if (shouldIgnoreResizerClick()) return;
                  setAIPanelVisibility(false);
                }}
                aria-label={t('收起 AI 助手面板')}
              />
            </Tiptop>
          ) : (
            <Tiptop text={t('打开 AI 助手面板')} placement="bottom">
              <button
                type="button"
                className="panel-collapse-strip panel-collapse-strip-vertical panel-collapse-strip-right no-drag"
                onClick={() => setAIPanelVisibility(true)}
                aria-label={t('打开 AI 助手面板')}
              >
                <ChevronLeft size={14} />
              </button>
            </Tiptop>
          ))}
          {aiPanelNode}
        </>
      )}
    </>
  );
}
