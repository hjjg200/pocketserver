#!/bin/bash

STATIC_PATH=./static

(cd ffmpeg_pipe && node build.js)
cp -r ffmpeg_pipe/dist/static ./

# Gzip large static

## Array of file paths
ary_to_gzip=(
    #"$STATIC_PATH/ffmpeg/ffmpeg-core.wasm"
    "$STATIC_PATH/ffmpeg/mt-ffmpeg-core.wasm"
)

## Process each file
for file in "${ary_to_gzip[@]}"; do
    sha1_file="$file.sha1" # Expected .sha1 file path

    # Check if the file exists
    if [[ ! -f "$file" ]]; then
        echo "File not found: $file"
        continue
    fi

    # Check if the .sha1 file exists
    if [[ ! -f "$sha1_file" ]]; then
        echo "SHA1 file missing for $file. Creating it and compressing the file."
        # Calculate SHA1 and save to the .sha1 file
        sha1sum "$file" | awk '{print $1}' > "$sha1_file"

        # Compress the file with gzip
        gzip -f -9 "$file"
        continue # Move to the next file after handling it
    fi

    # If .sha1 exists, validate the checksum
    current_sha1=$(sha1sum "$file" | awk '{print $1}')
    stored_sha1=$(cat "$sha1_file")

    if [[ "$current_sha1" == "$stored_sha1" ]]; then
        echo "SHA1 matches for $file. Removing file."
        rm "$file" # Remove the file
    else
        echo "SHA1 mismatch for $file. Updating .sha1 file and compressing."
        # Update the .sha1 file
        sha1sum "$file" | awk '{print $1}' > "$sha1_file"

        # Compress the file with gzip
        gzip -f -9 "$file"
    fi
done





go build -o bin/pocketserver