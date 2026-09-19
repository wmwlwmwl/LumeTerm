import { useEffect, useState } from 'react';
import * as AppGo from '../../../../wailsjs/go/wailsapp/App.js';
import { t as $t } from '../../../i18n.ts';
import { warnDev } from '../../../utils/devLog';

type AddToast = (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;

export const DEFAULT_FILE_MANAGER_DOWNLOAD_DIR = '${APP_DIR}\\download';

export function resolveFileManagerDownloadDirPreview(template: string, programDirectory: string) {
  const baseDir = String(programDirectory || '').trim();
  const rawTemplate = String(template || '').trim() || DEFAULT_FILE_MANAGER_DOWNLOAD_DIR;
  const separator = baseDir.includes('\\') ? '\\' : '/';
  const replaced = rawTemplate
    .replace(/\$\{APP_DIR\}/g, baseDir)
    .replace(/%APP_DIR%/g, baseDir)
    .replace(/[\\/]+/g, separator);
  if (!replaced) {
    return '';
  }
  if (/^(?:[A-Za-z]:[\\/]|\\\\|\/)/.test(replaced) || !baseDir) {
    return replaced;
  }
  return `${baseDir}${baseDir.endsWith('\\') || baseDir.endsWith('/') ? '' : separator}${replaced}`;
}

/** 文件管理器/传输调优设置：全部 localStorage/后端直写型开关与数值项 */
export function useFileManagerSettings({ addToast }: { addToast: AddToast }) {
  const [programDirectory, setProgramDirectory] = useState('');
  const [fileManagerCompressedTransfer, setFileManagerCompressedTransfer] = useState(localStorage.getItem('fileManagerCompressedTransfer') !== 'false');
  const [fileManagerAutoOpenTransferQueue, setFileManagerAutoOpenTransferQueue] = useState(localStorage.getItem('fileManagerAutoOpenTransferQueue') !== 'false');
  const [fileManagerShowTabIcons, setFileManagerShowTabIcons] = useState(localStorage.getItem('fileManagerShowTabIcons') !== 'false');
  const [fileManagerHideTabCloseButton, setFileManagerHideTabCloseButton] = useState(localStorage.getItem('fileManagerHideTabCloseButton') === 'true');
  const [fileManagerSharedPinnedTabs, setFileManagerSharedPinnedTabs] = useState(localStorage.getItem('fileManagerSharedPinnedTabs') === 'true');
  const [fileManagerLayoutMode, setFileManagerLayoutMode] = useState(localStorage.getItem('fileManagerLayoutMode') === 'sidebar_dual' ? 'sidebar_dual' : 'classic');
  const [fileManagerDualPaneDragTransferEnabled, setFileManagerDualPaneDragTransferEnabled] = useState(localStorage.getItem('fileManagerDualPaneDragTransferEnabled') !== 'false');
  const [fileManagerDualPaneDragPromptOnDirectory, setFileManagerDualPaneDragPromptOnDirectory] = useState(localStorage.getItem('fileManagerDualPaneDragPromptOnDirectory') !== 'false');
  const [fileManagerDualPaneDragInvertModifier, setFileManagerDualPaneDragInvertModifier] = useState(localStorage.getItem('fileManagerDualPaneDragInvertModifier') === 'true');
  const [fileManagerInitialPathMode, setFileManagerInitialPathMode] = useState(localStorage.getItem('fileManagerInitialPathMode') || 'session_initial_path');
  const [fileManagerNewTabPathMode, setFileManagerNewTabPathMode] = useState(localStorage.getItem('fileManagerNewTabPathMode') || 'inherit_current');
  const [fileManagerAskDownloadEveryTime, setFileManagerAskDownloadEveryTime] = useState(localStorage.getItem('fileManagerAskDownloadEveryTime') === 'true');
  const [fileManagerDownloadConflictStrategy, setFileManagerDownloadConflictStrategy] = useState(localStorage.getItem('fileManagerDownloadConflictStrategy') || 'auto_rename');
  const [fileManagerDownloadConflictDiffBySize, setFileManagerDownloadConflictDiffBySize] = useState(localStorage.getItem('fileManagerDownloadConflictDiffBySize') !== 'false');
  const [fileManagerDownloadConflictDiffByMtime, setFileManagerDownloadConflictDiffByMtime] = useState(localStorage.getItem('fileManagerDownloadConflictDiffByMtime') !== 'false');
  const [fileManagerDownloadRenameSuffixMode, setFileManagerDownloadRenameSuffixMode] = useState(localStorage.getItem('fileManagerDownloadRenameSuffixMode') || 'sequence');
  const [fileManagerDownloadDefaultDir, setFileManagerDownloadDefaultDir] = useState(localStorage.getItem('fileManagerDownloadDefaultDir') || DEFAULT_FILE_MANAGER_DOWNLOAD_DIR);
  const [fileManagerUploadChunkSizeKiB, setFileManagerUploadChunkSizeKiB] = useState(localStorage.getItem('fileManagerUploadChunkSizeKiB') || '256');
  const [fileManagerUploadMaxFiles, setFileManagerUploadMaxFiles] = useState(localStorage.getItem('fileManagerUploadMaxFiles') || '6');
  const [fileManagerUploadMaxChunksPerFile, setFileManagerUploadMaxChunksPerFile] = useState(localStorage.getItem('fileManagerUploadMaxChunksPerFile') || '8');
  const [fileManagerUploadGlobalInflightLimit, setFileManagerUploadGlobalInflightLimit] = useState(localStorage.getItem('fileManagerUploadGlobalInflightLimit') || '24');
  const [transferMaxPacketKiB, setTransferMaxPacketKiB] = useState('128');
  const [transferMaxRequestsPerFile, setTransferMaxRequestsPerFile] = useState('16');
  const [transferConcurrentWrites, setTransferConcurrentWrites] = useState(true);
  const [transferApplyToSharedClient, setTransferApplyToSharedClient] = useState(true);
  const [fileManagerChmodAutoApplyLastSettings, setFileManagerChmodAutoApplyLastSettings] = useState(false);
  const [fileManagerDoubleClickUncompressArchive, setFileManagerDoubleClickUncompressArchive] = useState(false);
  const [fileManagerSmartUncompressConflictStrategy, setFileManagerSmartUncompressConflictStrategy] = useState('auto_rename');
  const [fileManagerAutoRefreshDisabled, setFileManagerAutoRefreshDisabled] = useState(false);
  const [fileManagerMaxEditSizeMB, setFileManagerMaxEditSizeMB] = useState<number | string>(5);
  const [fileManagerDefaultOpenMode, setFileManagerDefaultOpenMode] = useState(() => {
    const mode = localStorage.getItem('fileManagerDefaultOpenMode') || 'builtin';
    return ['builtin', 'system', 'external'].includes(mode) ? mode : 'builtin';
  });
  const [fileManagerPreferredExternalApp, setFileManagerPreferredExternalApp] = useState(
    () => (localStorage.getItem('fileEditorPreferredApp') || '').trim(),
  );

  useEffect(() => {
    let cancelled = false;

    Promise.resolve(window?.go?.wailsapp?.App?.GetProgramDirectory?.())
      .then((dir) => {
        if (!cancelled && dir) setProgramDirectory(dir);
      })
      .catch(() => {});

    AppGo.GetChmodDialogSettings()
      .then((settings) => {
        if (cancelled || !settings) return;
        setFileManagerChmodAutoApplyLastSettings(settings.autoApplyLastSettings === true);
      })
      .catch(() => {});

    Promise.resolve(window?.go?.wailsapp?.App?.GetFileManagerSettings?.())
      .then((settings) => {
        if (cancelled || !settings) return;
        setFileManagerDoubleClickUncompressArchive(settings.doubleClickUncompressArchive === true);
        setFileManagerSmartUncompressConflictStrategy(
          settings.smartUncompressConflictStrategy === 'overwrite' || settings.smartUncompressConflictStrategy === 'prompt'
            ? settings.smartUncompressConflictStrategy
            : 'auto_rename'
        );
        setFileManagerAutoRefreshDisabled(settings.autoRefreshDisabled === true);
        if (Number.isFinite(Number(settings.maxEditSizeMB)) && Number(settings.maxEditSizeMB) >= 1) {
          setFileManagerMaxEditSizeMB(Number(settings.maxEditSizeMB));
        }
        if (Number.isFinite(Number(settings.transferMaxPacketKiB)) && Number(settings.transferMaxPacketKiB) > 0) {
          setTransferMaxPacketKiB(String(settings.transferMaxPacketKiB));
        }
        if (Number.isFinite(Number(settings.transferMaxRequestsPerFile)) && Number(settings.transferMaxRequestsPerFile) > 0) {
          setTransferMaxRequestsPerFile(String(settings.transferMaxRequestsPerFile));
        }
        setTransferConcurrentWrites(settings.transferConcurrentWrites !== false);
        setTransferApplyToSharedClient(settings.transferApplyToSharedClient !== false);
      })
      .catch(() => {});

    return () => { cancelled = true; };
  }, []);

  const handleToggleFileManagerCompressedTransfer = () => {
    const next = !fileManagerCompressedTransfer;
    setFileManagerCompressedTransfer(next);
    if (next) localStorage.removeItem('fileManagerCompressedTransfer');
    else localStorage.setItem('fileManagerCompressedTransfer', 'false');
  };
  const handleToggleFileManagerAutoOpenTransferQueue = () => {
    const next = !fileManagerAutoOpenTransferQueue;
    setFileManagerAutoOpenTransferQueue(next);
    if (next) localStorage.removeItem('fileManagerAutoOpenTransferQueue');
    else localStorage.setItem('fileManagerAutoOpenTransferQueue', 'false');
  };
  const handleToggleFileManagerShowTabIcons = () => {
    const next = !fileManagerShowTabIcons;
    setFileManagerShowTabIcons(next);
    if (next) localStorage.removeItem('fileManagerShowTabIcons');
    else localStorage.setItem('fileManagerShowTabIcons', 'false');
    window.dispatchEvent(new CustomEvent('file-manager-show-tab-icons-changed', { detail: next }));
  };
  const handleToggleFileManagerHideTabCloseButton = () => {
    const next = !fileManagerHideTabCloseButton;
    setFileManagerHideTabCloseButton(next);
    if (next) localStorage.setItem('fileManagerHideTabCloseButton', 'true');
    else localStorage.removeItem('fileManagerHideTabCloseButton');
    window.dispatchEvent(new CustomEvent('file-manager-hide-tab-close-button-changed', { detail: next }));
  };
  const handleToggleFileManagerSharedPinnedTabs = () => {
    const next = !fileManagerSharedPinnedTabs;
    setFileManagerSharedPinnedTabs(next);
    if (next) localStorage.setItem('fileManagerSharedPinnedTabs', 'true');
    else localStorage.removeItem('fileManagerSharedPinnedTabs');
    window.dispatchEvent(new CustomEvent('file-manager-shared-pinned-tabs-changed', { detail: next }));
  };
  const handleFileManagerLayoutModeChange = (value: string) => {
    const next = value === 'sidebar_dual' ? 'sidebar_dual' : 'classic';
    setFileManagerLayoutMode(next);
    if (next === 'classic') localStorage.removeItem('fileManagerLayoutMode');
    else localStorage.setItem('fileManagerLayoutMode', next);
    window.dispatchEvent(new CustomEvent('file-manager-layout-mode-changed', { detail: next }));
  };
  const handleToggleFileManagerDualPaneDragTransferEnabled = () => {
    const next = !fileManagerDualPaneDragTransferEnabled;
    setFileManagerDualPaneDragTransferEnabled(next);
    if (next) localStorage.removeItem('fileManagerDualPaneDragTransferEnabled');
    else localStorage.setItem('fileManagerDualPaneDragTransferEnabled', 'false');
    window.dispatchEvent(new CustomEvent('file-manager-dual-pane-drag-transfer-enabled-changed', { detail: next }));
  };
  const handleToggleFileManagerDualPaneDragPromptOnDirectory = () => {
    const next = !fileManagerDualPaneDragPromptOnDirectory;
    setFileManagerDualPaneDragPromptOnDirectory(next);
    if (next) localStorage.removeItem('fileManagerDualPaneDragPromptOnDirectory');
    else localStorage.setItem('fileManagerDualPaneDragPromptOnDirectory', 'false');
    window.dispatchEvent(new CustomEvent('file-manager-dual-pane-drag-prompt-on-directory-changed', { detail: next }));
  };
  const handleToggleFileManagerDualPaneDragInvertModifier = () => {
    const next = !fileManagerDualPaneDragInvertModifier;
    setFileManagerDualPaneDragInvertModifier(next);
    if (next) localStorage.setItem('fileManagerDualPaneDragInvertModifier', 'true');
    else localStorage.removeItem('fileManagerDualPaneDragInvertModifier');
    window.dispatchEvent(new CustomEvent('file-manager-dual-pane-drag-invert-modifier-changed', { detail: next }));
  };
  const handleFileManagerInitialPathModeChange = (value: string) => {
    setFileManagerInitialPathMode(value);
    localStorage.setItem('fileManagerInitialPathMode', value);
  };
  const handleFileManagerNewTabPathModeChange = (value: string) => {
    setFileManagerNewTabPathMode(value);
    localStorage.setItem('fileManagerNewTabPathMode', value);
  };
  const handleToggleFileManagerAskDownloadEveryTime = () => {
    const next = !fileManagerAskDownloadEveryTime;
    setFileManagerAskDownloadEveryTime(next);
    if (next) localStorage.setItem('fileManagerAskDownloadEveryTime', 'true');
    else localStorage.removeItem('fileManagerAskDownloadEveryTime');
  };
  const handleFileManagerDownloadConflictStrategyChange = (value: string) => {
    setFileManagerDownloadConflictStrategy(value);
    localStorage.setItem('fileManagerDownloadConflictStrategy', value);
  };
  const handleToggleFileManagerDownloadConflictDiffBySize = () => {
    const next = !fileManagerDownloadConflictDiffBySize;
    if (!next && !fileManagerDownloadConflictDiffByMtime) return;
    setFileManagerDownloadConflictDiffBySize(next);
    if (next) localStorage.removeItem('fileManagerDownloadConflictDiffBySize');
    else localStorage.setItem('fileManagerDownloadConflictDiffBySize', 'false');
  };
  const handleToggleFileManagerDownloadConflictDiffByMtime = () => {
    const next = !fileManagerDownloadConflictDiffByMtime;
    if (!next && !fileManagerDownloadConflictDiffBySize) return;
    setFileManagerDownloadConflictDiffByMtime(next);
    if (next) localStorage.removeItem('fileManagerDownloadConflictDiffByMtime');
    else localStorage.setItem('fileManagerDownloadConflictDiffByMtime', 'false');
  };
  const handleFileManagerDownloadRenameSuffixModeChange = (value: string) => {
    setFileManagerDownloadRenameSuffixMode(value);
    localStorage.setItem('fileManagerDownloadRenameSuffixMode', value);
  };
  const handleFileManagerUploadSettingChange = (key: string, setter: (v: string) => void) => (e: React.ChangeEvent<HTMLInputElement>) => {
    const next = e.target.value;
    setter(next);
    if (next === '') localStorage.removeItem(key);
    else localStorage.setItem(key, next);
  };
  const persistTransferTuning = (overrides: Record<string, number | boolean> = {}) => {
    const next = {
      maxPacketKiB: parseInt(transferMaxPacketKiB, 10) || 128,
      maxRequestsPerFile: parseInt(transferMaxRequestsPerFile, 10) || 16,
      concurrentWrites: transferConcurrentWrites,
      applyToSharedClient: transferApplyToSharedClient,
      ...overrides,
    };
    AppGo.SaveTransferTuningSettings(
      next.maxPacketKiB,
      next.maxRequestsPerFile,
      next.concurrentWrites,
      next.applyToSharedClient,
    ).catch(() => {});
  };
  const handleTransferNumberChange = (setter: (v: string) => void, field: 'maxPacketKiB' | 'maxRequestsPerFile') => (e: React.ChangeEvent<HTMLInputElement>) => {
    const next = e.target.value;
    setter(next);
    const parsed = parseInt(next, 10);
    if (Number.isFinite(parsed) && parsed > 0) {
      persistTransferTuning({ [field]: parsed });
    }
  };
  const handleToggleTransferConcurrentWrites = () => {
    const next = !transferConcurrentWrites;
    setTransferConcurrentWrites(next);
    persistTransferTuning({ concurrentWrites: next });
  };
  const handleToggleTransferApplyToSharedClient = () => {
    const next = !transferApplyToSharedClient;
    setTransferApplyToSharedClient(next);
    persistTransferTuning({ applyToSharedClient: next });
  };
  const handleFileManagerDefaultOpenModeChange = (value: string) => {
    const mode = ['builtin', 'system', 'external'].includes(value) ? value : 'builtin';
    setFileManagerDefaultOpenMode(mode);
    if (mode === 'builtin') localStorage.removeItem('fileManagerDefaultOpenMode');
    else localStorage.setItem('fileManagerDefaultOpenMode', mode);
    window.dispatchEvent(new CustomEvent('file-manager-default-open-mode-changed', { detail: mode }));
  };
  const handlePickFileManagerPreferredExternalApp = async () => {
    try {
      const editorPath = await AppGo.SelectExternalEditor();
      if (!editorPath) return;
      const cleaned = String(editorPath || '').trim();
      if (!cleaned) return;
      localStorage.setItem('fileEditorPreferredApp', cleaned);
      setFileManagerPreferredExternalApp(cleaned);
      window.dispatchEvent(new CustomEvent('file-editor-preferred-app-changed', { detail: cleaned }));
    } catch (err) {
      warnDev(err);
    }
  };
  const handleClearFileManagerPreferredExternalApp = () => {
    localStorage.removeItem('fileEditorPreferredApp');
    setFileManagerPreferredExternalApp('');
    window.dispatchEvent(new CustomEvent('file-editor-preferred-app-changed', { detail: '' }));
  };
  const handleToggleFileManagerChmodAutoApplyLastSettings = async () => {
    const next = !fileManagerChmodAutoApplyLastSettings;
    setFileManagerChmodAutoApplyLastSettings(next);
    try {
      const setter = window?.go?.wailsapp?.App?.SetChmodAutoApplyLastSettings;
      if (typeof setter !== 'function') {
        throw new Error($t('应用不可用'));
      }
      await setter(next);
    } catch (err) {
      setFileManagerChmodAutoApplyLastSettings(!next);
      addToast($t('请求失败') + `: ${err}`, 'error');
    }
  };
  const handleToggleFileManagerDoubleClickUncompressArchive = async () => {
    const next = !fileManagerDoubleClickUncompressArchive;
    setFileManagerDoubleClickUncompressArchive(next);
    try {
      const setter = window?.go?.wailsapp?.App?.SetFileManagerDoubleClickUncompressArchive;
      if (typeof setter !== 'function') {
        throw new Error($t('应用不可用'));
      }
      await setter(next);
      window.dispatchEvent(new CustomEvent('file-manager-double-click-uncompress-archive-changed', { detail: next }));
    } catch (err) {
      setFileManagerDoubleClickUncompressArchive(!next);
      addToast($t('请求失败') + `: ${err}`, 'error');
    }
  };
  const handleToggleFileManagerAutoRefreshDisabled = async () => {
    const next = !fileManagerAutoRefreshDisabled;
    setFileManagerAutoRefreshDisabled(next);
    try {
      const setter = window?.go?.wailsapp?.App?.SetFileManagerAutoRefreshDisabled;
      if (typeof setter !== 'function') {
        throw new Error($t('应用不可用'));
      }
      await setter(next);
      window.dispatchEvent(new CustomEvent('file-manager-auto-refresh-disabled-changed', { detail: next }));
    } catch (err) {
      setFileManagerAutoRefreshDisabled(!next);
      addToast($t('请求失败') + `: ${err}`, 'error');
    }
  };
  const handleFileManagerMaxEditSizeChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const raw = e.target.value;
    // 用 Number 而非 parseInt：避免 "12abc" 被解析为 12 而误持久化
    const next = Number(raw);
    // 非法值（空/非数字/越界）只更新 UI 态，不持久化
    if (!Number.isFinite(next) || next < 1 || next > 50) {
      setFileManagerMaxEditSizeMB(raw);
      addToast($t('文件编辑大小上限范围为 1-50 MB'), 'warning');
      return;
    }
    const previous = fileManagerMaxEditSizeMB;
    setFileManagerMaxEditSizeMB(next);
    try {
      const setter = window?.go?.wailsapp?.App?.SetFileManagerMaxEditSize;
      if (typeof setter !== 'function') {
        throw new Error($t('应用不可用'));
      }
      await setter(next);
      window.dispatchEvent(new CustomEvent('file-manager-max-edit-size-changed', { detail: next }));
    } catch (err) {
      setFileManagerMaxEditSizeMB(previous);
      addToast($t('请求失败') + `: ${err}`, 'error');
    }
  };
  const handleFileManagerSmartUncompressConflictStrategyChange = async (value: string) => {
    const next = value === 'overwrite' || value === 'prompt' ? value : 'auto_rename';
    const previous = fileManagerSmartUncompressConflictStrategy;
    setFileManagerSmartUncompressConflictStrategy(next);
    try {
      const setter = window?.go?.wailsapp?.App?.SetFileManagerSmartUncompressConflictStrategy;
      if (typeof setter !== 'function') {
        throw new Error($t('应用不可用'));
      }
      await setter(next);
      window.dispatchEvent(new CustomEvent('file-manager-smart-uncompress-conflict-strategy-changed', { detail: next }));
    } catch (err) {
      setFileManagerSmartUncompressConflictStrategy(previous);
      addToast($t('请求失败') + `: ${err}`, 'error');
    }
  };

  return {
    programDirectory,
    fileManagerCompressedTransfer, handleToggleFileManagerCompressedTransfer,
    fileManagerAutoOpenTransferQueue, handleToggleFileManagerAutoOpenTransferQueue,
    fileManagerShowTabIcons, handleToggleFileManagerShowTabIcons,
    fileManagerHideTabCloseButton, handleToggleFileManagerHideTabCloseButton,
    fileManagerSharedPinnedTabs, handleToggleFileManagerSharedPinnedTabs,
    fileManagerLayoutMode, handleFileManagerLayoutModeChange,
    fileManagerDualPaneDragTransferEnabled, handleToggleFileManagerDualPaneDragTransferEnabled,
    fileManagerDualPaneDragPromptOnDirectory, handleToggleFileManagerDualPaneDragPromptOnDirectory,
    fileManagerDualPaneDragInvertModifier, handleToggleFileManagerDualPaneDragInvertModifier,
    fileManagerChmodAutoApplyLastSettings, handleToggleFileManagerChmodAutoApplyLastSettings,
    fileManagerDoubleClickUncompressArchive, handleToggleFileManagerDoubleClickUncompressArchive,
    fileManagerSmartUncompressConflictStrategy, handleFileManagerSmartUncompressConflictStrategyChange,
    fileManagerAutoRefreshDisabled, handleToggleFileManagerAutoRefreshDisabled,
    fileManagerMaxEditSizeMB, handleFileManagerMaxEditSizeChange,
    fileManagerDefaultOpenMode, handleFileManagerDefaultOpenModeChange,
    fileManagerPreferredExternalApp, handlePickFileManagerPreferredExternalApp, handleClearFileManagerPreferredExternalApp,
    fileManagerInitialPathMode, handleFileManagerInitialPathModeChange,
    fileManagerNewTabPathMode, handleFileManagerNewTabPathModeChange,
    fileManagerAskDownloadEveryTime, handleToggleFileManagerAskDownloadEveryTime,
    fileManagerDownloadConflictStrategy, handleFileManagerDownloadConflictStrategyChange,
    fileManagerDownloadConflictDiffBySize, handleToggleFileManagerDownloadConflictDiffBySize,
    fileManagerDownloadConflictDiffByMtime, handleToggleFileManagerDownloadConflictDiffByMtime,
    fileManagerDownloadRenameSuffixMode, handleFileManagerDownloadRenameSuffixModeChange,
    fileManagerDownloadDefaultDir, setFileManagerDownloadDefaultDir,
    fileManagerUploadChunkSizeKiB, setFileManagerUploadChunkSizeKiB,
    fileManagerUploadMaxFiles, setFileManagerUploadMaxFiles,
    fileManagerUploadMaxChunksPerFile, setFileManagerUploadMaxChunksPerFile,
    fileManagerUploadGlobalInflightLimit, setFileManagerUploadGlobalInflightLimit,
    handleFileManagerUploadSettingChange,
    transferMaxPacketKiB, handleTransferNumberChange, setTransferMaxPacketKiB,
    transferMaxRequestsPerFile, setTransferMaxRequestsPerFile,
    transferConcurrentWrites, handleToggleTransferConcurrentWrites,
    transferApplyToSharedClient, handleToggleTransferApplyToSharedClient,
  };
}
