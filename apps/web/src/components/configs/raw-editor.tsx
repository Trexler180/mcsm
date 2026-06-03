import { useEffect, useRef } from "react";
import { EditorView, basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
import { oneDark } from "@codemirror/theme-one-dark";
import { json } from "@codemirror/lang-json";
import { yaml } from "@codemirror/lang-yaml";

function extensionsFor(filename: string) {
  const ext = filename.split(".").pop()?.toLowerCase();
  switch (ext) {
    case "json":
    case "json5":
    case "jsonc":
      return [json()];
    case "yml":
    case "yaml":
      return [yaml()];
    default:
      return [];
  }
}

interface RawEditorProps {
  filename: string;
  value: string;
  onChange: (value: string) => void;
  /** Bumping this key reseeds the editor with `value` (mode switch / reload). */
  resetKey: string;
}

// A controlled CodeMirror editor. We reseed only when `resetKey` changes so the
// user's cursor isn't reset on every keystroke.
export function RawEditor({ filename, value, onChange, resetKey }: RawEditorProps) {
  const hostRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  useEffect(() => {
    if (!hostRef.current) return;
    const state = EditorState.create({
      doc: value,
      extensions: [
        basicSetup,
        oneDark,
        ...extensionsFor(filename),
        EditorView.updateListener.of((u) => {
          if (u.docChanged) onChangeRef.current(u.state.doc.toString());
        }),
        EditorView.theme({
          "&": { height: "100%", backgroundColor: "#0f0f0f" },
          ".cm-scroller": {
            overflow: "auto",
            fontFamily: "JetBrains Mono, Consolas, monospace",
            fontSize: "13px",
          },
        }),
      ],
    });
    viewRef.current?.destroy();
    const view = new EditorView({ state, parent: hostRef.current });
    viewRef.current = view;
    return () => {
      view.destroy();
      viewRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resetKey, filename]);

  return <div ref={hostRef} className="h-full" />;
}
