
//importScripts('/static/utility.js'); no browser API

const CACHE_NAME = 'pocketserver-v1.20';
const DB_NAME = CACHE_NAME + '-db';
const DB_STORE = 'store';
const STATIC_RESOURCES = [''];
const API_ENDPOINTS = ['/list'];
const X_PRESERVED_ALBUMS = "X-Preserved-Albums";

let gPreservedAlbums = [];




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

// Function to clean up IndexedDB entries based on URL query
let handleCleanupQuery = null;
function setTimeoutCleanupQuery(timeoutMs = 1000 * 20) {
    const dbRequest = indexedDB.open(DB_NAME, 1);

    dbRequest.onupgradeneeded = (event) => {
        const db = event.target.result;
        if (!db.objectStoreNames.contains(DB_STORE)) {
            db.createObjectStore(DB_STORE, { keyPath: 'key' }); // Assuming 'key' is the primary key
        }
    };

    dbRequest.onsuccess = (event) => {
        const db = event.target.result;

        // Set up periodic cleanup
        clearTimeout(handleCleanupQuery);
        handleCleanupQuery = setTimeout(() => {

            const transaction = db.transaction(DB_STORE, 'readwrite');
            const store = transaction.objectStore(DB_STORE);

            store.openCursor().onsuccess = (cursorEvent) => {
                const cursor = cursorEvent.target.result;
                if (cursor) {
                    try {
                        const url = new URL(cursor.key);
                        const album = url.searchParams.get("album") || "";

                        // Check if the query parameter matches the target value
                        if (!gPreservedAlbums.includes(album)) {
                            console.log('Deleting record with matching query:', url);
                            cursor.delete(); // Delete the record
                        }
                    } catch (error) {
                        //console.error('Error parsing URL or query parameters:', error);
                    }

                    cursor.continue(); // Move to the next record
                }
            };

            transaction.oncomplete = () => {
                console.log('Periodic cleanup complete.');
            };

            transaction.onerror = (err) => {
                console.error('Error during periodic cleanup:', err);
            };
        }, timeoutMs);
    };

    dbRequest.onerror = (err) => {
        console.error('Error opening IndexedDB:', err);
    };
}




async function checkPreservedAlbumsChanged(fetchResponse) {
    
    const cached = await getFromIndexedDB(X_PRESERVED_ALBUMS);

    if (cached && cached.value) {
        gPreservedAlbums = cached.value;
    }

    const b64 = fetchResponse.headers.get(X_PRESERVED_ALBUMS);

    if (!b64)
        return;

    const rhs = JSON.parse(atob(b64) || "[]");
    rhs.sort();
    const same = (gPreservedAlbums.length == rhs.length) &&
        gPreservedAlbums.every((value, index) => value === rhs[index]);

    console.log(same, gPreservedAlbums, rhs, b64);
    if (!same) {
        gPreservedAlbums = rhs;
        
        await storeInIndexedDB(X_PRESERVED_ALBUMS, rhs);
        setTimeoutCleanupQuery();
    }

}








async function handleIndexedDBResource(request) {
    const requestKey = request.url; // Use the request URL as the key
    let cachedData;

    const url = new URL(requestKey);
    const album = url.searchParams.get("album") || "";

    try {
        cachedData = await getFromIndexedDB(requestKey);
    } catch (error) {
        console.error('Failed to get data from IndexedDB:', error);
        cachedData = null;
    }

    if (gPreservedAlbums.includes(album) && cachedData) {
        const { value: cachedResponse, metadata } = cachedData || {};
        const etag = metadata?.etag;
        const cachedLastModified = metadata?.lastModified;

        const headers = new Headers();
        if (etag) headers.set('If-None-Match', etag);
        if (cachedLastModified) headers.set('If-Modified-Since', cachedLastModified);

        try {
            const conditionalRequest = new Request(request, { headers });
            const networkResponse = await fetch(conditionalRequest);

            await checkPreservedAlbumsChanged(networkResponse);

            if (networkResponse.status === 304) {
                console.log('Static resource not modified, using cache');
                if (cachedResponse) {
                    const responseHeaders = deserializeHeaders(cachedResponse.options?.headers || {});
                    return new Response(cachedResponse.body, {
                        headers: responseHeaders,
                        status: cachedResponse.options?.status || 200,
                        statusText: cachedResponse.options?.statusText || 'OK',
                    });
                }
            } else {
                return await updateCacheAndRespond(networkResponse, requestKey, album);
            }
        } catch (error) {
            console.log('Network error for static resource, using cache:', error);
            if (cachedResponse) {
                const responseHeaders = deserializeHeaders(cachedResponse.options?.headers || {});
                return new Response(cachedResponse.body, {
                    headers: responseHeaders,
                    status: cachedResponse.options?.status || 200,
                    statusText: cachedResponse.options?.statusText || 'OK',
                });
            }
        }
    }

    // Nothing in cache, fetch and store
    try {
        const networkResponse = await fetch(request);
        await checkPreservedAlbumsChanged(networkResponse);
        return await updateCacheAndRespond(networkResponse, requestKey, album);
    } catch (error) {
        console.error('Network error:', error);
        return new Response('Network error', { status: 500 });
    }
}

