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

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
} from 'react';

const ThemeContext = createContext(null);
export const useTheme = () => useContext(ThemeContext);

const ActualThemeContext = createContext(null);
export const useActualTheme = () => useContext(ActualThemeContext);

const SetThemeContext = createContext(null);
export const useSetTheme = () => useContext(SetThemeContext);

/**
 * 主题：**只有亮色**（2026-10-10 下线夜间模式）。
 *
 * 为什么不再跟着系统走：面板要和营销站用同一套设计语言，营销站是纯亮色；
 * 而且暗色下的 Semi 变量（`body[theme-mode=dark]`）会覆盖我们映射的 Kumo 令牌，
 * 等于同一套界面出现两种观感。产品的决定是：一套皮肤，别让用户选。
 *
 * 保留 Provider / useTheme / useSetTheme 的形状 —— 有几十处组件在用，
 * 直接把 Context 删掉会连带炸掉它们；`setTheme` 变成空操作，旧的
 * localStorage 值会在挂载时被清掉。
 */
export const ThemeProvider = ({ children }) => {
  const theme = 'light';
  const actualTheme = 'light';

  // 清掉可能残留的暗色标记（用户以前选过 dark，或被 @media prefers-color-scheme 影响过）
  useEffect(() => {
    const body = document.body;
    body.removeAttribute('theme-mode');
    document.documentElement.classList.remove('dark');
    try {
      localStorage.setItem('theme-mode', 'light');
    } catch {
      /* 隐私模式下 localStorage 可能不可写，不影响渲染 */
    }
  }, []);

  const setTheme = useCallback(() => {}, []);

  return (
    <SetThemeContext.Provider value={setTheme}>
      <ActualThemeContext.Provider value={actualTheme}>
        <ThemeContext.Provider value={theme}>{children}</ThemeContext.Provider>
      </ActualThemeContext.Provider>
    </SetThemeContext.Provider>
  );
};
