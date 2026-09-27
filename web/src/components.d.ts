import InfoTip from './components/InfoTip.vue'
import ScoringExplainer from './components/ScoringExplainer.vue'
import Segmented from './components/ui/Segmented.vue'
import Select from './components/ui/Select.vue'

declare module 'vue' {
  export interface GlobalComponents {
    InfoTip: typeof InfoTip
    ScoringExplainer: typeof ScoringExplainer
    Segmented: typeof Segmented
    USelect: typeof Select
  }
}
export {}
