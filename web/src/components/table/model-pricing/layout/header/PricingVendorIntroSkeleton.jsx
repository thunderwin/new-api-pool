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

import React, { memo } from 'react';
import { Card, Skeleton } from '@douyinfe/semi-ui';

const THEME_COLORS = {
  // 头像占位块：跟 PricingVendorIntro 的 Kumo info tint 保持一致
  avatar: {
    background: 'rgba(var(--semi-blue-5), 0.12)',
    border: 'rgba(var(--semi-blue-5), 0.2)',
  },
  neutral: {
    background: 'rgba(156, 163, 175, 0.1)',
    border: 'rgba(156, 163, 175, 0.2)',
  },
};

const SIZES = {
  title: { width: { all: 120, specific: 100 }, height: 24 },
  tag: { width: 80, height: 20 },
  description: { height: 14 },
  avatar: { width: 40, height: 40 },
  searchInput: { height: 32 },
  button: { width: 80, height: 32 },
};

const SKELETON_STYLES = {
  // 骨架块改成 Kumo 的中性填充（原来是压在蓝色封面图上的白块）
  title: {
    backgroundColor: 'var(--semi-color-fill-0)',
    borderRadius: 6,
  },
  tag: {
    backgroundColor: 'var(--semi-color-fill-0)',
    borderRadius: 9999,
  },
  description: {
    backgroundColor: 'var(--semi-color-fill-0)',
    borderRadius: 4,
  },
  avatar: {
    backgroundColor: THEME_COLORS.avatar.background,
    borderRadius: 8,
    border: `1px solid ${THEME_COLORS.avatar.border}`,
  },
  searchInput: {
    backgroundColor: THEME_COLORS.neutral.background,
    borderRadius: 8,
    border: `1px solid ${THEME_COLORS.neutral.border}`,
  },
  button: {
    backgroundColor: THEME_COLORS.neutral.background,
    borderRadius: 8,
    border: `1px solid ${THEME_COLORS.neutral.border}`,
  },
};

const createSkeletonRect = (style = {}, key = null) => (
  <div key={key} className='animate-pulse' style={style} />
);

const PricingVendorIntroSkeleton = memo(
  ({ isAllVendors = false, isMobile = false }) => {
    const placeholder = (
      <Card className='tk-card'>
        <div className='flex items-center justify-between gap-4 mb-4'>
          <div className='flex-1 min-w-0'>
            <div className='flex flex-row flex-wrap items-center gap-2 sm:gap-3 mb-2'>
              {createSkeletonRect(
                {
                  ...SKELETON_STYLES.title,
                  width: isAllVendors
                    ? SIZES.title.width.all
                    : SIZES.title.width.specific,
                  height: SIZES.title.height,
                },
                'title',
              )}
              {createSkeletonRect(
                {
                  ...SKELETON_STYLES.tag,
                  width: SIZES.tag.width,
                  height: SIZES.tag.height,
                },
                'tag',
              )}
            </div>
            <div className='space-y-2'>
              {createSkeletonRect(
                {
                  ...SKELETON_STYLES.description,
                  width: '100%',
                  height: SIZES.description.height,
                },
                'desc1',
              )}
              {createSkeletonRect(
                {
                  ...SKELETON_STYLES.description,
                  width: '75%',
                  height: SIZES.description.height,
                },
                'desc2',
              )}
            </div>
          </div>

          <div className='flex-shrink-0 tk-tile w-16 h-16 flex items-center justify-center'>
            {createSkeletonRect(
              {
                ...SKELETON_STYLES.avatar,
                width: SIZES.avatar.width,
                height: SIZES.avatar.height,
              },
              'avatar',
            )}
          </div>
        </div>

        <div className='flex items-center gap-2 w-full'>
          <div className='flex-1'>
            {createSkeletonRect(
              {
                ...SKELETON_STYLES.searchInput,
                width: '100%',
                height: SIZES.searchInput.height,
              },
              'search',
            )}
          </div>

          {createSkeletonRect(
            {
              ...SKELETON_STYLES.button,
              width: SIZES.button.width,
              height: SIZES.button.height,
            },
            'copy-button',
          )}

          {isMobile &&
            createSkeletonRect(
              {
                ...SKELETON_STYLES.button,
                width: SIZES.button.width,
                height: SIZES.button.height,
              },
              'filter-button',
            )}
        </div>
      </Card>
    );

    return (
      <Skeleton loading={true} active placeholder={placeholder}></Skeleton>
    );
  },
);

PricingVendorIntroSkeleton.displayName = 'PricingVendorIntroSkeleton';

export default PricingVendorIntroSkeleton;
