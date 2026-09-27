import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import Scanner from './views/Scanner.vue'
import InstrumentPage from './views/InstrumentPage.vue'
import Rules from './views/Rules.vue'
import Status from './views/Status.vue'
import Positions from './views/Positions.vue'
import SettingsView from './views/Settings.vue'
import { startStream } from './store'
import './style.css'
import InfoTip from './components/InfoTip.vue'
import ScoringExplainer from './components/ScoringExplainer.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: Scanner },
    { path: '/instrument/:id', component: InstrumentPage, props: true },
    { path: '/rules/:id?', component: Rules, props: true },
    { path: '/status', component: Status },
    { path: '/positions', component: Positions },
    { path: '/settings', component: SettingsView },
  ],
  scrollBehavior: (_to, _from, saved) => saved || { top: 0 },
})

startStream()
createApp(App).use(router).component('InfoTip', InfoTip).component('ScoringExplainer', ScoringExplainer).mount('#app')
