
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
    return normalizedBuffer;
}

// This makes opus playable on iOS safari
// *for ogg, doesn't work on iphone while it somehow works on ipad
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

