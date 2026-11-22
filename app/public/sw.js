// Minimal pass-through service worker to satisfy Chromium PWA install heuristics.
// Zero-caching: does not cache assets or HTML, avoiding stale cache and storage growth.

self.addEventListener('install', () => {
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('fetch', () => {
  // Pass-through: network only, zero caching
});
