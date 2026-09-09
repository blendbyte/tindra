import { createApp } from 'vue'

// When a new deploy lands, preloaded chunk hashes change. Intercept the failure
// and do a hard reload so the browser fetches fresh assets instead of erroring.
window.addEventListener('vite:preloadError', (event) => {
  event.preventDefault()
  window.location.reload()
})
import { createPinia } from 'pinia'
import { VueQueryPlugin } from '@tanstack/vue-query'
import App from './App.vue'
import { createQueryClient } from './api/queryClient'
import { router } from './router'
import { installInvestigationRouter } from './router/investigation'
import { vTooltip } from './directives/tooltip'
import './assets/styles.css'

const app = createApp(App)
const pinia = createPinia()
app.use(pinia)
installInvestigationRouter(router, pinia)
app.use(router)
app.use(VueQueryPlugin, { queryClient: createQueryClient() })
app.directive('tooltip', vTooltip)
app.mount('#app')
