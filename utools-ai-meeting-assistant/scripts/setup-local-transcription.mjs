import { createHash } from "node:crypto";
import { createWriteStream, existsSync, statSync } from "node:fs";
import { mkdir, rename, rm } from "node:fs/promises";
import { Readable } from "node:stream";
import { pipeline } from "node:stream/promises";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const projectDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const targetDirectory = path.join(projectDirectory, "plugin", "native", `${process.platform}-${process.arch}`);
const executable = path.join(targetDirectory, process.platform === "win32" ? "tiehu-local-whisper.exe" : "tiehu-local-whisper");
const helperSource = path.join(projectDirectory, "native", "transcription", "main.cpp");
if (existsSync(executable) && statSync(executable).mtimeMs >= statSync(helperSource).mtimeMs) process.exit(0);

const cacheDirectory = path.join(projectDirectory, ".native-build", "local-transcription-sdk");
await mkdir(cacheDirectory, { recursive: true });

if (process.platform === "darwin") {
  const archive = path.join(cacheDirectory, "whisper-b5130-xcframework.zip");
  await download(
    "https://github.com/ggml-org/whisper.cpp/releases/download/b5130/whisper-b5130-xcframework.zip",
    archive,
    "033a43b0174e8cf9b366f72e4a428cdcf126f93ad1c87d3fa119a96bed6f231a",
  );
  const extracted = path.join(cacheDirectory, "xcframework");
  await rm(extracted, { recursive: true, force: true });
  await mkdir(extracted, { recursive: true });
  run("unzip", ["-q", archive, "-d", extracted]);
  run("/bin/sh", [
    path.join(projectDirectory, "native", "transcription", "build-macos.sh"),
    path.join(extracted, "build-apple", "whisper.xcframework"),
  ]);
} else if (process.platform === "win32") {
  const archive = path.join(cacheDirectory, "whisper-b5130-source.zip");
  await download(
    "https://github.com/ggml-org/whisper.cpp/archive/refs/tags/b5130.zip",
    archive,
  );
  const extracted = path.join(cacheDirectory, "source");
  await rm(extracted, { recursive: true, force: true });
  await mkdir(extracted, { recursive: true });
  run("powershell.exe", [
    "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command",
    `Expand-Archive -LiteralPath '${escapePowerShell(archive)}' -DestinationPath '${escapePowerShell(extracted)}' -Force`,
  ]);
  run("powershell.exe", [
    "-NoProfile", "-ExecutionPolicy", "Bypass", "-File",
    path.join(projectDirectory, "native", "transcription", "build-windows.ps1"),
    "-WhisperSource", path.join(extracted, "whisper.cpp-b5130"),
    "-Architecture", process.arch,
  ]);
} else {
  throw new Error(`Local transcription is not supported on ${process.platform}/${process.arch}`);
}

async function download(url, destination, expectedSha256) {
  if (existsSync(destination) && !expectedSha256) return;
  if (existsSync(destination) && expectedSha256 && await fileSha256(destination) === expectedSha256) return;
  const temporary = `${destination}.${process.pid}.download`;
  await rm(temporary, { force: true });
  const response = await fetch(url, { redirect: "follow" });
  if (!response.ok || !response.body) throw new Error(`Download failed: HTTP ${response.status}`);
  await pipeline(Readable.fromWeb(response.body), createWriteStream(temporary, { flags: "wx" }));
  if (expectedSha256 && await fileSha256(temporary) !== expectedSha256) {
    await rm(temporary, { force: true });
    throw new Error("Downloaded local transcription SDK failed SHA-256 verification");
  }
  await rename(temporary, destination);
}

async function fileSha256(filePath) {
  const hash = createHash("sha256");
  const { createReadStream } = await import("node:fs");
  for await (const chunk of createReadStream(filePath)) hash.update(chunk);
  return hash.digest("hex");
}

function run(command, args) {
  const result = spawnSync(command, args, { cwd: projectDirectory, stdio: "inherit", shell: false });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed with exit code ${String(result.status)}`);
}

function escapePowerShell(value) {
  return value.replaceAll("'", "''");
}
