import { t as $t } from '../../../i18n.ts';
import { Button, Modal } from '../../ui';
import { cn } from '../../../utils/cn.ts';
import { PROVIDER_LIST, getBackupFormatLabel, type ProviderKey } from './syncProviders.ts';

interface RestoreDialogsProps {
  confirmRestoreProvider: boolean;
  configuredProviderIds: () => ProviderKey[];
  failedRestoreProviders: ProviderKey[];
  loadingBackups: boolean;
  loadRestoreBackups: (providerId: string) => Promise<void>;
  setConfirmRestoreProvider: React.Dispatch<React.SetStateAction<boolean>>;
  confirmRestore: boolean;
  backupsList: Array<Record<string, unknown>>;
  selectedBackup: string | null;
  setSelectedBackup: React.Dispatch<React.SetStateAction<string | null>>;
  setConfirmRestore: React.Dispatch<React.SetStateAction<boolean>>;
  doRestore: (password?: string) => Promise<void>;
  restoring: boolean;
  restoreWithPassword: boolean;
  setRestoreWithPassword: React.Dispatch<React.SetStateAction<boolean>>;
  restorePasswordInput: string;
  setRestorePasswordInput: React.Dispatch<React.SetStateAction<string>>;
  doRestoreWithPassword: () => Promise<void>;
}

/** 云端恢复相关的三个弹窗：选择恢复来源 / 确认恢复（备份列表） / 解密失败后的密码重试 */
export default function RestoreDialogs({
  confirmRestoreProvider,
  configuredProviderIds,
  failedRestoreProviders,
  loadingBackups,
  loadRestoreBackups,
  setConfirmRestoreProvider,
  confirmRestore,
  backupsList,
  selectedBackup,
  setSelectedBackup,
  setConfirmRestore,
  doRestore,
  restoring,
  restoreWithPassword,
  setRestoreWithPassword,
  restorePasswordInput,
  setRestorePasswordInput,
  doRestoreWithPassword,
}: RestoreDialogsProps) {
  return (
    <>
      {/* 选择恢复来源 */}
      {confirmRestoreProvider && (() => {
        const availableProviders = configuredProviderIds().filter(id => !failedRestoreProviders.includes(id));
        return (
          <Modal open onClose={() => setConfirmRestoreProvider(false)} hideClose title={$t('选择恢复来源')}>
            <div>
              <div className="flex flex-col gap-2.5">
                {availableProviders.map(id => (
                  <Button key={id} variant="secondary" disabled={loadingBackups} onClick={() => loadRestoreBackups(id)}>
                    {PROVIDER_LIST.find(p => p.id === id)?.label || id}
                  </Button>
                ))}
                {availableProviders.length === 0 && <div className="text-secondary">{$t('没有可用的云端来源')}</div>}
              </div>
              <div className="flex justify-end gap-3">
                <Button variant="secondary" disabled={loadingBackups} onClick={() => setConfirmRestoreProvider(false)}>{$t('取消')}</Button>
              </div>
            </div>
          </Modal>
        );
      })()}

      {/* 确认恢复弹窗（含列表选择） */}
      {confirmRestore && (
        <Modal open onClose={() => setConfirmRestore(false)} hideClose title={$t('选择要恢复的云端备份')}>
          <div>
            <div className="text-secondary mb-4 text-md">
              {$t('此操作将覆盖当前所有的本地服务器配置，且无法撤销。请选择要恢复的备份时间：')}
            </div>

            <div className="max-h-[200px] overflow-y-auto mb-5 bg-canvas rounded-md p-2">
              {backupsList.map(bk => (
                <div
                  key={bk.name as string}
                  onClick={() => setSelectedBackup(bk.name as string)}
                  className={cn(
                    'py-2.5 px-3 rounded-sm cursor-pointer flex justify-between items-center border mb-1 transition-all duration-[200ms]',
                    selectedBackup === (bk.name as string) ? 'bg-[rgba(var(--success-rgb),0.10)] border-accent' : 'bg-transparent border-transparent',
                  )}
                >
                  <div className={selectedBackup === bk.name ? 'text-accent' : 'text-primary'}>
                    {bk.time as string}
                  </div>
                  <div className="flex items-center gap-2 text-secondary text-sm">
                    <span
                      className={cn(
                        'py-[2px] px-1.5 rounded-full border border-line',
                        getBackupFormatLabel(bk.name as string) === 'LUMETERM2' ? 'text-success' : 'text-secondary',
                      )}
                    >
                      {getBackupFormatLabel(bk.name as string)}
                    </span>
                    <span>{((bk.size as number) / 1024).toFixed(1)} KB</span>
                  </div>
                </div>
              ))}
            </div>

            <div className="flex gap-3 justify-end -mx-2.5">
              <Button variant="secondary" className="px-5" onClick={() => setConfirmRestore(false)}>{$t('取消')}</Button>
              <Button variant="danger" className="px-5" onClick={() => doRestore()} disabled={!selectedBackup || restoring}>
                {restoring ? $t('恢复中...') : $t('确定恢复')}
              </Button>
            </div>
          </div>
        </Modal>
      )}

      {/* 恢复失败 → 输入密码重试 */}
      {restoreWithPassword && (
        <Modal
          open
          onClose={() => { setRestoreWithPassword(false); setRestorePasswordInput(''); }}
          hideClose
          closeOnOverlay={false}
          size="sm"
          title={$t('输入恢复密码')}
        >
          <div>
            <div className="text-secondary mb-4 text-base leading-[1.6]">
              {$t('常规密钥解密失败。如果此备份是用恢复密码加密的，请输入恢复密码重试：')}
            </div>
            <input
              id="settings-modal-restore-password"
              name="settings-modal-restore-password"
              autoComplete="off"
              className="input w-full mb-4"
              type="password"
              placeholder={$t('恢复密码')}
              value={restorePasswordInput}
              onChange={(e) => setRestorePasswordInput(e.target.value)}
              autoFocus
            />
            <div className="flex gap-3 justify-end -mx-2.5">
              <Button variant="secondary" onClick={() => { setRestoreWithPassword(false); setRestorePasswordInput(''); }}>{$t('取消')}</Button>
              <Button variant="primary" onClick={doRestoreWithPassword} disabled={!restorePasswordInput.trim() || restoring}>
                {restoring ? $t('恢复中...') : $t('用密码恢复')}
              </Button>
            </div>
          </div>
        </Modal>
      )}
    </>
  );
}
