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
//   * 空输入（空回车）、续行 Enter、Ctrl+C 取消都不会产生 C。这类「没有 C 的挂起项」
//     必须能被下一条命令的 C 安全吸收，且吞掉位置时不能把屏幕文本再落一遍，
//     否则会出现重复条目。所以：空 fallback 直接不入队；一旦确认该 shell 会上报
//     命令行，超时只丢位置、不再用屏幕文本补记（见 dispatchFallback）。
//   * shell 中途被替换（如 fish 里 exec bash）后不再有 A/B，靠兜底超时把
//     promptSeen 复位，随后立即退回即时落库，不会一直延迟。
//
// 已知局限：OSC 133 没有给命令编号，配对只能按先进先出。若某条 C 迟到超过
// flushTimeoutMs，它会被下一条命令的位置吃掉（该条的落库内容仍是它自己的
// cmdline_url，不会串内容）；被吃掉的那条随后超时静默丢弃。实际只在极端慢的
// 链路上发生。

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
  /** 用户回车、拿到屏幕文本推断的命令时调用；空命令会被忽略 */
  submitCommand(fallbackCommand: string): SubmitOutcome;
  /** 用户取消当前输入（Ctrl+C / Ctrl+D）时调用：丢弃挂起项 */
  reset(): void;
  /** 会话结束：丢弃挂起项并清空所有状态 */
  dispose(): void;
}

interface PendingCommand {
  /** 屏幕文本推断的命令（权威命令行缺失时用它兜底） */
  text: string;
  timer: unknown;
  /**
   * 已超时但仍在队列里占位：等它那条迟到的 C 到来时把位置吃掉。
   * OSC 133 没有命令编号，位置是唯一可用的配对依据，过早移出队列会让
   * 迟到的 C 配到下一条命令上，从而串位置。
   */
  expired: boolean;
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
  /** 是否见过带命令行的 C 标记（说明该 shell 会上报命令，屏幕文本只是冗余兜底） */
  let sawCommandLine = false;
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
  /**
   * 用屏幕文本兜底落库。
   *
   * 只有「该 shell 从不给命令行」时屏幕文本才是唯一来源。一旦见过带命令行的 C，
   * 超时只说明这条输入没有执行（空回车 / 续行 / Ctrl+C 取消），此时再拿屏幕文本
   * 落库就会产生重复条目或把提示符写进历史，所以直接丢弃。
   */
  const dispatchFallback = (entry: PendingCommand) => {
    if (entry.text && !sawCommandLine) options.onCommand(entry.text);
  };
  /** 队列超限时挤掉队首（只在 C 长期不回来时发生） */
  const flushHead = () => {
    const head = queue.shift();
    if (!head) return false;
    clearEntry(head);
    dispatchFallback(head);
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
        const hadCommandLineSupport = sawCommandLine;
        if (mark.commandLine) sawCommandLine = true;
        const head = queue.shift();
        // 队列为空 = 这条命令不是用户敲回车触发的，各家入口已各自记账。
        if (!head) return true;
        clearEntry(head);
        const command = mark.commandLine || head.text;
        // 过期项（超时后仍在占位）分两种：
        //   - 超时时壳不上报命令行 → 已按屏幕文本落过库，这里不能再记一遍；
        //   - 超时时壳会上报命令行 → 当时没落库，说明这条命令其实执行了，用 C 补上。
        const alreadyRecorded = head.expired && !hadCommandLineSupport;
        if (command && !alreadyRecorded) options.onCommand(command);
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
      // 空输入（空回车）不会执行命令，也就不会有 C。绝不能入队：
      // 否则它会吃掉下一条命令的 C，队尾永远残留一项，进而重复落库。
      if (!fallbackCommand) return 'immediate';
      const entry: PendingCommand = { text: fallbackCommand, timer: null, expired: false };
      entry.timer = setTimeoutFn(() => {
        // 超时说明 C 还没来（空输入 / 续行未执行 / Ctrl+C / 链路很慢）。
        // 先复位活性标记，让后续命令立刻即时落库，不再每条都等 2s；
        // 落库策略见 dispatchFallback，位置保留一个宽限期等迟到的 C。
        entry.expired = true;
        promptSeen = false;
        dispatchFallback(entry);
        entry.timer = setTimeoutFn(() => removeEntry(entry), flushTimeoutMs);
      }, flushTimeoutMs);
      queue.push(entry);
      while (queue.length > MAX_PENDING) flushHead();
      return 'deferred';
    },

