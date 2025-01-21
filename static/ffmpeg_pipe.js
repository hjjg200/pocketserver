// node_modules/@ffmpeg/ffmpeg/dist/esm/const.js
var CORE_VERSION = "0.12.9";
var CORE_URL = `https://unpkg.com/@ffmpeg/core@${CORE_VERSION}/dist/umd/ffmpeg-core.js`;
var FFMessageType;
(function(FFMessageType2) {
  FFMessageType2["LOAD"] = "LOAD";
  FFMessageType2["EXEC"] = "EXEC";
  FFMessageType2["FFPROBE"] = "FFPROBE";
  FFMessageType2["WRITE_FILE"] = "WRITE_FILE";
  FFMessageType2["READ_FILE"] = "READ_FILE";
  FFMessageType2["DELETE_FILE"] = "DELETE_FILE";
  FFMessageType2["RENAME"] = "RENAME";
  FFMessageType2["CREATE_DIR"] = "CREATE_DIR";
  FFMessageType2["LIST_DIR"] = "LIST_DIR";
  FFMessageType2["DELETE_DIR"] = "DELETE_DIR";
  FFMessageType2["ERROR"] = "ERROR";
  FFMessageType2["DOWNLOAD"] = "DOWNLOAD";
  FFMessageType2["PROGRESS"] = "PROGRESS";
  FFMessageType2["LOG"] = "LOG";
  FFMessageType2["MOUNT"] = "MOUNT";
  FFMessageType2["UNMOUNT"] = "UNMOUNT";
})(FFMessageType || (FFMessageType = {}));

// node_modules/@ffmpeg/ffmpeg/dist/esm/utils.js
var getMessageID = /* @__PURE__ */ (() => {
  let messageID = 0;
  return () => messageID++;
})();

// node_modules/@ffmpeg/ffmpeg/dist/esm/errors.js
var ERROR_UNKNOWN_MESSAGE_TYPE = new Error("unknown message type");
var ERROR_NOT_LOADED = new Error("ffmpeg is not loaded, call `await ffmpeg.load()` first");
var ERROR_TERMINATED = new Error("called FFmpeg.terminate()");
var ERROR_IMPORT_FAILURE = new Error("failed to import ffmpeg-core.js");

