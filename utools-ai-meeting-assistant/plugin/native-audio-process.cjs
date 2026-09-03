const fs = require("node:fs");
const path = require("node:path");
const { spawn } = require("node:child_process");
const { createHash, randomBytes } = require("node:crypto");

const {
  NativeAudioFrameDecoder,
  nativeAudioFrameTypes,
} = require("./native-audio-protocol.cjs");

const startTimeoutMs = 15_000;
const stopTimeoutMs = 2_000;
const maxStderrBytes = 16 * 1024;
const expectedSampleRate = 48_000;
const expectedChannels = 1;
const expectedFormat = "pcm_s16le";
const maxNativeHelperBytes = 32 * 1024 * 1024;

class NativeSystemAudioError extends Error {
  constructor(code, message) {
    super(message);
    this.name = "NativeSystemAudioError";
    this.code = code;
  }
}

class NativeSystemAudioProcess {
  #child;
  #runtimeDirectory;
  #state = "idle";
  #stderr = "";
  #startTimer;
  #stopTimer;
  #startResolve;
  #startReject;
  #stopResolve;
  #onAudio;
  #onError;

  constructor({ runtimeDirectory } = {}) {
    if (runtimeDirectory !== undefined && typeof runtimeDirectory !== "string") {
      throw new TypeError("Native system audio runtime directory must be a string");
    }
    this.#runtimeDirectory = runtimeDirectory;
  }

  get isRunning() {
    return this.#state !== "idle";
  }

