// sanitizeSvg renders third-party category icons (raw SVG markup returned by the
// mod sources) safe to inject via dangerouslySetInnerHTML. Mod metadata is
// attacker-influenceable, so unsanitized SVG is a stored-XSS vector: SVG can
// carry <script>, event handlers, and javascript: URLs that execute on render.
//
// We parse the markup and rebuild it from a strict allowlist of SVG elements and
// attributes, dropping everything else (scripts, event handlers, foreignObject,
// external/js URLs). Anything that doesn't parse to a single <svg> root yields ""
// so the caller falls back to no icon rather than rendering untrusted HTML.

const ALLOWED_TAGS = new Set([
  "svg",
  "g",
  "path",
  "circle",
  "ellipse",
  "line",
  "polyline",
  "polygon",
  "rect",
  "defs",
  "title",
  "desc",
  "linearGradient",
  "radialGradient",
  "stop",
  "clippath",
  "use",
]);

const ALLOWED_ATTRS = new Set([
  "viewbox",
  "xmlns",
  "width",
  "height",
  "fill",
  "fill-rule",
  "fill-opacity",
  "stroke",
  "stroke-width",
  "stroke-linecap",
  "stroke-linejoin",
  "stroke-opacity",
  "stroke-dasharray",
  "d",
  "cx",
  "cy",
  "r",
  "rx",
  "ry",
  "x",
  "x1",
  "x2",
  "y",
  "y1",
  "y2",
  "points",
  "transform",
  "offset",
  "stop-color",
  "stop-opacity",
  "gradientunits",
  "gradienttransform",
  "class",
  "opacity",
  "id",
]);

// Attribute values that reference a URL must not smuggle a script. We only allow
// same-document fragment references (e.g. fill="url(#grad)").
function safeUrlAttr(name: string, value: string): boolean {
  if (name !== "fill" && name !== "stroke" && name !== "clip-path") return true;
  const v = value.trim().toLowerCase();
  if (v.startsWith("url(")) {
    return v.startsWith("url(#") || v.startsWith("url('#") || v.startsWith('url("#');
  }
  return true;
}

function scrub(el: Element): void {
  // Depth-first so we can remove children while iterating a static snapshot.
  for (const child of Array.from(el.children)) {
    if (!ALLOWED_TAGS.has(child.tagName.toLowerCase())) {
      child.remove();
      continue;
    }
    for (const attr of Array.from(child.attributes)) {
      const name = attr.name.toLowerCase();
      const value = attr.value;
      if (
        !ALLOWED_ATTRS.has(name) ||
        name.startsWith("on") ||
        value.toLowerCase().includes("javascript:") ||
        !safeUrlAttr(name, value)
      ) {
        child.removeAttribute(attr.name);
      }
    }
    scrub(child);
  }
}

export function sanitizeSvg(markup: string): string {
  if (typeof window === "undefined" || !markup || !markup.includes("<svg")) {
    return "";
  }
  const doc = new DOMParser().parseFromString(markup, "image/svg+xml");
  const root = doc.documentElement;
  if (
    !root ||
    root.tagName.toLowerCase() !== "svg" ||
    doc.getElementsByTagName("parsererror").length > 0
  ) {
    return "";
  }
  // Scrub the root's own attributes, then its subtree.
  for (const attr of Array.from(root.attributes)) {
    const name = attr.name.toLowerCase();
    if (!ALLOWED_ATTRS.has(name) || name.startsWith("on")) {
      root.removeAttribute(attr.name);
    }
  }
  scrub(root);
  return root.outerHTML;
}