// Helper function to update IndexedDB and return the response
async function updateCacheAndRespond(networkResponse, requestKey, album) {
    if (gPreservedAlbums.includes(album)) {
        const newMetadata = {
            etag: networkResponse.headers.get('Etag'),
            lastModified: networkResponse.headers.get('Last-Modified'),
        };

        let responseBody;
        const contentType = networkResponse.headers.get('Content-Type') || '';

        if (contentType.includes('application/json') || contentType.startsWith('text/')) {
            responseBody = await networkResponse.clone().text();
        } else {
            responseBody = await networkResponse.clone().arrayBuffer();
        }

        const newValue = {
            body: responseBody,
            options: {
                headers: serializeHeaders(networkResponse.headers),
                status: networkResponse.status,
                statusText: networkResponse.statusText,
            },
        };

        try {
            console.log("Storing in IndexedDB:", requestKey);
            await storeInIndexedDB(requestKey, newValue, newMetadata);
        } catch (error) {
            console.error('Failed to store data in IndexedDB:', error);
        }
    }

    const clonedResponse = networkResponse.clone();
    const responseHeaders = new Headers(clonedResponse.headers);

    return new Response(clonedResponse.body, {
        headers: responseHeaders,
        status: clonedResponse.status,
        statusText: clonedResponse.statusText,
    });
}
















