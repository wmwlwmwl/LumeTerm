import { FolderOpen } from 'lucide-react';
import Tiptop from '../Tiptop.tsx';
import { cn } from '../../utils/cn.ts';
import { type TransferQueueItem } from '../../utils/fileWorkbench.ts';
import {
  PROGRESS_FILL_COLOR,
  buildCompressedPhaseChunks,
  fmtSize,
  formatCompressedPhaseBytes,
  getChunkLabel,
  getCompressedPhaseDetail,
  getStatusMeta,
  getTransferErrorSummary,
  getUploadPhaseLabel,
  renderActionButton,
  type LooseT,
} from './uploadQueueMeta.tsx';
import AutoFollowChunkGrid from './AutoFollowChunkGrid.tsx';

interface UploadQueueCardProps {
  item: TransferQueueItem;
  isAbortable?: (item: TransferQueueItem) => boolean;
  onAbortItem?: (item: TransferQueueItem) => void;
  onRemoveItems?: (ids: string[]) => void;
  t: LooseT;
}

// 单条传输任务卡片（普通分块上传与压缩传输两种形态）
export default function UploadQueueCard({ item, isAbortable, onAbortItem, onRemoveItems, t }: UploadQueueCardProps) {
  const handleOpenCompletedDownload = async (item: TransferQueueItem) => {
    const localPath = String(item?.localPath || '').trim();
    if (!localPath) {
      return;
    }
    try {
      await window?.go?.wailsapp?.App?.OpenLocalPathInExplorer?.(localPath, item.mode !== 'download-file');
    } catch (err) {
      window.lumeDialog?.alert?.(`${t('打开所在目录失败')}: ${err}`);
    }
  };

  const openTransferErrorDetails = async (item: TransferQueueItem, explicitMessage = '') => {
    const message = String(explicitMessage || item?.error || '').trim();
    if (!message) {
      return;
    }
    const title = item?.direction === 'download' ? t('下载失败详情') : t('上传失败详情');
    if (window?.lumeDialog?.alert) {
      await window.lumeDialog.alert(message, title, { copyable: true });
      return;
    }
    window.alert(message);
  };

  const direction = item.direction || 'upload';
  const meta = getStatusMeta(item.status, direction, t);
  const progress = item.status === 'completed'
    ? 100
    : Math.max(0, Math.min(100, typeof item.progress === 'number' && Number.isFinite(item.progress) ? item.progress : 0));
  const Icon = meta.Icon;
  const chunks = Array.isArray(item.chunks) ? item.chunks : [];
  const chunksDone = item.chunksCompleted || chunks.filter((chunk) => chunk.status === 'completed').length;
  const chunksFailed = item.chunksFailed || chunks.filter((chunk) => chunk.status === 'failed').length;
  const chunksActive = chunks.filter((chunk) => chunk.status === 'reading' || chunk.status === 'uploading' || chunk.status === 'retrying').length;
  const isCompressed = item.mode === 'compressed' || item.mode === 'download-compressed';
  const phaseLabel = getUploadPhaseLabel(item.phase, direction, t);
  const phaseProgress = Math.max(0, Math.min(100, typeof item.phaseProgress === 'number' && Number.isFinite(item.phaseProgress) ? item.phaseProgress : 0));
  const phaseDetail = getCompressedPhaseDetail(item, t);
  const compressedPhaseChunks = isCompressed ? buildCompressedPhaseChunks(item) : null;
  const statusLabel = isCompressed ? phaseLabel : meta.label;
  const abortable = isAbortable?.(item);
  const displayPath = direction === 'download' ? (item.localPath || item.remotePath) : item.remotePath;
  const showOpenCompletedDownload = direction === 'download' && item.status === 'completed' && item.localPath;

  return (
    <div key={item.id} className="rounded-[var(--radius-md)] border border-line bg-canvas p-3 flex flex-col gap-2">
      <div className="flex items-center gap-2.5 min-w-0">
        <div className="w-7 h-7 rounded-[var(--radius-sm)] inline-flex items-center justify-center shrink-0" style={{ background: meta.bg, color: meta.color }}>
          <Icon size={14} />
        </div>
        <div className="flex flex-col min-w-0 flex-1">
          <div className="text-primary text-base font-semibold truncate">{item.name}</div>
          <div className="text-tertiary text-xs font-mono truncate">{displayPath}</div>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          {showOpenCompletedDownload ? (
            <Tiptop text={t('打开所在目录')} placement="bottom">
              <button
                type="button"
                aria-label={t('打开所在目录')}
                onClick={() => handleOpenCompletedDownload(item)}
                className="w-[30px] h-6 inline-flex items-center justify-center rounded-[var(--radius-sm)] border border-[color-mix(in_srgb,var(--success)_44%,var(--border))] bg-success-dim text-success cursor-pointer"
              >
                <FolderOpen size={14} />
              </button>
            </Tiptop>
          ) : (
            <div className="px-2 py-0.5 rounded-[var(--radius-sm)] text-xs font-semibold" style={{ background: meta.bg, color: meta.color }}>
              {statusLabel}
            </div>
          )}
          {abortable
            ? renderActionButton(t('强制终止'), true, () => onAbortItem?.(item))
            : renderActionButton(t('从列表中移除'), false, () => onRemoveItems?.([item.id]))}
        </div>
      </div>

      {isCompressed ? (
        <div className="rounded-[var(--radius-sm)] border border-line-subtle bg-sunken p-2 flex flex-col gap-[7px]">
            <div className="grid grid-cols-[auto_1fr_auto] items-center gap-2.5 text-xs text-tertiary">
              <span>{t('当前阶段')}: <span className={cn(item.status === 'failed' && 'text-danger', item.status === 'completed' && 'text-success', item.status !== 'failed' && item.status !== 'completed' && 'text-accent')}>{phaseLabel}</span></span>
              <span className="text-center font-mono truncate">
                {formatCompressedPhaseBytes(item, t)}
              </span>
              <span>{t('当前阶段进度')}: {phaseProgress.toFixed(0)}%</span>
            </div>
            <div className="h-1 bg-hover rounded-full overflow-hidden">
              <div
                className={cn('h-full rounded-full transition-[width] duration-300', PROGRESS_FILL_COLOR[item.status === 'failed' || item.status === 'completed' ? item.status : ''] ?? 'bg-accent')}
                style={{ width: `${phaseProgress}%` }}
              />
            </div>
            {item.phaseCurrent ? (
              <div className="text-xs text-tertiary leading-[1.45] truncate">
                {t('当前文件')}: {item.phaseCurrent}
              </div>
            ) : null}
            {phaseDetail ? (
              item.status === 'failed' ? (
                <button
                  type="button"
                  onClick={() => { void openTransferErrorDetails(item, phaseDetail); }}
                  title={phaseDetail}
                  className="border-none bg-transparent p-0 text-left text-xs text-danger leading-[1.45] cursor-pointer underline decoration-dotted truncate"
                >
                  {getTransferErrorSummary(phaseDetail, t)}
                </button>
              ) : (
                <div className="text-xs text-tertiary leading-[1.45] truncate">
                  {phaseDetail}
                </div>
              )
            ) : null}
            {compressedPhaseChunks && compressedPhaseChunks.chunks.length > 0 ? (
              <div className="rounded-lg border border-line-subtle bg-[color-mix(in_srgb,var(--surface-sunken)_72%,transparent)] p-2 flex flex-col gap-[7px]">
                <div className="flex justify-between gap-2.5 text-xs text-tertiary">
                  <span>{t('分块进度')}: {compressedPhaseChunks.chunksDone}/{compressedPhaseChunks.chunks.length}</span>
                  <span>{compressedPhaseChunks.chunksFailed > 0 ? `${compressedPhaseChunks.chunksFailed} ${t('失败')}` : `${fmtSize(compressedPhaseChunks.chunkSizeBytes || 0)} / ${t('块')}`}</span>
                </div>
                <AutoFollowChunkGrid
                  chunks={compressedPhaseChunks.chunks}
                  titleBuilder={(chunk) => `${t('分块')} ${chunk.index + 1}: ${getChunkLabel(chunk, t)}${chunk.error ? ` · ${chunk.error}` : ''}`}
                />
              </div>
            ) : null}
          </div>
      ) : (
        <>
          <div className="h-1 bg-hover rounded-full overflow-hidden">
            <div
              className={cn('h-full rounded-full transition-[width] duration-300', PROGRESS_FILL_COLOR[item.status === 'failed' || item.status === 'completed' ? item.status : ''] ?? 'bg-accent')}
              style={{ width: `${progress}%` }}
            />
          </div>
          <div className="grid grid-cols-[1fr_auto_1fr] items-center gap-3 text-xs text-tertiary font-mono">
            <span>{fmtSize(item.bytesUploaded || 0)} / {fmtSize(item.bytesTotal || 0)}</span>
            <span className={chunksActive > 0 ? 'text-accent' : undefined}>{t('块并发')}: {chunksActive}</span>
            <span className="text-right">{progress.toFixed(0)}%</span>
          </div>
          {chunks.length > 0 ? (
            <div className="rounded-lg border border-line-subtle bg-sunken p-2 flex flex-col gap-[7px]">
              <div className="flex justify-between gap-2.5 text-xs text-tertiary">
                <span>{t('分块进度')}: {chunksDone}/{chunks.length}</span>
                <span>{chunksFailed > 0 ? `${chunksFailed} ${t('失败')}` : `${fmtSize(item.chunkSizeBytes || 0)} / ${t('块')}`}</span>
              </div>
              <AutoFollowChunkGrid
                chunks={chunks}
                titleBuilder={(chunk) => `${t('分块')} ${chunk.index + 1}: ${getChunkLabel(chunk, t)}${chunk.attempt ? ` · ${t('重试')} ${chunk.attempt}/5` : ''}${chunk.error ? ` · ${chunk.error}` : ''}`}
              />
            </div>
          ) : null}
        </>
      )}

      {item.error && (!isCompressed || String(item.error).trim() !== String(phaseDetail || '').trim()) ? (
        <button
          type="button"
          onClick={() => { void openTransferErrorDetails(item); }}
          title={item.error}
          className="border-none bg-transparent p-0 text-left text-xs text-danger leading-normal cursor-pointer underline decoration-dotted break-all"
        >
          {getTransferErrorSummary(item.error, t)}
        </button>
      ) : null}
    </div>
  );
}
