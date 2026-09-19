import React from 'react';
import ReactDOM from 'react-dom/client';
import App from './App.tsx';
import { initializeI18n, t } from './i18n.ts';
import { AlertTriangle } from 'lucide-react';
import './index.css';
import { applyProgramFontPreferences } from './utils/programFonts.ts';
import { applyStoredThemePackage, loadThemePackages } from './utils/theme.ts';
// favicon 与 UI logo 共用同一源，避免 public/favicon.png 再拷一份
import logoFavicon from './assets/logo.webp';

(() => {
  let link = document.querySelector<HTMLLinkElement>("link[rel='icon']");
  if (!link) {
    link = document.createElement('link');
    link.rel = 'icon';
    link.type = 'image/png';
    document.head.appendChild(link);
  }
  link.href = logoFavicon;
})();

// 全局错误边界，防止渲染错误导致白屏
interface ErrorBoundaryState {
  hasError: boolean;
  error: Error | null;
}

class ErrorBoundary extends React.Component<{ children?: React.ReactNode }, ErrorBoundaryState> {
  constructor(props: { children?: React.ReactNode }) {
    super(props);
    this.state = { hasError: false, error: null };
  }
  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { hasError: true, error };
  }
  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error('[ErrorBoundary]', error, info.componentStack);
  }
  render() {
    if (this.state.hasError) {
      const msg = this.state.error?.message || String(this.state.error);
      const stack = this.state.error?.stack || '';
      console.error('[ErrorBoundary] 完整错误:', msg);
      console.error('[ErrorBoundary] 堆栈:', stack);
      return (
        <div className="flex font-mono" style={{
          flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
          height: '100vh', background: 'var(--surface-base)', color: 'var(--danger)', gap: 12,
          padding: 20, textAlign: 'center'
        }}>
          <div className="text-2xl"><AlertTriangle size={24} /></div>
          <div className="text-sm font-semibold">{t('界面渲染出错')}</div>
          <div style={{ fontSize: 11, color: 'var(--text-secondary)', maxWidth: 500, wordBreak: 'break-all' }}>{msg}</div>
          <pre style={{ fontSize: 10, color: 'var(--text-tertiary)', maxHeight: 200, overflow: 'auto', background: 'var(--surface-raised)', padding: 8, borderRadius: 4 }}>{stack}</pre>
          <button onClick={() => { this.setState({ hasError: false, error: null }); window.location.reload(); }} style={{
            padding: '6px 16px', borderRadius: 6, border: '1px solid var(--danger)', background: 'var(--danger-dim)',
            color: 'var(--danger)', cursor: 'pointer', fontSize: 13
          }}>{t('重新加载')}</button>
        </div>
      );
    }
    return this.props.children;
  }
}

// Load initial theme package synchronously from localStorage to avoid startup flash
applyStoredThemePackage();

// 禁用浏览器默认右键菜单（完全拦截，以便使用统一的自定义玻璃菜单）
document.addEventListener('contextmenu', (e) => e.preventDefault());

// 全局未捕获错误捕获，帮助定位白屏原因
window.addEventListener('error', (e) => {
  console.error('[Global Error]', e.message, e.filename, e.lineno, e.colno, e.error);
});
window.addEventListener('unhandledrejection', (e) => {
  console.error('[Unhandled Rejection]', e.reason);
});

async function bootstrap() {
  await initializeI18n();
  await loadThemePackages().catch(() => {});
  await applyProgramFontPreferences().catch(() => {});
  const rootElement = document.getElementById('root');
  if (!rootElement) {
    throw new Error('Root element #root not found');
  }
  ReactDOM.createRoot(rootElement).render(
    <ErrorBoundary>
      <React.StrictMode>
        <App />
      </React.StrictMode>
    </ErrorBoundary>
  );
}

void bootstrap();
