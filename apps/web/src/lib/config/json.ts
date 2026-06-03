// Span-aware JSON / JSON5 / JSONC parser.
//
// Produces a ConfigNode tree where every value remembers its exact source span.
// Tolerates // and /* */ comments, trailing commas, single-quoted strings, and
// unquoted (identifier) object keys — the JSON5 superset that mod configs use.
// If comments are present we flag `hadComments` so the caller keeps comment-safe
// in-place patching instead of re-serializing the whole document.

import type { ConfigNode, PathKey, Primitive } from "./model";

export interface JsonParseResult {
  root: ConfigNode;
  hadComments: boolean;
}

const WS = new Set([" ", "\t", "\n", "\r", "\f", "\v", "﻿"]);
const STOP = new Set([",", "}", "]", ":"]);

class JsonParser {
  i = 0;
  hadComments = false;
  pendingComments: string[] = [];
  constructor(public s: string) {}

  error(msg: string): never {
    throw new Error(`${msg} at position ${this.i}`);
  }

  /** Pull the comments seen since the last call (used as field help text). */
  takeComments(): string | undefined {
    if (this.pendingComments.length === 0) return undefined;
    const doc = this.pendingComments.join("\n").trim();
    this.pendingComments = [];
    return doc || undefined;
  }

  ws() {
    const s = this.s;
    while (this.i < s.length) {
      const c = s[this.i];
      if (WS.has(c)) {
        this.i++;
        continue;
      }
      if (c === "/" && s[this.i + 1] === "/") {
        this.hadComments = true;
        this.i += 2;
        const start = this.i;
        while (this.i < s.length && s[this.i] !== "\n") this.i++;
        this.pendingComments.push(s.slice(start, this.i).trim());
        continue;
      }
      if (c === "/" && s[this.i + 1] === "*") {
        this.hadComments = true;
        this.i += 2;
        const start = this.i;
        while (this.i < s.length && !(s[this.i] === "*" && s[this.i + 1] === "/"))
          this.i++;
        if (this.i >= s.length) this.error("unterminated block comment");
        const body = s.slice(start, this.i);
        this.pendingComments.push(
          body
            .split("\n")
            .map((l) => l.replace(/^\s*\*?\s?/, "").replace(/\s+$/, ""))
            .join("\n")
            .trim(),
        );
        this.i += 2;
        continue;
      }
      break;
    }
  }

  parseDocument(): ConfigNode {
    this.ws();
    const node = this.parseValue([], null, null);
    this.ws();
    if (this.i < this.s.length) this.error("trailing content");
    return node;
  }

  parseValue(path: PathKey[], key: string | null, index: number | null): ConfigNode {
    this.ws();
    const c = this.s[this.i];
    if (c === undefined) this.error("unexpected end of input");
    if (c === "{") return this.parseObject(path, key, index);
    if (c === "[") return this.parseArray(path, key, index);
    if (c === '"' || c === "'") return this.parseStringNode(path, key, index);
    return this.parseLiteral(path, key, index);
  }

  parseObject(path: PathKey[], key: string | null, index: number | null): ConfigNode {
    const start = this.i;
    this.i++; // {
    const children: ConfigNode[] = [];
    this.ws();
    while (this.s[this.i] !== "}") {
      if (this.i >= this.s.length) this.error("unterminated object");
      const doc = this.takeComments();
      const k = this.parseKey();
      this.ws();
      if (this.s[this.i] !== ":") this.error("expected ':'");
      this.i++;
      const child = this.parseValue([...path, k], k, null);
      if (doc) child.doc = doc;
      children.push(child);
      this.ws();
      if (this.s[this.i] === ",") {
        this.i++;
        this.ws();
      }
    }
    this.i++; // }
    return { type: "object", key, index, path, start, end: this.i, children };
  }

  parseArray(path: PathKey[], key: string | null, index: number | null): ConfigNode {
    const start = this.i;
    this.i++; // [
    const children: ConfigNode[] = [];
    this.ws();
    let idx = 0;
    while (this.s[this.i] !== "]") {
      if (this.i >= this.s.length) this.error("unterminated array");
      const doc = this.takeComments();
      const child = this.parseValue([...path, idx], null, idx);
      if (doc) child.doc = doc;
      children.push(child);
      idx++;
      this.ws();
      if (this.s[this.i] === ",") {
        this.i++;
        this.ws();
      }
    }
    this.i++; // ]
    return { type: "array", key, index, path, start, end: this.i, children };
  }

