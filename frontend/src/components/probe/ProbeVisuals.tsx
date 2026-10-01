import React, { useMemo, type CSSProperties, type ReactNode } from 'react';
import { GripVertical } from 'lucide-react';
import { cn } from '../../utils/cn.ts';
import { formatCapacity, formatPartitionCapacity } from '../../utils/probeFormatting.ts';
import { clampPct, pctColor, type DragHandleProps, type SparklineSeries } from './probeTypes.ts';

export const ProgressBar = React.memo(function ProgressBar({ value, color }: { value: number; color?: string }) {
  const pct = clampPct(value);
  return (
    <div className="probe-progress-track">
      <div className="probe-progress-fill" style={{ width: `${pct}%`, background: color || pctColor(pct) }} />
    </div>
  );
});

export const Card = React.memo(function Card({ children, className = '', style }: { children: ReactNode; className?: string; style?: CSSProperties }) {
  return <div className={cn('probe-card', className)} style={style}>{children}</div>;
});

export const SectionHeader = React.memo(function SectionHeader({
  icon,
  title,
  badge,
  action,
  dragHandleProps = null,
}: {
  icon: ReactNode;
  title: ReactNode;
  badge?: ReactNode;
  action?: ReactNode;
  dragHandleProps?: DragHandleProps | null;
}) {
  return (
    <div className="probe-section-header">
      <div
        className={`probe-section-handle${dragHandleProps ? ' probe-section-handle-sortable' : ''}${dragHandleProps?.dragReady ? ' is-ready' : ''}${dragHandleProps?.dragging ? ' is-dragging' : ''}`}
        draggable={dragHandleProps?.draggable || false}
        onPointerDown={dragHandleProps?.onPointerDown}
        onPointerUp={dragHandleProps?.onPointerUp}
        onPointerCancel={dragHandleProps?.onPointerCancel}
        onDragStart={dragHandleProps?.onDragStart}
        onDragEnd={dragHandleProps?.onDragEnd}
      >
        <span className="probe-section-icon">{icon}</span>
        <span className="probe-section-title">{title}</span>
        <span className="probe-section-spacer" />
        <span className="probe-section-handle-hint" aria-hidden="true"><GripVertical size={13} /></span>
      </div>
      {badge ? <span className="probe-section-badge">{badge}</span> : null}
      {action}
    </div>
  );
});

export const MetricCard = React.memo(function MetricCard({
  label,
  value,
  sub,
  color,
  icon,
  progress = null,
}: {
  label: string;
  value: ReactNode;
  sub?: string;
  color?: string;
  icon?: ReactNode;
  progress?: number | null;
}) {
  const hasProgress = progress !== null;
  return (
    <div className="probe-metric-card">
      <div className="probe-metric-main">
        <div className="probe-metric-top">
          <span className="probe-metric-icon" style={{ color }}>{icon}</span>
          <span className="probe-metric-label" title={label}>{label}</span>
        </div>
        {sub ? <div className="probe-metric-sub" title={sub}>{sub}</div> : null}
      </div>
      <div className="probe-metric-value" style={{ color }}>{value}</div>
      {hasProgress ? <div className="probe-metric-bar"><ProgressBar value={progress} color={color} /></div> : null}
    </div>
  );
});

export const CpuBar = React.memo(function CpuBar({ val = 0 }: { val?: number }) {
  const pct = clampPct(val);
  return <ProgressBar value={pct} color={pctColor(pct, 50, 80)} />;
});

export const CoreHeatGrid = React.memo(function CoreHeatGrid({ cores }: { cores: number[] }) {
  return (
    <div className="probe-core-grid">
      {cores.map((val, i) => {
        const pct = clampPct(val);
        return (
          <div
            key={i}
            className="probe-core-cell"
            title={`CPU ${i}: ${pct.toFixed(1)}%`}
            style={{ background: pctColor(pct, 50, 80), opacity: 0.32 + (pct / 100) * 0.68 }}
          />
        );
      })}
    </div>
  );
});

export const PartRow = React.memo(function PartRow({
  mount,
  size,
  avail,
  usedPct,
}: {
  mount?: string;
  size?: string;
  avail?: string;
  usedPct?: number;
}) {
  const pct = clampPct(usedPct || 0);
  const color = pctColor(pct, 60, 85);
  return (
    <div className="probe-partition-row">
      <span className="probe-partition-mount" title={mount}>{mount}</span>
      <div className="probe-partition-bar">
        <div className="h-full rounded-[2px]" style={{ width: `${pct}%`, background: color }} />
      </div>
      <span className="probe-partition-value" title={String(size)}>{formatPartitionCapacity(size)}</span>
      <span className="probe-partition-value" title={String(avail)}>{formatPartitionCapacity(avail)}</span>
      <span className="probe-partition-percent" style={{ color }}>{Math.round(pct)}%</span>
    </div>
  );
});

export const ProcessHotRow = React.memo(function ProcessHotRow({
  process,
}: {
  process: { cpu?: number; mem?: number; cmd?: string };
}) {
  const pct = clampPct(process.cpu || 0);
  return (
    <div className="probe-process-row">
      <div className="probe-process-cpu-cell">
        <span className="probe-process-cpu">{(process.cpu || 0).toFixed(1)}%</span>
        <CpuBar val={pct} />
      </div>
      <span className="probe-process-mem">{formatCapacity(process.mem || 0, 1)}</span>
      <span className="probe-process-cmd" title={process.cmd}>{process.cmd}</span>
    </div>
  );
});

