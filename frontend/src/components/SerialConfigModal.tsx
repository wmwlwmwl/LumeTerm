import { useState, useEffect } from 'react';
import { createPortal } from 'react-dom';
import { X, Cpu } from 'lucide-react';
import * as AppGo from '../../wailsjs/go/wailsapp/App.js';
import { useTranslation } from '../i18n.ts';
import { warnDev } from '../utils/devLog';
import { Button, Select, ModalDragStrip } from './ui';
import { useOverlayScrollLock } from '../hooks/useOverlayScrollLock.ts';

/** 串口连接配置（与 App.ConnectSerial 的参数对应） */
export interface SerialFormConfig {
  port: string;
  baudRate: number;
  dataBits: number;
  stopBits: number;
  parity: string;
}

interface SerialConfigModalProps {
  onClose: () => void;
  onConnect: (form: SerialFormConfig) => void;
}

export default function SerialConfigModal({ onClose, onConnect }: SerialConfigModalProps) {
  const { t } = useTranslation();
  const [ports, setPorts] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

  const [form, setForm] = useState<SerialFormConfig>({
    port: '',
    baudRate: 115200,
    dataBits: 8,
    stopBits: 1,
    parity: 'none',
  });

  useEffect(() => {
    let cancelled = false;
    AppGo.ListSerialPorts()
      .then((list) => {
        if (cancelled) return;
        setPorts(list || []);
        if (list && list.length > 0) {
          setForm((prev) => ({ ...prev, port: list[0] }));
        }
      })
      .catch((err) => {
        warnDev('Failed to list serial ports', err);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, []);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.port) return;
    onConnect(form);
  };

  useOverlayScrollLock(true);

  if (typeof document === 'undefined') return null;

  const overlayNode = (
    <div
      className="modal-overlay"
      data-modal-overlay="true"
      style={{ isolation: 'isolate' as const }}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <ModalDragStrip />
      <div className="modal modal-sm" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h3 className="modal-title">
            <Cpu size={16} />
            <span>{t('串口终端配置')}</span>
          </h3>
          <Button type="button" variant="ghost" size="icon" onClick={onClose} aria-label={t('关闭')}>
            <X size={16} />
          </Button>
        </div>

        <form onSubmit={handleSubmit}>
          <div className="modal-body">
            <div className="form-group">
              <label className="form-label" htmlFor="serial-config-port">{t('串口设备')}</label>
              {loading ? (
                <div className="text-sm text-tertiary">{t('正在检索串口设备...')}</div>
              ) : (ports.length === 0 ? (
                <div>
                  <input
                    id="serial-config-port"
                    name="serial-config-port"
                    autoComplete="off"
                    className="input"
                    placeholder={t('例如：COM3 或 /dev/ttyUSB0')}
                    value={form.port}
                    onChange={(e) => setForm({ ...form, port: e.target.value })}
                    required
                  />
                  <div className="text-xs text-warning mt-1">
                    {t('未检测到可用串口，您可以手动输入路径/设备名')}
                  </div>
                </div>
              ) : (
                <Select
                  id="serial-config-port"
                  name="serial-config-port"
                  value={form.port}
                  onChange={(val) => setForm((prev) => ({ ...prev, port: val }))}
                  options={ports.map((p) => ({ value: p, label: p }))}
                />
              ))}
            </div>

            <div className="form-group">
              <label className="form-label" htmlFor="serial-config-baud-rate">{t('波特率')}</label>
              <Select
                id="serial-config-baud-rate"
                name="serial-config-baud-rate"
                value={String(form.baudRate)}
                onChange={(val) => setForm((prev) => ({ ...prev, baudRate: parseInt(val, 10) }))}
                options={[1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200].map((b) => ({
                  value: String(b),
                  label: String(b),
                }))}
              />
            </div>

            <div className="grid grid-cols-2 gap-3">
              <div className="form-group">
                <label className="form-label" htmlFor="serial-config-data-bits">{t('数据位')}</label>
                <Select
                  id="serial-config-data-bits"
                  name="serial-config-data-bits"
                  value={String(form.dataBits)}
                  onChange={(val) => setForm((prev) => ({ ...prev, dataBits: parseInt(val, 10) }))}
                  options={[8, 7, 6, 5].map((d) => ({
                    value: String(d),
                    label: String(d),
                  }))}
                />
              </div>

              <div className="form-group">
                <label className="form-label" htmlFor="serial-config-stop-bits">{t('停止位')}</label>
                <Select
                  id="serial-config-stop-bits"
                  name="serial-config-stop-bits"
                  value={String(form.stopBits)}
                  onChange={(val) => setForm((prev) => ({ ...prev, stopBits: parseFloat(val) }))}
                  options={[
                    { value: '1', label: '1' },
                    { value: '1.5', label: '1.5' },
                    { value: '2', label: '2' },
                  ]}
                />
              </div>
            </div>

            <div className="form-group">
              <label className="form-label" htmlFor="serial-config-parity">{t('校验位')}</label>
              <Select
                id="serial-config-parity"
                name="serial-config-parity"
                value={form.parity}
                onChange={(val) => setForm((prev) => ({ ...prev, parity: val }))}
                options={[
                  { value: 'none', label: t('无校验') },
                  { value: 'odd', label: t('奇校验') },
                  { value: 'even', label: t('偶校验') },
                  { value: 'mark', label: t('标记校验') },
                  { value: 'space', label: t('空格校验') },
                ]}
              />
            </div>
          </div>

          <div className="modal-footer">
            <Button type="button" variant="secondary" onClick={onClose}>
              {t('取消')}
            </Button>
            <Button type="submit" variant="primary">
              {t('连接')}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );

  return createPortal(overlayNode, document.body);
}
