// 终端纯辅助函数：URL 识别、命令提取、粘贴归一化、缓冲区快照等。
// 从 Terminal.tsx 抽出，无 React 状态依赖。
import type { Terminal as XTerm, IBufferLine, IBufferRange } from '@xterm/xterm';
import { isDarkTerminalSurface, type TerminalTheme } from './theme.ts';
import { parseCommandInputContext } from './terminalCommandAutocompleteParser.ts';
import * as AppGo from '../../wailsjs/go/wailsapp/App.js';

export interface LineSegment {
  y0: number;
  text: string;
  colAt: number[];
  widthAt: number[];
}

export const textDecoder = new TextDecoder();
export const textEncoder = new TextEncoder();
export const DEFAULT_TERMINAL_SHORTCUTS = Object.freeze({
  copy: 'Ctrl+C',
  paste: 'Ctrl+V',
  pasteSelection: 'Ctrl+Shift+V',
  clear: 'Ctrl+L',
  newTab: 'Ctrl+T',
  find: 'Ctrl+F',
  sigint: 'Ctrl+C',
  eof: 'Ctrl+D',
  suspend: 'Ctrl+Z',
  clearLine: 'Ctrl+U',
});

// SearchAddon 只上背景、不改字色。按终端底色选高亮，不按界面 mode。
// 深色终端必须用「够深」的底：偏亮的半透明底会触发 minimumContrastRatio 把白字压成黑字。
export function getTermSearchDecorations(terminalTheme: TerminalTheme) {
  if (!isDarkTerminalSurface(terminalTheme)) {
    return {
      matchBackground: '#fbbf24',
      matchOverviewRuler: '#fbbf24',
      activeMatchBackground: '#ea580c',
      activeMatchColorOverviewRuler: '#ea580c',
    };
  }
  return {
    matchBackground: '#1d4ed8',
    matchOverviewRuler: '#3b82f6',
    activeMatchBackground: '#be123c',
    activeMatchColorOverviewRuler: '#fb7185',
  };
}

export function formatTerminalTimestamp(date = new Date()) {
  const pad = (value: number) => String(value).padStart(2, '0');
  // 固定 [HH:MM:SS]，括号内不加空格，避免 gutter 对齐时看起来「多一格」
  return `[${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}]`;
}

export function getTerminalBufferSnapshotText(term: XTerm | null) {
  if (!term?.buffer?.active) {
    return ''
  }
  const buffer = term.buffer.active
  const totalLines = Math.max(Number(buffer.length) || 0, (Number(buffer.baseY) || 0) + (Number(term.rows) || 0))
  const lines = []
  for (let index = 0; index < totalLines; index += 1) {
    const line = buffer.getLine(index)
    if (!line) {
      continue
    }
    lines.push(line.translateToString(true))
  }
  return lines.join('\n').trim()
}

