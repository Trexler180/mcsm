// Focused HOCON (.conf) parser for the subset mod configs use (e.g. TabTPS):
//   key=value · key: value · key { ... } · arrays · quoted & unquoted strings
//   · `#` and `//` comments (captured as help text).
// Scalars carry source spans so saving is an in-place value splice (comments and
// layout preserved). Anything it can't model confidently → caller drops to raw.

import type { ConfigNode, PathKey, Primitive } from "./model";

const WS = new Set([" ", "\t", "\n", "\r", "\f"]);
const KEY_STOP = new Set(["=", ":", "{", "}", "[", "]", ",", "\n"]);
const VAL_STOP = new Set([",", "\n", "}", "]"]);

function classify(raw: string): { type: ConfigNode["type"]; value: Primitive } {
  if (raw === "true" || raw === "false") return { type: "boolean", value: raw === "true" };
  if (raw === "null") return { type: "null", value: null };
  if (raw !== "" && !Number.isNaN(Number(raw)) && /^[+-]?(\d|\.)/.test(raw))
    return { type: "number", value: Number(raw) };
  return { type: "string", value: raw };
}

function splitDotted(key: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quote: string | null = null;
  for (let i = 0; i < key.length; i++) {
    const c = key[i];
    if (quote) {
      if (c === quote) quote = null;
      else cur += c;
    } else if (c === '"' || c === "'") {
      quote = c;
    } else if (c === ".") {
      out.push(cur);
      cur = "";
    } else cur += c;
  }
  out.push(cur);
  return out.filter((s) => s.length > 0);
}

class HoconParser {
  i = 0;
  pendingComments: string[] = [];
  constructor(public s: string) {}

  error(msg: string): never {
    throw new Error(`${msg} at ${this.i}`);
  }

  takeDoc(): string | undefined {
    if (!this.pendingComments.length) return undefined;
    const d = this.pendingComments.join("\n").trim();
    this.pendingComments = [];
    return d || undefined;
  }

  ws(captureComments = true) {
    const s = this.s;
    while (this.i < s.length) {
      const c = s[this.i];
      if (WS.has(c)) {
        this.i++;
        continue;
      }
      if (c === "#" || (c === "/" && s[this.i + 1] === "/")) {
        this.i += c === "#" ? 1 : 2;
        const start = this.i;
        while (this.i < s.length && s[this.i] !== "\n") this.i++;
        if (captureComments) this.pendingComments.push(s.slice(start, this.i).trim());
        continue;
      }
      if (c === "/" && s[this.i + 1] === "*") {
        this.i += 2;
        const start = this.i;
        while (this.i < s.length && !(s[this.i] === "*" && s[this.i + 1] === "/"))
          this.i++;
        if (captureComments) this.pendingComments.push(s.slice(start, this.i).trim());
        this.i += 2;
        continue;
      }
      break;
    }
  }

  parse(): ConfigNode {
    const root: ConfigNode = { type: "object", key: null, index: null, path: [], children: [] };
    this.parseMembers(root, false);
    return root;
  }

  parseMembers(container: ConfigNode, braced: boolean) {
    if (braced) this.i++; // consume '{'
    for (;;) {
      this.ws();
      if (this.i >= this.s.length) {
        if (braced) this.error("unterminated object");
        break;
      }
      if (this.s[this.i] === "}") {
        this.i++;
        break;
      }
      this.parseMember(container);
      // member separators: commas / newlines already eaten by ws()
      this.ws(false);
      if (this.s[this.i] === ",") this.i++;
    }
  }

