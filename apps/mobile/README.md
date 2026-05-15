# Multica Mobile (iOS)

Expo + React Native iOS client for Multica. Independent from web/desktop — shares only types from `@multica/core/`. See [`CLAUDE.md`](./CLAUDE.md) for the locked tech-stack baseline and import rules.

## Scripts

| Command | Env file | Backend |
|---|---|---|
| `pnpm dev:mobile` | `.env.development.local` | your own (LAN IP of local backend) |
| `pnpm dev:mobile:staging` | `.env.staging` | staging |
| `pnpm ios:mobile:device` | `.env.development.local` | your own |
| `pnpm ios:mobile:device:staging` | `.env.staging` | staging |

`dev:*` runs Metro only (reuse an already-installed dev-client build). `ios:device:*` does a full native rebuild + install onto a physically-connected iPhone.

Bundle identifier and display name switch on `APP_ENV` (see `app.config.ts`), so Dev / Staging / Production variants can coexist on the same device.

## Build your own version onto your iPhone

For a self-host user who wants to run Multica mobile against their own / staging backend:

1. Get the prerequisites set up (Mac, Xcode, free Apple ID added in Xcode → Settings → Accounts, iPhone connected, Developer Mode enabled). Follow Expo's [Set up your environment](https://docs.expo.dev/get-started/set-up-your-environment/) — pick **Development build → iOS Device**.
2. Copy `.env.example` to `.env.staging` (or `.env.development.local`) and set `EXPO_PUBLIC_API_URL` to the backend you want to hit.
3. From repo root, run:

   ```bash
   pnpm ios:mobile:device:staging
   ```

   First build downloads CocoaPods + compiles React Native from source — expect 10-20 minutes. Subsequent builds are much faster.

The app installs to your phone and runs without the Mac after the build completes.

## 7-day signing limit

A free Apple ID signs builds for **7 days only**. After that the app refuses to launch. Plug back into the Mac and re-run `pnpm ios:mobile:device:staging` to re-sign — there is no workaround short of an Apple Developer Program account ($99/yr).

## Pointing at a different backend

Edit `EXPO_PUBLIC_API_URL` in `.env.staging` (or `.env.development.local`), then rebuild. The value is baked into the JS bundle at compile time; runtime swaps are not supported.

For local backend testing, use your Mac's LAN IP (`ipconfig getifaddr en0`), not `localhost`.
