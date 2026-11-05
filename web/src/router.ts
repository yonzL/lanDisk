import { createRouter, createWebHistory } from 'vue-router'
import { ensureSession, session } from './session'

// Client routes. The virtual root ("/") lists every shared directory as a folder;
// inside a share the same view lists files and subdirectories, so there is no
// separate landing page to keep in sync.
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      name: 'root',
      component: () => import('./views/BrowseView.vue'),
    },
    {
      path: '/login',
      name: 'login',
      component: () => import('./views/LoginView.vue'),
    },
    {
      path: '/admin/logs',
      name: 'logs',
      component: () => import('./views/LogsView.vue'),
      meta: { requiresAdmin: true },
    },
    {
      path: '/admin/settings',
      name: 'settings',
      component: () => import('./views/SettingsView.vue'),
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
