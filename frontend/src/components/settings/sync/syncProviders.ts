import * as AppGo from '../../../../wailsjs/go/wailsapp/App.js';
import { t as $t, type I18nKey } from '../../../i18n.ts';

export type ProviderKey = 'webdav' | 'r2' | 'ftp' | 'sftp';
export type ProviderTestResult = 'ok' | 'fail' | null;

export type WebdavForm = { url: string; username: string; password: string; remotePath: string; maxBackups: string };
export type R2Form = { accessKeyId: string; secretAccessKey: string; bucket: string; endpoint: string; region: string; prefix: string; maxBackups: string };
export type FTPForm = { mode: string; host: string; port: number; username: string; password: string; remoteDir: string; maxBackups: string };
export type SFTPForm = { host: string; port: number; username: string; password: string; authMethod: string; privateKey: string; remoteDir: string; maxBackups: string };
export type ProviderFormMap = { webdav: WebdavForm; r2: R2Form; ftp: FTPForm; sftp: SFTPForm };

export interface SummaryField { label: string; value: string; primary?: boolean; fullWidth?: boolean; }

/** 云同步提供方定义（test/save 等方法均由 wailsjs 生成类型，返回 Record<string, any> 直接透传） */
export interface ProviderDefinition<F extends Record<string, unknown>> {
  name: string;
  titleKey: I18nKey;
  subtitleKey: I18nKey;
  accent: string;
  accentRgb: string;
  successMsgKey: I18nKey;
  defaultForm: F;
  test: (form: F) => Promise<unknown>;
  save: (form: F) => Promise<unknown>;
  sync: () => Promise<Record<string, unknown>>;
  backup: () => Promise<Record<string, unknown>>;
  list: () => Promise<Array<Record<string, unknown>>>;
  restore: (name: string) => Promise<unknown>;
  restoreWithPassword: (name: string, password: string) => Promise<unknown>;
  getConfig: () => Promise<Record<string, unknown>>;
  isConfigured: (form: F) => boolean;
  applyConfig: (data: Record<string, unknown>) => F;
  // 参数加宽到 SyncTab 的宽松 ProviderForm，保证 PROVIDERS 整体可赋给 SyncTab providers
  summaryFields: (form: F | Record<string, string | number>) => SummaryField[];
}

/** 云同步提供方状态（四份同构 state 的统一定义） */
export interface ProviderStateEntry<F extends Record<string, unknown>> {
  form: F;
  setForm: React.Dispatch<React.SetStateAction<F>>;
  configured: boolean;
  setConfigured: React.Dispatch<React.SetStateAction<boolean>>;
  editing: boolean;
  setEditing: React.Dispatch<React.SetStateAction<boolean>>;
  loading: boolean;
  setLoading: React.Dispatch<React.SetStateAction<boolean>>;
  testing: boolean;
  setTesting: React.Dispatch<React.SetStateAction<boolean>>;
  testResult: ProviderTestResult;
  setTestResult: React.Dispatch<React.SetStateAction<ProviderTestResult>>;
}

export const defaultWebdavForm: WebdavForm = {
  url: '',
  username: '',
  password: '',
  remotePath: '/LumeTerm/',
  maxBackups: '',
};

export const defaultR2Form: R2Form = {
  accessKeyId: '',
  secretAccessKey: '',
  bucket: '',
  endpoint: '',
  region: 'auto',
  prefix: 'LumeTerm/',
  maxBackups: '',
};

export const defaultFTPForm: FTPForm = {
  mode: 'explicit_tls',
  host: '',
  port: 21,
  username: '',
  password: '',
  remoteDir: '/LumeTerm/',
  maxBackups: '',
};

export const defaultSFTPForm: SFTPForm = {
  host: '',
  port: 22,
  username: '',
  password: '',
  authMethod: 'password',
  privateKey: '',
  remoteDir: '/LumeTerm/',
  maxBackups: '',
};

export const parseConnectionTestPort = (value: unknown) => {
  const port = parseInt(String(value ?? '').trim(), 10);
  if (!Number.isFinite(port) || port <= 0 || port > 65535) {
    throw new Error($t('请输入有效端口'));
  }
  return port;
};

