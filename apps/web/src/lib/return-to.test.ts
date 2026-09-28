import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { rememberReturnTo, takeReturnTo, returnToHref } from "./return-to";

describe("return-to", () => {
  beforeEach(() => sessionStorage.clear());
  afterEach(() => sessionStorage.clear());

  it("round-trips an internal path with its query string", () => {
    rememberReturnTo("/mcp-consent", "?request=abc123");
    expect(takeReturnTo()).toBe("/mcp-consent?request=abc123");
  });

  it("is single-use, so a stale entry cannot hijack a later login", () => {
    rememberReturnTo("/mcp-consent", "?request=abc123");
    expect(takeReturnTo()).toBe("/mcp-consent?request=abc123");
    expect(takeReturnTo()).toBeNull();
  });

  it("never sends the user back to login, which would loop", () => {
    rememberReturnTo("/login", "");
    expect(takeReturnTo()).toBeNull();
  });

  // Login is the classic open-redirect target: "sign in and we'll send you
  // onward" is exactly the shape of the trick.
  it.each([
    ["absolute http", "https://evil.test/steal", ""],
    ["protocol-relative", "//evil.test/steal", ""],
    ["backslash-smuggled", "/\\evil.test/steal", ""],
    ["scheme-relative in query", "/ok", "?next=//evil.test"],
  ])("refuses to store an off-site destination (%s)", (_name, path, search) => {
    rememberReturnTo(path, search);
    const stored = takeReturnTo();
    // Either nothing was stored, or what came back is an internal path that
    // cannot navigate off-origin on its own.
    if (stored !== null) {
      expect(stored.startsWith("/")).toBe(true);
      expect(stored.startsWith("//")).toBe(false);
      expect(stored).not.toMatch(/^\/\\/);
    }
  });

  it("rejects a value planted directly in storage", () => {
    sessionStorage.setItem("post_login_redirect", "https://evil.test/steal");
    expect(takeReturnTo()).toBeNull();
  });

  it("builds an href under the deploy base path", () => {
    // BASE_URL is "/" in test, so the href is the path itself.
    expect(returnToHref("/mcp-consent?request=abc")).toBe(
      "/mcp-consent?request=abc",
    );
  });
});
