<div align="center">

# LumeTerm

**轻量、高速、全平台终端客户端**

[![Release](https://img.shields.io/github/v/release/wmwlwmwl/LumeTerm?style=flat-square&color=0078D6&label=RELEASE)](https://github.com/wmwlwmwl/LumeTerm/releases)
[![Platform](https://img.shields.io/badge/PLATFORM-WINDOWS%20%7C%20macOS%20%7C%20Linux-0078D6.svg?style=flat-square)](https://github.com/wmwlwmwl/LumeTerm/releases)
[![License](https://img.shields.io/badge/LICENSE-Source%20License%201.1-8CBA00.svg?style=flat-square)](LICENSE)

[English](./README_EN.md) · [简体中文](./README.md)

</div>

---

## 概述

> **Android 客户端**（独立仓库、分开发版）：[LumeTerm-Android](https://github.com/wmwlwmwl/LumeTerm-Android) · [发行版](https://github.com/wmwlwmwl/LumeTerm-Android/releases)

LumeTerm 是一款面向开发者和运维人员的桌面终端客户端。基于 **Go 原生并发 + 本机 WebSocket + xterm.js**，在 Wails 桌面壳中提供低延迟终端体验。内置系统资源探针、远程文件管理器（内置/外置编辑器）、命令历史与智能补全、连接级代理、可选加密云同步、AI 对话与 MCP 集成等能力，**无需在服务器安装 Agent**。

<div align="center">
  <img src="assets/pc_empty_main.png" alt="LumeTerm 主面板" width="800" />
  <br /><br />
  <img src="assets/pc_connected_session.png" alt="LumeTerm 终端与资源监控" width="800" />
</div>

---

## 核心功能

### 终端与连接
- **原生级全异步 PTY** — Go 并发处理 I/O；终端流量走本机 loopback WebSocket（随机端口 + 会话级 token + Origin 校验），不经 Wails IPC 热路径
- **预测本地回显** — 高延迟网络下输入仍保持跟手
- **多终端标签** — 同一 SSH 连接内可开多个独立终端标签
- **多会话管理** — 同时维护多台服务器会话；标签右键：断开 / 关闭 / 重连
- **本地与串口终端** — Windows 支持 PowerShell、CMD 与已安装的 WSL 发行版，macOS/Linux 支持本机 Shell；还可直连串口设备。WSL/Unix 本地会话支持文件管理器与资源探针，Windows 原生 Shell 支持本地文件管理器但不支持资源探针，串口会话仅提供终端
- **终端编码** — 每个服务器可单独选择 UTF-8、GB18030/GBK/Big5、日/韩文编码、Windows/ISO-8859、IBM/OEM 等编码，输入输出双向转换
- **SSH 通道占用** — 会话标签显示终端、共享文件与上传通道合计，接近服务器上限时预警
- **可折叠命令块** — 终端左侧可显示命令块边框，点击收起输出，便于浏览长日志
- **终端链接** — URL 可点击，用系统浏览器打开
- **终端时间戳** — 可选行首时间戳（xterm marker，随 scrollback 同步）
- **日志关键字高亮** — 自定义关键字配色规则（ANSI 16 色或任意色值），长日志一眼定位 ERROR/告警
- **Alt 历史指令** — 按 Alt 呼出历史命令快速切换（可关闭）；命令输入区快捷键提示可用 F1 开关
- **敏感信息隐藏** — 一键隐藏/显示密码、密钥等
- **会话级 shell hook（bash）** — 连接 bash 时注入会话级 `PROMPT_COMMAND`，采集命令历史与 CWD（不写 `.bashrc`）；用于历史面板 / AI 与补全；另支持 OSC 733 序列兼容 zsh/fish 等非 bash shell 的目录跟随
- **端口转发** — SSH 本地/远程端口转发，增删改查，按会话持久化，断连自动回收

### 仪表盘
- **内联服务器编辑** — 左侧常驻添加/编辑表单，支持「保存」或「保存并连接」
- **卡片 / 表格双视图** — 网格卡片与表格布局切换
- **搜索过滤** — 按名称、主机、标签等实时搜索
- **智能延迟检测** — **SSH Banner RTT**（可穿透部分 TUN/代理场景）与 **TCP Dial**；可配置间隔或关闭；Banner 模式有更保守的刷新下限以降低安全设备误报
- **标签溢出下拉** — 标签过多时收起为可搜索下拉

### 服务器管理
- **保存并连接** — 添加后可立即建连
- **克隆服务器** — 右键克隆，拷贝配置（含密码/密钥/凭据引用/代理）
- **导入 / 导出** — 导出全部或勾选节点（含引用凭据与代理节点）为 **明文 JSON** 或 **密文 `.lumeterm2`**；密文可复用恢复密码或自定义密码；导入自动识别 JSON / `.lumeterm2`（兼容旧 `.lumin2`）；提供导入模板
- **重复检测** — host + port + username 重复时拦截
- **分组管理** — 分组、移动、按组过滤
- **操作系统图标** — 自动识别 Ubuntu、Debian、CentOS、RHEL、Rocky、Alma、Fedora、Arch、NixOS、Alpine、Kali、Gentoo、openSUSE、openEuler、OpenCloudOS、Anolis、TencentOS、Alibaba、AOSC、Oracle、FreeBSD、Windows、macOS 等；图标资源位于 `frontend/public/`
- **凭据管理** — 可复用用户名/密码/私钥，修改后引用方自动生效
- **连接级代理** — 直连、引用代理节点，或自定义 SOCKS5 / HTTP
- **初始路径** — 终端与文件管理器可分别设置初始目录

### 系统资源探针
- **零 Agent** — 连上后按需部署监控脚本（如 `~/.lumeterm/probe.sh`），无需常驻服务
- **实时指标** — 每核 CPU、内存、网络吞吐、磁盘分区等
- **GPU / RAID** — 额外信息查询（环境支持时）
- **进程管理** — 查看、搜索、排序、发信号；可开启终止确认；低开销 top-N 采样（兼容 OpenWrt 等精简环境，排除采样脚本自身开销）
- **网络明细** — 活动连接与流量相关视图
- **刷新间隔与面板位置** — 可配置；探针可放左侧或右侧

### 远程文件管理器
- **完整文件操作** — 浏览、上传、下载、删除、重命名、新建、复制/移动
- **剪贴板** — 右键复制 / 剪切 / 粘贴（会话内远程路径）
- **内置编辑器** — 语法高亮编辑远端文件（体积上限约 5MB）
- **外置编辑器** — 系统默认或指定本地编辑器；本地保存后自动回传（fsnotify + 防抖，约 5MB 上限）；记忆编辑器路径
- **压缩 / 解压** — tar.gz 等；支持双击解压压缩包（可在设置中开关）；解压时校验符号链接目标，防路径逃逸
- **压缩传输** — 多文件本机打包 tar.gz，上传后远端解压
- **分块上传** — 可配置块大小、并发文件数、每文件并发块、全局在途上限
- **传输队列** — 上传/下载队列面板；传输通道并发与复用经过优化
- **大目录性能** — 文件列表虚拟化渲染，避免大目录一次创建全部行节点
- **文件定位器** — 在当前目录按名称定位，支持上一个/下一个命中与键盘导航
- **类型识别与刷新** — 正确区分目录和符号链接；终端命令完成或面板重新聚焦时自动刷新当前目录
- **下载冲突策略** — 询问 / 覆盖 / 跳过 / 重命名；可按大小、修改时间判定
- **chmod / chown** — 可视化权限与所有者
- **跟随终端目录** — 终端 `cd` 后可同步路径
- **拖拽上传** — 本地文件拖入面板
- **复制远程路径** — 右键复制完整路径
- **布局**
  - 工作区停靠：**标签页 / 右侧分栏 / 底部分栏**
  - 文件列表：**顶部标签单栏** 或 **左侧标签双面板**（历史标签 + 左右双列表）

### 命令历史、补全与快捷指令
- **自动捕获** — 回车执行的命令写入本机历史库（按服务器 / 全局）
- **搜索与回放** — 搜索后一键重发
- **智能补全** — 汇总服务器历史、全局历史、快捷指令、常用命令与远端路径
- **快捷指令库** — 分组管理；可发到当前或全部会话
- **固定命令栏** — 将快捷指令常驻在终端输入区上方，点击后确认再发送
- **动态参数** — `p#` 占位，执行时弹窗填写

### 凭据与代理节点
- **凭据** — 集中管理密码/私钥/口令；多服务器引用
- **代理节点** — 设置 → 网络中维护 SOCKS5 / HTTP 节点；服务器可引用；AI 请求也可指定代理节点
- **导入导出联动** — 导出连接时带上被引用的代理节点

### AI 对话与 MCP
- **内置 AI 面板** — 多轮对话、流式输出、推理展示、消息编辑/重试/复制
- **多协议供应商** — Compatible / Messages / Responses 等
- **斜杠命令与 @ 提及** — `/` 自定义命令；`@` 引用终端输出或远端路径
- **工具审批** — 读取/写入自动批准可独立开关；执行审批为三档互斥模式：**基本规则**（命令白名单）/ **只读批准** / **全部批准**，支持许可与拒绝命令前缀（冲突时最长前缀匹配优先）；支持继续/终止、终端重新指派
- **变更审阅** — 远端编辑类工具提供 diff / patch / 恢复工作台
- **上下文压缩与对话备份** — Token 压缩；自动/手动备份与还原；任务标题可编辑
- **对话搜索** — 对 AI 对话历史做全文检索（SQLite FTS5 + CJK 回退）
- **联网搜索** — 支持 Responses API `web_search` 工具；可按供应商开关或指定专用联网搜索供应商
- **对话存储目录** — 可在 AI 设置中自定义对话数据存储路径，更改时自动迁移现有数据
- **协同相关能力** — 对话协同接管等（见 AI 面板）
- **多会话工作区** — AI 支持终端内多标签会话，布局与会话随工作区恢复
- **生成调优** — 思考深度最高可选 Max；模型缓存策略与高级采样参数可配
- **终端隔离** — 可为终端维护相对独立的 AI 运行期上下文
- **内置 MCP 服务（Streamable HTTP）** — 默认启用并监听 `127.0.0.1:5779`（可在 AI 设置中关闭）；**无 HTTP token**，主要靠 loopback + Origin 摩擦控制（**不是**同用户恶意进程的安全边界）
- **外部 CLI 接入** — Claude Code、Codex 等 CLI 可通过 MCP 连接操控已连接的服务器；操作痕迹在 MCP 活动面板可见（客户端、服务器、命令、状态）；可选开启写操作审批（[接入指南](./docs/MCP-CLI-GUIDE.md)）
- **MCP 客户端** — 添加外部 MCP（stdio / SSE / Streamable HTTP），启停、重载、超时等
- **终端输出控制** — 限制 MCP/AI 读取终端输出的行数与字符数

#### 内置 MCP 工具（节选）
`list_connected_sessions` · `get_work_path` · `list_files` · `read_file` · `write_to_file` · `transfer_batch` · `transfer_list` · `execute_command` · `ask_followup_question` · `attempt_completion` · `search_replace` · `apply_diff` · `apply_patch` · `edit_file`

### 云同步
- **后端** — WebDAV、Cloudflare R2（S3 兼容）、FTP、SFTP（均为**用户自建/自有**端点）
- **快照内容** — 服务器、凭据、快捷指令、AI 供应商与全局设置、代理节点、删除墓碑等
- **加密策略**
  - 已设置**恢复密码** → 上传 **`.lumeterm2`**（PBKDF2 + AES-GCM）
  - **未设置** → 上传**明文 `.json`**（便于迁移，但云端可读敏感字段，请谨慎）
- **合并与墓碑** — 按时间与删除记录合并，降低多端互相覆盖；自动同步可独立开关，模式可选单一后端或「全部」
- **保留份数** — 可配置

### 本地存储
- 配置**明文**存储：密码、私钥、passphrase、代理密码、凭据、恢复密码、AI Key 等直接写入本地 JSON（目录权限 0700）
- **不再本地加密**：密钥文件与密文同目录本无真实防护（拿到目录即拿到密钥），防护只保留在真正离开本机的环节——云同步与导出的 `LUMETERM2` 密文由**恢复密码**派生密钥
- 旧版本加密数据升级后自动解密读取，首次保存即转为明文

### 自动更新
- 启动约 2.5s 后检查 GitHub Release（不阻塞首屏）
- 设置 → 关于可手动检查
- 仅允许 HTTPS 的 GitHub Release 资产下载路径；可选 gh 镜像加速
- **强制 SHA256** 校验后安装/热替换（Windows 便携版、安装包；Linux deb/rpm；macOS dmg + codesign 校验等按平台）

### 系统托盘与单实例
- 关闭窗口：**每次询问 / 直接退出 / 最小化到托盘**
- 单实例；重复启动唤起已有窗口
- 托盘左键显示、右键菜单；久置后尽量强制前台（平台相关）

### 视觉、主题与布局
- **深色 / 浅色** — 可跟随系统
- **主题包** — 内置多套终端/界面主题（含深浅变体）；支持主题包在浅/深色间复制与 AI 辅助调色（配色模式对话默认不落库）
- **字体管理** — 导入 ttf/otf/ttc/woff/woff2，分别用于界面、终端、AI 面板
- **背景** — 全局背景与终端背景独立设置；全局背景可选「覆盖终端」，均支持透明度调节
- **标题栏主题快捷入口** — 可开关
- **分屏** — 探针、文件管理器、AI 面板宽度/位置可调；布局偏好持久化
- **Toast** — 非侵入提示

### 快捷键、语言与工作区
- **自定义快捷键** — 复制/粘贴/清屏/新建标签/信号与清空输入行等
- **国际化** — 内置 **28** 种语言/地区包，即时切换
- **工作区记忆** — 窗口尺寸与最大化；可选恢复会话与分屏；程序级或会话级持久化

### 运行环境
- 设置 → 运行环境：安装/检测 **uv**

---

## 快速开始

### 首次使用
1. 从 [Releases](https://github.com/wmwlwmwl/LumeTerm/releases) 下载当前平台包（Windows 便携/安装包、Linux deb/rpm、macOS dmg 等）
2. 运行后配置目录自动创建（见下表）
3. 仪表盘左侧填写主机信息 → **保存** 或 **保存并连接**
4. 需要时在设置中配置代理节点、云同步、恢复密码、AI 供应商等

### 日常
- 双击卡片或右键连接；会话内 `+` 开多终端
- 侧栏打开探针 / 文件 / AI
- 快捷指令、凭据、克隆、导入导出从主机列表与对应入口进入

---

## 配置与数据

### 路径

| 平台 | 配置目录 |
|------|----------|
| Windows | `%APPDATA%\LumeTerm\config\` |
| macOS | `~/Library/Application Support/LumeTerm/config/` |
| Linux | `~/.config/LumeTerm/config/` |

### 主要文件（节选）

| 文件 / 目录 | 用途 |
|-------------|------|
| `connections.json` | 服务器列表（明文存储） |
| `credentials.json` | 凭据库 |
| `webdav.json` 等 | 各同步后端配置（账号密钥等明文存储） |
| `quick_commands.json` | 快捷指令 |
| `param_history.json` | 动态参数历史 |
| `history/` | 命令历史 |
| `sync_mode.json` / `auto_sync_enabled.json` / `sync_tombstones.json` 等 | 同步模式、自动同步开关、时间戳与删除墓碑 |
| `recovery_password` | 恢复密码（明文存放，0600） |
| `ai_global_settings.json` | AI 全局（含 MCP 开关、自动批准等） |
| `ai_providers.json` | AI 供应商列表（含 API Key 等业务字段） |
| `proxy_nodes.json` | 代理节点 |
| `tasks/` | AI 对话与备份（存储目录可在 AI 设置中自定义，路径记录于 `app_settings.json`） |
| `port_forwards.json` | 端口转发记录 |
| `file_manager_settings.json` | 文件管理器偏好（双击解压、传输调优等） |
| `app_settings.json` | 应用偏好（GPU 加速、运行环境、主题包、AI 对话存储路径） |
| `workspace_*.json` | 工作区状态、偏好与会话恢复 |

> Windows 的 WebView2 用户数据目录固定在 `%APPDATA%\LumeTerm\`，与 `config\` 同级；便携版可执行文件改名不会再创建多套浏览器数据目录。

---

## 自动更新

1. 拉取 `wmwlwmwl/LumeTerm` 的 GitHub Release 元数据  
2. 按平台匹配安装包 / 便携包  
3. HTTPS 下载（可镜像）→ **`.sha256` 校验** → 平台安装或热替换  

版本号以 `wails.json`、`frontend/src/config.ts`、`frontend/package.json`、`frontend/package-lock.json` 为准，当前为 **1.3.1**。

---

## 设置界面

| 标签 | 内容 |
|------|------|
| **通用** | 语言、各类操作确认、关闭窗口行为、工作区记忆、更新镜像、WebView GPU 等 |
| **网络** | 延迟协议与间隔、探针刷新、代理节点 |
| **文件管理器** | 跟随终端、压缩传输、队列、双击解压、布局/标签、下载冲突、分块上传等 |
| **运行环境** | uv 安装与检测 |
| **外观** | 字体、终端主题与背景、界面主题包、探针位置、命令块边框等 |
| **快捷键** | 终端快捷键绑定 |
| **同步与云** | WebDAV / R2 / FTP / SFTP、恢复密码、保留份数、自动同步 |
| **关于** | 版本、检查更新、仓库与社区链接 |

> AI 供应商、工具审批、MCP 服务/客户端、对话备份等在 **AI 面板设置** 中，不占用上表标签。

---

## 构建

### 环境
- Go **1.26+**（见 `go.mod`）
- Node.js 18+
- [Wails v2](https://wails.io/) CLI

### 命令

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
git clone https://github.com/wmwlwmwl/LumeTerm.git
cd LumeTerm

wails dev            # 启动开发模式
wails build          # 便携/平台默认产物
wails build -nsis    # Windows 安装包（需 NSIS）
```

常见产物：`build/bin/LumeTerm` / `LumeTerm.exe`；安装包与 deb/rpm/dmg 由 CI 或本地脚本按平台生成。

Windows 本地一键构建（自动同步版本号、UPX 压缩、输出 `LumeTerm-V{版本}-portable.exe` 与 `LumeTerm-V{版本}-amd64-installer.exe`）：

```powershell
.\build_release.ps1    # 需本地具备 Go、NSIS、UPX 环境
```

正式发版（打 tag、更新日志、多平台包）见 [.github/RELEASE.md](.github/RELEASE.md)。

---

## 安全与注意

### 务必了解
- **配置明文存储** — 本机配置目录内含明文密码/私钥/AI Key（目录权限 0700）。请勿把配置目录拷给不受信任的人；需要外发备份时用**加密导出**（恢复密码保护）或加密云同步。
- **恢复密码与明文同步** — 未设恢复密码时，云同步为**明文 JSON**（含服务器密码、私钥、AI Key 等）。生产环境强烈建议设置强恢复密码。
- **主机密钥** — 首次连接核对指纹；变更会提示，降低 MITM 风险。
- **终端 WebSocket** — 仅 `127.0.0.1`、随机端口、随机 token、Origin 限制。
- **MCP** — 默认绑定 `127.0.0.1:5779`；可关闭。无 token 时，**同用户本地进程**理论上可调用已连接会话上的工具。不要对公网暴露，也不要假设能防本机木马。
- **更新链** — 依赖 GitHub Release 与 SHA256；账号/CI 被攻破时哈希可被同时替换，属供应链信任模型限制。

### 使用习惯
- 单实例；托盘与关闭行为在「通用」中配置  
- 外置编辑：先内置再外置；以文件变更监听回传，不以编辑器进程退出为准  
- 涉及本地运行环境能力时需先装好 **uv** 运行环境

---

## FAQ

### 密码如何存？
本机配置**明文**存储（目录 0700）。云同步与加密导出用恢复密码派生密钥生成 `.lumeterm2` 密文，或明文 `.json`。

### 如何多机同步？
设置 → 同步与云 → 配置自有 WebDAV/R2/FTP/SFTP。建议先设恢复密码再开自动同步。

### 克隆会带密码吗？
会，与原配置一致（含密钥与凭据引用）。

### 凭据和「写在服务器上的密码」有何不同？
凭据是可复用实体，一处修改、多处生效。

### 外置编辑远程文件？
文件管理器打开 → 内置编辑器 → 使用系统/指定编辑器；本地保存后自动回传。

### AI / MCP？
AI 面板配置供应商与审批策略。MCP 在 AI 设置中开关；启用后本机 MCP 客户端或编辑器可连 `127.0.0.1:5779`（详见面板说明）。

### 支持哪些桌面系统？
Windows、macOS、Linux 原生构建。

---

## 赞助

如果你觉得 LumeTerm 有用，欢迎扫码支持：

<div align="center">
  <table>
    <tr>
      <td align="center">
        <img src="assets/wx.png" alt="微信赞赏" width="200" />
        <br/><strong>微信</strong>
      </td>
      <td align="center">
        <img src="assets/zfb.png" alt="支付宝赞赏" width="200" />
        <br/><strong>支付宝</strong>
      </td>
      <td align="center">
        <img src="assets/qq.png" alt="QQ赞赏" width="200" />
        <br/><strong>QQ</strong>
      </td>
    </tr>
  </table>
</div>

---

## 贡献

- Bug：[Issues](https://github.com/wmwlwmwl/LumeTerm/issues/new)
- PR：Fork 后提交；尽量贴合现有风格；I/O 与网络保持非阻塞

---

## 许可证

见 [LICENSE](LICENSE)（**LumeTerm Source License 1.1**，与 Android 端同族）：

| | |
|--|--|
| **可以** | 非商业使用、学习、研究、公开二开（保留许可与署名；对外发布须**源码可得**） |
| **不可以** | 商用（出售、收费分发、商业内嵌、营利服务等，定义见 LICENSE） |
| **不可以** | 仅以加密/加壳/强混淆形式对外发布且不提供对应可读源码 |

**范围：** 本许可约束本仓**原创代码**；第三方依赖仍遵守其原许可证（不得用本许可去削弱第三方已授予的权利）。

本许可为自定义条款，**非正式法律意见**。涉及商业边界时请自行咨询律师。

> 桌面端与 Android **分仓分发**；本仓库 Release **仅 Desktop**。Android 见 [LumeTerm-Android](https://github.com/wmwlwmwl/LumeTerm-Android)。
