#include "whisper.h"

#include <algorithm>
#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <string>
#include <thread>
#include <vector>

namespace {

bool read_exact(char * data, std::size_t size) {
    std::cin.read(data, static_cast<std::streamsize>(size));
    return static_cast<std::size_t>(std::cin.gcount()) == size;
}

void write_u32(std::uint32_t value) {
    const char bytes[4] = {
        static_cast<char>(value & 0xff),
        static_cast<char>((value >> 8) & 0xff),
        static_cast<char>((value >> 16) & 0xff),
        static_cast<char>((value >> 24) & 0xff),
    };
    std::cout.write(bytes, sizeof(bytes));
}

void write_string(const std::string & value) {
    write_u32(static_cast<std::uint32_t>(value.size()));
    std::cout.write(value.data(), static_cast<std::streamsize>(value.size()));
}

std::uint32_t decode_u32(const char bytes[4]) {
    return static_cast<std::uint32_t>(static_cast<unsigned char>(bytes[0])) |
        (static_cast<std::uint32_t>(static_cast<unsigned char>(bytes[1])) << 8) |
        (static_cast<std::uint32_t>(static_cast<unsigned char>(bytes[2])) << 16) |
        (static_cast<std::uint32_t>(static_cast<unsigned char>(bytes[3])) << 24);
}

void write_error(const std::string & message) {
    write_u32(1);
    write_string(message);
    std::cout.flush();
}

} // namespace

int main(int argc, char ** argv) {
    if (argc != 2) {
        std::cerr << "usage: tiehu-local-whisper <model-path>\n";
        return 2;
    }

    whisper_context_params context_params = whisper_context_default_params();
    context_params.use_gpu = true;
    whisper_context * context = whisper_init_from_file_with_params(argv[1], context_params);
    if (context == nullptr) {
        std::cerr << "failed to load whisper model\n";
        return 3;
    }

    std::cout.write("READY\n", 6);
    std::cout.flush();

    constexpr std::uint32_t max_pcm_bytes = 30U * 16000U * 2U;
    char header[4];
    while (read_exact(header, sizeof(header))) {
        const std::uint32_t byte_count = decode_u32(header);
        if (byte_count == 0 || byte_count > max_pcm_bytes || byte_count % 2 != 0) {
            write_error("invalid PCM frame");
            if (byte_count > 0 && byte_count <= max_pcm_bytes) {
                std::vector<char> discarded(byte_count);
                if (!read_exact(discarded.data(), discarded.size())) {
                    break;
                }
            }
            continue;
        }

        std::vector<std::int16_t> pcm(byte_count / 2);
        if (!read_exact(reinterpret_cast<char *>(pcm.data()), byte_count)) {
            break;
        }
        std::vector<float> samples(pcm.size());
        std::transform(pcm.begin(), pcm.end(), samples.begin(), [](std::int16_t sample) {
            return static_cast<float>(sample) / 32768.0F;
        });

        whisper_full_params params = whisper_full_default_params(WHISPER_SAMPLING_GREEDY);
        const unsigned int hardware_threads = std::thread::hardware_concurrency();
        params.n_threads = static_cast<int>(std::max(1U, std::min(8U, hardware_threads)));
        // The first product version targets Chinese meetings. Explicitly
        // selecting Chinese avoids repeated language detection on every short
        // streaming window and is materially more accurate for Mandarin.
        params.language = "zh";
        params.translate = false;
        params.no_context = false;
        params.initial_prompt = "以下是中文会议的录音转写，包含普通话、数字、人名和专业术语。";
        params.carry_initial_prompt = true;
        params.print_progress = false;
        params.print_realtime = false;
        params.print_timestamps = false;
        params.suppress_blank = true;
        params.suppress_nst = true;

        if (whisper_full(context, params, samples.data(), static_cast<int>(samples.size())) != 0) {
            write_error("whisper inference failed");
            continue;
        }

        const int segment_count = whisper_full_n_segments(context);
        write_u32(0);
        write_u32(static_cast<std::uint32_t>(std::max(0, segment_count)));
        for (int index = 0; index < segment_count; ++index) {
            write_u32(static_cast<std::uint32_t>(whisper_full_get_segment_t0(context, index) * 10));
            write_u32(static_cast<std::uint32_t>(whisper_full_get_segment_t1(context, index) * 10));
            const char * text = whisper_full_get_segment_text(context, index);
            write_string(text == nullptr ? "" : text);
        }
        std::cout.flush();
    }

    whisper_free(context);
    return 0;
}
