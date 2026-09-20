import { useState } from 'react';
import * as AppGo from '../../../../wailsjs/go/wailsapp/App.js';
import { t as $t, type I18nKey } from '../../../i18n.ts';
import { syncWithRecoveryPassword } from '../../../utils/recoveryPasswordSync.ts';
import { settingsChoice, settingsPrompt } from '../settingsDialogs.ts';
import type { LumeDialogPromptOptions } from '../../../types/lumeDialog.js';
import { PROVIDERS, type ProviderFormMap, type ProviderKey, type ProviderStateEntry } from './syncProviders.ts';

type AddToast = (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;

interface UseSyncRestoreOptions {
  addToast: AddToast;
  onRestored?: () => void;
  refreshSyncMeta: () => Promise<void>;
  syncMode: string;
  providerState: { [K in ProviderKey]: ProviderStateEntry<ProviderFormMap[K]> };
  configuredProviderIds: () => ProviderKey[];
  confirmSecureProviders: (providerIds: ProviderKey[]) => Promise<void>;
  restoreWithPassword: boolean;
  setRestoreWithPassword: React.Dispatch<React.SetStateAction<boolean>>;
  restorePasswordInput: string;
  setRestorePasswordInput: React.Dispatch<React.SetStateAction<string>>;
}

/** 云端恢复（备份列表/确认弹窗数据）与合并同步 */
export function useSyncRestore({
  addToast,
  onRestored,
  refreshSyncMeta,
  syncMode,
  providerState: _providerState,
  configuredProviderIds,
  confirmSecureProviders,
  restoreWithPassword: _restoreWithPassword,
  setRestoreWithPassword,
  restorePasswordInput,
  setRestorePasswordInput,
}: UseSyncRestoreOptions) {
  const [restoring, setRestoring] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [confirmRestore, setConfirmRestore] = useState(false);
  const [confirmRestoreProvider, setConfirmRestoreProvider] = useState(false);
  // 备份列表来自 AppGo.List*Backups()（wailsjs 生成类型为 Record<string, any>）
  const [backupsList, setBackupsList] = useState<Array<Record<string, unknown>>>([]);
  const [selectedBackup, setSelectedBackup] = useState<string | null>(null);
  const [restoreProvider, setRestoreProvider] = useState<ProviderKey | null>(null);
  const [failedRestoreProviders, setFailedRestoreProviders] = useState<ProviderKey[]>([]);
  const [loadingBackups, setLoadingBackups] = useState(false);

  const loadRestoreBackups = async (providerId: string) => {
    // PROVIDERS 仅含 4 个 key；运行期 providerId 均来自 configuredProviderIds()/syncMode 选项
    const providerKey = providerId as ProviderKey;
    setLoadingBackups(true);
    try {
      const p = PROVIDERS[providerKey];
      await confirmSecureProviders([providerKey]);
      const list = await p.list();
      if (!list || list.length === 0) {
        setFailedRestoreProviders(prev => [...new Set([...prev, providerKey])]);
        addToast($t('云端未找到任何备份文件') + '，' + $t('请重新选择'), 'error');
        if (syncMode === 'all') setConfirmRestoreProvider(true);
        return;
      }
      list.sort((a, b) => new Date(b.time as string | number).getTime() - new Date(a.time as string | number).getTime());
      setRestoreProvider(providerKey);
      setBackupsList(list);
      setSelectedBackup(list[0].name as string);
      setConfirmRestoreProvider(false);
      setConfirmRestore(true);
    } catch (err) {
      setFailedRestoreProviders(prev => [...new Set([...prev, providerKey])]);
      addToast($t('获取备份列表失败') + ': ' + String(err) + '，' + $t('请重新选择'), 'error');
      if (syncMode === 'all') setConfirmRestoreProvider(true);
    } finally {
      setLoadingBackups(false);
    }
  };

  const handleRestore = async () => {
    setFailedRestoreProviders([]);
    if (syncMode === 'all') {
      const availableProviders = configuredProviderIds();
      if (availableProviders.length === 1) {
        await loadRestoreBackups(availableProviders[0]);
      } else {
        setConfirmRestoreProvider(true);
      }
    } else {
      await loadRestoreBackups(syncMode);
    }
  };

  const doRestore = async (password?: string) => {
    if (!selectedBackup || !restoreProvider) return;
    setRestoring(true);
    try {
      const p = PROVIDERS[restoreProvider];
      await confirmSecureProviders(syncMode === 'all' ? configuredProviderIds() : [restoreProvider]);
      if (password && p.restoreWithPassword) {
        await p.restoreWithPassword(selectedBackup, password);
      } else {
        await p.restore(selectedBackup);
      }
      if (syncMode === 'all') {
        await AppGo.SyncAllProviders();
      } else {
        await p.sync();
      }
      await refreshSyncMeta();
      addToast($t('恢复成功'), 'success');
      onRestored?.();
      setConfirmRestore(false);
      setRestoreWithPassword(false);
      setRestorePasswordInput('');
    } catch (err) {
      setFailedRestoreProviders(prev => [...new Set([...prev, restoreProvider])]);
      const errStr = String(err);
      if (errStr.includes('解密失败') && !password) {
        // 解密失败 → 弹密码输入框兜底
        setConfirmRestore(false);
        setRestoreWithPassword(true);
      } else {
        addToast($t('恢复失败') + `: ${err}，` + $t('请重新选择'), 'error');
        setConfirmRestore(false);
        setRestoreWithPassword(false);
        if (syncMode === 'all') setConfirmRestoreProvider(true);
      }
    } finally {
      setRestoring(false);
    }
  };

  const doRestoreWithPassword = async () => {
    await doRestore(restorePasswordInput);
  };

  const handleSync = async () => {
    setSyncing(true);
    try {
      // syncMode 非 'all' 时必为 4 个 provider key 之一
      await confirmSecureProviders(syncMode === 'all' ? configuredProviderIds() : [syncMode as ProviderKey]);

      // 先读目标云，再决定是否应用本地删除墓碑（不是点切换模式就弹）
      try {
        const preview = await AppGo.PreviewTombstoneConflicts();
        const delConns = preview?.wouldDeleteConnections || [];
        const delCreds = preview?.wouldDeleteCredentials || [];
        if (delConns.length > 0 || delCreds.length > 0) {
          const connNames = delConns.map((x) => x.name || x.host || x.id).filter(Boolean);
          const credNames = delCreds.map((x) => x.name || x.id).filter(Boolean);
          const lines = [];
          if (connNames.length) lines.push(`${$t('服务器')}：${connNames.slice(0, 8).join('、')}${connNames.length > 8 ? '…' : ''}`);
          if (credNames.length) lines.push(`${$t('凭据')}：${credNames.slice(0, 8).join('、')}${credNames.length > 8 ? '…' : ''}`);
          const body = `${$t('目标云上仍存在以下项，本地删除记录同步后将删除它们：')}\n${lines.join('\n')}\n\n${$t('请选择：删除它们，或保留它们并与本地合并。')}`;
          const action = await settingsChoice(
            body,
            $t('同步删除确认'),
            [
              { label: $t('保留并合并'), value: 'keep', primary: true },
              { label: $t('删除'), value: 'apply', secondary: true },
              { label: $t('取消'), value: 'cancel', secondary: true },
            ],
          );
          if (action === 'cancel' || action == null) return;
          if (action === 'keep') {
            await AppGo.ClearTombstoneConflicts(
              delConns.map((x) => x.id).filter(Boolean),
              delCreds.map((x) => x.id).filter(Boolean),
            );
          }
        }
      } catch (previewErr) {
        const cont = await settingsChoice(
          `${$t('无法检查删除冲突，仍继续同步可能按本地删除记录静默删除目标云上的项。')}\n${previewErr}`,
          $t('预检失败'),
          [
            { label: $t('仍继续同步'), value: 'continue', primary: true },
            { label: $t('取消'), value: 'cancel', secondary: true },
          ],
        );
        if (cont !== 'continue') return;
      }

      const sync = syncMode === 'all'
        ? () => AppGo.SyncAllProviders()
        : () => (PROVIDERS[syncMode as ProviderKey] || PROVIDERS.webdav).sync();
      const { result: res, cancelled } = await syncWithRecoveryPassword({
        sync,
        retry: (password) => AppGo.SyncWithRecoveryPassword(password),
        // 保持原 spread 语义（按位置透传）；无 checkbox 场景恒为 string | null，非 string 视作取消
        prompt: async (title, placeholder, message, okLabel, options) => {
          const value = await settingsPrompt(title, placeholder, message, okLabel, options as LumeDialogPromptOptions);
          return typeof value === 'string' ? value : null;
        },
        // $t 是严格 key 签名（I18nKey），此处逃生为宽松 (key: string)
        t: (key: string) => $t(key as I18nKey),
      });
      if (cancelled || !res) return;
      await refreshSyncMeta();
      addToast(`${$t('合并同步成功！本地')} ${res.localCount} ${$t('个 + 云端')} ${res.remoteCount} ${$t('个 =')} ${res.mergedCount} ${$t('个')}`, 'success');
      onRestored?.();
    } catch (err) {
      addToast($t('合并同步失败') + ': ' + err, 'error');
    } finally {
      setSyncing(false);
    }
  };

  return {
    restoring,
    syncing,
    loadingBackups,
    confirmRestore,
    setConfirmRestore,
    confirmRestoreProvider,
    setConfirmRestoreProvider,
    backupsList,
    selectedBackup,
    setSelectedBackup,
    failedRestoreProviders,
    loadRestoreBackups,
    handleRestore,
    doRestore,
    doRestoreWithPassword,
    handleSync,
  };
}
