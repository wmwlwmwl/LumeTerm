import { Fragment } from 'react';
import { t as $t } from '../../i18n.ts';
import { Select } from '../ui';
import { RadioOption, ToggleSwitch, SettingRow, SettingsField, SettingsDivider, SettingsPanel, SettingsSectionTitle, SettingsTabRoot, type SettingsDefinitionNode } from './SharedComponents';
import { settings } from './settingDefinitions';

interface GeneralTabProps {
  language: string;
  onLanguageChange: (event: React.ChangeEvent<HTMLSelectElement>) => void;
  availableLanguages?: Array<{ code: string; label: string }>;
  confirmCloseSession: boolean;
  onToggleConfirmCloseSession: () => void;
  confirmCloseAll: boolean;
  onToggleConfirmCloseAll: () => void;
  confirmFileDelete: boolean;
  onToggleConfirmFileDelete: () => void;
  confirmProcessKill: boolean;
  onToggleConfirmProcessKill: () => void;
  confirmGitDiscard: boolean;
  onToggleConfirmGitDiscard: () => void;
  confirmGitForcePush: boolean;
  onToggleConfirmGitForcePush: () => void;
  confirmGitUntrack: boolean;
  onToggleConfirmGitUntrack: () => void;
  confirmTerminalSelectionPaste: boolean;
  onToggleConfirmTerminalSelectionPaste: () => void;
  confirmTerminalMultilinePaste: boolean;
  onToggleConfirmTerminalMultilinePaste: () => void;
  windowCloseAction: string;
  onWindowCloseActionChange: (value: string) => void;
  updateUseProxy: boolean;
  onToggleUpdateUseProxy: () => void;
  terminalRightClickPasteOnEmpty: boolean;
  onTerminalRightClickPasteOnEmptyChange: (value: boolean) => void;
  terminalRightClickPasteMode: string;
  onTerminalRightClickPasteModeChange: (value: string) => void;
  terminalLeftClickCopyOnSelection: boolean;
  onTerminalLeftClickCopyOnSelectionChange: (value: boolean) => void;
  terminalLeftClickCopyOnSelectionMode: string;
  onTerminalLeftClickCopyOnSelectionModeChange: (value: string) => void;
  terminalTabDoubleClickActionEnabled: boolean;
  onTerminalTabDoubleClickActionEnabledChange: (value: boolean) => void;
  terminalTabDoubleClickAction: string;
  onTerminalTabDoubleClickActionChange: (value: string) => void;
  rememberWorkspace: boolean;
  onToggleRememberWorkspace: () => void;
  workspacePersistenceLevel: string;
  onWorkspacePersistenceLevelChange: (value: string) => void;
  supportsWebviewGpuDisable: boolean;
  webviewGpuDisabled: boolean;
  onToggleWebviewGpuDisabled: () => void;
}

interface SelectBinding {
  value: string;
  onChange: (value: string) => void;
  width: number;
  options: Array<{ value: string; label: string }>;
}

