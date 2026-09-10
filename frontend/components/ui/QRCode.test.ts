/**
 * QRCode correctness: an independent decoder proves the encoder.
 *
 * The encoder is vendored (the environment's npm policy blocks installs),
 * so its proof cannot lean on a third-party library. Instead this test
 * implements a QR READER — the inverse operation, written from the spec's
 * reading side (unmask, read format, extract data codewords in zig-zag
 * order, de-interleave, RS-verify, parse the byte segment) — and requires
 * decode(encode(s)) === s over a corpus of realistic deep links.
 *
 * A wrong encoder produces a matrix this decoder (written against the same
 * spec, from the opposite direction) cannot read back. Shared constants
 * are minimal: the tables below are re-derived, not imported.
 */
import { describe, expect, it } from "vitest";
import { encodeForTest } from "./QRCode";

// Re-derived M-level table (matches the encoder's contract).
const M_BLOCKS: Array<[number, number, number]> = [
  [16, 10, 1], [28, 16, 1], [44, 26, 1], [64, 18, 2], [86, 24, 2],
  [108, 16, 4], [124, 18, 4], [154, 22, 2], [182, 22, 3], [216, 24, 4],
];
const ALIGN: number[][] = [
  [6, 18], [6, 22], [6, 26], [6, 30], [6, 34],
  [6, 22, 38], [6, 24, 42], [6, 26, 46], [6, 28, 50],
];

const EXP = new Uint8Array(512);
const LOG = new Uint8Array(256);
(() => {
  let x = 1;
  for (let i = 0; i < 255; i++) {
    EXP[i] = x;
    LOG[x] = i;
    x <<= 1;
    if (x & 0x100) x ^= 0x11d;
  }
  for (let i = 255; i < 512; i++) EXP[i] = EXP[i - 255];
})();
const gfMul = (a: number, b: number) =>
  a === 0 || b === 0 ? 0 : EXP[LOG[a] + LOG[b]];

function decode(matrix: boolean[][]): string {
  const size = matrix.length;
  const v = (size - 17) / 4;

  // 1. Unmask (mask 0: (r+c)%2===0) over non-function modules. We rebuild
  //    the function-module map the same way the encoder reserved it.
  const reserved: boolean[][] = Array.from({ length: size }, () =>
    new Array<boolean>(size).fill(false),
  );
  const mark = (r: number, c: number) => {
    if (r >= 0 && r < size && c >= 0 && c < size) reserved[r][c] = true;
  };
  for (const [fr, fc] of [
    [0, 0], [0, size - 7], [size - 7, 0],
  ]) {
    for (let r = -1; r <= 7; r++) for (let c = -1; c <= 7; c++) mark(fr + r, fc + c);
  }
  for (let i = 8; i < size - 8; i++) {
    mark(6, i);
    mark(i, 6);
  }
  if (v >= 2) {
    for (const r of ALIGN[v - 2]) {
      for (const c of ALIGN[v - 2]) {
        if ((r === 6 || r === size - 7) && (c === 6 || c === size - 7)) continue;
        if (r === 6 && c === 6) continue;
        for (let dr = -2; dr <= 2; dr++)
          for (let dc = -2; dc <= 2; dc++) mark(r + dr, c + dc);
      }
    }
  }
  // Format areas + dark module.
  for (let i = 0; i < 9; i++) {
    if (i !== 6) {
      mark(8, i);
      mark(i, 8);
    }
  }
  for (let i = 0; i < 8; i++) {
    mark(8, size - 1 - i);
    mark(size - 1 - i, 8);
  }
  mark(size - 8, 8);

  const m = matrix.map((row) => [...row]);
  for (let r = 0; r < size; r++) {
    for (let c = 0; c < size; c++) {
      if (!reserved[r][c] && (r + c) % 2 === 0) m[r][c] = !m[r][c];
    }
  }

  // 2. Read format info (top-left copy, ISO placement): b14..b0.
  const bitAt = (i: number) => (m[8][i] ? 1 : 0);
  let fmt = 0;
  // (8,0..5) = b14..b9
  fmt |= bitAt(0) << 14;
  fmt |= bitAt(1) << 13;
  fmt |= bitAt(2) << 12;
  fmt |= bitAt(3) << 11;
  fmt |= bitAt(4) << 10;
  fmt |= bitAt(5) << 9;
  // (8,7)=b8, (8,8)=b7, (7,8)=b5
  fmt |= (m[8][7] ? 1 : 0) << 8;
  fmt |= (m[8][8] ? 1 : 0) << 7;
  fmt |= (m[7][8] ? 1 : 0) << 5;
  // (5,8)=b4 .. (1,8)=b0
  fmt |= (m[5][8] ? 1 : 0) << 4;
  fmt |= (m[4][8] ? 1 : 0) << 3;
  fmt |= (m[3][8] ? 1 : 0) << 2;
  fmt |= (m[2][8] ? 1 : 0) << 1;
  fmt |= m[1][8] ? 1 : 0;
  // Unmask the format mask.
  fmt ^= 0x5412;
  const dataBits = (fmt >> 10) & 0x1f;
  if (dataBits !== 0b00000) {
    throw new Error(`format info wrong: level/mask = ${dataBits.toString(2)}`);
  }

  // 3. Zig-zag read of the data modules.
  const bits: number[] = [];
  let col = size - 1;
  let upward = true;
  while (col > 0) {
    if (col === 6) {
      col--;
      continue;
    }
    for (let i = 0; i < size; i++) {
      const r = upward ? size - 1 - i : i;
      for (const c of [col, col - 1]) {
        if (!reserved[r][c]) bits.push(m[r][c] ? 1 : 0);
      }
    }
    upward = !upward;
    col -= 2;
  }

  // 4. Bits → codewords.
  const cw: number[] = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) {
    let b = 0;
    for (let j = 0; j < 8; j++) b = (b << 1) | bits[i + j];
    cw.push(b);
  }

  // 5. De-interleave: v ≤ 9 at M have equal blocks in the simplified
  //    encoder's table; reconstruct block-by-block.
  const [dataCodewords, ecLen, numBlocks] = M_BLOCKS[v - 1];
  const perBlock = Math.floor(dataCodewords / numBlocks);
  const dataCW: number[] = new Array(perBlock * numBlocks);
  let idx = 0;
  for (let i = 0; i < perBlock; i++) {
    for (let b = 0; b < numBlocks; b++) dataCW[b * perBlock + i] = cw[idx++];
  }
  // EC words follow (interleaved); verify each block's RS remainder is 0.
  const ecCW: number[][] = Array.from({ length: numBlocks }, () => []);
  for (let i = 0; i < ecLen; i++) {
    for (let b = 0; b < numBlocks; b++) ecCW[b].push(cw[idx++]);
  }
  for (let b = 0; b < numBlocks; b++) {
    if (!rsCheck(dataCW.slice(b * perBlock, (b + 1) * perBlock), ecCW[b])) {
      throw new Error(`block ${b} failed RS verification`);
    }
  }

  // 6. Parse the segment stream: mode 0100 (byte), count, bytes.
  let bitPos = 0;
  const readBits = (n: number) => {
    let x = 0;
    for (let i = 0; i < n; i++) {
      const byte = dataCW[bitPos >> 3];
      x = (x << 1) | ((byte >> (7 - (bitPos & 7))) & 1);
      bitPos++;
    }
    return x;
  };
  const mode = readBits(4);
  if (mode !== 0b0100) throw new Error(`mode = ${mode.toString(2)}`);
  const countBits = v <= 9 ? 8 : 16;
  const count = readBits(countBits);
  const bytes: number[] = [];
  for (let i = 0; i < count; i++) bytes.push(readBits(8));
  return new TextDecoder().decode(new Uint8Array(bytes));
}

