import { useEffect, useRef } from 'react';
import type * as React from 'react';
import type { IMarker, Terminal as XTerm } from '@xterm/xterm';
import type { I18nKey } from '../../i18n.ts';
import type { CommandBlockState } from './terminalTypes.ts';

type LooseT = (key: I18nKey, vars?: Record<string, unknown>) => string;

// 时间轴 / 命令块 gutter：左侧时间戳列 + 可折叠命令块树线，扫描 buffer 提示符分块；
// 收起时改写 buffer 只留一行摘要。从 Terminal.tsx 原样搬移，闭包变量同名传入。
export function useTerminalGutter(deps: {
  termRef: React.RefObject<XTerm | null>;
  containerRef: React.RefObject<HTMLDivElement | null>;
  timestampsEnabledRef: React.RefObject<boolean>;
  commandBlocksEnabledRef: React.RefObject<boolean>;
  alternateBufferActiveRef: React.RefObject<boolean>;
  commandBlocksVisible: boolean;
  t: LooseT;
  tsRingRef: React.RefObject<{ entries: Array<{ marker: IMarker; val: string } | null>; next: number }>;
  tsEnsureLine: (term: XTerm, line: number, timestampsByLine: Map<number, string>) => string;
  tsClearLine: (line: number) => void;
  tsSnapshotByLine: (term: XTerm, lineCount: number) => string[];
  tsRemountFromList: (term: XTerm, tsList: string[]) => void;
}) {
  const {
    termRef, containerRef, timestampsEnabledRef, commandBlocksEnabledRef, alternateBufferActiveRef,
    commandBlocksVisible, t, tsRingRef, tsEnsureLine, tsClearLine, tsSnapshotByLine, tsRemountFromList,
  } = deps;

  // 命令块：扫描 buffer 里「提示符行 → 下一提示符行」；收起时改写 buffer 只留一行摘要
  // 同名命令用「第几次出现」区分，避免收起第二个把第一个状态冲掉
  const CB_POOL = 400;
  // null! 惰性初始化惯用法：下方 if 守卫保证首次渲染即完成填充，后续恒非空
  const cbBlocksRef = useRef<Map<string, CommandBlockState>>(null!);
  if (!cbBlocksRef.current) {
    // key = `${commandLineText}#${occurrence}`；value = { id, commandLineText, occurrence, collapsed, savedOutput, savedOutputTs }
    cbBlocksRef.current = new Map();
  }
  const cbIdSeqRef = useRef(1);
  const cbRewriteLockRef = useRef(false);
  const isCollapseSummaryLine = (text: string) => /^⋯\s+\d+\s+lines\s*$/.test(String(text || '').trim());
  // 行首是 shell 提示符（后面可跟命令）。不能要求「整行以 # 结尾」，否则 `root@host:~# ping` 识别不到
  const isShellPromptLine = (text: string) => {
    const t = String(text || '').replace(/\s+$/g, '');
    if (!t || t.length < 2 || isCollapseSummaryLine(t)) return false;
    // user@host:path# cmd  /  user@host:path$ cmd
    if (/^[\w.-]+@[\w.-]+:[^\n]*?[#$](?:\s+|$)/.test(t)) return true;
    // [user@host dir]$ cmd
    if (/^\[[^\]]+\][#$%](?:\s+|$)/.test(t)) return true;
    // root@host ~]# cmd  一类
    if (/^[\w.-]+@[\w.-]+\s+[^\n]*[#$%](?:\s+|$)/.test(t)) return true;
    // Windows 本机终端：PowerShell "PS C:\path> cmd" / CMD "C:\path> cmd"。
    // 不识别时多行粘贴/命令输入栏的逐行门控只能干等 2.5s 超时（每行一次）。
    if (/^(?:PS\s+)?[A-Za-z]:[\\/][^\n]*?>(?:\s|$)/.test(t)) return true;
    // 极简：以 #/$ 单独起命令（少见）
    if (/^[#$]\s+\S/.test(t)) return true;
    return false;
  };
  // 空提示符可以显示时间；真正执行（回车）时再更新该行时间戳
  const normalizeCmdLineKey = (text: string) => String(text || '').replace(/\s+$/g, '');
  const blockStateKey = (commandLineText: string, occurrence: number) => `${normalizeCmdLineKey(commandLineText)}#${occurrence}`;
  const readTerminalBufferLines = (term: XTerm | null) => {
    const buf = term?.buffer?.active;
    if (!buf) return [];
    const lines = [];
    const total = buf.length;
    for (let i = 0; i < total; i += 1) {
      const bl = buf.getLine(i);
      lines.push(bl ? bl.translateToString(true) : '');
    }
    while (lines.length > 1 && lines[lines.length - 1] === '') {
      lines.pop();
    }
    return lines;
  };
  // 扫描所有提示符行下标：块 i = prompt[i] .. prompt[i+1]-1
  const scanPromptIndexes = (lines: string[]) => {
    const idxs: number[] = [];
    for (let i = 0; i < lines.length; i += 1) {
      if (isShellPromptLine(lines[i])) idxs.push(i);
    }
    return idxs;
  };
  // 在 buffer 里找「第 occurrence 次」出现的命令行（0-based）
  const findCommandLineOccurrence = (lines: string[], commandLineText: string, occurrence: number) => {
    const key = normalizeCmdLineKey(commandLineText);
    let seen = 0;
    for (let i = 0; i < lines.length; i += 1) {
      if (normalizeCmdLineKey(lines[i]) !== key) continue;
      if (seen === occurrence) return i;
      seen += 1;
    }
    return -1;
  };
  const getOrCreateBlockState = (commandLineText: string, occurrence: number) => {
    const textKey = normalizeCmdLineKey(commandLineText);
    if (!textKey) return null;
    const occ = Math.max(0, Number(occurrence) || 0);
    const key = blockStateKey(textKey, occ);
    const map = cbBlocksRef.current;
    let block = map.get(key);
    if (!block) {
      if (map.size >= CB_POOL) {
        const firstKey = map.keys().next().value;
        if (firstKey != null) map.delete(firstKey);
      }
      block = {
        id: cbIdSeqRef.current++,
        commandLineText: textKey,
        occurrence: occ,
        collapsed: false,
        savedOutput: null,
        savedOutputTs: null,
      };
      map.set(key, block);
    }
    return block;
  };
  const rewriteTerminalBufferLines = (term: XTerm | null, lines: string[], nextTimestamps: string[] | null = null, options: { anchorLine?: number } = {}) => {
    if (!term) return;
    const anchorLine = typeof options.anchorLine === 'number' ? options.anchorLine : -1;
    const bufBefore = term.buffer?.active;
    const viewportBefore = bufBefore ? bufBefore.viewportY : 0;
    const baseBefore = bufBefore ? bufBefore.baseY : 0;
    // 尽量保持视口相对锚点行的偏移，减少重写后整屏「跳一下」
    const anchorOffset = anchorLine >= 0 ? (anchorLine - viewportBefore) : 0;

    cbRewriteLockRef.current = true;
    // 一次 write 完成：清 scrollback + 清屏 + 回顶 + 写回。
    // 比 term.reset() 轻（不重置模式/字符集），并用 write 回调在同一渲染路径里恢复滚动与 gutter。
    const normalized = (Array.isArray(lines) ? lines : []).map((line) => String(line ?? ''));
    const body = normalized.length === 0
      ? ''
      : (normalized.length === 1
        ? normalized[0]
        : `${normalized.slice(0, -1).join('\r\n')}\r\n${normalized[normalized.length - 1]}`);
    // \x1b[3J 清 scrollback，\x1b[2J 清屏，\x1b[H 光标回原点
    const payload = `\x1b[3J\x1b[2J\x1b[H${body}`;

    const finish = () => {
      try {
        if (Array.isArray(nextTimestamps)) {
          tsRemountFromList(term, nextTimestamps);
        }
        const buf = term.buffer.active;
        const maxVp = Math.max(0, buf.baseY);
        if (anchorLine >= 0) {
          const nextVp = Math.max(0, Math.min(maxVp, anchorLine - anchorOffset));
          term.scrollToLine(nextVp);
        } else if (baseBefore > 0) {
          const ratio = Math.min(1, viewportBefore / baseBefore);
          term.scrollToLine(Math.floor(maxVp * ratio));
        } else {
          term.scrollToBottom();
        }
      } catch (_) {}
      cbRewriteLockRef.current = false;
      // 直接 sync，少一帧 rAF 延迟，减轻「收起后 gutter 晚半拍」
      try { syncGutter(); } catch (_) { scheduleGutterSync(); }
    };

    try {
      // 只影响本地 xterm，不发给 SSH
      term.write(payload, finish);
    } catch (_) {
      finish();
    }
  };
  const cbToggleBlock = (blockId: number) => {
    const term = termRef.current;
    if (!term || term.buffer.active.type !== 'normal' || cbRewriteLockRef.current) return;
    let block = null;
    for (const b of cbBlocksRef.current.values()) {
      if (b.id === blockId) { block = b; break; }
    }
    if (!block) return;

    const lines = readTerminalBufferLines(term);
    const oldTs = tsSnapshotByLine(term, lines.length);
    // 用「命令文本 + 第几次出现」定位，避免两次 ping 绑到同一行
    const start = findCommandLineOccurrence(lines, block.commandLineText, block.occurrence);
    if (start < 0) return;

    if (!block.collapsed) {
      // 收起：start+1 到「下一提示符前」→ 换成一行摘要
      const prompts = scanPromptIndexes(lines);
      const pIdx = prompts.indexOf(start);
      let end = start;
      if (pIdx >= 0 && pIdx + 1 < prompts.length) {
        end = prompts[pIdx + 1] - 1;
      } else if (isCollapseSummaryLine(lines[start + 1])) {
        end = start + 1;
      } else {
        for (let i = start + 1; i < lines.length; i += 1) {
          if (String(lines[i] || '').trim()) end = i;
        }
      }
      if (end <= start) return;
      // output / outputTs 同步构建，避免 filter 后 index 错位
      const output = [];
      const outputTs = [];
      for (let i = start + 1; i <= end; i += 1) {
        if (isCollapseSummaryLine(lines[i])) continue;
        output.push(lines[i]);
        outputTs.push(oldTs[i] || '');
      }
      if (output.length === 0) return;
      block.savedOutput = output;
      block.savedOutputTs = outputTs;
      block.collapsed = true;
      const nextLines = [
        ...lines.slice(0, start + 1),
        `⋯ ${output.length} lines`,
        ...lines.slice(end + 1),
      ];
      // 严格 index 手术：前缀 + 摘要 + 后缀（后缀行号整体前移，戳也整段切开）
      const summaryTs = outputTs.find(Boolean) || oldTs[start] || '';
      const nextTs = [
        ...oldTs.slice(0, start + 1),
        summaryTs,
        ...oldTs.slice(end + 1),
      ];
      // 以命令行作锚点，重写后视口尽量不跳
      rewriteTerminalBufferLines(term, nextLines, nextTs, { anchorLine: start });
      return;
    }

    // 展开
    if (!Array.isArray(block.savedOutput) || block.savedOutput.length === 0) {
      block.collapsed = false;
      scheduleGutterSync();
      return;
    }
    let summaryIdx = start + 1;
    if (!isCollapseSummaryLine(lines[summaryIdx])) {
      const nearby = lines.findIndex((l, idx) => idx > start && idx <= start + 3 && isCollapseSummaryLine(l));
      if (nearby < 0) {
        block.collapsed = false;
        block.savedOutput = null;
        block.savedOutputTs = null;
        scheduleGutterSync();
        return;
      }
      summaryIdx = nearby;
    }
    const restoredTs = Array.isArray(block.savedOutputTs) && block.savedOutputTs.length === (block.savedOutput || []).length
      ? block.savedOutputTs
      : (block.savedOutput || []).map(() => oldTs[start] || '');
    const nextLines = [
      ...lines.slice(0, start + 1),
      ...block.savedOutput,
      ...lines.slice(summaryIdx + 1),
    ];
    const nextTs = [
      ...oldTs.slice(0, start + 1),
      ...restoredTs,
      ...oldTs.slice(summaryIdx + 1),
    ];
    block.collapsed = false;
    rewriteTerminalBufferLines(term, nextLines, nextTs, { anchorLine: start });
  };
  // 关闭功能前：把所有已收起的块展开回 buffer，否则 savedOutput 清掉后无法再展开
  const cbExpandAllCollapsed = (term: XTerm | null) => {
    if (!term || term.buffer.active.type !== 'normal' || cbRewriteLockRef.current) return false;
    const collapsed = [...cbBlocksRef.current.values()].filter(
      (b) => b && b.collapsed && Array.isArray(b.savedOutput) && b.savedOutput.length > 0,
    );
    if (collapsed.length === 0) return false;

    let lines = readTerminalBufferLines(term);
    let oldTs = tsSnapshotByLine(term, lines.length);
    // 从后往前展开，避免前面插入行导致后面 occurrence 定位错位
    collapsed.sort((a, b) => b.occurrence - a.occurrence || b.commandLineText.localeCompare(a.commandLineText));

    for (const block of collapsed) {
      const start = findCommandLineOccurrence(lines, block.commandLineText, block.occurrence);
      if (start < 0) {
        block.collapsed = false;
        block.savedOutput = null;
        block.savedOutputTs = null;
        continue;
      }
      let summaryIdx = start + 1;
      if (!isCollapseSummaryLine(lines[summaryIdx])) {
        const nearby = lines.findIndex((l, idx) => idx > start && idx <= start + 3 && isCollapseSummaryLine(l));
        if (nearby < 0) {
          block.collapsed = false;
          block.savedOutput = null;
          block.savedOutputTs = null;
          continue;
        }
        summaryIdx = nearby;
      }
      const restoredTs = Array.isArray(block.savedOutputTs) && block.savedOutputTs.length === (block.savedOutput || []).length
        ? block.savedOutputTs
        : (block.savedOutput || []).map(() => oldTs[start] || '');
      lines = [
        ...lines.slice(0, start + 1),
        ...(block.savedOutput || []),
        ...lines.slice(summaryIdx + 1),
      ];
      oldTs = [
        ...oldTs.slice(0, start + 1),
        ...restoredTs,
        ...oldTs.slice(summaryIdx + 1),
      ];
      block.collapsed = false;
      block.savedOutput = null;
      block.savedOutputTs = null;
    }
    rewriteTerminalBufferLines(term, lines, oldTs);
    return true;
  };
  const cbClear = () => {
    cbBlocksRef.current = new Map();
  };
  const gutterRef = useRef<HTMLDivElement | null>(null);
  const gutterSyncRAFRef = useRef<number | null>(null);

  // ── 时间轴 / 命令块：同步 gutter 到 xterm 视口 ─────────────────
  function scheduleGutterSync() {
    const gutterNeeded = timestampsEnabledRef.current || commandBlocksEnabledRef.current;
    if (gutterSyncRAFRef.current !== null || !gutterNeeded || alternateBufferActiveRef.current) return;
    gutterSyncRAFRef.current = requestAnimationFrame(() => {
      gutterSyncRAFRef.current = null;
      syncGutter();
    });
  }

  function collectLiveCommandBlocks(term: XTerm) {
    // 完全按 buffer 扫描提示符：prompt[i] .. prompt[i+1]-1
    // 同名命令按出现次序分块，避免两次 ping 共用状态
    const lines = readTerminalBufferLines(term);
    const prompts = scanPromptIndexes(lines);
    const list = [];
    const occurrenceByText = new Map();
    for (let i = 0; i < prompts.length; i += 1) {
      const start = prompts[i];
      const commandLineText = normalizeCmdLineKey(lines[start]);
      const occurrence = occurrenceByText.get(commandLineText) || 0;
      occurrenceByText.set(commandLineText, occurrence + 1);
      const block = getOrCreateBlockState(commandLineText, occurrence);
      if (!block) continue;
      let end = start;
      if (block.collapsed && isCollapseSummaryLine(lines[start + 1])) {
        end = start + 1;
      } else if (i + 1 < prompts.length) {
        end = Math.max(start, prompts[i + 1] - 1);
      } else if (isCollapseSummaryLine(lines[start + 1])) {
        end = start + 1;
      } else {
        end = start;
        for (let j = start + 1; j < lines.length; j += 1) {
          if (isShellPromptLine(lines[j])) break;
          if (String(lines[j] || '').trim()) end = j;
        }
      }
      list.push({ block, start, end, collapsed: Boolean(block.collapsed) });
    }
    return list;
  }

  function syncGutter() {
    const gutter = gutterRef.current;
    const term = termRef.current;
    const showTs = timestampsEnabledRef.current;
    const showCb = commandBlocksEnabledRef.current;
    if (!gutter || !term || (!showTs && !showCb) || term.buffer.active.type !== 'normal') {
      return;
    }
    const buf = term.buffer.active;
    const rows = term.rows;
    if (!rows || !containerRef.current) return;

    const timestampsByLine = new Map();
    if (showTs) {
      const ring = tsRingRef.current;
      // 从旧到新扫：后写覆盖先写，保证「执行时刻」压过「提示符出现时刻」
      for (let offset = 0; offset < ring.entries.length; offset += 1) {
        const index = (ring.next + offset) % ring.entries.length;
        const entry = ring.entries[index];
      const line = entry?.marker?.line;
      if (!entry || entry.marker.isDisposed || typeof line !== 'number' || line < 0) {
        ring.entries[index] = null;
      } else {
        timestampsByLine.set(line, entry.val);
      }
      }
    }

    const liveBlocks = showCb ? collectLiveCommandBlocks(term) : [];
    // 行 → 所在块；块起点优先
    const blockByLine = new Map();
    liveBlocks.forEach((item) => {
      for (let line = item.start; line <= item.end; line += 1) {
        const existing = blockByLine.get(line);
        if (existing && existing.start === line && existing.start !== item.start) continue;
        blockByLine.set(line, item);
      }
    });

    const firstVisible = buf.viewportY; // buffer 中第一个可见行 (ydisp)

    // 通过 xterm screen/rows 的实际渲染尺寸计算行高，确保像素级对齐
    const screen = containerRef.current.querySelector('.xterm-screen');
    const rowsEl = containerRef.current.querySelector('.xterm-rows');
    let lineH = 0;
    if (screen && rowsEl) {
      const screenRect = screen.getBoundingClientRect();
      const rowsRect = rowsEl.getBoundingClientRect();
      lineH = Math.max(rowsRect.height / rows, 1);
      const paddingTop = `${Math.max(rowsRect.top - screenRect.top, 0)}px`;
      if (gutter.style.paddingTop !== paddingTop) gutter.style.paddingTop = paddingTop;
    } else {
      // xterm options 类型为可选，运行期恒有默认值（13 / 1.22）
      lineH = (term.options.fontSize ?? 13) * (term.options.lineHeight ?? 1.2);
    }

    // 时间戳用状态色；命令块树线/折叠钮用 accent，深色终端上更醒目
    const tsColor = 'var(--term-status-color)';
    const blockColor = 'var(--accent)';
    let html = '';
    for (let i = 0; i < rows; i++) {
      const tsIdx = firstVisible + i;
      const bufLine = buf.getLine(tsIdx);
      const lineText = bufLine ? bufLine.translateToString(true) : '';
      const isEmptyLine = !bufLine || lineText === '';
      const isWrapped = bufLine && bufLine.isWrapped;
      let ts = '';
      // 空提示符也可显示已有戳；当前光标行没有戳时补一个（出现提示符的时间）
      // 真正执行命令时会在回车路径更新为执行时刻
      if (showTs && !isEmptyLine && !isWrapped && tsIdx >= 0 && !isCollapseSummaryLine(lineText)) {
        ts = timestampsByLine.get(tsIdx)
          || (tsIdx === buf.baseY + buf.cursorY ? tsEnsureLine(term, tsIdx, timestampsByLine) : '');
      }

      const owning = showCb ? blockByLine.get(tsIdx) : null;
      const parts = [];
      // 固定列宽：时间戳列右对齐贴齐「]」，命令块列固定 14px，中间无 gap，避免看起来多一格空
      if (showTs) {
        // [HH:MM:SS] 共 10 字符；等宽 11px 约 66px，固定 70 够用
        parts.push(`<span style="display:inline-block;width:70px;min-width:70px;max-width:70px;text-align:right;flex-shrink:0;letter-spacing:0;box-sizing:border-box;color:${tsColor}">${ts || ''}</span>`);
      }
      if (showCb) {
        // 纯 CSS 树线：连续竖条 + 末行 L 折角（不用 │/└ 字符，避免粗细/对齐怪）
        // 整数 2px + border 画 L，避免 1.5px 横线抗锯齿后显得更细
        const barBg = `color-mix(in srgb, ${blockColor} 92%, transparent)`;
        const barW = 2;
        const armW = 7;
        const cell = (inner: string) =>
          `<span style="position:relative;display:inline-block;width:14px;min-width:14px;height:${lineH}px;flex-shrink:0;box-sizing:border-box;vertical-align:top">${inner || ''}</span>`;
        // 竖条水平居中于 14px 列
        const vBar = (top: string, bottom: string) =>
          `<span style="position:absolute;left:50%;top:${top};bottom:${bottom};width:${barW}px;margin-left:-${barW / 2}px;background:${barBg};pointer-events:none"></span>`;
        // 末行 L：同宽 border-left + border-bottom；横臂贴文字垂直中心略偏下（约 0.55em 基线感）
        // 用 lineH 比例 + 字号无关的下限，大字号时不显得折角悬在行上半
        const cornerH = Math.max(barW + 4, Math.round(lineH * 0.72));
        const lCorner = () =>
          `<span style="position:absolute;left:50%;top:0;width:${armW}px;height:${cornerH}px;margin-left:-${barW / 2}px;border-left:${barW}px solid ${barBg};border-bottom:${barW}px solid ${barBg};box-sizing:border-box;pointer-events:none"></span>`;
        let blockCell = cell('');
        if (owning && tsIdx === owning.start) {
          // 可折叠：展开有输出，或已收起可再展开
          const canFold = owning.collapsed || owning.end > owning.start;
          const icon = owning.collapsed ? '+' : '−';
          if (canFold) {
            const padTop = Math.max(0, (lineH - 14) / 2);
            const btn = `<button type="button" data-cb-id="${owning.block.id}" title="${owning.collapsed ? t('展开') : t('收起')}" style="position:absolute;left:0;top:${padTop}px;display:inline-flex;align-items:center;justify-content:center;width:14px;min-width:14px;height:14px;margin:0;padding:0;border:1px solid color-mix(in srgb, ${blockColor} 78%, transparent);border-radius:2px;background:color-mix(in srgb, ${blockColor} 16%, transparent);color:${blockColor};font-size:11px;line-height:1;cursor:pointer;font-family:var(--font-mono);box-sizing:border-box;font-weight:700;z-index:1">${icon}</button>`;
            // 多行展开：按钮下接到行底，与下一行整高竖条无缝衔接
            const barBelow = (!owning.collapsed && owning.end > owning.start)
              ? vBar(`${padTop + 14}px`, '0')
              : '';
            blockCell = cell(`${btn}${barBelow}`);
          } else {
            const padTop = Math.max(0, (lineH - 14) / 2);
            blockCell = cell(`<span style="position:absolute;left:0;top:${padTop}px;display:inline-flex;width:14px;height:14px;align-items:center;justify-content:center;border:1px solid color-mix(in srgb, ${blockColor} 55%, transparent);border-radius:2px;opacity:0.75;box-sizing:border-box"></span>`);
          }
        } else if (owning && !owning.collapsed && tsIdx > owning.start && tsIdx < owning.end) {
          blockCell = cell(vBar('0', '0'));
        } else if (owning && !owning.collapsed && tsIdx === owning.end && owning.end > owning.start) {
          blockCell = cell(lCorner());
        }
        parts.push(blockCell);
      }
      // overflow:visible：命令块竖条才能顶满行高无裁切；时间戳等宽不溢出
      html += `<div style="height:${lineH}px;line-height:${lineH}px;font-size:11px;font-family:var(--font-mono);font-variant-numeric:tabular-nums;white-space:nowrap;overflow:visible;padding:0 2px 0 4px;box-sizing:border-box;display:flex;align-items:center;justify-content:flex-end;gap:2px">${parts.join('')}</div>`;
    }
    gutter.innerHTML = html;
  }

  // gutter 点击折叠/展开（随显示状态重绑，避免首屏 display:none 时漏挂）
  useEffect(() => {
    const gutter = gutterRef.current;
    if (!gutter || !commandBlocksVisible) return undefined;
    const onClick = (event: MouseEvent) => {
      const btn = (event.target as Element | null)?.closest?.('button[data-cb-id]');
      if (!btn) return;
      event.preventDefault();
      event.stopPropagation();
      const id = Number(btn.getAttribute('data-cb-id'));
      if (Number.isFinite(id)) cbToggleBlock(id);
    };
    gutter.addEventListener('click', onClick);
    return () => gutter.removeEventListener('click', onClick);
  }, [commandBlocksVisible]);

  // ── 终端清屏处理：清空视口对应的时间戳 ─────────────────────
  function handleClearScreen() {
    const term = termRef.current;
    if (!term || term.buffer.active.type !== 'normal') return;
    const buf = term.buffer.active;
    const rows = term.rows || 24;
    const firstVisible = buf.viewportY;
    for (let i = 0; i < rows; i++) {
      tsClearLine(firstVisible + i);
    }
    scheduleGutterSync();
  }

  return { gutterRef, gutterSyncRAFRef, cbRewriteLockRef, isShellPromptLine, isCollapseSummaryLine, scheduleGutterSync, syncGutter, handleClearScreen, cbExpandAllCollapsed, cbClear };
}
