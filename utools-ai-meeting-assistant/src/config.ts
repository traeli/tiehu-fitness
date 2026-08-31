const rawApiBaseUrl = import.meta.env.VITE_API_BASE_URL ?? "http://127.0.0.1:8000";

function normalizeApiBaseUrl(raw: string): string {
	if (raw.startsWith("/") && !raw.startsWith("//")) {
		return raw.replace(/\/$/, "");
	}
	const url = new URL(raw);
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new Error("VITE_API_BASE_URL must use http or https");
  }
  return url.toString().replace(/\/$/, "");
}

export const appConfig = Object.freeze({
  apiBaseUrl: normalizeApiBaseUrl(rawApiBaseUrl),
  useMockApi: import.meta.env.VITE_USE_MOCK_API !== "false",
  useSyntheticAudio: import.meta.env.VITE_USE_SYNTHETIC_AUDIO === "true",
});
