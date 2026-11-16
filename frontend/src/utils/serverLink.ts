// 复制连接信息时的共享逻辑：读取真实密码、拼接 ssh:// 链接
import * as AppGo from '../../wailsjs/go/wailsapp/App.js';
import type { config } from '../../wailsjs/go/models.ts';

/** 读取连接的真实密码；旧连接可能未记录认证方式，回退读取连接自身的密码 */
export async function resolveServerPassword(serverId: string): Promise<string> {
  try {
    const password = await AppGo.GetConnectionPassword(serverId);
    if (password) return password;
  } catch {
    // 忽略，继续走回退逻辑
  }
  const conn = await AppGo.GetConnectionByID(serverId).catch(() => null);
  return conn?.password || '';
}

/** 生成 ssh://用户名:密码@主机:端口，无密码时省略密码段；IPv6 主机自动加方括号 */
export function buildSSHLink(server: config.Connection, password: string): string {
  const host = server.host.includes(':') && !server.host.startsWith('[') ? `[${server.host}]` : server.host;
  const user = encodeURIComponent(server.username || '');
  const auth = password ? `${user}:${encodeURIComponent(password)}` : user;
  return `ssh://${auth}@${host}:${server.port || 22}`;
}
