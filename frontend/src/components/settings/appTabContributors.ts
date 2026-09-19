import * as AppGo from '../../../wailsjs/go/wailsapp/App.js';

const CONTRIBUTORS_CACHE_TTL = 10 * 60 * 1000;

/** 贡献者条目（normalizeContributors 归一化后） */
export interface Contributor {
  login: string;
  avatar: string;
  total: number;
  additions: number;
  deletions: number;
  profileUrl: string;
}

let contributorsCache: { data: Contributor[] | null; expiresAt: number } = {
  data: null,
  expiresAt: 0,
};

export function getFreshContributorsCache(): Contributor[] | null {
  if (Array.isArray(contributorsCache.data) && contributorsCache.data.length > 0 && Date.now() < contributorsCache.expiresAt) {
    return contributorsCache.data;
  }
  return null;
}

export function getResolvedThemeMode(): 'light' | 'dark' {
  const savedTheme = localStorage.getItem('themeMode') || 'dark';
  if (savedTheme === 'system') {
    return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  }
  return savedTheme === 'light' ? 'light' : 'dark';
}

function resolveContributorAvatar(value: unknown): string {
  const rawValue = typeof value === 'string' ? value.trim() : '';
  if (!rawValue) {
    return '';
  }
  if (/^data:image\/[a-z0-9.+-]+;base64,/i.test(rawValue)) {
    return rawValue;
  }
  try {
    const parsed = new URL(rawValue);
    parsed.searchParams.delete('s');
    parsed.searchParams.delete('v');
    return parsed.toString();
  } catch {
    return rawValue;
  }
}

function normalizeContributors(payload: unknown): Contributor[] {
  if (!Array.isArray(payload)) {
    return [];
  }
  return (payload as Array<Record<string, unknown>>)
    .map((item) => {
      const author = item?.author && typeof item.author === 'object' ? item.author as Record<string, unknown> : item;
      const login = typeof author?.login === 'string' ? author.login.trim() : '';
      const avatar = resolveContributorAvatar(
        typeof author?.avatar === 'string'
          ? author.avatar
          : (typeof item?.avatar === 'string' ? item.avatar : '')
      );
      const path = typeof author?.path === 'string' ? author.path.trim() : '';
      const total = Number(item?.total) || 0;
      const weeks = Array.isArray(item?.weeks) ? item.weeks as Array<Record<string, unknown>> : (Array.isArray(item?.weeeks) ? item.weeeks as Array<Record<string, unknown>> : []);
      const additions = weeks.reduce((sum, week) => sum + (Number(week?.a) || 0), 0);
      const deletions = weeks.reduce((sum, week) => sum + (Number(week?.d) || 0), 0);
      if (!login || !avatar || !path || total <= 0) {
        return null;
      }
      const profileUrl = path.startsWith('http')
        ? path
        : `https://github.com${path.startsWith('/') ? path : `/${path}`}`;
      return {
        login,
        avatar,
        total,
        additions,
        deletions,
        profileUrl,
      };
    })
    .filter((item): item is Contributor => item !== null)
    .sort((left, right) => right.total - left.total);
}

export async function loadContributors(): Promise<Contributor[]> {
  const cached = getFreshContributorsCache();
  if (cached) {
    return cached;
  }
  const payload: unknown = await AppGo.GetGitHubContributors();
  const data = normalizeContributors(payload);
  if (data.length > 0) {
    contributorsCache.data = data;
    contributorsCache.expiresAt = Date.now() + CONTRIBUTORS_CACHE_TTL;
  } else {
    contributorsCache.data = null;
    contributorsCache.expiresAt = 0;
  }
  return data;
}
