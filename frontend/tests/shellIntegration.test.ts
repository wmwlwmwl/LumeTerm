// OSC 133 语义提示符解析的回归用例。
//
// 抓包事实（10.144.144.11 上 fish 4.8.1，真实 PTY 字节）：
//   ESC]133;A;click_events=1 ST   提示符出现
//   ESC]133;B ST                  命令行输入开始
//   ESC]133;C;cmdline_url=ls ST   开始执行，命令 percent 编码在 cmdline_url 里
//   ESC]133;D;0 ST                执行结束
// xterm 的 OscParser 会吃掉 `133;` 前缀，handler 拿到的是 `A;click_events=1` 这种
// 「标记 + 参数」串。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  decodeShellIntegrationCommand,
  normalizeShellIntegrationCommand,
  parseShellIntegrationMark,
} from '../src/utils/shellIntegration.ts';

test('四类标记都能识别', () => {
  assert.equal(parseShellIntegrationMark('A')?.kind, 'A');
  assert.equal(parseShellIntegrationMark('A;click_events=1')?.kind, 'A');
  assert.equal(parseShellIntegrationMark('B')?.kind, 'B');
  assert.equal(parseShellIntegrationMark('C;cmdline_url=ls')?.kind, 'C');
  assert.equal(parseShellIntegrationMark('D;0')?.kind, 'D');
  assert.equal(parseShellIntegrationMark('D;127')?.kind, 'D');
});

test('无法识别的 payload 必须返回 null（保持 xterm 默认行为）', () => {
  for (const data of ['', 'X', 'X;foo=1', '133', '1', 'ABC']) {
    assert.equal(parseShellIntegrationMark(data), null, `不应识别: ${JSON.stringify(data)}`);
  }
  // 标记大小写不敏感：某些集成会发小写
  assert.equal(parseShellIntegrationMark('a')?.kind, 'A');
  assert.equal(parseShellIntegrationMark('c;cmdline_url=ls')?.commandLine, 'ls');
});

test('cmdline_url：percent 解码出真实命令行', () => {
  // 以下三条都是实测抓到的 payload
  assert.equal(parseShellIntegrationMark('C;cmdline_url=ls')?.commandLine, 'ls');
  assert.equal(
    parseShellIntegrationMark('C;cmdline_url=fish%20--no-config')?.commandLine,
    'fish --no-config',
  );
  assert.equal(
    parseShellIntegrationMark('C;cmdline_url=hank%40hankreed%20~%3E%20ls')?.commandLine,
    'hank@hankreed ~> ls',
  );
  // 命令里的分号是 %3B，不能被参数分隔符切开
  assert.equal(
    parseShellIntegrationMark('C;cmdline_url=echo%20a%3B%20rm%20-rf%20%2Ftmp%2Fx')?.commandLine,
    'echo a; rm -rf /tmp/x',
  );
  // 无关参数应忽略
  assert.equal(
    parseShellIntegrationMark('C;click_events=1;cmdline_url=echo%20hi')?.commandLine,
    'echo hi',
  );
  // 只有 C 时不能编造命令
  assert.equal(parseShellIntegrationMark('C')?.commandLine, null);
});

test('未核实的键名一律忽略，交给屏幕文本兜底', () => {
  // `cmdline=` 不是 fish 的实现；未编码的命令会被 ; 截断，宁可不用
  assert.equal(parseShellIntegrationMark('C;cmdline=ls')?.commandLine, null);
  assert.equal(parseShellIntegrationMark('C;cmdline=echo a; rm -rf /tmp/x')?.commandLine, null);
});

test('非法 percent 转义不能抛异常', () => {
  assert.equal(decodeShellIntegrationCommand('100%'), '100%');
  assert.equal(parseShellIntegrationMark('C;cmdline_url=100%')?.commandLine, '100%');
});

test('命令行规范化：剥 ANSI、去控制字符、TAB 换空格', () => {
  assert.equal(normalizeShellIntegrationCommand('ls\u001b[0m\r\n'), 'ls');
  assert.equal(normalizeShellIntegrationCommand('  echo hi  '), 'echo hi');
  assert.equal(normalizeShellIntegrationCommand('echo\ta'), 'echo a');
  assert.equal(normalizeShellIntegrationCommand('echo hi\u0007'), 'echo hi');
  assert.equal(normalizeShellIntegrationCommand(''), '');
  assert.equal(normalizeShellIntegrationCommand(null), '');
});

test('多行命令判定为不可用（避免 Run Again 拆开执行 / 卡在 heredoc）', () => {
  assert.equal(normalizeShellIntegrationCommand('echo a\necho b'), '');
  assert.equal(normalizeShellIntegrationCommand('cat <<EOF\necho hi\nEOF'), '');
  assert.equal(normalizeShellIntegrationCommand('for i in 1 2\ndo echo $i\ndone'), '');
  // 单条命令尾部的换行只是换行，不算多行
  assert.equal(normalizeShellIntegrationCommand('ls\r\n'), 'ls');
});

test('超长 payload 直接丢弃', () => {
  const huge = 'C;cmdline_url=' + 'a'.repeat(70 * 1024);
  assert.equal(parseShellIntegrationMark(huge), null);
});
