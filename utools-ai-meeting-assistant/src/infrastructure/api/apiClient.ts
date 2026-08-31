export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly reason?: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export class ApiClient {
  #accessToken?: string;

  constructor(private readonly baseUrl: string) {}

  setAccessToken(accessToken: string): void {
    this.#accessToken = accessToken;
  }

  async request(path: string, init: RequestInit = {}): Promise<unknown> {
    const headers = new Headers(init.headers);
    headers.set("Accept", "application/json");
    if (init.body !== undefined) {
      headers.set("Content-Type", "application/json");
    }
    if (this.#accessToken) {
      headers.set("Authorization", `Bearer ${this.#accessToken}`);
    }

    const response = await fetch(`${this.baseUrl}${path}`, { ...init, headers });
    if (!response.ok) {
      const body: unknown = await response.json().catch(() => null);
      const detail = readErrorDetail(body);
      throw new ApiError(
        detail.message ?? `Request failed with status ${response.status}`,
        response.status,
        detail.reason,
      );
    }

    if (response.status === 204) {
      return undefined;
    }
    return response.json() as Promise<unknown>;
  }
}

function readErrorDetail(value: unknown): { message?: string; reason?: string } {
  if (!isRecord(value)) {
    return {};
  }
  return {
    message: typeof value.message === "string" ? value.message : undefined,
    reason: typeof value.reason === "string" ? value.reason : undefined,
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
