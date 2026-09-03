"use client";

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";

interface ResizablePanelProps {
  children: ReactNode;
  direction: "horizontal" | "vertical";
  defaultSize: number;
  minSize?: number;
  maxSize?: number;
  className?: string;
  /** Which edge the drag handle sits on. Default "end" — right, or bottom. */
  handleSide?: "start" | "end";
  /** Hold the panel at `collapsedSize` and disable dragging. */
  collapsed?: boolean;
  collapsedSize?: number;
  /** Names what is being resized, for screen readers. */
  label?: string;
}

const STEP = 24;

export default function ResizablePanel({
  children,
  direction,
  defaultSize,
  minSize = 80,
  maxSize = Infinity,
  className = "",
  handleSide = "end",
  collapsed = false,
  collapsedSize = 32,
  label = "panel",
}: ResizablePanelProps) {
  const [size, setSize] = useState(defaultSize);
  const dragging = useRef(false);
  const startPos = useRef(0);
  const startSize = useRef(0);

  const horizontal = direction === "horizontal";
  const atStart = handleSide === "start";
  const shown = collapsed ? collapsedSize : size;

  const clamp = useCallback(
    (n: number) => Math.max(minSize, Math.min(maxSize, n)),
    [maxSize, minSize],
  );

  const onPointerDown = useCallback(
    (e: React.PointerEvent) => {
      if (collapsed) return;
      e.preventDefault();
      e.stopPropagation();
      dragging.current = true;
      startPos.current = horizontal ? e.clientX : e.clientY;
      startSize.current = size;
      document.body.style.cursor = horizontal ? "col-resize" : "row-resize";
      document.body.style.userSelect = "none";
    },
    [collapsed, horizontal, size],
  );

  useEffect(() => {
    // Pointer events rather than mouse events, so a trackpad or a touchscreen
    // can resize the panels too.
    function onMove(e: PointerEvent) {
      if (!dragging.current) return;
      const delta = (horizontal ? e.clientX : e.clientY) - startPos.current;
      setSize(clamp(startSize.current + (atStart ? -delta : delta)));
    }
    function onUp() {
      if (!dragging.current) return;
      dragging.current = false;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    }
    document.addEventListener("pointermove", onMove);
    document.addEventListener("pointerup", onUp);
    document.addEventListener("pointercancel", onUp);
    return () => {
      document.removeEventListener("pointermove", onMove);
      document.removeEventListener("pointerup", onUp);
      document.removeEventListener("pointercancel", onUp);
    };
  }, [atStart, clamp, horizontal]);

  // Arrow keys move the divider. A layout that can only be adjusted with a
  // mouse cannot be adjusted at all by some of the people using it.
  const onKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (collapsed) return;
      const grow = horizontal ? "ArrowRight" : "ArrowDown";
      const shrink = horizontal ? "ArrowLeft" : "ArrowUp";
      if (e.key !== grow && e.key !== shrink) return;
      e.preventDefault();
      const towardsEnd = e.key === grow;
      const delta = (atStart ? -1 : 1) * (towardsEnd ? STEP : -STEP);
      setSize((s) => clamp(s + delta));
    },
    [atStart, clamp, collapsed, horizontal],
  );

  return (
    <div
      className={`relative flex-shrink-0 ${className}`}
      style={
        horizontal
          ? { width: shown, minWidth: shown, maxWidth: shown, height: "100%" }
          : { height: shown, minHeight: shown, maxHeight: shown, width: "100%" }
      }
    >
      <div className="h-full w-full overflow-hidden">{children}</div>

      {!collapsed && (
        <div
          role="separator"
          aria-label={`Resize the ${label}`}
          aria-orientation={horizontal ? "vertical" : "horizontal"}
          tabIndex={0}
          onPointerDown={onPointerDown}
          onKeyDown={onKeyDown}
          // The hit area straddles the boundary and is wider than the line it
          // draws, because a 1px target is a target you miss.
          className={`group absolute z-10 ${
            horizontal
              ? `top-0 bottom-0 w-2 cursor-col-resize ${atStart ? "-left-1" : "-right-1"}`
              : `left-0 right-0 h-2 cursor-row-resize ${atStart ? "-top-1" : "-bottom-1"}`
          }`}
        >
          <span
            aria-hidden
            className={`absolute bg-transparent transition-colors group-hover:bg-acid-lime/60 group-focus-visible:bg-acid-lime ${
              horizontal
                ? "top-0 bottom-0 left-1/2 w-[2px] -translate-x-1/2"
                : "left-0 right-0 top-1/2 h-[2px] -translate-y-1/2"
            }`}
          />
        </div>
      )}
    </div>
  );
}