// Cache-first strategy for static resources using IndexedDB
async function handleIndexedDBResource2(request) {
    const requestKey = request.url; // Use the request URL as the key
    let cachedData;

    const url = new URL(requestKey);
    const album = url.searchParams.get("album") || "";

    try {
        cachedData = await getFromIndexedDB(requestKey);
    } catch (error) {
        console.error('Failed to get data from IndexedDB:', error);
        cachedData = null;
    }

    if (gPreservedAlbums.includes(album) && cachedData) {
        const { value: cachedResponse, metadata } = cachedData || {};
        const etag = metadata?.etag;
        const cachedLastModified = metadata?.lastModified;

        const headers = new Headers();
        if (etag) headers.set('If-None-Match', etag);
        if (cachedLastModified) headers.set('If-Modified-Since', cachedLastModified);

        try {
            const conditionalRequest = new Request(request, { headers });
            const networkResponse = await fetch(conditionalRequest);     

            await checkPreservedAlbumsChanged(networkResponse);

            if (networkResponse.status === 304) {

                console.log('Static resource not modified, using cache');
                if (cachedResponse) {
                    return new Response(cachedResponse.body, {
                        headers: deserializeHeaders(cachedResponse.options?.headers || {}),
                        status: cachedResponse.options?.status || 200,
                        statusText: cachedResponse.options?.statusText || 'OK',
                    });
                }

            } else {

                console.log('Static resource modified, updating IndexedDB');

                const newMetadata = {
                    etag: networkResponse.headers.get('Etag'),
                    lastModified: networkResponse.headers.get('Last-Modified'),
                };
                    
                let responseBody;
                const contentType = networkResponse.headers.get('Content-Type') || '';
        
                // Handle binary and text responses differently
                if (contentType.includes('application/json') || contentType.startsWith('text/')) {
                    responseBody = await networkResponse.clone().text(); // Handle text responses
                } else {
                    responseBody = await networkResponse.clone().arrayBuffer(); // Handle binary responses
                }

                const newValue = {
                    body: responseBody,
                    options: {
                        headers: serializeHeaders(networkResponse.headers),
                        status: networkResponse.status,
                        statusText: networkResponse.statusText,
                    },
                };

                try {
                    console.log("Store", album);
                    await storeInIndexedDB(requestKey, newValue, newMetadata);
                } catch (error) {
                    console.error('Failed to store data in IndexedDB:', error);
                }

                return networkResponse;
            }
        } catch (error) {

            console.log('Network error for static resource, using cache:', error);
            if (cachedResponse) {
                return new Response(cachedResponse.body, {
                    headers: deserializeHeaders(cachedResponse.options?.headers || {}),
                    status: cachedResponse.options?.status || 200,
                    statusText: cachedResponse.options?.statusText || 'OK',
                });
            }

        }
    }

    // Nothing in cache, fetch and store
    try {
        const networkResponse = await fetch(request);
     
        await checkPreservedAlbumsChanged(networkResponse);

        if (gPreservedAlbums.includes(album)) {
            const newMetadata = {
                etag: networkResponse.headers.get('Etag'),
                lastModified: networkResponse.headers.get('Last-Modified'),
            };

            let responseBody;
            const contentType = networkResponse.headers.get('Content-Type') || '';
    
            // Handle binary and text responses differently
            if (contentType.includes('application/json') || contentType.startsWith('text/')) {
                responseBody = await networkResponse.clone().text(); // Handle text responses
            } else {
                responseBody = await networkResponse.clone().arrayBuffer(); // Handle binary responses
            }
    
            const newValue = {
                body: responseBody,
                options: {
                    headers: serializeHeaders(networkResponse.headers),
                    status: networkResponse.status,
                    statusText: networkResponse.statusText,
                },
            };
    
            try {
                console.log("Store", album);
                await storeInIndexedDB(requestKey, newValue, newMetadata);
            } catch (error) {
                console.error('Failed to store data in IndexedDB:', error);
            }
        }

        return networkResponse;
    } catch (error) {
        console.error('Network error:', error);
        return new Response('Network error', { status: 500 });
    }
}




self.addEventListener('fetch', event => {

    const url       = new URL(event.request.url);
    const path      = url.pathname; // Extract the pathname (a string)

    console.log("ENTER", path);

    if (path.startsWith('/view')) {
        event.respondWith(handleIndexedDBResource(event.request));
    } else if (STATIC_RESOURCES.some(resource => path.slice(1) === resource)) {
        event.respondWith(handleStaticResource(event.request));
        console.log("STATIC", path);
    } else if (API_ENDPOINTS.some(endpoint => path.includes(endpoint))) {
        event.respondWith(handleStaticResource(event.request));
        //event.respondWith(handleApiRequest(event.request));
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




self.addEventListener('activate', (event) => {
    const cacheWhitelist = [CACHE_NAME];
    const dbWhitelist = [DB_NAME];

    event.waitUntil(
        (async () => {
            // Clear outdated caches
            const cacheNames = await caches.keys();
            await Promise.all(
                cacheNames.map((cacheName) => {
                    if (!cacheWhitelist.includes(cacheName)) {
                        console.log(`Deleting outdated cache: ${cacheName}`);
                        return caches.delete(cacheName);
                    }
                })
            );

            // Clear outdated IndexedDB databases
            const dbNamesRequest = indexedDB.databases();
            const dbList = await dbNamesRequest;

            await Promise.all(
                dbList.map((db) => {
                    if (!dbWhitelist.includes(db.name)) {
                        console.log(`Deleting outdated IndexedDB: ${db.name}`);
                        return deleteIndexedDB(db.name);
                    }
                })
            );

            console.log('Activation cleanup complete.');
        })()
    );
});


// Helper function to delete IndexedDB
function deleteIndexedDB(dbName) {
    return new Promise((resolve, reject) => {
        const request = indexedDB.deleteDatabase(dbName);
        request.onsuccess = () => {
            console.log(`IndexedDB deleted: ${dbName}`);
            resolve();
        };
        request.onerror = (error) => {
            console.error(`Failed to delete IndexedDB: ${dbName}`, error);
            reject(error);
        };
        request.onblocked = () => {
            console.warn(`Delete operation blocked for IndexedDB: ${dbName}`);
        };
    });
}