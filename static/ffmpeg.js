var __defProp = Object.defineProperty;
var __typeError = (msg) => {
  throw TypeError(msg);
};
var __defNormalProp = (obj, key, value) => key in obj ? __defProp(obj, key, { enumerable: true, configurable: true, writable: true, value }) : obj[key] = value;
var __publicField = (obj, key, value) => __defNormalProp(obj, typeof key !== "symbol" ? key + "" : key, value);
var __accessCheck = (obj, member, msg) => member.has(obj) || __typeError("Cannot " + msg);
var __privateGet = (obj, member, getter) => (__accessCheck(obj, member, "read from private field"), getter ? getter.call(obj) : member.get(obj));
var __privateAdd = (obj, member, value) => member.has(obj) ? __typeError("Cannot add the same private member more than once") : member instanceof WeakSet ? member.add(obj) : member.set(obj, value);
var __privateSet = (obj, member, value, setter) => (__accessCheck(obj, member, "write to private field"), setter ? setter.call(obj, value) : member.set(obj, value), value);

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
var _worker, _resolves, _rejects, _logEventCallbacks, _progressEventCallbacks, _registerHandlers, _send;
var FFmpeg = class {
  constructor() {
    __privateAdd(this, _worker, null);
    /**
     * #resolves and #rejects tracks Promise resolves and rejects to
     * be called when we receive message from web worker.
     */
    __privateAdd(this, _resolves, {});
    __privateAdd(this, _rejects, {});
    __privateAdd(this, _logEventCallbacks, []);
    __privateAdd(this, _progressEventCallbacks, []);
    __publicField(this, "loaded", false);
    /**
     * register worker message event handlers.
     */
    __privateAdd(this, _registerHandlers, () => {
      if (__privateGet(this, _worker)) {
        __privateGet(this, _worker).onmessage = ({ data: { id, type, data } }) => {
          switch (type) {
            case FFMessageType.LOAD:
              this.loaded = true;
              __privateGet(this, _resolves)[id](data);
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
              __privateGet(this, _resolves)[id](data);
              break;
            case FFMessageType.LOG:
              __privateGet(this, _logEventCallbacks).forEach((f) => f(data));
              break;
            case FFMessageType.PROGRESS:
              __privateGet(this, _progressEventCallbacks).forEach((f) => f(data));
              break;
            case FFMessageType.ERROR:
              __privateGet(this, _rejects)[id](data);
              break;
          }
          delete __privateGet(this, _resolves)[id];
          delete __privateGet(this, _rejects)[id];
        };
      }
    });
    /**
     * Generic function to send messages to web worker.
     */
    __privateAdd(this, _send, ({ type, data }, trans = [], signal) => {
      if (!__privateGet(this, _worker)) {
        return Promise.reject(ERROR_NOT_LOADED);
      }
      return new Promise((resolve, reject) => {
        const id = getMessageID();
        __privateGet(this, _worker) && __privateGet(this, _worker).postMessage({ id, type, data }, trans);
        __privateGet(this, _resolves)[id] = resolve;
        __privateGet(this, _rejects)[id] = reject;
        signal == null ? void 0 : signal.addEventListener("abort", () => {
          reject(new DOMException(`Message # ${id} was aborted`, "AbortError"));
        }, { once: true });
      });
    });
    /**
     * Loads ffmpeg-core inside web worker. It is required to call this method first
     * as it initializes WebAssembly and other essential variables.
     *
     * @category FFmpeg
     * @returns `true` if ffmpeg core is loaded for the first time.
     */
    __publicField(this, "load", ({ classWorkerURL, ...config } = {}, { signal } = {}) => {
      if (!__privateGet(this, _worker)) {
        __privateSet(this, _worker, classWorkerURL ? new Worker(new URL(classWorkerURL, import.meta.url), {
          type: "module"
        }) : (
          // We need to duplicated the code here to enable webpack
          // to bundle worekr.js here.
          new Worker(new URL("./worker.js", import.meta.url), {
            type: "module"
          })
        ));
        __privateGet(this, _registerHandlers).call(this);
      }
      return __privateGet(this, _send).call(this, {
        type: FFMessageType.LOAD,
        data: config
      }, void 0, signal);
    });
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
    __publicField(this, "exec", (args, timeout = -1, { signal } = {}) => __privateGet(this, _send).call(this, {
      type: FFMessageType.EXEC,
      data: { args, timeout }
    }, void 0, signal));
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
    __publicField(this, "ffprobe", (args, timeout = -1, { signal } = {}) => __privateGet(this, _send).call(this, {
      type: FFMessageType.FFPROBE,
      data: { args, timeout }
    }, void 0, signal));
    /**
     * Terminate all ongoing API calls and terminate web worker.
     * `FFmpeg.load()` must be called again before calling any other APIs.
     *
     * @category FFmpeg
     */
    __publicField(this, "terminate", () => {
      const ids = Object.keys(__privateGet(this, _rejects));
      for (const id of ids) {
        __privateGet(this, _rejects)[id](ERROR_TERMINATED);
        delete __privateGet(this, _rejects)[id];
        delete __privateGet(this, _resolves)[id];
      }
      if (__privateGet(this, _worker)) {
        __privateGet(this, _worker).terminate();
        __privateSet(this, _worker, null);
        this.loaded = false;
      }
    });
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
    __publicField(this, "writeFile", (path, data, { signal } = {}) => {
      const trans = [];
      if (data instanceof Uint8Array) {
        trans.push(data.buffer);
      }
      return __privateGet(this, _send).call(this, {
        type: FFMessageType.WRITE_FILE,
        data: { path, data }
      }, trans, signal);
    });
    __publicField(this, "mount", (fsType, options, mountPoint) => {
      const trans = [];
      return __privateGet(this, _send).call(this, {
        type: FFMessageType.MOUNT,
        data: { fsType, options, mountPoint }
      }, trans);
    });
    __publicField(this, "unmount", (mountPoint) => {
      const trans = [];
      return __privateGet(this, _send).call(this, {
        type: FFMessageType.UNMOUNT,
        data: { mountPoint }
      }, trans);
    });
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
    __publicField(this, "readFile", (path, encoding = "binary", { signal } = {}) => __privateGet(this, _send).call(this, {
      type: FFMessageType.READ_FILE,
      data: { path, encoding }
    }, void 0, signal));
    /**
     * Delete a file.
     *
     * @category File System
     */
    __publicField(this, "deleteFile", (path, { signal } = {}) => __privateGet(this, _send).call(this, {
      type: FFMessageType.DELETE_FILE,
      data: { path }
    }, void 0, signal));
    /**
     * Rename a file or directory.
     *
     * @category File System
     */
    __publicField(this, "rename", (oldPath, newPath, { signal } = {}) => __privateGet(this, _send).call(this, {
      type: FFMessageType.RENAME,
      data: { oldPath, newPath }
    }, void 0, signal));
    /**
     * Create a directory.
     *
     * @category File System
     */
    __publicField(this, "createDir", (path, { signal } = {}) => __privateGet(this, _send).call(this, {
      type: FFMessageType.CREATE_DIR,
      data: { path }
    }, void 0, signal));
    /**
     * List directory contents.
     *
     * @category File System
     */
    __publicField(this, "listDir", (path, { signal } = {}) => __privateGet(this, _send).call(this, {
      type: FFMessageType.LIST_DIR,
      data: { path }
    }, void 0, signal));
    /**
     * Delete an empty directory.
     *
     * @category File System
     */
    __publicField(this, "deleteDir", (path, { signal } = {}) => __privateGet(this, _send).call(this, {
      type: FFMessageType.DELETE_DIR,
      data: { path }
    }, void 0, signal));
  }
  on(event, callback) {
    if (event === "log") {
      __privateGet(this, _logEventCallbacks).push(callback);
    } else if (event === "progress") {
      __privateGet(this, _progressEventCallbacks).push(callback);
    }
  }
  off(event, callback) {
    if (event === "log") {
      __privateSet(this, _logEventCallbacks, __privateGet(this, _logEventCallbacks).filter((f) => f !== callback));
    } else if (event === "progress") {
      __privateSet(this, _progressEventCallbacks, __privateGet(this, _progressEventCallbacks).filter((f) => f !== callback));
    }
  }
};
_worker = new WeakMap();
_resolves = new WeakMap();
_rejects = new WeakMap();
_logEventCallbacks = new WeakMap();
_progressEventCallbacks = new WeakMap();
_registerHandlers = new WeakMap();
_send = new WeakMap();

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
var ffmpeg = new FFmpeg();
var jobCounter = 0;
ffmpeg.on("progress", (prog) => {
  console.log(`[FFmpeg progress] frame=${prog.frame}, fps=${prog.fps}, time=${prog.time}`);
});
async function cycleJobs(socket) {
  try {
    while (true) {
      socket.send("ready");
      console.log("[FFmpeg] Sent 'ready' \u2013 waiting for new ffargs or 'nomore'...");
      const msg = await waitForTextMessage(socket);
      const line = msg.trim();
      if (line === "nomore") {
        console.log("[FFmpeg] No more jobs from server, stopping cycle.");
        break;
      }
      const ffargs = JSON.parse(line);
      await flow(ffargs, socket);
      console.log("[FFmpeg] Job done. Going for next job...");
    }
    console.log("[FFmpeg] cycleJobs ended \u2013 no more tasks.");
  } catch (err) {
    console.error("[FFmpeg] cycleJobs error:", err);
  }
}
async function flow(ffargs, socket) {
  jobCounter++;
  const jobLogs = [];
  const onLog = (entry) => {
    const logLine = entry.message;
    jobLogs.push(logLine);
    socket.send(JSON.stringify({ type: "logLine", logLine }));
  };
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
  ffmpeg.on("log", onLog);
  let outSafe = "";
  let hadOutput = false;
  try {
    let isFfprobe = false;
    if (ffargs.args[0].endsWith("ffprobe")) {
      isFfprobe = true;
    }
    for (const idx of ffargs.inputs) {
      const safeName = inputMap[idx];
      if (!safeName) continue;
      const origName = ffargs.args[idx];
      for (let r = 0; r < ffargs.args.length; r++) {
        if (ffargs.args[r] === origName) {
          ffargs.args[r] = safeName;
        }
      }
    }
    if (ffargs.output >= 0 && ffargs.output < ffargs.args.length) {
      const origOut = ffargs.args[ffargs.output];
      const outExt = guessExtension(origOut);
      outSafe = `job${jobCounter}_out${outExt}`;
      for (let r = 0; r < ffargs.args.length; r++) {
        if (ffargs.args[r] === origOut) {
          ffargs.args[r] = outSafe;
        }
      }
    }
    const callArgs = skipFirstIfNeeded(ffargs.args);
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
    if (outSafe) {
      const outData = await ffmpeg.readFile(outSafe);
      hadOutput = true;
      console.log(`[FFmpeg] Output size: ${outData.length} bytes`);
      const meta = JSON.stringify({ type: "outInfo", outInfo: [ffargs.output, outData.length] });
      socket.send(meta);
      socket.send(outData.buffer);
      console.log("[FFmpeg] Sent output to server");
    } else {
      socket.send(JSON.stringify({ type: "outInfo", outInfo: [-1, 0] }));
      console.log("[FFmpeg] No output. Sent 0 bytes info.");
    }
  } finally {
    for (const safeName of Object.values(inputMap)) {
      try {
        ffmpeg.FS("unlink", safeName);
      } catch (e) {
      }
    }
    if (outSafe && hadOutput) {
      try {
        ffmpeg.FS("unlink", outSafe);
      } catch (e) {
      }
    }
  }
}
function skipFirstIfNeeded(array) {
  if (array.length > 0) {
    const first = array[0];
    if (first.endsWith("ffmpeg") || first.endsWith("ffprobe")) {
      return array.slice(1);
    }
  }
  return array;
}
function guessExtension(filePath) {
  const i = filePath.lastIndexOf(".");
  if (i < 0) return ".dat";
  return filePath.substring(i);
}
async function mainLoop(socket) {
  await cycleJobs(socket);
  console.log("[FFmpeg] All jobs completed or 'nomore'.");
}
document.addEventListener("DOMContentLoaded", async () => {
  const wsProtocol = location.protocol === "https:" ? "wss://" : "ws://";
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
    const onErr = (err) => {
      cleanup();
      reject(err);
    };
    const onClose = () => {
      cleanup();
      reject(new Error("[FFmpeg] socket closed (text)"));
    };
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
    const onErr = (err) => {
      cleanup();
      reject(err);
    };
    const onClose = () => {
      cleanup();
      reject(new Error("[FFmpeg] socket closed (binary)"));
    };
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
