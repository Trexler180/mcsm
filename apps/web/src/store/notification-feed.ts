import { create } from "zustand";
import type { NotificationItem } from "@/lib/types";

const MAX_ITEMS = 100;

interface FeedState {
  items: NotificationItem[];
  unread: number;
  connected: boolean;
  // Replace the feed with a freshly fetched page (on login / manual refresh).
  setInitial: (items: NotificationItem[], unread: number) => void;
  // A live item arrived over the stream.
  pushItem: (item: NotificationItem) => void;
  markRead: (id: string) => void;
  markAllRead: () => void;
  setConnected: (connected: boolean) => void;
  reset: () => void;
}

export const useNotificationFeed = create<FeedState>()((set) => ({
  items: [],
  unread: 0,
  connected: false,
  setInitial: (items, unread) => set({ items, unread }),
  pushItem: (item) =>
    set((s) => {
      // Guard against a duplicate if the same item also arrives via a refetch.
      if (s.items.some((x) => x.id === item.id)) return s;
      return {
        items: [item, ...s.items].slice(0, MAX_ITEMS),
        unread: item.read_at ? s.unread : s.unread + 1,
      };
    }),
  markRead: (id) =>
    set((s) => {
      const item = s.items.find((x) => x.id === id);
      if (!item || item.read_at) return s;
      return {
        items: s.items.map((x) =>
          x.id === id ? { ...x, read_at: new Date().toISOString() } : x,
        ),
        unread: Math.max(0, s.unread - 1),
      };
    }),
  markAllRead: () =>
    set((s) => ({
      items: s.items.map((x) =>
        x.read_at ? x : { ...x, read_at: new Date().toISOString() },
      ),
      unread: 0,
    })),
  setConnected: (connected) => set({ connected }),
  reset: () => set({ items: [], unread: 0, connected: false }),
}));
