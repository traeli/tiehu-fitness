const crypto = require("node:crypto");
const fs = require("node:fs");
const fsp = require("node:fs/promises");
const path = require("node:path");
const { spawn } = require("node:child_process");
const { Readable } = require("node:stream");
const { pipeline } = require("node:stream/promises");

const MODEL_REVISION = "98aa99a0a9db05ae2342309f5096248665f7cba3";
const LOCAL_TRANSCRIPTION_MODELS = Object.freeze([
  Object.freeze({ id: "tiny-q5_1", fileName: "ggml-tiny-q5_1.bin", name: "Whisper Tiny Q5", description: "速度最快，适合较清晰的短会议", sizeBytes: 32_152_673, sha256: "818710568da3ca15689e31a743197b520007872ff9576237bda97bd1b469c3d7", recommended: false }),
  Object.freeze({ id: "base-q5_1", fileName: "ggml-base-q5_1.bin", name: "Whisper Base Q5", description: "速度与识别效果的轻量平衡", sizeBytes: 59_707_625, sha256: "422f1ae452ade6f30a004d7e5c6a43195e4433bc370bf23fac9cc591f01a8898", recommended: false }),
  Object.freeze({ id: "small-q5_1", fileName: "ggml-small-q5_1.bin", name: "Whisper Small Q5", description: "中文会议效果更均衡，推荐使用", sizeBytes: 190_085_487, sha256: "ae85e4a935d7a567bd102fe55afc16bb595bdb618e11b2fc7591bc08120411bb", recommended: true }),
  Object.freeze({ id: "medium-q5_0", fileName: "ggml-medium-q5_0.bin", name: "Whisper Medium Q5", description: "准确率更高，需要更多内存与处理时间", sizeBytes: 539_212_467, sha256: "19fea4b380c3a618ec4723c3eef2eb785ffba0d0538cf43f8f235e7b3b34220f", recommended: false }),
]);
const MAX_PCM_BYTES = 30 * 16000 * 2;

class LocalTranscriptionProcess {
  constructor({ runtimeDirectory, modelDirectory }) {
    this.runtimeDirectory = runtimeDirectory;
    this.modelDirectory = modelDirectory;
    this.child = undefined;
    this.stdoutBuffer = Buffer.alloc(0);
    this.pending = [];
    this.startPromise = undefined;
    this.runningModelId = undefined;
  }

  get binaryPath() {
    return path.join(
      this.runtimeDirectory,
      process.platform === "win32" ? "tiehu-local-whisper.exe" : "tiehu-local-whisper",
    );
  }

