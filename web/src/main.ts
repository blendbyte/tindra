import { createApp } from 'vue'
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
