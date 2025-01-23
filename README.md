
# Pocketserver

A simple server built for use on iSH, an emulated linux on iOS.

> [!CAUTION]
> Currently in **unstable development** refer to what is not implemented and advise not to use unless you understand the code.

## iSH specific notes

- when built for iSH, CPU core limited to 1 by `runtime.GOMAXPROCS(1)`
- requires `i686-linux-musl-gcc` for compiling
```sh
sudo apt install gcc-multilib g++-multilib binutils-multiarch
sudo apt install gcc-i686-linux-gnu g++-i686-linux-gnu
make clean
./configure --prefix=/usr/local/musl-1.2.5-i686 --host=i686-linux-gnu CC=i686-linux-gnu-gcc
make -j$(nproc)
sudo make install
sudo ln -s /usr/local/musl-1.2.5-i686/bin/musl-gcc /usr/local/bin/i686-linux-musl-gcc
```
- requires ffmpeg `apk add ffmpeg` on iSH will install it
- when iSH put **completely background** using `cat /dev/location &` it appears that it is turned off in 15 minutes; when locked while iSH is on the screen, tested maximum is 7 hours
- placing your own `mime.types` in `/etc/mime.types` is recommended; refer to [static/mime.types](./static/mime.types)
- ffmpeg is run using `popen` because I got invalid operation error using exec.Cmd.Run
- using HTTP is recommended for better throughput
- calling repeated ReadDir on mounted icloud drive causes freeze

## Features

