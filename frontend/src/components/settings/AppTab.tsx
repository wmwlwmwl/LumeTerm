import { useEffect, useState } from 'react';
import { ArrowUp, FilePlus, FileText, RefreshCw, Smartphone } from 'lucide-react';
import { t as $t } from '../../i18n.ts';
import {
  APP_GITHUB_ANDROID_RELEASES_URL,
  APP_GITHUB_ANDROID_REPO_URL,
  APP_GITHUB_ISSUES_URL,
  APP_GITHUB_RELEASES_URL,
  APP_GITHUB_REPO_URL,
} from '../../config.ts';
import logoImg from '../../assets/logo.webp';
import logoLightImg from '../../assets/logo_q.webp';
import logoDarkImg from '../../assets/logo_s.webp';
import { Z } from '../../constants/zIndex';
import { cn } from '../../utils/cn.ts';

// lucide 已移除品牌图标，GitHub 用官方 mark 内联
function GithubIcon({ size = 24 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M12 .5C5.65.5.5 5.65.5 12c0 5.08 3.29 9.39 7.86 10.91.58.11.79-.25.79-.55 0-.27-.01-1.17-.02-2.12-3.2.7-3.88-1.36-3.88-1.36-.52-1.33-1.28-1.68-1.28-1.68-1.04-.71.08-.7.08-.7 1.15.08 1.76 1.19 1.76 1.19 1.03 1.75 2.69 1.25 3.34.95.1-.74.4-1.25.72-1.53-2.55-.29-5.24-1.28-5.24-5.69 0-1.26.45-2.28 1.19-3.09-.12-.29-.52-1.46.11-3.05 0 0 .97-.31 3.18 1.18a11.1 11.1 0 0 1 5.8 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.76.11 3.05.74.81 1.19 1.83 1.19 3.09 0 4.42-2.7 5.39-5.26 5.68.41.35.77 1.05.77 2.12 0 1.53-.01 2.76-.01 3.14 0 .3.21.66.8.55A11.51 11.51 0 0 0 23.5 12C23.5 5.65 18.35.5 12 .5Z" />
    </svg>
  );
}
import { AboutLink } from './SharedComponents';
import { settings } from './settingDefinitions';
import { getFreshContributorsCache, getResolvedThemeMode, loadContributors, type Contributor } from './appTabContributors';

export interface AppTabProps {
  CURRENT_VERSION: string;
  BUILD_TIME: string | null;
  updateInfo: { hasUpdate?: boolean; latestVersion?: string } | null;
  checkingUpdate: boolean;
  downloadProgress: number;
  onCheckUpdate: () => void;
  onApplyUpdate: () => void;
}

