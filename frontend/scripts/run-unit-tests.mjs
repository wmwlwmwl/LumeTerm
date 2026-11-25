// 前端单元测试跑批器。
//
// 用项目已有的 esbuild（devDependency）把 tests/**/*.test.ts 打成单个 CJS，
// 再交给 Node 内置的 node:test 执行——不引入 vitest/jest 之类的新依赖，
// 也不需要联网安装。用法：npm run test:unit
//
// 注意：这里必须走 esbuild 的 JS API，不能直接执行 node_modules/esbuild/bin/esbuild。
// 后者在 Windows 上是 JS 垫片、在 Linux/macOS 上是原生二进制，用 node 去跑会挂。
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const frontendDir = resolve(scriptDir, '..');
const testsDir = join(frontendDir, 'tests');

function listTestFiles(dir) {
  const files = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      files.push(...listTestFiles(full));
    } else if (entry.endsWith('.test.ts')) {
      files.push(full);
    }
  }
  return files.sort();
}

const testFiles = listTestFiles(testsDir);
if (testFiles.length === 0) {
  console.error(`未找到任何 *.test.ts（${testsDir}）`);
  process.exit(1);
}

const workDir = mkdtempSync(join(tmpdir(), 'lume-unit-tests-'));
const entryPath = join(workDir, 'entry.ts');
const bundlePath = join(workDir, 'tests.cjs');

// 绝对路径导入，避免临时目录与 tests/ 的相对路径问题；Windows 反斜杠转正斜杠
writeFileSync(
  entryPath,
  testFiles.map((file) => `import ${JSON.stringify(file.replace(/\\/g, '/'))};`).join('\n') + '\n',
  'utf8',
);

try {
  await build({
    entryPoints: [entryPath],
    outfile: bundlePath,
    bundle: true,
    platform: 'node',
    format: 'cjs',
    // 用例自己断言，不跑模块里的 DEV 断言块（避免重复噪音）
    define: { 'import.meta.env.DEV': 'false' },
    logLevel: 'error',
  });
  execFileSync(process.execPath, ['--test', bundlePath], { stdio: 'inherit', cwd: frontendDir });
} finally {
  rmSync(workDir, { recursive: true, force: true });
}
