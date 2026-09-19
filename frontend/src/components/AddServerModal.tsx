import { Check, Plus, SquarePen } from 'lucide-react';
import type { config } from '../../wailsjs/go/models.ts';
import type { ServerFormData } from '../hooks/useServerCatalog.ts';
import { useTranslation } from '../i18n.ts';
import { Button, Modal } from './ui';
import { AddServerBasicSection } from './serverModal/AddServerBasicSection.tsx';
import { AddServerAuthSection } from './serverModal/AddServerAuthSection.tsx';
import { AddServerProxySection } from './serverModal/AddServerProxySection.tsx';
import { AddServerAdvancedSection } from './serverModal/AddServerAdvancedSection.tsx';
import { useAddServerForm } from './serverModal/useAddServerForm.ts';

export interface AddServerModalProps {
  server: (config.Connection & { authType?: string }) | null;
  onSave: (data: ServerFormData, shouldClearAfterAdd?: boolean) => Promise<config.Connection | null>;
  onSaveAndConnect?: (data: ServerFormData, shouldClearAfterAdd?: boolean) => Promise<config.Connection | null>;
  onClose: () => void;
  allGroups?: string[];
  credentials?: config.Credential[];
  onOpenCredentials?: () => void;
  inline?: boolean;
  shiningFields?: Record<string, unknown>;
}

export default function AddServerModal({
  server,
  onSave,
  onSaveAndConnect,
  onClose,
  allGroups = [],
  credentials = [],
  onOpenCredentials,
  inline = false,
  shiningFields = {},
}: AddServerModalProps) {
  const { t } = useTranslation();

  const {
    form,
    setForm,
    set,
    saving,
    showPassword,
    setShowPassword,
    showPassphrase,
    setShowPassphrase,
    showProxyPassword,
    setShowProxyPassword,
    proxyNodes,
    authMode,
    setAuthMode,
    selectedCredId,
    setSelectedCredId,
    clearAfterAdd,
    setClearAfterAdd,
    isEditing,
    submitForm,
    handleSubmit,
    handleSelectPrivateKeyFile,
    handleCancel,
  } = useAddServerForm({
    server,
    onSave,
    onSaveAndConnect,
    onClose,
    inline,
  });

  const inputClass = (key: string) => `input${shiningFields?.[key] ? ' editor-field-shine' : ''}`;
  const inputShellClass = (key: string) => `editor-field-shell${shiningFields?.[key] ? ' editor-field-shell-shine' : ''}`;

  const sections = (
    <>
      <AddServerBasicSection
        form={form}
        set={set}
        allGroups={allGroups}
        inputClass={inputClass}
        inputShellClass={inputShellClass}
      />
      <AddServerAuthSection
        form={form}
        set={set}
        authMode={authMode}
        setAuthMode={setAuthMode}
        selectedCredId={selectedCredId}
        setSelectedCredId={setSelectedCredId}
        credentials={credentials}
        onOpenCredentials={onOpenCredentials}
        isEditing={isEditing}
        showPassword={showPassword}
        setShowPassword={setShowPassword}
        showPassphrase={showPassphrase}
        setShowPassphrase={setShowPassphrase}
        handleSelectPrivateKeyFile={handleSelectPrivateKeyFile}
      />
      <AddServerProxySection
        form={form}
        set={set}
        proxyNodes={proxyNodes}
        showProxyPassword={showProxyPassword}
        setShowProxyPassword={setShowProxyPassword}
      />
      <AddServerAdvancedSection
        form={form}
        set={set}
        setForm={setForm}
        inputClass={inputClass}
        inputShellClass={inputShellClass}
      />
    </>
  );

  const footerButtons = isEditing ? (
    <>
      <Button variant="secondary" onClick={handleCancel}>
        {t('取消')}
      </Button>
      <Button variant="primary" disabled={saving} onClick={() => void submitForm('save')}>
        {saving ? t('保存中...') : t('保存配置')}
      </Button>
    </>
  ) : (
    <>
      <button
        type="button"
        role="checkbox"
        aria-checked={clearAfterAdd}
        title={t('添加成功后清空表单，方便连续添加多台服务器')}
        onClick={() => setClearAfterAdd((current) => !current)}
        className="server-editor-clear-check"
      >
        <span className={'custom-checkbox' + (clearAfterAdd ? ' checked' : '')}>
          {clearAfterAdd && <Check size={10} strokeWidth={4} />}
        </span>
        {t('添加后清空')}
      </button>
      {server && (
        <Button variant="secondary" onClick={handleCancel}>
          {t('取消')}
        </Button>
      )}
      <Button data-submit-action="save" disabled={saving} onClick={() => void submitForm('save')}>
        {saving ? t('保存中...') : t('添加')}
      </Button>
      <Button data-submit-action="connect" variant="primary" disabled={saving} onClick={() => void submitForm('connect')}>
        {saving ? t('保存中...') : t('添加并链接')}
      </Button>
    </>
  );

  if (inline) {
    return (
      <div className="glass-card dashboard-server-editor">
        <div className="dashboard-server-editor-shell">
          <div className="dashboard-server-editor-header shrink-0">
            <div className="dashboard-server-editor-title">
              <span className="inline-flex items-center dashboard-server-editor-title-icon" data-editor-add-target={!isEditing ? 'true' : undefined}>
                {isEditing ? <SquarePen size={16} /> : <Plus size={16} />}
              </span>
              {isEditing ? t('编辑配置') : t('添加')}
            </div>
          </div>
          <form onSubmit={handleSubmit} className="dashboard-server-editor-form flex" style={{ flexDirection: 'column', flex: 1, minHeight: 0, overflow: 'hidden' }}>
            <div className="dashboard-server-editor-body" style={{ flex: 1, minHeight: 0, overflowY: 'auto' }}>
              {sections}
            </div>
            <div className="dashboard-server-editor-footer shrink-0">
              {footerButtons}
            </div>
          </form>
        </div>
      </div>
    );
  }

  return (
    <Modal
      open
      onClose={onClose}
      size="xl"
      closeOnOverlay={false}
      closeOnEscape
      panelClassName="flex flex-col self-start mt-14 h-[calc(100vh-72px)] max-h-[calc(100vh-72px)]!"
      bodyClassName="flex-1 min-h-0 overflow-y-auto"
      icon={(
        <span className="inline-flex items-center shrink-0" data-editor-add-target={!isEditing ? 'true' : undefined}>
          {isEditing ? <SquarePen size={16} /> : <Plus size={16} />}
        </span>
      )}
      title={isEditing ? t('编辑配置') : t('添加')}
      footer={footerButtons}
    >
      <form onSubmit={handleSubmit} className="flex flex-col gap-3">
        {sections}
      </form>
    </Modal>
  );
}
