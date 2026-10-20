/*
 * 从营销站的登录态接管本 SPA 的会话，并把非 root 账号挡在门外（2026-10-20）。
 *
 * 背景：注册/登录已经全部搬到 https://www.tokwork.com，登录成功后只有
 * `api.tokwork.com` 上的 session cookie，**不会**给 admin.tokwork.com 写
 * localStorage。本 SPA 的 PrivateRoute 只认 localStorage.user，于是
 * 一进 admin 就跳 www/login，登完回来又跳，形成死循环。
 *
 * 这里在 React 渲染前用 cookie 调一次 `/api/user/whoami`（该接口只认
 * session cookie，不要求 New-Api-User 头），成功就把最小用户对象写进
 * localStorage，后续请求由 api.js 的 request 拦截器补 `New-API-User` 头。
 *
 * 门禁：admin.tokwork.com **只给 root（项目主人）用**，客户一律用营销站的
 * /console。所以：
 *   - 已登录但不是 root → 直接送去 www.tokwork.com/console，不渲染本 SPA
 *   - 未登录 → 送去营销站登录（带 next 回到这里）；账号类路径除外，
 *     它们由 App.jsx 里的 AuthSiteRedirect 自己转到营销站（要保留查询串）
 * 这只是界面层的门禁；真正的权限仍由后端按角色校验（客户本来就调不了管理接口）。
 *
 * 返回值：true = 继续渲染；false = 已经在跳转，不要渲染。
 */

import { AUTH_SITE_URL } from './auth';

const API_BASE = import.meta.env.VITE_REACT_APP_SERVER_URL || '';

// 与后端 common.RoleRootUser 一致
const ROLE_ROOT = 100;

// 这些路径交给 AuthSiteRedirect 处理（它会把查询串原样带去营销站）
const AUTH_PATHS = ['/login', '/register', '/reset', '/user/reset'];

function leave(url) {
  localStorage.removeItem('user');
  window.location.replace(url);
  return false;
}

function cachedUserIsRoot() {
  try {
    const user = JSON.parse(localStorage.getItem('user') || 'null');
    return Boolean(user && user.role >= ROLE_ROOT);
  } catch {
    return false;
  }
}

export async function bootstrapSession() {
  if (typeof window === 'undefined') return true;

  const path = window.location.pathname;
  const onAuthPath = AUTH_PATHS.some((p) => path === p || path.startsWith(`${p}/`));

  try {
    const res = await fetch(`${API_BASE}/api/user/whoami`, {
      credentials: 'include',
      headers: { Accept: 'application/json' },
    });

    // 没有有效 session：去营销站登录，登完带回本后台
    if (res.status === 401 || res.status === 403) {
      if (onAuthPath) {
        localStorage.removeItem('user');
        return true;
      }
      const next = encodeURIComponent(`${window.location.origin}/console`);
      return leave(`${AUTH_SITE_URL}/login?next=${next}`);
    }
    // 5xx / 网络层问题：保留现状，避免误登出；但缓存里不是 root 的身份不留
    if (!res.ok) {
      if (!cachedUserIsRoot()) localStorage.removeItem('user');
      return true;
    }

    const body = await res.json();
    const user = body?.data;
    if (!body?.success || !user?.id) {
      localStorage.removeItem('user');
      return true;
    }

    // 客户账号：本后台不对它开放，送回客户门户
    if (!(user.role >= ROLE_ROOT)) {
      return leave(`${AUTH_SITE_URL}/console`);
    }

    localStorage.setItem(
      'user',
      JSON.stringify({
        id: user.id,
        username: user.username,
        display_name: user.display_name || user.username,
        role: user.role,
        status: user.status,
        group: user.group,
      }),
    );
    return true;
  } catch {
    // 网络失败：保留 localStorage 里的 root 身份，避免把自己误登出
    if (!cachedUserIsRoot()) localStorage.removeItem('user');
    return true;
  }
}
