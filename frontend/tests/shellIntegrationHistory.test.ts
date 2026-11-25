// 命令历史落库状态机的回归用例（注入假定时器，不建真定时器）。
//
// 状态机的职责：支持 OSC 133 的 shell 在回车后主动上报权威命令行（C 标记），
// 屏幕文本推断只作兜底。这些用例锁死几条容易踩的路径：
//   * 没有 OSC 133 的 shell 必须保持「回车即落库」；
//   * 连按回车 / type-ahead 时 C 与命令要按序配对（不丢、不重复、不错配）；
//   * A/D 标记不能把还在等 C 的下一条提前落库（type-ahead 会张冠李戴）；
//   * Run Again / 快捷指令 / 多行粘贴已各自记账，队列为空时的 C 必须忽略；
//   * shell 被替换后要自愈回「立即落库」，不能一直延迟。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createShellIntegrationHistory } from '../src/utils/shellIntegrationHistory.ts';

function createHarness() {
  const out: string[] = [];
  const timers: Array<{ run: () => void; live: boolean }> = [];
  const history = createShellIntegrationHistory({
    onCommand: (command) => out.push(command),
    flushTimeoutMs: 2000,
    setTimeoutFn: (callback) => {
      const timer = { run: callback, live: true };
      timers.push(timer);
      return timer;
    },
    clearTimeoutFn: (handle) => {
      (handle as { live: boolean }).live = false;
    },
  });
  const fireTimeout = () => {
    for (const timer of timers.splice(0)) {
      if (!timer.live) continue;
      timer.live = false;
      timer.run();
    }
  };
  return { history, out, fireTimeout };
}

test('没有 OSC 133 的 shell：回车立刻落库（旧行为不变）', () => {
  const { history, out } = createHarness();
  assert.equal(history.submitCommand('ls'), 'immediate');
  // immediate 由调用方立刻落库，状态机自己不该派发
  assert.deepEqual(out, []);
});

test('正常一帧：回车挂起 → C 的权威命令行落库', () => {
  const { history, out } = createHarness();
  history.handleMark('A;click_events=1');
  history.handleMark('B');
  // 屏幕文本版本（fish 默认提示符下以前会带着提示符）
  assert.equal(history.submitCommand('hank@hankreed ~> ls'), 'deferred');
  assert.deepEqual(out, []);
  history.handleMark('C;cmdline_url=ls');
  assert.deepEqual(out, ['ls']);
});

test('连按回车 / type-ahead：C 与命令按序配对', () => {
  const { history, out } = createHarness();
  history.handleMark('A');
  assert.equal(history.submitCommand('cd /tmp'), 'deferred');
  assert.equal(history.submitCommand('ls'), 'deferred');
  assert.equal(history.submitCommand('pwd'), 'deferred');
  assert.deepEqual(out, []);
  history.handleMark('C;cmdline_url=cd%20%2Ftmp');
  history.handleMark('C;cmdline_url=ls');
  history.handleMark('C;cmdline_url=pwd');
  assert.deepEqual(out, ['cd /tmp', 'ls', 'pwd']);
});

test('A/D 标记不能把还在等 C 的下一条提前落库', () => {
  const { history, out } = createHarness();
  history.handleMark('A');
  history.submitCommand('ls');
  history.submitCommand('pwd');
  history.handleMark('C;cmdline_url=ls');
  // 上一条的 D 与下一条的 A/B 先到，此时 pwd 的 C 还没来
  history.handleMark('D;0');
  history.handleMark('A');
  history.handleMark('B');
  assert.deepEqual(out, ['ls']);
  history.handleMark('C;cmdline_url=pwd');
  assert.deepEqual(out, ['ls', 'pwd']);
});

test('C 丢失：由每条自己的兜底超时落库，且只落一次', () => {
  const { history, out, fireTimeout } = createHarness();
  history.handleMark('B');
  history.submitCommand('echo hi');
  history.handleMark('D;0');
  assert.deepEqual(out, []);
  fireTimeout();
  assert.deepEqual(out, ['echo hi']);
  history.handleMark('D;0');
  assert.deepEqual(out, ['echo hi']);
});

test('超时后自愈：后续命令退回立即落库（shell 被替换也不卡 2s）', () => {
  const { history, out, fireTimeout } = createHarness();
  history.handleMark('B');
  history.submitCommand('echo hi');
  fireTimeout();
  assert.deepEqual(out, ['echo hi']);
  assert.equal(history.submitCommand('ls -al'), 'immediate');
});

test('C 没有命令行时用屏幕文本兜底', () => {
  const { history, out } = createHarness();
  history.handleMark('B');
  history.submitCommand('echo hi');
  history.handleMark('C');
  assert.deepEqual(out, ['echo hi']);
});

test('屏幕文本为空时，C 的权威命令行仍要救回来', () => {
  const { history, out } = createHarness();
  history.handleMark('B');
  assert.equal(history.submitCommand(''), 'deferred');
  history.handleMark('C;cmdline_url=ls%20-al');
  assert.deepEqual(out, ['ls -al']);
});

test('空兜底 + C 也没有命令行：不产生空条目', () => {
  const { history, out } = createHarness();
  history.handleMark('B');
  history.submitCommand('');
  history.handleMark('C');
  assert.deepEqual(out, []);
});

test('Run Again / 快捷指令触发的 C（队列为空）必须忽略，避免重复记账', () => {
  const { history, out } = createHarness();
  history.handleMark('A');
  history.handleMark('C;cmdline_url=pwd');
  assert.deepEqual(out, []);
});

test('dispose 会清掉挂起项与定时器', () => {
  const { history, out, fireTimeout } = createHarness();
  history.handleMark('A');
  history.submitCommand('ls');
  history.dispose();
  fireTimeout();
  assert.deepEqual(out, []);
  // 释放后回到未初始化状态：立刻落库
  assert.equal(history.submitCommand('pwd'), 'immediate');
});

test('未知标记不消费（交回 xterm 默认处理）', () => {
  const { history } = createHarness();
  assert.equal(history.handleMark('X;foo=1'), false);
  assert.equal(history.handleMark('A'), true);
});
