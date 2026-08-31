import AVFAudio
import CoreMedia
import Darwin
import Foundation
import ScreenCaptureKit

private let targetSampleRate = 48_000
private let targetChannels = 1
private let chunkSamples = 4_800

private enum FrameType: UInt8 {
    case ready = 1
    case audio = 2
    case error = 3
}

private final class FrameWriter: @unchecked Sendable {
    private let lock = NSLock()

    func ready() {
        write(.ready, Data("{\"sampleRate\":48000,\"channels\":1,\"format\":\"pcm_s16le\"}".utf8))
    }

    func audio(_ payload: Data) {
        guard !payload.isEmpty, payload.count <= 65_536 else { return }
        write(.audio, payload)
    }

    func failure(code: String, message: String) {
        let value = ["code": code, "message": message]
        guard let payload = try? JSONSerialization.data(withJSONObject: value) else { return }
        write(.error, payload)
    }

    private func write(_ type: FrameType, _ payload: Data) {
        var frame = Data([0x54, 0x48, 0x41, 0x55, 0x01, type.rawValue, 0x00, 0x00])
        var payloadSize = UInt32(payload.count).littleEndian
        withUnsafeBytes(of: &payloadSize) { frame.append(contentsOf: $0) }
        frame.append(payload)
        lock.lock()
        defer { lock.unlock() }
        do {
            try FileHandle.standardOutput.write(contentsOf: frame)
        } catch {
            Darwin.exit(1)
        }
    }
}

@available(macOS 13.0, *)
private final class SystemAudioOutput: NSObject, SCStreamOutput, SCStreamDelegate {
    private let writer: FrameWriter
    private var pendingSamples: [Int16] = []

    init(writer: FrameWriter) {
        self.writer = writer
        pendingSamples.reserveCapacity(chunkSamples * 2)
    }

    func stream(
        _ stream: SCStream,
        didOutputSampleBuffer sampleBuffer: CMSampleBuffer,
        of outputType: SCStreamOutputType
    ) {
        guard outputType == .audio, sampleBuffer.isValid else { return }
        do {
            try sampleBuffer.withAudioBufferList { audioBufferList, _ in
                guard
                    let description = sampleBuffer.formatDescription?.audioStreamBasicDescription,
                    let format = AVAudioFormat(
                        standardFormatWithSampleRate: description.mSampleRate,
                        channels: description.mChannelsPerFrame
                    ),
                    let pcmBuffer = AVAudioPCMBuffer(
                        pcmFormat: format,
                        bufferListNoCopy: audioBufferList.unsafePointer
                    ),
                    let channelData = pcmBuffer.floatChannelData
                else {
                    return
                }
                let frameLength = Int(pcmBuffer.frameLength)
                let channelCount = Int(pcmBuffer.format.channelCount)
                guard frameLength > 0, channelCount > 0 else { return }
                for frameIndex in 0..<frameLength {
                    var mixed: Float = 0
                    for channelIndex in 0..<channelCount {
                        mixed += channelData[channelIndex][frameIndex]
                    }
                    mixed /= Float(channelCount)
                    let clamped = max(-1, min(1, mixed))
                    let scaled = clamped < 0 ? clamped * 32_768 : clamped * 32_767
                    pendingSamples.append(Int16(scaled.rounded()))
                }
                emitCompleteChunks()
            }
        } catch {
            writer.failure(code: "SYSTEM_AUDIO_SAMPLE_INVALID", message: "无法读取系统音频数据")
        }
    }

    func stream(_ stream: SCStream, didStopWithError error: Error) {
        writer.failure(code: "SYSTEM_AUDIO_CAPTURE_STOPPED", message: sanitize(error.localizedDescription))
    }

    private func emitCompleteChunks() {
        while pendingSamples.count >= chunkSamples {
            let samples = pendingSamples.prefix(chunkSamples)
            var payload = Data(capacity: chunkSamples * MemoryLayout<Int16>.size)
            for var sample in samples {
                sample = sample.littleEndian
                withUnsafeBytes(of: &sample) { payload.append(contentsOf: $0) }
            }
            pendingSamples.removeFirst(chunkSamples)
            writer.audio(payload)
        }
    }
}

@available(macOS 13.0, *)
private final class CaptureController {
    private let writer: FrameWriter
    private let output: SystemAudioOutput
    private let outputQueue = DispatchQueue(label: "com.tiehu.meeting.system-audio")
    private var stream: SCStream?

    init(writer: FrameWriter) {
        self.writer = writer
        self.output = SystemAudioOutput(writer: writer)
    }

    func start() async throws {
        let content = try await SCShareableContent.excludingDesktopWindows(
            false,
            onScreenWindowsOnly: true
        )
        guard let display = content.displays.first else {
            throw CaptureFailure(code: "SYSTEM_AUDIO_DISPLAY_NOT_FOUND", message: "没有找到可采集的显示器")
        }
        let filter = SCContentFilter(display: display, excludingWindows: [])
        let configuration = SCStreamConfiguration()
        configuration.capturesAudio = true
        configuration.sampleRate = targetSampleRate
        configuration.channelCount = targetChannels
        configuration.excludesCurrentProcessAudio = true
        configuration.width = 2
        configuration.height = 2
        configuration.minimumFrameInterval = CMTime(seconds: 1, preferredTimescale: 1)
        configuration.queueDepth = 3

        let stream = SCStream(filter: filter, configuration: configuration, delegate: output)
        try stream.addStreamOutput(output, type: .audio, sampleHandlerQueue: outputQueue)
        try await stream.startCapture()
        self.stream = stream
    }

    func stop() async {
        guard let stream else { return }
        do {
            try await stream.stopCapture()
        } catch {
            writer.failure(code: "SYSTEM_AUDIO_STOP_FAILED", message: "停止系统音频采集失败")
        }
        self.stream = nil
    }
}

private struct CaptureFailure: Error {
    let code: String
    let message: String
}

private func sanitize(_ message: String) -> String {
    String(message
        .replacingOccurrences(of: "\n", with: " ")
        .replacingOccurrences(of: "\r", with: " ")
        .prefix(500))
}

private func waitForStopCommand() async {
    await withCheckedContinuation { continuation in
        DispatchQueue.global(qos: .utility).async {
            _ = readLine()
            continuation.resume()
        }
    }
}

@main
private struct TiehuSystemAudioMain {
    static func main() async {
        let writer = FrameWriter()
        guard #available(macOS 13.0, *) else {
            writer.failure(code: "SYSTEM_AUDIO_OS_UNSUPPORTED", message: "系统音频录制需要 macOS 13 或更高版本")
            Darwin.exit(2)
        }
        let controller = CaptureController(writer: writer)
        do {
            try await controller.start()
            writer.ready()
            await waitForStopCommand()
            await controller.stop()
        } catch let failure as CaptureFailure {
            writer.failure(code: failure.code, message: failure.message)
            Darwin.exit(1)
        } catch {
            writer.failure(
                code: "SYSTEM_AUDIO_PERMISSION_DENIED",
                message: "无法启动系统音频录制，请在系统设置中允许屏幕与系统音频录制"
            )
            FileHandle.standardError.write(Data("ScreenCaptureKit start failed: \(sanitize(error.localizedDescription))\n".utf8))
            Darwin.exit(1)
        }
    }
}
