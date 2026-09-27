import InfoTip from './components/InfoTip.vue'
import ScoringExplainer from './components/ScoringExplainer.vue'

declare module 'vue' {
  export interface GlobalComponents {
    InfoTip: typeof InfoTip
    ScoringExplainer: typeof ScoringExplainer
  }
}
export {}
