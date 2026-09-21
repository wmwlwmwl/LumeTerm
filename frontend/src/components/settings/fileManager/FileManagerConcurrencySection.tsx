import { t as $t } from '../../../i18n.ts';
import { ToggleSwitch, SettingRow, SettingsDivider, SettingsPanel, SettingsSectionTitle } from '../SharedComponents';
import { settings } from '../settingDefinitions';
import type { FileManagerTabProps } from './fileManagerTabTypes';

export default function FileManagerConcurrencySection({
  fileManagerUploadChunkSizeKiB,
  onFileManagerUploadChunkSizeKiBChange,
  fileManagerUploadMaxFiles,
  onFileManagerUploadMaxFilesChange,
  fileManagerUploadMaxChunksPerFile,
  onFileManagerUploadMaxChunksPerFileChange,
  fileManagerUploadGlobalInflightLimit,
  onFileManagerUploadGlobalInflightLimitChange,
  transferMaxPacketKiB,
  onTransferMaxPacketKiBChange,
  transferMaxRequestsPerFile,
  onTransferMaxRequestsPerFileChange,
  transferConcurrentWrites,
  onToggleTransferConcurrentWrites,
  transferApplyToSharedClient,
  onToggleTransferApplyToSharedClient,
}: FileManagerTabProps) {
  const withDefaultValue = (text: string, value: string) => `${text} ${$t('默认值：{value}，仅影响下一次上传任务', { value })}`;
  const withTransferDefaultValue = (text: string, value: string) => `${text} ${$t('默认值：{value}，仅影响下一次传输任务', { value })}`;
  const renderChannelImpactHint = (text: string) => (
    <span className="block mt-0.5 text-warning">{text}</span>
  );
  const renderWarningDescription = (baseText: string, warningText: string) => (
    <>
      <span>{baseText}</span>
      {renderChannelImpactHint(warningText)}
    </>
  );
  // settingDefinitions.ts 已类型化，直接使用 settings 注册表
  const fmSettings = settings.fileManager;
  return (
    <div>
      <SettingsSectionTitle definition={fmSettings.sections.concurrency} />
      <SettingsPanel>
        <SettingRow
          definition={fmSettings.fields.chunkSize}
          description={withDefaultValue($t('控制单个文件上传时的默认分块大小'), '256 KiB')}
          action={<div className="w-40"><input id="fm-chunk-size" name="fm-chunk-size" className="input w-full text-right" type="number" autoComplete="off" value={fileManagerUploadChunkSizeKiB} onChange={onFileManagerUploadChunkSizeKiBChange} /></div>}
        />
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.maxTransferTasks}
          description={renderWarningDescription(withTransferDefaultValue($t('控制当前会话内同时进行的上传和下载任务数量,每个文件或文件夹都算一个任务'), '6'), $t('增大后可能提高同一会话内的 SFTP/SSH 通道占用'))}
          action={<div className="w-40"><input id="fm-max-transfer-tasks" name="fm-max-transfer-tasks" className="input w-full text-right" type="number" autoComplete="off" value={fileManagerUploadMaxFiles} onChange={onFileManagerUploadMaxFilesChange} /></div>}
        />
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.maxChunksPerFile}
          description={renderWarningDescription(withDefaultValue($t('控制单个文件在同一时间允许并发传输的分块数量'), '8'), $t('在压缩传输或原生单文件传输场景下,增大后可能提高同一会话内的 SFTP/SSH 通道占用'))}
          action={<div className="w-40"><input id="fm-max-chunks-per-file" name="fm-max-chunks-per-file" className="input w-full text-right" type="number" autoComplete="off" value={fileManagerUploadMaxChunksPerFile} onChange={onFileManagerUploadMaxChunksPerFileChange} /></div>}
        />
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.globalInflightLimit}
          description={renderWarningDescription(withDefaultValue($t('控制所有上传任务共享的在途分块总数'), '24'), $t('在前端分块上传场景下,增大后可能提高同一会话内的 SFTP/SSH 通道占用'))}
          action={<div className="w-40"><input id="fm-global-inflight-limit" name="fm-global-inflight-limit" className="input w-full text-right" type="number" autoComplete="off" value={fileManagerUploadGlobalInflightLimit} onChange={onFileManagerUploadGlobalInflightLimitChange} /></div>}
        />
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.maxPacketSize}
          description={withTransferDefaultValue($t('单个 SFTP 数据包的载荷上限,高延迟链路上调大可显著提速;如果服务器不接受会自动回退'), '128 KiB')}
          action={<div className="w-40"><input id="fm-max-packet-size" name="fm-max-packet-size" className="input w-full text-right" type="number" autoComplete="off" value={transferMaxPacketKiB} onChange={onTransferMaxPacketKiBChange} /></div>}
        />
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.requestPipelineDepth}
          description={withTransferDefaultValue($t('单个文件同时保持在链路上的 SFTP 请求数量;实际生效值会按 SSH 通道窗口自动收窄,填写超出窗口的数值不会提速,只会额外占用资源'), '16')}
          action={<div className="w-40"><input id="fm-request-pipeline-depth" name="fm-request-pipeline-depth" className="input w-full text-right" type="number" autoComplete="off" value={transferMaxRequestsPerFile} onChange={onTransferMaxRequestsPerFileChange} /></div>}
        />
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.concurrentWrites}
          description={$t('开启后单次写入内部并行发包,不再逐包等待服务器确认;关闭则退回逐包串行,速度会明显变慢')}
          action={<ToggleSwitch checked={transferConcurrentWrites} onChange={onToggleTransferConcurrentWrites} />}
        />
        <SettingsDivider />
        <SettingRow
          definition={fmSettings.fields.applySharedClient}
          description={$t('开启后下载,文件列表,读写文件都使用同一套调优参数;如果服务器兼容性较差可关闭,仅上传使用调优')}
          action={<ToggleSwitch checked={transferApplyToSharedClient} onChange={onToggleTransferApplyToSharedClient} />}
        />
      </SettingsPanel>
    </div>
  );
}
