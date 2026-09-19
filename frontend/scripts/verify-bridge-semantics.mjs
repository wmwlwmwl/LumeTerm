// 桥接语义等价验证工具：bundle 工作区版本 vs HEAD 版本，黑盒对比导出函数输出
// 用法：node scripts/verify-bridge-semantics.mjs <相对路径1> [相对路径2...]
// 依赖：esbuild（frontend devDependency）
import esbuild from 'esbuild';
import fs from 'node:fs';
import { join } from 'node:path';
import { execSync } from 'node:child_process';
import { createRequire } from 'node:module';
import assert from 'node:assert';

const require = createRequire(import.meta.url);
const I18N = 'export const t = (k) => k; export const getLanguage = () => "zh-CN";';

/** 统一的 window 桥 mock（各桥按需取用，缺失方法返回 undefined 走守卫分支） */
function installWindowMock() {
  global.window = {
    dispatchEvent: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    go: {
      wailsapp: {
        AIBindings: {
          ListAIConversationBackups: async () => [{ id: 'b1', ts: 1, message: 'm', messageRole: 'user', type: 'auto' }],
          GetAIConversationBackupHistory: async () => [{ role: 'user', content: 'hi' }],
          RestoreAIConversationBackup: async () => ({ id: 'c1' }),
          DeleteAIConversationBackup: async () => {},
          GetProxyNodes: async () => [{ id: 'p1', host: 'h', port: 1080 }],
          SaveProxyNodes: async () => {},
          ListAIConversations: async () => [{ id: 'c1', title: 'T' }],
          CreateAIConversation: async (t) => ({ id: 'c-new', title: t || 'x' }),
          GetAIAssistantFirstReply: async () => 'reply',
          GetAIConversation: async () => ({ id: 'c1', messages: [{ id: 'm1', kind: 'user' }] }),
          SearchAIConversationMessages: async () => [{ conversationId: 'c1', role: 'user' }],
          SaveAIConversation: async (s) => JSON.parse(s),
          DeleteAIConversation: async () => {},
          CondenseAIConversationContext: async () => ({ id: 'c1', snapshot: { id: 'c1' } }),
          PreviewAIConversationContextCondense: async () => ({ id: 'c1', snapshot: { id: 'c1' } }),
          ProbeAIProviderLiveness: async () => true,
          CreateAIConversationSummarySubtask: async () => ({ snapshot: { id: 'c1' }, continueText: 'ct' }),
          OpenAIConversationFolder: async () => {},
          PreprocessAIConversationLongText: async () => 'pp',
          ReadAIConversationWrappedFile: async () => 'wrapped',
          BuildAIConversationTokenLedger: async () => ({ systemRawTokens: '10', entries: [{ messageId: 'm1', rawTokens: '5' }], contextTokens: '3' }),
          CountAIConversationAPIMessageRawTokens: async () => [{ messageId: 'm1', rawTokens: '5' }],
        },
        App: {
          GetProxyNodes: async () => [{ id: 'p1', host: 'h', port: 1080 }],
          SaveProxyNodes: async () => {},
          SaveRuntimeEnvironmentSettings: async () => {},
          GetRuntimeEnvironmentStatus: async () => ({ environmentType: 'uv', ready: true, binaryPath: '/b' }),
          InstallRuntimeEnvironment: async () => ({ environmentType: 'uv', ready: true, binaryPath: '/b' }),
          GetMCPSettingsState: async () => ({ service: { url: 'http://x', tools: ['t1'] }, client: { servers: [{ name: 's1', tools: [{ name: 'tool1', inputSchema: { a: 1 } }] }, { name: '' }], globalConfigPath: '/p', globalConfigText: '{}', embeddedServers: ['e1'], globalServerOrder: ['s1'] } }),
          SaveMCPGlobalServer: async () => {},
          ReloadMCPGlobalServers: async () => {},
          DeleteMCPGlobalServer: async () => {},
          RestartMCPClientServer: async () => {},
          ToggleMCPClientServer: async () => {},
          ToggleMCPClientServerDisabledForPrompts: async () => {},
          UpdateMCPClientServerTimeout: async () => {},
        },
      },
    },
  };
}

