import { Component, type ReactNode } from "react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";

/**
 * The designer paints the step/root editor into a React root of its own, so the
 * providers mounted in main.tsx do not reach that subtree. Editors that use
 * React Query (the user picker) would throw and leave an empty panel, so every
 * editor is wrapped in this host, which re-provides the app's query client
 * inside the designer's root.
 */
export function EditorHost({ client, children }: { client: QueryClient; children: ReactNode }) {
  return (
    <QueryClientProvider client={client}>
      <EditorErrorBoundary>{children}</EditorErrorBoundary>
    </QueryClientProvider>
  );
}

/** Keeps a broken editor readable instead of silently collapsing the panel. */
class EditorErrorBoundary extends Component<{ children: ReactNode }, { error: string | null }> {
  state: { error: string | null } = { error: null };

  static getDerivedStateFromError(error: unknown) {
    return { error: error instanceof Error ? error.message : String(error) };
  }

  componentDidCatch(error: unknown) {
    console.error("Workflow editor failed to render", error);
  }

  render() {
    const { error } = this.state;
    if (error !== null) {
      return <p className="err">Could not show the settings for this step: {error}</p>;
    }
    return this.props.children;
  }
}
