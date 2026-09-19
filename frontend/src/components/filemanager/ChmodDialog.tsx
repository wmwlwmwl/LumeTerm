import { useState, useEffect, useMemo } from 'react';
import { createPortal } from 'react-dom';
import { Lock } from 'lucide-react';
import { Z } from '../../constants/zIndex.ts';
import { useOverlayScrollLock } from '../../hooks/useOverlayScrollLock.ts';
import { Button, ModalDragStrip } from '../ui';
import {
  buildIdentityOptionList,
  calcChmodOctal,
  normalizeChmodMode,
  permsFromChmodMode,
  resolveIdentityInputValue,
} from '../../utils/fileManagerChmodIdentity.ts';
import type { ChmodDialogProps } from './fileManagerTypes.ts';

// ── Chmod Dialog ──────────────────────────────────────────────
export default function ChmodDialog({ path, permission, mode, rememberedMode = '', autoApplyLastSettings = false, uid, gid, ownerCandidates = [], groupCandidates = [], includeSubdirectories = false, showIncludeSubdirectories = false, onSave, onClose, t }: ChmodDialogProps) {
  const parsePerms = (permStr: unknown) => {
    const normalizedPermStr = String(permStr || '');
    const p = normalizedPermStr.length >= 10 ? normalizedPermStr.slice(1) : '---------';
    return {
      user: { r: p[0] === 'r', w: p[1] === 'w', x: p[2] === 'x' },
      group: { r: p[3] === 'r', w: p[4] === 'w', x: p[5] === 'x' },
      other: { r: p[6] === 'r', w: p[7] === 'w', x: p[8] === 'x' },
    };
  };

  const currentMode = normalizeChmodMode(mode);
  const lastMode = normalizeChmodMode(rememberedMode);
  const initialMode = autoApplyLastSettings && lastMode ? lastMode : currentMode;
  const fallbackPerms = parsePerms(permission || '');
  const [perms, setPerms] = useState(initialMode ? permsFromChmodMode(initialMode) : fallbackPerms);
  const [octal, setOctal] = useState(initialMode || calcChmodOctal(fallbackPerms));
  const [includeChildren, setIncludeChildren] = useState(autoApplyLastSettings ? Boolean(includeSubdirectories) : false);
  const ownerOptions = useMemo(() => buildIdentityOptionList(uid, ownerCandidates), [uid, ownerCandidates]);
  const groupOptions = useMemo(() => buildIdentityOptionList(gid, groupCandidates), [gid, groupCandidates]);
  const ownerDefaultValue = useMemo(() => resolveIdentityInputValue(uid, ownerCandidates), [uid, ownerCandidates]);
  const groupDefaultValue = useMemo(() => resolveIdentityInputValue(gid, groupCandidates), [gid, groupCandidates]);
  const [ownerInput, setOwnerInput] = useState(ownerDefaultValue);
  const [groupInput, setGroupInput] = useState(groupDefaultValue);
  const [ownerTouched, setOwnerTouched] = useState(false);
  const [groupTouched, setGroupTouched] = useState(false);

  useEffect(() => {
    setOwnerTouched(false);
  }, [path, uid]);

  useEffect(() => {
    setGroupTouched(false);
  }, [path, gid]);

  useEffect(() => {
    if (!ownerTouched) {
      setOwnerInput(ownerDefaultValue);
    }
  }, [ownerDefaultValue, ownerTouched]);

  useEffect(() => {
    if (!groupTouched) {
      setGroupInput(groupDefaultValue);
    }
  }, [groupDefaultValue, groupTouched]);

  const filteredOwnerOptions = useMemo(() => {
    const query = String(ownerInput || '').trim().toLowerCase();
    const candidates = query
      ? ownerOptions.filter((option) => option.searchText.includes(query))
      : ownerOptions;
    return candidates.slice(0, 80);
  }, [ownerInput, ownerOptions]);

  const filteredGroupOptions = useMemo(() => {
    const query = String(groupInput || '').trim().toLowerCase();
    const candidates = query
      ? groupOptions.filter((option) => option.searchText.includes(query))
      : groupOptions;
    return candidates.slice(0, 80);
  }, [groupInput, groupOptions]);

  const togglePerm = (cat: 'user' | 'group' | 'other', key: 'r' | 'w' | 'x') => {
    setPerms(prev => {
      const next = { ...prev, [cat]: { ...prev[cat], [key]: !prev[cat][key] } };
      setOctal(calcChmodOctal(next));
      return next;
    });
  };

  const handleOctalChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const val = normalizeChmodMode(e.target.value);
    setOctal(val);
    if (val.length === 3) {
      setPerms(permsFromChmodMode(val));
    }
  };

  const canApplyLastSettings = Boolean(lastMode);
  const handleApplyLastSettings = () => {
    if (!lastMode) {
      return;
    }
    setOctal(lastMode);
    setPerms(permsFromChmodMode(lastMode));
    setIncludeChildren(Boolean(includeSubdirectories));
  };

  useOverlayScrollLock(true);

  if (typeof document === 'undefined') {
    return null;
  }
  return createPortal(
    <div className="modal-overlay" data-modal-overlay="true" style={{ zIndex: Z.MODAL, isolation: 'isolate' as const }}>
      <ModalDragStrip />
      <div className="modal modal-sm">
        <div className="modal-header">
          <div className="modal-title"><Lock size={14} /> {t('修改权限')}</div>
        </div>
        <div className="modal-body">
          <div className="chmod-dialog-body">
            <div className="chmod-dialog-path">{path}</div>
            <div className="grid gap-[10px] my-3">
              <div className="grid gap-1.5">
                <label htmlFor="chmod-owner-input" className="text-sm text-tertiary">{t('属主')}</label>
                <input
                  id="chmod-owner-input"
                  name="chmod-owner"
                  className="input"
                  list="chmod-owner-options"
                  autoComplete="off"
                  value={ownerInput}
                  onChange={(e) => {
                    setOwnerTouched(true);
                    setOwnerInput(e.target.value);
                  }}
                  placeholder={t('搜索或输入属主...')}
                />
              </div>
              <datalist id="chmod-owner-options">
                {filteredOwnerOptions.map((option) => (
                  <option key={option.label} value={option.label} />
                ))}
              </datalist>
              <div className="grid gap-1.5">
                <label htmlFor="chmod-group-input" className="text-sm text-tertiary">{t('属组')}</label>
                <input
                  id="chmod-group-input"
                  name="chmod-group"
                  className="input"
                  list="chmod-group-options"
                  autoComplete="off"
                  value={groupInput}
                  onChange={(e) => {
                    setGroupTouched(true);
                    setGroupInput(e.target.value);
                  }}
                  placeholder={t('搜索或输入属组...')}
                />
              </div>
              <datalist id="chmod-group-options">
                {filteredGroupOptions.map((option) => (
                  <option key={option.label} value={option.label} />
                ))}
              </datalist>
            </div>
            <div className="chmod-grid">
              <div className="chmod-row">
                <span></span>
                <span className="text-center text-sm text-tertiary">{t('读取')}</span>
                <span className="text-center text-sm text-tertiary">{t('写入')}</span>
                <span className="text-center text-sm text-tertiary">{t('执行')}</span>
              </div>
              <div className="chmod-row">
                <span className="chmod-row-label">{t('用户')}</span>
                {(['r','w','x'] as const).map(k => (
                  <label key={k} htmlFor={`fm-chmod-user-${k}`} className="chmod-checkbox" style={{ justifyContent: 'center' }}>
                    <input type="checkbox" id={`fm-chmod-user-${k}`} name={`fm-chmod-user-${k}`} autoComplete="off" checked={perms.user[k]} onChange={() => togglePerm('user', k)} />
                  </label>
                ))}
              </div>
              <div className="chmod-row">
                <span className="chmod-row-label">{t('组')}</span>
                {(['r','w','x'] as const).map(k => (
                  <label key={k} htmlFor={`fm-chmod-group-${k}`} className="chmod-checkbox" style={{ justifyContent: 'center' }}>
                    <input type="checkbox" id={`fm-chmod-group-${k}`} name={`fm-chmod-group-${k}`} autoComplete="off" checked={perms.group[k]} onChange={() => togglePerm('group', k)} />
                  </label>
                ))}
              </div>
              <div className="chmod-row">
                <span className="chmod-row-label">{t('其他')}</span>
                {(['r','w','x'] as const).map(k => (
                  <label key={k} htmlFor={`fm-chmod-other-${k}`} className="chmod-checkbox" style={{ justifyContent: 'center' }}>
                    <input type="checkbox" id={`fm-chmod-other-${k}`} name={`fm-chmod-other-${k}`} autoComplete="off" checked={perms.other[k]} onChange={() => togglePerm('other', k)} />
                  </label>
                ))}
              </div>
            </div>
            <div className="flex items-center gap-3">
              <label htmlFor="chmod-octal-input" className="text-sm text-tertiary">{t('八进制:')}</label>
              <input id="chmod-octal-input" name="chmod-octal" className="chmod-octal-input" autoComplete="off" value={octal} onChange={handleOctalChange} />
              <Button variant="ghost" size="sm" onClick={handleApplyLastSettings} disabled={!canApplyLastSettings}>
                {t('应用上次')}
              </Button>
            </div>
            {showIncludeSubdirectories && (
              <label htmlFor="fm-chmod-include-children" className="chmod-checkbox flex" style={{ alignItems: 'center', gap: 8, marginTop: 12 }}>
                <input type="checkbox" id="fm-chmod-include-children" name="fm-chmod-include-children" autoComplete="off" checked={includeChildren} onChange={(e) => setIncludeChildren(e.target.checked)} />
                <span>{t('包含子目录')}</span>
              </label>
            )}
          </div>
        </div>
        <div className="modal-footer">
          <Button variant="ghost" onClick={onClose}>{t('取消')}</Button>
          <Button variant="primary" onClick={() => onSave(octal.length === 3 ? octal : calcChmodOctal(perms), includeChildren, ownerInput, groupInput)}>{t('确定')}</Button>
        </div>
      </div>
    </div>,
    document.body
  );
}