  async status(modelId) {
    const selectedModel = resolveModel(modelId);
    const [binaryAvailable, models] = await Promise.all([
      isFile(this.binaryPath),
      Promise.all(LOCAL_TRANSCRIPTION_MODELS.map(async (model) => ({
        id: model.id,
        name: model.name,
        description: model.description,
        sizeBytes: model.sizeBytes,
        recommended: model.recommended,
        downloaded: await isValidModelFile(this.#modelPath(model), model.sizeBytes),
      }))),
    ]);
    const selectedStatus = models.find((model) => model.id === selectedModel.id);
    if (!selectedStatus) throw new Error("本地语音模型状态异常");
    return {
      supported: new Set(["darwin", "win32"]).has(process.platform),
      binaryAvailable,
      modelDownloaded: selectedStatus.downloaded,
      running: Boolean(this.child),
      selectedModelId: selectedModel.id,
      modelName: selectedModel.name,
      modelSizeBytes: selectedModel.sizeBytes,
      models,
    };
  }

  async downloadModel(modelId, onProgress) {
    const model = resolveModel(modelId);
    const modelPath = this.#modelPath(model);
    if (await isValidModelFile(modelPath, model.sizeBytes)) {
      onProgress?.({ phase: "verifying", receivedBytes: model.sizeBytes, totalBytes: model.sizeBytes });
      return this.status(model.id);
    }
    await fsp.mkdir(this.modelDirectory, { recursive: true });
    const temporaryPath = `${modelPath}.${process.pid}.download`;
    await fsp.rm(temporaryPath, { force: true });
    try {
      const modelURL = `https://huggingface.co/ggerganov/whisper.cpp/resolve/${MODEL_REVISION}/${model.fileName}`;
      const response = await fetch(modelURL, { redirect: "follow" });
      if (!response.ok || !response.body) {
        throw new Error(`模型下载失败（HTTP ${response.status}）`);
      }
      const totalBytes = Number(response.headers.get("content-length")) || model.sizeBytes;
      let receivedBytes = 0;
      const hash = crypto.createHash("sha256");
      const source = webResponseBodyToNodeStream(response.body);
      source.on("data", (chunk) => {
        receivedBytes += chunk.length;
        hash.update(chunk);
        onProgress?.({ phase: "downloading", receivedBytes, totalBytes });
      });
      await pipeline(source, fs.createWriteStream(temporaryPath, { flags: "wx" }));
      onProgress?.({ phase: "verifying", receivedBytes, totalBytes });
      if (receivedBytes !== model.sizeBytes || hash.digest("hex") !== model.sha256) {
        throw new Error("模型文件校验失败，请重新下载");
      }
      await fsp.rename(temporaryPath, modelPath);
      return this.status(model.id);
    } catch (error) {
      await fsp.rm(temporaryPath, { force: true }).catch(() => undefined);
      throw error;
    }
  }

  async start(modelId) {
    const model = resolveModel(modelId);
    if (this.child) {
      if (this.runningModelId === model.id) return;
      throw new Error("录制进行中不能切换语音模型");
    }
    if (this.startPromise) return this.startPromise;
    this.startPromise = this.#start(model).finally(() => {
      this.startPromise = undefined;
    });
    return this.startPromise;
  }

  async #start(model) {
    const status = await this.status(model.id);
    if (!status.binaryAvailable) {
      throw new Error("本地转写组件未安装，请重新安装包含本地引擎的插件版本");
    }
    if (!status.modelDownloaded) {
      throw new Error("请先下载本地语音模型");
    }
    const child = spawn(this.binaryPath, [this.#modelPath(model)], {
      cwd: this.runtimeDirectory,
      stdio: ["pipe", "pipe", "pipe"],
      windowsHide: true,
      env: {
        ...process.env,
        DYLD_FRAMEWORK_PATH: process.platform === "darwin" ? this.runtimeDirectory : undefined,
      },
    });
    this.child = child;
    this.runningModelId = model.id;
    child.stdout.on("data", (chunk) => this.#handleStdout(chunk));
    child.stderr.on("data", (chunk) => console.error("local whisper", chunk.toString("utf8").trim()));
    child.once("exit", (code, signal) => this.#handleExit(code, signal));
    child.once("error", (error) => this.#handleExit(undefined, undefined, error));
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("本地语音模型加载超时")), 60_000);
      this.ready = () => {
        clearTimeout(timer);
        resolve();
      };
      this.startReject = (error) => {
        clearTimeout(timer);
        reject(error);
      };
    }).catch((error) => {
      this.stop();
      throw error;
    });
  }

  transcribe(pcmData) {
    if (!this.child?.stdin.writable) {
      return Promise.reject(new Error("本地转写引擎未启动"));
    }
    if (!(pcmData instanceof ArrayBuffer) || pcmData.byteLength <= 0 || pcmData.byteLength > MAX_PCM_BYTES || pcmData.byteLength % 2 !== 0) {
      return Promise.reject(new TypeError("本地转写 PCM 数据无效"));
    }
    return new Promise((resolve, reject) => {
      this.pending.push({ resolve, reject });
      const header = Buffer.allocUnsafe(4);
      header.writeUInt32LE(pcmData.byteLength, 0);
      this.child.stdin.write(header);
      this.child.stdin.write(Buffer.from(pcmData));
    });
  }

  stop() {
    const child = this.child;
    this.child = undefined;
    this.runningModelId = undefined;
    if (child) {
      child.stdin.end();
      const killTimer = setTimeout(() => child.kill(), 2_000);
      if (typeof killTimer?.unref === "function") {
        killTimer.unref();
      }
    }
    this.#rejectPending(new Error("本地转写已停止"));
  }

  #handleStdout(chunk) {
    this.stdoutBuffer = Buffer.concat([this.stdoutBuffer, chunk]);
    if (this.ready) {
      const readyIndex = this.stdoutBuffer.indexOf("READY\n");
      if (readyIndex < 0) return;
      this.stdoutBuffer = this.stdoutBuffer.subarray(readyIndex + 6);
      const ready = this.ready;
      this.ready = undefined;
      this.startReject = undefined;
      ready();
    }
    while (this.stdoutBuffer.length >= 4 && this.pending.length > 0) {
      const status = this.stdoutBuffer.readUInt32LE(0);
      if (status === 1) {
        if (this.stdoutBuffer.length < 8) return;
        const length = this.stdoutBuffer.readUInt32LE(4);
        if (this.stdoutBuffer.length < 8 + length) return;
        const message = this.stdoutBuffer.subarray(8, 8 + length).toString("utf8");
        this.stdoutBuffer = this.stdoutBuffer.subarray(8 + length);
        this.pending.shift().reject(new Error(message));
        continue;
      }
      if (this.stdoutBuffer.length < 8) return;
      const count = this.stdoutBuffer.readUInt32LE(4);
      let offset = 8;
      const segments = [];
      for (let index = 0; index < count; index += 1) {
        if (this.stdoutBuffer.length < offset + 12) return;
        const startMs = this.stdoutBuffer.readUInt32LE(offset);
        const endMs = this.stdoutBuffer.readUInt32LE(offset + 4);
        const textLength = this.stdoutBuffer.readUInt32LE(offset + 8);
        if (this.stdoutBuffer.length < offset + 12 + textLength) return;
        segments.push({ startMs, endMs, text: this.stdoutBuffer.subarray(offset + 12, offset + 12 + textLength).toString("utf8").trim() });
        offset += 12 + textLength;
      }
      this.stdoutBuffer = this.stdoutBuffer.subarray(offset);
      this.pending.shift().resolve(segments.filter((segment) => segment.text));
    }
  }

  #handleExit(code, signal, error) {
    this.child = undefined;
    this.runningModelId = undefined;
    const failure = error ?? new Error(`本地转写进程已退出（${code ?? signal ?? "unknown"}）`);
    this.startReject?.(failure);
    this.ready = undefined;
    this.startReject = undefined;
    this.#rejectPending(failure);
  }

  #rejectPending(error) {
    for (const request of this.pending.splice(0)) request.reject(error);
  }

  #modelPath(model) {
    return path.join(this.modelDirectory, model.fileName);
  }
}

