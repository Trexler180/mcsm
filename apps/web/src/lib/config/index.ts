// Format detection + parse/save dispatch for the structured config editor.
//
// parseConfig() returns a ParsedConfig on success or null on failure (the UI
// then falls back to the raw text editor). saveConfig() turns an edited node
// tree back into file text using a single set of non-overlapping "ops":
//   • a changed scalar  → replace just its value span
//   • a dirty container → re-serialize that container into its span (JSON only)
//   • a (un)commented properties option → strip/add its comment prefix
// Everything we don't touch is preserved byte-for-byte, including comments.

import {
  type ConfigFormat,
  type ConfigNode,
  type ParsedConfig,
  type Primitive,
} from "./model";
import { jsonScalar, parseJsonFamily, serializeJsonNode } from "./json";
import { parseProperties, propertiesScalar } from "./properties";
import { parseToml, tomlScalar } from "./toml";
import { parseYaml, yamlScalar } from "./yaml";
import { parseHocon, hoconScalar } from "./hocon";

export * from "./model";

export function detectFormat(filename: string): ConfigFormat {
  const ext = filename.split(".").pop()?.toLowerCase() ?? "";
  switch (ext) {
    case "json":
      return "json";
    case "json5":
    case "jsonc":
      return "json5";
    case "properties":
      return "properties";
    case "toml":
      return "toml";
    case "yml":
    case "yaml":
      return "yaml";
    case "conf":
    case "hocon":
      return "hocon";
    case "cfg":
    case "ini":
      return "scalar";
    default:
      return "unknown";
  }
}

/** Whether we'll even attempt a structured view for this filename. */
export function isStructuredCandidate(filename: string): boolean {
  return detectFormat(filename) !== "unknown";
}

// Validate scalar spans against the source. If a scalar's recorded span doesn't
// slice back to its raw token, we can't trust it for patching — mark read-only.
function validateSpans(node: ConfigNode, text: string) {
  if (node.type === "object" || node.type === "array") {
    for (const child of node.children ?? []) validateSpans(child, text);
    return;
  }
  if (
    node.editable &&
    node.start !== undefined &&
    node.end !== undefined &&
    node.raw !== undefined
  ) {
    if (text.slice(node.start, node.end) !== node.raw) {
      node.editable = false;
    }
  }
}

