import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import { AlertTriangle, ChevronDown, ChevronUp } from "lucide-react";
import { ServerConsole } from "@/lib/ws";
import { api } from "@/lib/api";
import { matchCommands } from "./commands";

interface TerminalProps {
  serverId: string;
}

const HISTORY_KEY = (serverId: string) => `mcsm.console.history.${serverId}`;
const HISTORY_MAX = 50;

function loadHistory(serverId: string): string[] {
  try {
    const raw = localStorage.getItem(HISTORY_KEY(serverId));
    const parsed = raw ? (JSON.parse(raw) as unknown) : [];
    return Array.isArray(parsed) ? parsed.filter((x) => typeof x === "string") : [];
  } catch {
    return [];
  }
}

function saveHistory(serverId: string, history: string[]) {
  try {
    localStorage.setItem(HISTORY_KEY(serverId), JSON.stringify(history.slice(-HISTORY_MAX)));
  } catch {
    // Storage full/unavailable: history just doesn't persist.
  }
}

// RecentWarnings surfaces the server's indexed log warnings (the same events
// the audit view lists) right where the operator is already looking when
// something misbehaves. Collapsed by default to a one-line summary.
function RecentWarnings({ serverId }: { serverId: string }) {
  const [expanded, setExpanded] = useState(false);
  const { data: events = [] } = useQuery({
    queryKey: ["log-events", serverId],
    queryFn: () => api.servers.logEvents(serverId, { limit: 20 }),
    refetchInterval: 30_000,
  });

  // Only surface events from the last hour — older warnings belong to a
  // previous run and would cry wolf forever.
  const recent = useMemo(() => {
    const cutoff = Date.now() - 60 * 60_000;
    return events.filter((e) => new Date(e.created_at).getTime() >= cutoff);
  }, [events]);

  if (recent.length === 0) return null;
  const errors = recent.filter((e) => e.level === "error").length;

  return (
    <div className="flex-shrink-0 border-b border-border bg-surface">
      <button
        onClick={() => setExpanded((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary transition-colors"
      >
        <AlertTriangle
          className={`h-3.5 w-3.5 ${errors > 0 ? "text-red-400" : "text-yellow-400"}`}
        />
        <span>
          {recent.length} warning{recent.length === 1 ? "" : "s"} in the last hour
          {errors > 0 && ` (${errors} error${errors === 1 ? "" : "s"})`}
        </span>
        {expanded ? (
          <ChevronUp className="ml-auto h-3.5 w-3.5" />
        ) : (
          <ChevronDown className="ml-auto h-3.5 w-3.5" />
        )}
      </button>
      {expanded && (
        <ul className="max-h-40 overflow-y-auto border-t border-border/50 px-3 py-2 space-y-1">
          {recent.slice(0, 10).map((e) => (
            <li key={e.id} className="flex items-start gap-2 text-xs">
              <span
                className={`mt-0.5 inline-block h-1.5 w-1.5 flex-shrink-0 rounded-full ${
                  e.level === "error" ? "bg-red-400" : "bg-yellow-400"
                }`}
              />
              <span className="min-w-0 flex-1 truncate text-text-primary" title={e.message}>
                {e.message}
              </span>
              <span className="flex-shrink-0 tabular-nums text-text-secondary">
                {new Date(e.created_at).toLocaleTimeString(undefined, {
                  hour: "2-digit",
                  minute: "2-digit",
                })}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function ServerTerminal({ serverId }: TerminalProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerm | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const consoleRef = useRef<ServerConsole | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const [input, setInput] = useState("");
  const [connected, setConnected] = useState(false);

  // Command history: ↑/↓ cycles through past commands; the in-progress draft
  // is kept at position -1 so cycling down past the newest entry restores it.
  const historyRef = useRef<string[]>([]);
  const historyPosRef = useRef(-1);
  const draftRef = useRef("");

  useEffect(() => {
    historyRef.current = loadHistory(serverId);
    historyPosRef.current = -1;
  }, [serverId]);

  useEffect(() => {
    if (!containerRef.current) return;

    const term = new XTerm({
      theme: {
        background: "#0f0f0f",
        foreground: "#e5e7eb",
        cursor: "#22c55e",
        selectionBackground: "#22c55e33",
      },
      fontFamily: "JetBrains Mono, Fira Code, Consolas, monospace",
      fontSize: 13,
      lineHeight: 1.4,
      cursorBlink: false,
      convertEol: true,
      scrollback: 2000,
    });

    const fit = new FitAddon();
    const webLinks = new WebLinksAddon();
    term.loadAddon(fit);
    term.loadAddon(webLinks);
    term.open(containerRef.current);
    fit.fit();

    termRef.current = term;
    fitAddonRef.current = fit;

    // Touch scrolling: xterm renders its screen on top of the scroll viewport,
    // so a native touch-drag never reaches the scrollable element and the
    // console feels "stuck" on mobile. Translate one-finger vertical drags into
    // buffer scrolls ourselves. Two-finger gestures fall through to the browser.
    const rowHeight = (term.options.fontSize ?? 13) * (term.options.lineHeight ?? 1);
    let touchY = 0;
    let scrollRemainder = 0;
    const onTouchStart = (e: TouchEvent) => {
      if (e.touches.length !== 1) return;
      touchY = e.touches[0].clientY;
      scrollRemainder = 0;
    };
    const onTouchMove = (e: TouchEvent) => {
      if (e.touches.length !== 1) return;
      const y = e.touches[0].clientY;
      // Drag up (finger moves toward top) => positive delta => scroll down.
      scrollRemainder += (touchY - y) / rowHeight;
      touchY = y;
      const lines = Math.trunc(scrollRemainder);
      if (lines !== 0) {
        scrollRemainder -= lines;
        term.scrollLines(lines);
        // Only claim the gesture once we actually scroll, so a tap-to-focus or
        // a drag on a terminal too short to scroll still behaves normally.
        e.preventDefault();
      }
    };
    const touchTarget = containerRef.current;
    touchTarget.addEventListener("touchstart", onTouchStart, { passive: true });
    touchTarget.addEventListener("touchmove", onTouchMove, { passive: false });

    const sc = new ServerConsole(serverId);
    consoleRef.current = sc;

    // Until the first status frame arrives we don't know if the server is
    // online, so say so rather than rendering an empty black box.
    term.writeln("\x1b[2m--- Connecting… ---\x1b[0m");

    const unsub = sc.on((msg) => {
      if (msg.type === "line") {
        const d = msg.data as { line: string };
        term.writeln(d.line);
      } else if (msg.type === "status") {
        const d = msg.data as { status: string };
        const color = d.status === "online" ? "\x1b[32m" : "\x1b[33m";
        term.writeln(
          `\x1b[2m--- Server ${color}${d.status}\x1b[0m\x1b[2m ---\x1b[0m`,
        );
        setConnected(d.status === "online");
      }
    });

    sc.connect();

    const resizeObserver = new ResizeObserver(() => fit.fit());
    resizeObserver.observe(containerRef.current);

    return () => {
      unsub();
      sc.disconnect();
      resizeObserver.disconnect();
      touchTarget.removeEventListener("touchstart", onTouchStart);
      touchTarget.removeEventListener("touchmove", onTouchMove);
      term.dispose();
    };
  }, [serverId]);

  const suggestions = useMemo(
    () => (connected ? matchCommands(input).slice(0, 5) : []),
    [input, connected],
  );

  const sendCommand = () => {
    const cmd = input.trim();
    if (!cmd || !connected || !consoleRef.current) return;
    consoleRef.current.send(cmd);
    // Record into history, deduping an immediate repeat.
    const h = historyRef.current;
    if (h[h.length - 1] !== cmd) {
      h.push(cmd);
      saveHistory(serverId, h);
    }
    historyPosRef.current = -1;
    draftRef.current = "";
    setInput("");
  };

  const cycleHistory = (dir: -1 | 1) => {
    const h = historyRef.current;
    if (h.length === 0) return;
    let pos = historyPosRef.current;
    if (pos === -1) {
      if (dir === 1) return; // nothing newer than the draft
      draftRef.current = input;
      pos = h.length - 1;
    } else {
      pos += dir;
    }
    if (pos >= h.length) {
      historyPosRef.current = -1;
      setInput(draftRef.current);
      return;
    }
    if (pos < 0) pos = 0;
    historyPosRef.current = pos;
    setInput(h[pos]);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter") {
      sendCommand();
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      cycleHistory(-1);
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      cycleHistory(1);
    } else if (e.key === "Tab" && suggestions.length > 0) {
      e.preventDefault();
      setInput(suggestions[0].cmd + " ");
    }
  };

  return (
    <div className="flex flex-col h-full min-h-0 bg-[#0f0f0f] rounded-lg border border-border overflow-hidden">
      <RecentWarnings serverId={serverId} />
      <div ref={containerRef} className="flex-1 min-h-0 p-2" />
      {suggestions.length > 0 && (
        <div className="flex flex-shrink-0 flex-wrap items-center gap-1.5 border-t border-border bg-surface px-3 py-1.5">
          {suggestions.map((s, i) => (
            <button
              key={s.cmd}
              onClick={() => {
                setInput(s.cmd + " ");
                inputRef.current?.focus();
              }}
              className={`rounded border px-2 py-0.5 font-mono text-xs transition-colors ${
                i === 0
                  ? "border-accent/40 bg-accent/10 text-accent"
                  : "border-border bg-surface-2 text-text-secondary hover:text-text-primary"
              }`}
              title={s.hint ?? s.cmd}
            >
              {s.cmd}
            </button>
          ))}
          <span className="ml-auto hidden text-[10px] text-text-secondary sm:block">
            Tab completes · ↑↓ history
          </span>
        </div>
      )}
      <div className="flex flex-shrink-0 items-center gap-2 px-3 py-2 border-t border-border bg-surface">
        <span className="text-text-secondary text-sm font-mono flex-shrink-0">
          {connected ? (
            <span className="text-green-400">▶</span>
          ) : (
            <span className="text-gray-500">○</span>
          )}{" "}
          &gt;
        </span>
        <input
          ref={inputRef}
          type="text"
          className="flex-1 bg-transparent text-sm font-mono text-text-primary placeholder:text-text-secondary/40 focus:outline-none disabled:cursor-not-allowed disabled:opacity-50"
          placeholder={
            connected
              ? "Enter command…"
              : "Server offline — start it to send commands"
          }
          value={input}
          onChange={(e) => {
            setInput(e.target.value);
            historyPosRef.current = -1;
          }}
          onKeyDown={handleKeyDown}
          disabled={!connected}
          aria-label="Server console command"
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          enterKeyHint="send"
        />
        <button
          onClick={sendCommand}
          disabled={!connected}
          aria-label="Send command to server console"
          className="flex-shrink-0 text-xs text-text-secondary hover:text-text-primary px-2 py-1 rounded border border-border hover:border-border-hover transition-colors disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:text-text-secondary disabled:hover:border-border"
        >
          Send
        </button>
      </div>
    </div>
  );
}
