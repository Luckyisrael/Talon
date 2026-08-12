import { HTMLAttributes, forwardRef } from "react";

interface TonalCardProps extends HTMLAttributes<HTMLDivElement> {
  variant?: "default" | "elevated" | "outlined";
  padding?: "none" | "sm" | "md" | "lg";
}

/**
 * A panel on the bench: flat, bordered, machined radius. Separation comes from
 * the rail hairline and a step in surface tone, not from a blur shadow — the
 * surface is metal, not paper stacked on paper.
 */
const TonalCard = forwardRef<HTMLDivElement, TonalCardProps>(
  ({ className = "", variant = "default", padding = "md", children, ...props }, ref) => {
    const base = "rounded-2 border transition-colors";

    const variants = {
      default: "bg-slate border-rail",
      elevated: "bg-riser border-rail",
      outlined: "bg-transparent border-rail",
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
