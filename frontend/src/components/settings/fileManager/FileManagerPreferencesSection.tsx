import { t as $t } from '../../../i18n.ts';
import { Button } from '../../ui';
import { RadioOption, ToggleSwitch, SettingRow, SettingsDivider, SettingsPanel, SettingsSectionTitle } from '../SharedComponents';
import { settings } from '../settingDefinitions';
import type { FileManagerTabProps } from './fileManagerTabTypes';

export default function FileManagerPreferencesSection({
  fileManagerCompressedTransfer,
  onToggleFileManagerCompressedTransfer,
  fileManagerAutoOpenTransferQueue,
  onToggleFileManagerAutoOpenTransferQueue,
  fileManagerShowTabIcons,
  onToggleFileManagerShowTabIcons,
  fileManagerHideTabCloseButton,
  onToggleFileManagerHideTabCloseButton,
  fileManagerSharedPinnedTabs,
  onToggleFileManagerSharedPinnedTabs,
  fileManagerLayoutMode = 'classic',
  onFileManagerLayoutModeChange,
  fileManagerDualPaneDragTransferEnabled,
  onToggleFileManagerDualPaneDragTransferEnabled,
  fileManagerDualPaneDragPromptOnDirectory,
  onToggleFileManagerDualPaneDragPromptOnDirectory,
  fileManagerDualPaneDragInvertModifier,
  onToggleFileManagerDualPaneDragInvertModifier,
  fileManagerChmodAutoApplyLastSettings,
  onToggleFileManagerChmodAutoApplyLastSettings,
  fileManagerDoubleClickUncompressArchive,
  onToggleFileManagerDoubleClickUncompressArchive,
  fileManagerSmartUncompressConflictStrategy,
  onFileManagerSmartUncompressConflictStrategyChange,
  fileManagerAutoRefreshDisabled = false,
  onToggleFileManagerAutoRefreshDisabled,
  fileManagerMaxEditSizeMB = 5,
  onFileManagerMaxEditSizeChange,
  fileManagerDefaultOpenMode = 'builtin',
  onFileManagerDefaultOpenModeChange,
  fileManagerPreferredExternalApp = '',
  onPickFileManagerPreferredExternalApp,
  onClearFileManagerPreferredExternalApp,
  fileManagerInitialPathMode,
  onFileManagerInitialPathModeChange,
  fileManagerNewTabPathMode,
  onFileManagerNewTabPathModeChange,
}: FileManagerTabProps) {
  // settingDefinitions.ts 已类型化，直接使用 settings 注册表
  const fmSettings = settings.fileManager;
  return (
    <div>
      <SettingsSectionTitle definition={fmSettings.sections.preferences} />
      <SettingsPanel>
        <SettingRow
          definition={fmSettings.fields.compressedTransfer}
          description={$t('多文件或文件夹上传时先在本机打包为 tar.gz，上传后远端自动解压')}
          action={<ToggleSwitch checked={fileManagerCompressedTransfer} onChange={onToggleFileManagerCompressedTransfer} />}
        />
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.autoOpenTransferQueue}
          description={$t('上传或下载新建传输任务后自动展开传输队列面板')}
          action={<ToggleSwitch checked={fileManagerAutoOpenTransferQueue} onChange={onToggleFileManagerAutoOpenTransferQueue} />}
        />
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.showTabIcons}
          description={$t('关闭后只隐藏目录图标,仍显示固定图标')}
          action={<ToggleSwitch checked={fileManagerShowTabIcons} onChange={onToggleFileManagerShowTabIcons} />}
        />
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.hideTabCloseButton}
          description={$t('开启后,文件资源管理器标签页不显示关闭图标按钮,仅可双击关闭')}
          action={<ToggleSwitch checked={fileManagerHideTabCloseButton} onChange={onToggleFileManagerHideTabCloseButton} />}
        />
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.sharedPinnedTabs}
          description={$t('开启后,同一服务器下所有终端共用一组文件资源管理器固定标签')}
          action={<ToggleSwitch checked={fileManagerSharedPinnedTabs} onChange={onToggleFileManagerSharedPinnedTabs} />}
        />
        <SettingsDivider />
        <div data-settings-field-id={fmSettings.fields.layoutMode.id} className="flex flex-col gap-2">
          <div className="text-base text-primary">{$t('文件资源管理器视图')}</div>
          <div className="text-xs text-tertiary">{$t('选择顶部标签单栏,或左侧标签双面板视图')}</div>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-2">
            <RadioOption
              definition={fmSettings.fields.classicLayout}
              selected={fileManagerLayoutMode === 'classic'}
              label={$t('经典顶部标签')}
              description={$t('保留当前顶部横向标签栏与单内容区')}
              onClick={() => onFileManagerLayoutModeChange?.('classic')}
            />
            <RadioOption
              definition={fmSettings.fields.dualLayout}
              selected={fileManagerLayoutMode === 'sidebar_dual'}
              label={$t('左侧标签双面板')}
              description={$t('左侧显示历史标签,主内容区同时显示左右两个文件列表')}
              onClick={() => onFileManagerLayoutModeChange?.('sidebar_dual')}
            />
          </div>
          {fileManagerLayoutMode === 'sidebar_dual' ? (
            <div className="flex flex-col gap-2.5 p-3 rounded-md border border-line bg-raised">
              <div className="text-sm text-secondary">{$t('仅在左侧标签双面板视图中生效')}</div>
              <SettingRow
                definition={fmSettings.fields.dualDragTransfer}
                description={$t('开启后,可在双栏之间直接拖拽文件;默认复制,按住 Ctrl 为移动')}
                action={<ToggleSwitch checked={fileManagerDualPaneDragTransferEnabled} onChange={onToggleFileManagerDualPaneDragTransferEnabled} />}
              />
              <SettingsDivider margin="2px 0" />
              <SettingRow
                definition={fmSettings.fields.dualDragPrompt}
                description={$t('开启后,拖拽内容包含文件夹时先确认是否继续')}
                action={<ToggleSwitch checked={fileManagerDualPaneDragPromptOnDirectory} onChange={onToggleFileManagerDualPaneDragPromptOnDirectory} />}
              />
              <SettingsDivider margin="2px 0" />
              <SettingRow
                definition={fmSettings.fields.dualDragInvert}
                description={$t('开启后,默认移动,按住 Ctrl 为复制')}
                action={<ToggleSwitch checked={fileManagerDualPaneDragInvertModifier} onChange={onToggleFileManagerDualPaneDragInvertModifier} />}
              />
            </div>
          ) : null}
        </div>
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.chmodAutoApply}
          description={$t('开启后,修改权限弹窗会默认套用上次保存的权限模式和包含子目录选项')}
          action={<ToggleSwitch checked={fileManagerChmodAutoApplyLastSettings} onChange={onToggleFileManagerChmodAutoApplyLastSettings} />}
        />
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.doubleClickUncompress}
          description={$t('开启后,双击压缩包会直接解压;右键“解压”也会使用同样的智能解压规则')}
          action={<ToggleSwitch checked={fileManagerDoubleClickUncompressArchive} onChange={onToggleFileManagerDoubleClickUncompressArchive} />}
        />
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.autoRefresh}
          description={$t('在终端执行命令后、或切回文件管理器时自动刷新当前目录。关闭可减少对远程服务器的请求')}
          action={<ToggleSwitch checked={!fileManagerAutoRefreshDisabled} onChange={onToggleFileManagerAutoRefreshDisabled} />}
        />
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.maxEditSize}
          description={$t('双击或用编辑器打开文件时的最大文件大小，超过将拒绝打开以避免卡顿或内存溢出。范围 1-50，默认 5')}
          action={<div className="w-40"><input id="fm-max-edit-size" name="fm-max-edit-size" className="input w-full text-right" type="number" min={1} max={50} autoComplete="off" value={fileManagerMaxEditSizeMB} onChange={onFileManagerMaxEditSizeChange} /></div>}
        />
        <SettingsDivider />
        <div data-settings-field-id={fmSettings.fields.uncompressConflict.id} className="flex flex-col gap-2">
          <div className="text-base text-primary">{$t('智能解压遇到同名文件夹时')}</div>
          <div className="text-xs text-tertiary">{$t('如果准备解压到“压缩包同名文件夹”,但这个文件夹已经存在,就按这里处理')}</div>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-2">
            <RadioOption
              definition={fmSettings.fields.uncompressOverwrite}
              selected={fileManagerSmartUncompressConflictStrategy === 'overwrite'}
              label={$t('覆盖')}
              description={$t('继续解压到现有同名文件夹,里面同名文件会被替换')}
              onClick={() => onFileManagerSmartUncompressConflictStrategyChange?.('overwrite')}
            />
            <RadioOption
              definition={fmSettings.fields.uncompressRename}
              selected={fileManagerSmartUncompressConflictStrategy === 'auto_rename'}
              label={$t('自动重命名')}
              description={$t('保留已有文件夹,自动新建“压缩包名 (2)”这类文件夹')}
              onClick={() => onFileManagerSmartUncompressConflictStrategyChange?.('auto_rename')}
            />
            <RadioOption
              definition={fmSettings.fields.uncompressPrompt}
              selected={fileManagerSmartUncompressConflictStrategy === 'prompt'}
              label={$t('每次都询问我')}
              description={$t('每次遇到同名文件夹时都弹窗让我选')}
              onClick={() => onFileManagerSmartUncompressConflictStrategyChange?.('prompt')}
            />
          </div>
        </div>
        <SettingsDivider />
        <div data-settings-field-id={fmSettings.fields.defaultOpenMode.id} className="flex flex-col gap-2">
          <div className="text-base text-primary">{$t('打开文件默认方式')}</div>
          <div className="text-xs text-tertiary">{$t('双击或点“编辑”时的默认打开方式；编辑器内仍可随时切换到系统/指定编辑器')}</div>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-2">
            <RadioOption
              definition={fmSettings.fields.builtinEditor}
              selected={fileManagerDefaultOpenMode === 'builtin'}
              label={$t('内置编辑器')}
              description={$t('使用 LumeTerm 内置编辑器打开，支持高亮与保存回远端')}
              onClick={() => onFileManagerDefaultOpenModeChange?.('builtin')}
            />
            <RadioOption
              definition={fmSettings.fields.systemEditor}
              selected={fileManagerDefaultOpenMode === 'system'}
              label={$t('系统编辑器')}
              description={$t('用系统默认程序打开临时文件，保存后自动同步回远端')}
              onClick={() => onFileManagerDefaultOpenModeChange?.('system')}
            />
            <RadioOption
              definition={fmSettings.fields.externalEditor}
              selected={fileManagerDefaultOpenMode === 'external'}
              label={$t('指定外部编辑器')}
              description={$t('始终使用你选择的编辑器程序打开，例如 VS Code / Notepad++')}
              onClick={() => onFileManagerDefaultOpenModeChange?.('external')}
            />
          </div>
          {fileManagerDefaultOpenMode === 'external' ? (
            <div className="flex flex-col gap-2 p-3 rounded-md border border-line bg-raised">
              <div className="text-sm text-secondary">
                {$t('当前外部编辑器')}：{fileManagerPreferredExternalApp ? fileManagerPreferredExternalApp : $t('未选择（首次打开时会提示选择）')}
              </div>
              <div className="flex gap-2 flex-wrap">
                <Button className="px-2.5 py-1.5" onClick={() => onPickFileManagerPreferredExternalApp?.()}>
                  {fileManagerPreferredExternalApp ? $t('更换外部编辑器') : $t('选择外部编辑器')}
                </Button>
                {fileManagerPreferredExternalApp ? (
                  <Button variant="ghost" className="px-2.5 py-1.5" onClick={() => onClearFileManagerPreferredExternalApp?.()}>
                    {$t('清除')}
                  </Button>
                ) : null}
              </div>
            </div>
          ) : null}
        </div>
        <SettingsDivider />
        <div data-settings-field-id={fmSettings.fields.initialPath.id} className="flex flex-col gap-2">
          <div className="text-base text-primary">{$t('进入服务器默认路径')}</div>
          <div className="text-xs text-tertiary">{$t('首次打开文件管理器时的初始目录来源；优先使用配置中的文件管理器初始目录，未填写时使用当前终端启动目录，最后回退到 /root 和根目录')}</div>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-2">
            <RadioOption
              definition={fmSettings.fields.sessionInitialPath}
              selected={fileManagerInitialPathMode === 'session_initial_path'}
              label={$t('服务器初始目录')}
              description={$t('优先使用当前服务器配置中的文件管理器初始目录，未填写时使用当前终端启动目录')}
              onClick={() => onFileManagerInitialPathModeChange('session_initial_path')}
            />
            <RadioOption
              definition={fmSettings.fields.rootInitialPath}
              selected={fileManagerInitialPathMode === 'root'}
              label={$t('根目录')}
              description={$t('首次进入时从根目录开始')}
              onClick={() => onFileManagerInitialPathModeChange('root')}
            />
            <RadioOption
              definition={fmSettings.fields.terminalInitialPath}
              selected={fileManagerInitialPathMode === 'terminal_cwd'}
              label={$t('当前终端目录')}
              description={$t('使用当前终端最近一次上报的工作目录')}
              onClick={() => onFileManagerInitialPathModeChange('terminal_cwd')}
            />
          </div>
        </div>
        <SettingsDivider />
        <div data-settings-field-id={fmSettings.fields.newTabPath.id} className="flex flex-col gap-2">
          <div className="text-base text-primary">{$t('新建标签默认路径')}</div>
          <div className="text-xs text-tertiary">{$t('手动新建文件管理器标签时的初始目录来源；如果首选路径不可用，会依次回退到当前标签目录和根目录')}</div>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-2">
            <RadioOption
              definition={fmSettings.fields.inheritCurrentPath}
              selected={fileManagerNewTabPathMode === 'inherit_current'}
              label={$t('继承当前标签所在目录')}
              description={$t('新标签默认打开当前标签所在目录')}
              onClick={() => onFileManagerNewTabPathModeChange('inherit_current')}
            />
            <RadioOption
              definition={fmSettings.fields.newTabRootPath}
              selected={fileManagerNewTabPathMode === 'root'}
              label={$t('根目录')}
              description={$t('新标签始终从根目录开始')}
              onClick={() => onFileManagerNewTabPathModeChange('root')}
            />
            <RadioOption
              definition={fmSettings.fields.newTabSessionPath}
              selected={fileManagerNewTabPathMode === 'session_initial_path'}
              label={$t('服务器初始目录')}
              description={$t('优先使用当前服务器配置中的文件管理器初始目录，未填写时使用当前终端启动目录')}
              onClick={() => onFileManagerNewTabPathModeChange('session_initial_path')}
            />
            <RadioOption
              definition={fmSettings.fields.newTabTerminalPath}
              selected={fileManagerNewTabPathMode === 'terminal_cwd'}
              label={$t('当前终端目录')}
              description={$t('使用当前终端最近一次上报的工作目录')}
              onClick={() => onFileManagerNewTabPathModeChange('terminal_cwd')}
            />
          </div>
        </div>
      </SettingsPanel>
    </div>
  );
}
