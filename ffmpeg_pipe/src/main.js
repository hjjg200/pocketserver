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
 * getDuration:
 *   Retrieves the duration of a file using ffprobe JSON output.
 */
async function getDuration(ffmpeg, inputFileName) {
  let stdout = "";
  let stderr = "";

  const onLog = (evt) => {
    if (evt.type === "stdout") {
      stdout += evt.message + "\n";
    } else if (evt.type === "stderr") {
      stderr += evt.message + "\n";
    }
  };

  ffmpeg.on("log", onLog);

  // Run ffprobe in JSON mode
  // NOTE: ffmpeg.wasm provides 'ffmpeg.ffprobe' in some builds. If not, you can emulate
  // an ffprobe call with `-f ffprobe` or parse the console logs. Adjust as needed.
  await ffmpeg.ffprobe([
    "-i", inputFileName,
    "-show_entries", "format=duration",
    "-print_format", "json",
  ]);

  ffmpeg.off("log", onLog);

  let probeResult;
  try {
    probeResult = JSON.parse(stdout.trim());
  } catch (err) {
    throw new Error("ffprobe JSON parsing failed: " + err.message);
  }

  const duration = parseFloat(probeResult.format?.duration);
  if (isNaN(duration)) {
    throw new Error("Failed to parse duration from ffprobe output.");
  }
  return duration;
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
async function correctLoudnessPass2(ffmpeg, wavFileName, outputFileName, analysis, durationSeconds, targetLUFS) {
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

  // Encode to AAC, apply loudnorm filter, and keep the same duration
  await ffmpeg.exec([
    "-i", wavFileName,
    "-af", pass2Filter,
    "-t", String(durationSeconds),
    "-c:a", "aac",
    "-b:a", "128k",
    "-f", "ipod",
    "-movflags", "+faststart",
    outputFileName,
  ]);
}

/**
 * copyMetadataAndCover:
 *   Takes the loudness-corrected audio (tempCorrected.m4a) plus the original input,
 *   then merges the original metadata and embedded cover art (if any) re-encoded to MJPEG.
 *
 *   Steps:
 *     -map 0:a   : from the corrected audio
 *     -map 1:v:0?: from the original input's cover (if it exists at all)
 *     -c:a copy  : don't re-encode the audio again
 *     -c:v mjpeg : re-encode the cover to MJPEG (fixing h264 cover, etc.)
 *     -disposition:v:0 attached_pic : mark it as “cover art”
 *     -map_metadata 1 : copy metadata (title, artist, etc.) from input
 *     -movflags +faststart : better for web playback
 */
async function copyMetadataAndCover(ffmpeg, inputFileName, correctedFileName, finalFileName) {
  await ffmpeg.exec([
    // 1) The audio file with corrected loudness
    "-i", correctedFileName,
    // 2) The original file that has metadata and cover
    "-i", inputFileName,

    // Audio from the corrected file
    "-map", "0:a",

    // Cover art (video stream #0) from the original, if present. The `?` makes it optional.
    "-map", "1:v:0?",

    // Copy (passthrough) the audio from the corrected file
    "-c:a", "copy",

    // Re-encode the cover to MJPEG
    "-c:v", "mjpeg",

    // Mark it as attached cover
    "-disposition:v:0", "attached_pic",

    // Copy all metadata from input #1 (the original source)
    "-map_metadata", "1",

    // Prevent chrome freeze when embedding album art
    "-threads", "1",

    "-movflags", "+faststart",
    finalFileName,
  ]);
}

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
window.ffmpegSoundCheck = async (src, targetLUFS = -14) => {
  const ffmpeg = await newFFmpeg();

  // 0) Download the file into memory
  const inputFile = await fetchFile(src);
  const inputFileName = `input${guessExtension(src)}`; // e.g. input.mp3, input.m4a, etc.
  const wavFileName = "temp.wav";
  const tempCorrectedFile = "tempCorrected.m4a";
  const finalOutputFile = "finalOutput.m4a";

  // Write the input file to the in-memory FS
  console.log("Writing input file to FS...");
  await ffmpeg.writeFile(inputFileName, inputFile);

  // 1) Get input duration
  console.log("Probing duration...");
  const inputDuration = await getDuration(ffmpeg, inputFileName);
  console.log("Input duration (seconds):", inputDuration.toFixed(3));

  // 2) Convert input to WAV (PCM) for faster loudnorm pass
  console.log("Converting to WAV...");
  await ffmpeg.exec([
    "-i", inputFileName,
    "-c:a", "pcm_s16le",
    "-ar", "48000",
    wavFileName,
  ]);

  // 3) Loudness Analysis (Pass 1)
  console.log("Analyzing loudness (pass 1)...");
  const analysis = await analyzeLoudnessPass1(ffmpeg, wavFileName, targetLUFS);
  console.log("Pass 1 analysis:", analysis);

  // 4) Loudness Correction (Pass 2) -> tempCorrected.m4a (audio only, minimal metadata)
  console.log("Correcting loudness (pass 2)...");
  await correctLoudnessPass2(ffmpeg, wavFileName, tempCorrectedFile, analysis, inputDuration, targetLUFS);

  // 5) Merge original metadata + album art (re-encoded to MJPEG) into finalOutput.m4a
  console.log("Merging metadata & cover art...");
  await copyMetadataAndCover(ffmpeg, inputFileName, tempCorrectedFile, finalOutputFile);

  // 6) Read finalOutput.m4a back from FS, make a blob
  console.log("Reading final file...");
  const finalData = await ffmpeg.readFile(finalOutputFile);
  const blob = new Blob([finalData.buffer], { type: "audio/mp4" });

  // 7) Create a URL for the final .m4a
  const url = URL.createObjectURL(blob);
  console.log("Final Blob URL:", url);

  return url;
};






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
