import { useCallback, useEffect, useState } from 'react'
import { CheckCircle2, CircleOff, Package } from 'lucide-react'
import { t as $t } from '../../i18n.ts'
import { DEFAULT_RUNTIME_ENVIRONMENT_STATUS, getRuntimeEnvironmentStatus, installRuntimeEnvironment } from './runtimeEnvironmentBridge.ts'
import { cn } from '../../utils/cn.ts'
import { Button } from '../ui'
import { SettingsPanel, SettingsSectionTitle, SettingsTabRoot } from './SharedComponents.tsx'
import { settings } from './settingDefinitions'

export default function RuntimeEnvironmentTab() {
  const [runtimeEnvironmentStatus, setRuntimeEnvironmentStatus] = useState(DEFAULT_RUNTIME_ENVIRONMENT_STATUS)
  const [installing, setInstalling] = useState(false)
  // settingDefinitions.ts 已类型化，直接使用 settings 注册表
  const runtimeSettings = settings.runtimeEnvironment;
  const sectionNode = runtimeSettings.sections.environment!
  const uvNode = runtimeSettings.fields.uv!
  const uvBinaryNode = runtimeSettings.fields.uvBinary!

  const refreshRuntimeEnvironmentStatus = useCallback(async () => {
    const status = await getRuntimeEnvironmentStatus()
    setRuntimeEnvironmentStatus(status)
    return status
  }, [])

  useEffect(() => {
    let cancelled = false
    refreshRuntimeEnvironmentStatus()
      .then((status) => {
        if (!cancelled && status) {
          setRuntimeEnvironmentStatus(status)
        }
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [refreshRuntimeEnvironmentStatus])

  const handleInstall = useCallback(async () => {
    if (installing) {
      return
    }
    setInstalling(true)
    try {
      const status = await installRuntimeEnvironment()
      setRuntimeEnvironmentStatus(status)
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error || '')
      if (message.trim()) {
        if (window.lumeDialog?.alert) {
          void window.lumeDialog.alert(message, $t('提示'), { priority: 'settings' })
        } else {
          window.alert(message)
        }
      }
      await refreshRuntimeEnvironmentStatus()
    } finally {
      setInstalling(false)
    }
  }, [installing, refreshRuntimeEnvironmentStatus])

  const ready = runtimeEnvironmentStatus.ready === true

  return (
    <SettingsTabRoot>
      <div>
        <SettingsSectionTitle definition={sectionNode} />
        <div className="text-tertiary text-sm mb-2.5">{$t('管理应用运行所需的二进制工具与运行时依赖。')}</div>
        <SettingsPanel className="flex flex-col gap-3.5">
          <div data-settings-field-id={uvNode.id} className="flex items-start gap-3.5">
            <div className="w-12 h-12 rounded-[14px] bg-[rgba(var(--success-rgb),0.12)] text-accent flex items-center justify-center shrink-0">
              <Package size={24} />
            </div>
            <div className="min-w-0 flex flex-col gap-2 flex-1">
              <div className="flex items-center gap-2.5 flex-wrap">
                <div className="text-primary text-[26px] font-bold leading-none">uv</div>
                <div className="px-2.5 py-0.5 rounded-full border border-line text-secondary text-sm">{$t('内置')}</div>
                <div className={cn('ml-auto inline-flex items-center gap-1.5 text-sm', ready ? 'text-accent' : 'text-secondary')}>
                  {ready ? <CheckCircle2 size={16} /> : <CircleOff size={16} />}
                  <span>{ready ? $t('已就绪') : $t('未就绪')}</span>
                </div>
                {!ready ? (
                  <Button onClick={handleInstall} disabled={installing} className="h-[30px] px-3.5">
                    {installing ? $t('安装中...') : $t('安装')}
                  </Button>
                ) : null}
              </div>
              <div className="text-secondary text-base leading-[1.6]">
                {$t('用于 MCP 服务与依赖安装的 Python 包管理工具。')}
              </div>
            </div>
          </div>
          {ready ? (
            <div data-settings-field-id={uvBinaryNode.id} className="flex flex-col gap-2">
              <div className="text-primary text-base">{$t('uv 可执行文件')}</div>
              <input id="runtime-env-uv-binary" name="runtime-env-uv-binary" autoComplete="off" className="input w-full" type="text" value={runtimeEnvironmentStatus.binaryPath || ''} readOnly />
            </div>
          ) : null}
        </SettingsPanel>
      </div>
    </SettingsTabRoot>
  )
}
