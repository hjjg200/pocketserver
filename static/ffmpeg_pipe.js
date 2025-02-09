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

// node_modules/@ffmpeg/util/dist/esm/index.js
var readFromBlobOrFile = (blob) => new Promise((resolve, reject) => {
  const fileReader = new FileReader();
  fileReader.onload = () => {
    const { result } = fileReader;
    if (result instanceof ArrayBuffer) {
      resolve(new Uint8Array(result));
    } else {
      resolve(new Uint8Array());
    }
  };
  fileReader.onerror = (event) => {
    reject(Error(`File could not be read! Code=${event?.target?.error?.code || -1}`));
  };
  fileReader.readAsArrayBuffer(blob);
});
var fetchFile = async (file) => {
  let data;
  if (typeof file === "string") {
    if (/data:_data\/([a-zA-Z]*);base64,([^"]*)/.test(file)) {
      data = atob(file.split(",")[1]).split("").map((c) => c.charCodeAt(0));
    } else {
      data = await (await fetch(file)).arrayBuffer();
    }
  } else if (file instanceof URL) {
    data = await (await fetch(file)).arrayBuffer();
  } else if (file instanceof File || file instanceof Blob) {
    data = await readFromBlobOrFile(file);
  } else {
    return new Uint8Array();
  }
  return new Uint8Array(data);
};

// src/main.js
window.ffmpegRunCommands = async function(blob, cmds) {
  const ffmpeg = await newFFmpeg();
  const inputFileName = `input.dat`;
  await ffmpeg.writeFile(inputFileName, await fetchFile(blob));
  const ret = {};
  for (let cmd of cmds) {
    const exec = cmd.args[0] === "ffmpeg" ? ffmpeg.exec : ffmpeg.ffprobe;
    let args = cmd.args.slice();
    args[cmd.input] = inputFileName;
    args[cmd.output] = "output" + cmd.outputExt;
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
};
async function newFFmpeg() {
  const ffmpeg = new FFmpeg();
  await ffmpeg.load(
    /*mt ? */
    {
      coreURL: "/static/ffmpeg/mt-ffmpeg-core.js",
      wasmURL: "/static/ffmpeg/mt-ffmpeg-core.wasm",
      workerURL: "/static/ffmpeg/mt-ffmpeg-core.worker.js",
      classWorkerURL: "/static/ffmpeg/worker.js"
    }
    /*: {
      coreURL: "/static/ffmpeg/ffmpeg-core.js",
      wasmURL: "/static/ffmpeg/ffmpeg-core.wasm",
      classWorkerURL: "/static/ffmpeg/worker.js",
    }*/
  );
  return ffmpeg;
}
async function getTextMetadata(ffmpeg, inputFileName) {
  let stdout = "";
  const onLog = (evt) => {
    if (evt.type === "stdout") {
      stdout += evt.message + "\n";
    }
  };
  ffmpeg.on("log", onLog);
  await ffmpeg.ffprobe([
    "-i",
    inputFileName,
    "-show_format",
    "-show_entries",
    "format_tags=album,artist,title,comment:format=duration",
    "-print_format",
    "json",
    "-v",
    "quiet"
  ]);
  ffmpeg.off("log", onLog);
  let probeResult;
  try {
    probeResult = JSON.parse(stdout.trim());
  } catch (err) {
    throw new Error("ffprobe JSON parsing failed: " + err.message);
  }
  const tags = parseFloat(probeResult.format?.tags);
  const duration = parseFloat(probeResult.format?.duration);
  if (isNaN(duration)) {
  }
  return probeResult;
}
async function analyzeLoudnessPass1(ffmpeg, wavFileName, targetLUFS) {
  let jsonData = "";
  let collecting = false;
  const onLog = (evt) => {
    if (evt.type !== "stderr") return;
    if (evt.message.includes("[Parsed_loudnorm_")) {
      collecting = true;
      return;
    }
    if (collecting) {
      jsonData += evt.message;
      if (evt.message.trim().endsWith("}")) {
        collecting = false;
      }
    }
  };
  ffmpeg.on("log", onLog);
  await ffmpeg.exec([
    "-i",
    wavFileName,
    "-af",
    `loudnorm=I=${targetLUFS}:TP=-2.0:LRA=11:print_format=json`,
    "-f",
    "null",
    "-threads",
    "1",
    "-v",
    "info",
    "-"
  ]);
  ffmpeg.off("log", onLog);
  const trimmed = jsonData.trim();
  if (!trimmed) {
    throw new Error("No loudnorm JSON data from pass 1.");
  }
  return JSON.parse(trimmed);
}
async function correctLoudnessPass2(ffmpeg, wavFileName, outputFileName, analysis, targetLUFS) {
  if (typeof analysis.input_i === "undefined" || typeof analysis.input_tp === "undefined" || typeof analysis.input_lra === "undefined" || typeof analysis.input_thresh === "undefined") {
    throw new Error("Missing necessary loudnorm parameters from pass 1 analysis.");
  }
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
    "print_format=json"
  ].join(":");
  console.debug(pass2Filter);
  await ffmpeg.exec([
    "-i",
    wavFileName,
    "-af",
    pass2Filter,
    "-vn",
    "-c:a",
    "aac",
    "-q:a",
    "1",
    "-threads",
    "1",
    outputFileName
  ]);
}
async function copyMetadataAndCover(ffmpeg, inputFileName, correctedFileName, finalFileName) {
  const coverName = "cover.webp";
  await ffmpeg.exec([
    "-i",
    inputFileName,
    "-c:v",
    "libwebp",
    "-threads",
    "1",
    "-q:v",
    "80",
    "-pix_fmt",
    "yuv420p",
    "-an",
    coverName
  ]);
  const ls = await ffmpeg.listDir("/");
  const hasCover = ls.some((el) => el.name === coverName);
  await ffmpeg.exec([
    // 1) The audio file with corrected loudness
    "-i",
    correctedFileName,
    // 2) The original file that has metadata and cover
    "-i",
    inputFileName,
    // Handle album art only when the input had one
    ...hasCover ? [
      "-i",
      coverName,
      // Cover art (video stream #0) from the separate file, if present. 
      "-map",
      "2:v:0",
      // Mark it as attached cover
      "-disposition:v:0",
      "attached_pic",
      // Re-encode the cover to MJPEG
      "-c:v",
      "mjpeg"
    ] : [
      "-vn"
    ],
    // Audio from the corrected file
    "-map",
    "0:a",
    // Copy (passthrough) the audio from the corrected file
    "-c:a",
    "copy",
    // Copy all metadata from input #1 (original)
    "-map_metadata",
    "1",
    // Copy metadata from the first subtitle stream
    "-map_metadata",
    "1:s:0",
    // Ensure ID3v2 version is set correctly
    "-id3v2_version",
    "3",
    // Prevent chrome freeze when embedding album art
    "-threads",
    "1",
    //
    "-f",
    "mp4",
    //
    "-movflags",
    "+faststart",
    finalFileName
  ]);
}
window.ffmpegSoundCheck = new ProgressTask(async function(inputFile, targetLUFS = -14) {
  this.add(7);
  const ffmpeg = await newFFmpeg();
  if (gDEBUG) {
    ffmpeg.on("log", (event) => {
      console[event.type === "stderr" ? "warn" : "log"](event.message);
    });
  }
  try {
    const [inputBase, inputStem, inputExt] = parseFilename(inputFile.name);
    const inputFileName = `input${inputExt}`;
    const tempCorrectedFile = "tempCorrected.m4a";
    const finalOutputFile = "finalOutput.m4a";
    this.done("Writing input file to FS...");
    await ffmpeg.writeFile(inputFileName, await fetchFile(inputFile));
    this.done("Analyzing loudness (pass 1)...");
    const analysis = await analyzeLoudnessPass1(ffmpeg, inputFileName, targetLUFS);
    this.done("Pass 1 analysis:", analysis);
    this.done("Correcting loudness (pass 2)...");
    await correctLoudnessPass2(ffmpeg, inputFileName, tempCorrectedFile, analysis, targetLUFS);
    this.done("Merging metadata & cover art...");
    await copyMetadataAndCover(ffmpeg, inputFileName, tempCorrectedFile, finalOutputFile);
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
  const [cat, sub] = contentType.split("/");
  if (false === (cat === "audio" || cat === "video" || sub === "webp"))
    return metadata;
  const ffmpeg = await newFFmpeg();
  try {
    const inputFile = await fetchFile(new Blob([buf], { type: contentType }));
    const inputFileName = `input${guessExtension(src)}`;
    await ffmpeg.writeFile(inputFileName, inputFile);
    metadata[".json"] = await getTextMetadata(ffmpeg, inputFileName);
    if (isNaN(metadata[".json"].duration)) {
      metadata[".json"].duration = "N/A";
      return metadata;
    }
    const baseArgs = [
      "-i",
      inputFileName,
      "-c:v",
      "libwebp",
      "-threads",
      "1",
      "-q:v",
      "80",
      "-pix_fmt",
      "yuv420p",
      "-an"
    ];
    if (cat === "video") {
      await ffmpeg.exec([
        ...baseArgs,
        "-ss",
        "00:00:01",
        "-vframes",
        "1",
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
        "-vf",
        "'scale=iw*sqrt(16384/(iw*ih)):-1'",
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
};
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
async function cycleJobs(socket, signal) {
  try {
    while (true) {
      await pongBackMessageOfType(socket, "ready");
      await pongBackMessageOfType(socket, "taskReady");
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
        console.error(err);
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
async function flow(ffmpeg, ffargs, socket) {
  jobCounter++;
  ffmpegLog("info", `Job ${jobCounter}`);
  console.log(`Job ${jobCounter}`);
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
async function mainLoop() {
  while (true) {
    const wsProtocol = location.protocol === "https:" ? "wss://" : "ws://";
    const socketURL = wsProtocol + location.host + "/ws/ffmpeg";
    const socket = new WebSocket(socketURL);
    const controller = new AbortController();
    const { signal } = controller;
    let messageQueue = [];
    let pairQueue = [];
    let promise0, resolver0;
    let promise1;
    promise0 = new Promise((resolve) => resolver0 = resolve);
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
    await new Promise((resolve) => setTimeout(resolve, 5e3));
  }
}
document.addEventListener("DOMContentLoaded", async () => {
  mainLoop();
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
