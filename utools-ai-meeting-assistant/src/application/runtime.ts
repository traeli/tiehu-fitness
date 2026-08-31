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
  gatewayPromise ??= createMeetingGateway();
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
  const temporaryToken = await getDesktopBridge().getUserServerTemporaryToken();
  const authGateway = new UToolsAuthGateway(client);
  await authGateway.exchangeTemporaryToken(temporaryToken.token, getOrCreateDeviceID());
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
