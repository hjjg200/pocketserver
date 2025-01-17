// wavutils.c

// emcc wavutils.c   -s WASM=1   -s MODULARIZE=1   -s "EXPORTED_FUNCTIONS=['_create_wav','_malloc','_free']" -sALLOW_MEMORY_GROWTH   -o wavutils.js


#include <stdint.h>
#include <math.h>   // for fmin/fmax
#include <string.h> // for memcpy
#include <stdlib.h>

// A quick helper to write a 32-bit little-endian integer:
static void set_uint32_le(uint8_t *dst, int offset, uint32_t value) {
    dst[offset+0] = value & 0xFF;
    dst[offset+1] = (value >> 8) & 0xFF;
    dst[offset+2] = (value >> 16) & 0xFF;
    dst[offset+3] = (value >> 24) & 0xFF;
}

// A quick helper to write a 16-bit little-endian integer:
static void set_uint16_le(uint8_t *dst, int offset, uint16_t value) {
    dst[offset+0] = value & 0xFF;
    dst[offset+1] = (value >> 8) & 0xFF;
}

// WAV header is always 44 bytes. We'll fill it out in a buffer, then
// copy interleaved samples after that.
#define WAV_HEADER_SIZE 44

// This function takes the following arguments:
//   allChannelData: pointer to a contiguous float[] buffer containing
//                   channel data back-to-back (channel0 followed by channel1, etc.)
//   lengthPerChannel: number of frames (samples) per channel
//   numChannels: how many channels total
//   sampleRate: sampling rate in Hz
//
// It allocates a new buffer in WASM memory, writes a 44-byte WAV header,
// then interleaves the float samples into 16-bit PCM. It returns a pointer
// to that newly allocated buffer. The size of that buffer is
// (44 + lengthPerChannel * numChannels * 2).
//
// JavaScript will call this function via wasm exports. Then it can read back the bytes
// from wasm memory and form a Blob.
uint8_t* create_wav(const float *allChannelData,
                    int lengthPerChannel,
                    int numChannels,
                    int sampleRate)
{
    // 2 bytes per sample, total frames = lengthPerChannel
    int blockAlign = numChannels * 2;
    int byteRate = sampleRate * blockAlign;
    int dataSize = lengthPerChannel * blockAlign;
    int totalSize = WAV_HEADER_SIZE + dataSize;

    // Allocate in WASM memory:
    // (In Emscripten, you'd normally use malloc. Make sure to free after using.)
    uint8_t *wavData = (uint8_t*)malloc(totalSize);
    if (!wavData) {
        return 0; // or handle error
    }

    // Write the 44-byte WAV header:

    // "RIFF"
    wavData[0] = 'R'; wavData[1] = 'I'; wavData[2] = 'F'; wavData[3] = 'F';
    // file size = 36 + dataSize, little-endian
    set_uint32_le(wavData, 4, 36 + dataSize);
    // "WAVE"
    wavData[8] = 'W'; wavData[9] = 'A'; wavData[10] = 'V'; wavData[11] = 'E';

    // "fmt "
    wavData[12] = 'f'; wavData[13] = 'm'; wavData[14] = 't'; wavData[15] = ' ';
    // Subchunk1Size = 16
    set_uint32_le(wavData, 16, 16);
    // Audio format = 1 (PCM)
    set_uint16_le(wavData, 20, 1);
    // NumChannels
    set_uint16_le(wavData, 22, numChannels);
    // SampleRate
    set_uint32_le(wavData, 24, sampleRate);
    // ByteRate
    set_uint32_le(wavData, 28, byteRate);
    // BlockAlign
    set_uint16_le(wavData, 32, blockAlign);
    // BitsPerSample = 16
    set_uint16_le(wavData, 34, 16);

    // "data"
    wavData[36] = 'd'; wavData[37] = 'a'; wavData[38] = 't'; wavData[39] = 'a';
    // dataSize
    set_uint32_le(wavData, 40, dataSize);

    // Now interleave the float samples into 16-bit PCM after offset 44.
    // We assume "allChannelData" is structured as:
    //   [channel0(0), channel0(1), ..., channel0(lengthPerChannel-1),
    //    channel1(0), channel1(1), ..., channel1(lengthPerChannel-1),
    //    ... channelN ...]
    // If your layout is different, adapt accordingly.
    int outPos = WAV_HEADER_SIZE;
    for (int frame = 0; frame < lengthPerChannel; frame++) {
        for (int ch = 0; ch < numChannels; ch++) {
            // index into allChannelData = ch*lengthPerChannel + frame
            float sample = allChannelData[ch*lengthPerChannel + frame];
            // clamp [-1,1]
            if (sample < -1.f) sample = -1.f;
            if (sample >  1.f) sample =  1.f;
            // convert to 16-bit
            int16_t intSample = (int16_t)(sample < 0 ? sample * 0x8000 : sample * 0x7FFF);
            wavData[outPos++] = (uint8_t)(intSample & 0xFF);
            wavData[outPos++] = (uint8_t)((intSample >> 8) & 0xFF);
        }
    }

    return wavData;
}