  parseKey(): string {
    this.ws();
    const c = this.s[this.i];
    if (c === '"' || c === "'") {
      return this.readString();
    }
    // Unquoted JSON5 identifier key.
    const start = this.i;
    while (this.i < this.s.length) {
      const ch = this.s[this.i];
      if (WS.has(ch) || ch === ":" || ch === "}") break;
      this.i++;
    }
    if (this.i === start) this.error("expected object key");
    return this.s.slice(start, this.i);
  }

  parseStringNode(
    path: PathKey[],
    key: string | null,
    index: number | null,
  ): ConfigNode {
    const start = this.i;
    const value = this.readString();
    const end = this.i;
    return {
      type: "string",
      key,
      index,
      path,
      value,
      original: value,
      raw: this.s.slice(start, end),
      start,
      end,
      editable: true,
    };
  }

  readString(): string {
    const quote = this.s[this.i];
    this.i++;
    let out = "";
    while (this.i < this.s.length) {
      const c = this.s[this.i];
      if (c === "\\") {
        const n = this.s[this.i + 1];
        switch (n) {
          case "n": out += "\n"; break;
          case "t": out += "\t"; break;
          case "r": out += "\r"; break;
          case "b": out += "\b"; break;
          case "f": out += "\f"; break;
          case "/": out += "/"; break;
          case "\\": out += "\\"; break;
          case '"': out += '"'; break;
          case "'": out += "'"; break;
          case "u": {
            const hex = this.s.slice(this.i + 2, this.i + 6);
            out += String.fromCharCode(parseInt(hex, 16));
            this.i += 4;
            break;
          }
          case "\n": break; // line continuation
          default: out += n ?? "";
        }
        this.i += 2;
        continue;
      }
      if (c === quote) {
        this.i++;
        return out;
      }
      out += c;
      this.i++;
    }
    this.error("unterminated string");
  }

  parseLiteral(
    path: PathKey[],
    key: string | null,
    index: number | null,
  ): ConfigNode {
    const start = this.i;
    while (this.i < this.s.length) {
      const c = this.s[this.i];
      if (WS.has(c) || STOP.has(c)) break;
      if (c === "/" && (this.s[this.i + 1] === "/" || this.s[this.i + 1] === "*"))
        break;
      this.i++;
    }
    const raw = this.s.slice(start, this.i);
    if (raw === "") this.error("expected value");
    const end = this.i;

    let value: Primitive;
    let type: ConfigNode["type"];
    if (raw === "true" || raw === "false") {
      value = raw === "true";
      type = "boolean";
    } else if (raw === "null") {
      value = null;
      type = "null";
    } else {
      const num = Number(raw);
      if (!Number.isNaN(num) && raw !== "" && /^[+-]?(0x|0X|\.|\d|Infinity|NaN)/.test(raw)) {
        value = num;
        type = "number";
      } else {
        // Unquoted bareword we don't understand — keep as a string.
        value = raw;
        type = "string";
      }
    }
    return {
      type,
      key,
      index,
      path,
      value,
      original: value,
      raw,
      start,
      end,
      editable: true,
    };
  }
}

export function parseJsonFamily(text: string): JsonParseResult {
  const parser = new JsonParser(text);
  const root = parser.parseDocument();
  return { root, hadComments: parser.hadComments };
}

/** Serialize a JS value as a scalar JSON token (for in-place patching). */
export function jsonScalar(value: Primitive): string {
  return JSON.stringify(value);
}

// Serialize a (possibly structurally edited) container subtree back to JSON,
// indented to sit at `baseIndent`. Used to replace only that container's source
// span — comments outside the container are left untouched.
export function serializeJsonNode(
  node: ConfigNode,
  unit: string,
  baseIndent: string,
): string {
  if (node.type === "object") {
    const kids = node.children ?? [];
    if (kids.length === 0) return "{}";
    const inner = baseIndent + unit;
    const lines = kids.map(
      (c) =>
        `${inner}${JSON.stringify(c.key)}: ${serializeJsonNode(c, unit, inner)}`,
    );
    return `{\n${lines.join(",\n")}\n${baseIndent}}`;
  }
  if (node.type === "array") {
    const kids = node.children ?? [];
    if (kids.length === 0) return "[]";
    const inner = baseIndent + unit;
    const lines = kids.map((c) => `${inner}${serializeJsonNode(c, unit, inner)}`);
    return `[\n${lines.join(",\n")}\n${baseIndent}]`;
  }
  return jsonScalar(node.value ?? null);
}
