import { ShieldAlert } from "lucide-react";
import { EmptyState } from "@/components/ui/empty-state";
import { Header } from "@/components/layout/header";
import { useAuthStore } from "@/store/auth";

// Hiding an admin page from the sidebar is presentation, not access control: the
// URL still resolves, so a bookmark, a shared link, or a stale tab drops a
// non-admin straight onto the page. The API refuses every call it makes, so
// nothing leaks — but what the user sees is a page of enabled buttons that fail
// on click. This says the actual reason once, up front.
//
// The root layout blocks on auth before rendering any route, so `user` is
// settled by the time this runs.
export function RequireAdmin({
  title,
  children,
}: {
  /** Page title for the header shown in place of the page. */
  title: string;
  children: React.ReactNode;
}) {
  const user = useAuthStore((s) => s.user);
  if (user?.role === "admin") return <>{children}</>;
  return (
    <div className="flex flex-col">
      <Header title={title} />
      <EmptyState
        icon={ShieldAlert}
        title="Administrators only"
        hint="This page is limited to administrators. Ask an administrator if you need access to it."
      />
    </div>
  );
}
