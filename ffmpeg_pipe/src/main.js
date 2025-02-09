import { FFmpeg } from '@ffmpeg/ffmpeg';
import { fetchFile } from '@ffmpeg/util';

/*
  Final multi-job code with the following features:
    1) Filenames in wasm FS = "jobX_inputY.ext" or "jobX_outZ.ext".
       That avoids any UTF-8 or path issues.
    2) Ephemeral 'log' listener that sends logs line by line to the server,
       e.g. {type:"logLine", logLine:"..."} message
    3) If the 0th argument ends with "ffprobe" => use ffprobe,
       else use ffmpeg.exec.
    4) Multiple output files, read each and send the data to the server.
    5) Multi-job: cycle with "ready" <-> "nomore"/args.
*/



window.ffmpegRunCommands = async function (blob, cmds) {

  const ffmpeg = await newFFmpeg();

  // Clone array and write
  const inputFileName = `input.dat`;
  await ffmpeg.writeFile(inputFileName, await fetchFile(blob));

  const ret = {};
  for (let cmd of cmds) {

    const exec = cmd.args[0] === "ffmpeg" ? ffmpeg.exec : ffmpeg.ffprobe;

    let args = cmd.args.slice();
    args[cmd.input] = inputFileName;
    args[cmd.output] = "output" + cmd.outputExt;

    // Ignore 
    try {
      await exec(args.slice(1));
      ret[cmd.outputExt] = new Blob([await ffmpeg.readFile(args[cmd.output])], { type: cmd.outputMimeType });
    } catch (err) {
      if (cmd.required === true) {
        throw err;
      }
    }

  }
  
  return ret;

}






/**
 * newFFmpeg:
 *   Creates and loads the ffmpeg.wasm instance. Adjust the URLs to your environment.
 */
async function newFFmpeg() {

  const ffmpeg = new FFmpeg();
  await ffmpeg.load(
    /*mt ? */{
      coreURL: "/static/ffmpeg/mt-ffmpeg-core.js",
      wasmURL: "/static/ffmpeg/mt-ffmpeg-core.wasm",
      workerURL: "/static/ffmpeg/mt-ffmpeg-core.worker.js",
      classWorkerURL: "/static/ffmpeg/worker.js",
    }
    /*: {
      coreURL: "/static/ffmpeg/ffmpeg-core.js",
      wasmURL: "/static/ffmpeg/ffmpeg-core.wasm",
      classWorkerURL: "/static/ffmpeg/worker.js",
    }*/
  );
  return ffmpeg;
}

/**
 * getTextMetadata:
 *   Retrieves the text metadata of a file using ffprobe JSON output.
 */
async function getTextMetadata(ffmpeg, inputFileName) {
  let stdout = "";

  const onLog = (evt) => {
    if (evt.type === "stdout") {
      stdout += evt.message + "\n";
    }
  };

  ffmpeg.on("log", onLog);

  // Run ffprobe in JSON mode
  await ffmpeg.ffprobe([
    "-i", inputFileName,
    "-show_format",
    "-show_entries", "format_tags=album,artist,title,comment:format=duration",
    "-print_format", "json",
    "-v", "quiet",
  ]);

  ffmpeg.off("log", onLog);

  let probeResult;
  try {
    probeResult = JSON.parse(stdout.trim());
  } catch (err) {
    throw new Error("ffprobe JSON parsing failed: " + err.message);
  }

  const tags = parseFloat(probeResult.format?.tags);
  // tags.artist album comment title

  const duration = parseFloat(probeResult.format?.duration);
  if (isNaN(duration)) {
    // Not an audio or video, maybe image(webp)
  }

  return probeResult;
}



/**
 * analyzeLoudnessPass1:
 *   Pass 1 of loudnorm. Prints JSON stats needed for an accurate correction pass.
 */
