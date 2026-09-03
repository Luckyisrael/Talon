import { HTMLAttributes, forwardRef } from "react";

interface TonalCardProps extends HTMLAttributes<HTMLDivElement> {
  variant?: "default" | "elevated" | "outlined";
  padding?: "none" | "sm" | "md" | "lg";
}

/**
 * A Linear card: carbon surface, 12px radius, hairline inset edge. Separation
 * comes from the border, never an outer shadow.
 */
const TonalCard = forwardRef<HTMLDivElement, TonalCardProps>(
  ({ className = "", variant = "default", padding = "md", children, ...props }, ref) => {
    const base = "rounded-xl transition-colors shadow-subtle";

    const variants = {
      default: "bg-carbon",
      elevated: "bg-obsidian",
      outlined: "bg-transparent border border-graphite",
    };

    const paddings = {
      none: "",
      sm: "p-3",
      md: "p-4",
      lg: "p-6",
    };

    return (
      <div
        ref={ref}
        className={`${base} ${variants[variant]} ${paddings[padding]} ${className}`}
        {...props}
      >
        {children}
      </div>
    );
  }
);

TonalCard.displayName = "TonalCard";

export default TonalCard;