// 手写 URL 规则（不依赖 addon-web-links）；provider 负责点击，覆盖层负责常驻下划线
// 排除 ; | 等 shell 分隔符，避免 curl ...sh;else 把后续命令粘进链接
export const TERMINAL_URL_REGEX = /(https?|HTTPS?):[/]{2}[^\s"'!*(){}|\\^<>`;]*[^\s"':,.!?{}|\\^~[\]`()<>;]/;

export function isTerminalHttpUrl(urlString: string) {
  try {
    const url = new URL(urlString);
    const base = url.password && url.username
      ? `${url.protocol}//${url.username}:${url.password}@${url.host}`
      : (url.username
        ? `${url.protocol}//${url.username}@${url.host}`
        : `${url.protocol}//${url.host}`);
    return urlString.toLocaleLowerCase().startsWith(base.toLocaleLowerCase());
  } catch {
    return false;
  }
}

/**
 * 当前正在输入的逻辑行起始（0-based，含上键历史回显 / 多行 wrap）。
 * 该行及之后不识别链接：只有「已经执行过」滚到输出区的内容才可点/高亮。
 */
export function getTerminalInputStartLine(term: XTerm | null) {
  const buf = term?.buffer?.active;
  if (!buf) return Number.POSITIVE_INFINITY;
  let line = (buf.baseY || 0) + (buf.cursorY || 0);
  while (line > 0) {
    const row = buf.getLine(line);
    if (row?.isWrapped) line -= 1;
    else break;
  }
  return line;
}

/**
 * 一行 buffer → 文本 + 每个字符对应的 0-based 列。
 * 宽字符（CJK/emoji 等）占 2 列，不能用字符串下标当列号，否则高亮会整体偏左。
 * 行为对齐 translateToString(true)：跳过 width=0 的占位格，并去掉行尾空白。
 */
export function lineToTextAndCols(line: IBufferLine | undefined) {
  if (!line) return { text: '', colAt: [], widthAt: [] };
  let text = '';
  const colAt = [];
  const widthAt = [];
  const len = line.length;
  let col = 0;
  while (col < len) {
    const cell = line.getCell(col);
    if (!cell) break;
    const w = cell.getWidth();
    if (w === 0) {
      col += 1;
      continue;
    }
    const chars = cell.getChars() || ' ';
    const advance = w > 0 ? w : 1;
    for (let i = 0; i < chars.length; i += 1) {
      text += chars[i];
      colAt.push(col);
      widthAt.push(advance);
    }
    col += advance;
  }
  let end = text.length;
  while (end > 0 && text[end - 1] === ' ') end -= 1;
  return {
    text: text.slice(0, end),
    colAt: colAt.slice(0, end),
    widthAt: widthAt.slice(0, end),
  };
}

/**
 * 取含 bufferLine0 的逻辑行各段（处理 isWrapped 换行 URL）。
 * isWrapped=true 表示本行是上一行的续行。
 */
export function getLogicalLineSegments(term: XTerm, bufferLine0: number): LineSegment[] {
  const buf = term.buffer.active;
  let start = bufferLine0;
  while (start > 0) {
    const line = buf.getLine(start);
    if (!line?.isWrapped) break;
    start -= 1;
  }
  const segs = [];
  let y = start;
  for (;;) {
    const line = buf.getLine(y);
    if (!line) break;
    const mapped = lineToTextAndCols(line);
    segs.push({ y0: y, text: mapped.text, colAt: mapped.colAt, widthAt: mapped.widthAt });
    const next = buf.getLine(y + 1);
    if (!next?.isWrapped) break;
    y += 1;
  }
  return segs;
}

/**
 * joined 串 0-based 下标 → buffer 1-based 列/行。
 * edge='start'：该字符起始列（1-based）；edge='end'：该字符占用的末列（1-based 含），
 * 供下划线绘制 endCol = end.x 使用（与单宽时「末字符 1-based 列」一致，宽字符覆盖两列）。
 */
export function joinedIndexToPos(segs: LineSegment[], index: number, edge: 'start' | 'end' = 'start') {
  if (!segs.length) return { x: 1, y: 1 };
  let rem = index;
  for (const seg of segs) {
    if (rem < seg.text.length) {
      const col0 = seg.colAt[rem] ?? rem;
      const w = seg.widthAt[rem] ?? 1;
      const x = edge === 'end' ? col0 + w : col0 + 1;
      return { x: Math.max(1, x), y: seg.y0 + 1 };
    }
    rem -= seg.text.length;
  }
  const last = segs[segs.length - 1];
  if (!last.text.length) return { x: 1, y: last.y0 + 1 };
  const lastIdx = last.text.length - 1;
  const col0 = last.colAt[lastIdx] ?? lastIdx;
  const w = last.widthAt[lastIdx] ?? 1;
  return { x: Math.max(1, col0 + w), y: last.y0 + 1 };
}

/**
 * 扫描逻辑行（含 wrap）上的 http(s) 链接。
 * 换行 URL 会拼完整再匹配，range 可跨多行。输入逻辑行返回空。
 */
export function findTerminalHttpLinksOnLine(term: XTerm, bufferLineNumber: number) {
  const line0 = bufferLineNumber - 1;
  if (line0 >= getTerminalInputStartLine(term)) return [];
  const segs = getLogicalLineSegments(term, line0);
  if (!segs.length) return [];
  const joined = segs.map((s) => s.text).join('');
  if (!joined) return [];
  const rex = new RegExp(TERMINAL_URL_REGEX.source, (TERMINAL_URL_REGEX.flags || '') + 'g');
  const links: Array<{ text: string; range: IBufferRange }> = [];
  let match: RegExpExecArray | null;
  while ((match = rex.exec(joined))) {
    const value = match[0];
    if (!isTerminalHttpUrl(value)) continue;
    const start = joinedIndexToPos(segs, match.index, 'start');
    const end = joinedIndexToPos(segs, match.index + value.length - 1, 'end');
    links.push({ text: value, range: { start, end } });
  }
  return links;
}

export function isInteractivePromptText(value: unknown) {
  const text = String(value || '').trim()
  if (!text) return false
  // 1) 英文前缀：choose / select / enter / input / please enter / press enter ...
  if (/^(choose|select|enter|input|please enter|press enter|would you like|do you have|port to use)\b/i.test(text)) return true
  // 2) 中文前缀：含"请"的 + 常见安装脚本裸动词（设置/配置/指定/填写/输入/选择/确认/继续）
  //    注意这些动词作为 shell 命令几乎不存在（命令名都是英文），误伤风险极低。
  if (/^(请设置|请输入|请选择|请确认|请指定|请填写|请提供|是否|是否需要|是否继续|是否确认|按回车|回车确认|请按|设置|配置|指定|填写|输入|选择|确认|继续)/.test(text)) return true
  // 3) 提问结构探测：行内含【中英文冒号/问号】 OR 含【(默认 /（默认】 的默认值括号结构
  //    OR 含【[y/n] / [0] / [Y/N]】 这种方括号选择格式。
  //    这层判断把"用户回答导致行尾不是冒号"的情况也兜住了——只要整行里出现过提问痕迹，就算。
  const hasPromptStructure = /[:?：？]/.test(text)
    || /[（(]默认/.test(text)
    || /\[[yn][^[\]]*\/[^[\]]*[yn]\]/i.test(text)
    || /\[[0-9-][0-9,\- ]*\]/.test(text)
    || /按[^，。：:]*回车|回车[^，。：:]*继续|继续[^，。：:]*执行/.test(text)
  if (hasPromptStructure) {
    // 3.1) 英文关键词：default / leave empty / skip / y/n / yes/no / option / selection / password / username / port ...
    if (/\b(default|leave empty|skip|y\/n|yes\/no|option|selection|password|passwd|username|user|port|host|ip|path|dir|directory)\b/i.test(text)) return true
    // 3.2) 中文关键词：默认 / 留空 / 跳过 / 可选 / 选项 / 是 / 否 / 回车 / 确认 / 取消
    //        + 安装脚本高频词：用户名 / 密码 / 口令 / 账号 / SSH / 端口 / 安全入口 / 面板
    //        + 路径类：路径 / 目录 / 数据目录 / 安装路径 / 监听地址 / 主机
    if (/(默认|留空|跳过|可选|选项|是\/否|回车|确认|取消|可直接回车|按回车继续|回车确认|用户名|密码|口令|账号|端口|SSH|安全入口|面板|入口|监听|路径|目录|数据目录|安装路径|主机|地址|版本号|是否|继续执行|是否确认)/.test(text)) return true
  }
  // 4) 兜底：行尾就是【冒号/问号 (+ 可选数字)】，典型的"等待输入光标前最后一个字符"形式
  //    即便没命中关键词，这种格式也极大概率是交互提示，保留旧行为。
  if (/[:?：？]\s*(?:\d+)?\s*$/.test(text)) return true
  // 5) 兜底方括号选择格式行尾：[y/n] / [0] / [1-23] ...
  return /\[[yn0-9/-]+\]:?\s*(?:\d+)?\s*$/i.test(text)
}

// 从 xterm 可见缓冲区的"当前命令行"剥离提示符，返回真正执行的命令。
// 兼容：
//   - Linux user@host:path$ / # / %
//   - PowerShell "PS C:\path>" 与 Windows CMD "C:\path>"
//   - Starship/oh-my-posh 等自定义 prompt 的 Unicode 符号（❯ ➜ › » λ ƒ ψ）
//   - Python/Node 等 REPL 的 >>> / ...
// 用行首锚定的提示符结构匹配（非贪婪），只消费"提示符"本身、保留命令文本，
// 避免命令内部含 $/#（如 echo $HOME、含 # 注释）被误切。
export const SHELL_PROMPT_PREFIX_PATTERNS = [
  /^[\w.-]+@[\w.-]+:[^\n]*?[#$%]\s*/,    // user@host:path$ cmd
  /^\[[^\]]+\][#$%]\s*/,                 // [user@host dir]$ cmd
  /^[\w.-]+@[\w.-]+\s+[^\n]*?[#$%]\s*/,  // root@host ~]# cmd
  /^[#$%]\s+/,                            // 极简：单独的 $/#/% 起命令
  // Windows：兼容 "PS C:\path>" 与裸 "C:\path>"，盘符/路径后以 > 结尾。
  // 行首可选 PS，后跟 X:\... 路径，再 > 与空格（空格可选，空提示符也匹配）。
  /^(?:PS\s+)?[A-Za-z]:[\\/][^\n]*?>\s*/,
  // 自定义 Unicode 符号提示符：可能重复（如 ❯❯）或带颜色，后接空格再接命令。
  // 符号取自常见自定义 prompt：❯ ➜ › » λ ƒ ψ 等。
  /^[❯➜›»λƒψ▶▷]+[=>]?\s+/,
  // REPL 提示符：Python/Node 等的 >>> 与续行 ...
  /^(?:>>>|\.\.\.)\s+/,
]
export function extractCommandFromBufferLine(line: string) {
  if (!line) return ''
  let t = String(line)
  for (const re of SHELL_PROMPT_PREFIX_PATTERNS) {
    const m = t.match(re)
    if (m) {
      t = t.slice(m[0].length)
      break
    }
  }
  return t.trim()
}

export function splitTrailingIncompleteEscapeSequence(input: string) {
  if (!input) {
    return { complete: '', carry: '' }
  }

  const lastEscapeIndex = input.lastIndexOf('\x1b')
  if (lastEscapeIndex === -1) {
    return { complete: input, carry: '' }
  }

  const suffix = input.slice(lastEscapeIndex)
  if (suffix.length === 1) {
    return { complete: input.slice(0, lastEscapeIndex), carry: suffix }
  }

  if (suffix[1] === '[') {
    for (let index = 2; index < suffix.length; index += 1) {
      const code = suffix.charCodeAt(index)
      if (code >= 0x40 && code <= 0x7E) {
        return { complete: input, carry: '' }
      }
    }
    return { complete: input.slice(0, lastEscapeIndex), carry: suffix }
  }

  if (suffix[1] === ']') {
    for (let index = 2; index < suffix.length; index += 1) {
      if (suffix[index] === '\x07') {
        return { complete: input, carry: '' }
      }
      if (suffix[index] === '\x1b' && index + 1 < suffix.length && suffix[index + 1] === '\\') {
        return { complete: input, carry: '' }
      }
    }
    return { complete: input.slice(0, lastEscapeIndex), carry: suffix }
  }

  return { complete: input, carry: '' }
}

export function getTextareaAutocompletePopupPosition(textarea: HTMLTextAreaElement | null, _popupWidth = 760, _popupHeight = 260) {
  if (!textarea || typeof window === 'undefined' || typeof document === 'undefined') {
    return null
  }

  const style = window.getComputedStyle(textarea)
  const textareaRect = textarea.getBoundingClientRect()
  const selectionStart = textarea.selectionStart ?? textarea.value.length

  const mirror = document.createElement('div')
  const marker = document.createElement('span')
  const mirroredText = textarea.value.slice(0, selectionStart)

  // 镜像属性均为 CSSStyleDeclaration 的字符串字段（Extract 排除 Symbol.iterator 等 symbol 键）
  const mirroredProperties: Array<Extract<keyof CSSStyleDeclaration, string>> = [
    'boxSizing',
    'width',
    'fontFamily',
    'fontSize',
    'fontWeight',
    'fontStyle',
    'letterSpacing',
    'lineHeight',
    'textTransform',
    'textIndent',
    'paddingTop',
    'paddingRight',
    'paddingBottom',
    'paddingLeft',
    'borderTopWidth',
    'borderRightWidth',
    'borderBottomWidth',
    'borderLeftWidth',
    'whiteSpace',
    'wordBreak',
    'overflowWrap',
    'tabSize',
  ]

  mirror.style.position = 'fixed'
  mirror.style.left = '0'
  mirror.style.top = '0'
  mirror.style.visibility = 'hidden'
  mirror.style.pointerEvents = 'none'
  mirror.style.whiteSpace = 'pre-wrap'
  mirror.style.wordBreak = 'break-word'
  mirror.style.overflowWrap = 'break-word'
  mirror.style.overflow = 'hidden'

  mirroredProperties.forEach((property) => {
    (mirror.style as unknown as Record<string, string>)[property] = (style as unknown as Record<string, string>)[property];
  })

  mirror.textContent = mirroredText
  marker.textContent = '\u200b'
  mirror.appendChild(marker)
  document.body.appendChild(mirror)

  const mirrorRect = mirror.getBoundingClientRect()
  const markerRect = marker.getBoundingClientRect()
  const width = Math.min(Math.max(textareaRect.width, 420), window.innerWidth - 16)

  let left = textareaRect.left + (markerRect.left - mirrorRect.left) - textarea.scrollLeft
  const top = textareaRect.bottom + 8
  const maxHeight = Math.max(120, window.innerHeight - top - 8)

  if (left + width > window.innerWidth - 8) {
    left = window.innerWidth - width - 8
  }
  if (left < 8) {
    left = 8
  }

  document.body.removeChild(mirror)

  return {
    left,
    top,
    width,
    maxHeight,
  }
}

export function buildWrappedMultiLineCommand(command: string) {
  const source = String(command ?? '').replace(/\r\n?/g, '\n')
  let marker = '__LUMIN_WRAP_EOF__'
  while (source.includes(marker)) {
    marker += '_X'
  }
  return `bash <<'${marker}'\n${source}\n${marker}\n`
}

export const SCREEN_NON_INTERACTIVE_OPTIONS = new Set(['-ls', '-list', '-wipe', '-v', '-version', '--version', '-help', '--help']);

/** 只识别会进入 GNU screen 界面的命令；查询、后台启动和控制命令保持标准终端行为。 */
export function startsInteractiveScreen(command: string) {
  // ponytail: 仅解析提交行最后一个 shell 段和常用包装器；若要覆盖别名/函数，改由后端上报前台进程。
  const context = parseCommandInputContext(String(command ?? '').trim());
  const tokens = context.tokens.map((token) => token.text.replace(/^['"]|['"]$/g, ''));
  while (tokens.length && /^(?:sudo|env|command|exec)$/i.test(tokens[0])) tokens.shift();
  while (tokens.length && /^[A-Za-z_][A-Za-z0-9_]*=/.test(tokens[0])) tokens.shift();
  const executable = (tokens.shift() || '').replace(/\\/g, '/').split('/').pop()?.toLowerCase();
  if (executable !== 'screen') return false;
  if (tokens.includes('-X') || tokens.some((token) => /^-dm/i.test(token))) return false;
  const lowerOptions = tokens.map((token) => token.toLowerCase());
  if (lowerOptions.some((token) => SCREEN_NON_INTERACTIVE_OPTIONS.has(token))) return false;
  return !lowerOptions.includes('-d') || lowerOptions.some((token) => /^-r+$/.test(token) || token === '-x');
}

if (import.meta.env.DEV) {
  console.assert(startsInteractiveScreen('screen -S demo-session'), 'screen 启动命令识别失败');
  console.assert(startsInteractiveScreen('sudo /usr/bin/screen -r demo-session'), 'screen 恢复命令识别失败');
  console.assert(!startsInteractiveScreen('screen -ls'), 'screen 查询命令不应进入兼容模式');
  console.assert(!startsInteractiveScreen('screen -dmS demo-session command'), 'screen 后台命令不应进入兼容模式');
}

/** 粘贴到终端：统一换行并清掉尾部连续回车，避免右键粘贴时直接连发多次执行 */
export function normalizeTerminalPasteText(text: string) {
  return String(text ?? '')
    .replace(/\r\n/g, '\n')
    .replace(/\r/g, '\n')
    .replace(/\n+$/g, '')
    .replace(/\n/g, '\r')
}

/**
 * 多行粘贴“逐行 + 提示符回归门控”发送器。
 * 一次性灌入多行时，上一行 \r 执行后远端 readline 尚未就绪，紧跟的行首
 * 字符可能被吞(实测丢 1~2 字符；行首补空格可规避，侧面证实)。逐行发送并
 * 等待 ssh-command-finished(提示符回归)事件确认上一行执行完，再发下一行，
 * 与远端时序无关，字节零丢失。timeoutMs 兜底防提示符识别失效卡死；
 * send 返回 false(断连等)即中止；返回的 cancel 用于清除未完成链。
 */
export function createGatedLineSender(opts: {
  send: (chunk: string) => boolean;
  armCommandFinish: () => void;
  timeoutMs?: number;
}): (lines: string[]) => () => void {
  const timeoutMs = opts.timeoutMs ?? 2500;
  let queue: string[] = [];
  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let listener: (() => void) | null = null;

  const releaseGate = () => {
    if (timer !== null) { clearTimeout(timer); timer = null; }
    if (listener !== null) { window.removeEventListener('ssh-command-finished', listener); listener = null; }
  };
  const cancel = () => {
    stopped = true;
    queue = [];
    releaseGate();
  };
  const sendNext = () => {
    if (stopped) return;
    releaseGate();
    const line = queue.shift();
    if (line === undefined) return;
    if (!opts.send(line + '\r')) { stopped = true; return; }
    opts.armCommandFinish();
    timer = setTimeout(sendNext, timeoutMs);
    const onFinished = () => {
      releaseGate();
      sendNext();
    };
    listener = onFinished;
    window.addEventListener('ssh-command-finished', onFinished);
  };
  return (lines: string[]) => {
    cancel();
    stopped = false;
    queue = lines.filter((l) => l !== '');
    sendNext();
    return cancel;
  };
}

/**
 * 读取剪贴板文本：优先走 Wails 原生剪贴板接口，绕开 navigator.clipboard.readText()
 * 在 macOS WKWebView 下弹出的 "Paste" 提示气泡（issue #263）；非 Wails 运行时
 * （浏览器 dev）调用绑定会抛错，此时回退 Clipboard API。
 */
export async function readClipboardText(): Promise<string> {
  try {
    const text = await AppGo.ClipboardGetText();
    if (typeof text === 'string') return text;
  } catch {
    // 非 Wails 环境，回退
  }
  return navigator.clipboard.readText();
}

/** 终端控制信号字节（SIGINT/EOF/SIGTSTP/清行），按键热路径复用，避免每次 keydown 分配 */
export const TERMINAL_SIGNAL_BYTES: Record<string, Uint8Array<ArrayBuffer>> = Object.freeze({
  sigint: new Uint8Array([0x03]),     // Ctrl+C (ETX)
  eof: new Uint8Array([0x04]),        // Ctrl+D (EOT)
  suspend: new Uint8Array([0x1a]),    // Ctrl+Z (SUB)
  clearLine: new Uint8Array([0x15]),  // Ctrl+U (NAK)
});

// 命令栏按钮样式辅助函数
export const btnStyle = (color: string) => ({
  border: '1px solid var(--border)',
  background: 'var(--surface-raised)',
  color: color === 'red' ? 'var(--danger)' : 'var(--text-secondary)',
  cursor: 'pointer',
  borderRadius: 'var(--radius-xs)',
  padding: '3px 8px',
});
export const iconBtnStyle = (color: string, background?: string) => ({
  display: 'flex', alignItems: 'center', justifyContent: 'center',
  width: 24, height: 24,
  background: background || 'var(--surface-raised)',
  border: '1px solid var(--border)',
  borderRadius: 'var(--radius-xs)',
  color,
  cursor: 'pointer',
  transition: 'var(--transition-fast)',
});
