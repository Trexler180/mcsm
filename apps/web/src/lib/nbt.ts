// Minecraft NBT (Named Binary Tag) codec — Java edition, big-endian.
// No dependencies. Gzip handled by the browser's native (De)CompressionStream.
//
// Reference: https://minecraft.wiki/w/NBT_format
// Longs use BigInt because they exceed JS Number's 53-bit safe integer range.

export const TAG_END = 0;
export const TAG_BYTE = 1;
export const TAG_SHORT = 2;
export const TAG_INT = 3;
export const TAG_LONG = 4;
export const TAG_FLOAT = 5;
export const TAG_DOUBLE = 6;
export const TAG_BYTE_ARRAY = 7;
export const TAG_STRING = 8;
export const TAG_LIST = 9;
export const TAG_COMPOUND = 10;
export const TAG_INT_ARRAY = 11;
export const TAG_LONG_ARRAY = 12;

export const TAG_NAMES: Record<number, string> = {
  [TAG_END]: "End",
  [TAG_BYTE]: "Byte",
  [TAG_SHORT]: "Short",
  [TAG_INT]: "Int",
  [TAG_LONG]: "Long",
  [TAG_FLOAT]: "Float",
  [TAG_DOUBLE]: "Double",
  [TAG_BYTE_ARRAY]: "ByteArray",
  [TAG_STRING]: "String",
  [TAG_LIST]: "List",
  [TAG_COMPOUND]: "Compound",
  [TAG_INT_ARRAY]: "IntArray",
  [TAG_LONG_ARRAY]: "LongArray",
};

// A named entry inside a Compound. Order is preserved for byte-faithful round-trips.
export interface NbtEntry {
  name: string;
  tag: NbtTag;
}

// A single tag. `value`'s shape depends on `type`:
//   byte/short/int/float/double -> number
//   long                        -> bigint
//   string                      -> string
//   byteArray/intArray          -> number[]
//   longArray                   -> bigint[]
//   list                        -> NbtTag[]   (every item shares `listType`)
//   compound                    -> NbtEntry[]
export interface NbtTag {
  type: number;
  value: unknown;
  listType?: number; // only for TAG_LIST
}

export interface NbtRoot {
  name: string;
  tag: NbtTag; // typically a Compound
}

// ── Reader ────────────────────────────────────────────────────────────────

class Reader {
  private view: DataView;
  private off = 0;
  constructor(private buf: Uint8Array) {
    this.view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
  }
  u8() {
    return this.view.getUint8(this.off++);
  }
  i8() {
    return this.view.getInt8(this.off++);
  }
  i16() {
    const v = this.view.getInt16(this.off);
    this.off += 2;
    return v;
  }
  i32() {
    const v = this.view.getInt32(this.off);
    this.off += 4;
    return v;
  }
  i64() {
    const v = this.view.getBigInt64(this.off);
    this.off += 8;
    return v;
  }
  f32() {
    const v = this.view.getFloat32(this.off);
    this.off += 4;
    return v;
  }
  f64() {
    const v = this.view.getFloat64(this.off);
    this.off += 8;
    return v;
  }
  // Java modified UTF-8: NUL is 0xC0 0x80, supplementary chars are surrogate pairs.
  str() {
    const len = this.view.getUint16(this.off);
    this.off += 2;
    let out = "";
    const end = this.off + len;
    while (this.off < end) {
      const a = this.u8();
      if (a < 0x80) {
        out += String.fromCharCode(a);
      } else if ((a & 0xe0) === 0xc0) {
        const b = this.u8();
        out += String.fromCharCode(((a & 0x1f) << 6) | (b & 0x3f));
      } else {
        const b = this.u8();
        const c = this.u8();
        out += String.fromCharCode(
          ((a & 0x0f) << 12) | ((b & 0x3f) << 6) | (c & 0x3f),
        );
      }
    }
    return out;
  }
  payload(type: number): unknown {
    switch (type) {
      case TAG_BYTE:
        return this.i8();
      case TAG_SHORT:
        return this.i16();
      case TAG_INT:
        return this.i32();
      case TAG_LONG:
        return this.i64();
      case TAG_FLOAT:
        return this.f32();
      case TAG_DOUBLE:
        return this.f64();
      case TAG_BYTE_ARRAY: {
        const n = this.i32();
        const a: number[] = new Array(n);
        for (let i = 0; i < n; i++) a[i] = this.i8();
        return a;
      }
      case TAG_STRING:
        return this.str();
      case TAG_LIST: {
        const elem = this.u8();
        const n = this.i32();
        const items: NbtTag[] = new Array(n);
        for (let i = 0; i < n; i++)
          items[i] = { type: elem, value: this.payload(elem) };
        return items;
      }
      case TAG_COMPOUND: {
        const entries: NbtEntry[] = [];
        for (;;) {
          const t = this.u8();
          if (t === TAG_END) break;
          const name = this.str();
          entries.push({ name, tag: { type: t, value: this.payload(t) } });
        }
        return entries;
      }
      case TAG_INT_ARRAY: {
        const n = this.i32();
        const a: number[] = new Array(n);
        for (let i = 0; i < n; i++) a[i] = this.i32();
        return a;
      }
      case TAG_LONG_ARRAY: {
        const n = this.i32();
        const a: bigint[] = new Array(n);
        for (let i = 0; i < n; i++) a[i] = this.i64();
        return a;
      }
      default:
        throw new Error(`Unknown NBT tag type ${type} at offset ${this.off}`);
    }
  }
}