async function bundleToCjs(code, srcFile, outFile) {
  fs.writeFileSync(srcFile, code);
  const result = await esbuild.build({
    entryPoints: [srcFile], bundle: true, write: false, format: 'cjs', platform: 'node',
    plugins: [{
      name: 'mock-i18n',
      setup(build) {
        build.onResolve({ filter: /i18n\.ts$/ }, () => ({ path: 'i18n-mock', namespace: 'mock' }));
        build.onLoad({ filter: /.*/, namespace: 'mock' }, () => ({ contents: I18N, loader: 'js' }));
      },
    }],
  });
  fs.writeFileSync(outFile, result.outputFiles[0].text);
}

const files = process.argv.slice(2);
if (files.length === 0) {
  console.error('用法: node scripts/verify-bridge-semantics.mjs <相对路径1> [相对路径2...]');
  process.exit(1);
}

/** 深归一化时间戳（Date.now 量级的数字 → 'TS'），消除毫秒级随机差异 */
function scrubTimestamps(value) {
  if (typeof value === 'number' && value > 1e12) return 'TS';
  if (Array.isArray(value)) return value.map(scrubTimestamps);
  if (value && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, scrubTimestamps(v)]));
  }
  return value;
}

let totalPassed = 0, totalFailed = 0;
for (const file of files) {
  const idx = file.split('/').pop().replace('.ts', '');
  const newCode = fs.readFileSync(file, 'utf8');
  const oldCode = execSync(`git show HEAD:frontend/${file}`, { cwd: '..' }).toString();
  const newSrc = file.replace('.ts', `.new-${idx}.tmp.ts`);
  const oldSrc = file.replace('.ts', `.old-${idx}.tmp.ts`);
  const newOut = join(process.cwd(), `b1n-${idx}.tmp.cjs`);
  const oldOut = join(process.cwd(), `b1o-${idx}.tmp.cjs`);
  await bundleToCjs(newCode, newSrc, newOut);
  await bundleToCjs(oldCode, oldSrc, oldOut);
  const New = require(newOut);
  const Old = require(oldOut);

  let passed = 0, failed = 0;
  async function compare(name, fn) {
    try {
      const [n, o] = await Promise.all([fn(New), fn(Old)]);
      assert.deepStrictEqual(scrubTimestamps(n), scrubTimestamps(o));
      passed++;
    } catch (e) { failed++; console.log(`FAIL ${idx} ${name}:`, e.message.slice(0, 180)); }
  }

  installWindowMock();

  if (idx === 'inputDragSelect') {
    const listeners = {};
    global.window = { addEventListener: (k, f) => { listeners[k] = f; }, removeEventListener: (k) => { delete listeners[k]; } };
    const mkInput = (type = 'text') => ({ type, select: () => {}, style: { pointerEvents: '' } });
    const mkEvent = (input, buttons) => ({ buttons, currentTarget: input });
    for (const [b, t] of [[1, 'text'], [0, 'text'], [1, 'number'], [2, 'text'], [1, 'search']]) {
      await compare(`drag#${b}-${t}`, (m) => { const input = mkInput(t); m.handleInputDragSelectAll(mkEvent(input, b)); return { pe: input.style.pointerEvents, hasMouseup: !!listeners.mouseup }; });
    }
    delete global.window;
  } else if (idx === 'proxyNodesBridge') {
    const cases = [undefined, null, [], [{ id: 'n0', host: 'h', port: '8080' }], [{ id: 'a', host: 'h', port: '8080' }], [{ id: 'n1', host: '' }], [{ id: 'x', type: 'http', port: 70000 }], [{ id: 'y', type: 'socks5', port: 'abc' }], [{ id: 'a' }, { id: 'a', host: 'h2' }]];
    const nodeCases = [null, {}, { id: 'n0', host: 'h', port: '8080' }, { id: 'n1', host: '' }, { id: 'x', type: 'http', port: 70000 }, { id: 'y', type: 'socks5', port: 'abc' }, { id: 'a', name: '  ' }];
    for (let i = 0; i < cases.length; i++) {
      await compare(`nodes#${i}`, (m) => m.normalizeProxyNodes(cases[i]));
    }
    for (let i = 0; i < nodeCases.length; i++) {
      // id 为随机生成（Date.now/random），归一化后比较其余字段
      await compare(`node#${i}`, (m) => { const r = m.normalizeProxyNode(nodeCases[i]); return { ...r, id: 'X' }; });
    }
  } else if (idx === 'aiConversationBackupBridge') {
    const cases = [undefined, null, {}, { id: ' 1 ', ts: 5, message: 'm', messageRole: 'user', type: 'manual' }, { id: 3, ts: 'x' }];
    for (let i = 0; i < cases.length; i++) {
      await compare(`backup#${i}`, (m) => m.normalizeAIConversationBackup(cases[i]));
      await compare(`history#${i}`, (m) => m.normalizeAIConversationBackupHistoryEntry(cases[i]));
    }
    await compare('list', (m) => m.listAIConversationBackups('c1'));
    await compare('history', (m) => m.getAIConversationBackupHistory('c1', 'b1'));
    // snapshot 含 Date.now() 时间戳，归一化后比较
    await compare('restore', (m) => { const r = m.restoreAIConversationBackup('c1', 'b1'); return r.then((s) => (s && typeof s === 'object' ? { ...s, createdAt: 'X' } : s)); });
    await compare('delete', (m) => m.deleteAIConversationBackup('c1', 'b1'));
  } else if (idx === 'probeFormatting') {
    for (const v of [undefined, null, 0, -5, 1, 1024, 1048576, '512', '2G', '2.5 GiB', '100K', '1.5T', 'abc', '']) {
      await compare(`cap#${String(v)}`, (m) => m.formatCapacity(v, 1));
      await compare(`trans#${String(v)}`, (m) => m.formatTransferTotal(v));
      await compare(`rate#${String(v)}`, (m) => m.formatRate(v));
      await compare(`part#${String(v)}`, (m) => m.formatPartitionCapacity(v));
    }
    await compare('clamp0', (m) => m.clampPanelWidth(0));
    await compare('clamp1000', (m) => m.clampPanelWidth(1000));
    await compare('clamp300', (m) => m.clampPanelWidth(300));
  } else if (idx === 'aiExecutionContext') {
    global.window = { __luminEditorStates: { s1: { openFilePaths: ['/a.ts', '', '/b.ts'], activeFilePath: '/a.ts' } }, __luminFileManagerPaths: { s1: '/root' } };
    const opts = [undefined, {}, { sessionId: 's1', terminalId: 't1' }, { sessionId: '', terminalId: '' }, { sessionId: 5 }];
    for (let i = 0; i < opts.length; i++) {
      await compare(`snap#${i}`, (m) => { const s = m.getExecutionContextSnapshot(opts[i]); return { ...s, currentTimeISO: 'X', userTimeZone: 'Z' }; });
    }
    await compare('card', (m) => { const s = m.getExecutionContextSnapshot({ sessionId: 's1' }); return m.buildExecutionContextCardText({ ...s, currentTimeISO: 'T' }); });
    await compare('details', (m) => { const s = m.getExecutionContextSnapshot({ sessionId: 's1' }); return m.buildExecutionContextDetails({ ...s, currentTimeISO: 'T' }); });
    delete global.window;
  } else if (idx === 'aiProviderPasteHandlers') {
    const cases = [
      ['', undefined],
      ['not-json', undefined],
      ['{"access_token":"at"}', undefined],
      ['{"accessToken":"at2"}', undefined],
      ['{"entries":[{"key":"access_token","value":"et"}]}', undefined],
      ['{"apiKey":"ak"}', { source: 'cookie', path: { key: 'k' } }],
      ['{"foo":"bar"}', undefined],
      [null, undefined],
    ];
    for (let i = 0; i < cases.length; i++) {
      await compare(`fallback#${i}`, (m) => m.runAIProviderPasteHandlerById('missing-handler', cases[i][0], cases[i][1]));
      await compare(`blank#${i}`, (m) => m.runAIProviderPasteHandlerById('', cases[i][0], cases[i][1]));
    }
  } else if (idx === 'runtimeEnvironmentBridge') {
    const cases = [undefined, null, {}, { enabled: true, environmentType: 'uv' }, { targetPathTemplate: ' /x ', modulePath: 'm' }, { environmentType: 'py' }];
    for (let i = 0; i < cases.length; i++) {
      await compare(`set#${i}`, (m) => m.normalizeRuntimeEnvironmentSettings(cases[i]));
      await compare(`stat#${i}`, (m) => m.normalizeRuntimeEnvironmentStatus(cases[i]));
    }
    await compare('pathc', (m) => m.resolveRuntimeEnvironmentPathPreview('${APP_DIR}\\envs\\uv', 'C:\\App'));
    await compare('pathu', (m) => m.resolveRuntimeEnvironmentPathPreview('%APP_DIR%/envs', '/opt/lumin'));
    await compare('pathe', (m) => m.resolveRuntimeEnvironmentPathPreview('', ''));
    await compare('gets', (m) => m.getRuntimeEnvironmentSettings());
    await compare('save', (m) => m.saveRuntimeEnvironmentSettings({ environmentType: 'uv', targetPathTemplate: '/t' }));
    await compare('getst', (m) => m.getRuntimeEnvironmentStatus());
    await compare('inst', (m) => m.installRuntimeEnvironment());
  } else if (idx === 'aiSlashCommands') {
    const cmdCases = [undefined, null, [], [{ name: '/ls', prompt: 'list' }], [{ name: 'a b', prompt: 'x' }], [{ name: 'dup', prompt: '1' }, { name: 'DUP', prompt: '2' }], [{ name: 'ok', prompt: '' }], [{ name: '', prompt: 'p' }], 'str'];
    for (let i = 0; i < cmdCases.length; i++) {
      await compare(`norm#${i}`, (m) => m.normalizeAISlashCommands(cmdCases[i]));
      await compare(`find#${i}`, (m) => m.findAISlashCommandByName(cmdCases[i], '/dup'));
      await compare(`menu#${i}`, (m) => m.buildSlashCommandMenuItems(cmdCases[i], ''));
    }
    for (const [text, pos] of [['/ls hello', 3], ['plain', 2], ['/ab', 3], ['/x y', 2], ['', 0], ['/foo\nbar', 4]]) {
      await compare(`ctx#${pos}`, (m) => m.getSlashCommandMenuContext(text, pos));
      await compare(`insert#${pos}`, (m) => m.insertSlashCommandToken(text, pos, '/ls'));
      await compare(`expand#${pos}`, (m) => m.expandFirstSlashCommandForPrompt(text, [{ name: 'ls', prompt: 'list files' }]));
    }
  } else if (idx === 'messagesProvider') {
    for (const v of [undefined, null, '', 'claude-opus-4-8', 'claude-opus-4-7', 'claude-sonnet-4-x', 'claude-3.7-sonnet-x', 'claude-anything', 'gpt-4o', '  CLAUDE-OPUS-4-8  ', 42]) {
      await compare(`cap#${String(v)}`, (m) => m.messagesProvider.getModelCapability(v));
    }
  } else if (idx === 'aiGlobalSettingsBridge') {
    const cases = [undefined, null, {}, { currentProviderId: ' p ', soundVolume: '0.5' }, { allowedCommands: ['a', 'a', '', 5], soundEnabled: false }, { approvalButtonOrder: 'bogus' }, { alwaysAllowWrite: true, toolResultTokenThreshold: '100' }, { slashCommands: [{ name: 'x', prompt: 'p' }] }, { collaborationPromptPresets: [{ id: 'c1', title: 't', text: 'x' }] }, { proxyNodes: [{ id: 'n1', host: 'h', port: 8080, type: 'http' }], aiRequestProxyId: 'n1' }, { aiRequestProxyId: 'missing' }];
    for (let i = 0; i < cases.length; i++) {
      await compare(`norm#${i}`, (m) => m.normalizeAIGlobalSettings(cases[i]));
    }
    await compare('presets', (m) => m.normalizeAICollaborationPromptPresets([{ id: 'p1', text: 'a' }, { text: 'b' }, { text: '' }]));
    await compare('get', (m) => m.getAIGlobalSettings());
    await compare('save', (m) => m.saveAIGlobalSettings({ currentProviderId: 'x' }));
  } else if (idx === 'aiImageCompression') {
    for (const v of [undefined, null, '', 'data:image/png;base64,AAAA', 'rawbase64data', 'a,b']) {
      await compare(`size#${String(v)}`, (m) => m.calculateBase64Size(v));
    }
  } else if (idx === 'compatibleProvider' || idx === 'responsesProvider') {
    for (const v of [undefined, null, '', 'gpt-5.4', 'gpt-5.2-x', 'gpt-5.1', 'gpt-5-chat', 'gpt-5', 'gpt-5-2025-06-01', 'o4-mini-high', 'o3-mini-low', 'o3', 'o1-preview', 'codex-mini', 'gpt-4o', '  GPT-5.4  ', 42]) {
      await compare(`cap#${String(v)}`, (m) => m[idx === 'compatibleProvider' ? 'compatibleProvider' : 'responsesProvider'].getModelCapability(v));
    }
  } else if (idx === 'index') {
    for (const v of [undefined, null, '', 'Compatible', 'Responses', 'Messages', 'nope', ' compatible ']) {
      await compare(`def#${String(v)}`, (m) => m.getAIProviderDefinition(v).value);
      await compare(`web#${String(v)}`, (m) => m.canUseDedicatedWebSearchCandidate(v));
    }
    await compare('options', (m) => m.availableAIProviderOptions);
    await compare('providers', (m) => m.availableAIProviders.map((p) => p.value));
  } else if (idx === 'aiConversationBridge') {
    const snapshotCases = [undefined, null, {}, { id: 'c1', title: 'T', messages: [{ id: 'm1', kind: 'user', text: 'hi' }], apiMessages: [{ role: 'user', content: 'x' }], settings: { alwaysAllowWrite: true } }, { id: '  c2  ', archived: true, messageCount: 3, promptCacheBypassTimestamp: 'ts' }];
    for (let i = 0; i < snapshotCases.length; i++) {
      await compare(`snap#${i}`, (m) => m.normalizeAIConversationSnapshot(snapshotCases[i]));
      await compare(`summ#${i}`, (m) => m.normalizeAIConversationSummary(snapshotCases[i]));
      await compare(`task#${i}`, (m) => m.normalizeAIConversationTaskSettings(snapshotCases[i]));
    }
    const messageCases = [undefined, null, {}, { id: 'm1', kind: 'tool', question: 'q', questions: [{ text: 'q1', type: 'multi_select', options: [{ answer: 'a' }, {}] }], images: ['i1', '', 5], extra: { a: 1 } }];
    for (let i = 0; i < messageCases.length; i++) {
      await compare(`msg#${i}`, (m) => m.normalizeAIConversationMessage(messageCases[i]));
      await compare(`api#${i}`, (m) => m.normalizeAIConversationAPIMessage(messageCases[i]));
    }
    await compare('search', (m) => m.normalizeAIConversationMessageSearchResult({ conversationId: ' c ', role: 'user', updatedAt: 5 }));
    await compare('list', (m) => m.listAIConversations());
    await compare('create', (m) => m.createAIConversation('title'));
    await compare('first', (m) => m.getAIAssistantFirstReply());
    await compare('get', (m) => m.getAIConversation('c1'));
    await compare('searchMsgs', (m) => m.searchAIConversationMessages('q', 'c1', 10));
    await compare('save', (m) => m.saveAIConversation({ id: 'c1', messages: [] }));
    await compare('delete', (m) => m.deleteAIConversation('c1'));
    await compare('condense', (m) => m.condenseAIConversationContext('c1', 's1'));
    await compare('preview', (m) => m.previewAIConversationContextCondense('c1', 's1'));
    await compare('probe', (m) => m.probeAIProviderLiveness('c1', 's1'));
    await compare('subtask', (m) => m.createAIConversationSummarySubtask('c1', 's1'));
    await compare('openFolder', (m) => m.openAIConversationFolder('c1'));
    await compare('preprocess', (m) => m.preprocessAIConversationLongText('c1', 'text'));
    await compare('readWrapped', (m) => m.readAIConversationWrappedFile('c1', '/p'));
    await compare('ledger', (m) => m.buildAIConversationTokenLedger('s1', { id: 'c1' }));
    await compare('countTokens', (m) => m.countAIConversationAPIMessageRawTokens('s1', 'c1', [{ role: 'user', content: 'x' }]));
    await compare('subscribe', (m) => { const off = m.subscribeAIConversationChanges(() => {}); const r = typeof off === 'function'; off(); return r; });
  } else if (idx === 'mcpClientBridge') {
    await compare('getState', (m) => m.getMCPSettingsState());
    await compare('saveGlobal', (m) => m.saveMCPGlobalServer('s1', '{}'));
    await compare('reload', (m) => m.reloadMCPGlobalServers());
    await compare('deleteGlobal', (m) => m.deleteMCPGlobalServer('s1'));
    await compare('restart', (m) => m.restartMCPClientServer('s1', 'global'));
    await compare('toggle', (m) => m.toggleMCPClientServer('s1', 'global', true));
    await compare('togglePrompt', (m) => m.toggleMCPClientServerDisabledForPrompts('s1', 'global', false));
    await compare('timeout', (m) => m.updateMCPClientServerTimeout('s1', 'global', 30));
  } else {
    console.log(`${idx}: 未知模块（无测试用例），跳过`);
  }

  console.log(`${idx}: ${passed} passed, ${failed} failed`);
  totalPassed += passed; totalFailed += failed;
  for (const f of [newOut, oldOut, newSrc, oldSrc]) fs.rmSync(f, { force: true });
}

console.log(`\n总计: ${totalPassed} passed, ${totalFailed} failed`);
process.exit(totalFailed ? 1 : 0);
