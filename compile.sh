
(cd ffmpeg_pipe && node build.js)
cp -r ffmpeg_pipe/dist/static ./
go build -o bin/pocketserver