/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React from 'react';
import { Navigate } from 'react-router-dom';
import { history } from './history';

/**
 * 账号入口所在的站点（营销站）。
 *
 * **注册、登录、找回密码都在营销站完成**，面板不再自己渲染这些页面（2026-10-10）：
 * 客户从营销站登录/注册后带着 `?sso_user=<id>` 跳进来，cookie 已经种好，直接就是登录态。
 * 默认值写死成生产域名，可用构建变量 `VITE_AUTH_SITE_URL` 覆盖（本地联调时指向
 * `http://localhost:3000` 之类的本地营销站）。
 */
export const AUTH_SITE_URL = (
  import.meta.env.VITE_AUTH_SITE_URL || 'https://www.tokwork.com'
).replace(/\/+$/, '');

/**
 * 把控制台的账号类路由原样转到营销站，**查询串一起带上** ——
 * `/login?expired=true` 要能让营销站显示"登录已过期"，
 * `/user/reset?email=…&token=…` 更是离开查询串就废了。
 */
export function AuthSiteRedirect({ path }) {
  React.useEffect(() => {
    window.location.replace(`${AUTH_SITE_URL}${path}${window.location.search}`);
  }, [path]);
  return null;
}

export function authHeader() {
  // return authorization header with jwt token
  let user = JSON.parse(localStorage.getItem('user'));

  if (user && user.token) {
    return { Authorization: 'Bearer ' + user.token };
  } else {
    return {};
  }
}

export const AuthRedirect = ({ children }) => {
  const user = localStorage.getItem('user');

  if (user) {
    return <Navigate to='/console' replace />;
  }

  return children;
};

function PrivateRoute({ children }) {
  if (!localStorage.getItem('user')) {
    return <Navigate to='/login' state={{ from: history.location }} />;
  }
  return children;
}

export function AdminRoute({ children }) {
  const raw = localStorage.getItem('user');
  if (!raw) {
    return <Navigate to='/login' state={{ from: history.location }} />;
  }
  try {
    const user = JSON.parse(raw);
    if (user && typeof user.role === 'number' && user.role >= 10) {
      return children;
    }
  } catch (e) {
    // ignore
  }
  return <Navigate to='/forbidden' replace />;
}

export { PrivateRoute };
