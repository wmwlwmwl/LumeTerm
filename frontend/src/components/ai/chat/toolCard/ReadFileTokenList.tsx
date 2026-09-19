import { Check, Copy } from 'lucide-react';
import { useEffect, useState } from 'react';
import Tiptop from '../../../Tiptop.tsx';
import type { I18nKey } from '../../../../i18n.ts';
import { cn } from '../../../../utils/cn.ts';

export interface ReadFileTokenEstimate {
  path: string;
  displayPath: string;
  tokenCount: number;
  tokenDisplay: string;
}

export function normalizeReadFileTokenEstimates(value: unknown): ReadFileTokenEstimate[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    if (!item || typeof item !== 'object') {
      return [];
    }
    const rawItem = item as Record<string, unknown>;
    const path = typeof rawItem.path === 'string' ? rawItem.path.trim() : '';
    if (!path) {
      return [];
    }
    const displayPath = typeof rawItem.displayPath === 'string' && rawItem.displayPath.trim()
      ? rawItem.displayPath.trim()
      : path;
    const parsedTokenCount = Number(rawItem.tokenCount);
    const tokenCount = Number.isFinite(parsedTokenCount) ? Math.max(0, Math.trunc(parsedTokenCount)) : 0;
    const tokenDisplay = typeof rawItem.tokenDisplay === 'string' && rawItem.tokenDisplay.trim()
      ? rawItem.tokenDisplay.trim()
      : `${(tokenCount / 1000000).toFixed(6)}M`;
    return [{ path, displayPath, tokenCount, tokenDisplay }];
  });
}

export interface ReadFileTokenListProps {
  items: ReadFileTokenEstimate[];
  t: (key: I18nKey, vars?: Record<string, unknown>) => string;
}

export function ReadFileTokenList({ items, t }: ReadFileTokenListProps) {
  const [copiedPathIndex, setCopiedPathIndex] = useState<number | null>(null);

  useEffect(() => {
    if (copiedPathIndex === null) {
      return undefined;
    }
    const timeoutId = window.setTimeout(() => {
      setCopiedPathIndex(null);
    }, 1200);
    return () => {
      window.clearTimeout(timeoutId);
    };
  }, [copiedPathIndex]);

  if (items.length === 0) {
    return null;
  }

  return (
    <div className="mt-1.5 grid gap-0.5">
      {items.map((item, index) => {
        const copied = copiedPathIndex === index;
        return (
          <div
            key={`${item.path}-${index}`}
            className="flex min-w-0 items-center justify-between gap-2.5 rounded-md border border-[rgba(var(--accent-rgb),0.75)] bg-canvas px-2.5 py-[7px] font-mono text-sm leading-[1.35] text-secondary">
            <div className="flex min-w-0 flex-1 items-center gap-2">
              <Tiptop text={item.displayPath} className="flex" style={{ minWidth: 0, flex: 1 }}>
                <div className="min-w-0 flex-1 overflow-hidden">
                  <div className="flex w-max min-w-full animate-[ai-chat-read-file-path-marquee_4s_linear_infinite] items-center [will-change:transform]">
                    <span className="shrink-0 grow-0 basis-auto whitespace-nowrap pr-8">{item.displayPath}</span>
                    <span aria-hidden="true" className="shrink-0 grow-0 basis-auto whitespace-nowrap pr-8">{item.displayPath}</span>
                  </div>
                </div>
              </Tiptop>
              <Tiptop text={copied ? t('已复制' as I18nKey) : t('复制绝对路径' as I18nKey)} className="inline-flex shrink-0">
                <button
                  type="button"
                  onClick={(event) => {
                    event.stopPropagation();
                    void navigator.clipboard.writeText(item.path).then(() => {
                      setCopiedPathIndex(index);
                    }).catch(() => {});
                  }}
                  className={cn(
                    'inline-flex h-[22px] w-[22px] shrink-0 cursor-pointer items-center justify-center rounded-md',
                    copied
                      ? 'border border-[color-mix(in_srgb,var(--success)_30%,var(--border))] bg-[color-mix(in_srgb,var(--success)_8%,var(--surface-base))] text-success'
                      : 'border border-[color-mix(in_srgb,var(--accent)_24%,var(--border))] bg-[color-mix(in_srgb,var(--accent)_6%,var(--surface-base))] text-secondary',
                  )}>
                  {copied ? <Check size={11} color="currentColor" strokeWidth={2.5} /> : <Copy size={11} color="currentColor" strokeWidth={2.5} />}
                </button>
              </Tiptop>
            </div>
            <span className="shrink-0 tabular-nums text-secondary">{item.tokenDisplay}</span>
          </div>
        );
      })}
    </div>
  );
}