// ── Writer ──────────────────────────────────────────────────────────────

class Writer {
  private bytes: number[] = [];
  private scratch = new DataView(new ArrayBuffer(8));
  u8(v: number) {
    this.bytes.push(v & 0xff);
  }
  i16(v: number) {
    this.bytes.push((v >> 8) & 0xff, v & 0xff);
  }
  i32(v: number) {
    this.bytes.push((v >> 24) & 0xff, (v >> 16) & 0xff, (v >> 8) & 0xff, v & 0xff);
  }
  i64(v: bigint) {
    this.scratch.setBigInt64(0, BigInt.asIntN(64, v));
    for (let i = 0; i < 8; i++) this.bytes.push(this.scratch.getUint8(i));
  }
  f32(v: number) {
    this.scratch.setFloat32(0, v);
    for (let i = 0; i < 4; i++) this.bytes.push(this.scratch.getUint8(i));
  }
  f64(v: number) {
    this.scratch.setFloat64(0, v);
    for (let i = 0; i < 8; i++) this.bytes.push(this.scratch.getUint8(i));
  }
  str(s: string) {
    const enc: number[] = [];
    for (let i = 0; i < s.length; i++) {
      const c = s.charCodeAt(i);
      if (c >= 0x0001 && c <= 0x007f) {
        enc.push(c);
      } else if (c <= 0x07ff) {
        enc.push(0xc0 | (c >> 6), 0x80 | (c & 0x3f));
      } else {
        enc.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 0x3f), 0x80 | (c & 0x3f));
      }
    }
    this.i16(enc.length);
    for (const b of enc) this.bytes.push(b);
  }
  payload(tag: NbtTag) {
    switch (tag.type) {
      case TAG_BYTE:
        this.u8(tag.value as number);
        break;
      case TAG_SHORT:
        this.i16(tag.value as number);
        break;
      case TAG_INT:
        this.i32(tag.value as number);
        break;
      case TAG_LONG:
        this.i64(tag.value as bigint);
        break;
      case TAG_FLOAT:
        this.f32(tag.value as number);
        break;
      case TAG_DOUBLE:
        this.f64(tag.value as number);
        break;
      case TAG_BYTE_ARRAY: {
        const a = tag.value as number[];
        this.i32(a.length);
        for (const v of a) this.u8(v);
        break;
      }
      case TAG_STRING:
        this.str(tag.value as string);
        break;
      case TAG_LIST: {
        const items = tag.value as NbtTag[];
        const elem = items.length ? items[0].type : tag.listType ?? TAG_END;
        this.u8(elem);
        this.i32(items.length);
        for (const it of items) this.payload(it);
        break;
      }
      case TAG_COMPOUND: {
        for (const e of tag.value as NbtEntry[]) {
          this.u8(e.tag.type);
          this.str(e.name);
          this.payload(e.tag);
        }
        this.u8(TAG_END);
        break;
      }
      case TAG_INT_ARRAY: {
        const a = tag.value as number[];
        this.i32(a.length);
        for (const v of a) this.i32(v);
        break;
      }
      case TAG_LONG_ARRAY: {
        const a = tag.value as bigint[];
        this.i32(a.length);
        for (const v of a) this.i64(v);
        break;
      }
      default:
        throw new Error(`Cannot write unknown tag type ${tag.type}`);
    }
  }
  done() {
    return new Uint8Array(this.bytes);
  }
}

// ── Public API ──────────────────────────────────────────────────────────

export function isGzip(buf: Uint8Array): boolean {
  return buf.length >= 2 && buf[0] === 0x1f && buf[1] === 0x8b;
}

async function gunzip(buf: Uint8Array): Promise<Uint8Array> {
  const ds = new DecompressionStream("gzip");
  const stream = new Blob([buf as BlobPart]).stream().pipeThrough(ds);
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

async function gzip(buf: Uint8Array): Promise<Uint8Array> {
  const cs = new CompressionStream("gzip");
  const stream = new Blob([buf as BlobPart]).stream().pipeThrough(cs);
  return new Uint8Array(await new Response(stream).arrayBuffer());
}

export interface ParsedNbt {
  root: NbtRoot;
  gzipped: boolean;
}

// Parse raw file bytes into an editable tree. Transparently gunzips if needed.
export async function parseNbt(raw: Uint8Array): Promise<ParsedNbt> {
  const gzipped = isGzip(raw);
  const data = gzipped ? await gunzip(raw) : raw;
  const r = new Reader(data);
  const type = r.u8();
  if (type === TAG_END) throw new Error("Empty NBT data");
  const name = r.str();
  const value = r.payload(type);
  return { root: { name, tag: { type, value } }, gzipped };
}

// Serialize the tree back to bytes, re-gzipping if the original was gzipped.
export async function serializeNbt(
  root: NbtRoot,
  gzipped: boolean,
): Promise<Uint8Array> {
  const w = new Writer();
  w.u8(root.tag.type);
  w.str(root.name);
  w.payload(root.tag);
  const out = w.done();
  return gzipped ? await gzip(out) : out;
}
