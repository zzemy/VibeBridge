import type { CapacitorConfig } from "@capacitor/cli";

const config: CapacitorConfig = {
  appId: "com.vibebridge.app",
  appName: "VibeBridge",
  webDir: "dist",
  backgroundColor: "#09090b",
  server: {
    // The web client is bundled into the native app; no live server.
    androidScheme: "https",
    iosScheme: "https",
  },
  android: {
    // Allow mixed content only for local dev relay without TLS.
    // Production builds should use TLS on the relay.
    allowMixedContent: false,
    backgroundColor: "#09090b",
  },
  ios: {
    backgroundColor: "#09090b",
    // Disable scroll bounce for terminal UX.
    scrollEnabled: false,
  },
  plugins: {
    SplashScreen: {
      launchShowDuration: 500,
      backgroundColor: "#09090b",
      showSpinner: false,
    },
    // Push notifications configuration. The notification pipeline
    // is intentionally minimal: the Agent can send a push when a
    // session is waiting for input or an attachment transfer
    // completes. The relay does NOT have push credentials.
    PushNotifications: {
      presentationOptions: ["badge", "sound", "alert"],
    },
  },
};

export default config;
