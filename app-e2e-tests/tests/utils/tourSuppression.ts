import { BrowserContext, Page } from "@playwright/test";

// The single localStorage entry the app keeps every user preference in (app/src/api1/user/index.js).
const USER_PREFERENCES_KEY = "nudgebee.userPreferences";

const SNOOZED_FOREVER = 4102444800000; // 2100-01-01 in epoch ms

// Mirrors the PREFERENCE_*_TOUR_SEEN keys in app/src/api1/user/index.js — add a new section tour's key here.
export const TOUR_SUPPRESSION_PREFERENCES: Record<string, unknown> = {
  app_tour_seen: true,
  app_tour_snoozed_until: SNOOZED_FOREVER,
  troubleshoot_tour_seen: true,
  troubleshoot_investigations_tour_seen: true,
  troubleshoot_kg_tour_seen: true,
  optimize_tour_seen: true,
  tickets_tour_seen: true,
  cloud_tour_seen: true,
};

// Marks every guided tour as already-seen so no tour dialog ever mounts — no dialog means no overlay to intercept clicks.
// Runs before any app JS on every document, so it survives redirects, reloads and client-side route changes.
export async function suppressTourPopups(target: BrowserContext | Page): Promise<void> {
  await target.addInitScript(
    ({ key, flags }) => {
      // A corrupt entry must not stop the flags being written.
      let prefs: Record<string, unknown> = {};
      try {
        prefs = JSON.parse(window.localStorage.getItem(key) || "{}") || {};
      } catch {
        prefs = {};
      }
      try {
        // Merged, not replaced, so runtime prefs like the last selected account survive.
        window.localStorage.setItem(key, JSON.stringify({ ...prefs, ...flags }));
      } catch {
        // localStorage is unavailable on opaque origins — no tour runs there anyway.
      }
    },
    { key: USER_PREFERENCES_KEY, flags: TOUR_SUPPRESSION_PREFERENCES }
  );
}
