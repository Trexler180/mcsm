import {
  Fragment,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import type { ServerSection, ServerSectionItem } from "./shared";

// A springy ease with a touch of overshoot so the moving highlight "lands"
// on the target button with momentum instead of a flat slide.
const SPRING = "cubic-bezier(0.34, 1.35, 0.5, 1)";

type SectionGroup = { group: string; items: ServerSectionItem[] };

/**
 * The server-detail section switcher.
 *
 * Both the desktop sidebar and the mobile strip render a single, shared
 * "indicator" element positioned behind the buttons. On tab change we measure
 * the active button's box and animate the indicator's transform/size to it, so
 * the highlight glides from the old tab to the new one instead of teleporting.
 */
export function SectionNav({
  groups,
  active,
  onSelect,
}: {
  groups: SectionGroup[];
  active: ServerSection;
  onSelect: (section: ServerSection) => void;
}) {
  const deskNav = useRef<HTMLElement | null>(null);
  const mobTrack = useRef<HTMLDivElement | null>(null);
  const deskBtns = useRef(new Map<string, HTMLButtonElement>());
  const mobBtns = useRef(new Map<string, HTMLButtonElement>());

  const [desk, setDesk] = useState<{ top: number; height: number } | null>(null);
  const [mob, setMob] = useState<{ left: number; width: number } | null>(null);
  // Suppress the transition on first paint so the indicator appears in place
  // rather than flying in from the corner.
  const [animate, setAnimate] = useState(false);

  const registerDesk = useCallback(
    (value: string) => (el: HTMLButtonElement | null) => {
      if (el) deskBtns.current.set(value, el);
      else deskBtns.current.delete(value);
    },
    [],
  );
  const registerMob = useCallback(
    (value: string) => (el: HTMLButtonElement | null) => {
      if (el) mobBtns.current.set(value, el);
      else mobBtns.current.delete(value);
    },
    [],
  );

  const measure = useCallback(() => {
    const nav = deskNav.current;
    const deskBtn = deskBtns.current.get(active);
    if (nav && deskBtn) {
      const navRect = nav.getBoundingClientRect();
      const btnRect = deskBtn.getBoundingClientRect();
      setDesk({ top: btnRect.top - navRect.top + nav.scrollTop, height: btnRect.height });
    }
    const track = mobTrack.current;
    const mobBtn = mobBtns.current.get(active);
    if (track && mobBtn) {
      // Measure relative to the (scrolling) track so scroll position doesn't
      // offset the indicator — it lives inside the track and scrolls with it.
      const trackRect = track.getBoundingClientRect();
      const btnRect = mobBtn.getBoundingClientRect();
      setMob({ left: btnRect.left - trackRect.left, width: btnRect.width });
    }
  }, [active]);

  // Re-measure whenever the active tab or the set of groups changes.
  useLayoutEffect(() => {
    measure();
  }, [measure, groups]);

  // Enable transitions after the first commit.
  useEffect(() => {
    const id = requestAnimationFrame(() => setAnimate(true));
    return () => cancelAnimationFrame(id);
  }, []);

  // Keep the indicator aligned when the layout reflows (breakpoint change,
  // sidebar resize, font load, orientation change).
  useEffect(() => {
    const onResize = () => measure();
    window.addEventListener("resize", onResize);
    const ro = new ResizeObserver(() => measure());
    if (deskNav.current) ro.observe(deskNav.current);
    if (mobTrack.current) ro.observe(mobTrack.current);
    return () => {
      window.removeEventListener("resize", onResize);
      ro.disconnect();
    };
  }, [measure]);

  // Bring the active pill into view on the mobile strip when the section
  // changes (e.g. via deep link or the dashboard quick-links).
  useEffect(() => {
    mobBtns.current
      .get(active)
      ?.scrollIntoView({ inline: "center", block: "nearest" });
  }, [active]);

  return (
    <>
      {/* Mobile: a horizontally scrollable strip of pill tabs. Groups are
          separated by a thin divider; the active pill's highlight slides
          between tabs and resizes to fit each label. */}
      <div
        className="-mx-2 overflow-x-auto px-2 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden md:hidden"
        role="tablist"
        aria-label="Server sections"
      >
        <div ref={mobTrack} className="relative flex w-max items-center gap-1.5 py-0.5">
          {mob && (
            <span
              aria-hidden="true"
              className="pointer-events-none absolute top-1/2 h-9 rounded-full border border-accent/40 bg-accent/15"
              style={{
                left: 0,
                width: mob.width,
                transform: `translate(${mob.left}px, -50%)`,
                transition: animate
                  ? `transform 380ms ${SPRING}, width 380ms ${SPRING}`
                  : "none",
              }}
            />
          )}
          {groups.map((g, gi) => (
            <Fragment key={g.group}>
              {gi > 0 && (
                <span
                  className="mx-0.5 h-5 w-px flex-shrink-0 bg-border"
                  aria-hidden="true"
                />
              )}
              {g.items.map((section) => {
                const Icon = section.icon;
                const isActive = active === section.value;
                return (
                  <button
                    key={section.value}
                    ref={registerMob(section.value)}
                    role="tab"
                    aria-selected={isActive}
                    onClick={() => onSelect(section.value)}
                    className={`relative z-10 flex h-9 flex-shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full px-3.5 text-sm transition-colors ${
                      isActive
                        ? "font-medium text-text-primary"
                        : "text-text-secondary hover:text-text-primary"
                    }`}
                  >
                    <Icon
                      className={`h-4 w-4 flex-shrink-0 transition-colors ${
                        isActive ? "text-accent" : ""
                      }`}
                    />
                    {section.label}
                  </button>
                );
              })}
            </Fragment>
          ))}
        </div>
      </div>

      {/* Desktop: vertical sidebar nav, grouped with section headings. The
          active row's tinted highlight + accent rail glides between sections. */}
      <nav
        ref={deskNav}
        className="relative hidden md:flex md:flex-col md:gap-0.5"
        aria-label="Server sections"
      >
        {desk && (
          <span
            aria-hidden="true"
            className="pointer-events-none absolute inset-x-0 top-0 rounded-md bg-accent/10"
            style={{
              height: desk.height,
              transform: `translateY(${desk.top}px)`,
              transition: animate
                ? `transform 360ms ${SPRING}, height 360ms ${SPRING}`
                : "none",
            }}
          />
        )}
        {groups.map((g, gi) => (
          <div key={g.group} className={gi > 0 ? "md:mt-2" : ""}>
            <p className="px-3 pb-1.5 pt-1 text-[10px] font-semibold uppercase tracking-wider text-text-secondary/50">
              {g.group}
            </p>
            {g.items.map((section) => {
              const Icon = section.icon;
              const isActive = active === section.value;
              return (
                <button
                  key={section.value}
                  ref={registerDesk(section.value)}
                  onClick={() => onSelect(section.value)}
                  aria-current={isActive ? "page" : undefined}
                  className={`group relative z-10 flex h-9 w-full items-center gap-2.5 rounded-md pl-3.5 pr-3 text-left text-sm transition-colors ${
                    isActive
                      ? "font-medium text-text-primary"
                      : "text-text-secondary hover:bg-surface-2 hover:text-text-primary"
                  }`}
                >
                  <Icon
                    className={`h-4 w-4 flex-shrink-0 transition-colors ${
                      isActive
                        ? "text-accent"
                        : "text-text-secondary group-hover:text-text-primary"
                    }`}
                  />
                  {section.label}
                </button>
              );
            })}
          </div>
        ))}
      </nav>
    </>
  );
}
