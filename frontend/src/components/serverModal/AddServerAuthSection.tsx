import { Eye, EyeOff, FolderOpen, Key, KeyRound } from 'lucide-react';
import type { ChangeEvent } from 'react';
import type { config } from '../../../wailsjs/go/models.ts';
import { useTranslation } from '../../i18n.ts';
import { cn } from '../../utils/cn.ts';
import { Button, Select } from '../ui';
import type { ServerEditorForm } from './serverModalTypes.ts';

export interface AddServerAuthSectionProps {
  form: ServerEditorForm;
  set: (key: string) => (e: ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => void;
  authMode: string;
  setAuthMode: (mode: string) => void;
  selectedCredId: string;
  setSelectedCredId: (id: string) => void;
  credentials?: config.Credential[];
  onOpenCredentials?: () => void;
  isEditing: boolean;
  showPassword: boolean;
  setShowPassword: (show: boolean) => void;
  showPassphrase: boolean;
  setShowPassphrase: (show: boolean) => void;
  handleSelectPrivateKeyFile: () => Promise<void>;
}

export function AddServerAuthSection({
  form,
  set,
  authMode,
  setAuthMode,
  selectedCredId,
  setSelectedCredId,
  credentials = [],
  onOpenCredentials,
  isEditing,
  showPassword,
  setShowPassword,
  showPassphrase,
  setShowPassphrase,
  handleSelectPrivateKeyFile,
}: AddServerAuthSectionProps) {
  const { t } = useTranslation();

  return (
    <div className="webdav-section server-editor-section">
      <div className="webdav-section-title server-editor-section-title">
        <span className="server-editor-section-icon"><Key size={15} /></span> {t('认证方式')}
      </div>
      <div className="server-editor-fields">
        <div className="server-editor-auth-row">
          <div className="inline-flex h-8.5 flex-1 items-center gap-0.5 rounded-[var(--radius-sm)] border border-line-subtle bg-sunken p-0.5">
            <button
              type="button"
              className={cn(
                'flex-1 inline-flex h-7 items-center justify-center rounded-[6px] text-xs font-medium transition-colors',
                authMode === 'custom'
                  ? 'border border-accent-border bg-accent-dim text-accent font-semibold shadow-xs'
                  : 'border border-transparent text-secondary hover:text-primary hover:bg-hover/60',
              )}
              onClick={() => setAuthMode('custom')}
            >
              {t('自定义')}
            </button>
            <button
              type="button"
              className={cn(
                'flex-1 inline-flex h-7 items-center justify-center rounded-[6px] text-xs font-medium transition-colors',
                authMode === 'credential'
                  ? 'border border-accent-border bg-accent-dim text-accent font-semibold shadow-xs'
                  : 'border border-transparent text-secondary hover:text-primary hover:bg-hover/60',
              )}
              onClick={() => setAuthMode('credential')}
            >
              {t('使用凭据')}
            </button>
          </div>
          <button
            type="button"
            className="inline-flex h-8.5 shrink-0 items-center justify-center gap-1.5 rounded-[var(--radius-sm)] border border-line-subtle bg-sunken px-3 text-xs font-medium text-secondary hover:bg-hover hover:text-primary hover:border-line transition-colors"
            onClick={onOpenCredentials}
          >
            <KeyRound size={13} /> {t('凭据管理')}
          </button>
        </div>

        {authMode === 'credential' ? (
          credentials.length === 0 ? (
            <div className="server-editor-empty">
              {t('暂无凭据，请先创建')}
            </div>
          ) : (
            <>
              <div className="form-group">
                <label className="form-label" htmlFor="server-credential">{t('选择凭据')} *</label>
                <Select
                  id="server-credential"
                  name="credentialId"
                  value={selectedCredId}
                  onChange={(val) => setSelectedCredId(val)}
                  placeholder={t('请选择凭据')}
                  options={[
                    { value: '', label: t('请选择凭据') },
                    ...credentials.map((c) => ({
                      value: c.id,
                      label: `${c.name} (${c.username})`,
                    })),
                  ]}
                />
              </div>
              {selectedCredId && (() => {
                const sel = credentials.find((c) => c.id === selectedCredId);
                if (!sel) return null;
                return (
                  <div className="server-editor-credential-summary">
                    {sel.authMethod === 'privateKey' ? t('私钥认证') : t('密码认证')} · {sel.username}
                  </div>
                );
              })()}
            </>
          )
        ) : (
          <>
            <div className="form-group">
              <label className="form-label" htmlFor="server-auth-type">{t('认证方式')}</label>
              <Select
                id="server-auth-type"
                name="authType"
                value={form.authType}
                onChange={(val) => set('authType')({ target: { value: val } } as ChangeEvent<HTMLSelectElement>)}
                options={[
                  { value: 'password', label: t('密码认证') },
                  { value: 'key', label: t('私钥认证') },
                ]}
              />
            </div>

            {form.authType === 'password' ? (
              <div className="form-group">
                <label className="form-label" htmlFor="server-password">
                  {isEditing ? `${t('密码')}（${t('留空不修改')}）` : t('密码')} *
                </label>
                <div className="relative">
                  <input
                    id="server-password"
                    name="password"
                    autoComplete={isEditing ? 'new-password' : 'current-password'}
                    className="input"
                    type={showPassword ? 'text' : 'password'}
                    placeholder={t('请输入密码')}
                    value={form.password}
                    onChange={set('password')}
                    style={{ paddingRight: 36 }}
                  />
                  <button type="button" onClick={() => setShowPassword(!showPassword)} className="absolute right-2 top-1/2 -translate-y-1/2 bg-transparent border-0 text-tertiary cursor-pointer p-1 flex items-center">
                    {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                  </button>
                </div>
              </div>
            ) : (
              <>
                <div className="form-group">
                  <div className="flex justify-between items-center mb-1">
                    <label className="form-label" htmlFor="server-private-key">{t('私钥内容')}</label>
                    <Button variant="secondary" size="sm" className="server-editor-browse" onClick={() => void handleSelectPrivateKeyFile()}>
                      <FolderOpen size={12} className="inline-block align-middle mr-0.5" /> {t('浏览')}
                    </Button>
                  </div>
                  <textarea
                    id="server-private-key"
                    name="privateKey"
                    className="input"
                    style={{
                      fontFamily: 'var(--font-mono)',
                      fontSize: 12,
                      resize: 'vertical',
                      minHeight: 100,
                    }}
                    placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;...&#10;-----END OPENSSH PRIVATE KEY-----"
                    value={form.privateKey}
                    onChange={set('privateKey')}
                  />
                </div>
                <div className="form-group">
                  <label className="form-label" htmlFor="server-passphrase">{t('私钥密码短语 (可选)')}</label>
                  <div className="relative">
                    <input
                      id="server-passphrase"
                      name="passphrase"
                      className="input"
                      type={showPassphrase ? 'text' : 'password'}
                      placeholder={t('私钥密码短语')}
                      value={form.passphrase}
                      onChange={set('passphrase')}
                      style={{ paddingRight: 36 }}
                    />
                    <button type="button" onClick={() => setShowPassphrase(!showPassphrase)} className="absolute right-2 top-1/2 -translate-y-1/2 bg-transparent border-0 text-tertiary cursor-pointer p-1 flex items-center">
                      {showPassphrase ? <EyeOff size={16} /> : <Eye size={16} />}
                    </button>
                  </div>
                </div>
              </>
            )}
          </>
        )}
      </div>
    </div>
  );
}
