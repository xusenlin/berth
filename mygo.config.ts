// The configuration of `go tool mygo`. TypeScript rather than mygo.json so
// that Windows builds can take their own icon: mygo has one icon for every
// platform, and the macOS one leaves a margin that makes it look small on
// Windows. The Taskfile builds Windows with BERTH_ICON set.
export default {
  name: "Berth",
  identifier: "com.xusenlin.berth",
  version: "0.1.0",
  icon: process.env.BERTH_ICON || "design/icon.png",
  devUrl: "http://localhost:5173",
  devCommand: "pnpm --dir frontend dev",
  buildCommand: "pnpm --dir frontend build",
  frontendDist: "frontend/dist",
  bindings: "frontend/src/mygo.ts",
  out: "dist",
  macos: {
    notarize: {
      keychainProfile: "berth-notary",
    },
  },
  windows: {
    signCommand: "sh scripts/sign-windows.sh %1",
  },
};
