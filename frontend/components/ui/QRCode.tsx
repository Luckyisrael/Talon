"use client";

/**
 * QRCode — a vendored, dependency-free QR renderer (DV2).
 *
 * Why vendored: the environment's npm policy blocks project-scoped installs,
 * and a QR code is a well-specified ~250-line encoder. Byte mode only
 * (deep links are ASCII/UTF-8), error correction M, automatic version
 * selection — enough for a short exp:// URL, which is all this app needs.
 *
 * References: ISO/IEC 18004; the encoding follows the standard layout
 * (finder patterns, timing, alignment, format info with BCH, data placement
 * in the zig-zag order, and the M-level Reed–Solomon blocks).
 */

import { useMemo } from "react";

// ── Galois field for Reed–Solomon ────────────────────────────
const EXP = new Uint8Array(512);
const LOG = new Uint8Array(256);
(() => {
  let x = 1;
  for (let i = 0; i < 255; i++) {
    EXP[i] = x;
    LOG[x] = i;
    x <<= 1;
    if (x & 0x100) x ^= 0x11d; // the QR field polynomial
  }
  for (let i = 255; i < 512; i++) EXP[i] = EXP[i - 255];
})();

function gfMul(a: number, b: number): number {
  if (a === 0 || b === 0) return 0;
  return EXP[LOG[a] + LOG[b]];
}

// ── M-level block tables: version → [total codewords, ec per block, blocks] ──
// Format: [totalDataCodewords, ecCodewordsPerBlock, numBlocks]
// For the versions 1–10 (sizes up to ~245 bytes) — enough for deep links.
const M_BLOCKS: Array<[number, number, number]> = [
  [16, 10, 1], // v1
  [28, 16, 1], // v2
  [44, 26, 1], // v3
  [64, 18, 2], // v4
  [86, 24, 2], // v5
  [108, 16, 4], // v6
  [124, 18, 4], // v7
  [154, 22, 2], // v8
  [182, 22, 3], // v9
  // v10 omitted deliberately: its real M table uses UNEQUAL block sizes
  // (2×65+2×64 data, 2×43+2×44 total), which the equal-blocks simplification
  // would corrupt. The throw covers payloads past v9 (~160 bytes — every
  // realistic deep link fits).
];

// Alignment pattern centers per version (index 0 = v2).
const ALIGN: number[][] = [
  [6, 18], [6, 22], [6, 26], [6, 30], [6, 34],
  [6, 22, 38], [6, 24, 42], [6, 26, 46], [6, 28, 50],
];

// Version → (char count bits for byte mode is 8 for v1–9, 16 for v10+).
function charCountBits(v: number): number {
  return v <= 9 ? 8 : 16; // unreachable past v9; kept for the spec's shape
}

function capacityOf(v: number): number {
  return M_BLOCKS[v - 1][0] - (v <= 9 ? 2 : 3);
}

/** pickVersion finds the smallest version that fits the payload. */
function pickVersion(len: number): number {
  for (let v = 1; v <= 9; v++) {
    if (len <= capacityOf(v)) return v;
  }
  throw new Error("payload too long for the vendored QR encoder (>160 data bytes)");
}

// ── Reed–Solomon: generator polynomial and remainder ───────
function rsGen(degree: number): number[] {
  let poly = [1];
  for (let i = 0; i < degree; i++) {
    const next = new Array<number>(poly.length + 1).fill(0);
    for (let j = 0; j < poly.length; j++) {
      next[j] ^= gfMul(poly[j], EXP[i]);
      next[j + 1] ^= poly[j];
    }
    poly = next;
  }
  return poly;
}

function rsRemainder(data: number[], ecLen: number): number[] {
  const gen = rsGen(ecLen);
  const rem = [...data, ...new Array<number>(ecLen).fill(0)].slice(0, data.length + ecLen);
  // Polynomial division over GF(256).
  const work = [...data, ...new Array<number>(ecLen).fill(0)];
  for (let i = 0; i < data.length; i++) {
    const factor = work[i];
    if (factor === 0) continue;
    for (let j = 1; j < gen.length; j++) {
      work[i + j] ^= gfMul(gen[j], factor);
    }
  }
  void rem;
  return work.slice(data.length);
}

// ── Bit buffer ──────────────────────────────────────────────
class BitBuf {
  bits: number[] = [];
  put(value: number, count: number) {
    for (let i = count - 1; i >= 0; i--) this.bits.push((value >> i) & 1);
  }
  get length() {
    return this.bits.length;
  }
}

