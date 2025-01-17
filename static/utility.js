
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

async function loadAudio(audioContext, url) {
    const response = await fetch(url);
    const arrayBuffer = await response.arrayBuffer();
    return await audioContext.decodeAudioData(arrayBuffer);
}

function analyzeLoudness(audioBuffer) {
    let rms = 0;
    const numChannels = audioBuffer.numberOfChannels;

    // Average RMS across all channels
    for (let channel = 0; channel < numChannels; channel++) {
        const channelData = audioBuffer.getChannelData(channel);
        let channelRms = 0;
        for (let i = 0; i < channelData.length; i++) {
            channelRms += channelData[i] ** 2;
        }
        channelRms = Math.sqrt(channelRms / channelData.length);
        rms += channelRms;
    }

    rms /= numChannels; // Normalize RMS across all channels

    // Convert RMS to decibels
    const db = 20 * Math.log10(rms);
    return db;
}

async function normalizeAudio(/*audioContext,*/ audioBuffer, targetDb) {
    const currentDb = analyzeLoudness(audioBuffer);
    const gainDb = targetDb - currentDb;
    const gain = Math.pow(10, gainDb / 20);

    // Create an OfflineAudioContext for normalization
    const offlineContext = new OfflineAudioContext(
        audioBuffer.numberOfChannels,
        audioBuffer.duration * audioBuffer.sampleRate,
        audioBuffer.sampleRate
    );

    const source = offlineContext.createBufferSource();
    source.buffer = audioBuffer;

    const gainNode = offlineContext.createGain();
    gainNode.gain.value = gain;

    source.connect(gainNode);
    gainNode.connect(offlineContext.destination);

    source.start(0);
    const normalizedBuffer = await offlineContext.startRendering();
    console.log(offlineContext, audioBuffer);
    return normalizedBuffer;
}

async function normalizeAndBlobAudio(url, targetDb) {
    const audioContext = new (window.AudioContext || window.webkitAudioContext)();
    const audioBuffer = await loadAudio(audioContext, url);

    // Normalize the audio
    const normalizedBuffer = await normalizeAudio(/*audioContext,*/ audioBuffer, targetDb);

    return audioBufferToBlobURL(normalizedBuffer);
}

function audioBufferToBlobURL(audioBuffer) {
    const { numberOfChannels, length, sampleRate } = audioBuffer;

    // Gather data for each channel
    const allChannelData = [];
    for (let ch = 0; ch < numberOfChannels; ch++) {
        allChannelData[ch] = audioBuffer.getChannelData(ch);
    }

    // Create a WAV header
    // "length" here is the number of audio frames (samples per channel)
    const wavHeader = createWAVHeader(length, sampleRate, numberOfChannels);

    // Each frame has "numberOfChannels" samples, each sample is 2 bytes (16-bit)
    const totalBytes = length * numberOfChannels * 2;
    const wavData = new Uint8Array(wavHeader.length + totalBytes);

    // Copy header
    wavData.set(wavHeader);

    // Interleave channels into 16-bit PCM
    let offset = wavHeader.length;
    for (let frame = 0; frame < length; frame++) {
        for (let ch = 0; ch < numberOfChannels; ch++) {
            // Clamp sample to [-1, 1]
            const sample = Math.max(-1, Math.min(1, allChannelData[ch][frame]));
            // Convert to 16-bit signed
            const intSample = sample < 0 ? sample * 0x8000 : sample * 0x7FFF;
            // Little-endian
            wavData[offset++] = intSample & 0xff;
            wavData[offset++] = (intSample >> 8) & 0xff;
        }
    }

    // Create a Blob and return a URL
    const blob = new Blob([wavData], { type: "audio/wav" });
    return URL.createObjectURL(blob);
}


// Create a WAV file header
function createWAVHeader(samples, sampleRate, numChannels) {
    const blockAlign = numChannels * 2; // 2 bytes per sample per channel
    const byteRate = sampleRate * blockAlign;

    const buffer = new ArrayBuffer(44);
    const view = new DataView(buffer);

    // RIFF header
    writeString(view, 0, "RIFF");
    view.setUint32(4, 36 + samples * blockAlign, true); // File size
    writeString(view, 8, "WAVE");

    // Format chunk
    writeString(view, 12, "fmt ");
    view.setUint32(16, 16, true); // Subchunk1Size
    view.setUint16(20, 1, true); // Audio format (1 = PCM)
    view.setUint16(22, numChannels, true);
    view.setUint32(24, sampleRate, true);
    view.setUint32(28, byteRate, true);
    view.setUint16(32, blockAlign, true);
    view.setUint16(34, 16, true); // Bits per sample

    // Data chunk
    writeString(view, 36, "data");
    view.setUint32(40, samples * blockAlign, true); // Subchunk2Size

    return new Uint8Array(buffer);
}

function writeString(view, offset, string) {
    for (let i = 0; i < string.length; i++) {
        view.setUint8(offset + i, string.charCodeAt(i));
    }
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



function formatDuration(durationString) {
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
