import { useState, useEffect } from 'react';
import { Database, Upload, Download, FileDown, Eye, EyeOff } from 'lucide-react';
import { useTranslation } from '../i18n.ts';
import { Modal, Button } from './ui';
import { cn } from '../utils/cn.ts';

interface ExportOptions {
  useEncryption: boolean;
  password: string;
}

interface ImportExportDialogProps {
  onClose: () => void;
  onExport: (opts: ExportOptions) => void;
  onImport: () => void;
  onDownloadTemplate: () => void;
  hasRecoveryPassword: boolean;
  busy: boolean;
}

const ROW_BASE =
  'flex items-center gap-3 px-3 py-2.5 rounded-md border border-line cursor-pointer bg-sunken transition-[border-color] duration-[120ms]';
const ROW_ACTIVE = 'border-accent bg-accent-dim';

/**
 * 数据管理弹窗：导入 / 导出 / 下载模板。
 * 受控组件，由父级条件渲染。
 *
 * props:
 *   onClose          关闭回调
 *   onExport(opts)   导出回调，opts = { useEncryption: bool, password: string }
 *   onImport()       导出回调（内部会处理密码重试）
 *   onDownloadTemplate()  下载模板回调
 *   hasRecoveryPassword bool  本机是否设置了恢复密码（决定是否允许复用恢复密码）
 *   busy             bool  操作进行中（禁用按钮）
 */
