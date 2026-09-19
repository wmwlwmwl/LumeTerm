import { useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from '../i18n.ts';
import { warnDev } from '../utils/devLog';
import { formatShortcut } from '../utils/platform.ts';
import { useOverlayScrollLock } from '../hooks/useOverlayScrollLock.ts';
import { GLOBAL_CONTEXT_MENU_OPEN_EVENT, type GlobalContextMenuDetail } from '../utils/contextMenu.ts';
import {
  type ContextMenuItemInput,
  type ContextMenuPositionDetail,
  type EditableAction,
  type NormalizedMenuItem,
  normalizeMenuItems,
  resolveEditableTarget,
  resolveMenuPosition,
} from '../utils/globalContextMenu.ts';
import { MenuList, MenuPanel, type MenuItem } from './ui';
import { Z } from '../constants/zIndex.ts';
import * as runtime from '../../wailsjs/runtime/runtime.js';

interface MenuState {
  x: number;
  y: number;
  items: NormalizedMenuItem[];
}

/** 子菜单锚点（记录父项行位置，用于固定定位子菜单面板） */
interface SubmenuAnchor {
  key: string;
  top: number;
  left: number;
  right: number;
}

export default function GlobalContextMenu() {
  const { t } = useTranslation();
  const [menu, setMenu] = useState<MenuState | null>(null);
  const [openSubmenuKey, setOpenSubmenuKey] = useState<string | null>(null);
  const [submenuAnchor, setSubmenuAnchor] = useState<SubmenuAnchor | null>(null);
  // 菜单打开期间锁定背景滚动（Linux WebKitGTK 滚动条穿透，见 useOverlayScrollLock）
  useOverlayScrollLock(menu !== null);
  // 防止子菜单切换时触发 MenuList 的自动 onClose 关闭整个菜单（同 ServerList 模式）
  const submenuToggleRef = useRef(false);
  const menuRef = useRef<HTMLDivElement | null>(null);
  const listWrapperRef = useRef<HTMLDivElement | null>(null);

  const closeMenu = useCallback(() => {
    setMenu(null);
    setOpenSubmenuKey(null);
    setSubmenuAnchor(null);
  }, []);

  const getRowRect = useCallback((key: string): SubmenuAnchor | null => {
    const marker = listWrapperRef.current?.querySelector(`[data-gcm-key="${CSS.escape(key)}"]`);
    const row = marker instanceof HTMLElement ? marker.closest('button') : null;
    if (!row) {
      return null;
    }
    const rect = row.getBoundingClientRect();
    return { key, top: rect.top, left: rect.left, right: rect.right };
  }, []);

  const handleRowHoverEnter = useCallback((event: React.MouseEvent) => {
    const target = event.target as HTMLElement | null;
    const row = target?.closest('button');
    if (!row || !listWrapperRef.current?.contains(row)) {
      return;
    }
    const marker = row.querySelector('[data-gcm-key]');
    if (!marker) {
      setOpenSubmenuKey(null);
      return;
    }
    const key = marker.getAttribute('data-gcm-key') ?? '';
    const rect = row.getBoundingClientRect();
    setSubmenuAnchor({ key, top: rect.top, left: rect.left, right: rect.right });
    setOpenSubmenuKey(key);
  }, []);

  const handleInputAction = useCallback(async (targetInput: HTMLInputElement | HTMLTextAreaElement, action: EditableAction) => {
    if (!targetInput) {
      return;
    }
    targetInput.focus();
    try {
      if (action === 'copy') {
        const text = targetInput.value.substring(Number(targetInput.selectionStart), Number(targetInput.selectionEnd));
        if (text) {
          await runtime.ClipboardSetText(text);
        }
        return;
      }
      if (action === 'cut') {
        const text = targetInput.value.substring(Number(targetInput.selectionStart), Number(targetInput.selectionEnd));
        if (!text) {
          return;
        }
        await runtime.ClipboardSetText(text);
        const start = Number(targetInput.selectionStart);
        const end = Number(targetInput.selectionEnd);
        const proto = targetInput.tagName === 'TEXTAREA'
          ? window.HTMLTextAreaElement.prototype
          : window.HTMLInputElement.prototype;
        // 输入元素原型上的 value setter 必然存在（老 WebView 无剪贴板 API 时才走此路径）
        const nativeSetter = Object.getOwnPropertyDescriptor(proto, 'value')!.set as (this: HTMLInputElement | HTMLTextAreaElement, value: string) => void;
        const nextValue = targetInput.value.substring(0, start) + targetInput.value.substring(end);
        nativeSetter.call(targetInput, nextValue);
        targetInput.setSelectionRange(start, start);
        targetInput.dispatchEvent(new Event('input', { bubbles: true }));
        return;
      }
      if (action === 'paste') {
        let text = '';
        try {
          text = await runtime.ClipboardGetText();
        } catch {}
        if (!text) {
          try {
            text = await navigator.clipboard.readText();
          } catch {}
        }
        if (!text) {
          return;
        }
        const start = Number(targetInput.selectionStart);
        const end = Number(targetInput.selectionEnd);
        const proto = targetInput.tagName === 'TEXTAREA'
          ? window.HTMLTextAreaElement.prototype
          : window.HTMLInputElement.prototype;
        const nativeSetter = Object.getOwnPropertyDescriptor(proto, 'value')!.set as (this: HTMLInputElement | HTMLTextAreaElement, value: string) => void;
        nativeSetter.call(
          targetInput,
          targetInput.value.substring(0, start) + text + targetInput.value.substring(end)
        );
        targetInput.dispatchEvent(new Event('input', { bubbles: true }));
        return;
      }
      if (action === 'selectAll') {
        targetInput.select();
      }
    } catch (error) {
      warnDev('Context menu action failed:', error);
    }
  }, []);

  const buildInputMenuItems = useCallback((targetInput: HTMLInputElement | HTMLTextAreaElement): ContextMenuItemInput[] => {
    const start = Number(targetInput?.selectionStart);
    const end = Number(targetInput?.selectionEnd);
    const hasSelection = Number.isFinite(start) && Number.isFinite(end) && end > start;
    return [
      {
        key: 'cut',
        label: t('剪切'),
        shortcut: formatShortcut('Ctrl+X'),
        disabled: !hasSelection,
        onSelect: () => handleInputAction(targetInput, 'cut'),
      },
      {
        key: 'copy',
        label: t('复制'),
        shortcut: formatShortcut('Ctrl+C'),
        disabled: !hasSelection,
        onSelect: () => handleInputAction(targetInput, 'copy'),
      },
      {
        key: 'paste',
        label: t('粘贴'),
        shortcut: formatShortcut('Ctrl+V'),
        onSelect: () => handleInputAction(targetInput, 'paste'),
      },
      { type: 'divider', key: 'input-divider' },
      {
        key: 'select-all',
        label: t('全选'),
        shortcut: formatShortcut('Ctrl+A'),
        onSelect: () => handleInputAction(targetInput, 'selectAll'),
      },
    ];
  }, [handleInputAction, t]);

  const openMenu = useCallback((detail: ContextMenuPositionDetail & { items?: unknown }) => {
    const items = normalizeMenuItems(detail?.items);
    if (items.length === 0) {
      closeMenu();
      return;
    }
    const position = resolveMenuPosition(detail, items.length);
    setMenu({
      x: position.x,
      y: position.y,
      items,
    });
  }, [closeMenu]);

  const handleMenuItemClick = useCallback((item: NormalizedMenuItem) => {
    if (!item || item.type !== 'item' || item.disabled || typeof item.onSelect !== 'function') {
      return;
    }
    const onSelect = item.onSelect;
    closeMenu();
    Promise.resolve()
      .then(() => onSelect(item))
      .catch((error) => {
        warnDev('Context menu action failed:', error);
      });
  }, [closeMenu]);

  useEffect(() => {
    const handleContextMenu = (event: MouseEvent) => {
      const targetInput = resolveEditableTarget(event.target);
      if (!targetInput) {
        return;
      }
      event.preventDefault();
      openMenu({
        x: event.clientX,
        y: event.clientY,
        estimatedWidth: 160,
        estimatedHeight: 150,
        items: buildInputMenuItems(targetInput),
      });
    };

    const handleCustomOpen = (event: CustomEvent<GlobalContextMenuDetail>) => {
      openMenu(event?.detail);
    };

    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Node && menuRef.current?.contains(target)) {
        return;
      }
      closeMenu();
    };

    document.addEventListener('contextmenu', handleContextMenu);
    // 自定义事件监听器签名与 EventListener 不完全一致，此处按需断言
    window.addEventListener(GLOBAL_CONTEXT_MENU_OPEN_EVENT, handleCustomOpen as EventListener);
    window.addEventListener('pointerdown', handlePointerDown, true);
    window.addEventListener('blur', closeMenu);

    return () => {
      document.removeEventListener('contextmenu', handleContextMenu);
      window.removeEventListener(GLOBAL_CONTEXT_MENU_OPEN_EVENT, handleCustomOpen as EventListener);
      window.removeEventListener('pointerdown', handlePointerDown, true);
      window.removeEventListener('blur', closeMenu);
    };
  }, [buildInputMenuItems, closeMenu, openMenu]);

  if (!menu || typeof document === 'undefined') {
    return null;
  }

  const hasSubmenu = menu.items.some((item) => item.type === 'item' && item.children.length > 0);
  const activeParent = openSubmenuKey
    ? menu.items.find(
        (item): item is Extract<NormalizedMenuItem, { type: 'item' }> =>
          item.type === 'item' && item.key === openSubmenuKey && item.children.length > 0,
      )
    : undefined;
  const anchor = openSubmenuKey && submenuAnchor?.key === openSubmenuKey ? submenuAnchor : null;
  const submenuToLeft = typeof window !== 'undefined' && menu.x > window.innerWidth * 0.5;

  const toUiItems = (items: NormalizedMenuItem[]): MenuItem[] =>
    items.map((item): MenuItem => {
      if (item.type === 'divider') {
        return 'separator';
      }
      if (item.children.length > 0) {
        return {
          label: <span data-gcm-key={item.key}>{item.label}</span>,
          shortcut: '›',
          danger: item.danger,
          disabled: item.disabled,
          onSelect: () => {
            submenuToggleRef.current = true;
            const rect = anchor?.key === item.key ? anchor : getRowRect(item.key);
            if (rect) {
              setSubmenuAnchor(rect);
            }
            setOpenSubmenuKey((prev) => (prev === item.key ? null : item.key));
          },
        };
      }
      return {
        label: item.label,
        shortcut: item.shortcut || undefined,
        danger: item.danger,
        disabled: item.disabled,
        onSelect: () => handleMenuItemClick(item),
      };
    });

  return createPortal(
    <div
      ref={menuRef}
      data-context-menu="true"
      onMouseDown={(event) => {
        event.stopPropagation();
      }}
      onClick={(event) => {
        event.stopPropagation();
      }}
    >
      <MenuPanel
        className="fixed origin-top-left animate-[fadeIn_0.12s_ease] max-h-[calc(100vh-16px)]"
        style={{
          left: menu.x,
          top: menu.y,
          zIndex: Z.CONTEXT_MENU,
          overflow: hasSubmenu ? 'visible' : undefined,
        }}
      >
        <div ref={listWrapperRef} onMouseEnter={handleRowHoverEnter}>
          <MenuList
            items={toUiItems(menu.items)}
            onClose={() => {
              if (submenuToggleRef.current) {
                submenuToggleRef.current = false;
                return;
              }
              closeMenu();
            }}
          />
        </div>
        {activeParent && anchor && (
          <MenuPanel
            minWidth={160}
            className="fixed animate-[fadeIn_0.12s_ease]"
            style={
              submenuToLeft
                ? { top: anchor.top - 5, right: window.innerWidth - anchor.left + 2, zIndex: Z.CONTEXT_SUBMENU }
                : { top: anchor.top - 5, left: anchor.right + 2, zIndex: Z.CONTEXT_SUBMENU }
            }
          >
            <MenuList items={toUiItems(activeParent.children)} onClose={closeMenu} />
          </MenuPanel>
        )}
      </MenuPanel>
    </div>,
    document.body
  );
}
