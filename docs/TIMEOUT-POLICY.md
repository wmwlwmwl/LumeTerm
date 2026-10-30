# 超时策略（Timeout Policy）

本文档记录 Lumin-SSH 各类「远端操作 / 网络操作」的超时分层与规则，避免再次出现
「合法长耗时操作被短超时打断」或「卡死后永久挂起」两类问题。

## 分层表

| 场景 | 上限 | 实现位置 | 规则 |
|---|---|---|---|
| 远端短命令（stat/ls/mkdir/探测部署等） | 30s | `sshmanager.remoteCmdShortTimeout` | 只用于秒级命令，不得承载长任务 |
| 远端长命令（tar 压缩/解压、`rm -rf`、chmod -R、大文件 cp/mv） | 30min | `sshmanager.remoteCmdLongTimeout` | 必须走 `ExecuteLongCmdWithClientContext`，且必须响应 ctx 取消 |
| 非交互 git 命令 | 5min（重试仅针对 channel open 失败） | `sshmanager.ExecuteGitCommand` | 与交互式 git 终端一致；当前不透传 ctx，无法手动取消 |
| 探针一次采集（部署 15s + 运行 45s + 诊断 10s） | 70s 总预算 | `probeTotalBudget` | 必须小于前端等待上限（ProbePanel/BigScreen 90s） |
| 文件管理器长操作（压缩/解压/预览/chmod/chown/删除） | 30min | `fileOperationRegistry` + `AbortFileOperation(opID)` | 长操作必须有取消入口；tab/undo 这类短删除只传 opID、不弹常驻 Toast |
| 取消/超时后的远端进程 | `remoteCmdCancelGrace`（1s） | `terminateRemoteCommand` | 先发 `SIGTERM`（OpenSSH 对非受限会话是 `killpg`，能带走 tar/rm/unzip），等 1s 仍未退出才关 channel；**是 best-effort，不保证停住** |
| 远端删除（文件/目录） | 同上 | `removeRemotePaths` | POSIX 远端先 `rm -rf` 再用 SFTP `Lstat` 复核，未生效则回退 `RemoveAll`；**Windows 远端（banner 判定）直接走 SFTP**，不执行 POSIX shell 命令 |
| 下载/读文件 SFTP 传输 | 120s 无进展 | `stallguard` + 专用 SFTP 通道 | 只看「是否有进展」，不限制总时长；卡死只断本次专用通道。专用通道拿不到时回退共享 client（**无看门狗**） |
| 专用 SFTP 通道打开 | 30s | `transfer.dedicatedSFTPOpenTimeout` | `NewSession`/`RequestSubsystem` 不受 ctx 约束，打开阶段独立限时；超时回退共享 client |
| 上传分片写入 | 5min | `uploadChunkStallTimeout` | 超时先关 SSH session（唤醒阻塞 WriteAt）再丢弃该池 client，不阻塞后续分片 |
| R2 对象存储操作 | list/read/write 单次 60s、delete 30s | `internal/config/r2.go` | 幂等操作（list/read）对 5xx/429/408/网络超时重试 1 次，最坏约 120.5s |
| 前端等待后端 | 必须 > 后端预算 | 各面板常量 | 前端 `Promise.race` 超时只是「放弃等待」，不等于取消；取消必须显式调用 abort 接口 |

## 取消的副作用边界

| 操作 | 取消后的远端状态 | 是否可回滚 |
|---|---|---|
| 压缩 | 先写 `.lumeterm_compress_<ts>_*.tar.gz`，成功后才 `mv -f` 到目标；取消/失败会尝试删临时文件 | ✅ 目标归档可回滚；临时文件清理是 best-effort。若 SIGTERM 未生效且取消时临时文件还没建出来，`tar` 会继续跑完并 `mv` 成功，**取消之后目标目录可能才出现归档** |
| 解压（目标是新路径） | 解压前记录「本次新产生的输出」（folder 模式的新目录 / direct 模式的顶层文件或目录），取消时 `rm -rf` 清理 | ⚠️ best-effort：清理与仍在运行的远端进程有竞态（进程可能没被 SIGTERM 停住），且清理上限 30s，可能残留半成品 |
| 解压（覆盖/合并到已存在目录） | 已有文件可能被部分覆盖，无法区分哪些是本次写入 | ❌ 保持旧行为，取消可能留下部分改动 |
| 上传后远端解压（压缩上传） | 目标目录可能在取消时已有部分解压文件 | ❌ 暂未做事务化；建议取消后手动检查目标目录 |
| 删除（`rm -rf`） | 已删除的数据无法恢复；取消后目标可能部分删除，也可能继续删到结束 | ❌ 删除本质不可逆，取消只用于尽快止损 |
| 递归 chmod | 已改的权限无法自动恢复（需要逐文件快照） | ❌ 暂未实现快照回滚 |

