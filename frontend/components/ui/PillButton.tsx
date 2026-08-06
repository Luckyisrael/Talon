import { ButtonHTMLAttributes, forwardRef } from "react";

interface PillButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "primary" | "secondary" | "ghost";
  size?: "sm" | "md" | "lg";
}

const PillButton = forwardRef<HTMLButtonElement, PillButtonProps>(
  ({ className = "", variant = "primary", size = "md", children, ...props }, ref) => {
    const baseStyles = "rounded-full font-medium transition-all duration-150 ease-out inline-flex items-center justify-center gap-2";

    const variants = {
      primary: "bg-text-primary text-background hover:bg-text-secondary active:scale-[0.98]",
      secondary: "bg-surface-3 text-text-primary hover:bg-surface-4 active:scale-[0.98]",
      ghost: "bg-transparent text-text-primary hover:bg-surface-3 active:scale-[0.98]",
    };

    const sizes = {
      sm: "h-8 px-3 text-xs",
      md: "h-10 px-4 text-sm",
      lg: "h-12 px-6 text-base",
    };

    return (
      <button
        ref={ref}
        className={`${baseStyles} ${variants[variant]} ${sizes[size]} ${className}`}
        {...props}
      >
        {children}
      </button>
    );
  }
);

PillButton.displayName = "PillButton";

export default PillButton;
