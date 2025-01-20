import { FFmpeg } from '@ffmpeg/ffmpeg';
import { fetchFile } from '@ffmpeg/util';

/*
  Final multi-job code with the following features:
    1) Filenames in wasm FS = "input1.ext", "input2.ext", ... "output.ext".
       That avoids any UTF-8 or path issues.
    2) Ephemeral 'log' listener that sends logs line by line to the server,
       then sends 'EOF_LOG' after finishing.
    3) Check if the 0th argument is something like "ffprobe" => use ffprobe
       instead of exec. If there's an output file (like -o out.txt),
       we read it and send. Otherwise, 0 bytes.
    4) Multi-job: 'cycleJobs' with "ready" <-> "nomore"/args.
*/

const ffmpeg = new FFmpeg();

// Global job counter to name input files uniquely
let jobCounter = 0;

/** Log + progress forwarding is optional. We'll attach ephemeral 'log' listener for each job. */
ffmpeg.on('progress', (prog) => {
  console.log(`[FFmpeg progress] frame=${prog.frame}, fps=${prog.fps}, time=${prog.time}`);
});

/**
 * The main multi-job loop: 
 *   1) send "ready"
 *   2) wait for text message 
 *      - "nomore" => break
 *      - otherwise => parse ffargs => flow(ffargs)
 *   3) repeat
 */
async function cycleJobs(socket) {
  try {
    while (true) {
      socket.send("ready");
      console.log("[FFmpeg] Sent 'ready' – waiting for new ffargs or 'nomore'...");

      const msg = await waitForTextMessage(socket);
      const line = msg.trim();

      if (line === "nomore") {
        console.log("[FFmpeg] No more jobs from server, stopping cycle.");
        break;
      }

      // parse ffargs
      const ffargs = JSON.parse(line);
      await flow(ffargs, socket);

      console.log("[FFmpeg] Job done. Going for next job...");
    }
    console.log("[FFmpeg] cycleJobs ended – no more tasks.");
  } catch (err) {
    console.error("[FFmpeg] cycleJobs error:", err);
  }
}
/**
 * flow(ffargs, socket):
 *   1) receive input files and write them to wasm FS
 *   2) if we detect 'ffprobe' at the 0th arg, call ffmpeg.ffprobe(...) with a slice
 *      else call ffmpeg.exec(...) with a slice
 *   3) ephemeral log listener
 *   4) read output from FS if any, send to server
 */
async function flow(ffargs, socket) {
  jobCounter++;
  // ephemeral log array
  const jobLogs = [];

  // ephemeral log listener
  const onLog = (entry) => {
    const logLine = entry.message;
    jobLogs.push(logLine);
    // also stream each line to server
    socket.send(JSON.stringify({ type: "logLine", logLine }));
  };

  // 1) receive & write input files, using ASCII-safe names
  const inputMap = {};
  for (let i = 0; i < ffargs.inputs.length; i++) {
    const inputIndex = ffargs.inputs[i];
    const metaStr = await waitForTextMessage(socket);
    const [recvIndex, fileSize] = JSON.parse(metaStr);
    if (recvIndex !== inputIndex) {
      throw new Error(`Index mismatch: got ${recvIndex}, expected ${inputIndex}`);
    }
    const realName = ffargs.args[recvIndex];
    const ext = guessExtension(realName);
    const safeIn = `job${jobCounter}_input${i}${ext}`;
    console.log(`[FFmpeg] receiving input #${recvIndex} => ${safeIn}, size=${fileSize}`);

    const data = await receiveBinary(socket, fileSize);
    await ffmpeg.writeFile(safeIn, data);
    inputMap[recvIndex] = safeIn;
  }

  // 2) ephemeral log capturing
  ffmpeg.on("log", onLog);
  let outSafe = "";
  let hadOutput = false;
  try {
    // 2A) figure out if this is ffprobe or ffmpeg
    // We look at ffargs.args[0], but we only skip it right before calling the method
    let isFfprobe = false;
    if (ffargs.args[0].endsWith("ffprobe")) {
      isFfprobe = true;
    }

    // 2B) patch up input references in ffargs.args with our "safe" input FS names
    // (No slicing the array, just do find & replace of input file path)
    for (const idx of ffargs.inputs) {
      const safeName = inputMap[idx];
      if (!safeName) continue;
      const origName = ffargs.args[idx];
      // find occurrences in ffargs.args
      for (let r = 0; r < ffargs.args.length; r++) {
        if (ffargs.args[r] === origName) {
          ffargs.args[r] = safeName;
        }
      }
    }

    // 2C) detect output
    if (ffargs.output >= 0 && ffargs.output < ffargs.args.length) {
      const origOut = ffargs.args[ffargs.output];
      const outExt = guessExtension(origOut);
      outSafe = `job${jobCounter}_out${outExt}`;
      // find & replace
      for (let r = 0; r < ffargs.args.length; r++) {
        if (ffargs.args[r] === origOut) {
          ffargs.args[r] = outSafe;
        }
      }
    }

    // 2D) create the final "callArgs" by slicing if needed
    // If ffargs.args[0] is "pocketserver.ffmpeg" or "pocketserver.ffprobe" etc.
    // only skip the first item if it ends with "ffmpeg" or "ffprobe"
    const callArgs = skipFirstIfNeeded(ffargs.args);

    // 2E) run ffprobe or exec
    if (isFfprobe) {
      console.log("[FFmpeg] Running ffprobe with callArgs:", callArgs);
      await ffmpeg.ffprobe(callArgs);
      console.log("[FFmpeg] ffprobe done");
    } else {
      console.log("[FFmpeg] Running ffmpeg exec with callArgs:", callArgs);
      await ffmpeg.exec(callArgs);
      console.log("[FFmpeg] ffmpeg exec done");
    }

  } finally {
    ffmpeg.off("log", onLog);
  }

  try {
    // 3) read the output if we have outSafe
    if (outSafe) {
      const outData = await ffmpeg.readFile(outSafe);
      hadOutput = true;
      console.log(`[FFmpeg] Output size: ${outData.length} bytes`);

      // send meta + data
      const meta = JSON.stringify({ type: "outInfo", outInfo: [ffargs.output, outData.length]});
      socket.send(meta);
      socket.send(outData.buffer);
      console.log("[FFmpeg] Sent output to server");
    } else {
      // no output => send 0 length
      socket.send(JSON.stringify({ type: "outInfo", outInfo: [-1, 0]}));
      console.log("[FFmpeg] No output. Sent 0 bytes info.");
    }
  } finally {
    // optionally remove input & output from FS
    for (const safeName of Object.values(inputMap)) {
      try { ffmpeg.FS('unlink', safeName); } catch(e){}
    }
    if (outSafe && hadOutput) {
      try { ffmpeg.FS('unlink', outSafe); } catch(e){}
    }
  }
}

