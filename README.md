
# Pocketserver

A simple server built for use on iSH, an emulated linux on iOS.

> [!CAUTION]
> Currently in **unstable development** refer to what is not implemented and advise not to use unless you understand the code.

## iSH specific notes

- when built for iSH, CPU core limited to 1 by `runtime.GOMAXPROCS(1)`
- requires `i686-linux-musl-gcc` for compiling
- requires ffmpeg `apk add ffmpeg` on iSH will install it
- when iSH put **completely background** using `cat /dev/location &` it appears that it is turned off in 15 minutes; when locked while iSH is on the screen, tested maximum is 7 hours
- placing your own `mime.types` in `/etc/mime.types` is recommended; refer to [static/mime.types](./static/mime.types)
- ffmpeg is run using `popen` because I got invalid operation error
- using HTTP is recommended for better throughput

## Features

- Music player
- Drag and drop to upload
- Server to go for iPhone, local network can access the server using browser


## TODO

- list time, list details time
- service worker for offline availability; Cache config per album
- replay icon for retry; status timeout; graceful timeout; needs custom src handling for video, img, audio; default thumbnail and placeholder
- Create album
- paste to upload for iOS safari
- fix when playing stops after uploading
- playlist shuffle and loop single song
- ipv6

### Memo

```sh
ffmpeg -i in.opus -c:a libmp3lame -q:a 1 -ar 44100 -map_metadata 0 -map_metadata 0:s:0 -id3v2_version 3 out.mp3
ffmpeg -i in.opus -c:v mjpeg -c:a aac -b:a 128k -map_metadata 0 -map_metadata 0:s:0 -id3v2_version 3 -f ipod out.m4a
```