const { NativeSystemAudioProcess } = require("../plugin/native-audio-process.cjs");

const captureDurationMs = 5_000;
const capture = new NativeSystemAudioProcess();
let chunks = 0;
let samples = 0;
let sumSquares = 0;
let peak = 0;
let asyncFailure;

async function run() {
  console.log("请播放一段电脑音频；自检持续 5 秒，不保存音频，也不连接会议 API。");
  const ready = await capture.start({
    onAudio: (buffer) => {
      chunks += 1;
      const view = new DataView(buffer);
      for (let offset = 0; offset < view.byteLength; offset += 2) {
        const normalized = view.getInt16(offset, true) / 32_768;
        samples += 1;
        sumSquares += normalized * normalized;
        peak = Math.max(peak, Math.abs(normalized));
      }
    },
    onError: (error) => {
      asyncFailure = error;
    },
  });
  console.log(`系统音频组件已启动：${ready.sampleRate}Hz/${ready.channels}ch/${ready.format}`);
  await new Promise((resolve) => setTimeout(resolve, captureDurationMs));
  await capture.stop();
  if (asyncFailure) {
    throw asyncFailure;
  }
  const rms = samples > 0 ? Math.sqrt(sumSquares / samples) : 0;
  console.log(`PCM 分片=${chunks}，样本=${samples}，峰值=${peak.toFixed(6)}，RMS=${rms.toFixed(6)}`);
  if (chunks === 0) {
    throw new Error("没有收到系统音频 PCM 分片");
  }
  if (peak < 0.0001) {
    throw new Error("收到了 PCM，但音频为静音；请确认正在播放电脑音频和默认输出设备");
  }
  console.log("系统音频自检通过。");
}

process.once("SIGINT", () => {
  void capture.stop().finally(() => process.exit(130));
});

run().catch(async (error) => {
  try {
    await capture.stop();
  } catch (stopError) {
    console.error("停止自检失败：", stopError);
  }
  const code = typeof error?.code === "string" ? `${error.code}: ` : "";
  console.error(`系统音频自检失败：${code}${error instanceof Error ? error.message : String(error)}`);
  process.exitCode = 1;
});