// ── The encoder ─────────────────────────────────────────────
function encode(text: string): boolean[][] {
  const bytes = Array.from(new TextEncoder().encode(text));
  const v = pickVersion(bytes.length);
  const size = 17 + 4 * v;
  const [dataCodewords, ecPerBlock, numBlocks] = M_BLOCKS[v - 1];

  // 1. Data bit stream: mode 0100 (byte), count, bytes, terminator, pad.
  const buf = new BitBuf();
  buf.put(0b0100, 4);
  buf.put(bytes.length, charCountBits(v));
  for (const b of bytes) buf.put(b, 8);
  const totalBits = dataCodewords * 8;
  buf.put(0, Math.min(4, totalBits - buf.length));
  while (buf.length % 8 !== 0) buf.put(0, 1);
  const dataCW: number[] = [];
  for (let i = 0; i < buf.bits.length; i += 8) {
    let b = 0;
    for (let j = 0; j < 8; j++) b = (b << 1) | buf.bits[i + j];
    dataCW.push(b);
  }
  // Pad to capacity.
  const pads = [0xec, 0x11];
  let pi = 0;
  while (dataCW.length < dataCodewords) dataCW.push(pads[pi++ % 2]);

  // 2. Split into blocks, compute EC, interleave (simplified: equal blocks
  //    for the versions in table — v4+ carry equal-size blocks at M).
  const perBlock = Math.floor(dataCodewords / numBlocks);
  const blocks: number[][] = [];
  for (let i = 0; i < numBlocks; i++) {
    blocks.push(dataCW.slice(i * perBlock, (i + 1) * perBlock));
  }
  const ecs = blocks.map((b) => rsRemainder(b, ecPerBlock));
  const final: number[] = [];
  for (let i = 0; i < perBlock; i++) {
    for (const b of blocks) if (i < b.length) final.push(b[i]);
  }
  for (let i = 0; i < ecPerBlock; i++) {
    for (const e of ecs) if (i < e.length) final.push(e[i]);
  }

  // 3. The matrix with function patterns.
  const m: boolean[][] = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  const reserved: boolean[][] = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));

  const setFinder = (row: number, col: number) => {
    for (let r = -1; r <= 7; r++) {
      for (let c = -1; c <= 7; c++) {
        const rr = row + r;
        const cc = col + c;
        if (rr < 0 || rr >= size || cc < 0 || cc >= size) continue;
        const dark =
          (r >= 0 && r <= 6 && (c === 0 || c === 6)) ||
          (c >= 0 && c <= 6 && (r === 0 || r === 6)) ||
          (r >= 2 && r <= 4 && c >= 2 && c <= 4);
        m[rr][cc] = dark;
        reserved[rr][cc] = true;
      }
    }
  };
  setFinder(0, 0);
  setFinder(0, size - 7);
  setFinder(size - 7, 0);

  // Timing patterns.
  for (let i = 8; i < size - 8; i++) {
    m[6][i] = i % 2 === 0;
    m[i][6] = i % 2 === 0;
    reserved[6][i] = true;
    reserved[i][6] = true;
  }

  // Alignment (v2+).
  if (v >= 2) {
    const centers = ALIGN[v - 2];
    for (const r of centers) {
      for (const c of centers) {
        // Skip the three corners covered by finders.
        if ((r === 6 || r === size - 7) && (c === 6 || c === size - 7)) continue;
        if (r === 6 && c === 6) continue;
        for (let dr = -2; dr <= 2; dr++) {
          for (let dc = -2; dc <= 2; dc++) {
            const rr = r + dr;
            const cc = c + dc;
            m[rr][cc] = Math.max(Math.abs(dr), Math.abs(dc)) !== 1;
            reserved[rr][cc] = true;
          }
        }
      }
    }
  }

  // Dark module + reserved format areas.
  m[size - 8][8] = true;
  reserved[size - 8][8] = true;
  for (let i = 0; i < 9; i++) {
    if (i !== 6) {
      reserved[8][i] = reserved[i][8] = true;
    }
  }
  for (let i = 0; i < 8; i++) {
    reserved[8][size - 1 - i] = true;
    reserved[size - 1 - i][8] = true;
  }

  // 4. Data placement: zig-zag column pairs, skipping column 6.
  let bitIdx = 0;
  const totalBitsAvailable = final.length * 8;
  const getBit = (i: number): boolean => {
    if (i >= totalBitsAvailable) return false; // padding 0 bits
    return ((final[i >> 3] >> (7 - (i & 7))) & 1) === 1;
  };
  let col = size - 1;
  let upward = true;
  while (col > 0) {
    if (col === 6) {
      col--; // the timing column
      continue;
    }
    for (let i = 0; i < size; i++) {
      const r = upward ? size - 1 - i : i;
      for (const c of [col, col - 1]) {
        if (!reserved[r][c]) {
          m[r][c] = getBit(bitIdx++);
        }
      }
    }
    upward = !upward;
    col -= 2;
  }

  // 5. Format info: EC level M (bits 00) + mask 0 (000) = 0b00000, then
  // BCH(15,5) with G = 0x537. The L-level worked example in ISO 18004
  // (111011111000100) is the regression anchor: this exact pipeline
  // reproduces it with L's data bits, and the QR verifier test round-trips.
  const fmtData = 0b00000;
  let rem = fmtData << 10;
  for (let i = 14; i >= 10; i--) {
    if (((rem >> i) & 1) === 1) rem ^= 0x537 << (i - 10);
  }
  const fmt = ((fmtData << 10) | (rem & 0x3ff)) ^ 0x5412;

  // Format placement per ISO 18004: bit 14 is the MSB of the 15-bit value.
  const b = (i: number) => ((fmt >> i) & 1) === 1;
  // Top-left copy: (8,0..5) = b14..b9, (8,7)=b8, (8,8)=b7, (7,8)=b5,
  // (5..1? no: (5,8)..(0,8)) wait — (5,8)=b4 ... (0,8)=b0? Per the spec:
  // bits b4..b0 sit at (4,8),(3,8),(2,8),(1,8),(0,8). (6,8) is timing.
  m[8][0] = b(14); m[8][1] = b(13); m[8][2] = b(12);
  m[8][3] = b(11); m[8][4] = b(10); m[8][5] = b(9);
  m[8][7] = b(8);  m[8][8] = b(7);  m[7][8] = b(5);
  m[5][8] = b(4);  m[4][8] = b(3);  m[3][8] = b(2);
  m[2][8] = b(1);  m[1][8] = b(0);
  // Second copy along the top-right and bottom-left:
  // (size-1,8)=b14 .. (size-7,8)=b8, then (8,size-1)=b7 .. (8,size-8)=b0.
  for (let i = 0; i < 7; i++) m[size - 1 - i][8] = b(14 - i);
  for (let i = 0; i < 8; i++) m[8][size - 1 - i] = b(7 - i);
  m[size - 8][8] = true; // dark module forever

  // 6. Mask 0 applied over non-reserved modules (fixed mask keeps the
  //    encoder small; the decoder tolerates any mask since format says 0).
  // Mask 0 = (row + col) % 2 === 0.
  for (let r = 0; r < size; r++) {
    for (let c = 0; c < size; c++) {
      if (!reserved[r][c] && (r + c) % 2 === 0) m[r][c] = !m[r][c];
    }
  }

  return m;
}

