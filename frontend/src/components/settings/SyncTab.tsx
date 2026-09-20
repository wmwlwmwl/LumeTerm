import React from 'react';
import { t as $t } from '../../i18n.ts';
import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';
import { Cloud, Database, Folder, Lock, RefreshCw, Sparkles } from 'lucide-react';
import { cn } from '../../utils/cn.ts';
import { Button, Select } from '../ui';
import { SettingsPanel, SettingsTabRoot } from './SharedComponents';
import { settings } from './settingDefinitions';
import SyncProviderCard, { PROVIDER_ICON_CMP } from './sync/SyncProviderCard';
import { WebdavFormFields, R2FormFields, FtpFormFields, SftpFormFields } from './sync/SyncProviderForms';
import type { SyncTabProps } from './sync/syncTabTypes';

export type { SyncTabProps };

function formatSyncTime(timestamp: number | null | undefined) {
  if (!Number.isSafeInteger(Number(timestamp)) || Number(timestamp) <= 0) return '';
  const date = new Date(Number(timestamp));
  if (!Number.isFinite(date.getTime())) return '';
  const pad = (value: number, length = 2) => String(value).padStart(length, '0');
  return `${pad(date.getFullYear(), 4)}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

export default function SyncTab({
  syncProvider, onSyncProviderChange,
  syncMode, onSyncModeChange,
  autoSyncEnabled, onAutoSyncEnabledChange,
  providers, providerList,
  webdavForm, setWebdavField, webdavConfigured, webdavEditing, setWebdavEditing, webdavLoading, webdavTesting, webdavTestResult, onWebdavTest, onWebdavSave,
  r2Form, setR2Field, r2Configured, r2Editing, setR2Editing, r2Loading, r2Testing, r2TestResult, onR2Test, onR2Save,
  ftpForm, setFTPField, ftpConfigured, ftpEditing, setFtpEditing, ftpLoading, ftpTesting, ftpTestResult, onTestFTP, onSaveFTP,
  sftpForm, setSFTPField, sftpConfigured, sftpEditing, setSftpEditing, sftpLoading, sftpTesting, sftpTestResult, onTestSFTP, onSaveSFTP, setSftpForm,
  lastSyncTime, syncTombstoneStats, onPruneSyncTombstones, pruningTombstones, syncing, onSync, loadingBackups, restoring, onRestore, isAnyConfigured, addToast,
  hasRecoveryPassword, recoveryPasswordEditing, setRecoveryPasswordEditing, recoveryPasswordInput, setRecoveryPasswordInput, recoveryPasswordChanging, onSaveRecoveryPassword, onClearRecoveryPassword
}: SyncTabProps) {
  const formattedLastSyncTime = formatSyncTime(lastSyncTime);
  const tombstoneConnections = Number(syncTombstoneStats?.connections || 0);
  const tombstoneCredentials = Number(syncTombstoneStats?.credentials || 0);
  const tombstoneTotal = tombstoneConnections + tombstoneCredentials;
  const [tombstoneDays, setTombstoneDays] = React.useState(30);
  const handleLoadSftpPrivateKey = async () => {
    try {
      const key = await AppGo.ReadPrivateKeyFile();
      if (key) setSftpForm((prev) => ({ ...prev, privateKey: key }));
    } catch (e) {
      addToast($t('读取私钥文件失败') + ': ' + e, 'error');
    }
  };
  // settingDefinitions.ts 已类型化，直接使用 settings 注册表
  const settingsData = settings;
  const syncSettings = settingsData.sync;
  return (
    <SettingsTabRoot>
      <SettingsPanel data-settings-section-id={syncSettings.sections.sync.id} className="flex flex-col gap-2.5">
        <div data-settings-field-id={syncSettings.fields.autoSync.id} className="flex items-center gap-2 flex-wrap">
          <span className="text-lg font-semibold text-primary mr-1">{$t('自动同步')}</span>
          <Button variant={autoSyncEnabled ? 'primary' : 'secondary'} onClick={() => onAutoSyncEnabledChange(!autoSyncEnabled)}>
            {autoSyncEnabled ? $t('已开启') : $t('已关闭')}
          </Button>
        </div>
        <div data-settings-field-id={syncSettings.fields.autoSyncMode.id} className="flex items-center gap-2 flex-wrap">
          <span className="text-lg font-semibold text-primary mr-1">{$t('自动同步模式')}</span>
          {[
            { id: 'webdav', label: <><Cloud size={14} /> WebDAV</> },
            { id: 'r2', label: <><Database size={14} /> R2 (S3)</> },
            { id: 'ftp', label: <><Folder size={14} /> FTP</> },
            { id: 'sftp', label: <><Lock size={14} /> SFTP</> },
            { id: 'all', label: <><RefreshCw size={14} /> {$t('全部')}</> },
          ].map((opt) => (
            <Button key={opt.id} variant={syncMode === opt.id ? 'primary' : 'secondary'} onClick={() => onSyncModeChange(opt.id)}>
              {opt.label}
            </Button>
          ))}
        </div>
        <div data-settings-field-id={syncSettings.fields.encryption.id} className="flex items-center gap-2 flex-wrap">
          <span className="text-lg font-semibold text-primary mr-1">{$t('同步加密')}</span>
          {hasRecoveryPassword ? (
            <>
              <Button variant="primary" disabled>
                <Lock size={14} /> {$t('已加密')}
              </Button>
              <Button onClick={() => { setRecoveryPasswordEditing(true); setRecoveryPasswordInput(''); }} disabled={recoveryPasswordChanging}>
                {$t('修改密码')}
              </Button>
              <Button variant="ghost" className="text-danger hover:text-danger" onClick={onClearRecoveryPassword} disabled={recoveryPasswordChanging}>
                {$t('关闭加密')}
              </Button>
            </>
          ) : (recoveryPasswordEditing ? (
            <>
              <input id="sync-recovery-password" name="sync-recovery-password" className="input w-[200px] h-[34px] text-base" type="password" autoComplete="new-password" placeholder={$t('请输入恢复密码')} value={recoveryPasswordInput} disabled={recoveryPasswordChanging} onChange={(e) => setRecoveryPasswordInput(e.target.value)} autoFocus />
              <Button variant="primary" onClick={onSaveRecoveryPassword} disabled={!recoveryPasswordInput.trim() || recoveryPasswordChanging}>
                {$t('开启加密')}
              </Button>
              <Button variant="ghost" onClick={() => { setRecoveryPasswordEditing(false); setRecoveryPasswordInput(''); }} disabled={recoveryPasswordChanging}>
                {$t('取消')}
              </Button>
            </>
          ) : (
            <>
              <Button disabled>
                {$t('明文')}
              </Button>
              <Button onClick={() => setRecoveryPasswordEditing(true)}>
                <Lock size={14} /> {$t('加密同步')}
              </Button>
            </>
          ))}
        </div>
        {hasRecoveryPassword && recoveryPasswordEditing ? (
          <div className="flex items-center gap-2 flex-wrap">
            <input id="sync-recovery-new-password" name="sync-recovery-new-password" className="input w-[200px] h-[34px] text-base" type="password" autoComplete="new-password" placeholder={$t('请输入新恢复密码')} value={recoveryPasswordInput} disabled={recoveryPasswordChanging} onChange={(e) => setRecoveryPasswordInput(e.target.value)} autoFocus />
            <Button variant="primary" onClick={onSaveRecoveryPassword} disabled={!recoveryPasswordInput.trim() || recoveryPasswordChanging}>
              {$t('保存')}
            </Button>
            <Button variant="ghost" onClick={() => { setRecoveryPasswordEditing(false); setRecoveryPasswordInput(''); }} disabled={recoveryPasswordChanging}>
              {$t('取消')}
            </Button>
          </div>
        ) : null}
        <div className="text-sm text-tertiary leading-normal">
          {$t('默认明文同步，选择加密后需设置恢复密码。系统重装或云端凭据变更后，用恢复密码即可恢复备份。')}
          <div className="mt-1 text-warning">{$t('注意：多设备同步时，所有设备需使用相同的加密密码，否则其他设备无法解密同步数据。')}</div>
          {!hasRecoveryPassword ? <div className="mt-1 text-warning">{$t('未开启加密同步时会以明文保存到云端；如需保护云端备份，请选择加密并设置恢复密码。')}</div> : null}
        </div>
      </SettingsPanel>

      <SettingsPanel data-settings-section-id={syncSettings.sections.provider.id} className="grid grid-cols-[repeat(auto-fit,minmax(140px,1fr))] gap-2 p-2">
        {providerList.map((item) => (
          <button
            key={item.id}
            onClick={() => onSyncProviderChange(item.id)}
            aria-pressed={syncProvider === item.id}
            className={cn(
              'min-w-0 px-4 py-2.5 rounded-sm cursor-pointer text-md transition-all duration-[120ms] border flex items-center justify-center gap-2',
              syncProvider === item.id
                ? 'bg-accent-dim border-accent-border text-primary font-semibold shadow-[inset_0_0_0_1px_var(--accent-border)]'
                : 'bg-sunken border-line text-secondary',
            )}
          >
            {(() => { const IconCmp = PROVIDER_ICON_CMP[item.id]; return IconCmp ? <IconCmp size={16} /> : null; })()} {item.label}
          </button>
        ))}
      </SettingsPanel>

      {syncProvider === 'webdav' ? (
        <SyncProviderCard
          definition={syncSettings.fields.webdav}
          providerKey="webdav"
          provider={providers.webdav}
          form={webdavForm}
          configured={webdavConfigured}
          editing={webdavEditing}
          onEdit={() => setWebdavEditing(true)}
          onCancelEdit={() => setWebdavEditing(false)}
          testing={webdavTesting}
          testResult={webdavTestResult}
          onTest={onWebdavTest}
          loading={webdavLoading}
          onSave={onWebdavSave}
        >
          <WebdavFormFields webdavForm={webdavForm} setWebdavField={setWebdavField} />
        </SyncProviderCard>
      ) : null}

      {syncProvider === 'r2' ? (
        <SyncProviderCard
          definition={syncSettings.fields.r2}
          providerKey="r2"
          provider={providers.r2}
          form={r2Form}
          configured={r2Configured}
          editing={r2Editing}
          onEdit={() => setR2Editing(true)}
          onCancelEdit={() => setR2Editing(false)}
          testing={r2Testing}
          testResult={r2TestResult}
          onTest={onR2Test}
          loading={r2Loading}
          onSave={onR2Save}
        >
          <R2FormFields r2Form={r2Form} setR2Field={setR2Field} />
        </SyncProviderCard>
      ) : null}

      {syncProvider === 'ftp' ? (
        <SyncProviderCard
          definition={syncSettings.fields.ftp}
          providerKey="ftp"
          provider={providers.ftp}
          form={ftpForm}
          configured={ftpConfigured}
          editing={ftpEditing}
          onEdit={() => setFtpEditing(true)}
          onCancelEdit={() => setFtpEditing(false)}
          testing={ftpTesting}
          testResult={ftpTestResult}
          onTest={onTestFTP}
          loading={ftpLoading}
          onSave={onSaveFTP}
        >
          <FtpFormFields ftpForm={ftpForm} setFTPField={setFTPField} />
        </SyncProviderCard>
      ) : null}

      {syncProvider === 'sftp' ? (
        <SyncProviderCard
          definition={syncSettings.fields.sftp}
          providerKey="sftp"
          provider={providers.sftp}
          form={sftpForm}
          configured={sftpConfigured}
          editing={sftpEditing}
          onEdit={() => setSftpEditing(true)}
          onCancelEdit={() => setSftpEditing(false)}
          testing={sftpTesting}
          testResult={sftpTestResult}
          onTest={onTestSFTP}
          loading={sftpLoading}
          onSave={onSaveSFTP}
        >
          <SftpFormFields sftpForm={sftpForm} setSFTPField={setSFTPField} onLoadPrivateKey={() => { void handleLoadSftpPrivateKey(); }} />
        </SyncProviderCard>
      ) : null}

      <SettingsPanel data-settings-section-id={syncSettings.sections.cloud.id} className="p-3.5">
        <div data-settings-field-id={syncSettings.fields.cloudBackup.id} className="text-[16px] font-semibold text-primary mb-2">{$t('云端同步')}</div>
        <div className="text-sm text-secondary mb-5">
          {hasRecoveryPassword ? $t('同步将写入 .lumeterm2 加密备份') : $t('未开启同步加密时写入明文 .json 备份')}
        </div>
        {autoSyncEnabled && isAnyConfigured ? (
          <div className="flex items-center gap-2 px-3.5 py-2.5 bg-[rgba(var(--success-rgb),0.12)] border border-[rgba(var(--success-rgb),0.28)] rounded-lg mb-5 text-success text-base">
            <span className="inline-flex items-center"><Sparkles size={14} /></span> <span><strong>{$t('已开启自动云端备份：')}</strong>{$t('添加、编辑、删除时自动同步')}</span>
          </div>
        ) : null}
        {formattedLastSyncTime ? <div className="text-sm text-success mb-3">{$t('上次同步')}: {formattedLastSyncTime}</div> : null}
        <div className="flex flex-col gap-2.5 px-3.5 py-3 rounded-md border border-[color-mix(in_srgb,var(--warning)_35%,var(--border))] bg-[color-mix(in_srgb,var(--warning)_8%,var(--surface-raised))] text-secondary text-sm mb-4">
          <div data-settings-field-id={syncSettings.fields.tombstones.id}>
            <span className="text-primary font-semibold">{$t('删除记录')}</span>
            <span className="ml-2.5 px-2 py-px rounded-full bg-[color-mix(in_srgb,var(--warning)_18%,transparent)] text-warning font-semibold">
              {$t('连接')} {Number.isFinite(tombstoneConnections) ? tombstoneConnections : 0}
            </span>
            <span className="ml-2 px-2 py-px rounded-full bg-[color-mix(in_srgb,var(--text-secondary)_14%,transparent)] text-primary font-semibold">
              {$t('凭据')} {Number.isFinite(tombstoneCredentials) ? tombstoneCredentials : 0}
            </span>
            <div className="mt-1.5 text-tertiary leading-[1.45]">{$t('用于多设备同步删除，一般无需处理。')}</div>
          </div>
          {tombstoneTotal > 0 ? (
            <div className="flex items-center gap-2 flex-wrap">
              <span>{$t('清理超过')}</span>
              <div className="w-[90px]">
                <Select
                  id="sync-tombstone-days"
                  name="sync-tombstone-days"
                  value={String(tombstoneDays)}
                  disabled={pruningTombstones || syncing || loadingBackups || restoring}
                  onChange={(val) => setTombstoneDays(Number(val))}
                  options={[
                    { value: '7', label: '7' },
                    { value: '30', label: '30' },
                    { value: '90', label: '90' },
                    { value: '180', label: '180' },
                    { value: '0', label: $t('全部') },
                  ]}
                />
              </div>
              <span>{$t('天的删除记录')}</span>
              <button
                type="button"
                onClick={() => onPruneSyncTombstones?.(tombstoneDays)}
                disabled={pruningTombstones || syncing || loadingBackups || restoring}
                className="h-8 px-3 rounded-sm border border-[color-mix(in_srgb,var(--warning)_70%,transparent)] bg-[color-mix(in_srgb,var(--warning)_16%,transparent)] text-warning text-sm font-semibold cursor-pointer disabled:opacity-55 disabled:pointer-events-none"
              >
                {pruningTombstones ? $t('同步中...') : $t('清理删除记录')}
              </button>
            </div>
          ) : null}
        </div>
        <div className="flex gap-3">
          <Button data-settings-field-id={syncSettings.fields.mergeSync.id} onClick={onSync} disabled={syncing || loadingBackups || restoring}>
            {syncing ? $t('同步中...') : <><RefreshCw size={14} /> {$t('合并同步')}</>}
          </Button>
          <Button data-settings-field-id={syncSettings.fields.restore.id} onClick={onRestore} disabled={loadingBackups || restoring || syncing}>
            {loadingBackups ? $t('加载备份列表中...') : <><RefreshCw size={14} /> {$t('从云端恢复')}</>}
          </Button>
        </div>
      </SettingsPanel>
    </SettingsTabRoot>
  );
}