- Music player -- you can edit playlist by longpress
- Drag and drop to upload
- https server to go for iPhone, local network can access the server using browser
- **(Unstable)** pipeline iSH's ffmpeg request to ffmpeg.wasm on an http client so that it can perform better
```sh
# on iSH
ln -s .../path/to/pocketserver_ish /usr/local/bin/ffmpeg
ln -s .../path/to/pocketserver_ish /usr/local/bin/ffprobe # Replace both ffmpeg and ffprobe 
ffmpeg -i input.mp4 -i input.m4a ...args output.mp4
ffprobe input.mp4
# pocketserver_ish invoked with ffmpeg sends ffmpeg arguments via unix socket to the main worker
# main worker then sends the arguments to any available http client via websocket
# stream input files on iSH's end via websocket to ffmpeg.wasm
# ffmpeg.wasm sends the resulting outputn via websocket
# main worker writes the output file at the specified output path on iSH's end
```
- you can use `yt-dlp` installed on iSH with ffmpeg pipelining
    - loading python and up to printing ffmpeg version takes about 1 minute
    - single operation of [regexp for searching youtube nsig](https://github.com/ytdl-org/youtube-dl/blob/63fb0fc4159397618b12fa115f957b9ba70f3f88/youtube_dl/extractor/youtube.py#L1775) takes about 1.5 minutes
    - the above operation is run twice per video
    - rest of the process takes reasonable time compared to when run on desktop unless it invloves encoding
    - [multithreading is unstable](https://github.com/ffmpegwasm/ffmpeg.wasm/issues/597) 
```sh
# [1] Compatible mp4 for iOS devices that don't support AV1 (before M3/A17)
yt-dlp -o 'YTDLP/%(channel)s/[%(upload_date)s]%(fulltitle).50s(%(id)s)/[%(upload_date)s]%(fulltitle)s(%(id)s)' \
 -v -c --add-metadata --concurrent-fragments 20  --retries "infinite" \
 --merge-output-format webm \
 --embed-metadata --write-info-json --clean-infojson \
 --write-comments --write-subs --sub-lang all \
 --sub-format srt --write-description --write-thumbnail \
 --exec "ffmpeg -i {} -c:v libx265 -tag:v hvc1 -preset fast -crf 28 \
 -c:a aac -b:a 192k -x265-params "aq-mode=3" -pix_fmt yuv420p \
 -movflags faststart {}.h265.mp4" \
 -S "vcodec:vp09" $URL

# Line 3: Merge into webm before encoding to a compatible mp4
#   directly merging into mp4 cannot be done
# Line 7: Encode to a compatible mp4
# Line 9: Use -S to priortize vp09 because ffmpeg.wasm doesn't support av1
#   https://github.com/yt-dlp/yt-dlp?tab=readme-ov-file#sorting-formats
#   later when ffmpeg.wasm supports av1 no need for -S option
#   av01 > vp9.2 > vp9 > h265 > h264 > vp8 > h263 > theora > other
#   Specify acodec if necessary
#   flac/alac > wav/aiff > opus > vorbis > aac > mp4a > mp3 > ac4 > eac3 > ac3 > dts > other
# Works on ffmpeg.wasm on iOS safari

# [2] Safari WebM for iOS devices that don't support AV1 (before M3/A17)
yt-dlp -o 'YTDLP/%(channel)s/[%(upload_date)s]%(fulltitle).50s(%(id)s)/[%(upload_date)s]%(fulltitle)s(%(id)s)' \
 -v -c --add-metadata --concurrent-fragments 20  --retries "infinite" \
 --merge-output-format webm \
 --embed-metadata --write-info-json --clean-infojson \
 --write-comments --write-subs --sub-lang all \
 --sub-format srt --write-description --write-thumbnail \
 -S "vcodec:vp09" $URL

# It's likely that they support vp9 on **safari**
```
- test results of ffmpeg encodings on different browsers (left is single-threaded ffmpeg and right is @ffmpeg/core-mt)
    |Codec|Chrome[^1]|Safari[^2]|Firefox[^3]|
    |-|-|-|-|
    |`aac->aac`     |✅❌|⬜⬜|⬜⬜|
    |`x264->x265`   |⬜❌|⬜✅|⬜✅|
    |`x265->x264`   |⬜❌|⬜✅|⬜✅|
    |`vp9->x265`    |⬜❌|⬜✅|⬜✅|
    |`vp9->x264`    |⬜❌|⬜✅|⬜✅|
    |`x265->vp9`    |⬜❌|⬜❌|⬜❌|
    [^1]: windows amd64 chrome 131 24 threads
    [^2]: iOS safari a14 bionic
    [^3]: windows amd64 firefox 134 24 threads



## TODO

- FFmpeg piping (iSH <-> ffmpeg.wasm)
    - memory leak check
    - video compressor
    - music sound check run once during upload time, instead of javascript wav method
    - metadata extraction during upload time
    - ...
- Reload images src when non-cache fetch finished
- log functions fix argument handling
- playlist loop single song
- sub-playlist under album
- Create album, rename album
- remove metadata of removed files
- paste to upload for iOS safari


### Safari specific memo

- When using blob url wav as audio's src, doing `audio.pause()` causes media session API to behave unexpectedly. You can pause audio with `audio.playbackRate = 0` as a workaround.
- On iphone safari, accessing via `http://[::1]` somehow makes decoding of audio fail. It works perfectly fine on `http://127.0.0.1`
    - Might be related with blob url handling

### Memo

```sh
ffmpeg -i in.opus -c:a libmp3lame -q:a 1 -ar 44100 -map_metadata 0 -map_metadata 0:s:0 -id3v2_version 3 out.mp3
ffmpeg -i in.opus -c:v mjpeg -c:a aac -q:a 1 -map_metadata 0 -map_metadata 0:s:0 -id3v2_version 3 -f ipod out.m4a
ffmpeg -i "$INPUT" -c:v hevc_nvenc -tag:v hvc1 -preset slow -crf 28 -c:a aac -b:a 192k -x265-params "aq-mode=3" "${INPUT%.*}_2.mp4"
yt-dlp --extract-audio --audio-format best --embed-thumbnail --add-metadata --metadata-from-title "%(title)s" -o "%(title)s.%(ext)s" $1
alias goish='CC=i686-linux-musl-gcc CGO_ENABLED=1 GOOS=linux GOARCH=386 go'

ffmpeg -i in.mp3 -map 0:a -map 0:v:0 -c:a aac -c:v mjpeg -disposition:v attached_pic out.m4a
```