export const PROVIDERS: { [K in ProviderKey]: ProviderDefinition<ProviderFormMap[K]> } = {
  webdav: {
    name: 'WebDAV',
    titleKey: 'WebDAV 配置',
    subtitleKey: '配置 WebDAV 端点用于加密同步服务器列表',
    accent: 'var(--success)',
    accentRgb: '16, 185, 129',
    successMsgKey: '已成功绑定 WebDAV 服务',
    defaultForm: defaultWebdavForm,
    test: (f) => AppGo.TestWebdavConnection(f.url, f.username, f.password),
    save: (f) => AppGo.SaveWebdavConfig(f),
    sync: () => AppGo.SyncFromWebdav(),
    backup: () => AppGo.BackupToWebdav(),
    list: () => AppGo.ListWebdavBackups(),
    restore: (name) => AppGo.RestoreFromWebdavFile(name),
    restoreWithPassword: (name, pw) => AppGo.RestoreFromWebdavFileWithPassword(name, pw),
    getConfig: () => AppGo.GetWebdavConfig(),
    isConfigured: (f) => !!f.username,
    applyConfig: (data) => ({ url: (data.url as string) || '', username: (data.username as string) || '', password: (data.password as string) || '', remotePath: (data.remotePath as string) || '/LumeTerm/', maxBackups: (data.maxBackups as string) || '' }),
    summaryFields: (form) => {
      const f = form as WebdavForm;
      return [
        { label: $t('绑定账号'), value: f.username, primary: true },
        { label: $t('备份目录'), value: f.remotePath },
        { label: $t('保留份数'), value: f.maxBackups || $t('不限') },
        { label: $t('服务器地址'), value: f.url, fullWidth: true },
      ];
    },
  },
  r2: {
    name: 'R2',
    titleKey: 'R2 (S3 兼容) 配置',
    subtitleKey: '配置 Cloudflare R2 或任意 S3 兼容对象存储用于加密同步',
    accent: '#3b82f6',
    accentRgb: '59, 130, 246',
    successMsgKey: '已成功绑定 R2 对象存储',
    defaultForm: defaultR2Form,
    test: (f) => AppGo.TestR2Connection(f.accessKeyId, f.secretAccessKey, f.bucket, f.endpoint),
    save: (f) => AppGo.SaveR2Config(f),
    sync: () => AppGo.SyncFromR2(),
    backup: () => AppGo.BackupToR2(),
    list: () => AppGo.ListR2Backups(),
    restore: (name) => AppGo.RestoreFromR2File(name),
    restoreWithPassword: (name, pw) => AppGo.RestoreFromR2FileWithPassword(name, pw),
    getConfig: () => AppGo.GetR2Config(),
    isConfigured: (f) => !!(f.bucket && f.endpoint),
    applyConfig: (data) => ({ accessKeyId: (data.accessKeyId as string) || '', secretAccessKey: (data.secretAccessKey as string) || '', bucket: (data.bucket as string) || '', endpoint: (data.endpoint as string) || '', region: (data.region as string) || 'auto', prefix: (data.prefix as string) || 'LumeTerm/', maxBackups: (data.maxBackups as string) || '' }),
    summaryFields: (form) => {
      const f = form as R2Form;
      return [
        { label: $t('存储桶'), value: f.bucket, primary: true },
        { label: $t('前缀目录'), value: f.prefix },
        { label: $t('端点地址'), value: f.endpoint, fullWidth: true },
        { label: $t('保留份数'), value: f.maxBackups || $t('不限') },
      ];
    },
  },
  ftp: {
    name: 'FTP',
    titleKey: 'FTP 配置',
    subtitleKey: '配置 FTP 服务器用于加密同步服务器列表',
    accent: '#f472b6',
    accentRgb: '244, 114, 182',
    successMsgKey: '已成功绑定 FTP 服务器',
    defaultForm: defaultFTPForm,
    test: (f) => AppGo.TestFTPConnection(f.host, parseConnectionTestPort(f.port), f.username, f.password, f.mode),
    save: (f) => AppGo.SaveFTPConfig({ mode: f.mode, host: f.host, port: String(f.port), username: f.username, password: f.password, remoteDir: f.remoteDir, maxBackups: String(f.maxBackups || '') }),
    sync: () => AppGo.SyncFromFTP(),
    backup: () => AppGo.BackupToFTP(),
    list: () => AppGo.ListFTPBackups(),
    restore: (name) => AppGo.RestoreFromFTPFile(name),
    restoreWithPassword: (name, pw) => AppGo.RestoreFromFTPFileWithPassword(name, pw),
    getConfig: () => AppGo.GetFTPConfig(),
    isConfigured: (f) => !!f.host,
    applyConfig: (data) => ({ mode: (data.mode as string) || 'explicit_tls', host: (data.host as string) || '', port: (data.port as number) || 21, username: (data.username as string) || '', password: (data.password as string) || '', remoteDir: (data.remoteDir as string) || '/LumeTerm/', maxBackups: (data.maxBackups as string) || '' }),
    summaryFields: (form) => {
      const f = form as FTPForm;
      return [
        { label: $t('连接模式'), value: f.mode === 'plain' ? $t('普通 FTP（不安全）') : $t('显式 FTPS（推荐）'), primary: true },
        { label: $t('主机地址'), value: f.host, primary: true },
        { label: $t('端口'), value: String(f.port) },
        { label: $t('用户名'), value: f.username, primary: true },
        { label: $t('远程目录'), value: f.remoteDir },
        { label: $t('保留份数'), value: f.maxBackups || $t('不限') },
      ];
    },
  },
  sftp: {
    name: 'SFTP',
    titleKey: 'SFTP (SSH) 配置',
    subtitleKey: '配置 SFTP 服务器用于加密同步服务器列表',
    accent: 'var(--success)',
    accentRgb: '34, 197, 94',
    successMsgKey: '已成功绑定 SFTP 服务器',
    defaultForm: defaultSFTPForm,
    test: (f) => AppGo.TestSFTPConnection(f.host, parseConnectionTestPort(f.port), f.username, f.password, f.authMethod, f.privateKey, ''),
    save: (f) => AppGo.SaveSFTPConfig({ host: f.host, port: String(f.port), username: f.username, password: f.password, authMethod: f.authMethod, privateKey: f.privateKey, remoteDir: f.remoteDir, maxBackups: String(f.maxBackups || '') }),
    sync: () => AppGo.SyncFromSFTP(),
    backup: () => AppGo.BackupToSFTP(),
    list: () => AppGo.ListSFTPBackups(),
    restore: (name) => AppGo.RestoreFromSFTPFile(name),
    restoreWithPassword: (name, pw) => AppGo.RestoreFromSFTPFileWithPassword(name, pw),
    getConfig: () => AppGo.GetSFTPConfig(),
    isConfigured: (f) => !!f.host,
    applyConfig: (data) => ({ host: (data.host as string) || '', port: (data.port as number) || 22, username: (data.username as string) || '', password: (data.password as string) || '', authMethod: (data.authMethod as string) || 'password', privateKey: (data.privateKey as string) || '', remoteDir: (data.remoteDir as string) || '/LumeTerm/', maxBackups: (data.maxBackups as string) || '' }),
    summaryFields: (form) => {
      const f = form as SFTPForm;
      return [
        { label: $t('主机地址'), value: f.host, primary: true },
        { label: $t('端口'), value: String(f.port) },
        { label: $t('用户名'), value: f.username, primary: true },
        { label: $t('远程目录'), value: f.remoteDir },
        { label: $t('保留份数'), value: f.maxBackups || $t('不限') },
      ];
    },
  },
};

export const PROVIDER_LIST = [
  { id: 'webdav', label: 'WebDAV' },
  { id: 'r2', label: 'R2 (S3)' },
  { id: 'ftp', label: 'FTP' },
  { id: 'sftp', label: 'SFTP' },
];

export function getBackupFormatLabel(name = '') {
  const lower = String(name).toLowerCase();
  // .lumeterm2 为新格式，兼容识别旧 .lumin2
  if (lower.endsWith('.lumeterm2') || lower.endsWith('.lumin2')) return 'LUMETERM2';
  if (lower.endsWith('.json')) return 'JSON';
  return 'UNKNOWN';
}
