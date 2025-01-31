
function formatBytes(bytes) {
    if (bytes === 0) return "0 bytes";

    const units = ["B", "KB", "MB", "GB", "TB"];
    const k = 1024;
    const i = Math.min(units.length-1, Math.floor(Math.log(bytes) / Math.log(k)));
    const value = bytes / Math.pow(k, i);

    // Ensure at most 3 significant digits
    const formattedValue = value.toPrecision(3);

    return `${formattedValue} ${units[i]}`;
}

function formatSeconds(nanoseconds) {
    if (nanoseconds === 0) return "0 ns";

    const units = ["ns", "μs", "ms", "s"];
    const thresholds = [1, 1e3, 1e6, 1e9]; // Corresponding thresholds for ns, μs, ms, and s

    // Find the appropriate unit index
    let i = thresholds.length - 1;
    while (i > 0 && nanoseconds < thresholds[i]) {
        i--;
    }

    // Convert to the selected unit
    const value = nanoseconds / thresholds[i];

    // Ensure at most 3 significant digits
    const formattedValue = value.toPrecision(3);

    return `${formattedValue} ${units[i]}`;
}

function sumAndRemoveKeys(map, keys) {
    let sum = 0;

    if (keys == null) { // If keys is null or undefined
        for (const key in map) {
            if (map.hasOwnProperty(key)) {
                sum += map[key]; // Add all values to the sum
                delete map[key]; // Remove all keys
            }
        }
    } else {
        keys.forEach(key => {
            if (map.hasOwnProperty(key)) {
                sum += map[key]; // Add the value to the sum
                delete map[key]; // Remove the key from the map
            }
        });
    }

    return sum;
}

function seededRandom(seed) {
    // A simple seed-based RNG (e.g., Mulberry32)
    let t = seed;
    return function () {
        t += 0x6D2B79F5;
        let x = Math.imul(t ^ (t >>> 15), t | 1);
        x ^= x + Math.imul(x ^ (x >>> 7), x | 61);
        return ((x ^ (x >>> 14)) >>> 0) / 4294967296;
    };
}

function shuffleArray(array, seed) {
    if (seed === null) return array.slice();

    const rng = seededRandom(seed); // Create a seeded random number generator
    const shuffled = array.slice(); // Clone the array to avoid mutating the original

    for (let i = shuffled.length - 1; i > 0; i--) {
        // Get a random index based on the seed
        const j = Math.floor(rng() * (i + 1));
        // Swap elements i and j
        [shuffled[i], shuffled[j]] = [shuffled[j], shuffled[i]];
    }

    return shuffled;
}

async function clearRetryInterval(handle) {
    if (handle) {
        await handle.abort();
    }
}

function createRetryInterval(callback, interval) {
    const controller = new AbortController(); // Create an AbortController
    const { signal } = controller; // Extract the signal for cancellation

    let running = false; // Track if the callback is running
    let handle; // Timeout handle

    const promise = new Promise((resolve, reject) => {
        const attempt = async () => {
            if (running || signal.aborted) return; // Skip if already running or aborted

            running = true;
            try {
                const result = await callback(signal); // Pass the signal to the callback
                resolve(result); // Resolve the promise on success
                clearTimeout(handle); // Stop further timeouts
            } catch (error) {
                if (signal.aborted) {
                    reject(new DOMException("Aborted", "AbortError")); // Reject if aborted
                } else {
                    // Schedule the next retry after the interval
                    console.error(error);
                    handle = setTimeout(attempt, interval);
                }
            } finally {
                running = false;
            }
        };

        // Start the first attempt immediately
        handle = setTimeout(attempt, 0);
    });

    const abort = async () => {
        controller.abort(); // Abort the signal
        clearTimeout(handle); // Stop the timeout
        while (running) {
            await new Promise((resolve) => setTimeout(resolve, 10)); // Wait for the current callback to finish
        }
    };

    return { promise, abort };
}










function getQueryParam(key) {
    const params = new URLSearchParams(window.location.search);
    return params.get(key);
}

function removeQueryParam(key, push=false) {
    const params = new URLSearchParams(window.location.search);
    params.delete(key);
    const newUrl = `${window.location.pathname}?${params.toString()}`;
    window.history[push ? 'pushState':'replaceState']({}, '', newUrl.endsWith('?') ? window.location.pathname : newUrl);
}

