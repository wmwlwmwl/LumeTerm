// 命令历史 / 提示符识别的回归用例。
//
// 背景：issue #423 —— fish 默认提示符 `user@host ~>` 结尾是 `>`，不在剥离白名单里，
// 历史被整行存成「提示符+命令」，Run Again 再把提示符当命令发出去，
// 报 `fish: Unknown command: user@host`。
//
// 这些用例锁死两件事：
//   1. fish（以及既有各类）提示符必须只留下命令本身；
//   2. 普通命令输出不能被当成提示符（否则命令块/门控会误判）。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  extractCommandFromBufferLine,
  isCollapseSummaryLine,
  isLooseShellPromptLine,
  isShellPromptLine,
} from '../src/utils/terminalHelpers.ts';

test('fish 默认提示符：只保留命令', () => {
  const cases: Array<[string, string]> = [
    // issue #423 的现场（反馈者的真实提示符）
    ['jgh@ov-fa02c1 ~> clear', 'clear'],
    // 本机实测提示符
    ['hank@hankreed ~> ls', 'ls'],
    ['hank@hankreed ~>', ''],
    ['hank@hankreed ~> ', ''],
    // 深路径
    ['hank@hankreed /home/hank/allcode/golang/Lumin-SSH> ls', 'ls'],
    // 命令内重定向不能被非贪婪的 > 切走
    ['hank@hankreed ~> cat a > b', 'cat a > b'],
    ['hank@hankreed ~> ls > out.txt', 'ls > out.txt'],
    // 命令内的 $ 不能被通用 [#$%] 规则吃掉
    ['hank@hankreed ~> echo $HOME', 'echo $HOME'],
    ['hank@hankreed ~> echo "a > b"', 'echo "a > b"'],
    ['hank@hankreed ~> ls -la | head -3', 'ls -la | head -3'],
  ];
  for (const [line, expected] of cases) {
    assert.equal(extractCommandFromBufferLine(line), expected, `剥离失败: ${JSON.stringify(line)}`);
  }
});

test('不得回归：#$% 结尾提示符 + 命令内重定向', () => {
  // fish 规则排在 [#$%] 通用形态之前，但必须带 (?![^>]*[#$%]) 前瞻，
  // 否则这些行会被 fish 规则先命中，命令被切成重定向目标。
  const cases: Array<[string, string]> = [
    ['hank@hankreed ~$ echo hi > f', 'echo hi > f'],
    ['hank@hankreed ~ % echo hi > f', 'echo hi > f'],
    ['root@hankreed ~# echo hi > /tmp/f', 'echo hi > /tmp/f'],
    ['hank@hankreed ~$ ls -l 2>&1', 'ls -l 2>&1'],
    ['charlie@host ~ % cat a > b', 'cat a > b'],
  ];
  for (const [line, expected] of cases) {
    assert.equal(extractCommandFromBufferLine(line), expected, `剥离失败: ${JSON.stringify(line)}`);
  }
});

test('既有提示符形态保持不变', () => {
  const cases: Array<[string, string]> = [
    ['root@host:~$ ls -al', 'ls -al'],
    ['root@host:/etc# ls', 'ls'],
    ['[root@host ~]$ ls', 'ls'],
    ['root@host ~]# ls', 'ls'],
    ['$ ls', 'ls'],
    ['❯ ls', 'ls'],
    ['PS C:\\Users\\me> dir', 'dir'],
    ['>>> print(1)', 'print(1)'],
    // 非提示符行原样返回
    ['git@github.com: Permission denied (publickey).', 'git@github.com: Permission denied (publickey).'],
  ];
  for (const [line, expected] of cases) {
    assert.equal(extractCommandFromBufferLine(line), expected, `剥离结果变了: ${JSON.stringify(line)}`);
  }
});

test('命令块分块判定：接受 fish 提示符且不误判输出', () => {
  const prompts = [
    'root@host:~# ping',
    'root@host:~$ ls',
    'root@host ~]# ls',
    'user@host ~>',
    'user@host ~> ls',
    'user@host /a/b/c> ls',
    'PS C:\\Users\\me> dir',
  ];
  for (const line of prompts) {
    assert.equal(isShellPromptLine(line), true, `应识别为提示符: ${JSON.stringify(line)}`);
  }
  const outputs = [
    'total 24',
    '⋯ 12 lines',
    '~',
    // 升级日志里的箭头不能当成 fish 提示符的 >
    'pkg@1.2.3 -> pkg@1.2.4',
    'index@2 > total@1',
    'git@github.com Permission denied (publickey).',
  ];
  for (const line of outputs) {
    assert.equal(isShellPromptLine(line), false, `不应识别为提示符: ${JSON.stringify(line)}`);
  }
  assert.equal(isCollapseSummaryLine('⋯ 12 lines'), true);
  assert.equal(isCollapseSummaryLine('total 24'), false);
});

test('逐行门控判定：fish 提示符（含带命令形态）应识别', () => {
  const prompts = [
    'user@host ~>',
    'user@host ~> ls',
    'user@host /a/very/deep/path> tail -f x.log',
    '❯ ls',
    'λ ',
    '(venv) user@host:~$ ls',
  ];
  for (const line of prompts) {
    assert.equal(isLooseShellPromptLine(line), true, `应识别: ${JSON.stringify(line)}`);
  }
  const outputs = [
    '123456',
    '$ hello',
    'f(λ)',
    'output»',
    'pkg@1.2.3 -> pkg@1.2.4',
    'index@2 > total@1',
    '',
  ];
  for (const line of outputs) {
    assert.equal(isLooseShellPromptLine(line), false, `不应识别: ${JSON.stringify(line)}`);
  }
});
