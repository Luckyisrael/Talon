import { HTMLAttributes, forwardRef } from "react";

interface StatusBadgeProps extends HTMLAttributes<HTMLSpanElement> {
  variant?: "free" | "pro" | "active" | "paused" | "error";
  size?: "sm" | "md";
}

const StatusBadge = forwardRef<HTMLSpanElement, StatusBadgeProps>(
  ({ className = "", variant = "free", size = "sm", children, ...props }, ref) => {
    const baseStyles = "pill font-mono uppercase tracking-wider inline-flex items-center";

    const variants = {
      free: "bg-surface-container-high text-on-surface-variant",
      pro: "bg-solana-purple text-white",
      active: "bg-success-container text-success",
      paused: "bg-surface-container-highest text-on-surface-variant",
      error: "bg-error-container text-error",
    };

    const sizes = {
      sm: "h-5 px-2 text-[10px]",
      md: "h-6 px-3 text-xs",
    };

    return (
      <span
        ref={ref}
        className={`${baseStyles} ${variants[variant]} ${sizes[size]} ${className}`}
        {...props}
      >
        {children || variant.toUpperCase()}
      </span>
    );
  }
);

StatusBadge.displayName = "StatusBadge";

export default StatusBadge;
