/* A small QR code encoder for the Remote access page (spec 57, REQ-RA-083).
   The dashboard's CSP is default-src 'self', so the encoder ships with the app.
   It follows ISO/IEC 18004 the same way Project Nayuki's qrcodegen does: byte
   mode, error correction level M, versions 1 to 40, and the mask with the
   lowest penalty. Only what the page needs is here: encodeQr returns the
   module grid (true = dark). */

const ECC_M_CODEWORDS_PER_BLOCK = [
  -1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26,
  26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28,
  28, 28, 28,
];
const ECC_M_BLOCKS = [
  -1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17,
  18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49,
];
/* Format bits of level M are 00. */
const ECC_M_FORMAT = 0;

/* at reads a number the index is known to hold (the tables and buffers are
   sized up front); it keeps noUncheckedIndexedAccess satisfied. */
function at(list: readonly number[], index: number): number {
  return list[index] ?? 0;
}

function rawDataModules(version: number): number {
  let result = (16 * version + 128) * version + 64;
  if (version >= 2) {
    const align = Math.floor(version / 7) + 2;
    result -= (25 * align - 10) * align - 55;
    if (version >= 7) {
      result -= 36;
    }
  }
  return result;
}

function dataCodewords(version: number): number {
  return (
    Math.floor(rawDataModules(version) / 8) -
    at(ECC_M_CODEWORDS_PER_BLOCK, version) * at(ECC_M_BLOCKS, version)
  );
}

/* Reed-Solomon over GF(2^8) with the polynomial 0x11D. */
function gfMultiply(x: number, y: number): number {
  let z = 0;
  for (let i = 7; i >= 0; i--) {
    z = (z << 1) ^ ((z >>> 7) * 0x11d);
    z ^= ((y >>> i) & 1) * x;
  }
  return z & 0xff;
}

function rsDivisor(degree: number): number[] {
  const result = new Array<number>(degree).fill(0);
  result[degree - 1] = 1;
  let root = 1;
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < result.length; j++) {
      let value = gfMultiply(at(result, j), root);
      if (j + 1 < result.length) {
        value ^= at(result, j + 1);
      }
      result[j] = value;
    }
    root = gfMultiply(root, 0x02);
  }
  return result;
}

function rsRemainder(data: number[], divisor: number[]): number[] {
  const result = divisor.map(() => 0);
  for (const b of data) {
    const factor = b ^ (result.shift() ?? 0);
    result.push(0);
    divisor.forEach((coef, i) => {
      result[i] = at(result, i) ^ gfMultiply(coef, factor);
    });
  }
  return result;
}

function utf8Bytes(text: string): number[] {
  return Array.from(new TextEncoder().encode(text));
}

function addEcc(data: number[], version: number): number[] {
  const numBlocks = at(ECC_M_BLOCKS, version);
  const blockEcc = at(ECC_M_CODEWORDS_PER_BLOCK, version);
  const rawCodewords = Math.floor(rawDataModules(version) / 8);
  const numShort = numBlocks - (rawCodewords % numBlocks);
  const shortLen = Math.floor(rawCodewords / numBlocks);
  const divisor = rsDivisor(blockEcc);
  const blocks: number[][] = [];
  for (let i = 0, k = 0; i < numBlocks; i++) {
    const dat = data.slice(k, k + shortLen - blockEcc + (i < numShort ? 0 : 1));
    k += dat.length;
    const ecc = rsRemainder(dat, divisor);
    if (i < numShort) {
      dat.push(0);
    }
    blocks.push(dat.concat(ecc));
  }
  const result: number[] = [];
  const blockLen = blocks[0] ? blocks[0].length : 0;
  for (let i = 0; i < blockLen; i++) {
    blocks.forEach((block, j) => {
      if (i !== shortLen - blockEcc || j >= numShort) {
        result.push(at(block, i));
      }
    });
  }
  return result;
}

class Grid {
  readonly size: number;
  /* Row-major module colours and function-pattern flags. */
  readonly dark: boolean[];
  readonly fixed: boolean[];

  constructor(readonly version: number) {
    this.size = version * 4 + 17;
    this.dark = new Array<boolean>(this.size * this.size).fill(false);
    this.fixed = new Array<boolean>(this.size * this.size).fill(false);
  }

  get(x: number, y: number): boolean {
    return this.dark[y * this.size + x] ?? false;
  }

  isFunction(x: number, y: number): boolean {
    return this.fixed[y * this.size + x] ?? false;
  }

  set(x: number, y: number, dark: boolean) {
    this.dark[y * this.size + x] = dark;
  }

  setFunction(x: number, y: number, dark: boolean) {
    this.set(x, y, dark);
    this.fixed[y * this.size + x] = true;
  }

  rows(): boolean[][] {
    return Array.from({ length: this.size }, (_, y) =>
      this.dark.slice(y * this.size, (y + 1) * this.size)
    );
  }

