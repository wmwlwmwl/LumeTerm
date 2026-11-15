// 桥接模块（自 .js 收编后类型化）：代理节点持久化与归一化
function getAppBridge() {
  return window?.go?.wailsapp?.App;
}

function createProxyId() {
  return `proxy_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
}

/** 归一化后的代理节点（normalizeProxyNode 输出） */
export interface ProxyNode {
  id: string;
  name: string;
  type: 'http' | 'https' | 'socks5';
  host: string;
  port: number;
  username: string;
  password: string;
  updatedAt: number;
}

export function normalizeProxyNode(node: unknown): ProxyNode {
  const p = (node ?? {}) as Record<string, unknown>;
  const parsedPort = parseInt(String(p.port ?? '').trim(), 10);
  return {
    id: String(p.id || createProxyId()).trim(),
    name: String(p.name || '').trim(),
    type: p.type === 'http' || p.type === 'https' ? p.type : 'socks5',
    host: String(p.host || '').trim(),
    port: Number.isFinite(parsedPort) && parsedPort > 0 && parsedPort <= 65535 ? parsedPort : 1080,
    username: String(p.username || '').trim(),
    password: String(p.password || ''),
    updatedAt: Number.isFinite(Number(p.updatedAt)) && Number(p.updatedAt) > 0 ? Number(p.updatedAt) : Date.now(),
  };
}

function normalizeProxyNodes(nodes: unknown): ProxyNode[] {
  if (!Array.isArray(nodes)) {
    return [];
  }
  const seen = new Set<string>();
  const normalized: ProxyNode[] = [];
  nodes.forEach((node) => {
    const nextNode = normalizeProxyNode(node);
    if (!nextNode.host || seen.has(nextNode.id)) {
      return;
    }
    seen.add(nextNode.id);
    normalized.push(nextNode);
  });
  return normalized;
}

export async function getProxyNodes(): Promise<ProxyNode[]> {
  const bridge = getAppBridge();
  if (!bridge?.GetProxyNodes) {
    return [];
  }
  try {
    return normalizeProxyNodes(await bridge.GetProxyNodes());
  } catch {
    return [];
  }
}

export async function saveProxyNodes(nodes: unknown): Promise<ProxyNode[]> {
  const normalized = normalizeProxyNodes(nodes);
  const bridge = getAppBridge();
  if (bridge?.SaveProxyNodes) {
    await bridge.SaveProxyNodes(JSON.stringify(normalized));
  }
  return normalized;
}
