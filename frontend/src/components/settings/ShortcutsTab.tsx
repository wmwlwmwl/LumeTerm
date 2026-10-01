import { useState } from 'react';
import { t as $t } from '../../i18n.ts';
import { cn } from '../../utils/cn.ts';
import { Button } from '../ui';
import { SettingsPanel, SettingsSectionTitle, SettingsTabRoot } from './SharedComponents.tsx';
import { settings } from './settingDefinitions';
import { formatShortcut, isMac } from '../../utils/platform.ts';

interface ShortcutRowProps {
  definition?: { id?: string };
  label: string;
  keyName: string;
  shortcuts: Record<string, string>;
  listeningKey: string | null;
  onSetListening: (key: string) => void;
  withBorder: boolean;
}

function ShortcutRow({ definition, label, keyName, shortcuts, listeningKey, onSetListening, withBorder }: ShortcutRowProps) {
  const isListening = listeningKey === keyName;
  return (
    <div data-settings-field-id={definition?.id} className={cn('flex justify-between items-center px-3 py-2.5', withBorder && 'border-b border-line')}>
      <span className="text-secondary text-base">{label}</span>
      <button
        onClick={() => onSetListening(keyName)}
        className={cn(
          'font-mono text-sm px-2.5 py-1 rounded-md cursor-pointer bg-raised transition-colors duration-[120ms] border',
          isListening ? 'text-success border-success' : 'text-tertiary border-line',
        )}
      >
        {isListening ? $t('请按下快捷键...') : formatShortcut(shortcuts[keyName])}
      </button>
    </div>
  );
}

interface ShortcutsTabProps {
  shortcuts: Record<string, string>;
  listeningKey: string | null;
  onSetListeningKey: (key: string) => void;
  onResetShortcuts: () => void;
}

export default function ShortcutsTab({ shortcuts, listeningKey, onSetListeningKey, onResetShortcuts }: ShortcutsTabProps) {
  // settingDefinitions.ts 已类型化，直接使用 settings 注册表
  const sectionNode = settings.shortcuts.sections.terminal!;
  const fieldNodes = (sectionNode.children || []).flatMap((node) => node.children || []).filter((node) => node.type === 'field');
  const shortcutNodes = fieldNodes.filter((node) => node.control === 'shortcut');
  const altHistoryScopeNode = fieldNodes.find((node) => node.alias === 'altOpenHistoryScope');
  const resetNode = (sectionNode.children || []).flatMap((node) => node.children || []).find((node) => node.type === 'action');
  const [altHistoryScope, setAltHistoryScope] = useState<'global' | 'input'>(
    localStorage.getItem('altOpenHistoryScope') === 'global' ? 'global' : 'input',
  );
  const updateAltHistoryScope = (scope: 'global' | 'input') => {
    setAltHistoryScope(scope);
    localStorage.setItem('altOpenHistoryScope', scope);
    window.dispatchEvent(new CustomEvent('alt-open-history-scope-changed', { detail: scope }));
  };
  return (
    <SettingsTabRoot>
      <div>
        <div className="flex items-center justify-between gap-3 mb-1.5">
          <SettingsSectionTitle definition={sectionNode} style={{ marginBottom: 0 }} />
          {resetNode ? (
            <Button
              size="sm"
              data-settings-field-id={resetNode.id}
              onClick={onResetShortcuts}
            >
              {resetNode.titleKey ? $t(resetNode.titleKey) : ''}
            </Button>
          ) : null}
        </div>
        <SettingsPanel className="p-0">
          {shortcutNodes.map((node, index) => (
            <ShortcutRow
              key={node.id}
              definition={node}
              label={node.titleKey ? $t(node.titleKey) : ''}
              keyName={node.alias || ''}
              shortcuts={shortcuts}
              listeningKey={listeningKey}
              onSetListening={onSetListeningKey}
              withBorder={index < shortcutNodes.length - 1}
            />
          ))}
          {altHistoryScopeNode ? (
            <div data-settings-field-id={altHistoryScopeNode.id} className="flex items-center justify-between gap-3 px-3 py-2.5 border-t border-line">
              <span className="text-secondary text-base">{altHistoryScopeNode.titleKey ? $t(altHistoryScopeNode.titleKey) : ''}</span>
              <div className="inline-flex shrink-0 rounded-md border border-line bg-raised p-0.5">
                <button
                  type="button"
                  onClick={() => updateAltHistoryScope('global')}
                  className={cn('px-2 py-1 rounded-[3px] text-sm transition-colors duration-[120ms]', altHistoryScope === 'global' ? 'bg-accent text-white' : 'text-tertiary hover:bg-hover')}
                >
                  {$t('全局')}
                </button>
                <button
                  type="button"
                  onClick={() => updateAltHistoryScope('input')}
                  className={cn('px-2 py-1 rounded-[3px] text-sm transition-colors duration-[120ms]', altHistoryScope === 'input' ? 'bg-accent text-white' : 'text-tertiary hover:bg-hover')}
                >
                  {$t('仅聚焦命令输入框时')}
                </button>
              </div>
            </div>
          ) : null}
        </SettingsPanel>
        <p className="mt-2.5 text-sm text-tertiary">{$t('注：部分快捷键行为受终端内的 Shell 设置影响。')}</p>
        {isMac ? (
          <p className="mt-1 text-sm text-tertiary">{$t('注：macOS 上 ⌘ 为主快捷键，物理 ⌃C/⌃D 等组合始终作为终端控制信号发送。')}</p>
        ) : null}
      </div>
    </SettingsTabRoot>
  );
}