  async start({ onAudio, onError }) {
    if (this.isRunning) {
      throw new NativeSystemAudioError("SYSTEM_AUDIO_ALREADY_RUNNING", "系统音频采集已经启动");
    }
    if (typeof onAudio !== "function" || typeof onError !== "function") {
      throw new TypeError("Native system audio callbacks must be functions");
    }
    this.#state = "starting";
    this.#stderr = "";
    this.#onAudio = onAudio;
    this.#onError = onError;
    let executablePath;
    try {
      executablePath = await prepareNativeAudioExecutable(
        __dirname,
        this.#runtimeDirectory,
        process.platform,
        process.arch,
      );
    } catch (error) {
      this.#state = "idle";
      this.#resetCallbacks();
      if (error instanceof NativeSystemAudioError) {
        throw error;
      }
      throw new NativeSystemAudioError(
        "SYSTEM_AUDIO_HELPER_INSTALL_FAILED",
        `无法准备系统音频组件：${error instanceof Error ? error.message : String(error)}`,
      );
    }
    const decoder = new NativeAudioFrameDecoder();
    const child = spawn(executablePath, [], {
      cwd: path.dirname(executablePath),
      env: { ...process.env, LANG: "C" },
      shell: false,
      stdio: ["pipe", "pipe", "pipe"],
      windowsHide: true,
    });
    this.#child = child;

    child.stdout.on("data", (chunk) => {
      try {
        for (const frame of decoder.push(chunk)) {
          this.#handleFrame(frame);
        }
      } catch (error) {
        this.#fail(normalizeNativeAudioError(error));
        this.#terminateChild();
      }
    });
    child.stderr.on("data", (chunk) => {
      if (this.#stderr.length >= maxStderrBytes) {
        return;
      }
      this.#stderr += chunk.toString("utf8", 0, maxStderrBytes - this.#stderr.length);
    });
    child.stdin.on("error", (error) => {
      if (this.#state !== "stopping" && this.#state !== "idle") {
        this.#fail(new NativeSystemAudioError(
          "SYSTEM_AUDIO_HELPER_IO_FAILED",
          `系统音频组件控制通道异常：${error.message}`,
        ));
        this.#terminateChild();
      }
    });
    child.once("error", (error) => {
      console.error("native system audio helper spawn failed", {
        platform: process.platform,
        arch: process.arch,
        executablePath,
        cwd: path.dirname(executablePath),
        code: typeof error?.code === "string" ? error.code : "UNKNOWN",
      });
      this.#fail(new NativeSystemAudioError(
        "SYSTEM_AUDIO_HELPER_START_FAILED",
        `无法启动系统音频组件：${error.message}`,
      ));
    });
    child.once("close", (code, signal) => this.#handleExit(code, signal));

    return new Promise((resolve, reject) => {
      this.#startResolve = resolve;
      this.#startReject = reject;
      this.#startTimer = setTimeout(() => {
        this.#fail(new NativeSystemAudioError(
          "SYSTEM_AUDIO_START_TIMEOUT",
          "系统音频组件启动超时，请检查录屏权限后重试",
        ));
        this.#terminateChild();
      }, startTimeoutMs);
    });
  }

  async stop() {
    if (this.#state === "idle") {
      return;
    }
    if (this.#state === "stopping") {
      return new Promise((resolve) => {
        const previousResolve = this.#stopResolve;
        this.#stopResolve = () => {
          previousResolve?.();
          resolve();
        };
      });
    }
    this.#state = "stopping";
    clearTimeout(this.#startTimer);
    return new Promise((resolve) => {
      this.#stopResolve = resolve;
      this.#stopTimer = setTimeout(() => {
        this.#terminateChild();
      }, stopTimeoutMs);
      this.#child?.stdin.end("stop\n");
    });
  }

  #handleFrame(frame) {
    switch (frame.type) {
      case nativeAudioFrameTypes.READY: {
        if (this.#state !== "starting") {
          throw new NativeSystemAudioError("SYSTEM_AUDIO_PROTOCOL_INVALID", "系统音频组件重复发送就绪消息");
        }
        const ready = parseJSONPayload(frame.payload, "ready");
        if (
          ready.sampleRate !== expectedSampleRate ||
          ready.channels !== expectedChannels ||
          ready.format !== expectedFormat
        ) {
          throw new NativeSystemAudioError("SYSTEM_AUDIO_FORMAT_INVALID", "系统音频组件返回了不支持的音频格式");
        }
        clearTimeout(this.#startTimer);
        this.#state = "running";
        const resolve = this.#startResolve;
        this.#startResolve = undefined;
        this.#startReject = undefined;
        resolve?.(ready);
        break;
      }
      case nativeAudioFrameTypes.AUDIO: {
        if (this.#state !== "running" || frame.payload.length === 0 || frame.payload.length % 2 !== 0) {
          throw new NativeSystemAudioError("SYSTEM_AUDIO_PROTOCOL_INVALID", "系统音频组件返回了无效 PCM 分片");
        }
        const copy = Uint8Array.from(frame.payload);
        this.#onAudio?.(copy.buffer);
        break;
      }
      case nativeAudioFrameTypes.ERROR: {
        const failure = parseJSONPayload(frame.payload, "error");
        const code = typeof failure.code === "string" ? failure.code : "SYSTEM_AUDIO_NATIVE_FAILED";
        const message = typeof failure.message === "string" ? failure.message : "系统音频采集失败";
        this.#fail(new NativeSystemAudioError(code, message));
        this.#terminateChild();
        break;
      }
      default:
        throw new NativeSystemAudioError("SYSTEM_AUDIO_PROTOCOL_INVALID", "系统音频组件返回了未知消息");
    }
  }

  #handleExit(code, signal) {
    clearTimeout(this.#startTimer);
    clearTimeout(this.#stopTimer);
    const previousState = this.#state;
    const stderr = sanitizeStderr(this.#stderr);
    if (previousState === "starting" || previousState === "running") {
      const detail = stderr ? `：${stderr}` : "";
      this.#fail(new NativeSystemAudioError(
        "SYSTEM_AUDIO_HELPER_EXITED",
        `系统音频组件异常退出（code=${String(code)}, signal=${String(signal)}）${detail}`,
      ));
    }
    this.#child = undefined;
    this.#state = "idle";
    this.#resetCallbacks();
    this.#stopResolve?.();
    this.#stopResolve = undefined;
  }

  #fail(error) {
    clearTimeout(this.#startTimer);
    if (this.#state === "starting") {
      this.#startReject?.(error);
      this.#startResolve = undefined;
      this.#startReject = undefined;
    } else if (this.#state === "running") {
      this.#onError?.(error);
    }
    if (this.#state !== "stopping" && this.#state !== "idle") {
      this.#state = "failed";
    }
  }

  #terminateChild() {
    if (this.#child && !this.#child.killed) {
      this.#child.kill();
    }
  }

  #resetCallbacks() {
    this.#onAudio = undefined;
    this.#onError = undefined;
    this.#startResolve = undefined;
    this.#startReject = undefined;
  }
}

