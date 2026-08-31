const protocolMagic = Buffer.from("THAU", "ascii");
const protocolVersion = 1;
const headerBytes = 12;
const maxPayloadBytes = 64 * 1024;

const nativeAudioFrameTypes = Object.freeze({
  READY: 1,
  AUDIO: 2,
  ERROR: 3,
});

class NativeAudioProtocolError extends Error {
  constructor(message) {
    super(message);
    this.name = "NativeAudioProtocolError";
  }
}

// NativeAudioFrameDecoder owns framing validation at the process boundary so
// malformed or stale helper output never reaches the renderer audio graph.
class NativeAudioFrameDecoder {
  #buffer = Buffer.alloc(0);

  push(chunk) {
    if (!Buffer.isBuffer(chunk)) {
      throw new TypeError("Native audio protocol chunk must be a Buffer");
    }
    if (chunk.length === 0) {
      return [];
    }
    this.#buffer = this.#buffer.length === 0
      ? chunk
      : Buffer.concat([this.#buffer, chunk], this.#buffer.length + chunk.length);
    const frames = [];
    while (this.#buffer.length >= headerBytes) {
      if (!this.#buffer.subarray(0, protocolMagic.length).equals(protocolMagic)) {
        throw new NativeAudioProtocolError("Native audio frame magic is invalid");
      }
      const version = this.#buffer.readUInt8(4);
      if (version !== protocolVersion) {
        throw new NativeAudioProtocolError(`Unsupported native audio protocol version ${version}`);
      }
      const type = this.#buffer.readUInt8(5);
      if (!Object.values(nativeAudioFrameTypes).includes(type)) {
        throw new NativeAudioProtocolError(`Unknown native audio frame type ${type}`);
      }
      const payloadBytes = this.#buffer.readUInt32LE(8);
      if (payloadBytes > maxPayloadBytes) {
        throw new NativeAudioProtocolError("Native audio frame payload is too large");
      }
      const frameBytes = headerBytes + payloadBytes;
      if (this.#buffer.length < frameBytes) {
        break;
      }
      frames.push({
        type,
        payload: this.#buffer.subarray(headerBytes, frameBytes),
      });
      this.#buffer = this.#buffer.subarray(frameBytes);
    }
    return frames;
  }
}

module.exports = {
  NativeAudioFrameDecoder,
  NativeAudioProtocolError,
  nativeAudioFrameTypes,
};
