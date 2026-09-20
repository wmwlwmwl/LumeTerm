import { useState, useEffect } from 'react';
import { Database, Download, Eye, EyeOff } from 'lucide-react';
import { useTranslation } from '../i18n.ts';
import { Modal, Button } from './ui';
import { cn } from '../utils/cn.ts';

interface ExportOptions {
  useEncryption: boolean;
  password: string;
}

interface ExportSelectedDialogProps {
  onClose: () => void;
  onExport: (opts: ExportOptions) => void;
  hasRecoveryPassword: boolean;
  busy: boolean;
  selectedCount: number;
}

const ROW_BASE =
  'flex items-center gap-3 px-3 py-2.5 rounded-md border border-line cursor-pointer bg-sunken transition-[border-color] duration-[120ms]';
const ROW_ACTIVE = 'border-accent bg-accent-dim';

/**
 * 导出已选择节点弹窗。
 *
 * props:
 *   onClose          关闭回调
 *   onExport(opts)   导出回调，opts = { useEncryption: bool, password: string }
 *   hasRecoveryPassword bool  本机是否设置了恢复密码
 *   busy             bool  操作进行中
 *   selectedCount    number 已选择的服务器数量
 */
export default function ExportSelectedDialog({ onClose, onExport, hasRecoveryPassword, busy, selectedCount }: ExportSelectedDialogProps) {
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
      title={t('导出已选节点')}
      icon={<Database size={18} />}
      closeOnOverlay={false}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>{t('关闭')}</Button>
          <Button variant="primary" onClick={handleExportClick} disabled={!canExport()} className="min-w-20">
            <Download size={14} className="mr-1.5" />{t('导出')}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <div className="text-base text-secondary bg-sunken px-3 py-2.5 rounded-sm border-l-[3px] border-l-accent">
          {t('您已选择 {count} 个服务器节点进行导出。', { count: selectedCount })}
        </div>

        <div className="flex flex-col gap-2.5">
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
                    id="export-selected-password"
                    name="export-selected-password"
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
        </div>
      </div>
    </Modal>
  );
}
