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
import Segmented from './components/ui/Segmented.vue'
import Select from './components/ui/Select.vue'

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

// Browser zoom gestures fight the charts' own pinch/wheel zoom. iOS Safari ignores user-scalable=no
// and touch-action for pinch, so its gesture events are cancelled; a trackpad pinch (ctrl+wheel) is
// cancelled only over a chart, so page zoom still works elsewhere on desktop.
for (const t of ['gesturestart', 'gesturechange', 'gestureend']) document.addEventListener(t, e => e.preventDefault(), { passive: false })
document.addEventListener('touchmove', e => { const s = (e as TouchEvent & { scale?: number }).scale; if (s !== undefined && s !== 1) e.preventDefault() }, { passive: false }) // older iOS
window.addEventListener('wheel', e => { if (e.ctrlKey && (e.target as Element | null)?.closest?.('.tv-lightweight-charts, canvas')) e.preventDefault() }, { passive: false })

startStream()
createApp(App).use(router).component('InfoTip', InfoTip).component('ScoringExplainer', ScoringExplainer).component('Segmented', Segmented).component('USelect', Select).mount('#app')