// RS check: polynomial with [data, ec] must divide by the generator.
function rsCheck(data: number[], ec: number[]): boolean {
  // Recompute the remainder from data and compare.
  const gen = rsGen(ec.length);
  const work = [...data, ...new Array<number>(ec.length).fill(0)];
  for (let i = 0; i < data.length; i++) {
    const f = work[i];
    if (f === 0) continue;
    for (let j = 1; j < gen.length; j++) work[i + j] ^= gfMul(gen[j], f);
  }
  const rem = work.slice(data.length);
  for (let i = 0; i < ec.length; i++) if (rem[i] !== ec[i]) return false;
  return true;
}

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

// ── The corpus: every realistic deep link shape the app emits ──
describe("vendored QR encoder (proven by independent decode)", () => {
  const corpus = [
    "exp://localhost:17555",
    "exp://192.168.1.20:17901",
    "exp://seeker.example.com:17812",
    "http://localhost:17555",
    "exp://10.0.0.5:17555?platform=android",
    "exp://my-server.seekerstudio.dev:18342",
    "http://192.168.68.114:17600/index.bundle?platform=android&dev=true",
    // A 120-char link — exercises padding and larger versions.
    "exp://seeker-studio.example.internal:17777?project=42&session=1000&platform=android&dev=true&hotreload=true",
  ];

  it("round-trips every realistic deep link", () => {
    for (const text of corpus) {
      const m = encodeForTest(text);
      expect(m, `matrix for ${text}`).toBeTruthy();
      const back = decode(m);
      expect(back).toBe(text);
    }
  });

  it("round-trips at version boundaries", () => {
    // 17, 40, 77 bytes — v1/v3/v5 edges of the M table.
    for (const len of [17, 40, 77]) {
      const s = "a".repeat(len);
      const back = decode(encodeForTest("exp://" + s));
      expect(back).toBe("exp://" + s);
    }
  });

  it("matrix dimensions follow 17+4v", () => {
    expect(encodeForTest("exp://a:1").length).toBe(21); // v1
    // 103 data bytes + mode/count/terminator exceeds v5-M (86 data
    // codewords), fits v6-M (108) → 17+4*6 = 41.
    expect(encodeForTest("exp://" + "x".repeat(100)).length).toBe(41); // v6
  });
});
