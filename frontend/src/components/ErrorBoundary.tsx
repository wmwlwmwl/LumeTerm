import React from 'react';
import { t } from '../i18n.ts';

interface ErrorBoundaryProps {
  children?: React.ReactNode;
  fallback?: React.ReactNode;
  label?: string;
}

interface ErrorBoundaryState {
  hasError: boolean;
  error: Error | null;
}

export default class ErrorBoundary extends React.Component<ErrorBoundaryProps, ErrorBoundaryState> {
  constructor(props: ErrorBoundaryProps) {
    super(props);
    this.state = { hasError: false, error: null };
  }

  static getDerivedStateFromError(error: Error): Partial<ErrorBoundaryState> {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: React.ErrorInfo) {
    console.error('[ErrorBoundary]', error, errorInfo);
  }

  render() {
    if (this.state.hasError) {
      const { fallback, label } = this.props;
      if (fallback) return fallback;
      return (
        <div style={{
          display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
          height: '100%', padding: 24, color: 'var(--danger)', background: 'var(--surface-overlay)',
          fontFamily: 'system-ui, sans-serif', fontSize: 13,
        }}>
          <div className="font-semibold" style={{ marginBottom: 8 }}>
            {label || t('组件渲染出错')}
          </div>
          <div style={{ color: 'var(--text-secondary)', fontSize: 12, marginBottom: 12, textAlign: 'center', maxWidth: 400 }}>
            {this.state.error?.message || t('未知错误')}
          </div>
          <button
            onClick={() => this.setState({ hasError: false, error: null })}
            style={{
              padding: '4px 16px', border: '1px solid var(--border)', borderRadius: 4,
              background: 'transparent', color: 'var(--text-primary)', cursor: 'pointer', fontSize: 12,
            }}
          >
            {t('重试')}
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