function addQueryParam(key, value, push=false) {
    const params = new URLSearchParams(window.location.search);
    params.set(key, value); // Add or update the key with the new value
    const newUrl = `${window.location.pathname}?${params.toString()}`;
    window.history[push ? 'pushState':'replaceState']({}, '', newUrl);
}

function buildURL(url, queryMap = {}, hash = null) {

    if (url instanceof URL) {

        url = new URL(url);
        
    } else {

        let pathSegments = [];

        if (typeof url === "string") {
            // Split the string by slashes, preserving individual components
            pathSegments = url.split('/');
        } else if (Array.isArray(url)) {
            // Split each element by slashes and flatten the result
            pathSegments = url.flatMap(element => element.split('/'));
        } else {
            throw new Error("First argument must be a string or an array of strings");
        }

        const path = pathSegments.map(encodeURIComponent).join('/');
        url = new URL(path, window.location.origin);

    }
    
    // Add query parameters from the queryMap
    Object.entries(queryMap).forEach(([key, value]) => {
        if (value === null || value === undefined) {
            url.searchParams.append(key, ""); // Append key without a value
        } else {
            url.searchParams.set(key, value);
        }
    });

    // Add hash if provided
    if (hash) {
        url.hash = hash;
    }

    return url;

}

const createElement = (tag, ...classes) => {
    const element = document.createElement(tag);
    element.classList.add(...classes);
    return element;
};

function ensureLiVisibility(ul, li) {
    if (!ul || !li) return;

    // Get bounding rectangles
    const ulRect = ul.getBoundingClientRect();
    const liRect = li.getBoundingClientRect();

    // Calculate offsets relative to the scrolling container
    const ulScrollTop = ul.scrollTop;
    const ulHeight = ul.clientHeight;
    const liOffsetTop = li.offsetTop - ul.offsetTop; // Adjust offset for relative positioning
    const liHeight = li.offsetHeight;

    // Check if li is fully visible
    const isFullyVisible =
        liRect.top >= ulRect.top && liRect.bottom <= ulRect.bottom;

    if (isFullyVisible) {
        return; // Already visible, no scrolling needed
    }

    // Calculate the target scroll position to center the li
    const targetScrollTop = liOffsetTop - (ulHeight / 2) + (liHeight / 2);

    // Ensure target scrollTop stays within bounds
    const boundedScrollTop = Math.max(0, Math.min(targetScrollTop, ul.scrollHeight - ulHeight));

    // Smoothly scroll to the calculated position
    ul.scrollTo({
        top: boundedScrollTop,
        behavior: "smooth"
    });
}

function createSlider(slider, initialValue, updateOnDrag, callback, binder) {
    const fill = slider.querySelector(".slider-fill");
    let dragging = false;

    function update(value) {
        fill.style.width = Math.floor(value * 100) + "%"; // Update fill width
    }

    function evaluate(event, doCallback) {
        const rect = slider.getBoundingClientRect();
        const pointerX = event.clientX - rect.left; // Pointer X relative to slider
        const clampedX = Math.max(0, Math.min(pointerX, rect.width)); // Clamp within slider bounds
        const value = clampedX / rect.width; // Normalize value (0 to 1)

        update(value);
        if (doCallback) callback(value); // Trigger callback if needed
    }

    slider.addEventListener('pointerdown', (event) => {
        dragging = true;
        slider.setPointerCapture(event.pointerId); // Capture pointer
        evaluate(event, updateOnDrag);
    });

    document.addEventListener(
        'pointermove',
        (event) => {
            if (dragging) {
                event.preventDefault(); // Prevent scrolling during drag
                evaluate(event, updateOnDrag);
            }
        },
        { passive: false }
    );

    document.addEventListener('pointerup', (event) => {
        if (dragging) {
            evaluate(event, !updateOnDrag);
            dragging = false;
            slider.releasePointerCapture(event.pointerId); // Release pointer
        }
    });

    // Handle external updates
    binder((value) => {
        if (dragging) return; // Ignore updates while dragging
        update(value);
    });

    // Initialize with the current value
    callback(initialValue);
}

function getRem() {
    return parseFloat(getComputedStyle(document.documentElement).fontSize);
}

function formatDuration(seconds) {
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const secs = Math.floor(seconds % 60);

  if (hours > 0) {
      return `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}:${String(secs).padStart(2, '0')}`;
  } else {
      return `${String(minutes).padStart(2, '0')}:${String(secs).padStart(2, '0')}`;
  }
}

