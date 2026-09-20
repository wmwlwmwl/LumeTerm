import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from '@babel/parser';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const localeRoot = path.join(root, 'src', 'i18n');
const files = fs.readdirSync(localeRoot)
  .map((name) => path.join(localeRoot, name, 'basic.ts'))
  .filter((file) => fs.existsSync(file))
  .sort();

function readTable(file) {
  const source = fs.readFileSync(file, 'utf8');
  const ast = parse(source, { sourceType: 'module', plugins: ['jsx', 'typescript'] });
  const declaration = ast.program.body.find((node) => node.type === 'ExportDefaultDeclaration');
  // 语言表以 `export default {...} satisfies I18nDict` 标注，需展开 satisfies/as 包装
  let exportValue = declaration?.declaration;
  while (exportValue && (exportValue.type === 'TSSatisfiesExpression' || exportValue.type === 'TSAsExpression')) {
    exportValue = exportValue.expression;
  }
  if (!exportValue || exportValue.type !== 'ObjectExpression') {
    throw new Error(`${file}: default export must be an object`);
  }
  const entries = new Map();
  const duplicates = [];
  for (const property of exportValue.properties) {
    if (property.type !== 'ObjectProperty' || property.computed) continue;
    const key = property.key.type === 'Identifier'
      ? property.key.name
      : property.key.type === 'StringLiteral'
        ? property.key.value
        : null;
    if (!key) continue;
    if (entries.has(key)) duplicates.push(key);
    const value = property.value.type === 'StringLiteral' ? property.value.value : '';
    entries.set(key, value);
  }
  return { entries, duplicates };
}

function placeholders(value) {
  return [...String(value).matchAll(/\{(\w+)\}/g)].map((match) => match[1]).sort();
}

const tables = new Map();
let failed = false;
for (const file of files) {
  const locale = path.basename(path.dirname(file));
  try {
    tables.set(locale, readTable(file));
  } catch (error) {
    failed = true;
    console.error(`[parse] ${error.message}`);
  }
}

const baseline = tables.get('zh-CN');
if (!baseline) {
  console.error('[fatal] missing zh-CN/basic.js');
  process.exit(1);
}
const baselineKeys = new Set(baseline.entries.keys());
const english = tables.get('en-US')?.entries || new Map();
const naturalEnglish = /[A-Za-z]{3,}\s+[A-Za-z]{2,}|^(?:[A-Za-z]+[ .,:;!?-]*){2,}$/;
const technicalOnly = /^(AI|SSH|GitHub|TCP|TUN|URL|JSON|MCP|WebDAV|SFTP|FTP|FTPS|HTTP|HTTPS|RTT|EOF|SIGINT|SIGTSTP|Ctrl|Alt|Shift|Space|WebSocket|Windows|Android|Linux|Unix|Debian|Ubuntu|macOS|OpenAI|Anthropic|Claude|Sonnet|Opus|Haiku|LumeTerm|Lumin|Lumin-SSH|Lumin-SSH-Android|Clash|V2Ray|Git|GitHub|Markdown|CSS|HTML|JavaScript|Python|Go|SQL|SCP|SSH Banner RTT|TCP Dial|<1ms|\d+(?:\.\d+)?\s*(?:ms|KB|MB|GB))$/i;
const englishSameWordAllowlist = {
  cs: new Set(['编辑器', '模型']),
  de: new Set(['存储桶', '终端', '名称', '离线', '文本', '系统', '缓存', '别名', '系统编辑', '编辑器', '参数', '版本', '导出']),
  es: new Set(['终端', '八进制:', '通用', '别名', '编辑器']),
  fr: new Set(['终端', '会话', '别名', '选项', '版本', '饱和度', '助手']),
  id: new Set(['存储桶', '别名', '编辑器', '程序', '模型']),
  it: new Set(['标记校验', '存储桶', '端点地址', '别名']),
  nl: new Set(['偶校验', '存储桶', '离线', '提示：', '别名', '编辑器', '模型', '运行环境', '停止']),
  pl: new Set(['区域 (Region)', '别名', '模型']),
  'pt-BR': new Set(['存储桶', '端点地址', '八进制:', '别名', '编辑器']),
  ro: new Set(['终端', '八进制:', '离线', '通用', '前缀 (Prefix)', '别名', '编辑器', '程序', '模型']),
};

const showAllCandidates = process.argv.includes('--all-english');
const jsonOutput = process.argv.includes('--json');
const report = {};

if (!jsonOutput) {
  console.log(`Locales: ${tables.size}`);
  console.log(`Baseline zh-CN keys: ${baselineKeys.size}`);
  if (baseline.duplicates.length) console.log(`zh-CN duplicates: ${[...new Set(baseline.duplicates)].join(', ')}`);
}

for (const [locale, table] of tables) {
  const keys = new Set(table.entries.keys());
  const missing = [...baselineKeys].filter((key) => !keys.has(key));
  const extra = [...keys].filter((key) => !baselineKeys.has(key));
  const placeholderMismatches = [...baselineKeys].filter((key) => keys.has(key)
    && JSON.stringify(placeholders(baseline.entries.get(key))) !== JSON.stringify(placeholders(table.entries.get(key))));
  const englishCandidates = locale === 'en-US' ? [] : [...keys].filter((key) => {
    const value = table.entries.get(key);
    const reference = english.get(key);
    return value && reference && value === reference && naturalEnglish.test(value)
      && !technicalOnly.test(value) && !englishSameWordAllowlist[locale]?.has(key);
  });
  report[locale] = {
    keys: keys.size,
    missing,
    extra,
    duplicates: [...new Set(table.duplicates)],
    placeholderMismatches,
    englishCandidates: Object.fromEntries(englishCandidates.map((key) => [key, table.entries.get(key)])),
  };
  if (!jsonOutput) {
    console.log(`${locale}: keys=${keys.size} missing=${missing.length} extra=${extra.length} duplicate=${table.duplicates.length} placeholders=${placeholderMismatches.length} englishCandidates=${englishCandidates.length}`);
    if (missing.length) console.log(`  missing: ${missing.join(' | ')}`);
    if (extra.length) console.log(`  extra: ${extra.join(' | ')}`);
    if (placeholderMismatches.length) console.log(`  placeholder mismatch: ${placeholderMismatches.join(' | ')}`);
    if (englishCandidates.length) {
      const reported = showAllCandidates ? englishCandidates : englishCandidates.slice(0, 20);
      console.log(`  english candidates: ${reported.map((key) => `${key} => ${table.entries.get(key)}`).join(' | ')}${!showAllCandidates && englishCandidates.length > 20 ? ' | ...' : ''}`);
    }
    if (table.duplicates.length) console.log(`  duplicates: ${[...new Set(table.duplicates)].join(' | ')}`);
  }
  if (missing.length || extra.length || table.duplicates.length || placeholderMismatches.length) failed = true;
}

if (jsonOutput) console.log(JSON.stringify(report, null, 2));
process.exitCode = failed ? 1 : 0;
