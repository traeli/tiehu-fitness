const fs = require("node:fs");
const path = require("node:path");
const { spawn } = require("node:child_process");

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

class NativeSystemAudioError extends Error {
  constructor(code, message) {
    super(message);
    this.name = "NativeSystemAudioError";
    this.code = code;
  }
}

class NativeSystemAudioProcess {
  #child;
  #state = "idle";
  #stderr = "";
  #startTimer;
  #stopTimer;
  #startResolve;
  #startReject;
  #stopResolve;
  #onAudio;
  #onError;

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
    const executablePath = resolveNativeAudioExecutable(__dirname, process.platform, process.arch);
    if (!fs.existsSync(executablePath)) {
      throw new NativeSystemAudioError(
        "SYSTEM_AUDIO_HELPER_MISSING",
        `缺少 ${process.platform}/${process.arch} 系统音频组件，请重新安装完整插件`,
      );
    }
    this.#state = "starting";
    this.#stderr = "";
    this.#onAudio = onAudio;
    this.#onError = onError;
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
  resolveNativeAudioExecutable,
};
