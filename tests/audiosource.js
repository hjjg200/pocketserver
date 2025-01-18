
class AudioSource {
    constructor(targetDb) {
        this.audioContext = new (window.AudioContext || window.webkitAudioContext)();
        this.targetDb = targetDb;
        this.url = null;
        this.audioBuffer = null;
        this.source = null;
        this.startTime = 0;
        this.pauseTime = 0;
        this.isPlaying = false;
        this.eventListeners = {
            playing: [],
            ended: []
        };
    }

    set src(url) {
        this.url = url;
        this.load(); // Automatically load the buffer when the URL is set
    }

    async load() {
        if (!this.url) {
            console.error("URL is not set. Set the src property before loading.");
            return;
        }
        const audioBuffer = await loadAudio(this.audioContext, this.url);
        this.audioBuffer = await normalizeAudio(audioBuffer, this.targetDb);
    }

    addEventListener(event, callback) {
        if (this.eventListeners[event]) {
            this.eventListeners[event].push(callback);
        } else {
            console.error(`Unsupported event type: ${event}`);
        }
    }

    dispatchEvent(event) {
        if (this.eventListeners[event]) {
            this.eventListeners[event].forEach(callback => callback());
        }
    }

    async play() {
        if (!this.audioBuffer) {
            await this.load();
        }

        if (this.isPlaying) return;

        // Create a new AudioBufferSourceNode
        this.source = this.audioContext.createBufferSource();
        this.source.buffer = this.audioBuffer;
        this.source.connect(this.audioContext.destination);

        const offset = this.pauseTime;
        this.source.start(0, offset); // Start from the paused time
        this.startTime = this.audioContext.currentTime - offset;
        this.isPlaying = true;

        this.dispatchEvent("playing"); // Dispatch "playing" event

        this.source.onended = () => {
            this.isPlaying = false;
            if (this.audioContext.currentTime - this.startTime >= this.audioBuffer.duration) {
                this.pauseTime = 0; // Reset only if playback reaches the end
                this.dispatchEvent("ended"); // Dispatch "ended" event
            }
        };
    }

    pause() {
        if (!this.isPlaying) return;
        this.source.stop(0); // Stop playback
        this.pauseTime = this.audioContext.currentTime - this.startTime; // Save the current playback time
        this.isPlaying = false;
    }

    stop() {
        if (this.source) {
            this.source.stop(0);
            this.source = null;
            this.pauseTime = 0;
            this.isPlaying = false;
        }
    }

    seek(time) {
        if (time < 0 || time > this.audioBuffer.duration) {
            console.error("Seek time out of range.");
            return;
        }
        this.pauseTime = time; // Update the paused time
        if (this.isPlaying) {
            this.pause();
            this.play();
        }
    }

    get currentTime() {
        return this.isPlaying
            ? this.audioContext.currentTime - this.startTime
            : this.pauseTime;
    }

    get duration() {
        return this.audioBuffer ? this.audioBuffer.duration : 0;
    }

    set currentTime(time) {
        this.seek(time);
    }

    get playbackRate() {
        return this.source ? this.source.playbackRate.value : 1;
    }

    get paused() {
        return !this.isPlaying;
    }
}