async function analyzeLoudnessPass1(ffmpeg, wavFileName, targetLUFS) {
  let jsonData = "";
  let collecting = false;

  const onLog = (evt) => {
    if (evt.type !== "stderr") return;

    // Once we see the `[Parsed_loudnorm_` line, start collecting
    if (evt.message.includes("[Parsed_loudnorm_")) {
      collecting = true;
      return;
    }
    if (collecting) {
      jsonData += evt.message;
      // If it ends with '}', assume we hit the end of the JSON object
      if (evt.message.trim().endsWith("}")) {
        collecting = false;
      }
    }
  };

  ffmpeg.on("log", onLog);

  // Pass 1: measure only
  await ffmpeg.exec([
    "-i", wavFileName,
    "-af", `loudnorm=I=${targetLUFS}:TP=-2.0:LRA=11:print_format=json`,
    "-f", "null",
    "-threads", "1",
    "-v", "info",
    "-",
  ]);

  ffmpeg.off("log", onLog);

  const trimmed = jsonData.trim();
  if (!trimmed) {
    throw new Error("No loudnorm JSON data from pass 1.");
  }

  return JSON.parse(trimmed);
}

/**
 * correctLoudnessPass2:
 *   Pass 2 of loudnorm. Applies the measured stats from Pass 1 for a precise correction.
 */
async function correctLoudnessPass2(ffmpeg, wavFileName, outputFileName, analysis, targetLUFS) {
  if (
    typeof analysis.input_i === "undefined" ||
    typeof analysis.input_tp === "undefined" ||
    typeof analysis.input_lra === "undefined" ||
    typeof analysis.input_thresh === "undefined"
  ) {
    throw new Error("Missing necessary loudnorm parameters from pass 1 analysis.");
  }

  // Build the two-pass filter
  const pass2Filter = [
    `loudnorm=I=${targetLUFS}:TP=-2.0:LRA=11`,
    `measured_I=${analysis.input_i}`,
    `measured_TP=${analysis.input_tp}`,
    `measured_LRA=${analysis.input_lra}`,
    `measured_thresh=${analysis.input_thresh}`,
    // If your pass 1 yields `offset` or `gain`, you can add them, e.g.:
    // `offset=${analysis.offset}`,
    // `gain=${analysis.gain}`,
    "linear=true",
    "print_format=json",
  ].join(":");

  console.debug(pass2Filter);

  // Encode to AAC, apply loudnorm filter, and keep the same duration
  await ffmpeg.exec([
    "-i", wavFileName,
    "-af", pass2Filter,
    "-vn",
    "-c:a", "aac",
    "-q:a", "1",
    "-threads", "1",
    outputFileName,
  ]);
}


async function copyMetadataAndCover(ffmpeg, inputFileName, correctedFileName, finalFileName) {

  // Without separately handling cover art, there are very few audio files that hang when embedding album arts
  const coverName = "cover.webp";
  await ffmpeg.exec([
    "-i", inputFileName,
    "-c:v", "libwebp",
    "-threads", "1",
    "-q:v", "80",
    "-pix_fmt", "yuv420p",
    "-an",
    coverName
  ]);

  // Check if the audio had album art
  const ls = await ffmpeg.listDir("/");
  const hasCover = ls.some(el => el.name === coverName);

  await ffmpeg.exec([
    // 1) The audio file with corrected loudness
    "-i", correctedFileName,

    // 2) The original file that has metadata and cover
    "-i", inputFileName,

    // Handle album art only when the input had one
    ...(hasCover ? [
      
    "-i", coverName,

    // Cover art (video stream #0) from the separate file, if present. 
    "-map", "2:v:0",

    // Mark it as attached cover
    "-disposition:v:0", "attached_pic",

    // Re-encode the cover to MJPEG
    "-c:v", "mjpeg"
  
    ] : [

    "-vn"

    ]),

    // Audio from the corrected file
    "-map", "0:a",

    // Copy (passthrough) the audio from the corrected file
    "-c:a", "copy",

    // Copy all metadata from input #1 (original)
    "-map_metadata", "1",

    // Copy metadata from the first subtitle stream
    "-map_metadata", "1:s:0",

    // Ensure ID3v2 version is set correctly
    "-id3v2_version", "3",

    // Prevent chrome freeze when embedding album art
    "-threads", "1",

    //
    "-f", "mp4",

    //
    "-movflags", "+faststart",

    finalFileName,
  ]);
}
/*
async function copyMetadataAndCover2(ffmpeg, inputFileName, correctedFileName, finalFileName) {
  await ffmpeg.exec([
    // 1) The audio file with corrected loudness
    "-i", correctedFileName,

    // 2) The original file that has metadata and cover
    "-i", inputFileName,

    // Audio from the corrected file
    "-map", "0:a",

    // Cover art (video stream #0) from the original, if present. The `?` makes it optional.
    "-map", "1:v:0?",
    //"-map", "[vcover]",

    // Copy (passthrough) the audio from the corrected file
    "-c:a", "copy",

    // Re-encode the cover to MJPEG
    "-c:v", "mjpeg",  

    //
    "-loglevel", "debug",

    // Force a low frame rate on the cover art stream
    //"-filter_complex", "[1:v:0]setpts=PTS-STARTPTS,fps=1,format=yuv420p[vcover]",
    "-filter:v", "fps=1",
    "-frames:v", "1",
    "-reset_timestamps", "1",
    "-vsync", "2",

    // Mark it as attached cover
    "-disposition:v:0", "attached_pic",

    // Copy all metadata from input #1 (original)
    "-map_metadata", "1",

    // Copy metadata from the first subtitle stream
    "-map_metadata", "1:s:0",

    // Ensure ID3v2 version is set correctly
    "-id3v2_version", "3",

    // Prevent chrome freeze when embedding album art
    "-threads", "1",

    //
    "-f", "mp4",

    //
    "-movflags", "+faststart",

    finalFileName,
  ]);
}*/

