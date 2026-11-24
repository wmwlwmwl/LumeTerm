// OSC 133 驱动的「命令历史落库」状态机。
//
// 背景（issue #423）：命令历史原先只有一个来源——回车时读 xterm 当前行、剥掉提示符。
// 这是启发式：提示符形态没进白名单时整行（含提示符）都会被当成命令存下来，
// Run Again 再把提示符当命令发给 shell，fish 下就报 `Unknown command: user@host`。
//
// 支持 OSC 133 的 shell（fish 4.x 等）会主动上报：
//   A 提示符出现 → B 命令行开始 → C 开始执行（可带 cmdline_url） → D 执行结束
// 于是一段命令的生命周期是明确的，可以在「回车」时先挂起屏幕文本版本，
// 等 C 携带的权威命令行到达后以其落库，屏幕文本仅作为兜底。
//
// 关键约束：
//   * 未上报 OSC 133 的 shell 必须完全不受影响（保持回车即落库）。
//   * 连按回车 / type-ahead 时会有多条命令同时「已回车、等 C」，所以待落库项是
//     **FIFO 队列**而不是单槽，否则 C 与命令会错配（丢一条、重复一条）。
//   * 不是用户敲回车触发的命令（Run Again / 快捷命令 / 多行粘贴批量执行）已经
//     各自记过账，C 到达时若队列为空必须忽略，不能重复记录。
//   * C 丢失一律由 per-entry 超时兜底：A/D 到达时不能 flush 队首——type-ahead 下
//     上一段的 A/D 会先到、下一段的 C 还没来，flush 会张冠李戴。
//   * shell 中途被替换（如 fish 里 exec bash）后不再有 A/B，靠兜底超时把
//     promptSeen 复位，随后立即退回即时落库，不会一直延迟。

import { parseShellIntegrationMark } from './shellIntegration.ts';

/** 默认兜底延迟：已收到过 A/B 说明集成在工作，C 正常几毫秒内就到，留足余量即可 */
const DEFAULT_FLUSH_TIMEOUT_MS = 2000;

/** 待落库队列上限：C 长时间不回来时防止无限堆积，超出后按屏幕文本挤掉最早的 */
const MAX_PENDING = 8;

export type SubmitOutcome =
  /** 已挂起，等 C 上的权威命令行到达后再落库 */
  | 'deferred'
  /** 当前会话没有可用的 OSC 133，调用方应立即用屏幕文本落库 */
  | 'immediate';

export interface ShellIntegrationHistoryOptions {
  /** 命令落库出口 */
  onCommand: (command: string) => void;
  flushTimeoutMs?: number;
  /** 注入定时器，便于离线用例断言 */
  setTimeoutFn?: (callback: () => void, ms: number) => unknown;
  clearTimeoutFn?: (handle: unknown) => void;
}

export interface ShellIntegrationHistory {
  /** 收到 OSC 133 payload 时调用；返回 true 表示该标记已被识别并消费 */
  handleMark(data: string): boolean;
  /** 用户回车、拿到屏幕文本推断的命令时调用 */
  submitCommand(fallbackCommand: string): SubmitOutcome;
  /** 取消所有挂起项并释放定时器 */
  dispose(): void;
}

interface PendingCommand {
  /** 屏幕文本推断的命令（权威命令行缺失时用它兜底） */
  text: string;
  timer: unknown;
}

