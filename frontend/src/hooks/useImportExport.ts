import { useCallback, useState } from 'react';
import * as AppGo from '../../wailsjs/go/wailsapp/App.js';

/** 导出选项 */
export interface ExportOptions {
  serverIds?: string[];
  useEncryption?: boolean;
  password?: string;
}

export interface UseImportExportOptions {
  addToast: (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;
  loadServers: () => Promise<void> | void;
  t: (key: string, vars?: Record<string, unknown>) => string;
  lang: string;
}

export interface UseImportExportResult {
  showImportExportDialog: boolean;
  setShowImportExportDialog: React.Dispatch<React.SetStateAction<boolean>>;
  showExportSelectedDialog: boolean;
  setShowExportSelectedDialog: React.Dispatch<React.SetStateAction<boolean>>;
  exportSelectedIds: string[];
  setExportSelectedIds: React.Dispatch<React.SetStateAction<string[]>>;
  ieBusy: boolean;
  hasRecoveryPassword: boolean;
  handleOpenImportExport: () => Promise<void>;
  handleExport: (opts?: ExportOptions) => Promise<void>;
  handleBatchExport: (ids: string[]) => Promise<void>;
  handleExportSelected: (opts?: ExportOptions) => Promise<void>;
  handleImport: () => Promise<void>;
  handleDownloadTemplate: () => Promise<void>;
}

export default function useImportExport({ addToast, loadServers, t, lang }: UseImportExportOptions): UseImportExportResult {
  const [showImportExportDialog, setShowImportExportDialog] = useState(false);
  const [showExportSelectedDialog, setShowExportSelectedDialog] = useState(false);
  const [exportSelectedIds, setExportSelectedIds] = useState<string[]>([]);
  const [ieBusy, setIeBusy] = useState(false);
  const [hasRecoveryPassword, setHasRecoveryPassword] = useState(false);

  const handleOpenImportExport = useCallback(async () => {
    try {
      setHasRecoveryPassword(!!(await AppGo.HasRecoveryPassword()));
    } catch {
      setHasRecoveryPassword(false);
    }
    setShowImportExportDialog(true);
  }, []);

  const handleExport = useCallback(async (opts?: ExportOptions) => {
    setIeBusy(true);
    try {
      const options = opts || {};
      const serverIds = options.serverIds || [];
      const path = serverIds.length > 0
        ? await AppGo.ExportConnectionsByIDs(serverIds, !!options.useEncryption, options.password || '')
        : await AppGo.ExportConnections(!!options.useEncryption, options.password || '');
      if (path) addToast(t('已导出到 {path}', { path }), 'success');
    } catch (error) {
      addToast(`${t('导出失败')}: ${String(error)}`, 'error');
    } finally {
      setIeBusy(false);
    }
  }, [addToast, t]);

  const handleBatchExport = useCallback(async (ids: string[]) => {
    try {
      setHasRecoveryPassword(!!(await AppGo.HasRecoveryPassword()));
    } catch {
      setHasRecoveryPassword(false);
    }
    setExportSelectedIds(ids);
    setShowExportSelectedDialog(true);
  }, []);

  const handleExportSelected = useCallback(async (opts?: ExportOptions) => {
    setIeBusy(true);
    try {
      const options = opts || {};
      const path = await AppGo.ExportConnectionsByIDs(
        exportSelectedIds,
        !!options.useEncryption,
        options.password || '',
      );
      if (path) {
        addToast(t('已成功导出选择的 {count} 个节点到 {path}', {
          count: exportSelectedIds.length,
          path,
        }), 'success');
        setShowExportSelectedDialog(false);
        setExportSelectedIds([]);
      }
    } catch (error) {
      addToast(`${t('导出失败')}: ${String(error)}`, 'error');
    } finally {
      setIeBusy(false);
    }
  }, [addToast, exportSelectedIds, t]);

  const handleImport = useCallback(async () => {
    setIeBusy(true);
    try {
      let filePath = '';
      try {
        filePath = await AppGo.SelectImportFile();
      } catch (error) {
        addToast(`${t('导入失败')}: ${String(error)}`, 'error');
        return;
      }
      if (!filePath) return;

      const doImport = async (password: string) => {
        const result = await AppGo.ImportConnections(filePath, password);
        if (result && result.total === 0 && result.imported === 0 && result.skipped === 0) {
          return null;
        }
        return result;
      };
      const finishImportSuccess = (result: { imported: number; skipped: number }) => {
        if (result.imported > 0 || result.skipped > 0) {
          addToast(t('已导入 {imported} 个，跳过 {skipped} 个重复', {
            imported: result.imported,
            skipped: result.skipped,
          }), 'success');
        }
        void loadServers();
      };

      try {
        const result = await doImport('');
        if (result) finishImportSuccess(result);
      } catch (error) {
        if (!String(error).includes('need password')) {
          addToast(`${t('导入失败')}: ${String(error)}`, 'error');
          return;
        }
        const password = await window.lumeDialog?.prompt?.(
          t('密文需要密码请输入'), '', t('导入密码'), '',
        );
        if (password === null) return;
        try {
          const result = await doImport(typeof password === 'object' && password ? password.value : String(password ?? ''));
          if (result) finishImportSuccess(result);
        } catch {
          addToast(`${t('导入失败')}: ${t('密码错误或文件不兼容')}`, 'error');
        }
      }
    } finally {
      setIeBusy(false);
    }
  }, [addToast, loadServers, t]);

  const handleDownloadTemplate = useCallback(async () => {
    setIeBusy(true);
    try {
      const path = await AppGo.DownloadImportTemplate(lang);
      if (path) addToast(t('已下载模板到 {path}', { path }), 'success');
    } catch (error) {
      addToast(`${t('模板下载失败')}: ${String(error)}`, 'error');
    } finally {
      setIeBusy(false);
    }
  }, [addToast, lang, t]);

  return {
    showImportExportDialog,
    setShowImportExportDialog,
    showExportSelectedDialog,
    setShowExportSelectedDialog,
    exportSelectedIds,
    setExportSelectedIds,
    ieBusy,
    hasRecoveryPassword,
    handleOpenImportExport,
    handleExport,
    handleBatchExport,
    handleExportSelected,
    handleImport,
    handleDownloadTemplate,
  };
}
