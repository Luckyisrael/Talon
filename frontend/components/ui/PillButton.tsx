import { ButtonHTMLAttributes, forwardRef } from "react";

interface PillButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "primary" | "secondary" | "ghost";
  size?: "sm" | "md" | "lg";
}

/**
 * The name is kept for its call sites, but the shape is no longer a pill — on
 * the bench nothing is fully round except the signal lamp. This is a machined
 * control: tight radius, neutral fills. `primary` reads as high-contrast paper
 * rather than a semantic hue, because signal/probe/filament/fault each mean one
 * real thing and "this is the button you want" is not one of them.
 */
const PillButton = forwardRef<HTMLButtonElement, PillButtonProps>(
  ({ className = "", variant = "primary", size = "md", children, ...props }, ref) => {
    const base =
      "inline-flex items-center justify-center gap-2 rounded-1 font-medium whitespace-nowrap transition-colors disabled:opacity-40 disabled:cursor-not-allowed";

    const variants = {
      primary: "bg-paper text-ink hover:bg-white",
      secondary: "bg-riser text-paper border border-rail hover:bg-rail hover:border-edge",
      ghost:
        "bg-transparent text-graphite border border-transparent hover:bg-riser hover:border-rail hover:text-paper",
    };

    const sizes = {
      sm: "h-8 px-3 text-small",
      md: "h-9 px-4 text-small",
      lg: "h-11 px-6 text-body",
    };

    return (
      <button
        ref={ref}
        className={`${base} ${variants[variant]} ${sizes[size]} ${className}`}
        {...props}
      >
        {children}
      </button>
    );
  }
);

PillButton.displayName = "PillButton";

export default PillButton;
