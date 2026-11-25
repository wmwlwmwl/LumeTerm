// OSC 133 语义提示符解析（FinalTerm / iTerm2 起的语义提示符协议）。
//
// shell 在「提示符出现 / 开始输入 / 开始执行 / 执行结束」四个节点主动上报，
// 终端因此不必再靠屏幕文本猜「用户执行了什么命令」：
//
//   OSC 133 ; A                             提示符出现（可带 click_events=1 等扩展参数）
//   OSC 133 ; B                             命令行输入开始（光标停在这里等输入）
//   OSC 133 ; C ; cmdline_url=<percent编码>  开始执行；fish 在这里给出整行命令
//   OSC 133 ; D ; <退出码>                   执行结束
//
// fish 4.x 内置发送（见 issue #423）。`cmdline_url` 是 fish 的实现（已用真实抓包
// 核对）；目前只支持这一个键，其它 shell 若用别的键名会解析不到命令行，
// 自动退回「屏幕文本剥离」的兜底路径，不会出错。
//
// 拿到 C 上的命令行后，命令历史与会话提示符长什么样彻底解耦：fish 默认的
// `user@host ~>`、starship 的 `❯`、`user@host:~$` 都一视同仁。
//
// 本模块只做纯字符串解析，不依赖终端实例，便于在 DEV 断言里直接验证。

export type ShellIntegrationKind = 'A' | 'B' | 'C' | 'D'

export interface ShellIntegrationMark {
  kind: ShellIntegrationKind
  /** 仅 C 上可能非空：shell 上报的完整命令行（已 percent 解码并清理） */
  commandLine: string | null
}

/**
 * 单条 OSC 133 payload 的解析上限。注意 xterm 自己会先在内部累积到
 * PAYLOAD_LIMIT(10MB) 才回调，这里的上限只是限制本模块的解析开销，
 * 起不到「防内存打爆」的作用；64KB 足够覆盖常见的长命令行。
 */
const OSC133_PAYLOAD_MAX = 64 * 1024

/**
 * 通用 ANSI 序列：CSI / OSC / DCS·SOS·PM·APC 字符串 / 单字符 ESC。
 * 只用于清洗「shell 上报的命令行」，不参与终端渲染。
 */
