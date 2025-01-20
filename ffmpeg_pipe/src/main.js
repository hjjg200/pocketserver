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

const ffmpeg = new FFmpeg();

// Global job counter to name input & output files uniquely
let jobCounter = 0;

/** Optional: track progress events for debugging. */
ffmpeg.on('progress', (prog) => {
  console.log(`[FFmpeg progress] frame=${prog.frame}, fps=${prog.fps}, time=${prog.time}`);
});

/**
 * The main multi-job loop:
 *   1) send "ready"
 *   2) wait for text message 
 *      - "nomore" => break
 *      - otherwise => parse JSON => flow(ffargs)
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

      // parse the ffargs object
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
 *   1) receive input files => "jobX_inputY.ext" in FS
 *   2) if 0th arg ends with "ffprobe" => ffprobe(...) else ffmpeg.exec(...)
 *   3) ephemeral logs => log lines streaming
 *   4) read each output => send to server
 */
async function flow(ffargs, socket) {
  jobCounter++;

  // ephemeral log array
  const jobLogs = [];

  // ephemeral log listener
  const onLog = (entry) => {
    const logLine = entry.message;
    jobLogs.push(logLine);
    // Also stream each line to server
    socket.send(JSON.stringify({ type: "logLine", logLine }));
  };

  // Make a local copy of arguments that we can patch in-place
  const safeArgs = ffargs.args.slice();

  // 1) Receive & write input files, using ASCII-safe names
  const inputMap = {};
  /*
  let i = 0;
  let fileSizes = {};
  const inputPromise = new Promise((resolve, reject) => {
  
    if (ffargs.inputs.length == 0) {
      resolve();
      return;
    }

    const onMessage = async (evt) => {

      if (typeof evt.data === "string") {
        const inputIndex = ffargs.inputs[i];
        const metaStr = evt.data;
        const [recvIndex, fileSize] = JSON.parse(metaStr);
        if (recvIndex !== inputIndex) {
          throw new Error(`Index mismatch: got ${recvIndex}, expected ${inputIndex}`);
        }
        console.log(`[FFmpeg] expecting input #${recvIndex} size=${fileSize}`);

        fileSizes[inputIndex] = fileSize;

        const inputInfoOk = JSON.stringify({ type: "inputInfoOk" });
        socket.send(inputInfoOk);
        
      } else {

        const inputIndex = ffargs.inputs[i];
        const realName = ffargs.args[inputIndex];
        const fileSize = fileSizes[inputIndex];
        const ext = guessExtension(realName);

        // e.g. "job2_input0.mp4"
        const safeIn = `job${jobCounter}_input${i}${ext}`;
        console.log(`[FFmpeg] receiving input #${inputIndex} => ${safeIn}`);

        let received = 0;
        const chunks = [];
        while (received < fileSize) {
          const chunk = await waitForBinaryMessage(socket);
          chunks.push(chunk);
          received += chunk.length;
          console.log(`[FFmpeg] got chunk size=${chunk.length}, total so far=${received}/${fileSize}`);
        }
        return mergeChunks(chunks, received);
        
        const abuf = await evt.data.arrayBuffer();
        const fileData = new Uint8Array(abuf);
        await ffmpeg.writeFile(safeIn, fileData);
  
        // Patch safeArgs so it references the safeIn path
        inputMap[inputIndex] = safeIn;
        safeArgs[inputIndex] = safeIn;

        const inputOk = JSON.stringify({ type: "inputOk" });
        socket.send(inputOk);
        
        i++;
  
        // if it is the last file
        if (i >= ffargs.inputs.length)
          resolve();
      }

    }
    const onErr = (err) => { cleanup(); reject(err); };
    const onClose = () => { cleanup(); reject(new Error("[FFmpeg] socket closed (text)")); };

    function cleanup () {
      socket.removeEventListener("message", onMessage);
      socket.removeEventListener("error", onErr);
      socket.removeEventListener("close", onClose);
    }

    socket.addEventListener("message", onMessage);
    socket.addEventListener("error", onErr);
    socket.addEventListener("close", onClose);
  });
  await inputPromise;*/

  for (let i = 0; i < ffargs.inputs.length; i++) {
    const inputIndex = ffargs.inputs[i];

    // Wait for a text message describing the file's size
    const metaStr = await waitForTextMessage(socket);
    const [recvIndex, fileSize] = JSON.parse(metaStr);
    if (recvIndex !== inputIndex) {
      throw new Error(`Index mismatch: got ${recvIndex}, expected ${inputIndex}`);
    }
    socket.send(JSON.stringify({ type: "inputInfoOk" }));

    const realName = ffargs.args[recvIndex];
    const ext = guessExtension(realName);

    // e.g. "job2_input0.mp4"
    const safeIn = `job${jobCounter}_input${i}${ext}`;
    console.log(`[FFmpeg] receiving input #${recvIndex} => ${safeIn}, size=${fileSize}`);

    // Wait for the binary data
    const fileData = await receiveBinary(socket, fileSize);
    await ffmpeg.writeFile(safeIn, fileData);

    socket.send(JSON.stringify({ type: "inputOk" }));

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
    // 2A) determine if ffprobe or ffmpeg
    let isFfprobe = false;
    if (ffargs.args[0].endsWith("ffprobe")) {
      isFfprobe = true;
    }

    // We'll skip the first argument if it ends with "ffmpeg" or "ffprobe"
    const callArgs = skipFirstIfNeeded(safeArgs);

    // 2B) run
    if (isFfprobe) {
      console.log("[FFmpeg] Running ffprobe with callArgs:", callArgs);
      await ffmpeg.ffprobe(callArgs);
      console.log("[FFmpeg] ffprobe done");
    } else {
      console.log("[FFmpeg] Running ffmpeg exec with callArgs:", callArgs);
      await ffmpeg.exec(callArgs);
      console.log("[FFmpeg] ffmpeg exec done");
    }

    // 2C) send log end
    const logEnd = JSON.stringify({ type: "logEnd" });
    socket.send(logEnd);

    // 3) read each output from FS
    for (let i = 0; i < ffargs.outputs.length; i++) {
      const outIndex = ffargs.outputs[i];
      if (outIndex < 0 || outIndex >= ffargs.args.length) {
        // invalid => 0
        socket.send(JSON.stringify({ type: "outInfo", outInfo: [outIndex, 0] }));
        console.log(`[FFmpeg] Output index ${outIndex} is out of range => 0 bytes`);
        continue;
      }
      const safePath = outMap[outIndex];
      if (!safePath) {
        socket.send(JSON.stringify({ type: "outInfo", outInfo: [outIndex, 0] }));
        console.log(`[FFmpeg] No safe path => 0 bytes for outIndex ${outIndex}`);
        continue;
      }
      // read it
      console.log("dc", await ffmpeg.listDir("/"), safePath)
      const outData = await ffmpeg.readFile(safePath);
      console.log(`[FFmpeg] Output #${i}, original index ${outIndex}, size: ${outData.length} bytes`);
      // send meta + data
      const meta = JSON.stringify({ type: "outInfo", outInfo: [outIndex, outData.length] });
      socket.send(meta);
      socket.send(outData.buffer);
      console.log("[FFmpeg] Sent output to server");
    }

  } finally {
    // Remove ephemeral log listener
    ffmpeg.off("log", onLog);

    // 4) remove inputs from FS
    for (const safeIn of Object.values(inputMap)) {
      try { ffmpeg.FS("unlink", safeIn); } catch(e){}
    }

    // remove outputs from FS
    for (const safeOut of Object.values(outMap)) {
      try { ffmpeg.FS("unlink", safeOut); } catch(e){}
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

/**
 * skipFirstIfNeeded: if the first argument ends with 'ffmpeg' or 'ffprobe',
 * strip it out. Otherwise return the array unchanged.
 */
function skipFirstIfNeeded(args) {
  if (!args.length) return args;
  const first = args[0];
  if (first.endsWith("ffmpeg") || first.endsWith("ffprobe")) {
    return args.slice(1);
  }
  return args;
}

/* multi-job approach from earlier: send "ready", if "nomore" => break, else parse => flow(...) */
async function mainLoop(socket) {
  await cycleJobs(socket);
  console.log("[FFmpeg] All jobs completed or 'nomore'.");
}

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

/* -------------------------------------------------------------------
   The waitForTextMessage, receiveBinary, etc. for chunked input.
------------------------------------------------------------------- */



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