/**
 * Only strip out the first item from "args" if it's "ffmpeg" or "ffprobe"
 */
function skipFirstIfNeeded(array) {
  if (array.length > 0) {
    const first = array[0];
    if (first.endsWith("ffmpeg") || first.endsWith("ffprobe")) {
      return array.slice(1);
    }
  }
  return array; // unchanged
}
  
/* ------------------------------------------------------------------- */
/* Additional Helpers */
/* ------------------------------------------------------------------- */

// a simple guessExtension function. real logic might parse the string
// or do ".mp4" if we see "mp4" etc. We'll just do a naive approach here.
function guessExtension(filePath) {
  // find last dot
  const i = filePath.lastIndexOf(".");
  if (i < 0) return ".dat"; 
  return filePath.substring(i); // e.g. ".mp4"
}

/* same multi-job approach from earlier: send "ready", if "nomore" break, else parse ffargs => flow(...) */
async function mainLoop(socket) {
  await cycleJobs(socket);
  console.log("[FFmpeg] All jobs completed or 'nomore'.");
}

/* We'll call mainLoop after we open the socket + load ffmpeg. */
document.addEventListener('DOMContentLoaded', async () => {
  const wsProtocol = (location.protocol === "https:") ? "wss://" : "ws://";
  const socketURL = wsProtocol + location.host + "/ws/ffmpeg";
  const socket = new WebSocket(socketURL);

  socket.addEventListener("open", async () => {
    console.log("[FFmpeg] WebSocket open. Loading ffmpeg core...");
    await ffmpeg.load({
      corePath: "/static/ffmpeg/ffmpeg-core.js",
      classWorkerURL: "/static/ffmpeg/worker.js"
    });
    console.log("[FFmpeg] ffmpeg core loaded. Starting job cycle...");
    mainLoop(socket);
  });

  socket.addEventListener("close", (ev) => {
    console.log("[FFmpeg] WebSocket closed:", ev);
  });
  socket.addEventListener("error", (err) => {
    console.error("[FFmpeg] WebSocket error:", err);
  });
});

/* ------------------------------------------------------------------- */
/* The waitForTextMessage, receiveBinary, etc. for chunked input. */
/* ------------------------------------------------------------------- */

function waitForTextMessage(socket) {
  return new Promise((resolve, reject) => {
    const onMessage = (evt) => {
      if (typeof evt.data === "string") {
        cleanup();
        resolve(evt.data);
      } else {
        console.warn("[FFmpeg] ignoring binary while expecting text msg");
      }
    };
    const onErr = (err) => { cleanup(); reject(err); };
    const onClose = () => { cleanup(); reject(new Error("[FFmpeg] socket closed (text)")); };

    function cleanup() {
      socket.removeEventListener("message", onMessage);
      socket.removeEventListener("error", onErr);
      socket.removeEventListener("close", onClose);
    }

    socket.addEventListener("message", onMessage);
    socket.addEventListener("error", onErr);
    socket.addEventListener("close", onClose);
  });
}

async function receiveBinary(socket, fileSize) {
  let received = 0;
  const chunks = [];
  while (received < fileSize) {
    const chunk = await waitForBinaryMessage(socket);
    chunks.push(chunk);
    received += chunk.length;
    console.log(`[FFmpeg] got chunk size=${chunk.length}, total so far=${received}/${fileSize}`);
  }
  return mergeChunks(chunks, received);
}

function waitForBinaryMessage(socket) {
  return new Promise((resolve, reject) => {
    const onMessage = async (evt) => {
      if (typeof evt.data === "string") {
        console.warn("[FFmpeg] ignoring text while expecting binary");
        return;
      }
      cleanup();
      const abuf = await evt.data.arrayBuffer();
      resolve(new Uint8Array(abuf));
    };
    const onErr = (err) => { cleanup(); reject(err); };
    const onClose = () => { cleanup(); reject(new Error("[FFmpeg] socket closed (binary)")); };

    function cleanup() {
      socket.removeEventListener("message", onMessage);
      socket.removeEventListener("error", onErr);
      socket.removeEventListener("close", onClose);
    }

    socket.addEventListener("message", onMessage);
    socket.addEventListener("error", onErr);
    socket.addEventListener("close", onClose);
  });
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