export function createShellIntegrationHistory(
  options: ShellIntegrationHistoryOptions,
): ShellIntegrationHistory {
  const setTimeoutFn = options.setTimeoutFn
    ?? ((callback: () => void, ms: number) => setTimeout(callback, ms) as unknown);
  const clearTimeoutFn = options.clearTimeoutFn
    ?? ((handle: unknown) => clearTimeout(handle as ReturnType<typeof setTimeout>));
  const flushTimeoutMs = options.flushTimeoutMs ?? DEFAULT_FLUSH_TIMEOUT_MS;

  /** 该会话是否发过 OSC 133 标记 */
  let available = false;
  /** 自上一条命令开始执行后，是否又收到过新的提示符标记（A/B） */
  let promptSeen = false;
  /** 已回车、等 C 落库的命令，FIFO（与 C 的到达顺序一致） */
  const queue: PendingCommand[] = [];

  const clearEntry = (entry: PendingCommand) => {
    if (entry.timer !== null) {
      clearTimeoutFn(entry.timer);
      entry.timer = null;
    }
  };
  const removeEntry = (entry: PendingCommand) => {
    clearEntry(entry);
    const index = queue.indexOf(entry);
    if (index >= 0) queue.splice(index, 1);
  };
  /** 队列超限时挤掉队首（只在 C 长期不回来时发生） */
  const flushHead = () => {
    const head = queue.shift();
    if (!head) return false;
    clearEntry(head);
    if (head.text) options.onCommand(head.text);
    return true;
  };
  const dropAll = () => {
    while (queue.length) {
      const entry = queue.pop();
      if (entry) clearEntry(entry);
    }
  };

  return {
    handleMark(data: string): boolean {
      const mark = parseShellIntegrationMark(data);
      if (!mark) return false;
      available = true;
      if (mark.kind === 'C') {
        // 行内命令已经跑起来了，下一轮提示符会重新把 promptSeen 置真
        promptSeen = false;
        const head = queue.shift();
        // 队列为空 = 这条命令不是用户敲回车触发的，各家入口已各自记账
        if (!head) return true;
        clearEntry(head);
        const command = mark.commandLine || head.text;
        if (command) options.onCommand(command);
        return true;
      }
      if (mark.kind === 'A' || mark.kind === 'B') {
        // 提示符/输入行出现：集成在工作，记下活性标记。
        // 注意这里**不能**顺手 flush 队首：type-ahead 时上一段的 A/D 会先到、
        // 而下一段的 C 还没来，flush 会把下一条命令提前按屏幕文本落库（张冠李戴）。
        // C 丢失一律交给 per-entry 超时兜底，它只影响自己那一条，不会错配。
        promptSeen = true;
        return true;
      }
      // D：上一条命令结束。同上，只更新活性状态，不 flush。
      return true;
    },

    submitCommand(fallbackCommand: string): SubmitOutcome {
      // 没有 OSC 133，或这一轮提示符没上报过（例如刚换了 shell）→ 立刻落库
      if (!available || !promptSeen) return 'immediate';
      // 注意：即使 fallback 为空也要入队。屏幕文本剥离不出命令（提示符形态
      // 不认识、缓冲区读失败）时，C 上的权威命令行仍然可以救回来。
      const entry: PendingCommand = { text: fallbackCommand, timer: null };
      entry.timer = setTimeoutFn(() => {
        // 超时说明 C 根本没来（半吊子集成 / shell 已被替换）：落库后复位活性标记，
        // 让后续命令立刻走即时落库，不再每条都等 2s。
        removeEntry(entry);
        promptSeen = false;
        if (entry.text) options.onCommand(entry.text);
      }, flushTimeoutMs);
      queue.push(entry);
      while (queue.length > MAX_PENDING) flushHead();
      return 'deferred';
    },

    dispose(): void {
      dropAll();
      available = false;
      promptSeen = false;
    },
  };
}

