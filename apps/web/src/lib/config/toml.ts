// Focused TOML parser for the subset mod configs use: [tables], [nested.tables],
// [[array.tables]], dotted keys, single-line string/number/bool/inline-array
// values, and `# comments` (which become help text). Anything it can't model
// confidently is surfaced read-only rather than risking a bad edit. Saves are
// in-place span replacements, so comments and layout survive untouched.

import type { ConfigNode, PathKey, Primitive } from "./model";

function classifyLiteral(raw: string): { type: ConfigNode["type"]; value: Primitive } {
  if (raw === "true" || raw === "false") return { type: "boolean", value: raw === "true" };
  if (/^[+-]?(\d[\d_]*|0x[0-9a-fA-F_]+|0o[0-7_]+|0b[01_]+)$/.test(raw)) {
    return { type: "number", value: Number(raw.replace(/_/g, "")) };
  }
  if (/^[+-]?(\d[\d_]*\.?[\d_]*([eE][+-]?\d+)?|\.\d+|inf|nan)$/.test(raw)) {
    const v = Number(raw.replace(/_/g, ""));
    if (!Number.isNaN(v)) return { type: "number", value: v };
  }
  return { type: "string", value: raw };
}

// Split a dotted key path, honouring quoted segments: a."b.c".d → ["a","b.c","d"]
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
      out.push(cur.trim());
      cur = "";
    } else {
      cur += c;
    }
  }
  out.push(cur.trim());
  return out.filter((s) => s.length > 0);
}

// Find the index where the value ends (start of an unquoted trailing comment).
function valueEndIndex(line: string, from: number): number {
  let quote: string | null = null;
  let depth = 0;
  for (let i = from; i < line.length; i++) {
    const c = line[i];
    if (quote) {
      if (c === quote) quote = null;
    } else if (c === '"' || c === "'") {
      quote = c;
    } else if (c === "[" || c === "{") {
      depth++;
    } else if (c === "]" || c === "}") {
      depth--;
    } else if (c === "#" && depth === 0) {
      return i;
    }
  }
  return line.length;
}

export class TomlParser {
  root: ConfigNode = { type: "object", key: null, index: null, path: [], children: [] };
  current: ConfigNode = this.root;
  pendingDoc: string[] = [];
  offset = 0;

  parse(text: string): ConfigNode {
    for (const original of text.split(/\n/)) {
      const line = original.endsWith("\r") ? original.slice(0, -1) : original;
      this.handleLine(line);
      this.offset += original.length + 1;
    }
    return this.root;
  }

  takeDoc(): string | undefined {
    const doc = this.pendingDoc.length ? this.pendingDoc.join("\n") : undefined;
    this.pendingDoc = [];
    return doc;
  }