> 说明：解压的「覆盖/合并」和删除、chmod 属于破坏性操作，POSIX 没有跨文件事务；
> 要完全回滚只能做 staging/快照，成本和风险较高，当前只在「新输出」场景做清理。

## 规则

1. **新增远端长操作必须提供可取消入口**：走 ctx 版本方法，并在 Wails binding
   暴露 `operationId`；前端提供取消按钮（Toast action）。
2. **不要给所有操作统一调大超时**：短命令保持 30s 兜底，避免僵死命令长期占用
   SSH channel（服务端 `MaxSessions` 默认 10）。
3. **阻塞式 IO（SFTP Read/Write）不要只靠 ctx**：`ctx` 无法打断已阻塞的 `Read`；
   需要专用通道 + 看门狗关闭通道。关闭时必须**先关 SSH session**（对端回
   `CHANNEL_CLOSE` 才能唤醒本地阻塞读），再关 sftp client；反过来会卡在
   `pkg/sftp` 等待 recv goroutine 的 `wg.Wait` 上。关闭函数本身不能同步阻塞调用方。
4. **超时值要成组设计**：后端总预算必须明显小于前端等待上限，否则会出现
   「前端先报超时、后端还在跑」的假超时。
5. **取消后的副作用要写清楚**：解压/删除/递归改权限被取消时，远端可能留下
   部分结果；压缩取消会清理本次产生的新归档，已有同名归档不删除。
6. **取消 ≠ 立即停住远端进程**：SSH 协议里关闭 channel 没有杀进程语义，
   `Session.Close()` 只是发一个 `CHANNEL_CLOSE`，OpenSSH 的
   `session_close_by_channel()` 在子进程存活时只 detach、不 kill。所以要显式
   发 `SIGTERM`（`terminateRemoteCommand`）并等 `remoteCmdCancelGrace`；
   即便这样，受限 shell（`ForceCommand`）、SFTP 子系统会话、非 OpenSSH 服务端
   和 Windows 远端都可能不生效。文档和验收标准里不要写「取消后远端立即停止」。
7. **shell 删除的结果必须复核**：`rm -rf` 的退出码不可全信。Windows
   `cmd.exe` 不解析 POSIX 单引号，`rm -rf 'C:\dir'` 拿到的是带引号的字面量
   路径——PATH 里有 coreutils/Git 的 `rm` 时 `-f` 会让它「静默成功」（退出码 0、
   什么都没删），没有可识别的错误文本。因此删除统一走 `removeRemotePaths`：
   POSIX 远端 `rm -rf` + SFTP `Lstat` 复核，Windows 远端（看 SSH banner）
   跳过 shell 直接 `RemoveAll`。代价是 Windows 远端大目录删除会慢（逐项 RPC），
   但不会出现「提示删除成功、实际没删」。

## 验证建议

- 大目录（GB 级）下载/上传/右键压缩解压：不再 30s 失败；点取消约 2s 内返回
  （1s SIGTERM 等待 + 清理，解压取消的清理最多 30s）。
- 取消删除/压缩后，**不要**用「远端条目数停止下降」「归档不再出现」作为验收
  标准：脚本类进程收到 SIGTERM 才是常态，但服务端不支持 signal 时远端会继续
  跑到结束。
- 慢服务器探针：90s 内返回结果或明确错误，后端最坏 70s。
- SFTP 卡死（服务端活着但不响应）：120s 后报 `remote SFTP made no progress`，
  其它终端/传输不受影响。本地 session 删除、非递归 chmod 目前不响应取消。
