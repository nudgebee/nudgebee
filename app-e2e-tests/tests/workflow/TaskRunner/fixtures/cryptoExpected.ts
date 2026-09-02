import { createCipheriv, createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { join } from "node:path";

// The spec types inputs from cryptoInputs.json; every expected value is computed
// here from those same inputs, so nothing expected is ever stored on disk.

const INPUTS_PATH = join(__dirname, "cryptoInputs.json");
const AES_KEY_BYTES = 32;
const GCM_NONCE_BYTES = 12;
const GCM_TAG_BYTES = 16;

export interface CryptoNegativeCase {
  data?: string;
  key?: string;
  algorithm?: string;
  keyEncoding?: string;
  errorPattern: string;
}

export interface CryptoInputs {
  plaintext: string;
  unicodePlaintext: string;
  keyText: string;
  aesKeyBase64: string;
  knownVectorNonceHex: string;
  hashAlgorithms: string[];
  encodeAlgorithms: string[];
  negative: Record<string, CryptoNegativeCase>;
  statusPatterns: { failed: string };
}

export const inputs: CryptoInputs = JSON.parse(readFileSync(INPUTS_PATH, "utf8")) as CryptoInputs;

export function expectedHash(algorithm: string, text: string = inputs.plaintext): string {
  return createHash(algorithm).update(Buffer.from(text, "utf8")).digest("hex");
}

export function expectedEncode(algorithm: string, text: string = inputs.plaintext): string {
  return Buffer.from(text, "utf8").toString(algorithm as BufferEncoding);
}

export function expectedDecode(algorithm: string, encoded: string): string {
  return Buffer.from(encoded, algorithm as BufferEncoding).toString("utf8");
}

// The key string to type per encoding: text is hashed to 32 bytes by the
// product, base64 and hex hand over the same 32 raw bytes already decoded.
export function keyFor(encoding?: string): string {
  if (!encoding) return inputs.keyText;
  return Buffer.from(inputs.aesKeyBase64, "base64").toString(encoding as BufferEncoding);
}

// The random nonce rules out pinning the ciphertext, but aes-256-gcm's
// nonce||ciphertext||tag layout makes its byte length fixed and checkable.
export function expectedCiphertextByteLength(text: string = inputs.plaintext): number {
  return GCM_NONCE_BYTES + Buffer.byteLength(text, "utf8") + GCM_TAG_BYTES;
}

// The exact layout crypto.encrypt emits. The nonce is pinned only so this
// vector is reproducible; the product picks a random one.
export function knownCiphertext(): string {
  const nonce = Buffer.from(inputs.knownVectorNonceHex, "hex");
  const cipher = createCipheriv("aes-256-gcm", createHash("sha256").update(inputs.keyText).digest(), nonce);
  const body = Buffer.concat([cipher.update(Buffer.from(inputs.plaintext, "utf8")), cipher.final()]);
  return Buffer.concat([nonce, body, cipher.getAuthTag()]).toString("base64");
}

// A wrong-length key would make every encrypt case fail with the error the
// undersized-key case is supposed to be alone in producing.
export function assertInputsAreUsable(): void {
  const keyLength = Buffer.from(inputs.aesKeyBase64, "base64").length;
  if (keyLength !== AES_KEY_BYTES) {
    throw new Error(`cryptoInputs.json: aesKeyBase64 decodes to ${keyLength} bytes; aes-256 needs exactly ${AES_KEY_BYTES}.`);
  }
}
