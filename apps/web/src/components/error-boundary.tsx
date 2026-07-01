import { Component, type ErrorInfo, type ReactNode } from "react";
import { AlertTriangle, RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";

function ErrorFallback({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const message = error instanceof Error ? error.message : String(error);
  return (
    <div className="flex min-h-[50vh] items-center justify-center p-6">
      <div className="max-w-md rounded-lg border border-border bg-surface p-6 text-center">
        <AlertTriangle className="mx-auto mb-3 h-8 w-8 text-danger" />
        <h2 className="mb-1 text-base font-semibold text-text-primary">Something went wrong</h2>
        <p className="mb-4 break-words text-sm text-text-secondary">{message}</p>
        <Button onClick={onRetry}>
          <RotateCcw className="mr-1.5 h-4 w-4" />
          Reload
        </Button>
      </div>
    </div>
  );
}

// RouteErrorFallback is TanStack Router's defaultErrorComponent: a render
// error inside a route replaces just that route's outlet, and retry re-renders
// the route without a full page load.
export function RouteErrorFallback({ error, reset }: { error: Error; reset?: () => void }) {
  return <ErrorFallback error={error} onRetry={() => (reset ? reset() : window.location.reload())} />;
}

// AppErrorBoundary is the last line of defense around the router itself —
// without it a crash outside any route (store init, provider code) unmounts
// React and leaves a blank page.
export class AppErrorBoundary extends Component<{ children: ReactNode }, { error: unknown }> {
  state: { error: unknown } = { error: null };

  static getDerivedStateFromError(error: unknown) {
    return { error };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error("Unhandled render error:", error, info.componentStack);
  }

  render() {
    if (this.state.error !== null) {
      return <ErrorFallback error={this.state.error} onRetry={() => window.location.reload()} />;
    }
    return this.props.children;
  }
}