  handleLine(line: string) {
    const trimmed = line.trim();
    if (trimmed === "") {
      this.pendingDoc = [];
      return;
    }
    if (trimmed.startsWith("#")) {
      this.pendingDoc.push(trimmed.replace(/^#\s?/, ""));
      return;
    }
    if (trimmed.startsWith("[")) {
      this.handleHeader(trimmed);
      return;
    }
    this.handleKeyValue(line);
  }

  handleHeader(trimmed: string) {
    const arrayTable = trimmed.startsWith("[[");
    const inner = arrayTable
      ? trimmed.replace(/^\[\[/, "").replace(/\]\]\s*$/, "")
      : trimmed.replace(/^\[/, "").replace(/\]\s*$/, "");
    const segments = splitDotted(inner);
    const doc = this.takeDoc();

    let node = this.root;
    const path: PathKey[] = [];
    for (let s = 0; s < segments.length; s++) {
      const seg = segments[s];
      path.push(seg);
      const last = s === segments.length - 1;
      let child = node.children?.find((c) => c.key === seg);

      if (last && arrayTable) {
        if (!child || child.type !== "array") {
          child = { type: "array", key: seg, index: null, path: [...path], children: [] };
          node.children!.push(child);
        }
        const item: ConfigNode = {
          type: "object",
          key: null,
          index: child.children!.length,
          path: [...path, child.children!.length],
          children: [],
          doc,
        };
        child.children!.push(item);
        this.current = item;
        return;
      }

      if (!child || child.type !== "object") {
        child = { type: "object", key: seg, index: null, path: [...path], children: [] };
        node.children!.push(child);
      }
      if (last) {
        if (doc) child.doc = doc;
        this.current = child;
      }
      node = child;
    }
  }

  handleKeyValue(line: string) {
    // Find the first top-level '=' (not inside quotes, before any comment).
    let quote: string | null = null;
    let eqIdx = -1;
    for (let i = 0; i < line.length; i++) {
      const c = line[i];
      if (quote) {
        if (c === quote) quote = null;
      } else if (c === '"' || c === "'") {
        quote = c;
      } else if (c === "=") {
        eqIdx = i;
        break;
      } else if (c === "#") {
        break;
      }
    }
    if (eqIdx === -1) {
      this.pendingDoc = [];
      return;
    }

    const keyPart = line.slice(0, eqIdx).trim();
    const segments = splitDotted(keyPart);
    if (segments.length === 0) {
      this.pendingDoc = [];
      return;
    }
    const doc = this.takeDoc();

    // Navigate dotted key into nested objects under the current table.
    let parent = this.current;
    const basePath = [...this.current.path];
    for (let s = 0; s < segments.length - 1; s++) {
      const seg = segments[s];
      basePath.push(seg);
      let child = parent.children?.find((c) => c.key === seg);
      if (!child || child.type !== "object") {
        child = { type: "object", key: seg, index: null, path: [...basePath], children: [] };
        parent.children!.push(child);
      }
      parent = child;
    }
    const leafKey = segments[segments.length - 1];
    const valuePath = [...basePath, leafKey];

    const node = this.parseValue(line, eqIdx + 1, valuePath, leafKey);
    node.doc = doc;
    // Replace if key already exists, else append.
    const existing = parent.children?.findIndex((c) => c.key === leafKey) ?? -1;
    if (existing >= 0) parent.children![existing] = node;
    else parent.children!.push(node);
  }

  parseValue(line: string, from: number, path: PathKey[], key: string | null): ConfigNode {
    const end = valueEndIndex(line, from);
    // Trim surrounding whitespace, tracking absolute offsets.
    let a = from;
    while (a < end && /\s/.test(line[a])) a++;
    let b = end;
    while (b > a && /\s/.test(line[b - 1])) b--;
    const raw = line.slice(a, b);
    const absStart = this.offset + a;
    const absEnd = this.offset + b;

    if (raw.startsWith("[")) {
      const arr = this.parseInlineArray(line, a, b, path, key);
      if (arr) return arr;
    }

    if (raw.startsWith('"') || raw.startsWith("'")) {
      const value = raw.slice(1, -1);
      return {
        type: "string", key, index: null, path, value, original: value,
        raw, start: absStart, end: absEnd, editable: true,
      };
    }

    if (raw.startsWith("{")) {
      // Inline table — show read-only to stay safe.
      return {
        type: "string", key, index: null, path, value: raw, original: raw,
        raw, start: absStart, end: absEnd, editable: false,
      };
    }

    const { type, value } = classifyLiteral(raw);
    return {
      type, key, index: null, path, value, original: value,
      raw, start: absStart, end: absEnd, editable: true,
    };
  }

  // Single-line inline array of scalars. Returns null if it isn't a clean
  // scalar array (caller then falls back to a read-only raw value).
  parseInlineArray(
    line: string,
    a: number,
    b: number,
    path: PathKey[],
    key: string | null,
  ): ConfigNode | null {
    const inner = line.slice(a + 1, b - 1);
    if (inner.includes("[") || inner.includes("{")) return null; // nested — bail
    const children: ConfigNode[] = [];
    let quote: string | null = null;
    let tokStart = -1;
    let idx = 0;

    const pushTok = (relEnd: number) => {
      if (tokStart === -1) return;
      let ts = tokStart;
      let te = relEnd;
      while (ts < te && /\s/.test(line[ts])) ts++;
      while (te > ts && /\s/.test(line[te - 1])) te--;
      if (te <= ts) {
        tokStart = -1;
        return;
      }
      const raw = line.slice(ts, te);
      const elPath = [...path, idx];
      let node: ConfigNode;
      if (raw.startsWith('"') || raw.startsWith("'")) {
        const value = raw.slice(1, -1);
        node = {
          type: "string", key: null, index: idx, path: elPath, value, original: value,
          raw, start: this.offset + ts, end: this.offset + te, editable: true,
        };
      } else {
        const { type, value } = classifyLiteral(raw);
        node = {
          type, key: null, index: idx, path: elPath, value, original: value,
          raw, start: this.offset + ts, end: this.offset + te, editable: true,
        };
      }
      children.push(node);
      idx++;
      tokStart = -1;
    };

    for (let i = a + 1; i < b - 1; i++) {
      const c = line[i];
      if (quote) {
        if (c === quote) quote = null;
        continue;
      }
      if (c === '"' || c === "'") {
        if (tokStart === -1) tokStart = i;
        quote = c;
        continue;
      }
      if (c === ",") {
        pushTok(i);
        continue;
      }
      if (!/\s/.test(c) && tokStart === -1) tokStart = i;
    }
    pushTok(b - 1);

    return {
      type: "array", key, index: null, path, children,
      start: this.offset + a, end: this.offset + b,
    };
  }
}

export function parseToml(text: string): ConfigNode {
  return new TomlParser().parse(text);
}

/** Serialize a scalar as a TOML value token. `quoted` keeps string typing. */
export function tomlScalar(value: Primitive, wasString: boolean): string {
  if (value === null) return '""';
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "number" && !wasString) return String(value);
  // String (or a number being stored where a string was) → basic string.
  const s = String(value).replace(/\\/g, "\\\\").replace(/"/g, '\\"');
  return `"${s}"`;
}
