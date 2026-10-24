import type { CapacitorConfig } from '@capacitor/cli'

// iOS app shell (see docs/ios.md). The web build is bundled into the app; API calls go to production.
const config: CapacitorConfig = {
  appId: 'top.gugenzzz.ctg',
  appName: 'OKX Helper',
  webDir: 'dist',
  backgroundColor: '#000000',
  ios: { contentInset: 'never', backgroundColor: '#000000', scrollEnabled: true },
  plugins: {
    SplashScreen: { launchShowDuration: 0, launchAutoHide: true, backgroundColor: '#000000' },
    StatusBar: { style: 'DARK', overlaysWebView: false, backgroundColor: '#000000' },
  },
}

export default config
