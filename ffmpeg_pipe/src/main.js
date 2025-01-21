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

let ffmpeg;

// Global job counter to name input & output files uniquely
let jobCounter = 0;


async function pongBackMessageOfType(socket, typ) {
  const obj = JSON.parse(await waitForTextMessage(socket));
  if (obj.type !== "ready") {
    throw new Error(`Wrongly typed message, expected ${typ}, received ${obj.type}`);
  }
  socket.send(JSON.stringify({ type: typ }));
  console.log(`[FFmpeg] Ping ${typ} from server, pong-backed ${typ}`);

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
async function cycleJobs(socket) {
  try {
    while (true) {

      // Ready
      pongBackMessageOfType(socket, "ready");

      // taskReady
      pongBackMessageOfType(socket, "taskReady");

      // parse the ffargs object
      const ffargs = pongBackMessageOfType(socket, "ffargs");
      try {
        ffmpeg = new FFmpeg();
        await ffmpeg.load({
          corePath: "/static/ffmpeg/ffmpeg-core.js",
          classWorkerURL: "/static/ffmpeg/worker.js"
        });
        await flow(ffargs, socket);
      } finally {
        ffmpeg.terminate();
      }

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

  // ephemeral log listener
  const onLog = (entry) => {
    const msg = JSON.stringify({
      type: "logLine",
      logType: entry.type,
      logLine: entry.message });
    // Also stream each line to server
    socket.send(msg);
  };

  // Make a local copy of arguments that we can patch in-place
  const safeArgs = ffargs.args.slice();

  // 2A) determine if ffprobe or ffmpeg
  let isFfprobe = false;
  if (ffargs.args[0].endsWith("ffprobe")) {
    isFfprobe = true;

    console.log(`[FFmpeg] works as ffprobe`);
  }

  // 1) Receive & write input files, using ASCII-safe names
  const inputMap = {};
  for (let i = 0; i < ffargs.inputs.length; i++) {
    const inputIndex = ffargs.inputs[i];

    console.log(`[FFmpeg] wait for input ${inputIndex}`);
    // Wait for a text message describing the file's size
    const metaStr = await waitForTextMessage(socket);
    const [recvIndex, fileSize] = JSON.parse(metaStr);
    if (recvIndex !== inputIndex) {
      throw new Error(`Index mismatch: got ${recvIndex}, expected ${inputIndex}`);
    }
    socket.send(JSON.stringify({ type: "inputInfoOk" }));
    console.log(`[FFmpeg] inputInfoOk ${inputIndex}`);

    const realName = ffargs.args[recvIndex];
    const ext = guessExtension(realName);

    // e.g. "job2_input0.mp4"
    const safeIn = `job${jobCounter}_input${i}${ext}`;
    console.log(`[FFmpeg] receiving input #${recvIndex} => ${safeIn}, size=${fileSize}`);

    // Wait for the binary data
    const fileData = await receiveBinary(socket, fileSize);
    await ffmpeg.writeFile(safeIn, fileData);

    socket.send(JSON.stringify({ type: "inputOk" }));
    console.log(`[FFmpeg] inputOk ${inputIndex}`);

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
    console.log("[FFmpeg] logEnd");

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
    console.log("[FFmpeg] ffmpeg core loaded. Starting job cycle...");
    mainLoop(socket);
  });

  let queue = [];
  let resolve;
  socket.shift = async () => {
    if (queue.length > 0) return queue.shift();
    return await new Promise((queueResolve) => {
      resolve = queueResolve;
    });
  };

  socket.addEventListener("message", (ev) => {
    if (resolve !== null) {
      resolve(ev);
      resolve = null;
      return;
    }
    queue.push(ev);
  });
  socket.addEventListener("close", (ev) => {
    queue = [];
    console.log("[FFmpeg] WebSocket closed:", ev);
  });
  socket.addEventListener("error", (err) => {
    queue = [];
    console.error("[FFmpeg] WebSocket error:", err);
  });
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
    console.log(`[FFmpeg] got chunk size=${chunk.length}, total so far=${received}/${fileSize}`);
  }
  return mergeChunks(chunks, received);
}

function waitForTextMessage2(socket) {
  return new Promise((resolve, reject) => {
    const onMessage = (evt) => {
      if (typeof evt.data === "string") {
        cleanup();
        resolve(evt.data);
      } else {
        cleanup();
        reject("[FFmpeg] binary given while expecting text");
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


function waitForBinaryMessage2(socket) {
  return new Promise((resolve, reject) => {
    const onMessage = async (evt) => {
      if (typeof evt.data === "string") {
        cleanup();
        reject("[FFmpeg] text given while expecting binary");
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
