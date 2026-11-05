import { useEffect, useState } from 'react';
import { t as $t } from '../../i18n.ts';

export type ToastFn = (message: string | Error, type?: string, duration?: number, actions?: unknown[]) => number;

export function useSettingsGeneralState(addToast: ToastFn) {
  const [terminalRightClickPasteOnEmpty, setTerminalRightClickPasteOnEmpty] = useState(localStorage.getItem('terminalRightClickPasteOnEmpty') === 'true');
  const [terminalRightClickPasteMode, setTerminalRightClickPasteMode] = useState(localStorage.getItem('terminalRightClickPasteMode') === 'always' ? 'always' : 'empty');
  const [terminalLeftClickCopyOnSelection, setTerminalLeftClickCopyOnSelection] = useState(localStorage.getItem('terminalLeftClickCopyOnSelection') === 'true');
  const [terminalLeftClickCopyOnSelectionMode, setTerminalLeftClickCopyOnSelectionMode] = useState(localStorage.getItem('terminalLeftClickCopyOnSelectionMode') === 'mouseup' ? 'mouseup' : 'click');
  const [terminalTabDoubleClickActionEnabled, setTerminalTabDoubleClickActionEnabled] = useState(() => {
    const stored = localStorage.getItem('terminalTabDoubleClickActionEnabled');
    if (stored === 'true' || stored === 'false') {
      return stored === 'true';
    }
    return localStorage.getItem('terminalTabDoubleClickDuplicate') === 'true';
  });
  const [terminalTabDoubleClickAction, setTerminalTabDoubleClickAction] = useState(() => {
    const stored = localStorage.getItem('terminalTabDoubleClickAction');
    if (stored === 'close' || stored === 'duplicate') {
      return stored;
    }
    return 'duplicate';
  });

  const handleTerminalRightClickPasteOnEmptyChange = (enabled: boolean) => {
    setTerminalRightClickPasteOnEmpty(enabled);
    localStorage.setItem('terminalRightClickPasteOnEmpty', String(enabled));
    window.dispatchEvent(new CustomEvent('terminal-right-click-paste-on-empty-changed', { detail: enabled }));
  };

  const handleTerminalRightClickPasteModeChange = (mode: string) => {
    const next = mode === 'always' ? 'always' : 'empty';
    setTerminalRightClickPasteMode(next);
    if (next === 'empty') localStorage.removeItem('terminalRightClickPasteMode');
    else localStorage.setItem('terminalRightClickPasteMode', next);
    window.dispatchEvent(new CustomEvent('terminal-right-click-paste-mode-changed', { detail: next }));
  };

  const handleTerminalLeftClickCopyOnSelectionChange = (enabled: boolean) => {
    setTerminalLeftClickCopyOnSelection(enabled);
    localStorage.setItem('terminalLeftClickCopyOnSelection', String(enabled));
    window.dispatchEvent(new CustomEvent('terminal-left-click-copy-on-selection-changed', { detail: enabled }));
  };

  const handleTerminalLeftClickCopyOnSelectionModeChange = (mode: string) => {
    const next = mode === 'mouseup' ? 'mouseup' : 'click';
    setTerminalLeftClickCopyOnSelectionMode(next);
    if (next === 'click') localStorage.removeItem('terminalLeftClickCopyOnSelectionMode');
    else localStorage.setItem('terminalLeftClickCopyOnSelectionMode', next);
    window.dispatchEvent(new CustomEvent('terminal-left-click-copy-on-selection-mode-changed', { detail: next }));
  };

  const handleTerminalTabDoubleClickActionEnabledChange = (enabled: boolean) => {
    setTerminalTabDoubleClickActionEnabled(enabled);
    localStorage.setItem('terminalTabDoubleClickActionEnabled', String(enabled));
  };

  const handleTerminalTabDoubleClickActionChange = (action: string) => {
    const next = action === 'close' ? 'close' : 'duplicate';
    setTerminalTabDoubleClickAction(next);
    localStorage.setItem('terminalTabDoubleClickAction', next);
  };

  // 操作确认开关
  const [confirmCloseSession, setConfirmCloseSession] = useState(localStorage.getItem('skipCloseSessionConfirm') !== 'true');
  const [confirmCloseAll, setConfirmCloseAll] = useState(localStorage.getItem('skipCloseAllConfirm') !== 'true');
  const [confirmFileDelete, setConfirmFileDelete] = useState(localStorage.getItem('skipFileDeleteConfirm') !== 'true');
  const [confirmProcessKill, setConfirmProcessKill] = useState(localStorage.getItem('skipProcessKillConfirm') !== 'true');
  const [confirmTerminalSelectionPaste, setConfirmTerminalSelectionPaste] = useState(localStorage.getItem('skipTerminalSelectionPasteConfirm') !== 'true');
  // 多行粘贴确认默认开启（对齐 Windows Terminal 的多行粘贴警告），可在设置里关闭
  const [confirmTerminalMultilinePaste, setConfirmTerminalMultilinePaste] = useState(localStorage.getItem('terminalMultiLinePasteConfirm') !== 'false');
  const [confirmGitDiscard, setConfirmGitDiscard] = useState(localStorage.getItem('skipGitDiscardConfirm') !== 'true');
  const [confirmGitForcePush, setConfirmGitForcePush] = useState(localStorage.getItem('skipGitForcePushConfirm') !== 'true');
  const [confirmGitUntrack, setConfirmGitUntrack] = useState(localStorage.getItem('skipGitUntrackConfirm') !== 'true');
  const [windowCloseAction, setWindowCloseAction] = useState(localStorage.getItem('windowCloseAction') || 'ask');
  const [updateUseProxy, setUpdateUseProxy] = useState(localStorage.getItem('updateUseProxy') === 'true');
  const [rememberWorkspace, setRememberWorkspace] = useState(false);
  const [workspacePersistenceLevel, setWorkspacePersistenceLevel] = useState('program');
  const [webviewGpuDisabled, setWebviewGpuDisabled] = useState(false);
  const handleToggleConfirmCloseSession = () => {
    const next = !confirmCloseSession;
    setConfirmCloseSession(next);
    if (next) localStorage.removeItem('skipCloseSessionConfirm');
    else localStorage.setItem('skipCloseSessionConfirm', 'true');
  };
  const handleToggleConfirmCloseAll = () => {
    const next = !confirmCloseAll;
    setConfirmCloseAll(next);
    if (next) localStorage.removeItem('skipCloseAllConfirm');
    else localStorage.setItem('skipCloseAllConfirm', 'true');
  };
  const handleToggleConfirmFileDelete = () => {
    const next = !confirmFileDelete;
    setConfirmFileDelete(next);
    if (next) localStorage.removeItem('skipFileDeleteConfirm');
    else localStorage.setItem('skipFileDeleteConfirm', 'true');
  };
  const handleToggleConfirmProcessKill = () => {
    const next = !confirmProcessKill;
    setConfirmProcessKill(next);
    if (next) localStorage.removeItem('skipProcessKillConfirm');
    else localStorage.setItem('skipProcessKillConfirm', 'true');
  };
  const handleToggleConfirmTerminalSelectionPaste = () => {
    const next = !confirmTerminalSelectionPaste;
    setConfirmTerminalSelectionPaste(next);
    if (next) localStorage.removeItem('skipTerminalSelectionPasteConfirm');
    else localStorage.setItem('skipTerminalSelectionPasteConfirm', 'true');
  };
  const handleToggleConfirmTerminalMultilinePaste = () => {
    const next = !confirmTerminalMultilinePaste;
    setConfirmTerminalMultilinePaste(next);
    localStorage.setItem('terminalMultiLinePasteConfirm', String(next));
    window.dispatchEvent(new CustomEvent('terminal-multiline-paste-confirm-changed', { detail: next }));
  };
  const handleToggleConfirmGitDiscard = () => {
    const next = !confirmGitDiscard;
    setConfirmGitDiscard(next);
    if (next) localStorage.removeItem('skipGitDiscardConfirm');
    else localStorage.setItem('skipGitDiscardConfirm', 'true');
  };
  const handleToggleConfirmGitForcePush = () => {
    const next = !confirmGitForcePush;
    setConfirmGitForcePush(next);
    if (next) localStorage.removeItem('skipGitForcePushConfirm');
    else localStorage.setItem('skipGitForcePushConfirm', 'true');
  };
  const handleToggleConfirmGitUntrack = () => {
    const next = !confirmGitUntrack;
    setConfirmGitUntrack(next);
    if (next) localStorage.removeItem('skipGitUntrackConfirm');
    else localStorage.setItem('skipGitUntrackConfirm', 'true');
  };
  const handleWindowCloseActionChange = (value: string) => {
    setWindowCloseAction(value);
    if (value === 'ask') localStorage.removeItem('windowCloseAction');
    else localStorage.setItem('windowCloseAction', value);
  };
  const handleToggleUpdateUseProxy = () => {
    const next = !updateUseProxy;
    setUpdateUseProxy(next);
    if (next) localStorage.setItem('updateUseProxy', 'true');
    else localStorage.removeItem('updateUseProxy');
  };
  const handleToggleRememberWorkspace = async () => {
    const next = !rememberWorkspace;
    setRememberWorkspace(next);
    try {
      await window?.go?.wailsapp?.App?.SetRememberWorkspace?.(next);
      window.dispatchEvent(new CustomEvent('workspace-remember-changed', { detail: next }));
    } catch (err) {
      setRememberWorkspace(!next);
      addToast($t('记忆工作区设置保存失败') + `: ${err}`, 'error');
    }
  };
  const handleWorkspacePersistenceLevelChange = async (value: string) => {
    const next = value === 'session' ? 'session' : 'program';
    const previous = workspacePersistenceLevel;
    setWorkspacePersistenceLevel(next);
    try {
      await window?.go?.wailsapp?.App?.SetWorkspacePersistenceLevel?.(next);
      window.dispatchEvent(new CustomEvent('workspace-persistence-level-changed', { detail: next }));
    } catch (err) {
      setWorkspacePersistenceLevel(previous);
      addToast($t('工作区持久化级别保存失败') + `: ${err}`, 'error');
    }
  };
  const handleToggleWebviewGpuDisabled = async () => {
    const next = !webviewGpuDisabled;
    setWebviewGpuDisabled(next);
    try {
      await window?.go?.wailsapp?.App?.SetWebviewGpuDisabled?.(next);
      addToast($t('设置已保存，重启后生效'), 'success');
    } catch (err) {
      setWebviewGpuDisabled(!next);
      addToast($t('硬件加速设置保存失败') + `: ${err}`, 'error');
    }
  };
  const [supportsWebviewGpuDisable, setSupportsWebviewGpuDisable] = useState(false);
  // 粘贴确认弹窗里勾「不再询问」会直接改 localStorage 并广播，设置面板打开时也要同步
  useEffect(() => {
    const handleMultilinePasteConfirmChange = (event: Event) => {
      setConfirmTerminalMultilinePaste((event as CustomEvent<boolean>).detail === true);
    };
    window.addEventListener('terminal-multiline-paste-confirm-changed', handleMultilinePasteConfirmChange);
    return () => window.removeEventListener('terminal-multiline-paste-confirm-changed', handleMultilinePasteConfirmChange);
  }, []);
  useEffect(() => {
    let cancelled = false;

    Promise.resolve(window?.go?.wailsapp?.App?.GetRememberWorkspace?.())
      .then((enabled) => {
        if (!cancelled && typeof enabled === 'boolean') setRememberWorkspace(enabled);
      })
      .catch(() => {});
    Promise.resolve(window?.go?.wailsapp?.App?.GetWorkspacePersistenceLevel?.())
      .then((level) => {
        if (!cancelled && typeof level === 'string') setWorkspacePersistenceLevel(level === 'session' ? 'session' : 'program');
      })
      .catch(() => {});

    Promise.resolve(window?.go?.wailsapp?.App?.SupportsWebviewGpuDisable?.())
      .then((supported) => {
        if (cancelled || supported !== true) return;
        setSupportsWebviewGpuDisable(true);
        Promise.resolve(window?.go?.wailsapp?.App?.GetWebviewGpuDisabled?.())
          .then((enabled) => {
            if (!cancelled && typeof enabled === 'boolean') setWebviewGpuDisabled(enabled);
          })
          .catch(() => {});
      })
      .catch(() => {});

    return () => { cancelled = true; };
  }, []);

  return {
    terminalRightClickPasteOnEmpty,
    terminalRightClickPasteMode,
    terminalLeftClickCopyOnSelection,
    terminalLeftClickCopyOnSelectionMode,
    terminalTabDoubleClickActionEnabled,
    terminalTabDoubleClickAction,
    confirmCloseSession,
    confirmCloseAll,
    confirmFileDelete,
    confirmProcessKill,
    confirmTerminalSelectionPaste,
    confirmTerminalMultilinePaste,
    confirmGitDiscard,
    confirmGitForcePush,
    confirmGitUntrack,
    windowCloseAction,
    updateUseProxy,
    rememberWorkspace,
    workspacePersistenceLevel,
    webviewGpuDisabled,
    supportsWebviewGpuDisable,
    handleTerminalRightClickPasteOnEmptyChange,
    handleTerminalRightClickPasteModeChange,
    handleTerminalLeftClickCopyOnSelectionChange,
    handleTerminalLeftClickCopyOnSelectionModeChange,
    handleTerminalTabDoubleClickActionEnabledChange,
    handleTerminalTabDoubleClickActionChange,
    handleToggleConfirmCloseSession,
    handleToggleConfirmCloseAll,
    handleToggleConfirmFileDelete,
    handleToggleConfirmProcessKill,
    handleToggleConfirmTerminalSelectionPaste,
    handleToggleConfirmTerminalMultilinePaste,
    handleToggleConfirmGitDiscard,
    handleToggleConfirmGitForcePush,
    handleToggleConfirmGitUntrack,
    handleWindowCloseActionChange,
    handleToggleUpdateUseProxy,
    handleToggleRememberWorkspace,
    handleWorkspacePersistenceLevelChange,
    handleToggleWebviewGpuDisabled,
  };
}
