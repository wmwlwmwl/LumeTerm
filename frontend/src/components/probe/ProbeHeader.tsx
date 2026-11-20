import { Clipboard, Eye, EyeOff, Monitor } from 'lucide-react';
import type { I18nKey } from '../../i18n.ts';
import Tiptop from '../Tiptop.tsx';
import { Card } from './ProbeVisuals.tsx';
import { maskAddress, persistProbeHideIP, type ProbeInfo } from './probeTypes.ts';

export interface ProbeHeaderProps {
  t: (key: I18nKey) => string;
  info: ProbeInfo;
  displayIP: string;
  hideIP: boolean;
  addToast?: (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;
}

export function ProbeHeader({
  t,
  info,
  displayIP,
  hideIP,
  addToast,
}: ProbeHeaderProps) {
  const osParts = info.os?.split(' ') || ['Linux'];

  const handleCopyIP = () => {
    navigator.clipboard.writeText(displayIP)
      .then(() => addToast?.(hideIP ? t('已复制') : `${t('已复制')} ${displayIP}`, 'success'))
      .catch(() => addToast?.(t('复制失败'), 'error'));
  };

  return (
    <Card className="probe-header-card">
      <div className="probe-host-row">
        <div className="probe-host-title">
          <Monitor size={14} />
          <span>{t('系统监控')}</span>
        </div>
      </div>
      {displayIP && (
        <div className="probe-ip-actions">
          <span className="probe-ip-chip" title={hideIP ? '' : displayIP}>
            {hideIP ? maskAddress(displayIP) : displayIP}
          </span>
          <Tiptop text={t('复制主机地址')} placement="bottom">
            <button onClick={handleCopyIP} aria-label={t('复制主机地址')} className="probe-icon-btn">
              <Clipboard size={13} />
            </button>
          </Tiptop>
          <Tiptop text={hideIP ? t('显示 IP') : t('隐藏 IP')} placement="bottom">
            <button onClick={() => persistProbeHideIP(!hideIP)} aria-label={hideIP ? t('显示 IP') : t('隐藏 IP')} className="probe-icon-btn">
              {hideIP ? <Eye size={13} /> : <EyeOff size={13} />}
            </button>
          </Tiptop>
        </div>
      )}
      <div className="probe-system-grid">
        <div className="probe-os-row">
          <span className="probe-os-chip">{osParts[0]}</span>
          <span className="probe-os-detail" title={info.os}>
            {info.os?.replace(osParts[0], '').trim() || info.os}
          </span>
        </div>
        <span title={`${t('时区')} ${info.timezone}`}>
          {t('时区')} <b>{info.timezone}</b>
        </span>
        <span title={`${t('运行')} ${info.uptime}`}>
          {t('运行')} <b>{info.uptime}</b>
        </span>
      </div>
    </Card>
  );
}
