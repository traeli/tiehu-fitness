import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const projectDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

let command;
let args;
if (process.platform === "darwin") {
  command = "/bin/sh";
  args = [path.join(projectDirectory, "native", "macos", "build.sh")];
} else if (process.platform === "win32") {
  if (!new Set(["x64", "arm64"]).has(process.arch)) {
    throw new Error(`Unsupported Windows architecture ${process.arch}`);
  }
  command = "powershell.exe";
  args = [
    "-NoProfile",
    "-ExecutionPolicy",
    "Bypass",
    "-File",
    path.join(projectDirectory, "native", "windows", "build.ps1"),
    "-Architecture",
    process.arch,
  ];
} else {
  throw new Error(`Native system audio is not supported on ${process.platform}/${process.arch}`);
}

const result = spawnSync(command, args, {
  cwd: projectDirectory,
  stdio: "inherit",
  shell: false,
});
if (result.error) {
  throw result.error;
}
if (result.status !== 0) {
  throw new Error(`Native audio build failed with exit code ${String(result.status)}`);
}
