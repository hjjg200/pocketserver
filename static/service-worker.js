
//importScripts('/static/utility.js'); no browser API

const CACHE_NAME = 'pocketserver-v1.3';
const DB_NAME = 'pocketserver-db';
const DB_STORE = 'store';
const STATIC_RESOURCES = ['/'];
const API_ENDPOINTS = ['/list'];


// Cache-first strategy for static resources
async function handleStaticResource(request) {
    const cache = await caches.open(CACHE_NAME);
    const cachedResponse = await cache.match(request);

    console.log(request);
    
    if (cachedResponse) {
        // We have a cache, check if we can validate it
        const etag = cachedResponse.headers.get('Etag');
        const cachedLastModified = cachedResponse.headers.get('Last-Modified');
        
        const headers = new Headers();
        if (etag) {
            headers.set('If-None-Match', etag); // Use ETag as-is
        }
    
        if (cachedLastModified) {
            headers.set('If-Modified-Since', cachedLastModified); // Use Last-Modified if present
        }

        if (cachedLastModified) {
            try {
                // Validate cache with conditional request
                const conditionalRequest = new Request(request, {
                    headers
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

// IndexedDB utility functions
async function getFromIndexedDB(key) {
    return new Promise((resolve, reject) => {
        const request = indexedDB.open(DB_NAME, 1);
        request.onupgradeneeded = (event) => {
            const db = event.target.result;
            if (!db.objectStoreNames.contains(DB_STORE)) {
                db.createObjectStore(DB_STORE, { keyPath: 'key' });
            }
        };

        request.onsuccess = (event) => {
            const db = event.target.result;
            const transaction = db.transaction(DB_STORE, 'readonly');
            const store = transaction.objectStore(DB_STORE);
            const getRequest = store.get(key);

            getRequest.onsuccess = () => {
                resolve(getRequest.result ? getRequest.result.value : null);
            };

            getRequest.onerror = () => reject('Error fetching from IndexedDB');
        };

        request.onerror = () => reject('Error opening IndexedDB');
    });
}

function serializeHeaders(headers) {
    const serialized = {};
    for (const [key, value] of headers.entries()) {
        serialized[key] = value;
    }
    return serialized;
}

function deserializeHeaders(serializedHeaders) {
    return new Headers(serializedHeaders);
}


async function storeInIndexedDB(key, value, metadata = {}) {
    return new Promise((resolve, reject) => {
        const request = indexedDB.open(DB_NAME, 1);
        request.onupgradeneeded = (event) => {
            const db = event.target.result;
            if (!db.objectStoreNames.contains(DB_STORE)) {
                db.createObjectStore(DB_STORE, { keyPath: 'key' });
            }
        };

        request.onsuccess = (event) => {
            const db = event.target.result;
            const transaction = db.transaction(DB_STORE, 'readwrite');
            const store = transaction.objectStore(DB_STORE);
            store.put({ key, value, metadata });
            transaction.oncomplete = () => resolve();
            transaction.onerror = () => reject('Error storing in IndexedDB');
        };

        request.onerror = () => reject('Error opening IndexedDB');
    });
}


// Cache-first strategy for static resources using IndexedDB
async function handleIndexedDBResource(request) {
    const requestKey = request.url; // Use the request URL as the key
    const cachedData = await getFromIndexedDB(requestKey);

    if (cachedData) {
        const { value: cachedResponse, metadata } = cachedData;
        const etag = metadata?.etag;
        const cachedLastModified = metadata?.lastModified;

        const headers = new Headers();
        if (etag) {
            headers.set('If-None-Match', etag);
        }

        if (cachedLastModified) {
            headers.set('If-Modified-Since', cachedLastModified);
        }

        if (cachedLastModified || etag) {
            try {
                // Validate cache with conditional request
                const conditionalRequest = new Request(request, {
                    headers,
                });
                const networkResponse = await fetch(conditionalRequest);

                if (networkResponse.status === 304) {
                    // Cache is still valid
                    console.log('Static resource not modified, using cache');
                    return new Response(cachedResponse.body, cachedResponse.options);
                } else {
                    // Resource changed, update cache quietly
                    console.log('Static resource modified, updating IndexedDB');
                    const newMetadata = {
                        etag: networkResponse.headers.get('Etag'),
                        lastModified: networkResponse.headers.get('Last-Modified'),
                    };
                    const newValue = {
                        body: await networkResponse.clone().text(),
                        options: {
                            headers: networkResponse.headers,
                            status: networkResponse.status,
                            statusText: networkResponse.statusText,
                        },
                    };
                    await storeInIndexedDB(requestKey, {
                        body: await networkResponse.clone().text(),
                        options: {
                            headers: serializeHeaders(networkResponse.headers),
                            status: networkResponse.status,
                            statusText: networkResponse.statusText,
                        },
                    }, newMetadata);
                    return networkResponse;
                }
            } catch (error) {
                console.log('Network error for static resource, using cache');
                return new Response(cachedResponse.body, {
                    headers: deserializeHeaders(cachedResponse.options.headers),
                    status: cachedResponse.options.status,
                    statusText: cachedResponse.options.statusText,
                });
            }
        }

        // No validation headers, just use the cache
        return new Response(cachedResponse.body, {
            headers: deserializeHeaders(cachedResponse.options.headers),
            status: cachedResponse.options.status,
            statusText: cachedResponse.options.statusText,
        });
    }

    // Nothing in cache, fetch and store
    try {
        const networkResponse = await fetch(request);
        const newMetadata = {
            etag: networkResponse.headers.get('Etag'),
            lastModified: networkResponse.headers.get('Last-Modified'),
        };
        const newValue = {
            body: await networkResponse.clone().text(),
            options: {
                headers: networkResponse.headers,
                status: networkResponse.status,
                statusText: networkResponse.statusText,
            },
        };
        await storeInIndexedDB(requestKey, {
            body: await networkResponse.clone().text(),
            options: {
                headers: serializeHeaders(networkResponse.headers),
                status: networkResponse.status,
                statusText: networkResponse.statusText,
            },
        }, newMetadata);
        return networkResponse;
    } catch (error) {
        return new Response('Network error', { status: 500 });
    }
}





self.addEventListener('fetch', event => {

    const url       = new URL(event.request.url);
    const path      = url.pathname; // Extract the pathname (a string)
    
    if (STATIC_RESOURCES.some(resource => url.pathname.includes(resource))) {
        event.respondWith(handleStaticResource(event.request));
    } else if (API_ENDPOINTS.some(endpoint => url.pathname.includes(endpoint))) {
        event.respondWith(handleStaticResource(event.request));
        //event.respondWith(handleApiRequest(event.request));
    } else if (path.startsWith('/view')) {
        event.respondWith(handleIndexedDBResource(event.request));

        return;
        event.respondWith(
            fetch(event.request)
                .then((response) => {
                    const clonedResponse = response.clone();
                    clonedResponse.blob().then((blob) => {
                        // Store the response in IndexedDB
                        storeInIndexedDB(requestUrl, blob);
                    });
                    return response;
                })
                .catch(() => {
                    // Serve from IndexedDB if offline
                    return getFromIndexedDB(requestUrl).then((data) => {
                        return new Response(data, {
                            headers: { 'Content-Type': 'image/jpeg' },
                        });
                    });
                })
        );
    }





});



// Cache-first strategy for static resources
async function handleStaticResource(request) {
    const cache = await caches.open(CACHE_NAME);
    const cachedResponse = await cache.match(request);
    
    if (cachedResponse) {
        // We have a cache, check if we can validate it
        const etag = cachedResponse.headers.get('Etag');
        const cachedLastModified = cachedResponse.headers.get('Last-Modified');
        
        const headers = new Headers();
        if (etag) {
            headers.set('If-None-Match', etag); // Use ETag as-is
        }
    
        if (cachedLastModified) {
            headers.set('If-Modified-Since', cachedLastModified); // Use Last-Modified if present
        }

        if (cachedLastModified) {
            try {
                // Validate cache with conditional request
                const conditionalRequest = new Request(request, {
                    headers
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

self.addEventListener('install', (event) => {
    event.waitUntil(
        fetch('/static/') // Fetch list of static files
            .then((response) => response.json())
            .then((masterJson) => {
                files = Object.keys(masterJson);
                STATIC_RESOURCES.push(...files);

                return caches.open(CACHE_NAME).then((cache) => {
                    console.log('Caching static files:', files);
                    return cache.addAll(files);
                });
            })
            .catch((err) => {
                console.error('Failed to fetch static files list:', err);
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