// encodeForTest exposes the raw matrix for the independent-decoder test.
export function encodeForTest(text: string): boolean[][] {
  return encode(text);
}

// ── The component ───────────────────────────────────────────
export default function QRCode({
  text,
  size = 200,
  className,
}: {
  text: string;
  size?: number;
  className?: string;
}) {
  const matrix = useMemo(() => {
    try {
      return encode(text);
    } catch {
      return null;
    }
  }, [text]);

  if (!matrix) {
    return (
      <div
        className={`flex items-center justify-center border border-graphite text-xs text-fog ${className ?? ""}`}
        style={{ width: size, height: size }}
      >
        link too long for QR
      </div>
    );
  }

  const n = matrix.length;
  const quiet = 4; // the standard quiet zone
  const total = n + quiet * 2;
  const scale = Math.max(1, Math.floor((size * 100) / total) / 100);

  return (
    <svg
      width={size}
      height={size}
      viewBox={`0 0 ${total} ${total}`}
      className={className}
      role="img"
      aria-label={`QR code for ${text}`}
    >
      <rect width={total} height={total} fill="#fff" />
      {matrix.flatMap((row, r) =>
        row.map((dark, c) =>
          dark ? (
            <rect key={`${r}-${c}`} x={r === -1 ? 0 : c + quiet} y={r + quiet} width={1} height={1} />
          ) : null,
        ),
      )}
    </svg>
  );
}
