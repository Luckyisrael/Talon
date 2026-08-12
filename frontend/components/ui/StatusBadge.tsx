import { HTMLAttributes, forwardRef } from "react";

interface StatusBadgeProps extends HTMLAttributes<HTMLSpanElement> {
  variant?: "free" | "pro" | "active" | "paused" | "error";
  size?: "sm" | "md";
}

/**
 * Mono, uppercase, tracked — the same register as an instrument label, because
 * a badge names a real condition. Colour appears only where it states one:
 * "active" borrows signal because the project is actually running, "error" is a
 * fault. A tier is a plan, not a live state, so free/pro stay neutral and
 * differ only in emphasis.
 */
const StatusBadge = forwardRef<HTMLSpanElement, StatusBadgeProps>(
  ({ className = "", variant = "free", size = "sm", children, ...props }, ref) => {
    const base =
      "inline-flex items-center rounded-1 border font-mono uppercase tracking-wider";

    const variants = {
      free: "bg-riser border-rail text-dust",
      pro: "bg-riser border-edge text-paper",
      active: "bg-signal/10 border-signal/30 text-signal",
      paused: "bg-riser border-rail text-graphite",
      error: "bg-fault/10 border-fault/30 text-fault",
    };

    const sizes = {
      sm: "h-5 px-2 text-micro",
      md: "h-6 px-2.5 text-tiny",
    };

    return (
      <span
        ref={ref}
        className={`${base} ${variants[variant]} ${sizes[size]} ${className}`}
        {...props}
      >
        {children || variant.toUpperCase()}
      </span>
    );
  }
);

StatusBadge.displayName = "StatusBadge";

export default StatusBadge;
