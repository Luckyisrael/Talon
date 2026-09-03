import { HTMLAttributes, forwardRef } from "react";

interface StatusBadgeProps extends HTMLAttributes<HTMLSpanElement> {
  variant?: "free" | "pro" | "active" | "paused" | "error";
  size?: "sm" | "md";
}

/**
 * System badge: 4px radius, Inter 12px/400, fog on subtle fill. Colour appears
 * only where it states a condition — pulse-green for actually-running, coral
 * for error. Tiers stay neutral and differ only in emphasis.
 */
const StatusBadge = forwardRef<HTMLSpanElement, StatusBadgeProps>(
  ({ className = "", variant = "free", size = "sm", children, ...props }, ref) => {
    const base =
      "inline-flex items-center rounded-[4px] border border-transparent font-sans";

    const variants = {
      free: "bg-white/5 text-fog",
      pro: "bg-white/5 text-mist border-white/10",
      active: "bg-pulse-green/10 border-pulse-green/30 text-[#4cc069]",
      paused: "bg-white/5 text-ash",
      error: "bg-coral-red/10 border-coral-red/30 text-[#f08080]",
    };

    const sizes = {
      sm: "h-5 px-1.5 text-xs",
      md: "h-6 px-2.5 text-xs",
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
