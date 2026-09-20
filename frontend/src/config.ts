// 全局配置文件
export const APP_VERSION = '1.3.4';
export const APP_BUILD_TIME = typeof __APP_BUILD_TIME__ === 'string' ? __APP_BUILD_TIME__ : '';
export const APP_GITHUB_REPO_URL = 'https://github.com/wmwlwmwl/LumeTerm';
export const APP_GITHUB_ISSUES_URL = `${APP_GITHUB_REPO_URL}/issues/new`;
export const APP_GITHUB_RELEASES_URL = `${APP_GITHUB_REPO_URL}/releases`;
const APP_GITHUB_REPO_PATH = new URL(APP_GITHUB_REPO_URL).pathname.replace(/^\/+/, '');
export const APP_GITHUB_RELEASE_API = `https://api.github.com/repos/${APP_GITHUB_REPO_PATH}/releases/latest`;
export const APP_GITHUB_ANDROID_REPO_URL = 'https://github.com/wmwlwmwl/Lumin-SSH-Android';
export const APP_GITHUB_ANDROID_RELEASES_URL = `${APP_GITHUB_ANDROID_REPO_URL}/releases`;