/**
 * ffmpegSoundCheck:
 *   Overall pipeline:
 *     1. Download the file
 *     2. Probe for duration
 *     3. Convert to WAV
 *     4. Analyze loudness (pass 1)
 *     5. Apply loudness correction & encode to tempCorrected.m4a (pass 2)
 *     6. Merge cover art + metadata from input
 *     7. Return final .m4a blob
 */
window.ffmpegSoundCheck = new ProgressTask(async function (inputFile, targetLUFS = -14) {

  this.add(7);
  const ffmpeg = await newFFmpeg();

  if (gDEBUG) {
    ffmpeg.on("log", (event) => {
      console[event.type === "stderr" ? "warn" : "log"](event.message);
    });
  }

  try {

    // 0) Download the file into memory
    const [ inputStem, inputExt ] = parseFilename(inputFile.name);
    const inputFileName = `input${inputExt}`;
    const tempCorrectedFile = "tempCorrected.m4a";
    const finalOutputFile = "finalOutput.m4a";

    // Write the input file to the in-memory FS
    this.done("Writing input file to FS...");
    await ffmpeg.writeFile(inputFileName, await fetchFile(inputFile));

    // 3) Loudness Analysis (Pass 1)
    this.done("Analyzing loudness (pass 1)...");
    const analysis = await analyzeLoudnessPass1(ffmpeg, inputFileName, targetLUFS);
    this.done("Pass 1 analysis:", analysis);

    // 4) Loudness Correction (Pass 2) -> tempCorrected.m4a (audio only, minimal metadata)
    this.done("Correcting loudness (pass 2)...");
    await correctLoudnessPass2(ffmpeg, inputFileName, tempCorrectedFile, analysis, targetLUFS);

    // 5) Merge original metadata + album art (re-encoded to MJPEG) into finalOutput.m4a
    this.done("Merging metadata & cover art...");
    await copyMetadataAndCover(ffmpeg, inputFileName, tempCorrectedFile, finalOutputFile);

    // 6) Read finalOutput.m4a back from FS, make a blob
    this.done("Reading final file...");
    const finalData = await ffmpeg.readFile(finalOutputFile);

    this.done("Sound check complete");

    return new File([finalData.buffer], `${inputStem}.m4a`, { type: "audio/mp4" });

  } catch (err) {
    throw err;
  } finally {
    ffmpeg.terminate();
  }

});


