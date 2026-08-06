import { HTMLAttributes, forwardRef } from "react";

interface TonalCardProps extends HTMLAttributes<HTMLDivElement> {
  variant?: "default" | "elevated" | "outlined";
  padding?: "none" | "sm" | "md" | "lg";
}

const TonalCard = forwardRef<HTMLDivElement, TonalCardProps>(
  ({ className = "", variant = "default", padding = "md", children, ...props }, ref) => {
    const baseStyles = "rounded-xl transition-all duration-150";

    const variants = {
      default: "bg-surface-3",
      elevated: "bg-surface-3 shadow-md",
      outlined: "bg-surface-2",
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
        className={`${baseStyles} ${variants[variant]} ${paddings[padding]} ${className}`}
        {...props}
      >
        {children}
      </div>
    );
  }
);

TonalCard.displayName = "TonalCard";

export default TonalCard;