  alignmentPositions(): number[] {
    if (this.version === 1) {
      return [];
    }
    const count = Math.floor(this.version / 7) + 2;
    const step =
      this.version === 32
        ? 26
        : Math.ceil((this.version * 4 + 4) / (count * 2 - 2)) * 2;
    const result = [6];
    for (let pos = this.size - 7; result.length < count; pos -= step) {
      result.splice(1, 0, pos);
    }
    return result;
  }

  drawFunctionPatterns() {
    for (let i = 0; i < this.size; i++) {
      this.setFunction(6, i, i % 2 === 0);
      this.setFunction(i, 6, i % 2 === 0);
    }
    this.drawFinder(3, 3);
    this.drawFinder(this.size - 4, 3);
    this.drawFinder(3, this.size - 4);
    const align = this.alignmentPositions();
    const n = align.length;
    for (let i = 0; i < n; i++) {
      for (let j = 0; j < n; j++) {
        if (
          (i === 0 && j === 0) ||
          (i === 0 && j === n - 1) ||
          (i === n - 1 && j === 0)
        ) {
          continue;
        }
        this.drawAlignment(at(align, i), at(align, j));
      }
    }
    this.drawFormatBits(0);
    this.drawVersion();
  }

  drawFinder(x: number, y: number) {
    for (let dy = -4; dy <= 4; dy++) {
      for (let dx = -4; dx <= 4; dx++) {
        const dist = Math.max(Math.abs(dx), Math.abs(dy));
        const xx = x + dx;
        const yy = y + dy;
        if (xx >= 0 && xx < this.size && yy >= 0 && yy < this.size) {
          this.setFunction(xx, yy, dist !== 2 && dist !== 4);
        }
      }
    }
  }

  drawAlignment(x: number, y: number) {
    for (let dy = -2; dy <= 2; dy++) {
      for (let dx = -2; dx <= 2; dx++) {
        this.setFunction(x + dx, y + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
      }
    }
  }

  drawFormatBits(mask: number) {
    const data = (ECC_M_FORMAT << 3) | mask;
    let rem = data;
    for (let i = 0; i < 10; i++) {
      rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    }
    const bits = ((data << 10) | rem) ^ 0x5412;
    const bit = (i: number) => ((bits >>> i) & 1) !== 0;
    for (let i = 0; i <= 5; i++) {
      this.setFunction(8, i, bit(i));
    }
    this.setFunction(8, 7, bit(6));
    this.setFunction(8, 8, bit(7));
    this.setFunction(7, 8, bit(8));
    for (let i = 9; i < 15; i++) {
      this.setFunction(14 - i, 8, bit(i));
    }
    for (let i = 0; i < 8; i++) {
      this.setFunction(this.size - 1 - i, 8, bit(i));
    }
    for (let i = 8; i < 15; i++) {
      this.setFunction(8, this.size - 15 + i, bit(i));
    }
    this.setFunction(8, this.size - 8, true);
  }

  drawVersion() {
    if (this.version < 7) {
      return;
    }
    let rem = this.version;
    for (let i = 0; i < 12; i++) {
      rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25);
    }
    const bits = (this.version << 12) | rem;
    for (let i = 0; i < 18; i++) {
      const dark = ((bits >>> i) & 1) !== 0;
      const a = this.size - 11 + (i % 3);
      const b = Math.floor(i / 3);
      this.setFunction(a, b, dark);
      this.setFunction(b, a, dark);
    }
  }

  drawCodewords(data: number[]) {
    let i = 0;
    for (let right = this.size - 1; right >= 1; right -= 2) {
      if (right === 6) {
        right = 5;
      }
      for (let vert = 0; vert < this.size; vert++) {
        for (let j = 0; j < 2; j++) {
          const x = right - j;
          const upward = ((right + 1) & 2) === 0;
          const y = upward ? this.size - 1 - vert : vert;
          if (!this.isFunction(x, y) && i < data.length * 8) {
            this.set(x, y, ((at(data, i >>> 3) >>> (7 - (i & 7))) & 1) !== 0);
            i++;
          }
        }
      }
    }
  }