export const Sparkline = React.memo(function Sparkline({
  data,
  series,
  height = 42,
}: {
  data?: number[];
  series?: SparklineSeries[];
  height?: number;
}) {
  const lines = useMemo(() => {
    if (Array.isArray(series) && series.length > 0) return series;
    return [{ data: data || [], color: 'var(--success)', fill: true }];
  }, [data, series]);
  const max = useMemo(() => Math.max(...lines.flatMap((item) => item.data || []), 1), [lines]);
  const paths = useMemo(() => lines.map((item) => {
    const pts = item.data || [];
    if (pts.length < 2) return { ...item, points: '', fillPts: '' };
    const W = 200;
    const p = pts.map((v, i) => `${(i / (pts.length - 1)) * W},${height - (clampPct((v / max) * 100) / 100) * (height - 3) - 1}`).join(' ');
    return { ...item, points: p, fillPts: `0,${height} ${p} 200,${height}` };
  }), [height, lines, max]);
  if (!paths.some((item) => item.points)) return <div className="probe-sparkline-empty" style={{ height }} />;
  return (
    <svg className="probe-sparkline" viewBox={`0 0 200 ${height}`} preserveAspectRatio="none" style={{ height }}>
      {paths.map((item, index) => item.points && item.fill ? (
        <polygon key={`fill-${index}`} points={item.fillPts} style={{ fill: item.color }} opacity={0.10} />
      ) : null)}
      {paths.map((item, index) => item.points ? (
        <polyline key={`line-${index}`} points={item.points} fill="none" style={{ stroke: item.color }} strokeWidth={1.7} strokeLinejoin="round" strokeLinecap="round" />
      ) : null)}
    </svg>
  );
});

export const MemDonut = React.memo(function MemDonut({
  used,
  free,
  total,
}: {
  used: number;
  free: number;
  total: number;
}) {
  const r = 27;
  const cx = 35;
  const cy = 35;
  const circ = 2 * Math.PI * r;
  const f1 = total > 0 ? Math.min(Math.max(used / total, 0), 1) : 0;
  const reclaimable = Math.max(total - used - free, 0);
  const f2 = total > 0 ? Math.min(Math.max(reclaimable / total, 0), 1 - f1) : 0;
  const f3 = Math.max(1 - f1 - f2, 0);
  const seg = (frac: number, color: string, start: number) => (frac > 0.005 ? (
    <circle
      cx={cx}
      cy={cy}
      r={r}
      fill="none"
      stroke={color}
      strokeWidth={8}
      strokeDasharray={`${frac * circ} ${circ}`}
      strokeLinecap="butt"
      transform={`rotate(${-90 + start * 360} ${cx} ${cy})`}
    />
  ) : null);
  return (
    <svg width={70} height={70} className="probe-mem-donut" aria-hidden="true">
      <circle cx={cx} cy={cy} r={r} fill="none" stroke="var(--border)" strokeWidth={8} />
      {seg(f1, 'var(--danger)', 0)}
      {seg(f2, 'var(--warning)', f1)}
      {seg(f3, 'var(--success)', f1 + f2)}
    </svg>
  );
});

export interface MemoryBreakdownItem {
  id: string;
  label: string;
  value: number;
  color: string;
  cpu?: number;
  description?: string;
}

export const MemoryBreakdownDonut = React.memo(function MemoryBreakdownDonut({
  items,
  total,
  onItemHover,
}: {
  items: MemoryBreakdownItem[];
  total: number;
  onItemHover?: (item: MemoryBreakdownItem | null) => void;
}) {
  const r = 33;
  const cx = 44;
  const cy = 44;
  const circ = 2 * Math.PI * r;
  const usableTotal = Math.max(total, 1);
  const strokeWidth = 7;
  const gap = 4;
  const visibleItems = items.filter((item) => item.value / usableTotal > 0.005);
  const drawableLength = Math.max(circ - visibleItems.length * gap, 0);
  let cursor = 0;
  return (
    <svg width={88} height={88} viewBox="0 0 88 88" className="probe-mem-donut probe-mem-donut-detail" role="img" aria-label="Memory breakdown">
      <circle cx={cx} cy={cy} r={r} fill="none" stroke="var(--surface-raised)" strokeWidth={strokeWidth} />
      <circle cx={cx} cy={cy} r={r - 8} fill="var(--surface-sunken)" />
      {visibleItems.map((item) => {
        const fraction = Math.min(Math.max(item.value / usableTotal, 0), 1);
        // `round` 端点各占半个线宽；从 dash 中扣除它们，使可见弧长仍严格按原始占比。
        const visibleLength = fraction * drawableLength;
        const dashLength = Math.max(visibleLength - strokeWidth, 0);
        const start = (cursor + strokeWidth / 2) / circ;
        cursor += visibleLength + gap;
        return (
          <circle
            key={item.id}
            cx={cx}
            cy={cy}
            r={r}
            fill="none"
            stroke={item.color}
            strokeWidth={strokeWidth}
            strokeDasharray={`${dashLength} ${circ}`}
            strokeLinecap="round"
            transform={`rotate(${-90 + start * 360} ${cx} ${cy})`}
            className="probe-memory-detail-segment"
            tabIndex={0}
            aria-label={`${item.label}: ${formatCapacity(item.value, 1)}`}
            onMouseEnter={() => onItemHover?.(item)}
            onMouseLeave={() => onItemHover?.(null)}
            onFocus={() => onItemHover?.(item)}
            onBlur={() => onItemHover?.(null)}
          />
        );
      })}
    </svg>
  );
});
