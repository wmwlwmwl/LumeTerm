import { useEffect, useRef, useState, type ChangeEvent, type FormEvent, type MouseEvent as ReactMouseEvent } from 'react';
import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';
import type { config } from '../../../wailsjs/go/models.ts';
import { useTranslation } from '../../i18n.ts';
import { getAIGlobalSettings } from '../ai/aiGlobalSettingsBridge.ts';
import type { ServerFormData } from '../../hooks/useServerCatalog.ts';
import {
  defaultForm,
  PROXY_NODES_CHANGED_EVENT,
  type ProxyNode,
  type ServerEditorForm,
} from './serverModalTypes.ts';

export interface UseAddServerFormOptions {
  server: (config.Connection & { authType?: string }) | null;
  onSave: (data: ServerFormData, shouldClearAfterAdd?: boolean) => Promise<config.Connection | null>;
  onSaveAndConnect?: (data: ServerFormData, shouldClearAfterAdd?: boolean) => Promise<config.Connection | null>;
  onClose: () => void;
  inline?: boolean;
}

export function useAddServerForm({
  server,
  onSave,
  onSaveAndConnect,
  onClose,
  inline = false,
}: UseAddServerFormOptions) {
  const { t } = useTranslation();
  const [form, setForm] = useState<ServerEditorForm>(defaultForm);
  const [saving, setSaving] = useState(false);

  const [showPassword, setShowPassword] = useState(false);
  const [showPassphrase, setShowPassphrase] = useState(false);
  const [showProxyPassword, setShowProxyPassword] = useState(false);
  const [proxyNodes, setProxyNodes] = useState<ProxyNode[]>([]);

  const [authMode, setAuthMode] = useState('custom');
  const [selectedCredId, setSelectedCredId] = useState('');
  const [clearAfterAdd, setClearAfterAdd] = useState(true);

  const isEditing = !!server?.id;
  const suppressSubmitUntilRef = useRef(0);

  const resetInlineForm = () => {
    setAuthMode('custom');
    setSelectedCredId('');
    setForm(defaultForm);
    setShowPassword(false);
    setShowPassphrase(false);
    setShowProxyPassword(false);
  };

  useEffect(() => {
    if (server) {
      const useCred = !!server.credentialId;
      setAuthMode(useCred ? 'credential' : 'custom');
      setSelectedCredId(useCred && server.credentialId ? server.credentialId : '');
      setForm({
        ...defaultForm,
        ...server,
        port: server.port ? String(server.port) : '',
        authType: server.authMethod ? (server.authMethod === 'privateKey' ? 'key' : 'password') : (server.authType || 'password'),
        password: '',
        passphrase: server.passphrase || '',
        proxyMode: server.proxyMode || 'direct',
        proxyNodeId: server.proxyNodeId || '',
        proxyType: server.proxyType || 'socks5',
        proxyHost: server.proxyHost || '',
        proxyPort: server.proxyPort ? String(server.proxyPort) : '1080',
        proxyUsername: server.proxyUsername || '',
        proxyPassword: '',
        terminalEncoding: server.terminalEncoding || 'utf-8',
      });
      setShowProxyPassword(false);
    } else {
      resetInlineForm();
    }
  }, [server]);

  useEffect(() => {
    let cancelled = false;
    const loadProxyNodes = () => {
      getAIGlobalSettings()
        .then((settings) => {
          if (cancelled) return;
          setProxyNodes(Array.isArray(settings?.proxyNodes) ? settings.proxyNodes : []);
        })
        .catch(() => {
          if (cancelled) return;
          setProxyNodes([]);
        });
    };
    const handleProxyNodesChanged = (event: Event) => {
      setProxyNodes(Array.isArray((event as CustomEvent)?.detail) ? (event as CustomEvent).detail : []);
    };
    loadProxyNodes();
    window.addEventListener(PROXY_NODES_CHANGED_EVENT, handleProxyNodesChanged);
    return () => {
      cancelled = true;
      window.removeEventListener(PROXY_NODES_CHANGED_EVENT, handleProxyNodesChanged);
    };
  }, []);

  const set = (key: string) => (e: ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) =>
    setForm((f) => ({ ...f, [key]: e.target.value }));

  const submitForm = async (submitAction = 'save') => {
    if (Date.now() < suppressSubmitUntilRef.current) return;
    if (!form.host.trim()) return window.lumeDialog?.alert(t('请填写主机地址'));
    if (authMode === 'custom' && !form.username.trim()) return window.lumeDialog?.alert(t('请填写用户名'));
    if (authMode === 'credential' && !selectedCredId) return window.lumeDialog?.alert(t('请选择凭据'));
    if (form.proxyMode === 'node' && !form.proxyNodeId) return window.lumeDialog?.alert(t('请选择代理节点'));
    if (form.proxyMode === 'custom' && !String(form.proxyHost || '').trim()) return window.lumeDialog?.alert(t('请输入代理主机地址'));

    setSaving(true);
    try {
      const data: ServerFormData = { ...form };
      data.port = parseInt(String(data.port), 10) || 22;
      data.terminalInitPath = String(data.terminalInitPath || '').trim();
      data.fileManagerInitPath = String(data.fileManagerInitPath || '').trim();
      data.terminalEncoding = String(data.terminalEncoding || '').trim() || 'utf-8';
      data.allowLegacySshRsa = !!form.allowLegacySshRsa;
      data.autoReconnect = !!form.autoReconnect;
      data.proxyMode = form.proxyMode || 'direct';
      data.proxyNodeId = String(data.proxyNodeId || '').trim();
      data.proxyType = form.proxyType || 'socks5';
      data.proxyHost = String(data.proxyHost || '').trim();
      data.proxyPort = parseInt(String(data.proxyPort || '').trim(), 10) || 1080;
      data.proxyUsername = String(data.proxyUsername || '').trim();

      if (authMode === 'credential') {
        data.credentialId = selectedCredId;
        delete data.password;
        delete data.privateKey;
        delete data.passphrase;
        delete data.authMethod;
        delete data.authType;
      } else {
        data.authMethod = form.authType === 'key' ? 'privateKey' : 'password';
        data.credentialId = '';
        if (server?.id && !data.password) delete data.password;
        if (!server?.id && server && !data.password && server.password) data.password = server.password;
        if (server?.id && (!data.privateKey || data.privateKey === '[key configured]')) {
          delete data.privateKey;
        }
        if (server?.id && (!data.passphrase || data.passphrase === '****')) {
          delete data.passphrase;
        }
      }

      if (server?.id && data.proxyMode === 'custom' && !data.proxyPassword) {
        delete data.proxyPassword;
      }

      if (server?.id) data.id = server.id;

      if (submitAction === 'connect' && !server?.id && onSaveAndConnect) {
        const result = await onSaveAndConnect(data, clearAfterAdd);
        if (clearAfterAdd && result) resetInlineForm();
      } else {
        const result = await onSave(data, clearAfterAdd);
        if (!server?.id && clearAfterAdd && result) resetInlineForm();
      }
    } finally {
      setSaving(false);
    }
  };

  const handleSubmit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const submitAction = (e.nativeEvent as SubmitEvent)?.submitter?.dataset?.submitAction || 'save';
    await submitForm(submitAction);
  };

  const handleSelectPrivateKeyFile = async () => {
    try {
      const content = await AppGo.ReadPrivateKeyFile();
      if (content) {
        setForm((f) => ({ ...f, privateKey: content }));
      }
    } catch (e) {
      if (e) window.lumeDialog?.alert(`${t('读取私钥文件失败')}: ${e}`, t('错误'));
    }
  };

  const handleCancel = (e?: ReactMouseEvent) => {
    e?.preventDefault();
    e?.stopPropagation();
    suppressSubmitUntilRef.current = Date.now() + 300;
    if (inline && !server) {
      resetInlineForm();
      return;
    }
    if (inline && server) {
      window.setTimeout(() => onClose(), 0);
      return;
    }
    onClose();
  };

  return {
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
    resetInlineForm,
  };
}
