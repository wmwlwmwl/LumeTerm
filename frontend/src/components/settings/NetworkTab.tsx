import { useEffect, useState } from 'react';
import { t as $t } from '../../i18n.ts';
import { Lightbulb } from 'lucide-react';
import { cn } from '../../utils/cn.ts';
import { Button, Select } from '../ui';
import { ToggleSwitch, RadioOption, SettingRow, SettingsPanel, SettingsSectionTitle, SettingsTabRoot } from './SharedComponents';
import { settings } from './settingDefinitions';
import { getAIGlobalSettings, saveAIGlobalSettings } from '../ai/aiGlobalSettingsBridge.ts';
import { getProxyNodes, saveProxyNodes, normalizeProxyNode } from './proxyNodesBridge.ts';

const PROXY_NODES_CHANGED_EVENT = 'lumin:proxy-nodes-changed';

/** 代理节点表单（port 为字符串输入态，保存时转数字） */
interface ProxyFormState {
  name: string;
  type: string;
  host: string;
  port: string;
  username: string;
  password: string;
}

/** 代理节点（来自 proxyNodesBridge.ts（已类型化桥接），字段按需取用） */
interface ProxyNodeLike {
  id?: unknown;
  name?: unknown;
  type?: unknown;
  host?: unknown;
  port?: unknown;
  username?: unknown;
  password?: unknown;
}

const defaultProxyForm: ProxyFormState = {
  name: '',
  type: 'socks5',
  host: '',
  port: '1080',
  username: '',
  password: '',
};

function createProxyId() {
  return `proxy_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`;
}

const MOBILE_MEDIA_QUERY = '(max-width: 820px)';

function getIsMobileLayout() {
  return typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia(MOBILE_MEDIA_QUERY).matches;
}

interface NetworkTabProps {
  pingEnabled: boolean;
  onTogglePingEnabled: () => void;
  pingMode: string;
  onPingModeChange: (mode: string) => void;
  probeInterval: number;
  onProbeIntervalChange: (seconds: number) => void;
  pingInterval: number;
  onPingIntervalChange: (seconds: number) => void;
}