export default function GeneralTab({
  language,
  onLanguageChange,
  availableLanguages = [],
  confirmCloseSession,
  onToggleConfirmCloseSession,
  confirmCloseAll,
  onToggleConfirmCloseAll,
  confirmFileDelete,
  onToggleConfirmFileDelete,
  confirmProcessKill,
  onToggleConfirmProcessKill,
  confirmGitDiscard,
  onToggleConfirmGitDiscard,
  confirmGitForcePush,
  onToggleConfirmGitForcePush,
  confirmGitUntrack,
  onToggleConfirmGitUntrack,
  confirmTerminalSelectionPaste,
  onToggleConfirmTerminalSelectionPaste,
  confirmTerminalMultilinePaste,
  onToggleConfirmTerminalMultilinePaste,
  windowCloseAction,
  onWindowCloseActionChange,
  updateUseProxy,
  onToggleUpdateUseProxy,
  terminalRightClickPasteOnEmpty,
  onTerminalRightClickPasteOnEmptyChange,
  terminalRightClickPasteMode,
  onTerminalRightClickPasteModeChange,
  terminalLeftClickCopyOnSelection,
  onTerminalLeftClickCopyOnSelectionChange,
  terminalLeftClickCopyOnSelectionMode,
  onTerminalLeftClickCopyOnSelectionModeChange,
  terminalTabDoubleClickActionEnabled,
  onTerminalTabDoubleClickActionEnabledChange,
  terminalTabDoubleClickAction,
  onTerminalTabDoubleClickActionChange,
  rememberWorkspace,
  onToggleRememberWorkspace,
  workspacePersistenceLevel,
  onWorkspacePersistenceLevelChange,
  supportsWebviewGpuDisable,
  webviewGpuDisabled,
  onToggleWebviewGpuDisabled,
}: GeneralTabProps) {
  // settingDefinitions.ts 已类型化，直接使用 settings 注册表
  const settingsData = settings.general;
  const generalTabNode = settingsData.node;
  const fieldValuesById: Record<string, boolean> = {
    [(settingsData.fields.rightClickPaste.id || '')]: terminalRightClickPasteOnEmpty,
    [(settingsData.fields.leftClickCopy.id || '')]: terminalLeftClickCopyOnSelection,
    [(settingsData.fields.terminalDoubleClick.id || '')]: terminalTabDoubleClickActionEnabled,
    [(settingsData.fields.rememberWorkspace.id || '')]: rememberWorkspace,
  };
  const toggleBindings: Record<string, { checked: boolean; onChange: () => void }> = {
    confirmCloseSession: { checked: confirmCloseSession, onChange: onToggleConfirmCloseSession },
    confirmCloseAll: { checked: confirmCloseAll, onChange: onToggleConfirmCloseAll },
    confirmFileDelete: { checked: confirmFileDelete, onChange: onToggleConfirmFileDelete },
    confirmProcessKill: { checked: confirmProcessKill, onChange: onToggleConfirmProcessKill },
    confirmGitDiscard: { checked: confirmGitDiscard, onChange: onToggleConfirmGitDiscard },
    confirmGitForcePush: { checked: confirmGitForcePush, onChange: onToggleConfirmGitForcePush },
    confirmGitUntrack: { checked: confirmGitUntrack, onChange: onToggleConfirmGitUntrack },
    confirmTerminalSelectionPaste: { checked: confirmTerminalSelectionPaste, onChange: onToggleConfirmTerminalSelectionPaste },
    confirmTerminalMultilinePaste: { checked: confirmTerminalMultilinePaste, onChange: onToggleConfirmTerminalMultilinePaste },
    terminalRightClickPasteOnEmpty: { checked: terminalRightClickPasteOnEmpty, onChange: () => onTerminalRightClickPasteOnEmptyChange(!terminalRightClickPasteOnEmpty) },
    terminalLeftClickCopyOnSelection: { checked: terminalLeftClickCopyOnSelection, onChange: () => onTerminalLeftClickCopyOnSelectionChange(!terminalLeftClickCopyOnSelection) },
    terminalTabDoubleClickActionEnabled: { checked: terminalTabDoubleClickActionEnabled, onChange: () => onTerminalTabDoubleClickActionEnabledChange(!terminalTabDoubleClickActionEnabled) },
    rememberWorkspace: { checked: rememberWorkspace, onChange: onToggleRememberWorkspace },
    updateUseProxy: { checked: updateUseProxy, onChange: onToggleUpdateUseProxy },
    webviewGpuDisabled: { checked: webviewGpuDisabled, onChange: onToggleWebviewGpuDisabled },
  };
  const selectBindings: Record<string, SelectBinding> = {
    language: {
      value: language,
      onChange: (val) => {
        onLanguageChange({ target: { value: val } } as React.ChangeEvent<HTMLSelectElement>);
      },
      width: 200,
      options: availableLanguages.map((item) => ({ value: item.code, label: item.label })),
    },
    windowCloseAction: {
      value: windowCloseAction,
      onChange: (val) => onWindowCloseActionChange(val),
      width: 160,
      options: [
        { value: 'ask', label: $t('每次询问') },
        { value: 'quit', label: $t('直接退出') },
        { value: 'tray', label: $t('最小化到托盘') },
      ],
    },
  };
  const radioBindings: Record<string, { value: string; onChange: (value: string) => void }> = {
    terminalRightClickPasteMode: { value: terminalRightClickPasteMode, onChange: onTerminalRightClickPasteModeChange },
    terminalLeftClickCopyOnSelectionMode: { value: terminalLeftClickCopyOnSelectionMode, onChange: onTerminalLeftClickCopyOnSelectionModeChange },
    terminalTabDoubleClickAction: { value: terminalTabDoubleClickAction, onChange: onTerminalTabDoubleClickActionChange },
    workspacePersistenceLevel: { value: workspacePersistenceLevel, onChange: onWorkspacePersistenceLevelChange },
  };
  const shouldRenderNode = (node: SettingsDefinitionNode) => {
    if (node.id === settingsData.fields.webviewGpu.id) {
      return supportsWebviewGpuDisable;
    }
    if (node.type === 'conditional') {
      return fieldValuesById[node.when?.field ?? ''] === node.when?.equals;
    }
    return true;
  };
  const renderFieldAction = (node: SettingsDefinitionNode): React.ReactNode => {
    if (node.control === 'toggle') {
      const binding = toggleBindings[node.stateKey || ''];
      if (!binding) {
        return null;
      }
      return <ToggleSwitch checked={binding.checked} onChange={binding.onChange} />;
    }
    if (node.control === 'select') {
      const binding = selectBindings[node.stateKey || ''];
      if (!binding) {
        return null;
      }
      const selectId = `general-${(node.stateKey || '').replace(/[A-Z]/g, (m: string) => m.toLowerCase())}`;
      return (
        <Select
          id={selectId}
          name={selectId}
          width={binding.width}
          value={binding.value}
          onChange={binding.onChange}
          options={binding.options}
        />
      );
    }
    return null;
  };
  const renderNode = (node: SettingsDefinitionNode): React.ReactElement[] => {
    if (!shouldRenderNode(node)) {
      return [];
    }
    if (node.type === 'panel' || node.type === 'conditional') {
      return (node.children || []).flatMap((child: SettingsDefinitionNode) => renderNode(child));
    }
    if (node.type === 'subsection') {
      const children = (node.children || []).flatMap((child: SettingsDefinitionNode) => renderNode(child));
      if (children.length === 0) {
        return [];
      }
      return [(
        <div key={node.id} className="grid gap-1.5">
          <div className="px-1 pt-1 text-sm font-semibold text-secondary">{node.titleKey ? $t(node.titleKey) : ''}</div>
          <div className="overflow-hidden rounded-[var(--radius-sm)] border border-line-subtle bg-overlay px-3">
            {children.map((item, index) => (
              <Fragment key={`${node.id}-${index}`}>
                {index > 0 ? <SettingsDivider /> : null}
                {item}
              </Fragment>
            ))}
          </div>
        </div>
      )];
    }
    if (node.type === 'field') {
      const action = renderFieldAction(node);
      if (!action) {
        return [];
      }
      return [<SettingRow key={node.id} definition={node} action={action} />];
    }
    if (node.type === 'field-group') {
      const binding = radioBindings[node.stateKey || ''];
      if (!binding) {
        return [];
      }
      return [(
        <SettingsField key={node.id} definition={node}>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-2">
            {(node.children || []).map((option: SettingsDefinitionNode) => (
              <RadioOption
                key={option.id}
                definition={option}
                selected={binding.value === option.value}
                label={option.titleKey ? $t(option.titleKey) : ''}
                description={option.descriptionKey ? $t(option.descriptionKey) : ''}
                onClick={() => binding.onChange(String(option.value ?? ''))}
              />
            ))}
          </div>
        </SettingsField>
      )];
    }
    return [];
  };

  return (
    <SettingsTabRoot>
      {(generalTabNode.children || []).map((sectionNode) => {
        if (sectionNode.id === settingsData.sections.rendering!.id && !supportsWebviewGpuDisable) {
          return null;
        }
        const renderedItems = (sectionNode.children || []).flatMap((node) => renderNode(node));
        if (renderedItems.length === 0) {
          return null;
        }
        return (
          <div key={sectionNode.id}>
            <SettingsSectionTitle definition={sectionNode} />
            <SettingsPanel>
              {renderedItems.map((item, index) => (
                <Fragment key={`${sectionNode.id}-${index}`}>
                  {index > 0 ? <SettingsDivider /> : null}
                  {item}
                </Fragment>
              ))}
            </SettingsPanel>
          </div>
        );
      })}
    </SettingsTabRoot>
  );
}
