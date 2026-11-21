export const PROXY_NODES_CHANGED_EVENT = 'lumin:proxy-nodes-changed';

/** 编辑器表单字段（含历史遗留 authType/group；index signature 供 set(key) 计算键更新） */
export interface ServerEditorForm {
  name: string;
  host: string;
  port: string;
  username: string;
  authType: string;
  password: string;
  privateKey: string;
  passphrase: string;
  terminalInitPath: string;
  fileManagerInitPath: string;
  terminalEncoding: string;
  allowLegacySshRsa: boolean;
  autoReconnect: boolean;
  proxyMode: string;
  proxyNodeId: string;
  proxyType: string;
  proxyHost: string;
  proxyPort: string;
  proxyUsername: string;
  proxyPassword: string;
  proxyAllowInsecureTLS: boolean;
  group?: string;
  [key: string]: unknown;
}

/** 代理节点（来自 AI 全局设置） */
export interface ProxyNode {
  id?: string;
  name?: string;
  type?: string;
  host?: string;
  port?: string | number;
}

export const defaultForm: ServerEditorForm = {
  name: '',
  host: '',
  port: '',
  username: 'root',
  authType: 'password',
  password: '',
  privateKey: '',
  passphrase: '',
  terminalInitPath: '',
  fileManagerInitPath: '',
  terminalEncoding: 'utf-8',
  allowLegacySshRsa: false,
  autoReconnect: false,
  proxyMode: 'direct',
  proxyNodeId: '',
  proxyType: 'socks5',
  proxyHost: '',
  proxyPort: '1080',
  proxyUsername: '',
  proxyPassword: '',
  proxyAllowInsecureTLS: false,
};
