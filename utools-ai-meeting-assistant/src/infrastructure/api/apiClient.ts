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

export class ApiRequestTimeoutError extends Error {
  constructor(readonly timeoutMs: number) {
    super(`会议服务请求在 ${Math.ceil(timeoutMs / 1_000)} 秒内没有响应`);
    this.name = "ApiRequestTimeoutError";
  }
}

interface ApiRequestOptions {
  timeoutMs?: number;
  retryUnauthorized?: boolean;
}

const defaultRequestTimeoutMs = 15_000;

export class ApiClient {
  #accessToken?: string;
  #unauthorizedHandler?: () => Promise<void>;
  #authenticationRefresh?: Promise<void>;

  constructor(private readonly baseUrl: string) {}

  setAccessToken(accessToken: string): void {
    this.#accessToken = accessToken;
  }

  setUnauthorizedHandler(handler: (() => Promise<void>) | undefined): void {
    this.#unauthorizedHandler = handler;
  }

  async request(
    path: string,
    init: RequestInit = {},
    options: ApiRequestOptions = {},
  ): Promise<unknown> {
    return this.#request(path, init, options, options.retryUnauthorized !== false);
  }

  async #request(
    path: string,
    init: RequestInit,
    options: ApiRequestOptions,
    allowUnauthorizedRetry: boolean,
  ): Promise<unknown> {
    const headers = new Headers(init.headers);
    headers.set("Accept", "application/json");
    if (init.body !== undefined) {
      headers.set("Content-Type", "application/json");
    }
    if (this.#accessToken) {
      headers.set("Authorization", `Bearer ${this.#accessToken}`);
    }

    const timeoutMs = positiveTimeout(options.timeoutMs ?? defaultRequestTimeoutMs);
    const requestAbort = createRequestAbort(init.signal, timeoutMs);
    try {
      const response = await fetch(`${this.baseUrl}${path}`, {
        ...init,
        headers,
        signal: requestAbort.signal,
      });
      if (response.status === 401 && allowUnauthorizedRetry && this.#unauthorizedHandler) {
        await this.#refreshAuthentication();
        return this.#request(path, init, options, false);
      }
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
      return await response.json() as unknown;
    } catch (error) {
      if (requestAbort.didTimeout()) {
        throw new ApiRequestTimeoutError(timeoutMs);
      }
      throw error;
    } finally {
      requestAbort.dispose();
    }
  }

  async #refreshAuthentication(): Promise<void> {
    if (!this.#unauthorizedHandler) {
      return;
    }
    this.#authenticationRefresh ??= this.#unauthorizedHandler().finally(() => {
      this.#authenticationRefresh = undefined;
    });
    await this.#authenticationRefresh;
  }
}

function positiveTimeout(timeoutMs: number): number {
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
    throw new Error("API request timeout must be positive");
  }
  return timeoutMs;
}

function createRequestAbort(parent: AbortSignal | null | undefined, timeoutMs: number): {
  signal: AbortSignal;
  didTimeout: () => boolean;
  dispose: () => void;
} {
  const controller = new AbortController();
  let timedOut = false;
  const abortFromParent = () => controller.abort(parent?.reason);
  if (parent?.aborted) {
    abortFromParent();
  } else {
    parent?.addEventListener("abort", abortFromParent, { once: true });
  }
  const timer = globalThis.setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeoutMs);
  return {
    signal: controller.signal,
    didTimeout: () => timedOut,
    dispose: () => {
      globalThis.clearTimeout(timer);
      parent?.removeEventListener("abort", abortFromParent);
    },
  };
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
