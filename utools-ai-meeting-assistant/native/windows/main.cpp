#define NOMINMAX
#include <windows.h>
#include <audioclient.h>
#include <fcntl.h>
#include <functiondiscoverykeys_devpkey.h>
#include <io.h>
#include <ksmedia.h>
#include <mmdeviceapi.h>
#include <wrl/client.h>

#include <algorithm>
#include <atomic>
#include <cmath>
#include <cstdint>
#include <cstring>
#include <iostream>
#include <memory>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

using Microsoft::WRL::ComPtr;

namespace {
constexpr std::uint32_t kTargetSampleRate = 48'000;
constexpr std::uint32_t kTargetChannels = 1;
constexpr std::size_t kChunkSamples = 4'800;

enum class FrameType : std::uint8_t {
  Ready = 1,
  Audio = 2,
  Error = 3,
};

class FrameWriter {
 public:
  void Ready() {
    Write(FrameType::Ready,
          R"({"sampleRate":48000,"channels":1,"format":"pcm_s16le"})");
  }

  void Audio(const std::vector<std::int16_t>& samples) {
    const auto* bytes = reinterpret_cast<const char*>(samples.data());
    Write(FrameType::Audio, std::string(bytes, samples.size() * sizeof(std::int16_t)));
  }

  void Error(const std::string& code, const std::string& message) {
    Write(FrameType::Error,
          std::string("{\"code\":\"") + EscapeJSON(code) +
              "\",\"message\":\"" + EscapeJSON(message) + "\"}");
  }

 private:
  void Write(FrameType type, const std::string& payload) {
    if (payload.size() > 65'536) {
      return;
    }
    const std::uint8_t header[12] = {
        'T', 'H', 'A', 'U', 1, static_cast<std::uint8_t>(type), 0, 0,
        static_cast<std::uint8_t>(payload.size() & 0xff),
        static_cast<std::uint8_t>((payload.size() >> 8) & 0xff),
        static_cast<std::uint8_t>((payload.size() >> 16) & 0xff),
        static_cast<std::uint8_t>((payload.size() >> 24) & 0xff),
    };
    std::cout.write(reinterpret_cast<const char*>(header), sizeof(header));
    std::cout.write(payload.data(), static_cast<std::streamsize>(payload.size()));
    std::cout.flush();
  }

  static std::string EscapeJSON(const std::string& value) {
    std::string escaped;
    escaped.reserve(std::min<std::size_t>(value.size(), 500));
    for (const char character : value) {
      if (escaped.size() >= 500) {
        break;
      }
      switch (character) {
        case '\\': escaped += "\\\\"; break;
        case '"': escaped += "\\\""; break;
        case '\r':
        case '\n':
        case '\t': escaped += ' '; break;
        default:
          if (static_cast<unsigned char>(character) >= 0x20) {
            escaped += character;
          }
      }
    }
    return escaped;
  }
};

class SampleConverter {
 public:
  explicit SampleConverter(const WAVEFORMATEX& format)
      : source_rate_(format.nSamplesPerSec),
        channels_(format.nChannels),
        bits_per_sample_(format.wBitsPerSample),
        block_align_(format.nBlockAlign),
        source_per_target_(static_cast<double>(format.nSamplesPerSec) /
                           static_cast<double>(kTargetSampleRate)) {
    if (source_rate_ == 0 || channels_ == 0 || block_align_ == 0) {
      throw std::runtime_error("默认播放设备返回了无效音频格式");
    }
    if (format.wFormatTag == WAVE_FORMAT_IEEE_FLOAT) {
      is_float_ = true;
    } else if (format.wFormatTag == WAVE_FORMAT_PCM) {
      is_pcm_ = true;
    } else if (format.wFormatTag == WAVE_FORMAT_EXTENSIBLE &&
               format.cbSize >= sizeof(WAVEFORMATEXTENSIBLE) - sizeof(WAVEFORMATEX)) {
      const auto& extensible = reinterpret_cast<const WAVEFORMATEXTENSIBLE&>(format);
      is_float_ = IsEqualGUID(extensible.SubFormat, KSDATAFORMAT_SUBTYPE_IEEE_FLOAT);
      is_pcm_ = IsEqualGUID(extensible.SubFormat, KSDATAFORMAT_SUBTYPE_PCM);
    }
    if (!is_float_ && !is_pcm_) {
      throw std::runtime_error("默认播放设备使用了不支持的音频编码");
    }
    source_buffer_.reserve(source_rate_ / 2);
  }