    reset(): void {
      // Ctrl+C / Ctrl+D 取消当前输入：挂起项对应的命令不会执行了，全部丢弃。
      dropAll();
    },

    dispose(): void {
      dropAll();
      available = false;
      promptSeen = false;
      sawCommandLine = false;
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
    // 只触发最早一个未触发的定时器（用于构造「C 迟到」的时序）
    const fireNext = () => {
      const index = timers.findIndex((t) => t.live);
      if (index < 0) return;
      const [timer] = timers.splice(index, 1);
      timer.live = false;
      timer.run();
    };
    return { history, out, fireTimeout, fireNext };
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
  // C 丢失 → 超时兜底（shell 不上报命令行时，屏幕文本是唯一来源）
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
  // 空回车不入队：否则会吃掉下一条命令的 C，队尾永远残留一项
  {
    const { history, out } = createHarness();
    history.handleMark('A');
    console.assert(history.submitCommand('') === 'immediate', '空回车不应入队');
    console.assert(history.submitCommand('ls') === 'deferred', '下一条命令仍应正常挂起');
    history.handleMark('C;cmdline_url=ls');
    console.assert(out.join('|') === 'ls', '空回车不应影响下一条命令落库');
  }
  // 确认 shell 会上报命令行后，超时只丢位置、不再用屏幕文本补记（避免重复条目）
  {
    const { history, out, fireTimeout } = createHarness();
    history.handleMark('A');
    history.submitCommand('ls');
    history.handleMark('C;cmdline_url=ls');
    console.assert(out.join('|') === 'ls', '第一条命令应正常落库');
    // 续行 / Ctrl+C 这类「没有 C 的输入」超时后必须静默丢弃
    history.submitCommand('echo "half');
    fireTimeout();
    console.assert(out.join('|') === 'ls', '已确认上报命令行后，超时不应再落一遍屏幕文本');
  }
  // 迟到的 C：位置被保留，迟到者吃掉自己的位置，不会串到下一条命令上
  {
    const { history, out, fireNext } = createHarness();
    history.handleMark('A');
    history.submitCommand('ls');
    history.handleMark('C;cmdline_url=ls'); // 先让壳「确认会上报命令行」
    history.handleMark('A'); // 新提示符出现，才能继续输入
    history.submitCommand('pwd');
    history.submitCommand('whoami');
    fireNext(); // pwd 超时 → 过期占位，但位置保留
    console.assert(out.join('|') === 'ls', '超时后不应提前落库');
    history.handleMark('C;cmdline_url=pwd'); // pwd 的 C 迟到
    history.handleMark('C;cmdline_url=whoami');
    console.assert(out.join('|') === 'ls|pwd|whoami', '迟到的 C 应被自己的位置吸收，不串位');
  }
  {
    // Ctrl+C 取消：挂起项立即丢弃，不会在后续命令上错配
    const { history, out, fireTimeout } = createHarness();
    history.handleMark('A');
    history.submitCommand('echo "half');
    history.reset();
    fireTimeout();
    console.assert(out.length === 0, 'Ctrl+C 取消后不应落库');
    history.submitCommand('ls');
    history.handleMark('C;cmdline_url=ls');
    console.assert(out.join('|') === 'ls', '取消后下一条命令应正常落库');
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