window.ffmpegGetMetadata = async (buf, contentType) => {

  const metadata = {};

  // Check eligibility
  const [ cat, sub ] = contentType.split("/");
  if (false === (cat === "audio" || cat === "video" || sub === "webp"))
    return metadata;

  //
  const ffmpeg = await newFFmpeg();

  try {
    
    // 0) Download the file into memory
    const inputFile = await fetchFile(new Blob([buf], {type: contentType}));
    const inputFileName = `input${guessExtension(src)}`;

    // Write the input file to the in-memory FS
    await ffmpeg.writeFile(inputFileName, inputFile);

    // Json
    metadata[".json"] = await getTextMetadata(ffmpeg, inputFileName);
    if (isNaN(metadata[".json"].duration)) {
      // Indicate this webp is image
      metadata[".json"].duration = "N/A";

      return metadata;
    }

    // Thumbnail
    const baseArgs = [
      "-i", inputFileName,
      "-c:v", "libwebp",
      "-threads", "1",
      "-q:v", "80",
      "-pix_fmt", "yuv420p",
      "-an"
    ];

    if (cat === "video") {

      await ffmpeg.exec([
        ...baseArgs,
        "-ss", "00:00:01",
        "-vframes", "1",
        "thumb.webp"
      ]);
    
      const thumb = await ffmpeg.readFile("thumb.webp");
      metadata[".webp"] = new Blob([thumb.buffer], { type: "image/webp" });
    
    } else {
      
      await ffmpeg.exec([
        ...baseArgs,
        "thumb.webp"
      ]);
      await ffmpeg.exec([
        ...baseArgs,
        "-vf", "'scale=iw*sqrt(16384/(iw*ih)):-1'",
        "small.webp"
      ]);
    
      const thumb = await ffmpeg.readFile("thumb.webp");
      const small = await ffmpeg.readFile("small.webp");
      metadata[".webp"] = new Blob([thumb.buffer], { type: "image/webp" });
      metadata["_small.webp"] = new Blob([small.buffer], { type: "image/webp" });
      
    }

  } finally {
    ffmpeg.terminate();
  }

  return metadata;

}




// WEBSOCKET FFMPEG --
// Global job counter to name input & output files uniquely
let jobCounter = 0;

async function pongBackMessageOfType(socket, typ) {
  const obj = JSON.parse(await waitForTextMessage(socket));
  if (obj.type !== typ) {
    throw new Error(`Wrongly typed message, expected ${typ}, received ${obj.type}`);
  }
  socket.send(JSON.stringify({ type: typ }));
  ffmpegLog("info", `Ping ${typ} from server, pong-backed ${typ}`);

  return obj[typ] || null;
}


/**
 * The main multi-job loop:
 *   1) send "ready"
 *   2) wait for text message 
 *      - "nomore" => break
 *      - otherwise => parse JSON => flow(ffargs)
 *   3) repeat
 */
async function cycleJobs(socket, signal) {

  try {
    while (true) {

      // Ready
      await pongBackMessageOfType(socket, "ready");

      // taskReady
      await pongBackMessageOfType(socket, "taskReady");

      // parse the ffargs object
      const ffargs = await pongBackMessageOfType(socket, "ffargs");
      let ffmpeg;

      const terminator = () => {
        if (ffmpeg) {
          ffmpeg.terminate();
          ffmpeg = null;
        }
      };
      try {

        ffmpegLogShow();
        ffmpeg = await newFFmpeg();
        signal.addEventListener("abort", terminator);

        await flow(ffmpeg, ffargs, socket);

      } catch (err) {
        socket.close();
        console.error(err)
        throw new Error("flow error:", { cause: err });
      } finally {
        signal.removeEventListener("abort", terminator);
        terminator();
      }

      ffmpegLog("info", "Job done. Going for next job...");

    }
  } catch (err) {
    ffmpegLog("error", "cycleJobs error:", err);
  }
}

/**
 * flow(ffargs, socket):
 *   1) receive input files => "jobX_inputY.ext" in FS
 *   2) if 0th arg ends with "ffprobe" => ffprobe(...) else ffmpeg.exec(...)
 *   3) ephemeral logs => log lines streaming
 *   4) read each output => send to server
 */
