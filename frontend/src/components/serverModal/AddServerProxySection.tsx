import { Eye, EyeOff, Globe } from 'lucide-react';
import type { ChangeEvent } from 'react';
import { useTranslation } from '../../i18n.ts';
import { cn } from '../../utils/cn.ts';
import { Select } from '../ui';
import type { ProxyNode, ServerEditorForm } from './serverModalTypes.ts';

export interface AddServerProxySectionProps {
  form: ServerEditorForm;
  set: (key: string) => (e: ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => void;
  proxyNodes: ProxyNode[];
  showProxyPassword: boolean;
  setShowProxyPassword: (show: boolean) => void;
}

export function AddServerProxySection({
  form,
  set,
  proxyNodes,
  showProxyPassword,
  setShowProxyPassword,
}: AddServerProxySectionProps) {
  const { t } = useTranslation();

  return (
    <div className="webdav-section server-editor-section">
      <div className="webdav-section-title server-editor-section-title">
        <span className="server-editor-section-icon"><Globe size={15} /></span> {t('代理服务器')}
      </div>
      <div className="server-editor-fields">
        <div className="form-group">
          <span id="proxy-mode-label" className="form-label">{t('代理模式')}</span>
          <div
            role="radiogroup"
            aria-labelledby="proxy-mode-label"
            className="inline-flex h-8.5 w-full items-center gap-0.5 rounded-[var(--radius-sm)] border border-line-subtle bg-sunken p-0.5"
          >
            <button
              type="button"
              role="radio"
              aria-checked={!form.proxyMode || form.proxyMode === 'direct'}
              className={cn(
                'flex-1 inline-flex h-7 items-center justify-center rounded-[6px] text-xs font-medium transition-colors',
                (!form.proxyMode || form.proxyMode === 'direct')
                  ? 'border border-accent-border bg-accent-dim text-accent font-semibold shadow-xs'
                  : 'border border-transparent text-secondary hover:text-primary hover:bg-hover/60',
              )}
              onClick={() => set('proxyMode')({ target: { value: 'direct', name: 'proxyMode' } } as unknown as ChangeEvent<HTMLSelectElement>)}
            >
              {t('直连')}
            </button>
            <button
              type="button"
              role="radio"
              aria-checked={form.proxyMode === 'node'}
              className={cn(
                'flex-1 inline-flex h-7 items-center justify-center rounded-[6px] text-xs font-medium transition-colors',
                form.proxyMode === 'node'
                  ? 'border border-accent-border bg-accent-dim text-accent font-semibold shadow-xs'
                  : 'border border-transparent text-secondary hover:text-primary hover:bg-hover/60',
              )}
              onClick={() => set('proxyMode')({ target: { value: 'node', name: 'proxyMode' } } as unknown as ChangeEvent<HTMLSelectElement>)}
            >
              {t('代理节点')}
            </button>
            <button
              type="button"
              role="radio"
              aria-checked={form.proxyMode === 'custom'}
              className={cn(
                'flex-1 inline-flex h-7 items-center justify-center rounded-[6px] text-xs font-medium transition-colors',
                form.proxyMode === 'custom'
                  ? 'border border-accent-border bg-accent-dim text-accent font-semibold shadow-xs'
                  : 'border border-transparent text-secondary hover:text-primary hover:bg-hover/60',
              )}
              onClick={() => set('proxyMode')({ target: { value: 'custom', name: 'proxyMode' } } as unknown as ChangeEvent<HTMLSelectElement>)}
            >
              {t('自定义代理')}
            </button>
          </div>
        </div>
        {form.proxyMode === 'node' ? (
          proxyNodes.length === 0 ? (
            <div className="server-editor-empty">
              {t('暂无代理节点，请先到设置中创建')}
            </div>
          ) : (
            <div className="form-group">
              <label className="form-label" htmlFor="server-proxy-node">{t('代理节点')} *</label>
              <Select
                id="server-proxy-node"
                name="proxyNodeId"
                value={form.proxyNodeId || ''}
                onChange={(val) => set('proxyNodeId')({ target: { value: val } } as ChangeEvent<HTMLSelectElement>)}
                placeholder={t('请选择代理节点')}
                options={[
                  { value: '', label: t('请选择代理节点') },
                  ...proxyNodes.map((node) => ({
                    value: node.id || '',
                    label: [
                      node.name || t('未命名节点'),
                      node.type === 'https' ? t('HTTPS 代理') : node.type === 'http' ? t('HTTP 代理') : t('SOCKS5 代理'),
                      `${node.host}:${node.port}`,
                    ].join(' · '),
                  })),
                ]}
              />
            </div>
          )
        ) : null}
        {form.proxyMode === 'custom' ? (
          <>
            <div className="form-group">
              <label className="form-label" htmlFor="server-proxy-type">{t('协议类型')}</label>
              <Select
                id="server-proxy-type"
                name="proxyType"
                value={form.proxyType || 'socks5'}
                onChange={(val) => set('proxyType')({ target: { value: val } } as ChangeEvent<HTMLSelectElement>)}
                options={[
                  { value: 'socks5', label: t('SOCKS5 代理') },
                  { value: 'http', label: t('HTTP 代理') },
                  { value: 'https', label: t('HTTPS 代理') },
                ]}
              />
            </div>
            <div className="form-row">
              <div className="form-group">
                <label className="form-label" htmlFor="server-proxy-host">{t('代理主机')} *</label>
                <input
                  id="server-proxy-host"
                  name="proxyHost"
                  className="input"
                  placeholder="127.0.0.1"
                  value={form.proxyHost || ''}
                  onChange={set('proxyHost')}
                />
              </div>
              <div className="form-group">
                <label className="form-label" htmlFor="server-proxy-port">{t('代理端口')}</label>
                <input
                  id="server-proxy-port"
                  name="proxyPort"
                  className="input"
                  type="number"
                  min={1}
                  max={65535}
                  placeholder="1080"
                  value={form.proxyPort || ''}
                  onChange={set('proxyPort')}
                />
              </div>
            </div>
            <div className="form-group">
              <label className="form-label" htmlFor="server-proxy-username">{t('代理用户名')}</label>
              <input
                id="server-proxy-username"
                name="proxyUsername"
                className="input"
                placeholder="user"
                value={form.proxyUsername || ''}
                onChange={set('proxyUsername')}
              />
            </div>
            <div className="form-group">
              <label className="form-label" htmlFor="server-proxy-password">{t('代理密码')}</label>
              <div className="relative">
                <input
                  id="server-proxy-password"
                  name="proxyPassword"
                  className="input"
                  type={showProxyPassword ? 'text' : 'password'}
                  placeholder={t('代理密码')}
                  value={form.proxyPassword || ''}
                  onChange={set('proxyPassword')}
                  style={{ paddingRight: 36 }}
                />
                <button type="button" onClick={() => setShowProxyPassword(!showProxyPassword)} className="absolute right-2 top-1/2 -translate-y-1/2 bg-transparent border-0 text-tertiary cursor-pointer p-1 flex items-center">
                  {showProxyPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                </button>
              </div>
            </div>
            {form.proxyType === 'https' ? (
              <label className="server-editor-compat-check" htmlFor="server-proxy-insecure-tls">
                <input
                  id="server-proxy-insecure-tls"
                  name="proxyAllowInsecureTLS"
                  type="checkbox"
                  checked={!!form.proxyAllowInsecureTLS}
                  onChange={(e) => set('proxyAllowInsecureTLS')({ target: { value: e.target.checked } } as unknown as ChangeEvent<HTMLInputElement>)}
                />
                <span>
                  <strong>{t('允许自签证书')}</strong>
                  <small>{t('仅跳过代理证书校验，隧道内目标服务器证书仍正常校验')}</small>
                </span>
              </label>
            ) : null}
          </>
        ) : null}
      </div>
    </div>
  );
}
