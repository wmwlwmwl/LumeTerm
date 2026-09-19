import { useEffect, useRef, useState } from 'react';
import { ShieldAlert, ShieldQuestion, KeyRound, Eye, EyeOff, Clipboard, type LucideIcon } from 'lucide-react';
import { Z } from '../constants/zIndex';
import type { SessionAuthPrompt } from '../hooks/useSessionConnections.ts';

interface AuthButton {
  label: string;
  value: string | number;
  primary?: boolean;
  // 字母快捷键，按下该字母直接触发；渲染为「标签 (S)」式下划线提示
  accessKey?: string;
}

interface SessionAuthCardProps {
  prompt: SessionAuthPrompt;
  isActive: boolean;
  t: (key: string, vars?: Record<string, unknown>) => string;
  onResolve: (result: { value: string; persist: boolean } | string | number | null) => void;
}

// 会话内的交互卡片：主机密钥确认 / 认证失败重输密码。
// 与 ConnectingCard 同一挂载点、同一配色，每个会话各自渲染一张，
// 因此批量连接时 N 个会话会得到 N 张卡片，互不干扰。
export default function SessionAuthCard({ prompt, isActive, t, onResolve }: SessionAuthCardProps) {
  const isPassword = prompt.kind === 'password';
  const [value, setValue] = useState('');
  const [checked, setChecked] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [focusIdx, setFocusIdx] = useState(0);
  const inputRef = useRef<HTMLInputElement | null>(null);
  // 卡片卸载前可能连收两次回车/双击，onResolve 会调后端，必须只生效一次
  const resolvedRef = useRef(false);

  const buttons: AuthButton[] = isPassword
    ? [
        { label: t('确定'), value: 'ok', primary: true },
        { label: t('取消'), value: 'cancel' },
      ]
    : [
        { label: t('只接受本次'), value: 1, accessKey: 'o' },
        { label: t('接受并保存'), value: 2, primary: true, accessKey: 's' },
        { label: t('取消'), value: 0, accessKey: 'c' },
      ];

  // 密钥已变更（可能中间人）时默认落在「取消」，避免回车误接受；首次连接落在主按钮
  useEffect(() => {
    if (isPassword) {
      setFocusIdx(0);
      return;
    }
    if (prompt.danger) {
      setFocusIdx(2);
      return;
    }
    setFocusIdx(1);
  }, [isPassword, prompt.danger]);

  useEffect(() => {
    if (!isPassword || !isActive) return undefined;
    // 非活动会话的祖先为 display:none，autoFocus 无效，切回时再聚焦
    const id = requestAnimationFrame(() => inputRef.current?.focus());
    return () => cancelAnimationFrame(id);
  }, [isPassword, isActive]);

  const canSubmit = !isPassword || value.length > 0;

  const submit = (btnValue: string | number) => {
    if (resolvedRef.current) return;
    if (!isPassword) {
      resolvedRef.current = true;
      onResolve(btnValue);
      return;
    }
    if (btnValue === 'cancel') {
      resolvedRef.current = true;
      onResolve(null);
      return;
    }
    if (!value) return;
    resolvedRef.current = true;
    onResolve({ value, persist: checked });
  };

  useEffect(() => {
    if (!isActive) return undefined;
    const handleKeyDown = (e: KeyboardEvent) => {
      // ponytail: 全局弹窗（GlobalDialog / 各类 Modal）打开时让位，避免 Esc/Enter 被双重处理
      if (document.querySelector('.modal-overlay')) return;
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        submit(isPassword ? 'cancel' : 0);
        return;
      }
      if (!isPassword && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
        e.preventDefault();
        setFocusIdx((prev) => {
          const n = buttons.length;
          return e.key === 'ArrowLeft' ? (prev - 1 + n) % n : (prev + 1) % n;
        });
        return;
      }
      // 按字母快捷键（o/s/c）直接触发，无修饰键以免干扰复制等操作
      if (!isPassword && !e.ctrlKey && !e.altKey && !e.metaKey && !e.shiftKey) {
        const hit = buttons.find((b) => b.accessKey && e.key.toLowerCase() === b.accessKey);
        if (hit) {
          e.preventDefault();
          e.stopPropagation();
          submit(hit.value);
          return;
        }
      }
      if (e.key === 'Enter') {
        e.preventDefault();
        if (isPassword) {
          submit('ok');
          return;
        }
        const btn = buttons[focusIdx] || buttons[0];
        submit(btn.value);
      }
    };
    window.addEventListener('keydown', handleKeyDown, { capture: true });
    return () => window.removeEventListener('keydown', handleKeyDown, { capture: true });
  }, [isActive, isPassword, focusIdx, value, checked]);

  const pasteFromClipboard = async () => {
    try {
      const text = await navigator.clipboard.readText();
      if (text) {
        setValue(text);
        return;
      }
    } catch {}
    try {
      const { ClipboardGetText } = await import('../../wailsjs/runtime/runtime.js');
      const text = await ClipboardGetText();
      if (text) setValue(text);
    } catch {}
  };

  const Icon: LucideIcon = isPassword ? KeyRound : (prompt.danger ? ShieldAlert : ShieldQuestion);

  return (
    <div
      className="absolute inset-0 flex items-center justify-center bg-scrim"
      style={{ zIndex: Z.FULLSCREEN_OVERLAY }}
    >
      <div className="w-[440px] max-w-[calc(100%-32px)] rounded-[var(--radius-lg)] overflow-hidden bg-overlay border border-line shadow-xl pt-5 px-6 pb-[22px]">
        {/* 标题行 */}
        <div className="flex items-center gap-3.5 mb-4">
          <div className={`w-[42px] h-[42px] rounded-[var(--radius-md)] shrink-0 flex items-center justify-center ${isPassword || !prompt.danger ? 'bg-[rgba(var(--warning-rgb),0.85)]' : 'bg-[rgba(var(--danger-rgb),0.85)]'}`}>
            <Icon size={22} className="text-white" />
          </div>
          <div className="text-lg font-bold text-primary min-w-0">
            {prompt.title}
          </div>
        </div>

        {/* 正文（含指纹 / 错误详情） */}
        <div className="text-[12.5px] text-secondary leading-[1.65] whitespace-pre-wrap break-words [overflow-wrap:anywhere] select-text max-h-[38vh] overflow-y-auto mb-[18px]">
          {prompt.message}
        </div>

        {isPassword && (
          <>
            <div className={`relative ${prompt.checkboxLabel ? 'mb-3' : 'mb-[18px]'}`}>
              <input
                ref={inputRef}
                id="session-auth-password"
                name="session-auth-password"
                autoComplete="off"
                className="input rounded-[var(--radius-sm)] text-sm"
                type={showPassword ? 'text' : 'password'}
                value={value}
                onChange={(e) => setValue(e.target.value)}
                placeholder={t('请输入密码')}
                style={{ width: '100%', padding: '10px 68px 10px 14px' }}
              />
              <button
                type="button"
                onClick={() => setShowPassword((v) => !v)}
                aria-label={showPassword ? t('隐藏密码') : t('显示密码')}
                title={showPassword ? t('隐藏密码') : t('显示密码')}
                className="absolute right-[38px] top-1/2 -translate-y-1/2 bg-transparent border-0 text-muted cursor-pointer p-1 flex items-center rounded-[var(--radius-sm)]"
              >{showPassword ? <EyeOff size={16} /> : <Eye size={16} />}</button>
              <button
                type="button"
                onClick={pasteFromClipboard}
                aria-label={t('粘贴')}
                title={t('粘贴')}
                className="absolute right-2 top-1/2 -translate-y-1/2 bg-transparent border-0 text-muted cursor-pointer p-1 flex items-center rounded-[var(--radius-sm)]"
              ><Clipboard size={16} /></button>
            </div>
            {prompt.checkboxLabel && (
              <label htmlFor="session-auth-remember" className="flex items-center gap-2 mb-[18px] text-[12.5px] text-secondary cursor-pointer">
                <input
                  id="session-auth-remember"
                  name="session-auth-remember"
                  autoComplete="off"
                  type="checkbox"
                  checked={checked}
                  onChange={(e) => setChecked(e.target.checked)}
                />
                {prompt.checkboxLabel}
              </label>
            )}
          </>
        )}

        {/* 操作按钮 */}
        <div className="flex gap-2">
          {buttons.map((btn, i) => {
            const disabled = btn.value === 'ok' && !canSubmit;
            return (
              <button
                key={i}
                type="button"
                disabled={disabled}
                onClick={() => submit(btn.value)}
                onMouseEnter={() => setFocusIdx(i)}
                aria-keyshortcuts={btn.accessKey?.toUpperCase()}
                className={`flex-1 py-[9px] text-[12.5px] rounded-[var(--radius-sm)] whitespace-nowrap ${
                  btn.primary
                    ? 'bg-accent text-white border border-accent'
                    : 'bg-sunken hover:bg-hover text-secondary border border-line'
                } ${disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer'} ${
                  focusIdx === i ? 'outline-2 outline-offset-2 outline-accent' : 'outline-none'
                }`}
              >
                {btn.accessKey ? `${btn.label}(${btn.accessKey.toUpperCase()})` : btn.label}
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}
