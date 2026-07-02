import { describe, expect, it } from "vitest";
import { sanitizeSvg } from "./sanitize";

// sanitizeSvg guards the app's only dangerouslySetInnerHTML call site; mod
// sources are attacker-influenceable, so every bypass here is stored XSS.
describe("sanitizeSvg", () => {
  it("keeps allowed markup intact", () => {
    const svg =
      '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M0 0h24v24H0z" fill="#fff"/></svg>';
    const out = sanitizeSvg(svg);
    expect(out).toContain("<path");
    expect(out).toContain('d="M0 0h24v24H0z"');
    expect(out).toContain('viewBox="0 0 24 24"');
  });

  it("removes script elements", () => {
    const out = sanitizeSvg('<svg><script>alert(1)</script><path d="M0 0"/></svg>');
    expect(out).not.toContain("script");
    expect(out).not.toContain("alert");
    expect(out).toContain("<path");
  });

  it("strips event handler attributes", () => {
    const out = sanitizeSvg('<svg><circle cx="1" cy="1" r="1" onclick="alert(1)"/></svg>');
    expect(out).not.toContain("onclick");
    expect(out).not.toContain("alert");
    expect(out).toContain("<circle");
  });

  it("strips event handlers on the svg root itself", () => {
    const out = sanitizeSvg('<svg onload="alert(1)"><path d="M0 0"/></svg>');
    expect(out).not.toContain("onload");
  });

  it("strips javascript: in attribute values", () => {
    const out = sanitizeSvg(
      '<svg><path d="M0 0" fill="javascript:alert(1)"/></svg>',
    );
    expect(out).not.toContain("javascript:");
  });

  it("removes foreignObject (HTML smuggling vector)", () => {
    const out = sanitizeSvg(
      '<svg><foreignObject><body onload="alert(1)"/></foreignObject><path d="M0 0"/></svg>',
    );
    expect(out).not.toContain("foreignObject");
    expect(out).not.toContain("onload");
  });

  it("removes disallowed elements nested inside allowed ones", () => {
    const out = sanitizeSvg(
      '<svg><g><g><image href="https://evil.test/x.svg"/></g><path d="M0 0"/></g></svg>',
    );
    expect(out).not.toContain("image");
    expect(out).not.toContain("evil.test");
    expect(out).toContain("<path");
  });

  it("neutralizes <use>: the element survives but href does not", () => {
    const out = sanitizeSvg('<svg><use href="https://evil.test/x.svg#p"/></svg>');
    expect(out).not.toContain("href");
    expect(out).not.toContain("evil.test");
  });

  it("keeps same-document url() references but strips external ones", () => {
    const kept = sanitizeSvg('<svg><rect x="0" fill="url(#grad)"/></svg>');
    expect(kept).toContain('fill="url(#grad)"');

    const stripped = sanitizeSvg(
      '<svg><rect x="0" fill="url(https://evil.test/f)"/></svg>',
    );
    expect(stripped).not.toContain("evil.test");
    expect(stripped).not.toContain("fill=");
  });

  it("returns empty string for non-SVG and unparseable input", () => {
    expect(sanitizeSvg("")).toBe("");
    expect(sanitizeSvg("plain text")).toBe("");
    expect(sanitizeSvg("<div>html</div>")).toBe("");
    expect(sanitizeSvg("<svg><unclosed")).toBe("");
    // Root element is not <svg> even though the string mentions one.
    expect(sanitizeSvg('<html><svg><path d="M0 0"/></svg></html>')).toBe("");
  });
});
