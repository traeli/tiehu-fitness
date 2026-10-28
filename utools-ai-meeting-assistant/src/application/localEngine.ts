export type LocalEngineSetupState =
  | "checking"
  | "unsupported"
  | "runtime_missing"
  | "model_required"
  | "downloading"
  | "verifying"
  | "ready"
  | "failed";

export type LocalModelOperationPhase = "idle" | "downloading" | "verifying";

export function resolveLocalEngineSetupState(input: {
  status?: LocalTranscriptionStatus;
  operationPhase: LocalModelOperationPhase;
  failed: boolean;
}): LocalEngineSetupState {
  if (input.operationPhase === "downloading") return "downloading";
  if (input.operationPhase === "verifying") return "verifying";
  if (input.failed) return "failed";
  if (!input.status) return "checking";
  if (!input.status.supported) return "unsupported";
  if (!input.status.binaryAvailable) return "runtime_missing";
  if (!input.status.modelDownloaded) return "model_required";
  return "ready";
}

export function modelDownloadPercent(receivedBytes: number, totalBytes: number): number {
  if (!Number.isFinite(receivedBytes) || !Number.isFinite(totalBytes) || totalBytes <= 0) return 0;
  return Math.max(0, Math.min(100, Math.round(receivedBytes / totalBytes * 100)));
}