  parseMember(container: ConfigNode) {
    const doc = this.takeDoc();
    const keyRaw = this.readKey();
    if (keyRaw === "") this.error("expected key");
    const segments = splitDotted(keyRaw);

    // Walk/destructure dotted keys into nested objects under `container`.
    let parent = container;
    const basePath = [...container.path];
    for (let s = 0; s < segments.length - 1; s++) {
      const seg = segments[s];
      basePath.push(seg);
      let child = parent.children?.find((c) => c.key === seg && c.type === "object");
      if (!child) {
        child = { type: "object", key: seg, index: null, path: [...basePath], children: [] };
        parent.children!.push(child);
      }
      parent = child;
    }
    const leafKey = segments[segments.length - 1];
    const valuePath = [...basePath, leafKey];

    this.ws();
    let sepConsumed = false;
    if (this.s[this.i] === "=" || this.s[this.i] === ":") {
      this.i++;
      sepConsumed = true;
      this.ws();
    }
    void sepConsumed;

    const node = this.parseValue(valuePath, leafKey, null);
    if (doc) node.doc = doc;
    const existing = parent.children?.findIndex((c) => c.key === leafKey) ?? -1;
    if (existing >= 0) parent.children![existing] = node;
    else parent.children!.push(node);
  }

  readKey(): string {
    this.ws();
    const c = this.s[this.i];
    if (c === '"' || c === "'") return this.readString().value;
    const start = this.i;
    while (this.i < this.s.length && !WS.has(this.s[this.i]) && !KEY_STOP.has(this.s[this.i]))
      this.i++;
    return this.s.slice(start, this.i).trim();
  }

  parseValue(path: PathKey[], key: string | null, index: number | null): ConfigNode {
    this.ws();
    const c = this.s[this.i];
    if (c === "{") {
      const node: ConfigNode = { type: "object", key, index, path, children: [] };
      this.parseMembers(node, true);
      return node;
    }
    if (c === "[") return this.parseArray(path, key, index);
    if (c === '"' || c === "'") {
      const start = this.i;
      const { value } = this.readString();
      return {
        type: "string", key, index, path, value, original: value,
        raw: this.s.slice(start, this.i), start, end: this.i, editable: true,
      };
    }
    // Unquoted scalar: read to end of line / delimiter / comment.
    const start = this.i;
    while (this.i < this.s.length) {
      const ch = this.s[this.i];
      if (VAL_STOP.has(ch)) break;
      if (ch === "#" || (ch === "/" && this.s[this.i + 1] === "/")) break;
      this.i++;
    }
    let end = this.i;
    while (end > start && WS.has(this.s[end - 1])) end--;
    const raw = this.s.slice(start, end);
    const { type, value } = classify(raw);
    return {
      type, key, index, path, value, original: value,
      raw, start, end, editable: true,
    };
  }

  parseArray(path: PathKey[], key: string | null, index: number | null): ConfigNode {
    this.i++; // [
    const children: ConfigNode[] = [];
    let idx = 0;
    for (;;) {
      this.ws();
      if (this.i >= this.s.length) this.error("unterminated array");
      if (this.s[this.i] === "]") {
        this.i++;
        break;
      }
      children.push(this.parseValue([...path, idx], null, idx));
      idx++;
      this.ws(false);
      if (this.s[this.i] === ",") this.i++;
    }
    return { type: "array", key, index, path, children };
  }

  readString(): { value: string } {
    const quote = this.s[this.i];
    this.i++;
    let out = "";
    while (this.i < this.s.length) {
      const c = this.s[this.i];
      if (c === "\\") {
        const n = this.s[this.i + 1];
        const map: Record<string, string> = { n: "\n", t: "\t", r: "\r", '"': '"', "'": "'", "\\": "\\" };
        out += map[n] ?? n ?? "";
        this.i += 2;
        continue;
      }
      if (c === quote) {
        this.i++;
        return { value: out };
      }
      out += c;
      this.i++;
    }
    this.error("unterminated string");
  }
}

export function parseHocon(text: string): ConfigNode {
  return new HoconParser(text).parse();
}

/** Serialize a scalar as a HOCON value token, matching original quote style. */
export function hoconScalar(value: Primitive, node: ConfigNode): string {
  if (value === null) return "null";
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "number" && node.type === "number") return String(value);
  const s = String(value);
  const wasQuoted = node.raw?.startsWith('"') || node.raw?.startsWith("'");
  // Quote if it was quoted before, or if bare form would be ambiguous/unsafe.
  if (
    wasQuoted ||
    s === "" ||
    /[",:=#{}[\]]/.test(s) ||
    /^\s|\s$/.test(s) ||
    /^(true|false|null)$/.test(s) ||
    !Number.isNaN(Number(s))
  ) {
    return `"${s.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
  }
  return s;
}