async function isFile(filePath) {
  try {
    return (await fsp.stat(filePath)).isFile();
  } catch (error) {
    if (error?.code === "ENOENT") return false;
    throw error;
  }
}

function webResponseBodyToNodeStream(body) {
  if (typeof Readable.fromWeb === "function") {
    return Readable.fromWeb(body);
  }
  if (typeof body?.getReader !== "function") {
    throw new Error("当前 uTools 版本不支持读取模型下载响应，请升级 uTools 后重试");
  }
  const reader = body.getReader();
  return Readable.from((async function* readWebStream() {
    try {
      while (true) {
        const result = await reader.read();
        if (result.done) return;
        if (result.value?.byteLength > 0) {
          yield Buffer.from(result.value.buffer, result.value.byteOffset, result.value.byteLength);
        }
      }
    } finally {
      reader.releaseLock?.();
    }
  })());
}

async function isValidModelFile(filePath, expectedSizeBytes) {
  try {
    const stat = await fsp.stat(filePath);
    return stat.isFile() && stat.size === expectedSizeBytes;
  } catch (error) {
    if (error?.code === "ENOENT") return false;
    throw error;
  }
}

function resolveModel(modelId) {
  const model = LOCAL_TRANSCRIPTION_MODELS.find((candidate) => candidate.id === modelId);
  if (!model) throw new TypeError("未知的本地语音模型");
  return model;
}

module.exports = { LOCAL_TRANSCRIPTION_MODELS, LocalTranscriptionProcess };