  applyMask(mask: number) {
    for (let y = 0; y < this.size; y++) {
      for (let x = 0; x < this.size; x++) {
        let invert: boolean;
        switch (mask) {
          case 0: invert = (x + y) % 2 === 0; break;
          case 1: invert = y % 2 === 0; break;
          case 2: invert = x % 3 === 0; break;
          case 3: invert = (x + y) % 3 === 0; break;
          case 4: invert = (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0; break;
          case 5: invert = ((x * y) % 2) + ((x * y) % 3) === 0; break;
          case 6: invert = (((x * y) % 2) + ((x * y) % 3)) % 2 === 0; break;
          default: invert = (((x + y) % 2) + ((x * y) % 3)) % 2 === 0; break;
        }
        if (!this.isFunction(x, y) && invert) {
          this.set(x, y, !this.get(x, y));
        }
      }
    }
  }

  penalty(): number {
    const size = this.size;
    const m = this.rows();
    let result = 0;
    const runPenalty = (line: boolean[]) => {
      let score = 0;
      let runColor = false;
      let runLen = 0;
      const history = [0, 0, 0, 0, 0, 0, 0];
      const addHistory = (len: number) => {
        if (at(history, 0) === 0) {
          len += size;
        }
        history.pop();
        history.unshift(len);
      };
      const countPatterns = () => {
        const h = (k: number) => at(history, k);
        const n = h(1);
        const core =
          n > 0 && h(2) === n && h(3) === n * 3 && h(4) === n && h(5) === n;
        return (
          (core && h(0) >= n * 4 && h(6) >= n ? 1 : 0) +
          (core && h(6) >= n * 4 && h(0) >= n ? 1 : 0)
        );
      };
      for (const color of line) {
        if (color === runColor) {
          runLen++;
          if (runLen === 5) {
            score += 3;
          } else if (runLen > 5) {
            score++;
          }
        } else {
          addHistory(runLen);
          if (!runColor) {
            score += countPatterns() * 40;
          }
          runColor = color;
          runLen = 1;
        }
      }
      if (runColor) {
        addHistory(runLen);
        runLen = 0;
      }
      runLen += size;
      addHistory(runLen);
      score += countPatterns() * 40;
      return score;
    };
    for (const row of m) {
      result += runPenalty(row);
    }
    for (let x = 0; x < size; x++) {
      result += runPenalty(m.map((row) => row[x] ?? false));
    }
    for (let y = 0; y < size - 1; y++) {
      for (let x = 0; x < size - 1; x++) {
        const c = this.get(x, y);
        if (
          c === this.get(x + 1, y) &&
          c === this.get(x, y + 1) &&
          c === this.get(x + 1, y + 1)
        ) {
          result += 3;
        }
      }
    }
    const dark = this.dark.filter(Boolean).length;
    const total = size * size;
    const k = Math.ceil(Math.abs(dark * 20 - total * 10) / total) - 1;
    result += k * 10;
    return result;
  }
}

/* encodeQr returns the dark modules of text as a square grid, without the
   quiet zone. It throws when text does not fit in version 40. */
export function encodeQr(text: string): boolean[][] {
  const bytes = utf8Bytes(text);
  let version = 1;
  for (; version <= 40; version++) {
    const countBits = version <= 9 ? 8 : 16;
    const needed = 4 + countBits + bytes.length * 8;
    if (needed <= dataCodewords(version) * 8) {
      break;
    }
  }
  if (version > 40) {
    throw new Error("text is too long for a QR code");
  }
  const capacityBits = dataCodewords(version) * 8;
  const bits: number[] = [];
  const append = (value: number, length: number) => {
    for (let i = length - 1; i >= 0; i--) {
      bits.push((value >>> i) & 1);
    }
  };
  append(0x4, 4);
  append(bytes.length, version <= 9 ? 8 : 16);
  bytes.forEach((b) => append(b, 8));
  append(0, Math.min(4, capacityBits - bits.length));
  append(0, (8 - (bits.length % 8)) % 8);
  for (let pad = 0xec; bits.length < capacityBits; pad ^= 0xec ^ 0x11) {
    append(pad, 8);
  }
  const data: number[] = [];
  for (let i = 0; i < bits.length; i += 8) {
    let b = 0;
    for (let j = 0; j < 8; j++) {
      b = (b << 1) | at(bits, i + j);
    }
    data.push(b);
  }

  const codewords = addEcc(data, version);
  let best: boolean[][] | null = null;
  let bestPenalty = Infinity;
  for (let mask = 0; mask < 8; mask++) {
    const grid = new Grid(version);
    grid.drawFunctionPatterns();
    grid.drawCodewords(codewords);
    grid.applyMask(mask);
    grid.drawFormatBits(mask);
    const score = grid.penalty();
    if (score < bestPenalty) {
      bestPenalty = score;
      best = grid.rows();
    }
  }
  return best ?? [];
}

/* qrPath draws the dark modules as one SVG path, one unit per module, offset
   by the quiet zone. */
export function qrPath(grid: boolean[][], quiet = 4): string {
  const parts: string[] = [];
  grid.forEach((row, y) => {
    let x = 0;
    while (x < row.length) {
      if (!row[x]) {
        x++;
        continue;
      }
      const start = x;
      while (x < row.length && row[x]) {
        x++;
      }
      parts.push(
        "M" + (start + quiet) + " " + (y + quiet) + "h" + (x - start) + "v1h-" + (x - start) + "z"
      );
    }
  });
  return parts.join("");
}
