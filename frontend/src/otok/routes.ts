/**
 * otok: fork 定制页面路由，在上游 router/index.ts 的 404 路由之前展开。
 */
import type { RouteRecordRaw } from 'vue-router'

export const otokRoutes: RouteRecordRaw[] = [
  {
    path: '/admin/orders/usdt-transfers',
    name: 'OtokAdminUsdtTransfers',
    component: () => import('@/views/admin/otok/OtokUsdtTransfersView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'USDT Transfers',
      titleKey: 'otok.nav.usdtTransfers',
      requiresPayment: true,
    },
  },
  {
    path: '/admin/otok/contact',
    name: 'OtokAdminContact',
    component: () => import('@/views/admin/otok/OtokContactSettingsView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Contact',
      titleKey: 'otok.nav.contactSettings',
    },
  },
  {
    path: '/admin/otok/quality',
    name: 'OtokAdminQuality',
    component: () => import('@/views/admin/otok/OtokQualitySettingsView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Quality Check',
      titleKey: 'otok.nav.qualitySettings',
    },
  },
  {
    path: '/otok/quality',
    name: 'OtokQuality',
    component: () => import('@/views/user/otok/OtokQualityView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: false,
      title: 'Quality Check',
      titleKey: 'otok.nav.quality',
    },
  },
]
