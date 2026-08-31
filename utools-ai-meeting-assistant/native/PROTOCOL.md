# Native system-audio protocol

The helper writes framed messages to stdout and reserves stderr for bounded
diagnostics. The preload process writes `stop\n` to stdin for graceful shutdown.

Each frame uses a 12-byte little-endian header:

| Offset | Bytes | Meaning |
| --- | ---: | --- |
| 0 | 4 | ASCII magic `THAU` |
| 4 | 1 | Protocol version (`1`) |
| 5 | 1 | Type: `1` ready, `2` audio, `3` error |
| 6 | 2 | Reserved, zero |
| 8 | 4 | Payload byte length, at most 65536 |

Ready payload:

```json
{"sampleRate":48000,"channels":1,"format":"pcm_s16le"}
```

Audio payloads contain signed 16-bit little-endian mono PCM. Error payloads
contain a stable `code` and a sanitized `message`. The helper must never write
logs or banners to stdout.
