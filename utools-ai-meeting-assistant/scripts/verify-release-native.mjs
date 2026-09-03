import { open, stat } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const projectDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const artifacts = [
  {
    platform: "macOS Apple Silicon",
    relativePath: path.join("plugin", "native", "darwin-arm64", "tiehu-system-audio"),
    magic: Buffer.from([0xcf, 0xfa, 0xed, 0xfe]),
    executable: true,
  },
  {
    platform: "macOS Intel",
    relativePath: path.join("plugin", "native", "darwin-x64", "tiehu-system-audio"),
    magic: Buffer.from([0xcf, 0xfa, 0xed, 0xfe]),
    executable: true,
  },
  {
    platform: "Windows x64",
    relativePath: path.join("plugin", "native", "win32-x64", "tiehu-system-audio.exe"),
    magic: Buffer.from("MZ", "ascii"),
    executable: false,
  },
];

const failures = [];
for (const artifact of artifacts) {
  const absolutePath = path.join(projectDirectory, artifact.relativePath);
  try {
    const metadata = await stat(absolutePath);
    if (!metadata.isFile() || metadata.size <= artifact.magic.length) {
      failures.push(`${artifact.platform}: 文件无效 (${artifact.relativePath})`);
      continue;
    }
    if (artifact.executable && (metadata.mode & 0o111) === 0) {
      failures.push(`${artifact.platform}: 文件缺少可执行权限 (${artifact.relativePath})`);
      continue;
    }
    const handle = await open(absolutePath, "r");
    try {
      const header = Buffer.alloc(artifact.magic.length);
      const { bytesRead } = await handle.read(header, 0, header.length, 0);
      if (bytesRead !== header.length || !header.equals(artifact.magic)) {
        failures.push(`${artifact.platform}: 文件格式不正确 (${artifact.relativePath})`);
      }
    } finally {
      await handle.close();
    }
  } catch (error) {
    const reason = error instanceof Error ? error.message : String(error);
    failures.push(`${artifact.platform}: 缺少发布文件 (${artifact.relativePath}): ${reason}`);
  }
}

if (failures.length > 0) {
  throw new Error(`跨平台原生音频发布检查失败：\n- ${failures.join("\n- ")}`);
}

console.log("跨平台原生音频发布检查通过：macOS arm64、macOS x64、Windows x64。");