function formatDurationDEP(durationString) {
    try {
        const [hours, minutes, seconds] = durationString.split(':');
        const [wholeSeconds] = seconds.split('.'); // Drop decimal part
        if (hours === '00') {
            return `${minutes}:${wholeSeconds}`;
        }
        return `${hours}:${minutes}:${wholeSeconds}`;
    } catch {
        return "N/A";
    }
}

function durationToSecondsDEP(duration) {
    if (duration.length !== 11) return 0;
    // Split the time string into components
    const [hours, minutes, seconds] = duration.split(":");
    const [wholeSeconds, milliseconds] = seconds.split(".");

    // Calculate total seconds
    return (
        parseInt(hours, 10) * 3600 +
        parseInt(minutes, 10) * 60 +
        parseInt(wholeSeconds, 10) +
        parseFloat(`0.${milliseconds}`)
    );
}

async function fetchJSON(url) {
    try {
        const response = await fetch(url);
        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }
        const data = await response.json();
        return data;
    } catch (error) {
        console.error('Error fetching JSON:', error);
    }
}

function base64ToUtf8(base64) {
    // Decode Base64 to a binary string, then convert it back to a UTF-8 string
    return new TextDecoder().decode(
        new Uint8Array(
            atob(base64).split('').map(char => char.charCodeAt(0))
        )
    );
}

(() => {

    let wakeLock = null;

    window.requestWakeLock = async function() {
        try {
            // Request a screen wake lock
            wakeLock = await navigator.wakeLock.request('screen');
            console.log('Wake lock is active.');

            // Handle the wake lock being released (e.g., when the tab becomes inactive)
            wakeLock.addEventListener('release', () => {
                console.log('Wake lock released.');
            });
        } catch (err) {
            console.error(`Failed to request wake lock: ${err.name}, ${err.message}`);
        }
    }

    window.releaseWakeLock = function() {
        if (wakeLock) {
            wakeLock.release()
            .then(() => {
                console.log('Wake lock released manually.');
                wakeLock = null;
            });
        }
    }

})();

(() => { // OBSERVER

    const callbackMap = new Map();

    // Create the Intersection Observer
    const observer = new IntersectionObserver((entries, observer) => {
        entries.forEach(entry => {
            if (entry.isIntersecting) {
                const callback = callbackMap.get(entry.target); // Retrieve the associated callback
                if (callback) {
                    callback(entry.target); // Execute the callback with the target element
                    callbackMap.delete(entry.target);
                }
                observer.unobserve(entry.target); // Stop observing
            }
        },
        {
            root: null, // Observe relative to the viewport
            rootMargin: '0', // Trigger when 100px away from the viewport
            threshold: 0 // Trigger when 10% of the element is visible
        });
    });

    // Observe an element with its specific callback
    window.observeWithCallback = function(element, callback) {
        callbackMap.set(element, callback); // Associate the callback with the element
        observer.observe(element); // Observe the element
    }

})();

function debounce(delay, func) {
    let timeout;

    return function (...args) {
        clearTimeout(timeout); // Clear the previous timer
        timeout = setTimeout(() => {
            func.apply(this, args); // Call the function after the delay
        }, delay);
    };
}

function throttle(limit, func) {
    let lastCall = 0;

    return function (...args) {
        const now = Date.now();
        if (now - lastCall >= limit) {
            lastCall = now;
            func.apply(this, args);
        }
    };
}

function toUint8Array(data) {
  if (data instanceof ArrayBuffer)
    data = new Uint8Array(data);
  else if (data instanceof Uint8Array)
    return data;

  throw new Error("Only ArrayBuffer and Uint8Array are supported");
}

function setPlainCookie(name, value, days) {
    const date = new Date();
    date.setTime(date.getTime() + days * 24 * 60 * 60 * 1000); // Convert days to milliseconds
    const expires = `expires=${date.toUTCString()}`;
    document.cookie = `${name}=${value}; ${expires}; path=/; SameSite=Strict`;
}

function getCookie(name) {
    const cookies = document.cookie.split('; ');
    for (const cookie of cookies) {
        const [key, value] = cookie.split('=');
        if (key === name) {
            return decodeURIComponent(value); // Decode the value if it's URL-encoded
        }
    }
    return null; // Return null if the cookie is not found
}


function splitMimeType(mimeType) {
  return mimeType.split("/", 2);
}