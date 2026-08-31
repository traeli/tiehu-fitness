import { describe, expect, it } from "vitest";

import { ApiClient } from "./apiClient";
import { UToolsAuthGateway, WebAuthGateway } from "./authGateway";

class StubApiClient extends ApiClient {
  readonly requests: { path: string; init: RequestInit }[] = [];
  accessToken?: string;

  constructor(private readonly response: unknown) {
    super("http://127.0.0.1:8000");
  }

  override setAccessToken(accessToken: string): void {
    this.accessToken = accessToken;
  }

  override async request(path: string, init: RequestInit = {}): Promise<unknown> {
    this.requests.push({ path, init });
    return this.response;
  }
}

describe("WebAuthGateway", () => {
  it("registers with the current backend JSON contract", async () => {
    const client = new StubApiClient({
      access_token: "access-token",
      user: { user_id: "user-id", nickname: "本地用户" },
    });
    const user = await new WebAuthGateway(client).register({
      email: "local.user@example.com",
      password: "password-123",
      nickname: "本地用户",
      deviceId: "device-id",
    });
    expect(user).toEqual({ userId: "user-id", nickname: "本地用户" });
    expect(client.accessToken).toBe("access-token");
    expect(client.requests[0]?.path).toBe("/v1/auth/register");
    expect(JSON.parse(String(client.requests[0]?.init.body))).toEqual({
      email: "local.user@example.com",
      password: "password-123",
      nickname: "本地用户",
      device_id: "device-id",
    });
  });

  it("accepts the Proto camelCase response", async () => {
    const client = new StubApiClient({
      accessToken: "access-token",
      user: { userId: "user-id", nickname: "Camel User" },
    });
    await expect(new WebAuthGateway(client).login({
      email: "camel.user@example.com",
      password: "password-123",
      deviceId: "device-id",
    })).resolves.toEqual({ userId: "user-id", nickname: "Camel User" });
  });
});

describe("UToolsAuthGateway", () => {
  it("uses snake_case for the current backend request", async () => {
    const client = new StubApiClient({
      access_token: "access-token",
      user: { user_id: "user-id", nickname: "uTools 用户" },
    });
    await new UToolsAuthGateway(client).exchangeTemporaryToken("temporary-token", "device-id");
    expect(JSON.parse(String(client.requests[0]?.init.body))).toEqual({
      temporary_token: "temporary-token",
      device_id: "device-id",
    });
    expect(client.accessToken).toBe("access-token");
  });
});