export default function AppTab({ CURRENT_VERSION, BUILD_TIME, updateInfo, checkingUpdate, downloadProgress, onCheckUpdate, onApplyUpdate }: AppTabProps) {
  const [contributors, setContributors] = useState<Contributor[]>(() => getFreshContributorsCache() || []);
  const [contributorsLoading, setContributorsLoading] = useState(() => !getFreshContributorsCache());
  const [showRefreshedLogo, setShowRefreshedLogo] = useState(false);
  const [resolvedThemeMode, setResolvedThemeMode] = useState<'light' | 'dark'>(() => getResolvedThemeMode());
  const logoTransitionImg = resolvedThemeMode === 'light' ? logoLightImg : logoDarkImg;
  // settingDefinitions.ts 已类型化，直接使用 settings 注册表
  const settingsData = settings;
  const appSettings = settingsData.app;

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setShowRefreshedLogo(true);
    }, 260);
    return () => {
      window.clearTimeout(timer);
    };
  }, []);

  useEffect(() => {
    const refreshThemeMode = () => {
      setResolvedThemeMode(getResolvedThemeMode());
    };
    const mediaQuery = window.matchMedia('(prefers-color-scheme: light)');
    window.addEventListener('theme-mode-changed', refreshThemeMode);
    if (typeof mediaQuery.addEventListener === 'function') {
      mediaQuery.addEventListener('change', refreshThemeMode);
    } else if (typeof mediaQuery.addListener === 'function') {
      mediaQuery.addListener(refreshThemeMode);
    }
    return () => {
      window.removeEventListener('theme-mode-changed', refreshThemeMode);
      if (typeof mediaQuery.removeEventListener === 'function') {
        mediaQuery.removeEventListener('change', refreshThemeMode);
      } else if (typeof mediaQuery.removeListener === 'function') {
        mediaQuery.removeListener(refreshThemeMode);
      }
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    const cached = getFreshContributorsCache();
    if (cached) {
      setContributors(cached);
      setContributorsLoading(false);
      return () => {
        cancelled = true;
      };
    }
    setContributorsLoading(true);
    loadContributors()
      .then((data) => {
        if (cancelled) {
          return;
        }
        setContributors(data);
        setContributorsLoading(false);
      })
      .catch(() => {
        if (cancelled) {
          return;
        }
        setContributors([]);
        setContributorsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const LOGO_IMG_TRANSITION = 'opacity 0.6s ease, transform 0.7s cubic-bezier(0.22, 1, 0.36, 1), filter 0.6s ease';

  return (
    <div className="flex flex-col w-full max-w-none px-6 py-4 gap-8">
      {/* 顶部布局：图标与标题 */}
      <div className="flex items-center gap-6">
        <div className="relative w-24 h-24 rounded-3xl overflow-hidden shadow-sm border border-line-light bg-overlay shrink-0">
          <img
            src={logoImg}
            alt="LumeTerm"
            className="absolute inset-0 w-full h-full object-contain"
            style={{
              opacity: showRefreshedLogo ? 0 : 1,
              transform: showRefreshedLogo ? 'scale(0.9) rotate(-8deg)' : 'scale(1) rotate(0deg)',
              filter: showRefreshedLogo ? 'blur(8px)' : 'blur(0px)',
              transition: LOGO_IMG_TRANSITION,
            }}
          />
          <img
            src={logoTransitionImg}
            alt="LumeTerm Refresh"
            className="absolute inset-0 w-full h-full object-contain"
            style={{
              opacity: showRefreshedLogo ? 1 : 0,
              transform: showRefreshedLogo ? 'scale(1) rotate(0deg)' : 'scale(1.12) rotate(8deg)',
              filter: showRefreshedLogo ? 'blur(0px)' : 'blur(10px)',
              transition: LOGO_IMG_TRANSITION,
            }}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <div className="text-[32px] font-extrabold text-primary tracking-[-0.5px] flex items-baseline gap-2">
            LumeTerm
            <span className="text-md font-medium text-tertiary tracking-normal">by WuMing</span>
          </div>
          <div className="flex items-center gap-3 flex-wrap">
            <span className="text-md text-secondary font-mono">
              {CURRENT_VERSION}
            </span>
            {BUILD_TIME && (
              <span className="text-sm text-tertiary font-mono">
                {BUILD_TIME}
              </span>
            )}
            <button
              data-settings-field-id={appSettings.fields.checkUpdate.id}
              onClick={onCheckUpdate}
              disabled={checkingUpdate}
              className={cn(
                'ml-1 inline-flex items-center gap-1.5 rounded-lg px-3.5 py-1.5 text-sm font-medium shrink-0 border border-line bg-overlay text-secondary transition-all duration-[200ms]',
                checkingUpdate ? 'opacity-70 cursor-not-allowed' : 'cursor-pointer hover:bg-sunken',
              )}
            >
              <RefreshCw size={14} strokeWidth={2.5} className={checkingUpdate ? 'animate-[spin_1s_linear_infinite]' : ''} />
              {checkingUpdate
                 ? $t('检查中...')
                 : $t('检查更新')}
            </button>
            {(updateInfo?.hasUpdate || downloadProgress >= 0) && (
              <span
                onClick={onApplyUpdate}
                className={cn(
                  'relative flex items-center justify-center gap-1 min-w-20 rounded-[var(--radius-sm)] px-2 py-0.5 text-sm font-semibold shadow-none overflow-hidden',
                  downloadProgress >= 0
                    ? 'bg-accent-dim text-accent cursor-default'
                    : 'bg-[rgba(var(--success-rgb),0.12)] text-success cursor-pointer',
                )}
              >
                {downloadProgress >= 0 && (
                  <div className="absolute left-0 top-0 bottom-0 bg-[rgba(var(--accent-rgb),0.22)] transition-[width] duration-[200ms] ease-out" style={{ width: `${downloadProgress}%` }}></div>
                )}
                <span className="relative flex items-center gap-1" style={{ zIndex: Z.CONTENT }}>
                  {downloadProgress >= 0 ? (
                    <>
                      <RefreshCw size={12} strokeWidth={2.5} className="animate-[spin_1s_linear_infinite]" />
                      {Math.round(downloadProgress)}%
                    </>
                  ) : (
                    <>
                      <ArrowUp size={12} />
                      {(updateInfo?.latestVersion)} {$t('立即更新')}
                    </>
                  )}
                </span>
              </span>
            )}
          </div>
        </div>
      </div>

      <div className="grid grid-cols-[repeat(auto-fit,minmax(110px,1fr))] gap-3 mt-3">
        <AboutLink
          definition={appSettings.fields.feedback}
          icon={<FilePlus size={24} />}
          title={$t('反馈问题')}
          url={APP_GITHUB_ISSUES_URL}
        />
        <AboutLink
          definition={appSettings.fields.github}
          icon={<GithubIcon size={24} />}
          title={$t('GitHub')}
          url={APP_GITHUB_REPO_URL}
        />
        <AboutLink
          definition={appSettings.fields.android}
          icon={<Smartphone size={24} />}
          title={$t('Android 客户端')}
          url={APP_GITHUB_ANDROID_REPO_URL}
        />
        <AboutLink
          definition={appSettings.fields.releases}
          icon={<FileText size={24} />}
          title={$t('更新内容')}
          url={APP_GITHUB_RELEASES_URL}
        />
      </div>

      <div data-settings-field-id={appSettings.fields.crossPlatform.id} className="mt-1 px-4 py-3.5 rounded-md border border-line bg-overlay flex flex-col gap-2">
        <div className="text-base font-semibold text-primary">
          {$t('跨端说明')}
        </div>
        <div className="text-sm text-secondary leading-[1.55]">
          {$t('本产品为桌面端。Android 客户端独立仓库、分开发版，数据可通过云同步互通。')}
        </div>
        <div className="text-sm text-tertiary leading-[1.55]">
          {$t('本 Release 仅 Desktop，Android 端见 Lumin-SSH-Android')}
          {' · '}
          {$t('许可见仓库 LICENSE')}
        </div>
        <div className="flex gap-2.5 flex-wrap mt-0.5">
          <button
            type="button"
            onClick={() => window.runtime?.BrowserOpenURL?.(APP_GITHUB_ANDROID_REPO_URL)}
            className="rounded-lg px-3 py-1.5 text-sm font-semibold cursor-pointer border border-line bg-canvas text-accent"
          >
            {$t('打开 Android 仓库')}
          </button>
          <button
            type="button"
            onClick={() => window.runtime?.BrowserOpenURL?.(APP_GITHUB_ANDROID_RELEASES_URL)}
            className="rounded-lg px-3 py-1.5 text-sm font-medium cursor-pointer border border-line bg-canvas text-secondary"
          >
            {$t('Android 发行版')}
          </button>
        </div>
      </div>

      <div data-settings-field-id={appSettings.fields.contributors.id} className="flex flex-col gap-4 mt-2">
        <div className="text-[18px] font-bold text-primary">
          {$t('特别鸣谢')}
        </div>
        <div className="grid grid-cols-[repeat(auto-fit,minmax(260px,1fr))] gap-3.5 p-[18px] rounded-lg border border-line bg-canvas content-start max-h-[min(420px,48vh)] overflow-y-auto overflow-x-hidden">
          {contributorsLoading
            ? Array.from({ length: 4 }).map((_, index) => (
                <div
                  key={`contributor-skeleton-${index}`}
                  className="flex items-center gap-3.5 px-[18px] py-4 rounded-md bg-overlay border border-line"
                >
                  <div className="w-16 h-16 rounded-full bg-hover shrink-0"></div>
                  <div className="flex flex-col gap-2 min-w-0 flex-1">
                    <div className="w-[116px] h-4 rounded-full bg-hover"></div>
                    <div className="w-[68px] h-3 rounded-full bg-hover"></div>
                    <div className="w-[138px] h-3.5 rounded-full bg-hover"></div>
                  </div>
                </div>
              ))
            : contributors.map((item) => (
                <div
                  key={item.login}
                  onClick={() => window.runtime?.BrowserOpenURL?.(item.profileUrl)}
                  className="flex items-center gap-3.5 px-[18px] py-4 rounded-md cursor-pointer transition-all duration-[200ms] text-left bg-overlay border border-line hover:border-accent-border hover:bg-sunken"
                >
                  <img
                    src={item.avatar}
                    alt={item.login}
                    loading="lazy"
                    className="w-16 h-16 rounded-full object-cover border border-line-light shadow-xs shrink-0"
                  />
                  <div className="flex flex-col gap-1.5 min-w-0 flex-1">
                    <div className="text-[16px] font-bold text-primary leading-[1.3] break-words">
                      {item.login}
                    </div>
                    <div className="inline-flex items-center gap-1.5 text-base text-tertiary font-mono">
                      <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                        <circle cx="12" cy="12" r="3"></circle>
                        <line x1="3" y1="12" x2="9" y2="12"></line>
                        <line x1="15" y1="12" x2="21" y2="12"></line>
                      </svg>
                      {item.total}
                    </div>
                    <div className="inline-flex items-center gap-3 flex-wrap text-sm font-mono">
                      <span className="text-success font-semibold">+{item.additions.toLocaleString()} ++</span>
                      <span className="text-danger font-semibold">-{item.deletions.toLocaleString()} --</span>
                    </div>
                  </div>
                </div>
              ))}
        </div>
      </div>
    </div>
  );
}
