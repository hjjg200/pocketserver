

const CACHE_NAME = 'pocketserver-v1';
const STATIC_RESOURCES = ['/static/crc32.js'];
const API_ENDPOINTS = ['/list'];


// Cache-first strategy for static resources
async function handleStaticResource(request) {
    const cache = await caches.open(CACHE_NAME);
    const cachedResponse = await cache.match(request);
    
    if (cachedResponse) {
        // We have a cache, check if we can validate it
        const cachedLastModified = cachedResponse.headers.get('Last-Modified');
        
        if (cachedLastModified) {
            try {
                // Validate cache with conditional request
                const conditionalRequest = new Request(request, {
                    headers: {
                        'If-Modified-Since': cachedLastModified
                    }
                });
                
                const networkResponse = await fetch(conditionalRequest);
                
                if (networkResponse.status === 304) {
                    // Cache is still valid
                    console.log('Static resource not modified, using cache');
                    return cachedResponse;
                } else {
                    // Resource changed, update cache quietly
                    console.log('Static resource modified, updating cache');
                    await cache.put(request, networkResponse.clone());
                    return networkResponse;
                }
            } catch (error) {
                // Network error, use cache
                console.log('Network error for static resource, using cache');
                return cachedResponse;
            }
        }
        // No Last-Modified header, just use cache
        return cachedResponse;
    }
    
    // Nothing in cache, fetch and cache
    try {
        const networkResponse = await fetch(request);
        await cache.put(request, networkResponse.clone());
        return networkResponse;
    } catch (error) {
        return new Response('Network error', { status: 500 });
    }
}

// Network-first strategy for API requests
async function handleApiRequest(request) {
    const cache = await caches.open(CACHE_NAME);
    
    try {
        // Always try network first
        const networkResponse = await fetch(request);
        
        // Cache the new response
        await cache.put(request, networkResponse.clone());
        console.log('API: Fresh data fetched and cached');
        return networkResponse;
        
    } catch (error) {
        console.log('Network error for API request, trying cache');
        
        // Network failed, try cache
        const cachedResponse = await cache.match(request);
        if (cachedResponse) {
            const cachedDate = cachedResponse.headers.get('date');
            const cacheAge = cachedDate ? (Date.now() - new Date(cachedDate).getTime()) : Infinity;
            
            // Log cache age for debugging
            console.log(`Using cached API response from ${Math.round(cacheAge / 1000)} seconds ago`);
            
            return cachedResponse;
        }
        
        // No cache available
        return new Response('Network error and no cache available', { status: 500 });
    }
}


self.addEventListener('fetch', event => {
    const url = new URL(event.request.url);
    
    if (STATIC_RESOURCES.some(resource => url.pathname.includes(resource))) {
        event.respondWith(handleStaticResource(event.request));
    } else if (API_ENDPOINTS.some(endpoint => url.pathname.includes(endpoint))) {
        event.respondWith(handleStaticResource(event.request));
        //event.respondWith(handleApiRequest(event.request));
    }
});

self.addEventListener('install', event => {
    event.waitUntil(
        caches.open(CACHE_NAME)
            .then(cache => {
                console.log('Pre-caching static resources');
                return cache.addAll(STATIC_RESOURCES);
            })
    );
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