async function flow(ffmpeg, ffargs, socket) {

  jobCounter++;
  ffmpegLog("info", `Job ${jobCounter}`);
  console.log(`Job ${jobCounter}`)

  // ephemeral log listener
  const onLog = (entry) => {
    const msg = JSON.stringify({
      type: "logLine",
      logType: entry.type,
      logLine: entry.message });
    // Also stream each line to server
    socket.send(msg);
    //
    if (entry.type === "stderr")
      ffmpegLog("internal", entry.message);
  };

  // Make a local copy of arguments that we can patch in-place
  const safeArgs = ffargs.args.slice();

  // 2A) determine if ffprobe or ffmpeg
  let isFfprobe = false;
  if (ffargs.args[0].endsWith("ffprobe")) {
    isFfprobe = true;

    ffmpegLog("info", `works as ffprobe`);
  }

  // 1) Receive & write input files, using ASCII-safe names
  const inputMap = {};
  for (let i = 0; i < ffargs.inputs.length; i++) {
    const inputIndex = ffargs.inputs[i];

    ffmpegLog("info", `wait for input ${inputIndex}`);
    // Wait for a text message describing the file's size
    const metaStr = await waitForTextMessage(socket);
    const [recvIndex, fileSize] = JSON.parse(metaStr);
    if (recvIndex !== inputIndex) {
      throw new Error(`Index mismatch: got ${recvIndex}, expected ${inputIndex}`);
    }
    socket.send(JSON.stringify({ type: "inputInfoOk" }));
    ffmpegLog("info", `inputInfoOk ${inputIndex}`);

    const realName = ffargs.args[recvIndex];
    const ext = guessExtension(realName);

    // e.g. "job2_input0.mp4"
    const safeIn = `job${jobCounter}_input${i}${ext}`;
    ffmpegLog("info", `receiving input #${recvIndex} => ${safeIn}, size=${fileSize}`);

    // Wait for the binary data
    const fileData = await receiveBinary(socket, fileSize);
    await ffmpeg.writeFile(safeIn, fileData);

    socket.send(JSON.stringify({ type: "inputOk" }));
    ffmpegLog("info", `inputOk ${inputIndex}`);

    // Patch safeArgs so it references the safeIn path
    inputMap[recvIndex] = safeIn;
    safeArgs[recvIndex] = safeIn;
  }

  // 2) Build a map of output index => safeOut path
  const outMap = {};
  for (let i = 0; i < ffargs.outputs.length; i++) {
    // For each output index, build something like "job2_out0.mp4"
    // if i is within range
    const outIndex = ffargs.outputs[i];
    if (outIndex >= 0 && outIndex < ffargs.args.length) {
      const origOut = ffargs.args[outIndex];
      const outExt = guessExtension(origOut);
      const safeOut = `job${jobCounter}_out${i}${outExt}`;

      outMap[outIndex] = safeOut;
      safeArgs[outIndex] = safeOut;
    }
  }

  // ephemeral log capturing
  ffmpeg.on("log", onLog);

  try {
    // We'll skip the first argument if it ends with "ffmpeg" or "ffprobe"
    const callArgs = safeArgs.slice(1);

    // 2B) run
    if (isFfprobe) {
      ffmpegLog("info", "Running ffprobe with callArgs:", callArgs);
      await ffmpeg.ffprobe(callArgs);
      ffmpegLog("info", "ffprobe done");
    } else {
      ffmpegLog("info", "Running ffmpeg exec with callArgs:", callArgs);
      await ffmpeg.exec(callArgs);
      ffmpegLog("info", "ffmpeg exec done");
    }

    // 2C) send log end
    const logEnd = JSON.stringify({ type: "logEnd" });
    socket.send(logEnd);
    ffmpegLog("info", "logEnd");

    // 3) read each output from FS
    for (let i = 0; i < ffargs.outputs.length; i++) {
      const outIndex = ffargs.outputs[i];
      if (outIndex < 0 || outIndex >= ffargs.args.length) {
        // invalid => 0
        socket.send(JSON.stringify({ type: "outInfo", outInfo: [outIndex, 0] }));
        ffmpegLog("info", `Output index ${outIndex} is out of range => 0 bytes`);
        continue;
      }
      const safePath = outMap[outIndex];
      if (!safePath) {
        socket.send(JSON.stringify({ type: "outInfo", outInfo: [outIndex, 0] }));
        ffmpegLog("info", `No safe path => 0 bytes for outIndex ${outIndex}`);
        continue;
      }
      // read it
      const outData = await ffmpeg.readFile(safePath);
      ffmpegLog("info", `Output #${i}, original index ${outIndex}, size: ${outData.length} bytes`);
      // send meta + data
      const meta = JSON.stringify({ type: "outInfo", outInfo: [outIndex, outData.length] });
      socket.send(meta);
      socket.send(outData.buffer);
      ffmpegLog("info", "Sent output to server");
    }

  } finally {
    // Remove ephemeral log listener
    ffmpeg.off("log", onLog);

    // 4) remove inputs from FS
    for (const safeIn of Object.values(inputMap)) {
      try { ffmpeg.deleteFile(safeIn); } catch(e){}
    }

    // remove outputs from FS
    for (const safeOut of Object.values(outMap)) {
      try { ffmpeg.deleteFile(safeOut); } catch(e){}
    }
  }
}