// A single-scalar file (e.g. recipe_cooldown.cfg containing just `50`).
// Leading # / // comment lines become the field's help text.
function parseBareScalar(text: string): ConfigNode | null {
  const docs: string[] = [];
  let valueLine: { raw: string; start: number } | null = null;
  let offset = 0;
  for (const original of text.split(/\n/)) {
    const lineStart = offset;
    offset += original.length + 1;
    const line = original.endsWith("\r") ? original.slice(0, -1) : original;
    const trimmed = line.trim();
    if (trimmed === "") continue;
    if (trimmed.startsWith("#") || trimmed.startsWith("//")) {
      docs.push(trimmed.replace(/^(#|\/\/)\s?/, ""));
      continue;
    }
    if (valueLine) return null; // more than one value line → not a bare scalar
    const lead = line.length - line.trimStart().length;
    valueLine = { raw: trimmed, start: lineStart + lead };
  }
  if (!valueLine) return null;

  const raw = valueLine.raw;
  let type: ConfigNode["type"] = "string";
  let value: Primitive = raw;
  if (raw === "true" || raw === "false") {
    type = "boolean";
    value = raw === "true";
  } else if (!Number.isNaN(Number(raw)) && /^[+-]?(\d|\.)/.test(raw)) {
    type = "number";
    value = Number(raw);
  }
  return {
    type,
    key: null,
    index: null,
    path: [],
    value,
    original: value,
    raw,
    start: valueLine.start,
    end: valueLine.start + raw.length,
    editable: true,
    doc: docs.length ? docs.join("\n") : undefined,
  };
}

export function parseConfig(filename: string, text: string): ParsedConfig | null {
  const format = detectFormat(filename);
  try {
    switch (format) {
      case "json":
      case "json5": {
        const { root } = parseJsonFamily(text);
        validateSpans(root, text);
        // JSON-family supports add/remove: containers are re-serialized into
        // their own span, so comments outside them survive.
        return { format, root, structural: true };
      }
      case "properties": {
        const root = parseProperties(text);
        validateSpans(root, text);
        return { format, root, structural: false };
      }
      case "toml": {
        const root = parseToml(text);
        validateSpans(root, text);
        return { format, root, structural: false };
      }
      case "yaml": {
        const root = parseYaml(text);
        validateSpans(root, text);
        return { format, root, structural: false };
      }
      case "hocon": {
        const root = parseHocon(text);
        validateSpans(root, text);
        return { format, root, structural: false };
      }
      case "scalar": {
        const root = parseBareScalar(text);
        if (!root) return null;
        validateSpans(root, text);
        return { format, root, structural: false };
      }
      default:
        return null;
    }
  } catch {
    return null;
  }
}

// Serialize one scalar value as a source token appropriate for the format.
function scalarToken(format: ConfigFormat, node: ConfigNode, value: Primitive): string {
  switch (format) {
    case "json":
    case "json5":
      return jsonScalar(value);
    case "properties":
    case "scalar":
      return propertiesScalar(value);
    case "toml":
      return tomlScalar(value, node.type === "string");
    case "yaml":
      return yamlScalar(value, node.raw);
    case "hocon":
      return hoconScalar(value, node);
    default:
      return String(value ?? "");
  }
}

function detectIndentUnit(text: string): string {
  if (/\n\t/.test(text)) return "\t";
  const m = text.match(/\n( +)\S/);
  if (m && m[1].length >= 4) return "    ";
  return "  ";
}

// The literal indentation characters preceding `offset` on its line.
function leadingIndent(text: string, offset: number): string {
  const lineStart = text.lastIndexOf("\n", offset - 1) + 1;
  const slice = text.slice(lineStart, offset);
  const m = slice.match(/^[ \t]*/);
  return m ? m[0] : "";
}

interface Op {
  start: number;
  end: number;
  text: string;
}

function collectOps(
  node: ConfigNode,
  format: ConfigFormat,
  source: string,
  unit: string,
  ops: Op[],
) {
  if (node.type === "object" || node.type === "array") {
    if (
      node.dirty &&
      node.start !== undefined &&
      node.end !== undefined &&
      (format === "json" || format === "json5")
    ) {
      const base = leadingIndent(source, node.start);
      ops.push({
        start: node.start,
        end: node.end,
        text: serializeJsonNode(node, unit, base),
      });
      return; // descendants are covered by this re-serialization
    }
    for (const child of node.children ?? [])
      collectOps(child, format, source, unit, ops);
    return;
  }

  // Properties enable/disable (comment toggling).
  if (
    node.togglable &&
    node.disabled !== node.disabledOriginal &&
    node.lineStart !== undefined &&
    node.keyStart !== undefined
  ) {
    if (node.disabled) {
      // Active → disabled: prefix the line with "# ".
      const prefix = source.slice(node.lineStart, node.keyStart);
      ops.push({
        start: node.lineStart,
        end: node.keyStart,
        text: `# ${prefix}`,
      });
    } else {
      // Disabled → active: strip the "# " prefix and any trailing comment.
      ops.push({ start: node.lineStart, end: node.keyStart, text: "" });
      if (node.trailingStart !== undefined && node.lineEnd !== undefined) {
        ops.push({ start: node.trailingStart, end: node.lineEnd, text: "" });
      }
    }
  }

  // Scalar value change.
  if (
    node.editable &&
    node.start !== undefined &&
    node.end !== undefined &&
    !Object.is(node.value, node.original)
  ) {
    ops.push({
      start: node.start,
      end: node.end,
      text: scalarToken(format, node, node.value as Primitive),
    });
  }
}

export function saveConfig(parsed: ParsedConfig, originalText: string): string {
  const unit = detectIndentUnit(originalText);
  const ops: Op[] = [];
  collectOps(parsed.root, parsed.format, originalText, unit, ops);

  // Apply back-to-front so earlier offsets stay valid. Drop any op that would
  // overlap one already applied (defensive — shouldn't normally happen).
  ops.sort((a, b) => b.start - a.start);
  let text = originalText;
  let lastStart = Infinity;
  for (const op of ops) {
    if (op.end > lastStart) continue;
    text = text.slice(0, op.start) + op.text + text.slice(op.end);
    lastStart = op.start;
  }
  return text;
}

/** Does the parsed tree contain any editable scalar? (Else raw mode is better.) */
export function hasEditableContent(node: ConfigNode): boolean {
  if (node.type === "object" || node.type === "array") {
    return (node.children ?? []).some(hasEditableContent);
  }
  return true;
}
