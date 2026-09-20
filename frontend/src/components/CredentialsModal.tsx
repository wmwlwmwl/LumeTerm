import { useEffect, useState } from 'react';
import { Plus, Pencil, Trash2, Key, Lock, Eye, EyeOff } from 'lucide-react';
import * as AppGo from '../../wailsjs/go/wailsapp/App.js';
import type { config } from '../../wailsjs/go/models.ts';
import { useTranslation } from '../i18n.ts';
import { warnDev } from '../utils/devLog';
import Tiptop from './Tiptop.tsx';
import { Button, Modal, Select } from './ui';

/** 凭据表单（保存时补齐 id 即为 config.Credential） */
interface CredentialForm {
  name: string;
  authMethod: string;
  username: string;
  password: string;
  privateKey: string;
  passphrase: string;
}

const defaultCredForm: CredentialForm = {
  name: '',
  authMethod: 'password',
  username: 'root',
  password: '',
  privateKey: '',
  passphrase: '',
};

interface CredentialsModalProps {
  onClose: () => void;
  onChange?: () => void;
  addToast: (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;
}

export default function CredentialsModal({ onClose, onChange, addToast }: CredentialsModalProps) {
  const { t } = useTranslation();
  const [credentials, setCredentials] = useState<config.Credential[]>([]);
  const [editing, setEditing] = useState<string | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState<CredentialForm>(defaultCredForm);
  const [showPassword, setShowPassword] = useState(false);
  const [showPassphrase, setShowPassphrase] = useState(false);
  const [saving, setSaving] = useState(false);

  const loadCredentials = async (signal?: { cancelled: boolean }) => {
    try {
      const list = await AppGo.GetCredentials();
      if (signal?.cancelled) return;
      setCredentials(list || []);
    } catch (e) {
      if (signal?.cancelled) return;
      warnDev('Failed to load credentials:', e);
    }
  };

  useEffect(() => {
    const signal = { cancelled: false };
    void loadCredentials(signal);
    return () => { signal.cancelled = true; };
  }, []);

  const set = (key: keyof CredentialForm) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => setForm((f) => ({ ...f, [key]: e.target.value }));

  const closeForm = () => {
    setEditing(null);
    setShowForm(false);
    setForm(defaultCredForm);
    setShowPassword(false);
    setShowPassphrase(false);
  };

  const startCreate = () => {
    setEditing(null);
    setShowForm(true);
    setForm(defaultCredForm);
    setShowPassword(false);
    setShowPassphrase(false);
  };

  const startEdit = (cred: config.Credential) => {
    setEditing(cred.id);
    setShowForm(true);
    setForm({
      name: cred.name || '',
      authMethod: cred.authMethod || 'password',
      username: cred.username || 'root',
      password: '',
      privateKey: '',
      passphrase: '',
    });
    setShowPassword(false);
    setShowPassphrase(false);
  };

  const handleSave = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!form.name.trim()) return window.lumeDialog?.alert(t('凭据名称'));
    if (!form.username.trim()) return window.lumeDialog?.alert(t('请填写用户名'));
    setSaving(true);
    try {
      // 新增时无 id，保存参数允许缺省；断言为 Credential 便于调 Go 侧类型
      const data = { ...form, ...(editing ? { id: editing } : {}) } as config.Credential;
      await AppGo.SaveCredential(data);
      await loadCredentials();
      addToast(t('凭据已保存'), 'success');
      closeForm();
      onChange?.();
    } catch (err) {
      window.lumeDialog?.alert(String(err));
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (cred: config.Credential) => {
    const ok = await window.lumeDialog?.confirm(t('确定删除此凭据？'));
    if (!ok) return;
    try {
      await AppGo.DeleteCredential(cred.id);
      await loadCredentials();
      addToast(t('凭据已删除'), 'success');
      if (editing === cred.id) closeForm();
      onChange?.();
    } catch (err) {
      window.lumeDialog?.alert(String(err));
    }
  };

  const isEditing = editing !== null;

  return (
    <>
      <Modal open onClose={showForm ? closeForm : onClose} title={t('凭据管理')} size="md">
      <div className="flex flex-col gap-2.5 max-h-[calc(80vh-120px)] overflow-y-auto">
        {credentials.length === 0 ? (
          <div className="text-center pt-7 pb-3 text-tertiary text-base">
            {t('暂无凭据')}
          </div>
        ) : (
          credentials.map((cred) => (
            <div
              key={cred.id}
              className={`flex items-center gap-2.5 py-2.5 px-3 rounded-md border border-line ${editing === cred.id ? 'bg-accent-dim' : 'bg-sunken'}`}
            >
              <div className={cred.authMethod === 'privateKey' ? 'text-warning shrink-0' : 'text-accent shrink-0'}>
                {cred.authMethod === 'privateKey' ? <Key size={16} /> : <Lock size={16} />}
              </div>
              <div className="flex-1 min-w-0">
                <div className="text-md font-semibold text-primary">{cred.name}</div>
                <div className="text-sm text-tertiary">
                  {cred.username} · {cred.authMethod === 'privateKey' ? t('私钥认证') : t('密码认证')}
                </div>
              </div>
              <Tiptop text={t('编辑凭据')}>
                <Button variant="ghost" size="icon" onClick={() => startEdit(cred)} aria-label={t('编辑凭据')}>
                  <Pencil size={14} />
                </Button>
              </Tiptop>
              <Tiptop text={t('删除凭据')}>
                <button
                  type="button"
                  onClick={() => handleDelete(cred)}
                  aria-label={t('删除凭据')}
                  className="inline-flex items-center justify-center w-[26px] min-w-[26px] h-[26px] p-0 rounded-sm bg-transparent border border-transparent text-danger cursor-pointer outline-none transition-colors duration-[80ms] hover:bg-hover"
                >
                  <Trash2 size={14} />
                </button>
              </Tiptop>
            </div>
          ))
        )}

        <Button variant="secondary" block onClick={startCreate}>
          <Plus size={16} /> {t('新增凭据')}
        </Button>
      </div>
      </Modal>

      {showForm && (
        <Modal
          open
          onClose={closeForm}
          title={isEditing ? t('编辑凭据') : t('新增凭据')}
          size="sm"
          closeOnEscape={false}
          footer={
            <>
              <Button variant="secondary" onClick={closeForm} disabled={saving}>
                {t('取消')}
              </Button>
              <Button type="submit" form="credential-form" variant="primary" disabled={saving}>
                {isEditing ? t('保存') : t('新增凭据')}
              </Button>
            </>
          }
        >
          <form id="credential-form" onSubmit={handleSave} className="flex flex-col gap-3">
            <div className="form-group">
              <label className="form-label" htmlFor="cred-name">{t('凭据名称')} *</label>
              <input className="input" id="cred-name" name="cred-name" autoComplete="off" value={form.name} onChange={set('name')} placeholder={t('凭据名称')} autoFocus />
            </div>

            <div className="form-group">
              <label className="form-label" htmlFor="cred-auth-method">{t('认证方式')}</label>
              <Select
                id="cred-auth-method"
                name="cred-auth-method"
                value={form.authMethod}
                onChange={(val) => setForm((f) => ({
                  ...f,
                  authMethod: val,
                  password: '',
                  privateKey: '',
                  passphrase: '',
                }))}
                options={[
                  { value: 'password', label: t('密码认证') },
                  { value: 'privateKey', label: t('私钥认证') },
                ]}
              />
            </div>

            <div className="form-group">
              <label className="form-label" htmlFor="cred-username">{t('用户名')} *</label>
              <input className="input" id="cred-username" name="cred-username" autoComplete="off" value={form.username} onChange={set('username')} placeholder="root" />
            </div>

            {form.authMethod === 'password' ? (
              <div className="form-group">
                <label className="form-label" htmlFor="cred-password">{t('密码')}</label>
                <div className="relative">
                  <input
                    className="input"
                    id="cred-password"
                    name="cred-password"
                    autoComplete="current-password"
                    type={showPassword ? 'text' : 'password'}
                    value={form.password}
                    onChange={set('password')}
                    placeholder={isEditing ? t('留空不修改') : t('密码')}
                    style={{ paddingRight: 36 }}
                  />
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => setShowPassword(!showPassword)}
                    className="absolute right-1 top-1/2 -translate-y-1/2"
                    aria-label={showPassword ? t('隐藏密码') : t('显示密码')}
                  >
                    {showPassword ? <EyeOff size={14} /> : <Eye size={14} />}
                  </Button>
                </div>
              </div>
            ) : (
              <>
                <div className="form-group">
                  <label className="form-label" htmlFor="cred-private-key">{t('私钥')}</label>
                  <textarea
                    id="cred-private-key"
                    name="cred-private-key"
                    className="input resize-y font-mono"
                    rows={4}
                    value={form.privateKey}
                    onChange={set('privateKey')}
                    placeholder={isEditing ? t('留空不修改') : '-----BEGIN RSA PRIVATE KEY-----...'}
                    style={{ fontSize: 12 }}
                  />
                </div>
                <div className="form-group">
                  <label className="form-label" htmlFor="cred-passphrase">{t('私钥密码短语')}</label>
                  <div className="relative">
                    <input
                      className="input"
                      id="cred-passphrase"
                      name="cred-passphrase"
                      autoComplete="current-password"
                      type={showPassphrase ? 'text' : 'password'}
                      value={form.passphrase}
                      onChange={set('passphrase')}
                      placeholder={isEditing ? t('留空不修改') : t('私钥密码短语')}
                      style={{ paddingRight: 36 }}
                    />
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => setShowPassphrase(!showPassphrase)}
                      className="absolute right-1 top-1/2 -translate-y-1/2"
                      aria-label={showPassphrase ? t('隐藏密码') : t('显示密码')}
                    >
                      {showPassphrase ? <EyeOff size={14} /> : <Eye size={14} />}
                    </Button>
                  </div>
                </div>
              </>
            )}
          </form>
        </Modal>
      )}
    </>
  );
}