// node_modules/@ffmpeg/ffmpeg/dist/esm/classes.js
var FFmpeg = class {
  #worker = null;
  /**
   * #resolves and #rejects tracks Promise resolves and rejects to
   * be called when we receive message from web worker.
   */
  #resolves = {};
  #rejects = {};
  #logEventCallbacks = [];
  #progressEventCallbacks = [];
  loaded = false;
  /**
   * register worker message event handlers.
   */
  #registerHandlers = () => {
    if (this.#worker) {
      this.#worker.onmessage = ({ data: { id, type, data } }) => {
        switch (type) {
          case FFMessageType.LOAD:
            this.loaded = true;
            this.#resolves[id](data);
            break;
          case FFMessageType.MOUNT:
          case FFMessageType.UNMOUNT:
          case FFMessageType.EXEC:
          case FFMessageType.FFPROBE:
          case FFMessageType.WRITE_FILE:
          case FFMessageType.READ_FILE:
          case FFMessageType.DELETE_FILE:
          case FFMessageType.RENAME:
          case FFMessageType.CREATE_DIR:
          case FFMessageType.LIST_DIR:
          case FFMessageType.DELETE_DIR:
            this.#resolves[id](data);
            break;
          case FFMessageType.LOG:
            this.#logEventCallbacks.forEach((f) => f(data));
            break;
          case FFMessageType.PROGRESS:
            this.#progressEventCallbacks.forEach((f) => f(data));
            break;
          case FFMessageType.ERROR:
            this.#rejects[id](data);
            break;
        }
        delete this.#resolves[id];
        delete this.#rejects[id];
      };
    }
  };
  /**
   * Generic function to send messages to web worker.
   */
  #send = ({ type, data }, trans = [], signal) => {
    if (!this.#worker) {
      return Promise.reject(ERROR_NOT_LOADED);
    }
    return new Promise((resolve, reject) => {
      const id = getMessageID();
      this.#worker && this.#worker.postMessage({ id, type, data }, trans);
      this.#resolves[id] = resolve;
      this.#rejects[id] = reject;
      signal?.addEventListener("abort", () => {
        reject(new DOMException(`Message # ${id} was aborted`, "AbortError"));
      }, { once: true });
    });
  };
  on(event, callback) {
    if (event === "log") {
      this.#logEventCallbacks.push(callback);
    } else if (event === "progress") {
      this.#progressEventCallbacks.push(callback);
    }
  }
  off(event, callback) {
    if (event === "log") {
      this.#logEventCallbacks = this.#logEventCallbacks.filter((f) => f !== callback);
    } else if (event === "progress") {
      this.#progressEventCallbacks = this.#progressEventCallbacks.filter((f) => f !== callback);
    }
  }
  /**
   * Loads ffmpeg-core inside web worker. It is required to call this method first
   * as it initializes WebAssembly and other essential variables.
   *
   * @category FFmpeg
   * @returns `true` if ffmpeg core is loaded for the first time.
   */
  load = ({ classWorkerURL, ...config } = {}, { signal } = {}) => {
    if (!this.#worker) {
      this.#worker = classWorkerURL ? new Worker(new URL(classWorkerURL, import.meta.url), {
        type: "module"
      }) : (
        // We need to duplicated the code here to enable webpack
        // to bundle worekr.js here.
        new Worker(new URL("./worker.js", import.meta.url), {
          type: "module"
        })
      );
      this.#registerHandlers();
    }
    return this.#send({
      type: FFMessageType.LOAD,
      data: config
    }, void 0, signal);
  };
  /**
   * Execute ffmpeg command.
   *
   * @remarks
   * To avoid common I/O issues, ["-nostdin", "-y"] are prepended to the args
   * by default.
   *
   * @example
   * ```ts
   * const ffmpeg = new FFmpeg();
   * await ffmpeg.load();
   * await ffmpeg.writeFile("video.avi", ...);
   * // ffmpeg -i video.avi video.mp4
   * await ffmpeg.exec(["-i", "video.avi", "video.mp4"]);
   * const data = ffmpeg.readFile("video.mp4");
   * ```
   *
   * @returns `0` if no error, `!= 0` if timeout (1) or error.
   * @category FFmpeg
   */
  exec = (args, timeout = -1, { signal } = {}) => this.#send({
    type: FFMessageType.EXEC,
    data: { args, timeout }
  }, void 0, signal);
  /**
   * Execute ffprobe command.
   *
   * @example
   * ```ts
   * const ffmpeg = new FFmpeg();
   * await ffmpeg.load();
   * await ffmpeg.writeFile("video.avi", ...);
   * // Getting duration of a video in seconds: ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 video.avi -o output.txt
   * await ffmpeg.ffprobe(["-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", "video.avi", "-o", "output.txt"]);
   * const data = ffmpeg.readFile("output.txt");
   * ```
   *
   * @returns `0` if no error, `!= 0` if timeout (1) or error.
   * @category FFmpeg
   */
  ffprobe = (args, timeout = -1, { signal } = {}) => this.#send({
    type: FFMessageType.FFPROBE,
    data: { args, timeout }
  }, void 0, signal);
  /**
   * Terminate all ongoing API calls and terminate web worker.
   * `FFmpeg.load()` must be called again before calling any other APIs.
   *
   * @category FFmpeg
   */
  terminate = () => {
    const ids = Object.keys(this.#rejects);
    for (const id of ids) {
      this.#rejects[id](ERROR_TERMINATED);
      delete this.#rejects[id];
      delete this.#resolves[id];
    }
    if (this.#worker) {
      this.#worker.terminate();
      this.#worker = null;
      this.loaded = false;
    }
  };
  /**
   * Write data to ffmpeg.wasm.
   *
   * @example
   * ```ts
   * const ffmpeg = new FFmpeg();
   * await ffmpeg.load();
   * await ffmpeg.writeFile("video.avi", await fetchFile("../video.avi"));
   * await ffmpeg.writeFile("text.txt", "hello world");
   * ```
   *
   * @category File System
   */
  writeFile = (path, data, { signal } = {}) => {
    const trans = [];
    if (data instanceof Uint8Array) {
      trans.push(data.buffer);
    }
    return this.#send({
      type: FFMessageType.WRITE_FILE,
      data: { path, data }
    }, trans, signal);
  };
  mount = (fsType, options, mountPoint) => {
    const trans = [];
    return this.#send({
      type: FFMessageType.MOUNT,
      data: { fsType, options, mountPoint }
    }, trans);
  };
  unmount = (mountPoint) => {
    const trans = [];
    return this.#send({
      type: FFMessageType.UNMOUNT,
      data: { mountPoint }
    }, trans);
  };
  /**
   * Read data from ffmpeg.wasm.
   *
   * @example
   * ```ts
   * const ffmpeg = new FFmpeg();
   * await ffmpeg.load();
   * const data = await ffmpeg.readFile("video.mp4");
   * ```
   *
   * @category File System
   */
  readFile = (path, encoding = "binary", { signal } = {}) => this.#send({
    type: FFMessageType.READ_FILE,
    data: { path, encoding }
  }, void 0, signal);
  /**
   * Delete a file.
   *
   * @category File System
   */
  deleteFile = (path, { signal } = {}) => this.#send({
    type: FFMessageType.DELETE_FILE,
    data: { path }
  }, void 0, signal);
  /**
   * Rename a file or directory.
   *
   * @category File System
   */
  rename = (oldPath, newPath, { signal } = {}) => this.#send({
    type: FFMessageType.RENAME,
    data: { oldPath, newPath }
  }, void 0, signal);
  /**
   * Create a directory.
   *
   * @category File System
   */
  createDir = (path, { signal } = {}) => this.#send({
    type: FFMessageType.CREATE_DIR,
    data: { path }
  }, void 0, signal);
  /**
   * List directory contents.
   *
   * @category File System
   */
  listDir = (path, { signal } = {}) => this.#send({
    type: FFMessageType.LIST_DIR,
    data: { path }
  }, void 0, signal);
  /**
   * Delete an empty directory.
   *
   * @category File System
   */
  deleteDir = (path, { signal } = {}) => this.#send({
    type: FFMessageType.DELETE_DIR,
    data: { path }
  }, void 0, signal);
};

