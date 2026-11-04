import { createRouter, createWebHistory } from 'vue-router'
import { ensureSession, session } from './session'

// Client routes. Everything that is not /api or a built asset falls through to
// this router, so the server only ever serves index.html for these paths.
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      name: 'home',
      component: () => import('./views/HomeView.vue'),
    },
    {
      path: '/login',
      name: 'login',
      component: () => import('./views/LoginView.vue'),
    },
    {
      path: '/admin/settings',
      name: 'settings',
      component: () => import('./views/SettingsView.vue'),
      meta: { requiresAdmin: true },
    },
    {
      path: '/admin/logs',
      name: 'logs',
      component: () => import('./views/LogsView.vue'),
      meta: { requiresAdmin: true },
    },
    {
      path: '/admin/trash',
      name: 'trash',
      component: () => import('./views/TrashView.vue'),
      meta: { requiresAdmin: true },
    },
    {
      path: '/admin/shares',
      name: 'shares',
      component: () => import('./views/SharesView.vue'),
      meta: { requiresAdmin: true },
    },
    {
      path: '/admin/password',
      name: 'password',
      component: () => import('./views/PasswordView.vue'),
      meta: { requiresAdmin: true },
    },
    {
      path: '/s/:id/preview/:pathMatch(.*)*',
      name: 'preview',
      component: () => import('./views/PreviewView.vue'),
    },
    {
      path: '/s/:id/:pathMatch(.*)*',
      name: 'browse',
      component: () => import('./views/BrowseView.vue'),
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('./views/NotFoundView.vue'),
    },
  ],
})

router.beforeEach(async (to) => {
  if (!to.meta.requiresAdmin) return true
  try {
    await ensureSession()
  } catch {
    // Server unreachable: fall through to the redirect below.
  }
  if (!session.authenticated) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  return true
})
