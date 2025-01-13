

const CACHE_NAME = 'my-cache-v1';
const ENDPOINTS_TO_CACHE = [
    '/static/crc32.js',
];

// Install event: Cache specific endpoints
self.addEventListener('install', event => {
    event.waitUntil(
        caches.open(CACHE_NAME).then(cache => {
            console.log('Opened cache');
            return cache.addAll(ENDPOINTS_TO_CACHE);
        })
    );
});

// Fetch event: Serve cached responses
self.addEventListener('fetch', event => {
    if (ENDPOINTS_TO_CACHE.some(endpoint => event.request.url.includes(endpoint))) {
        event.respondWith(
            caches.match(event.request).then(cachedResponse => {
                // Serve from cache if available, otherwise fetch from network
                return cachedResponse || fetch(event.request).then(networkResponse => {
                    return caches.open(CACHE_NAME).then(cache => {
                        cache.put(event.request, networkResponse.clone());
                        return networkResponse;
                    });
                });
            })
        );
    }
});

self.addEventListener('activate', event => {
    const cacheWhitelist = [CACHE_NAME];
    event.waitUntil(
        caches.keys().then(cacheNames => {
            return Promise.all(
                cacheNames.map(cacheName => {
                    if (!cacheWhitelist.includes(cacheName)) {
                        return caches.delete(cacheName);
                    }
                })
            );
        })
    );
});
