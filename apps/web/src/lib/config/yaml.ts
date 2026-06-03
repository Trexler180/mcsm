// Block-style YAML parser for the subset mod configs use: nested maps, block
// sequences (scalars and maps), `key: value` scalars, and `# comments` as help
// text. It is intentionally conservative — flow collections ([], {}) and other
// exotic syntax are surfaced read-only, and any parse failure makes the caller
// fall back to the raw editor. Scalar edits save by in-place span replacement,
// so comments and layout are preserved.

import type { ConfigNode, PathKey, Primitive } from "./model";

interface Token {
  indent: number;
  content: string; // line text with leading indent removed
  lineStart: number; // absolute offset of the line start
  doc?: string;
}

function tokenize(text: string): Token[] {
  const tokens: Token[] = [];
  let offset = 0;
  let pendingDoc: string[] = [];
  for (const original of text.split(/\n/)) {
    const lineStart = offset;
    offset += original.length + 1;
    const line = original.endsWith("\r") ? original.slice(0, -1) : original;
    if (line.trim() === "") {
      pendingDoc = [];
      continue;
    }
    const indent = line.length - line.replace(/^\s+/, "").length;
    const content = line.slice(indent);
    if (content.startsWith("#")) {
      pendingDoc.push(content.replace(/^#\s?/, ""));
      continue;
    }
    if (content === "---" || content === "...") {
      pendingDoc = [];
      continue;
    }
    tokens.push({
      indent,
      content,
      lineStart,
      doc: pendingDoc.length ? pendingDoc.join("\n") : undefined,
    });
    pendingDoc = [];
  }
  return tokens;
}

// Locate the ": " (or trailing ":") that separates a map key from its value,
// skipping quoted regions. Returns -1 if there is no such separator.
function findColon(content: string): number {
  let quote: string | null = null;
  for (let i = 0; i < content.length; i++) {
    const c = content[i];
    if (quote) {
      if (c === quote) quote = null;
      continue;
    }
    if (c === '"' || c === "'") {
      quote = c;
      continue;
    }
    if (c === ":" && (i + 1 >= content.length || content[i + 1] === " ")) {
      return i;
    }
  }
  return -1;
}

// Find where a value ends inside a line (strips an inline " # comment").
function valueLen(value: string): number {
  let quote: string | null = null;
  for (let i = 0; i < value.length; i++) {
    const c = value[i];
    if (quote) {
      if (c === quote) quote = null;
      continue;
    }
    if (c === '"' || c === "'") {
      quote = c;
      continue;
    }
    if (c === "#" && i > 0 && /\s/.test(value[i - 1])) return i;
  }
  return value.length;
}

function classifyScalar(
  raw: string,
  lineStart: number,
  col: number,
  path: PathKey[],
  key: string | null,
  index: number | null,
): ConfigNode {
  const trimmed = raw.replace(/\s+$/, "");
  const node: ConfigNode = {
    type: "string",
    key,
    index,
    path,
    raw: trimmed,
    start: lineStart + col,
    end: lineStart + col + trimmed.length,
    editable: true,
  };

  if (trimmed.startsWith("[") || trimmed.startsWith("{")) {
    // Flow collection — keep read-only to stay safe.
    node.value = trimmed;
    node.original = trimmed;
    node.editable = false;
    return node;
  }
  if (trimmed.startsWith('"') || trimmed.startsWith("'")) {
    const v = trimmed.slice(1, -1);
    node.value = v;
    node.original = v;
    return node;
  }
  if (trimmed === "true" || trimmed === "false") {
    node.type = "boolean";
    node.value = trimmed === "true";
  } else if (trimmed === "null" || trimmed === "~" || trimmed === "") {
    node.type = "null";
    node.value = null;
  } else if (!Number.isNaN(Number(trimmed)) && /^[+-]?(\d|\.)/.test(trimmed)) {
    node.type = "number";
    node.value = Number(trimmed);
  } else {
    node.value = trimmed;
  }
  node.original = node.value;
  return node;
}

class YamlParser {
  pos = 0;
  constructor(public tokens: Token[]) {}

  parse(): ConfigNode {
    const root: ConfigNode = {
      type: "object",
      key: null,
      index: null,
      path: [],
      children: [],
    };
    if (this.tokens.length === 0) return root;
    const baseIndent = this.tokens[0].indent;
    const block = this.parseBlock(baseIndent, []);
    return block;
  }

  // Parse a run of sibling entries all at `indent`. Decides map vs sequence
  // from the first entry.
  parseBlock(indent: number, path: PathKey[]): ConfigNode {
    const isSeq = this.tokens[this.pos]?.content.startsWith("-");
    const node: ConfigNode = {
      type: isSeq ? "array" : "object",
      key: null,
      index: null,
      path,
      children: [],
    };

    while (this.pos < this.tokens.length && this.tokens[this.pos].indent === indent) {
      const tok = this.tokens[this.pos];
      if (tok.content.startsWith("-")) {
        if (node.type !== "array") break;
        this.parseSeqItem(node, indent, path);
      } else {
        if (node.type !== "object") break;
        this.parseMapEntry(node, indent, path);
      }
    }
    return node;
  }

  parseMapEntry(parent: ConfigNode, indent: number, path: PathKey[]) {
    const tok = this.tokens[this.pos];
    this.pos++;
    const colon = findColon(tok.content);
    if (colon === -1) {
      return; // not a key:value line we understand
    }
    let key = tok.content.slice(0, colon).trim();
    if (
      (key.startsWith('"') && key.endsWith('"')) ||
      (key.startsWith("'") && key.endsWith("'"))
    ) {
      key = key.slice(1, -1);
    }
    const childPath = [...path, key];
    const after = tok.content.slice(colon + 1);
    const valueRaw = after.replace(/^\s/, "");
    const valCol = indent + colon + 1 + (after.length - valueRaw.length);
    const trimmedLen = valueLen(valueRaw);
    const value = valueRaw.slice(0, trimmedLen);

    if (value.trim() === "") {
      // Container value — parse the deeper block (if any).
      if (
        this.pos < this.tokens.length &&
        this.tokens[this.pos].indent > indent
      ) {
        const childIndent = this.tokens[this.pos].indent;
        const child = this.parseBlock(childIndent, childPath);
        child.key = key;
        child.doc = tok.doc;
        parent.children!.push(child);
      } else {
        // Empty value with no children → treat as null scalar.
        parent.children!.push({
          type: "null",
          key,
          index: null,
          path: childPath,
          value: null,
          original: null,
          doc: tok.doc,
          editable: false,
        });
      }
      return;
    }

    const scalar = classifyScalar(value, tok.lineStart, valCol, childPath, key, null);
    scalar.doc = tok.doc;
    parent.children!.push(scalar);
  }

  parseSeqItem(parent: ConfigNode, indent: number, path: PathKey[]) {
    const tok = this.tokens[this.pos];
    const index = parent.children!.length;
    const itemPath = [...path, index];
    // Strip the leading "- ".
    const dashRest = tok.content.replace(/^-\s*/, "");
    const dashOffset = tok.content.length - dashRest.length; // chars consumed incl. spaces

    if (dashRest === "") {
      // "-" alone: the item's content is on following deeper lines.
      this.pos++;
      if (
        this.pos < this.tokens.length &&
        this.tokens[this.pos].indent > indent
      ) {
        const child = this.parseBlock(this.tokens[this.pos].indent, itemPath);
        child.index = index;
        child.doc = tok.doc;
        parent.children!.push(child);
      }
      return;
    }

    const colon = findColon(dashRest);
    if (colon === -1) {
      // Scalar sequence element.
      this.pos++;
      const valCol = indent + dashOffset;
      const trimmedLen = valueLen(dashRest);
      const scalar = classifyScalar(
        dashRest.slice(0, trimmedLen),
        tok.lineStart,
        valCol,
        itemPath,
        null,
        index,
      );
      scalar.doc = tok.doc;
      parent.children!.push(scalar);
      return;
    }

    // Sequence item is a map whose first key is inline after "- ".
    // Subsequent keys are indented at (indent + dashOffset).
    const itemNode: ConfigNode = {
      type: "object",
      key: null,
      index,
      path: itemPath,
      doc: tok.doc,
      children: [],
    };
    const childIndent = indent + dashOffset;
    // Rewrite this token to look like a plain map entry at childIndent, then
    // parse the contiguous map block starting here.
    this.tokens[this.pos] = {
      indent: childIndent,
      content: dashRest,
      lineStart: tok.lineStart,
      doc: tok.doc,
    };
    const map = this.parseBlock(childIndent, itemPath);
    itemNode.children = map.children;
    parent.children!.push(itemNode);
  }
}

export function parseYaml(text: string): ConfigNode {
  return new YamlParser(tokenize(text)).parse();
}

/** Serialize a scalar as a YAML value token, matching the original quote style. */
export function yamlScalar(value: Primitive, originalRaw: string | undefined): string {
  if (value === null) return "null";
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "number") return String(value);
  const s = String(value);
  const quoted = originalRaw?.trimStart();
  if (quoted?.startsWith("'")) return `'${s.replace(/'/g, "''")}'`;
  if (quoted?.startsWith('"')) return `"${s.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
  // Quote if the bare form would be ambiguous.
  if (s === "" || /^[\s]|[\s]$|[:#]|^[-?&*!|>%@`'"]/.test(s) || /^(true|false|null|~)$/i.test(s) || !Number.isNaN(Number(s))) {
    return `"${s.replace(/\\/g, "\\\\").replace(/"/g, '\\"')}"`;
  }
  return s;
}