const ANSI_SEQUENCE_REGEX = /\x1b\[[0-9;:?<>=]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[PX^_][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[^[PX^_]/g

/**
 * 解析 xterm 传给 OSC 133 handler 的 payload。
 * 注意 xterm 的 OscParser 已经吃掉了 `OSC 133 ;` 里的标识，这里拿到的是
 * `A;click_events=1` / `C;cmdline_url=ls` / `D;0` 这种「标记 + 参数」串。
 * 返回 null 表示不是可识别的标记，调用方应保持 xterm 默认行为。
 */
export function parseShellIntegrationMark(data: string): ShellIntegrationMark | null {
  if (typeof data !== 'string' || !data || data.length > OSC133_PAYLOAD_MAX) return null
  const tokens = data.split(';')
  const kind = tokens[0].trim().toUpperCase()
  if (kind !== 'A' && kind !== 'B' && kind !== 'C' && kind !== 'D') return null

  let commandLine: string | null = null
  for (let i = 1; i < tokens.length; i += 1) {
    const token = tokens[i]
    const eq = token.indexOf('=')
    if (eq <= 0) continue
    // 只认 fish 已核实的 cmdline_url（percent 编码，命令里的 ; 不会被切错）。
    // 其它键名一律忽略，让调用方退回屏幕文本兜底，避免猜错键名把命令截断。
    if (commandLine === null && token.slice(0, eq).trim().toLowerCase() === 'cmdline_url') {
      const command = normalizeShellIntegrationCommand(decodeShellIntegrationCommand(token.slice(eq + 1)))
      // 空串表示「上报了但不可用」（多行命令等），保持 null 让调用方走兜底
      if (command) commandLine = command
    }
  }
  return { kind, commandLine }
}

/** percent 解码。远端输出不可信，非法转义时必须原样返回而不是抛异常。 */
export function decodeShellIntegrationCommand(raw: string): string {
  if (!raw) return ''
  try {
    return decodeURIComponent(raw)
  } catch {
    return raw
  }
}

/**
 * 规范化 shell 上报的命令行：剥离 ANSI 序列、剔除控制字符、裁首尾空白。
 *
 * **多行命令（续行 / heredoc）直接返回空串**，表示「这条上报不可用」，调用方会
 * 退回屏幕文本兜底（与改动前只记最后一行的行为一致）。原因：历史列表是单行展示，
 * 而 Run Again 会把命令原样写回 PTY——保留 `\n` 会被 shell 当成多次回车把一条命令
 * 拆成几段执行；压平成空格又会破坏 heredoc 语义（`cat <<EOF echo hi EOF` 会让 shell
 * 停在收行态，把用户后续输入全吞掉）。两种回放都不可接受，所以干脆不采信。
 */
export function normalizeShellIntegrationCommand(value: string | null): string {
  if (!value) return ''
  // 先整段吃掉 ANSI（CSI / OSC / DCS-SOS-PM-APC / 单字符 ESC），
  // 只删 ESC 本身会把 `[0m` 这类序列正文留在命令里
  const stripped = value.replace(ANSI_SEQUENCE_REGEX, '').trim()
  // 裁掉首尾空白后再判多行：行尾的 \r\n 只是换行，不算多行命令
  if (/[\r\n]/.test(stripped)) return ''
  return stripped
    // 除制表符外的 C0 / DEL / C1 全部剔除：远端可能夹带转义残留
    .replace(/[\x00-\x08\x0B-\x1F\x7F-\x9F]/g, '')
    // TAB 回放时会触发远端补全，不该出现在命令文本里
    .replace(/\t/g, ' ')
    .trim()
}

if (import.meta.env.DEV) {
  // 标记识别
  console.assert(parseShellIntegrationMark('A')?.kind === 'A', 'OSC133 A 应识别');
  console.assert(parseShellIntegrationMark('A;click_events=1')?.kind === 'A', 'OSC133 A 带扩展参数应识别');
  console.assert(parseShellIntegrationMark('B')?.kind === 'B', 'OSC133 B 应识别');
  console.assert(parseShellIntegrationMark('D;0')?.kind === 'D', 'OSC133 D 应识别');
  console.assert(parseShellIntegrationMark('D;127')?.kind === 'D', 'OSC133 D 带退出码也应识别');
  console.assert(parseShellIntegrationMark('') === null, '空 payload 不应识别');
  console.assert(parseShellIntegrationMark('X') === null, '未知标记不应识别');
  console.assert(parseShellIntegrationMark('133') === null, '裸数字不应识别');

  // 命令行解码：fish 真实抓包得到的值
  console.assert(
    parseShellIntegrationMark('C;cmdline_url=ls')?.commandLine === 'ls',
    'OSC133 C 应解出简单命令',
  );
  console.assert(
    parseShellIntegrationMark('C;cmdline_url=fish%20--no-config')?.commandLine === 'fish --no-config',
    'OSC133 C 应解出带空格的命令',
  );
  console.assert(
    parseShellIntegrationMark('C;cmdline_url=hank%40hankreed%20~%3E%20ls')?.commandLine === 'hank@hankreed ~> ls',
    'OSC133 C 应解出含 @ 与 > 的命令文本',
  );
  console.assert(
    parseShellIntegrationMark('C;click_events=1;cmdline_url=echo%20hi')?.commandLine === 'echo hi',
    'OSC133 C 应忽略无关参数',
  );
  console.assert(
    parseShellIntegrationMark('C;cmdline_url=echo%20a%3B%20rm%20-rf%20%2Ftmp%2Fx')?.commandLine === 'echo a; rm -rf /tmp/x',
    '命令里的分号不应被截断',
  );
  console.assert(
    parseShellIntegrationMark('C;cmdline=ls')?.commandLine === null,
    '未核实的 cmdline 键应忽略，交给屏幕文本兜底',
  );
  console.assert(parseShellIntegrationMark('C')?.commandLine === null, '只有 C 时不应编造命令');
  console.assert(
    decodeShellIntegrationCommand('100%') === '100%',
    '非法 percent 转义应原样返回',
  );
  console.assert(
    normalizeShellIntegrationCommand('ls\u001b[0m\r\n') === 'ls',
    '命令行应剔除转义残留并裁空白',
  );
  console.assert(
    normalizeShellIntegrationCommand('echo\ta') === 'echo a',
    '命令里的 TAB 应换成空格（回放时 TAB 会触发补全）',
  );
  // 多行命令不采信：交给屏幕文本兜底，避免 Run Again 时被拆成多次执行 / 卡在 heredoc
  console.assert(
    normalizeShellIntegrationCommand('echo a\necho b') === '',
    '多行命令应判定为不可用',
  );
  console.assert(
    normalizeShellIntegrationCommand('cat <<EOF\necho hi\nEOF') === '',
    'heredoc 应判定为不可用',
  );
}
