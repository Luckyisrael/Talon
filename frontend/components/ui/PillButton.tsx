import { ButtonHTMLAttributes, forwardRef } from "react";

interface PillButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "primary" | "secondary" | "ghost";
  size?: "sm" | "md" | "lg";
}

/**
 * The shared action button. Despite the legacy name, the shape follows the
 * system: 6px radius, Inter 13–14px at weight 500. `primary` is the single
 * chromatic action per view (acid lime on void); everything else stays
 * neutral. For true pills (tags, chips) use the `.pill` class; for the white
 * nav CTA use `.btn-pill`.
 */
const PillButton = forwardRef<HTMLButtonElement, PillButtonProps>(
  ({ className = "", variant = "primary", size = "md", children, ...props }, ref) => {
    const base =
      "inline-flex items-center justify-center gap-2 rounded-md font-medium whitespace-nowrap transition-colors disabled:opacity-40 disabled:cursor-not-allowed";

    const variants = {
      primary: "bg-acid-lime text-void hover:bg-[#eef75c] border border-acid-lime hover:border-[#eef75c]",
      secondary:
        "bg-transparent text-mist border border-graphite hover:border-smoke hover:text-paper",
      ghost:
        "bg-transparent text-fog border border-transparent hover:text-mist",
    };

    const sizes = {
      sm: "h-8 px-3 text-[13px]",
      md: "h-9 px-4 text-sm",
      lg: "h-11 px-6 text-sm",
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