export default function ImportExportDialog({ onClose, onExport, onImport, onDownloadTemplate, hasRecoveryPassword, busy }: ImportExportDialogProps) {
  const { t } = useTranslation();
  const [format, setFormat] = useState<'plain' | 'encrypted'>('plain');
  const [keyMode, setKeyMode] = useState<'recovery' | 'password'>('recovery');
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);

  // 切到明文时重置密码相关状态
  useEffect(() => {
    if (format === 'plain') {
      setKeyMode('recovery');
      setPassword('');
    }
  }, [format]);

  // 未设置恢复密码时，密文默认走自定义密码
  useEffect(() => {
    if (format === 'encrypted' && !hasRecoveryPassword) {
      setKeyMode('password');
    }
  }, [format, hasRecoveryPassword]);

  const canExport = () => {
    if (busy) return false;
    if (format === 'encrypted' && keyMode === 'password' && !password.trim()) return false;
    return true;
  };

  const handleExportClick = () => {
    if (!canExport()) return;
    onExport({
      useEncryption: format === 'encrypted',
      password: format === 'encrypted' && keyMode === 'password' ? password : '',
    });
  };

  const radioDot = (active: boolean) => (
    <span
      className={cn(
        'w-4 h-4 rounded-full border-2 inline-flex items-center justify-center shrink-0',
        active ? 'border-accent' : 'border-tertiary',
      )}
    >
      {active && <span className="w-2 h-2 rounded-full bg-accent" />}
    </span>
  );

  return (
    <Modal
      open
      onClose={onClose}
      size="md"
      title={t('数据管理')}
      icon={<Database size={18} />}
      closeOnOverlay={false}
      bodyClassName="overflow-y-auto max-h-[calc(80vh-120px)]"
      footer={
        <Button variant="secondary" onClick={onClose}>{t('关闭')}</Button>
      }
    >
      <div className="flex flex-col gap-4">
        {/* 导出区 */}
        <div className="flex flex-col gap-2.5">
          <div className="text-base font-semibold text-secondary flex items-center gap-1.5">
            <Download size={14} /> {t('导出全部节点')}
          </div>

          {/* 导出格式选择 */}
          <div className="flex gap-2">
            <div onClick={() => setFormat('plain')} role="radio" aria-checked={format === 'plain'} className={cn(ROW_BASE, format === 'plain' && ROW_ACTIVE, 'flex-1')}>
              {radioDot(format === 'plain')}
              <div>
                <div className="text-base font-semibold">{t('明文')}</div>
                <div className="text-xs text-tertiary">.json</div>
              </div>
            </div>
            <div onClick={() => setFormat('encrypted')} role="radio" aria-checked={format === 'encrypted'} className={cn(ROW_BASE, format === 'encrypted' && ROW_ACTIVE, 'flex-1')}>
              {radioDot(format === 'encrypted')}
              <div>
                <div className="text-base font-semibold">{t('密文')}</div>
                <div className="text-xs text-tertiary">.lumeterm2</div>
              </div>
            </div>
          </div>

          {/* 加密方式选择（仅密文时显示） */}
          {format === 'encrypted' && (
            <div className="flex flex-col gap-2 pl-1">
              <div className="text-sm text-tertiary">{t('加密方式')}</div>
              <div className="flex flex-col gap-1.5">
                <label
                  className={cn(ROW_BASE, 'px-3 py-2', !hasRecoveryPassword && 'cursor-not-allowed opacity-50')}
                  onClick={() => hasRecoveryPassword && setKeyMode('recovery')}>
                  {radioDot(keyMode === 'recovery')}
                  <div className="flex flex-col gap-0.5">
                    <span className="text-base">{t('复用恢复密码')}</span>
                    <span className="text-xs text-tertiary">{t('与同步加密使用同一个恢复密码')}</span>
                  </div>
                  {!hasRecoveryPassword && <span className="text-xs text-tertiary ml-auto">{t('未设置')}</span>}
                </label>
                <label
                  className={cn(ROW_BASE, 'px-3 py-2', keyMode === 'password' && ROW_ACTIVE)}
                  onClick={() => setKeyMode('password')}>
                  {radioDot(keyMode === 'password')}
                  <span className="text-base">{t('自定义密码')}</span>
                </label>
              </div>

              {keyMode === 'password' && (
                <div className="relative mt-0.5">
                  <input
                    id="import-export-password"
                    name="import-export-password"
                    autoComplete="off"
                    type={showPassword ? 'text' : 'password'}
                    placeholder={t('请输入导出密码')}
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    autoFocus
                    className="w-full box-border pr-9 h-[34px] px-2.5 bg-sunken border border-line rounded-sm text-primary text-base outline-none transition-colors duration-[80ms] placeholder:text-muted focus:border-focus focus:shadow-[0_0_0_2px_var(--accent-dim)]"
                  />
                  <button
                    type="button"
                    onClick={() => setShowPassword((v) => !v)}
                    aria-label="toggle password visibility"
                    className="absolute right-2 top-1/2 -translate-y-1/2 bg-transparent border-none p-0.5 flex cursor-pointer text-tertiary"
                  >
                    {showPassword ? <EyeOff size={14} /> : <Eye size={14} />}
                  </button>
                </div>
              )}
              {keyMode === 'recovery' && !hasRecoveryPassword && (
                <div className="text-xs text-warning px-0.5">
                  {t('未设置恢复密码，请输入自定义密码或先在同步设置中设置')}
                </div>
              )}
            </div>
          )}

          <Button variant="primary" onClick={handleExportClick} disabled={!canExport()} className="h-[34px] text-base">
            <Download size={14} className="mr-1.5" />{t('导出')}
          </Button>
        </div>

        {/* 分隔线 */}
        <div className="h-px bg-line" />

        {/* 导入区 */}
        <div className="flex flex-col gap-2.5">
          <div className="text-base font-semibold text-secondary flex items-center gap-1.5">
            <Upload size={14} /> {t('从文件导入')}
          </div>
          <div className="text-xs text-tertiary leading-normal">
            {t('支持明文 JSON 与密文 .lumeterm2；密文会优先尝试恢复密码，失败时提示输入密码')}
          </div>
          <Button variant="secondary" onClick={onImport} disabled={busy} className="h-[34px] text-base">
            <Upload size={14} className="mr-1.5" />{t('选择文件并导入')}
          </Button>
          {/* 模板下载：隶属导入区，明文模板供用户照着填后导入 */}
          <Button variant="ghost" onClick={onDownloadTemplate} disabled={busy} className="h-[30px] text-sm justify-start text-tertiary">
            <FileDown size={13} className="mr-1.5" />{t('下载导入模板')}
          </Button>
        </div>
      </div>
    </Modal>
  );
}