  std::vector<std::int16_t> Push(const BYTE* data, UINT32 frames, bool silent) {
    for (UINT32 frame = 0; frame < frames; ++frame) {
      float mixed = 0.0f;
      if (!silent && data != nullptr) {
        const BYTE* frame_data = data + static_cast<std::size_t>(frame) * block_align_;
        for (WORD channel = 0; channel < channels_; ++channel) {
          mixed += Decode(frame_data, channel);
        }
        mixed /= static_cast<float>(channels_);
      }
      source_buffer_.push_back(std::clamp(mixed, -1.0f, 1.0f));
    }

    std::vector<std::int16_t> output;
    output.reserve(static_cast<std::size_t>(frames / source_per_target_) + 1);
    while (source_position_ + 1.0 < static_cast<double>(source_buffer_.size())) {
      const auto left_index = static_cast<std::size_t>(source_position_);
      const double fraction = source_position_ - static_cast<double>(left_index);
      const float left = source_buffer_[left_index];
      const float right = source_buffer_[left_index + 1];
      const float sample = left + (right - left) * static_cast<float>(fraction);
      const float scaled = sample < 0 ? sample * 32768.0f : sample * 32767.0f;
      output.push_back(static_cast<std::int16_t>(std::lround(scaled)));
      source_position_ += source_per_target_;
    }
    const auto discard = static_cast<std::size_t>(source_position_);
    if (discard > 0) {
      source_buffer_.erase(source_buffer_.begin(), source_buffer_.begin() + discard);
      source_position_ -= static_cast<double>(discard);
    }
    return output;
  }

 private:
  float Decode(const BYTE* frame_data, WORD channel) const {
    const std::size_t bytes_per_sample = block_align_ / channels_;
    const BYTE* sample = frame_data + static_cast<std::size_t>(channel) * bytes_per_sample;
    if (is_float_ && bits_per_sample_ == 32) {
      float value = 0;
      std::memcpy(&value, sample, sizeof(value));
      return std::isfinite(value) ? value : 0.0f;
    }
    if (!is_pcm_) {
      return 0.0f;
    }
    if (bits_per_sample_ == 16) {
      std::int16_t value = 0;
      std::memcpy(&value, sample, sizeof(value));
      return static_cast<float>(value) / 32768.0f;
    }
    if (bits_per_sample_ == 24) {
      std::int32_t value = static_cast<std::int32_t>(sample[0]) |
                           (static_cast<std::int32_t>(sample[1]) << 8) |
                           (static_cast<std::int32_t>(sample[2]) << 16);
      if ((value & 0x00800000) != 0) {
        value |= static_cast<std::int32_t>(0xff000000);
      }
      return static_cast<float>(value) / 8388608.0f;
    }
    if (bits_per_sample_ == 32) {
      std::int32_t value = 0;
      std::memcpy(&value, sample, sizeof(value));
      return static_cast<float>(static_cast<double>(value) / 2147483648.0);
    }
    return 0.0f;
  }

  std::uint32_t source_rate_;
  WORD channels_;
  WORD bits_per_sample_;
  WORD block_align_;
  bool is_float_ = false;
  bool is_pcm_ = false;
  double source_per_target_;
  double source_position_ = 0;
  std::vector<float> source_buffer_;
};

void Check(HRESULT result, const char* operation) {
  if (FAILED(result)) {
    throw std::runtime_error(std::string(operation) + "失败，HRESULT=" +
                             std::to_string(static_cast<unsigned long>(result)));
  }
}

class WasapiLoopbackCapture {
 public:
  WasapiLoopbackCapture() {
    Check(CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL,
                           IID_PPV_ARGS(&enumerator_)),
          "创建音频设备枚举器");
    Check(enumerator_->GetDefaultAudioEndpoint(eRender, eConsole, &device_),
          "获取默认播放设备");
    Check(device_->Activate(__uuidof(IAudioClient), CLSCTX_ALL, nullptr,
                            reinterpret_cast<void**>(client_.GetAddressOf())),
          "打开默认播放设备");
    Check(client_->GetMixFormat(&mix_format_), "读取默认播放格式");
    converter_ = std::make_unique<SampleConverter>(*mix_format_);

