import type { ApiClient } from "./apiClient";

export interface AuthenticatedUser {
  userId: string;
  nickname: string;
}

export interface WebAuthInput {
  email: string;
  password: string;
  nickname?: string;
  deviceId: string;
}

export class WebAuthGateway {
  constructor(private readonly client: ApiClient) {}

  register(input: WebAuthInput): Promise<AuthenticatedUser> {
    return this.authenticate("/v1/auth/register", {
      email: input.email,
      password: input.password,
      nickname: input.nickname ?? "",
      device_id: input.deviceId,
    });
  }

  login(input: WebAuthInput): Promise<AuthenticatedUser> {
    return this.authenticate("/v1/auth/login", {
      email: input.email,
      password: input.password,
      device_id: input.deviceId,
    });
  }

  private async authenticate(path: string, body: Record<string, string>): Promise<AuthenticatedUser> {
    const response = await this.client.request(path, {
      method: "POST",
      body: JSON.stringify(body),
    });
    return applyAuthenticationResponse(this.client, response);
  }
}

export class UToolsAuthGateway {
  constructor(private readonly client: ApiClient) {}

  async exchangeTemporaryToken(temporaryToken: string, deviceId: string): Promise<void> {
    const response = await this.client.request("/v1/auth/utools/login", {
      method: "POST",
      body: JSON.stringify({ temporary_token: temporaryToken, device_id: deviceId }),
    });
    applyAuthenticationResponse(this.client, response);
  }
}

function applyAuthenticationResponse(client: ApiClient, response: unknown): AuthenticatedUser {
  if (!isRecord(response)) {
    throw new Error("Authentication response is invalid");
  }
  const accessToken = readString(response, "accessToken", "access_token");
  if (!accessToken) {
    throw new Error("Authentication response did not contain an access token");
  }
  if (!isRecord(response.user)) {
    throw new Error("Authentication response did not contain a user");
  }
  const userId = readString(response.user, "userId", "user_id");
  const nickname = readString(response.user, "nickname", "nickname");
  if (!userId || !nickname) {
    throw new Error("Authentication user is invalid");
  }
  client.setAccessToken(accessToken);
  return { userId, nickname };
}

function readString(value: Record<string, unknown>, camel: string, snake: string): string | undefined {
  const raw = value[camel] ?? value[snake];
  return typeof raw === "string" && raw ? raw : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