// node_modules/@ffmpeg/ffmpeg/dist/esm/types.js
var FFFSType;
(function(FFFSType2) {
  FFFSType2["MEMFS"] = "MEMFS";
  FFFSType2["NODEFS"] = "NODEFS";
  FFFSType2["NODERAWFS"] = "NODERAWFS";
  FFFSType2["IDBFS"] = "IDBFS";
  FFFSType2["WORKERFS"] = "WORKERFS";
  FFFSType2["PROXYFS"] = "PROXYFS";
})(FFFSType || (FFFSType = {}));

// node_modules/@ffmpeg/util/dist/esm/errors.js
var ERROR_RESPONSE_BODY_READER = new Error("failed to get response body reader");
var ERROR_INCOMPLETED_DOWNLOAD = new Error("failed to complete download");

// src/main.js
var ffmpeg;
var jobCounter = 0;
async function pongBackMessageOfType(socket, typ) {
  const obj = JSON.parse(await waitForTextMessage(socket));
  if (obj.type !== typ) {
    throw new Error(`Wrongly typed message, expected ${typ}, received ${obj.type}`);
  }
  socket.send(JSON.stringify({ type: typ }));
  ffmpegLog("info", `Ping ${typ} from server, pong-backed ${typ}`);
  return obj[typ] || null;
}
async function cycleJobs(socket) {
  try {
    while (true) {
      await pongBackMessageOfType(socket, "ready");
      await pongBackMessageOfType(socket, "taskReady");
      const ffargs = await pongBackMessageOfType(socket, "ffargs");
      try {
        ffmpeg = new FFmpeg();
        await ffmpeg.load({
          corePath: "/static/ffmpeg/ffmpeg-core.js",
          classWorkerURL: "/static/ffmpeg/worker.js"
        });
        ffmpegLogShow();
        await flow(ffargs, socket);
      } finally {
        ffmpeg.terminate();
      }
      ffmpegLog("info", "Job done. Going for next job...");
    }
    ffmpegLog("info", "cycleJobs ended \u2013 no more tasks.");
  } catch (err) {
    ffmpegLog("error", "cycleJobs error:", err);
  }
}
async function flow(ffargs, socket) {
  jobCounter++;
  const onLog = (entry) => {
    const msg = JSON.stringify({
      type: "logLine",
      logType: entry.type,
      logLine: entry.message
    });
    socket.send(msg);
    if (entry.type === "stderr")
      ffmpegLog("internal", entry.message);
  };
  const safeArgs = ffargs.args.slice();
  let isFfprobe = false;
  if (ffargs.args[0].endsWith("ffprobe")) {
    isFfprobe = true;
    ffmpegLog("info", `works as ffprobe`);
  }
  const inputMap = {};
  for (let i = 0; i < ffargs.inputs.length; i++) {
    const inputIndex = ffargs.inputs[i];
    ffmpegLog("info", `wait for input ${inputIndex}`);
    const metaStr = await waitForTextMessage(socket);
    const [recvIndex, fileSize] = JSON.parse(metaStr);
    if (recvIndex !== inputIndex) {
      throw new Error(`Index mismatch: got ${recvIndex}, expected ${inputIndex}`);
    }
    socket.send(JSON.stringify({ type: "inputInfoOk" }));
    ffmpegLog("info", `inputInfoOk ${inputIndex}`);
    const realName = ffargs.args[recvIndex];
    const ext = guessExtension(realName);
    const safeIn = `job${jobCounter}_input${i}${ext}`;
    ffmpegLog("info", `receiving input #${recvIndex} => ${safeIn}, size=${fileSize}`);
    const fileData = await receiveBinary(socket, fileSize);
    await ffmpeg.writeFile(safeIn, fileData);
    socket.send(JSON.stringify({ type: "inputOk" }));
    ffmpegLog("info", `inputOk ${inputIndex}`);
    inputMap[recvIndex] = safeIn;
    safeArgs[recvIndex] = safeIn;
  }
  const outMap = {};
  for (let i = 0; i < ffargs.outputs.length; i++) {
    const outIndex = ffargs.outputs[i];
    if (outIndex >= 0 && outIndex < ffargs.args.length) {
      const origOut = ffargs.args[outIndex];
      const outExt = guessExtension(origOut);
      const safeOut = `job${jobCounter}_out${i}${outExt}`;
      outMap[outIndex] = safeOut;
      safeArgs[outIndex] = safeOut;
    }
  }
  ffmpeg.on("log", onLog);
  try {
    const callArgs = safeArgs.slice(1);
    if (isFfprobe) {
      ffmpegLog("info", "Running ffprobe with callArgs:", callArgs);
      await ffmpeg.ffprobe(callArgs);
      ffmpegLog("info", "ffprobe done");
    } else {
      ffmpegLog("info", "Running ffmpeg exec with callArgs:", callArgs);
      await ffmpeg.exec(callArgs);
      ffmpegLog("info", "ffmpeg exec done");
    }
    const logEnd = JSON.stringify({ type: "logEnd" });
    socket.send(logEnd);
    ffmpegLog("info", "logEnd");
    for (let i = 0; i < ffargs.outputs.length; i++) {
      const outIndex = ffargs.outputs[i];
      if (outIndex < 0 || outIndex >= ffargs.args.length) {
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
      const outData = await ffmpeg.readFile(safePath);
      ffmpegLog("info", `Output #${i}, original index ${outIndex}, size: ${outData.length} bytes`);
      const meta = JSON.stringify({ type: "outInfo", outInfo: [outIndex, outData.length] });
      socket.send(meta);
      socket.send(outData.buffer);
      ffmpegLog("info", "Sent output to server");
    }
  } finally {
    ffmpeg.off("log", onLog);
    for (const safeIn of Object.values(inputMap)) {
      try {
        ffmpeg.deleteFile(safeIn);
      } catch (e) {
      }
    }
    for (const safeOut of Object.values(outMap)) {
      try {
        ffmpeg.deleteFile(safeOut);
      } catch (e) {
      }
    }
  }
}
function guessExtension(filePath) {
  if (!filePath) return ".dat";
  const i = filePath.lastIndexOf(".");
  if (i < 0) return ".dat";
  return filePath.substring(i);
}
async function mainLoop(socket) {
  await cycleJobs(socket);
  ffmpegLog("info", "All jobs completed or 'nomore'.");
}
document.addEventListener("DOMContentLoaded", async () => {
  const wsProtocol = location.protocol === "https:" ? "wss://" : "ws://";
  const socketURL = wsProtocol + location.host + "/ws/ffmpeg";
  const socket = new WebSocket(socketURL);
  socket.addEventListener("open", async () => {
    ffmpegLog("info", "WebSocket open. Loading ffmpeg core...");
    ffmpegLog("info", "ffmpeg core loaded. Starting job cycle...");
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
    ffmpegLog("error", "WebSocket closed:", ev);
  });
  socket.addEventListener("error", (err) => {
    queue = [];
    ffmpegLog("error", "WebSocket error:", err);
  });
});
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