    audio_event_ = CreateEventW(nullptr, FALSE, FALSE, nullptr);
    stop_event_ = CreateEventW(nullptr, TRUE, FALSE, nullptr);
    if (audio_event_ == nullptr || stop_event_ == nullptr) {
      throw std::runtime_error("创建音频同步事件失败");
    }
    const DWORD flags = AUDCLNT_STREAMFLAGS_LOOPBACK | AUDCLNT_STREAMFLAGS_EVENTCALLBACK;
    Check(client_->Initialize(AUDCLNT_SHAREMODE_SHARED, flags, 0, 0, mix_format_, nullptr),
          "初始化 WASAPI Loopback");
    Check(client_->SetEventHandle(audio_event_), "设置 WASAPI 音频事件");
    Check(client_->GetService(__uuidof(IAudioCaptureClient),
                              reinterpret_cast<void**>(capture_client_.GetAddressOf())),
          "创建 WASAPI 采集客户端");
  }

  ~WasapiLoopbackCapture() {
    if (client_) {
      client_->Stop();
    }
    if (mix_format_ != nullptr) {
      CoTaskMemFree(mix_format_);
    }
    if (audio_event_ != nullptr) {
      CloseHandle(audio_event_);
    }
    if (stop_event_ != nullptr) {
      CloseHandle(stop_event_);
    }
  }

  void Run(FrameWriter& writer) {
    Check(client_->Start(), "启动 WASAPI Loopback");
    writer.Ready();
    std::thread stop_reader([this]() {
      std::string command;
      std::getline(std::cin, command);
      SetEvent(stop_event_);
    });

    HANDLE events[] = {stop_event_, audio_event_};
    bool running = true;
    while (running) {
      const DWORD wait_result = WaitForMultipleObjects(2, events, FALSE, INFINITE);
      if (wait_result == WAIT_OBJECT_0) {
        running = false;
        continue;
      }
      if (wait_result != WAIT_OBJECT_0 + 1) {
        throw std::runtime_error("等待 WASAPI 音频事件失败");
      }
      DrainPackets(writer);
    }
    Check(client_->Stop(), "停止 WASAPI Loopback");
    if (stop_reader.joinable()) {
      stop_reader.join();
    }
  }

 private:
  void DrainPackets(FrameWriter& writer) {
    UINT32 packet_frames = 0;
    Check(capture_client_->GetNextPacketSize(&packet_frames), "读取 WASAPI 分片长度");
    while (packet_frames > 0) {
      BYTE* data = nullptr;
      UINT32 frames = 0;
      DWORD flags = 0;
      Check(capture_client_->GetBuffer(&data, &frames, &flags, nullptr, nullptr),
            "读取 WASAPI 分片");
      const bool silent = (flags & AUDCLNT_BUFFERFLAGS_SILENT) != 0;
      const auto converted = converter_->Push(data, frames, silent);
      capture_client_->ReleaseBuffer(frames);
      pending_samples_.insert(pending_samples_.end(), converted.begin(), converted.end());
      while (pending_samples_.size() >= kChunkSamples) {
        std::vector<std::int16_t> chunk(
            pending_samples_.begin(), pending_samples_.begin() + kChunkSamples);
        pending_samples_.erase(pending_samples_.begin(),
                               pending_samples_.begin() + kChunkSamples);
        writer.Audio(chunk);
      }
      Check(capture_client_->GetNextPacketSize(&packet_frames), "读取下一 WASAPI 分片长度");
    }
  }

  ComPtr<IMMDeviceEnumerator> enumerator_;
  ComPtr<IMMDevice> device_;
  ComPtr<IAudioClient> client_;
  ComPtr<IAudioCaptureClient> capture_client_;
  WAVEFORMATEX* mix_format_ = nullptr;
  HANDLE audio_event_ = nullptr;
  HANDLE stop_event_ = nullptr;
  std::unique_ptr<SampleConverter> converter_;
  std::vector<std::int16_t> pending_samples_;
};
}  // namespace

int main() {
  _setmode(_fileno(stdout), _O_BINARY);
  SetErrorMode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX);
  FrameWriter writer;
  const HRESULT com_result = CoInitializeEx(nullptr, COINIT_MULTITHREADED);
  if (FAILED(com_result)) {
    writer.Error("SYSTEM_AUDIO_INITIALIZATION_FAILED", "无法初始化 Windows 音频组件");
    return 1;
  }
  try {
    {
      WasapiLoopbackCapture capture;
      capture.Run(writer);
    }
    CoUninitialize();
    return 0;
  } catch (const std::exception& error) {
    writer.Error("SYSTEM_AUDIO_WASAPI_FAILED", error.what());
    std::cerr << "WASAPI loopback failed: " << error.what() << '\n';
    CoUninitialize();
    return 1;
  }
}
