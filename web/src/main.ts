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
import { router } from './router'
import { vTooltip } from './directives/tooltip'
import './assets/styles.css'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.use(VueQueryPlugin)
app.directive('tooltip', vTooltip)
app.mount('#app')
