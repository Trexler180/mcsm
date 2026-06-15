// .properties parser. Flat key=value (or key:value) pairs. Comment lines (# or !)
// immediately above a key become its help text. Values are inferred as
// boolean/number/string for nicer controls, but saved by in-place span
// replacement so comments, ordering, and untouched lines are preserved exactly.
//
// It also *attempts* to recognise commented-out options — the convention many
// mods (e.g. ModernFix) use to ship a menu of toggles like
//   `#  mixin.perf.dynamic_resources=false # (default)`
// These become togglable fields: enabling one uncomments the line (and strips
// the trailing ` # note`), disabling re-comments it.

import type { ConfigNode, Primitive } from "./model";

const KEY_RE = /^[A-Za-z_][\w.-]*$/;

function inferType(raw: string): { type: ConfigNode["type"]; value: Primitive } {
  const t = raw.trim();
  if (t === "true" || t === "false") return { type: "boolean", value: t === "true" };
  if (t !== "" && !Number.isNaN(Number(t)) && /^[+-]?(\d|\.)/.test(t))
    return { type: "number", value: Number(t) };
  return { type: "string", value: raw };
}

// Locate the value/trailing-comment split inside a value string: the first
// " #" that begins an inline comment (only honoured for disabled-option lines).
function splitTrailingComment(value: string): { value: string; commentRel: number } {
  const m = value.match(/\s#/);
  if (m && m.index !== undefined) {
    return { value: value.slice(0, m.index), commentRel: m.index };
  }
  return { value, commentRel: -1 };
}

export function parseProperties(text: string): ConfigNode {
  const children: ConfigNode[] = [];
  const byKey = new Map<string, number>(); // key → index in children (last wins)
  const root: ConfigNode = {
    type: "object",
    key: null,
    index: null,
    path: [],
    children,
  };

  let offset = 0;
  let pendingDoc: string[] = [];
  let sawDisabled = false;

  const upsert = (node: ConfigNode) => {
    const existing = byKey.get(node.key as string);
    if (existing !== undefined) {
      node.path = children[existing].path;
      children[existing] = node;
    } else {
      byKey.set(node.key as string, children.length);
      children.push(node);
    }
  };

  for (const original of text.split(/\n/)) {
    const lineStart = offset;
    offset += original.length + 1;
    const line = original.endsWith("\r") ? original.slice(0, -1) : original;
    const trimmed = line.trim();

    if (trimmed === "") {
      pendingDoc = [];
      continue;
    }

    // Comment line: maybe a disabled option, otherwise prose help text.
    if (trimmed.startsWith("#") || trimmed.startsWith("!")) {
      const m = line.match(/^(\s*[#!]+\s*)(\S.*)$/);
      const rest = m ? m[2] : "";
      const eq = rest.search(/[=:]/);
      const candidateKey = eq >= 0 ? rest.slice(0, eq).trim() : "";
      if (m && eq >= 0 && KEY_RE.test(candidateKey)) {
        // Disabled option.
        const prefixLen = m[1].length;
        const valuePartRaw = rest.slice(eq + 1);
        const { value: realStr, commentRel } = splitTrailingComment(valuePartRaw);
        const leadSpaces = realStr.length - realStr.trimStart().length;
        const trimmedReal = realStr.trim();
        const valueAbsBase = lineStart + prefixLen + eq + 1;
        const valueStart = valueAbsBase + leadSpaces;
        const valueEnd = valueStart + trimmedReal.length;
        const { type, value } = inferType(trimmedReal);
        const trailingStart =
          commentRel >= 0 ? valueAbsBase + commentRel : undefined;

        sawDisabled = true;
        upsert({
          type,
          key: candidateKey,
          index: null,
          path: [candidateKey],
          doc:
            commentRel >= 0
              ? valuePartRaw.slice(commentRel).replace(/^\s*#\s?/, "").trim()
              : undefined,
          value,
          original: value,
          raw: trimmedReal,
          start: valueStart,
          end: valueEnd,
          editable: true,
          togglable: true,
          disabled: true,
          disabledOriginal: true,
          lineStart,
          keyStart: lineStart + prefixLen,
          trailingStart,
          lineEnd: lineStart + line.length,
        });
        continue;
      }
      pendingDoc.push(trimmed.replace(/^[#!]+\s?/, ""));
      continue;
    }

    // Active key=value (or key:value) line.
    let sep = -1;
    for (let i = 0; i < line.length; i++) {
      const c = line[i];
      if (c === "\\") {
        i++;
        continue;
      }
      if (c === "=" || c === ":") {
        sep = i;
        break;
      }
    }
    if (sep === -1) {
      pendingDoc = [];
      continue;
    }

    const key = line.slice(0, sep).trim();
    const rawValue = line.slice(sep + 1);
    const valueStart = lineStart + sep + 1;
    const valueEnd = lineStart + line.length;
    const { type, value } = inferType(rawValue);
    const keyStart = lineStart + (line.length - line.trimStart().length);

    upsert({
      type,
      key,
      index: null,
      path: [key],
      doc: pendingDoc.length ? pendingDoc.join("\n") : undefined,
      value,
      original: value,
      raw: rawValue,
      start: valueStart,
      end: valueEnd,
      editable: true,
      disabled: false,
      disabledOriginal: false,
      lineStart,
      keyStart,
      lineEnd: lineStart + line.length,
    });
    pendingDoc = [];
  }

  // If the file shipped any commented-out options, every option becomes
  // togglable (so active ones can be disabled too).
  if (sawDisabled) for (const c of children) c.togglable = true;

  return root;
}

/** Serialize a scalar as a .properties value (no quoting). */
export function propertiesScalar(value: Primitive): string {
  if (value === null) return "";
  return String(value);
}
