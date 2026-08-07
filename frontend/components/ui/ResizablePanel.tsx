"use client";

import { useState, useCallback, useRef, useEffect, ReactNode } from "react";

interface ResizablePanelProps {
  children: ReactNode;
  direction: "horizontal" | "vertical";
  defaultSize: number;
  minSize?: number;
  maxSize?: number;
  className?: string;
  /** Which edge the drag handle sits on. Default: "end" (right for horizontal, bottom for vertical) */
  handleSide?: "start" | "end";
}

export default function ResizablePanel({
  children,
  direction,
  defaultSize,
  minSize = 80,
  maxSize = Infinity,
  className = "",
  handleSide = "end",
}: ResizablePanelProps) {
  const [size, setSize] = useState(defaultSize);
  const dragging = useRef(false);
  const startPos = useRef(0);
  const startSize = useRef(0);

  const isHorizontal = direction === "horizontal";
  const isStart = handleSide === "start";

  const onMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      e.stopPropagation();
      dragging.current = true;
      startPos.current = isHorizontal ? e.clientX : e.clientY;
      startSize.current = size;
      document.body.style.cursor = isHorizontal ? "col-resize" : "row-resize";
      document.body.style.userSelect = "none";
    },
    [isHorizontal, size]
  );

  useEffect(() => {
    function onMouseMove(e: MouseEvent) {
      if (!dragging.current) return;
      const delta = isHorizontal
        ? e.clientX - startPos.current
        : e.clientY - startPos.current;
      // start means handle is on left/top — dragging right/down GROWS the panel
      // end means handle is on right/bottom — dragging right/down SHRINKS the panel
      const effectiveDelta = isStart ? delta : -delta;
      let newSize = startSize.current + effectiveDelta;
      newSize = Math.max(minSize, Math.min(maxSize, newSize));
      setSize(newSize);
    }

    function onMouseUp() {
      if (!dragging.current) return;
      dragging.current = false;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    }

    document.addEventListener("mousemove", onMouseMove);
    document.addEventListener("mouseup", onMouseUp);
    return () => {
      document.removeEventListener("mousemove", onMouseMove);
      document.removeEventListener("mouseup", onMouseUp);
    };
  }, [isHorizontal, isStart, minSize, maxSize]);

  const outerStyle: React.CSSProperties = isHorizontal
    ? { width: `${size}px`, minWidth: `${size}px`, maxWidth: `${size}px`, height: "100%" }
    : { height: `${size}px`, minHeight: `${size}px`, maxHeight: `${size}px`, width: "100%" };

  // Handle position: inside the panel at the edge, NOT extending beyond
  const handleStyle: React.CSSProperties = isHorizontal
    ? {
        position: "absolute",
        top: 0,
        bottom: 0,
        ...(isStart
          ? { left: -4, width: "8px" }   // left edge: handle straddles the boundary
          : { right: -4, width: "8px" }), // right edge
        cursor: "col-resize",
        zIndex: 10,
      }
    : {
        position: "absolute",
        left: 0,
        right: 0,
        ...(isStart
          ? { top: -4, height: "8px" }   // top edge: handle straddles the boundary
          : { bottom: -4, height: "8px" }), // bottom edge
        cursor: "row-resize",
        zIndex: 10,
      };

  // The visible line position (centered on the boundary)
  const lineStyle: React.CSSProperties = isHorizontal
    ? { top: 0, bottom: 0, left: "50%", width: "1px" }
    : { left: 0, right: 0, top: "50%", height: "1px" };

  const hoverLineStyle: React.CSSProperties = isHorizontal
    ? { top: 0, bottom: 0, left: "50%", width: "2px" }
    : { left: 0, right: 0, top: "50%", height: "2px" };

  return (
    <div
      className={className}
      style={{
        ...outerStyle,
        flexShrink: 0,
        position: "relative",
      }}
    >
      {/* Content */}
      <div style={{ width: "100%", height: "100%", overflow: "hidden" }}>
        {children}
      </div>

      {/* Drag handle */}
      <div
        onMouseDown={onMouseDown}
        style={handleStyle}
        className="group"
      >
        <div className="absolute inset-0" />
        <div
          className="absolute transition-colors"
          style={{ ...lineStyle, background: "var(--color-surface-3)" }}
        />
        <div
          className="absolute transition-colors opacity-0 group-hover:opacity-100"
          style={{ ...hoverLineStyle, background: "var(--color-accent)" }}
        />
      </div>
    </div>
  );
}
