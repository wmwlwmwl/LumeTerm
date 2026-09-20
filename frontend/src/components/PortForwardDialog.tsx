import { useEffect, useState } from 'react';
import { X } from 'lucide-react';
import * as AppGo from '../../wailsjs/go/wailsapp/App.js';
import type { sshmanager } from '../../wailsjs/go/models.ts';
import { useTranslation } from '../i18n.ts';
import { Button, Modal } from './ui';
import type { PortForwardInitialMapping } from '../hooks/usePortForwardDialog.ts';
import { PortForwardListTab } from './portForward/PortForwardListTab.tsx';
import { PortForwardNewTab } from './portForward/PortForwardNewTab.tsx';

export interface PortForwardDialogProps {
  sessionId: string;
  onClose: () => void;
  initialMapping: PortForwardInitialMapping | null;
  initialTab: string | null;
}

export default function PortForwardDialog({
  sessionId,
  onClose,
  initialMapping = null,
  initialTab = null,
}: PortForwardDialogProps) {
  const { t } = useTranslation();
  const [activeTab, setActiveTab] = useState<string>(initialTab || (initialMapping ? 'new' : 'list'));
  const [portForwards, setPortForwards] = useState<sshmanager.PortForwardInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [kind, setKind] = useState<string>(initialMapping?.kind || 'local');
  const [localHost, setLocalHost] = useState<string>(initialMapping?.localHost || '127.0.0.1');
  const [localPort, setLocalPort] = useState<string>(initialMapping?.localPort || '');
  const [remoteHost, setRemoteHost] = useState<string>(initialMapping?.remoteHost || '127.0.0.1');
  const [remotePort, setRemotePort] = useState<string>(initialMapping?.remotePort || '');
  const [error, setError] = useState('');

  const refreshPortForwards = async () => {
    setLoading(true);
    try {
      const list = await AppGo.ListPortForwards(sessionId);
      setPortForwards(list || []);
    } catch (err) {
      window.lumeDialog?.alert(`${t('加载端口映射失败')}: ${String(err)}`);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refreshPortForwards();
  }, [sessionId]);

  useEffect(() => {
    if (!initialMapping) return;
    setActiveTab('new');
    setKind(initialMapping.kind || 'local');
    setLocalHost(initialMapping.localHost || '127.0.0.1');
    setLocalPort(initialMapping.localPort || '');
    setRemoteHost(initialMapping.remoteHost || '127.0.0.1');
    setRemotePort(initialMapping.remotePort || '');
  }, [initialMapping]);

  const notifyChanged = () => {
    window.dispatchEvent(new CustomEvent('port-forward-changed', { detail: { sessionId } }));
  };

  const normalizePort = (value: string) => value.trim();

  const validatePort = (value: string) => {
    const port = normalizePort(value);
    if (!/^[0-9]+$/.test(port)) {
      return false;
    }
    const intValue = Number(port);
    return intValue >= 1 && intValue <= 65535;
  };

  const handleCreate = async () => {
    setError('');
    const normalizedLocalPort = normalizePort(localPort);
    const normalizedRemotePort = normalizePort(remotePort);
    if (!validatePort(normalizedLocalPort) || !validatePort(normalizedRemotePort)) {
      setError(t('请输入有效的端口号（1-65535）'));
      return;
    }
    if (!remoteHost.trim()) {
      setError(t('请输入远程主机地址'));
      return;
    }
    if (!localHost.trim()) {
      setError(t('请输入本机主机地址'));
      return;
    }

    const localAddr = `${localHost}:${normalizedLocalPort}`;
    const remoteAddr = `${remoteHost}:${normalizedRemotePort}`;
    setSubmitting(true);

    try {
      if (kind === 'local') {
        await AppGo.StartLocalPortForward(sessionId, localAddr, remoteAddr);
      } else {
        await AppGo.StartRemotePortForward(sessionId, remoteAddr, localAddr);
      }
      notifyChanged();
      await refreshPortForwards();
      setActiveTab('list');
    } catch (err) {
      if (kind === 'local' && String(err).includes('local port already in use')) {
        setError(t('本地端口已占用'));
        return;
      }
      window.lumeDialog?.alert(`${kind === 'local' ? t('创建本地端口映射失败') : t('创建远程端口映射失败')}: ${String(err)}`);
    } finally {
      setSubmitting(false);
    }
  };

  const handleStop = async (id: string) => {
    try {
      await AppGo.StopPortForwardForSession(sessionId, id);
      notifyChanged();
      await refreshPortForwards();
    } catch (err) {
      window.lumeDialog?.alert(`${t('关闭端口映射失败')}: ${String(err)}`);
    }
  };

  const handleRestart = async (id: string) => {
    try {
      await AppGo.RestartPortForwardForSession(sessionId, id);
      notifyChanged();
      await refreshPortForwards();
    } catch (err) {
      window.lumeDialog?.alert(`${t('关闭端口映射失败')}: ${String(err)}`);
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await AppGo.DeletePortForwardForSession(sessionId, id);
      setPortForwards((prev) => prev.filter((info) => info.ID !== id));
      notifyChanged();
      await refreshPortForwards();
    } catch (err) {
      window.lumeDialog?.alert(`${t('关闭端口映射失败')}: ${String(err)}`);
    }
  };

  return (
    <Modal open onClose={onClose} align="top" hideClose panelClassName="flex flex-col overflow-hidden max-h-[calc(100vh-80px)]" bodyClassName="p-0 gap-0">
        <div className="px-5 pt-4 flex items-center justify-between gap-2">
          <div>
            <div className="flex items-center gap-2 text-md font-semibold text-primary mb-1">{t('端口映射')}</div>
            <div className="text-secondary text-[0.92rem]">
              {t('在本机与远程服务器之间建立端口转发通道。')}
            </div>
          </div>
          <Button variant="ghost" size="sm" type="button" onClick={onClose} aria-label={t('关闭')}>
            <X size={16} />
          </Button>
        </div>

        <div className="flex gap-2 px-6 pt-2">
          <Button
            variant="ghost"
            size="sm"
            aria-pressed={activeTab === 'list'}
            onClick={() => setActiveTab('list')}
          >
            {t('当前映射')}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            aria-pressed={activeTab === 'new'}
            onClick={() => setActiveTab('new')}
          >
            {t('新建映射')}
          </Button>
        </div>

        <div className="p-6 overflow-y-auto flex-1 min-h-0">
          {activeTab === 'list' ? (
            <PortForwardListTab
              portForwards={portForwards}
              loading={loading}
              onRefresh={refreshPortForwards}
              onRestart={handleRestart}
              onStop={handleStop}
              onDelete={handleDelete}
            />
          ) : (
            <PortForwardNewTab
              kind={kind}
              setKind={setKind}
              localHost={localHost}
              setLocalHost={setLocalHost}
              localPort={localPort}
              setLocalPort={setLocalPort}
              remoteHost={remoteHost}
              setRemoteHost={setRemoteHost}
              remotePort={remotePort}
              setRemotePort={setRemotePort}
              error={error}
              submitting={submitting}
              onClose={onClose}
              onCreate={handleCreate}
            />
          )}
        </div>
    </Modal>
  );
}
