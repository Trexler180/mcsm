import { useEffect, useRef, useState } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import { ServerConsole } from "@/lib/ws";

interface TerminalProps {
  serverId: string;
}

export function ServerTerminal({ serverId }: TerminalProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<XTerm | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const consoleRef = useRef<ServerConsole | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const [input, setInput] = useState("");
  const [connected, setConnected] = useState(false);

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

    const sc = new ServerConsole(serverId);
    consoleRef.current = sc;

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
      term.dispose();
    };
  }, [serverId]);

  const sendCommand = () => {
    const cmd = input.trim();
    if (!cmd || !consoleRef.current) return;
    consoleRef.current.send(cmd);
    setInput("");
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter") sendCommand();
  };

  return (
    <div className="flex flex-col h-full min-h-0 bg-[#0f0f0f] rounded-lg border border-border overflow-hidden">
      <div ref={containerRef} className="flex-1 min-h-0 p-2" />
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
          className="flex-1 bg-transparent text-sm font-mono text-text-primary placeholder:text-text-secondary/40 focus:outline-none"
          placeholder="Enter command…"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
        />
        <button
          onClick={sendCommand}
          className="text-xs text-text-secondary hover:text-text-primary px-2 py-1 rounded border border-border hover:border-border-hover transition-colors"
        >
          Send
        </button>
      </div>
    </div>
  );
}
