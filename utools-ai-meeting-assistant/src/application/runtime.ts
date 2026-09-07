import { appConfig } from "@/config";
import { ApiClient } from "@/infrastructure/api/apiClient";
import {
  UToolsAuthGateway,
  WebAuthGateway,
  type AuthenticatedUser,
  type WebAuthInput,
} from "@/infrastructure/api/authGateway";
import {
  HttpMeetingGateway,
  MockMeetingGateway,
  type MeetingGateway,
} from "@/infrastructure/api/meetingGateway";
import { getDesktopBridge } from "@/infrastructure/desktop/desktopBridge";
import { DesktopFileRecordingRepository } from "@/infrastructure/recording/desktopFileRecordingRepository";
import { IndexedDBRecordingRepository } from "@/infrastructure/recording/indexedDBRecordingRepository";
import type { RecordingRepository } from "@/infrastructure/recording/recordingRepository";

let gatewayPromise: Promise<MeetingGateway> | undefined;
let apiClient: ApiClient | undefined;
let recordingRepository: RecordingRepository | undefined;
const deviceIDStorageKey = "tiehu.meeting.device-id";

export function getMeetingGateway(): Promise<MeetingGateway> {
  gatewayPromise ??= createMeetingGateway().catch((error: unknown) => {
    // A temporary network or uTools authentication failure must not poison all
    // later retries for the lifetime of the renderer.
    gatewayPromise = undefined;
    throw error;
  });
  return gatewayPromise;
}

export function getRecordingRepository(): RecordingRepository {
  recordingRepository ??= window.meetingDesktop
    ? new DesktopFileRecordingRepository(getDesktopBridge())
    : new IndexedDBRecordingRepository();
  return recordingRepository;
}

export function requiresBrowserAuthentication(): boolean {
  return !appConfig.useMockApi && window.meetingDesktop === undefined;
}

export async function authenticateBrowser(
  mode: "login" | "register",
  input: Omit<WebAuthInput, "deviceId">,
): Promise<AuthenticatedUser> {
  if (!requiresBrowserAuthentication()) {
    throw new Error("Browser authentication is not available in this runtime");
  }
  const client = getApiClient();
  const gateway = new WebAuthGateway(client);
  const request = { ...input, deviceId: getOrCreateDeviceID() };
  const user = mode === "register" ? await gateway.register(request) : await gateway.login(request);
  gatewayPromise = Promise.resolve(new HttpMeetingGateway(client));
  return user;
}

async function createMeetingGateway(): Promise<MeetingGateway> {
  if (appConfig.useMockApi) {
    return new MockMeetingGateway();
  }

  if (requiresBrowserAuthentication()) {
    throw new Error("请先使用邮箱登录");
  }
  const client = getApiClient();
  const authGateway = new UToolsAuthGateway(client);
  const authenticate = async () => {
    const temporaryToken = await withRuntimeTimeout(
      getDesktopBridge().getUserServerTemporaryToken(),
      10_000,
      "获取 uTools 登录凭证超时",
    );
    await authGateway.exchangeTemporaryToken(temporaryToken.token, getOrCreateDeviceID());
  };
  await authenticate();
  client.setUnauthorizedHandler(authenticate);
  return new HttpMeetingGateway(client);
}

function getApiClient(): ApiClient {
  apiClient ??= new ApiClient(appConfig.apiBaseUrl);
  return apiClient;
}

function getOrCreateDeviceID(): string {
  const stored = window.localStorage.getItem(deviceIDStorageKey);
  if (stored) {
    return stored;
  }
  const created = crypto.randomUUID();
  window.localStorage.setItem(deviceIDStorageKey, created);
  return created;
}

function withRuntimeTimeout<T>(promise: Promise<T>, timeoutMs: number, message: string): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = globalThis.setTimeout(() => reject(new Error(message)), timeoutMs);
    promise.then(
      (value) => {
        globalThis.clearTimeout(timer);
        resolve(value);
      },
      (error: unknown) => {
        globalThis.clearTimeout(timer);
        reject(error);
      },
    );
  });
}