/**
 * guessExtension: a naive approach to get an extension from a path.
 * If there's no '.', returns ".dat".
 */
function guessExtension(filePath) {
  if (!filePath) return ".dat";
  const i = filePath.lastIndexOf(".");
  if (i < 0) return ".dat";
  return filePath.substring(i);
}


/* multi-job approach from earlier: send "ready", if "nomore" => break, else parse => flow(...) */
async function mainLoop() {

  while (true) {
      
    const wsProtocol = (location.protocol === "https:") ? "wss://" : "ws://";
    const socketURL = wsProtocol + location.host + "/ws/ffmpeg";
    const socket = new WebSocket(socketURL);
    const controller = new AbortController();
    const { signal } = controller;

    let messageQueue = [];
    let pairQueue = [];
    let promise0, resolver0;
    let promise1;

    promise0 = new Promise(resolve => resolver0 = resolve);
    socket.addEventListener("open", async () => {
      ffmpegLog("info", "WebSocket for ffmpeg open");

      promise1 = cycleJobs(socket, signal);
      signal.addEventListener("abort", () => {
        socket.close();
        while (pairQueue.length) {
          const { reject } = pairQueue.shift();
          reject(new Error("Socket closed"));
        }
        messageQueue = null;
        pairQueue = null;
        resolver0();
      });

      resolver0();
    });

    socket.shift = async () => {
      if (messageQueue.length > 0) return messageQueue.shift();
      return new Promise((resolve, reject) => {
        pairQueue.push({ resolve, reject });
      });
    };

    socket.addEventListener("message", (ev) => {
      const { resolve } = pairQueue.shift() || {};
      if (resolve) {
        resolve(ev);
        return;
      }
      messageQueue.push(ev);
    });
    socket.addEventListener("close", (ev) => {
      ffmpegLog("error", "WebSocket closed:", ev);
      controller.abort();
    });
    socket.addEventListener("error", (err) => {
      ffmpegLog("error", "WebSocket error:", err);
      controller.abort();
    });

    await promise0;
    await promise1;
    await new Promise((resolve) => setTimeout(resolve, 5000));

  }

}

document.addEventListener('DOMContentLoaded', async () => {
  mainLoop();
});

/* -------------------------------------------------------------------
   The waitForTextMessage, receiveBinary, etc. for chunked input.
------------------------------------------------------------------- */

async function waitForTextMessage(socket) {
  const msg = await socket.shift();
  if (typeof msg.data !== "string")
    throw new Error("Binary given, expected text");
  return msg.data;
}

async function waitForBinaryMessage(socket) {
  const msg = await socket.shift();
  if (typeof msg.data === "string")
    throw new Error("Text given, expected binary");
  const abuf = await msg.data.arrayBuffer();
  return new Uint8Array(abuf);
}

async function receiveBinary(socket, fileSize) {
  let received = 0;
  const chunks = [];
  while (received < fileSize) {
    const chunk = await waitForBinaryMessage(socket);
    chunks.push(chunk);
    received += chunk.length;
    ffmpegLog("info", `got chunk size=${chunk.length}, total so far=${received}/${fileSize}`);
  }
  return mergeChunks(chunks, received);
}


function mergeChunks(chunks, totalSize) {
  const out = new Uint8Array(totalSize);
  let offset = 0;
  for (const c of chunks) {
    out.set(c, offset);
    offset += c.length;
  }
  return out;
}