if (import.meta.env.DEV) {
  // 假定时器：用例里手动触发超时，不建真定时器
  const createHarness = () => {
    const out: string[] = [];
    const timers: Array<{ run: () => void; live: boolean }> = [];
    const history = createShellIntegrationHistory({
      onCommand: (command) => out.push(command),
      flushTimeoutMs: 2000,
      setTimeoutFn: (callback) => { const timer = { run: callback, live: true }; timers.push(timer); return timer; },
      clearTimeoutFn: (handle) => { (handle as { live: boolean }).live = false; },
    });
    const fireTimeout = () => { for (const timer of timers.splice(0)) if (timer.live) { timer.live = false; timer.run(); } };
    return { history, out, fireTimeout };
  };

  // 无 OSC 133：立即落库（旧行为）
  console.assert(createHarness().history.submitCommand('ls') === 'immediate', '无 OSC133 应立刻落库');
  // 正常一帧：B → 回车挂起 → C 权威命令
  {
    const { history, out } = createHarness();
    history.handleMark('B');
    console.assert(history.submitCommand('hank@hankreed ~> ls') === 'deferred', '有 OSC133 应先挂起');
    console.assert(out.length === 0, 'C 到达前不应落库');
    history.handleMark('C;cmdline_url=ls');
    console.assert(out.length === 1 && out[0] === 'ls', 'C 上的权威命令应落库');
  }
  // 连按两次回车（type-ahead）：C 按提交顺序配对，不能丢/不能重复
  {
    const { history, out } = createHarness();
    history.handleMark('A');
    history.submitCommand('cd /tmp');
    history.submitCommand('ls');
    history.submitCommand('pwd');
    console.assert(out.length === 0, '挂起期间不应落库');
    history.handleMark('C;cmdline_url=cd%20%2Ftmp');
    history.handleMark('C;cmdline_url=ls');
    history.handleMark('C;cmdline_url=pwd');
    console.assert(out.join('|') === 'cd /tmp|ls|pwd', 'type-ahead 三条命令应按顺序各落一条');
  }
  // C 丢失 → 超时兜底（只影响这一条，不会错过其他命令）
  {
    const { history, out, fireTimeout } = createHarness();
    history.handleMark('B');
    history.submitCommand('echo hi');
    history.handleMark('D;0');
    console.assert(out.length === 0, 'D 到达但 C 缺失时不应立刻落库（可能是 type-ahead 的下一条）');
    fireTimeout();
    console.assert(out.join('|') === 'echo hi', '超时应兜底落库');
    history.handleMark('D;0');
    console.assert(out.length === 1, '兜底后不应重复落库');
  }
  // type-ahead：前一条的 D / 下一条的 A 不能把队列里等待中的命令提前 flush
  {
    const { history, out } = createHarness();
    history.handleMark('A');
    history.submitCommand('ls');
    history.submitCommand('pwd');
    history.handleMark('C;cmdline_url=ls');
    history.handleMark('D;0');
    history.handleMark('A');
    history.handleMark('B');
    console.assert(out.join('|') === 'ls', 'A/D 不应把还在等 C 的下一条提前落库');
    history.handleMark('C;cmdline_url=pwd');
    console.assert(out.join('|') === 'ls|pwd', '下一条仍应由自己的 C 落库');
  }
  // C/D 全丢 → 超时兜底，且复位活性标记
  {
    const { history, out, fireTimeout } = createHarness();
    history.handleMark('B');
    history.submitCommand('echo hi');
    fireTimeout();
    console.assert(out.join('|') === 'echo hi', '超时应兜底落库');
    console.assert(history.submitCommand('ls -al') === 'immediate', '超时后应退回即时落库');
  }
  // Run Again / 快捷命令：队列为空时的 C 必须忽略
  {
    const { history, out } = createHarness();
    history.handleMark('A');
    history.handleMark('C;cmdline_url=pwd');
    console.assert(out.length === 0, '非回车触发的 C 不应落库');
  }
  // 屏幕文本为空时，C 的权威命令仍要救回来
  {
    const { history, out } = createHarness();
    history.handleMark('B');
    history.submitCommand('');
    history.handleMark('C;cmdline_url=ls%20-al');
    console.assert(out.join('|') === 'ls -al', '屏幕文本为空时应由 C 补齐');
  }
  // 空兜底 + C 也没有命令 → 不产生空条目
  {
    const { history, out } = createHarness();
    history.handleMark('B');
    history.submitCommand('');
    history.handleMark('C');
    console.assert(out.length === 0, '空命令不应落库');
  }
  // shell 被替换（不再发 A/B）→ 立刻回到即时落库
  {
    const { history, out } = createHarness();
    history.handleMark('A');
    history.submitCommand('exec bash');
    history.handleMark('C;cmdline_url=exec%20bash');
    console.assert(history.submitCommand('ls') === 'immediate', '换 shell 后应立刻落库');
    console.assert(out.join('|') === 'exec bash', '换 shell 那条命令应正常落库');
  }
  // 非法标记不消费
  console.assert(createHarness().history.handleMark('X;foo=1') === false, '未知标记不应消费');
}