export default function NetworkTab({ pingEnabled, onTogglePingEnabled, pingMode, onPingModeChange, probeInterval, onProbeIntervalChange, pingInterval, onPingIntervalChange }: NetworkTabProps) {
  // settingDefinitions.ts 已类型化，直接使用 settings 注册表
  const settingsData = settings.network;
  const [proxyNodes, setProxyNodes] = useState<ProxyNodeLike[]>([]);
  const [proxyForm, setProxyForm] = useState<ProxyFormState>(defaultProxyForm);
  const [editingProxyId, setEditingProxyId] = useState('');
  const [isMobileLayout, setIsMobileLayout] = useState(() => getIsMobileLayout());
  const [aiGlobalSettings, setAIGlobalSettings] = useState<{ aiRequestProxyId?: string } | null>(null);

  const persistProxyNodes = (nextNodes: ProxyNodeLike[]) => {
    setProxyNodes(nextNodes);
    window.dispatchEvent(new CustomEvent(PROXY_NODES_CHANGED_EVENT, { detail: nextNodes }));
    saveProxyNodes(nextNodes).catch(() => {});
    const currentSelectedProxyId = aiGlobalSettings?.aiRequestProxyId || '';
    const nextSelectedProxyId = nextNodes.some((item) => item?.id === currentSelectedProxyId) ? currentSelectedProxyId : '';
    if (nextSelectedProxyId !== currentSelectedProxyId) {
      const nextSettings = {
        ...(aiGlobalSettings || {}),
        aiRequestProxyId: nextSelectedProxyId,
      };
      setAIGlobalSettings(nextSettings);
      saveAIGlobalSettings(nextSettings).catch(() => {});
    }
  };

  const setProxyField = (key: keyof ProxyFormState) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
    const value = e?.target?.value ?? '';
    setProxyForm((current) => ({ ...current, [key]: value }));
  };

  const resetProxyForm = () => {
    setProxyForm(defaultProxyForm);
    setEditingProxyId('');
  };

  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
      return undefined;
    }
    const mediaQuery = window.matchMedia(MOBILE_MEDIA_QUERY);
    const handleChange = (event: MediaQueryListEvent) => setIsMobileLayout(event.matches);
    setIsMobileLayout(mediaQuery.matches);
    if (typeof mediaQuery.addEventListener === 'function') {
      mediaQuery.addEventListener('change', handleChange);
      return () => mediaQuery.removeEventListener('change', handleChange);
    }
    mediaQuery.addListener(handleChange);
    return () => mediaQuery.removeListener(handleChange);
  }, []);

  useEffect(() => {
    let cancelled = false;
    Promise.all([getAIGlobalSettings(), getProxyNodes()])
      .then(([settingsResult, nextNodes]) => {
        if (cancelled) {
          return;
        }
        setAIGlobalSettings(settingsResult);
        setProxyNodes(nextNodes);
        window.dispatchEvent(new CustomEvent(PROXY_NODES_CHANGED_EVENT, { detail: nextNodes }));
      })
      .catch(() => {
        if (cancelled) {
          return;
        }
        setProxyNodes([]);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const showAlert = (message: string) => {
    if (window?.lumeDialog?.alert) {
      window.lumeDialog.alert(message, $t('提示'), { priority: 'settings' });
      return;
    }
    window.alert(message);
  };

  const handleProxySubmit = (e: React.FormEvent<HTMLFormElement>) => {
    e?.preventDefault();
    const host = String(proxyForm.host || '').trim();
    const port = parseInt(String(proxyForm.port || '').trim(), 10);
    if (!host) {
      showAlert($t('请输入代理主机地址'));
      return;
    }
    if (!Number.isFinite(port) || port <= 0 || port > 65535) {
      showAlert($t('请输入有效端口'));
      return;
    }
    const nextNode = normalizeProxyNode({
      ...proxyForm,
      id: editingProxyId || createProxyId(),
      host,
      port,
      updatedAt: Date.now(),
    });
    const nextNodes = editingProxyId
      ? proxyNodes.map((item) => item?.id === editingProxyId ? nextNode : item)
      : [...proxyNodes, nextNode];
    persistProxyNodes(nextNodes);
    resetProxyForm();
  };

  const handleProxyEdit = (node: ProxyNodeLike) => {
    setEditingProxyId(String(node.id));
    setProxyForm({
      name: String(node.name || ''),
      type: String(node.type || 'socks5'),
      host: String(node.host || ''),
      port: String(node.port || 1080),
      username: String(node.username || ''),
      password: String(node.password || ''),
    });
  };

  const handleProxyDelete = async (id: string) => {
    const node = proxyNodes.find((item) => item?.id === id);
    const name = node?.name || node?.host || $t('未命名节点');
    const confirmed = await window.lumeDialog?.confirm?.(
      `${$t('确定删除代理节点')}「${name}」？${$t('此操作不可撤销')}`,
      $t('操作确认'),
      '',
      { priority: 'settings' },
    );
    if (!confirmed) {
      return;
    }
    const nextNodes = proxyNodes.filter((item) => item?.id !== id);
    persistProxyNodes(nextNodes);
    if (editingProxyId === id) {
      resetProxyForm();
    }
  };

  return (
    <SettingsTabRoot>
      <div>
        <SettingsSectionTitle definition={settingsData.sections.latency} />
        <div className="text-sm text-secondary mb-2.5">{$t('开启或关闭对主页所有服务器的网络可用性及延迟自动探测。')}</div>
        <SettingsPanel>
          <SettingRow
            definition={settingsData.fields.pingEnabled}
            description={$t('定期向服务器发起轻量级探测，实时了解服务器的在线状态和响应速度')}
            action={<ToggleSwitch checked={pingEnabled} onChange={onTogglePingEnabled} />}
          />
        </SettingsPanel>
        <div className="mt-2.5 px-3.5 py-2.5 bg-[rgba(var(--warning-rgb),0.12)] rounded-lg text-sm text-tertiary leading-[1.7] border border-[rgba(var(--warning-rgb),0.28)]">
          <span className="inline-flex items-center align-middle mr-1"><Lightbulb size={14} /></span>{' '}
          <strong className="text-secondary">{$t('安全说明：')}</strong>
          {$t('延迟检测会在主页对列表中的服务器周期性探测 SSH 端口，仅做连通/延迟判断，不会使用密码或密钥登录。智能检测默认 2 秒刷新，直连只做 TCP；疑似 TUN/代理时约每 30 秒才做一次 Banner 确认以防假在线。选择「SSH Banner RTT」时会自动将间隔调整为至少 15 秒。若环境有登录失败告警策略，可关闭检测、增大间隔，或在纯内网使用 TCP Dial。')}
        </div>
      </div>
      <div>
        <SettingsSectionTitle definition={settingsData.sections.mode} />
        <div className="text-sm text-secondary mb-2.5">{$t('选择延迟检测的探测方式，不同方式适用于不同网络环境。')}</div>
        <SettingsPanel data-settings-field-id={settingsData.fields.detectionMode.id} className="flex flex-col gap-2">
          {[
            { id: 'auto', label: <><span className="text-[10px] bg-accent-dim border border-accent-border text-accent px-1.5 py-px rounded-sm font-bold mr-1.5">{$t('推荐')}</span>{$t('智能检测')}</>, desc: $t('直连用 TCP 测延迟；检测到代理/TUN 时低频 Banner 确认可达性，避免 Clash 等环境下不可达主机显示 0 毫秒在线') },
            { id: 'banner', label: $t('SSH Banner RTT'), desc: $t('所有连接都读取 SSH 握手响应测速，准确反映真实可达性，能穿透 TUN/代理；选择后会自动将延迟检测间隔调整为至少 15 秒') },
            { id: 'tcp', label: $t('TCP Dial'), desc: $t('仅检测 TCP 端口连通性，速度最快，但在 TUN/代理下可能把不可达服务器误判为在线') },
          ].map((opt) => (
            <RadioOption
              key={opt.id}
              definition={opt.id === 'auto' ? settingsData.fields.smartDetection : (opt.id === 'banner' ? settingsData.fields.bannerRtt : settingsData.fields.tcpDial)}
              selected={pingMode === opt.id}
              label={opt.label}
              description={opt.desc}
              onClick={() => onPingModeChange(opt.id)}
            />
          ))}
        </SettingsPanel>
        <div className="mt-2.5 px-3.5 py-2.5 bg-overlay rounded-lg text-sm text-tertiary leading-[1.7] border border-line-light">
          <span className="inline-flex items-center align-middle mr-1"><Lightbulb size={14} /></span> <strong className="text-secondary">{$t('提示：')}</strong>{$t('使用 TUN 模式代理（Clash/V2Ray 等）时建议选「智能检测」：可识别不可达主机且不会每 2 秒半开 SSH。纯局域网或未开代理时智能检测会优先 TCP Dial；若你强制选择 Banner，间隔会自动不低于 15 秒。')}
        </div>
      </div>
      <div>
        <SettingsSectionTitle definition={settingsData.sections.refresh} />
        <div className="text-sm text-secondary mb-2.5">{$t('设置探针数据和延迟测试的自动刷新间隔。延迟检测默认 2 秒（适合 TCP/智能模式）；选择 Banner 时自动不低于 15 秒。')}</div>
        <SettingsPanel className="flex flex-col gap-2.5">
          <div data-settings-field-id={settingsData.fields.probeInterval.id} className={cn('flex justify-between', isMobileLayout ? 'flex-col items-stretch gap-2.5' : 'flex-row items-center')}>
            <span className="text-base text-secondary">{$t('探针刷新间隔')}</span>
            <div className={cn('flex items-center gap-2 flex-wrap', isMobileLayout ? 'justify-start' : 'justify-end')}>
              {[1, 3, 5, 10, 30].map((s) => (
                <button
                  key={s}
                  onClick={() => onProbeIntervalChange(s)}
                  className={cn(
                    'px-3 py-1 rounded-md text-sm font-semibold transition-all duration-[120ms] border cursor-pointer',
                    probeInterval === s ? 'border-success bg-[rgba(var(--success-rgb),0.12)] text-success' : 'border-line bg-sunken text-secondary',
                  )}
                >{s}s</button>
              ))}
            </div>
          </div>
          <div data-settings-field-id={settingsData.fields.pingInterval.id} className={cn('flex justify-between', isMobileLayout ? 'flex-col items-stretch gap-2.5' : 'flex-row items-center')}>
            <span className="text-base text-secondary">{$t('延迟检测间隔')}</span>
            <div className={cn('flex items-center gap-2 flex-wrap', isMobileLayout ? 'justify-start' : 'justify-end')}>
              {[2, 5, 10, 15, 30].map((s) => {
                const disabled = pingMode === 'banner' && s < 15;
                return (
                  <button
                    key={s}
                    onClick={() => !disabled && onPingIntervalChange(s)}
                    disabled={disabled}
                    title={disabled ? $t('Banner 模式下间隔不能低于 15 秒') : undefined}
                    className={cn(
                      'px-3 py-1 rounded-md text-sm font-semibold transition-all duration-[120ms] border',
                      disabled
                        ? 'opacity-45 cursor-not-allowed border-line bg-sunken text-tertiary'
                        : (pingInterval === s
                          ? 'border-success bg-[rgba(var(--success-rgb),0.12)] text-success'
                          : 'border-line bg-sunken text-secondary cursor-pointer'),
                    )}
                  >{s}s</button>
                );
              })}
            </div>
          </div>
          {pingMode === 'banner' ? (
            <div className="text-sm text-tertiary leading-[1.6]">
              {$t('当前为 Banner 模式：延迟检测间隔已限制为至少 15 秒，以降低安全设备将半开 SSH 记为登录失败的概率。')}
            </div>
          ) : null}
        </SettingsPanel>
      </div>
      <div>
        <SettingsSectionTitle definition={settingsData.sections.proxy} />
        <div className="text-sm text-secondary mb-2.5">{$t('添加并管理本地代理节点，可供 AI 请求与服务器 SSH/SFTP 连接复用。')}</div>
        <div data-settings-field-id={settingsData.fields.proxyNodes.id} className={cn('grid gap-3.5 items-start', isMobileLayout ? 'grid-cols-1' : 'grid-cols-[minmax(0,1.1fr)_minmax(320px,0.9fr)]')}>
          <div className="flex flex-col gap-2 min-w-0">
            {proxyNodes.length === 0 ? (
              <div className="px-3.5 py-4 bg-overlay rounded-lg border border-dashed border-line text-tertiary leading-[1.7]">
                <div className="text-base font-semibold text-secondary mb-1">{$t('暂无代理节点')}</div>
                <div className="text-sm">{$t('创建第一个代理节点后会显示在这里。')}</div>
              </div>
            ) : proxyNodes.map((node) => (
              <div key={String(node?.id)} className="p-3 bg-overlay rounded-lg border border-line flex flex-col gap-2 min-w-0">
                <div className={cn('flex justify-between gap-2.5', isMobileLayout ? 'flex-col items-stretch' : 'flex-row items-center')}>
                  <div className="min-w-0">
                    <div className={cn('text-base font-bold text-primary overflow-hidden text-ellipsis [overflow-wrap:anywhere]', isMobileLayout ? 'whitespace-normal' : 'whitespace-nowrap')}>{String(node?.name || $t('未命名节点'))}</div>
                    <div className={cn('text-xs text-tertiary mt-0.5 overflow-hidden text-ellipsis [overflow-wrap:anywhere] leading-[1.6]', isMobileLayout ? 'whitespace-normal' : 'whitespace-nowrap')}>
                      {[
                        node?.type === 'http' ? $t('HTTP 代理') : $t('SOCKS5 代理'),
                        `${String(node?.host)}:${String(node?.port)}`,
                        ...(node?.username ? [`${$t('用户名')}: ${String(node?.username)}`] : []),
                        ...(node?.password ? [`${$t('密码')}: ••••••`] : []),
                      ].join(' · ')}
                    </div>
                  </div>
                  <div className={cn('flex gap-2 shrink-0 flex-wrap', isMobileLayout ? 'justify-start' : 'justify-end')}>
                    <Button size="sm" onClick={() => handleProxyEdit(node)}>{$t('编辑')}</Button>
                    <Button size="sm" className="text-danger hover:text-danger" onClick={() => handleProxyDelete(String(node?.id))}>{$t('删除')}</Button>
                  </div>
                </div>
              </div>
            ))}
          </div>
          <SettingsPanel className="p-3">
            <form onSubmit={handleProxySubmit} className="flex flex-col gap-2.5 min-w-0">
              <div className="text-base font-bold text-primary">{editingProxyId ? $t('编辑') : $t('添加')}</div>
              <div className="form-group" data-settings-field-id={settingsData.fields.proxyName.id}>
                <label className="form-label" htmlFor="network-proxy-name">{$t('代理名称（备注）')}</label>
                <input id="network-proxy-name" name="network-proxy-name" autoComplete="off" className="input" value={proxyForm.name} onChange={setProxyField('name')} placeholder={$t('代理名称（备注）')} />
                <div className="mt-1.5 text-xs text-tertiary">{$t('仅用于区分代理节点，不参与连接逻辑')}</div>
              </div>
              <div className="form-group" data-settings-field-id={settingsData.fields.proxyType.id}>
                <label className="form-label" htmlFor="network-proxy-type">{$t('协议类型')}</label>
                <Select
                  id="network-proxy-type"
                  name="network-proxy-type"
                  value={proxyForm.type}
                  onChange={(val) => setProxyForm((current) => ({ ...current, type: val }))}
                  options={[
                    { value: 'socks5', label: $t('SOCKS5 代理') },
                    { value: 'http', label: $t('HTTP 代理') },
                  ]}
                />
              </div>
              <div className="form-group" data-settings-field-id={settingsData.fields.proxyHost.id}>
                <label className="form-label" htmlFor="network-proxy-host">{$t('主机地址')}</label>
                <input id="network-proxy-host" name="network-proxy-host" autoComplete="off" className="input" value={proxyForm.host} onChange={setProxyField('host')} placeholder="127.0.0.1" />
              </div>
              <div className="form-group" data-settings-field-id={settingsData.fields.proxyPort.id}>
                <label className="form-label" htmlFor="network-proxy-port">{$t('端口')}</label>
                <input id="network-proxy-port" name="network-proxy-port" autoComplete="off" className="input" type="number" min={1} max={65535} value={proxyForm.port} onChange={setProxyField('port')} placeholder="1080" />
              </div>
              <div className="form-group" data-settings-field-id={settingsData.fields.proxyUsername.id}>
                <label className="form-label" htmlFor="network-proxy-username">{$t('用户名')}</label>
                <input id="network-proxy-username" name="network-proxy-username" autoComplete="off" className="input" value={proxyForm.username} onChange={setProxyField('username')} placeholder={$t('用户名')} />
              </div>
              <div className="form-group" data-settings-field-id={settingsData.fields.proxyPassword.id}>
                <label className="form-label" htmlFor="network-proxy-password">{$t('密码')}</label>
                <input id="network-proxy-password" name="network-proxy-password" autoComplete="off" className="input" type="password" value={proxyForm.password} onChange={setProxyField('password')} placeholder={$t('密码')} />
              </div>
              <div className="flex gap-2.5 justify-end mt-0.5 flex-wrap">
                {editingProxyId ? <Button onClick={resetProxyForm}>{$t('取消编辑')}</Button> : null}
                <Button type="submit" variant="primary">{editingProxyId ? $t('保存配置') : $t('添加')}</Button>
              </div>
            </form>
          </SettingsPanel>
        </div>
      </div>
    </SettingsTabRoot>
  );
}