function resolveNativeAudioExecutable(baseDirectory, platform, arch) {
  const supported = new Set(["darwin-arm64", "darwin-x64", "win32-x64", "win32-arm64"]);
  const target = `${platform}-${arch}`;
  if (!supported.has(target)) {
    throw new NativeSystemAudioError(
      "SYSTEM_AUDIO_PLATFORM_UNSUPPORTED",
      `当前平台暂不支持系统音频采集：${target}`,
    );
  }
  const executableName = platform === "win32" ? "tiehu-system-audio.exe" : "tiehu-system-audio";
  return path.join(baseDirectory, "native", target, executableName);
}

async function prepareNativeAudioExecutable(baseDirectory, runtimeDirectory, platform, arch) {
  const bundledPath = resolveNativeAudioExecutable(baseDirectory, platform, arch);
  if (!fs.existsSync(bundledPath)) {
    throw new NativeSystemAudioError(
      "SYSTEM_AUDIO_HELPER_MISSING",
      `缺少 ${platform}/${arch} 系统音频组件，请重新安装完整插件`,
    );
  }
  if (runtimeDirectory) {
    return materializeNativeAudioExecutable(bundledPath, runtimeDirectory, platform);
  }
  return bundledPath;
}

async function materializeNativeAudioExecutable(sourcePath, runtimeDirectory, platform) {
  const sourceStat = await fs.promises.stat(sourcePath);
  if (!sourceStat.isFile() || sourceStat.size <= 0 || sourceStat.size > maxNativeHelperBytes) {
    throw new Error("插件内的系统音频组件文件无效");
  }
  const source = await fs.promises.readFile(sourcePath);
  const digest = sha256(source);
  const targetDirectory = path.join(runtimeDirectory, digest);
  const targetPath = path.join(targetDirectory, path.basename(sourcePath));
  await fs.promises.mkdir(targetDirectory, { recursive: true });

  if (await fileMatches(targetPath, digest)) {
    if (platform === "darwin") {
      await fs.promises.chmod(targetPath, 0o755);
    }
    return targetPath;
  }

  const temporaryPath = path.join(
    targetDirectory,
    `${path.basename(sourcePath)}.${process.pid}.${randomBytes(6).toString("hex")}.tmp`,
  );
  try {
    await fs.promises.writeFile(temporaryPath, source, { flag: "wx", mode: 0o755 });
    try {
      await fs.promises.rename(temporaryPath, targetPath);
    } catch (error) {
      if (!error || !["EEXIST", "EPERM"].includes(error.code) || !(await fileMatches(targetPath, digest))) {
        throw error;
      }
    }
    if (!(await fileMatches(targetPath, digest))) {
      throw new Error("释放后的系统音频组件校验失败");
    }
    if (platform === "darwin") {
      await fs.promises.chmod(targetPath, 0o755);
    }
    return targetPath;
  } finally {
    try {
      await fs.promises.unlink(temporaryPath);
    } catch (error) {
      if (!error || error.code !== "ENOENT") {
        console.error("cleanup native audio helper temporary file failed", error);
      }
    }
  }
}

async function fileMatches(filePath, expectedDigest) {
  try {
    const stat = await fs.promises.lstat(filePath);
    if (!stat.isFile() || stat.isSymbolicLink() || stat.size <= 0 || stat.size > maxNativeHelperBytes) {
      return false;
    }
    return sha256(await fs.promises.readFile(filePath)) === expectedDigest;
  } catch (error) {
    if (error && error.code === "ENOENT") {
      return false;
    }
    throw error;
  }
}

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

function parseJSONPayload(payload, frameName) {
  try {
    const value = JSON.parse(payload.toString("utf8"));
    if (!value || typeof value !== "object" || Array.isArray(value)) {
      throw new Error("payload is not an object");
    }
    return value;
  } catch (error) {
    throw new NativeSystemAudioError(
      "SYSTEM_AUDIO_PROTOCOL_INVALID",
      `系统音频组件 ${frameName} 消息无效：${error.message}`,
    );
  }
}

function normalizeNativeAudioError(error) {
  if (error instanceof NativeSystemAudioError) {
    return error;
  }
  return new NativeSystemAudioError(
    "SYSTEM_AUDIO_PROTOCOL_INVALID",
    error instanceof Error ? error.message : "系统音频组件协议错误",
  );
}

function sanitizeStderr(value) {
  return String(value || "")
    .replace(/[\r\n\t]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 500);
}

module.exports = {
  NativeSystemAudioError,
  NativeSystemAudioProcess,
  prepareNativeAudioExecutable,
  resolveNativeAudioExecutable,
};
