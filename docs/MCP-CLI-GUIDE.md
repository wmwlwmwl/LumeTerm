# 外部 CLI 通过 MCP 控制 LumeTerm 服务器 — 接入指南

LumeTerm 内置 MCP 服务（Streamable HTTP），监听 `127.0.0.1:5779/mcp`。Claude Code、Codex 等 CLI 工具可通过 MCP 连接，像真人一样操控你已连接的 SSH 服务器——命令在终端里实时可见，操作痕迹显示在 MCP 活动面板中。

## 快速接入

### Claude Code

在 `~/.claude.json` 顶层的 `mcpServers` 对象里加入 `lumeterm` 条目（**只加内层条目，不要再包一层 `mcpServers`**）：

```json
{
  "mcpServers": {
    "ui-kit": {
      "type": "sse",
      "url": "https://ui-kit-mcp.example.com/sse"
    },
    "lumeterm": {
      "type": "http",
      "url": "http://127.0.0.1:5779/mcp"
    }
  }
}
```

注意：
- 条目必须带 `type` 字段：Streamable HTTP 用 `"http"`，SSE 端点（URL 以 `/sse` 结尾）用 `"sse"`，本地命令用 `"stdio"`。
- 如果文件里已有顶层 `"mcpServers": { ... }`，只把 `"lumeterm": { ... }` 这一段粘进去，整段示例再粘一遍会形成双层嵌套，导致条目被识别为无效服务器而跳过。

或通过命令行（推荐，免去手工编辑）：

```bash
claude mcp add lumeterm --transport http http://127.0.0.1:5779/mcp
```

### Codex / 其他 MCP 客户端

在客户端的 MCP 配置（通常是 `mcp.json` 或类似）中加入，同样只加内层条目并带 `type`：

```json
{
  "mcpServers": {
    "lumeterm": {
      "type": "http",
      "url": "http://127.0.0.1:5779/mcp"
    }
  }
}
```

## 使用流程

1. 在 LumeTerm 中**连接你的 SSH 服务器**（MCP 只能操控已连接的会话）
2. CLI 连接 MCP 后，先调用 `list_connected_sessions` 获取 `session_id`
3. 使用 `session_id` 调用其他工具：`execute_command`、`list_files`、`read_file`、`write_to_file` 等
4. 若某个工具提示服务器已断开连接，调用 `reconnect_server`（传原来的 `session_id`）重连后重试

## 可用工具

| 工具 | 说明 |
|------|------|
| `list_connected_sessions` | 列出所有已连接的 SSH 会话（含 `address` 字段 `user@host:port`，可区分同名服务器） |
| `get_work_path` | 获取会话当前工作目录 |
| `list_files` | 列出远程目录 |
| `read_file` | 读取远程文件 |
| `write_to_file` | 写入远程文件 |
| `execute_command` | 在真实终端中执行命令（可见、交互式） |
| `transfer_batch` | 批量传输文件 |
| `transfer_list` | 查看传输队列 |
| `search_replace` / `apply_diff` / `apply_patch` / `edit_file` | 远程文件编辑 |
| `reconnect_server` | 重连已断开的 SSH 会话（复用原父会话 id，返回新旧终端 id 映射） |

## 断线重连

SSH 服务器意外断开（网络波动、保活超时等）时：

1. 对该会话的任何工具调用都会返回明确错误，提示「服务器已断开连接，请调用 reconnect_server」
2. AI 调用 `reconnect_server`（`session_id` 传原父会话 id 或任一子终端 id），LumeTerm 会在后台重新拨号，复用原父会话 id 并按原数量重开子终端；界面会自动恢复该会话的终端、布局与工作区
3. 若连续 3 次重连失败（网络不通、认证失败、主机密钥变更等），LumeTerm 会弹窗提醒用户手动处理；主机密钥变更时会在界面弹出确认框，用户确认后 AI 再次调用 `reconnect_server` 即可成功

> **授权与开关**：`reconnect_server` 与其它 MCP 能力一致，跟随 MCP 全局授权——MCP 服务器开启即可用，关闭则工具不会出现在工具目录里，AI 自然无法调用。「自动重连」则属于连接管理域，仍按服务器独立开关（服务器编辑 → 高级选项，默认关闭），只为开启的服务器自动重连。

## 可见性

外部 MCP 的操作会在以下位置留下痕迹：

- **终端**：命令直接在 SSH 终端中执行（与手动输入无异）
- **MCP 活动弹窗**：默认关闭；在 AI 设置 → MCP 中开启「外部 MCP 操作弹窗」后自动弹出（右下角活动按钮可重新打开），显示客户端名称、服务器、命令、状态、输出
- **审批**（可选）：在 AI 设置 → MCP 中开启「外部 MCP 写操作需审批」后，写操作需在活动弹窗中手动批准（开启审批会同时开启活动弹窗）

## 安全说明

- MCP 服务仅绑定 `127.0.0.1`，不对公网暴露
- 默认拒绝浏览器调用（仅允许无 Origin 的本机 CLI）
- 审批门是可选的摩擦层，**不是**防同用户恶意进程的安全边界
- 外部 CLI 能操控的范围 = 你在 LumeTerm 中已连接的 SSH 会话