(() => { // Segmented encoding PoC
  /**
   * Use the segment muxer to split into valid segments that each start at a keyframe.
   * Return an array of { name, data } in memory.
   */
  async function segmentVideo(ffmpeg, inputName, segmentTime) {
    // We'll produce segment_0.mp4, segment_1.mp4, etc.
    // -f segment: use the segment muxer
    // -segment_time: how many seconds each segment is
    // -force_key_frames: ensures each segment starts on a keyframe
    // -reset_timestamps 1: ensures each segment starts at t=0
    const segPattern = "segment_%d.mp4";
  
    await ffmpeg.exec([
      "-i", inputName,
      "-c", "copy",
      "-f", "segment",
      "-segment_time", `${segmentTime}`,            // e.g. 10 seconds per segment
      "-reset_timestamps", "1",
      "-force_key_frames", `expr:gte(t,n_forced*${segmentTime})`,
      "-threads", "1",
      segPattern,
    ]);
  
    // The tricky part: we need to figure out how many segments got created
    // We can do that by checking the FS for files that match "segment_0.mp4", "segment_1.mp4", etc.
    // We'll keep reading until we can no longer find a file.
    let index = 0;
    const segments = [];
  
    while (true) {
      const name = `segment_${index}.mp4`;
      try {
        const data = await ffmpeg.readFile(name);
        segments.push({ name, data });
        // optionally remove from FS
        await ffmpeg.deleteFile(name);
        index++;
      } catch {
        // no more segments
        break;
      }
    }
  
    return segments;
  }
  
  /**
   * Encode a single segment with libx265
   */
  let i = 0;
  async function encodeSegmentX265(segment, crf = 28) {
    const ff = await newFFmpeg();
  
    // Write the segment data
    await ff.writeFile(segment.name, segment.data);
  
    // We'll produce an output "encoded_XXX.mp4"
    const outName = `encoded_${segment.name}`;
  
    let j = ++i;
    
    console.log("ENCODE IN", j);
  
    await ff.exec([
      "-i", segment.name,
      "-c:v", "libx265",
      "-crf", `${crf}`,
      "-c:a", "copy", // keep original audio if present
      "-threads", "1",
      outName,
    ]);
  
    const outData = await ff.readFile(outName);
  
    // Cleanup
    await ff.deleteFile(segment.name);
    await ff.deleteFile(outName);
  
    console.log("ENCODE OUT", j);
  
    ff.terminate();
    return { name: outName, data: outData };
  }
  
  /**
   * Encode multiple segments in parallel using Promise.all
   */
  async function encodeAllSegmentsParallel(segments, crf) {
    const tasks = segments.map(seg => encodeSegmentX265(seg, crf));
    return Promise.all(tasks);
  }
  
  /**
   * Concatenate all encoded segments back into a single MP4
   */
  async function concatSegments(ffmpeg, encodedSegs) {
    for (const seg of encodedSegs) {
      await ffmpeg.writeFile(seg.name, seg.data);
    }
    // Create a concat list
    const listFile = "concat.txt";
    const listContent = encodedSegs.map(s => `file '${s.name}'`).join("\n");
    await ffmpeg.writeFile(listFile, listContent);
  
    const outName = "finalOutput.mp4";
  
    await ffmpeg.exec([
      "-f", "concat",
      "-safe", "0",
      "-i", listFile,
      "-c", "copy",
      "-threads", "1",
      outName
    ]);
  
    const finalData = await ffmpeg.readFile(outName);
  
    // Cleanup if desired
    // for (const seg of encodedSegs) {
    //   await ffmpeg.unlink(seg.name);
    // }
    // await ffmpeg.unlink(listFile);
    // await ffmpeg.unlink(outName);
  
    return { name: outName, data: finalData };
  }
  
  /**
   * Main function
   *  1. Load a "splitter" FFmpeg instance
   *  2. Segment using the segment muxer
   *  3. Encode all segments in parallel
   *  4. Concat them
   *  5. Return final Blob URL
   */
  window.encodeWithFFmpegWasm = async function(src, segmentTime = 10, crf = 28) {
    // 1) Create the "splitter" instance & load input
    const splitterFF = await newFFmpeg();
    const inputFile = await fetchFile(src);
    const inputName = "input.mp4";
    await splitterFF.writeFile(inputName, inputFile);
  
    console.log("SEG NO", segmentTime);
    // 2) Segment the input video
    const segments = await segmentVideo(splitterFF, inputName, segmentTime);
    console.log("SEGMENT", segments.length);
  
    // Cleanup / exit
    await splitterFF.deleteFile(inputName);
    splitterFF.terminate();
  
    if (!segments.length) {
      throw new Error("No segments were created!");
    }
  
    // 3) Encode all segments in parallel
    const encodedSegments = await encodeAllSegmentsParallel(segments, crf);
    console.log("ENCODE END");
  
    // 4) Concat them in a new FFmpeg instance
    const concatFF = await newFFmpeg();
    const { data: finalData } = await concatSegments(concatFF, encodedSegments);
    concatFF.terminate();
    console.log("CONCAT");
  
    // 5) Convert to Blob URL
    const blob = new Blob([finalData.buffer], { type: "video/mp4" });
    const url = URL.createObjectURL(blob);
    console.log("Final output Blob URL:", url);
    return url;
  };
  
  })/*()*/;
  