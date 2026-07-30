// VibeBridge Service Worker
//
// Cache strategy:
//   - Static assets (JS/CSS/SVG/HTML): stale-while-revalidate
//   - API calls (/status, /pairing/*, /agent/*): network-only (never cache)
//   - WebSocket upgrades: not intercepted (service workers can't intercept WS)
//
// Security:
//   - No auth tokens, session credentials, or response bodies from
//     authenticated endpoints are ever cached.
//   - The cache only holds public static build artifacts.

const CACHE_NAME = "vibebridge-v1";
const STATIC_ASSET_PATTERN = /\.(?:js|css|svg|png|jpg|jpeg|gif|woff2?|html?)$/;
const NEVER_CACHE = ["/status", "/pairing/", "/agent/", "/ws"];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) => cache.addAll(["/", "/favicon.svg"]))
  );
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k)))
    )
  );
  self.clients.claim();
});

self.addEventListener("fetch", (event) => {
  const { request } = event;

  // Only handle GET.
  if (request.method !== "GET") return;

  const url = new URL(request.url);

  // Never intercept WebSocket upgrades.
  if (request.headers.get("upgrade") === "websocket") return;

  // Never cache authenticated or dynamic endpoints.
  if (NEVER_CACHE.some((prefix) => url.pathname.startsWith(prefix))) return;

  // Stale-while-revalidate for static assets.
  if (STATIC_ASSET_PATTERN.test(url.pathname) || url.pathname === "/") {
    event.respondWith(
      caches.open(CACHE_NAME).then((cache) =>
        cache.match(request).then((cached) => {
          const fetchPromise = fetch(request).then((response) => {
            if (response.ok) {
              cache.put(request, response.clone());
            }
            return response;
          });
          return cached || fetchPromise;
        })
      )
    );
  }
});

// Update flow: when a new service worker takes over, notify clients.
self.addEventListener("controllerchange", () => {
  self.clients.matchAll().then((clients) => {
    clients.forEach((client) => client.postMessage({ type: "SW_UPDATED" }));
  });
});
