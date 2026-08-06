import { HTMLAttributes, forwardRef } from "react";

interface SpinnerProps extends HTMLAttributes<HTMLDivElement> {
  size?: "sm" | "md" | "lg";
  color?: "primary" | "currentColor";
}

const Spinner = forwardRef<HTMLDivElement, SpinnerProps>(
  ({ className = "", size = "md", color = "primary", ...props }, ref) => {
    const sizes = {
      sm: "h-4 w-4 border-2",
      md: "h-6 w-6 border-2",
      lg: "h-8 w-8 border-[3px]",
    };

    const colors = {
      primary: "border-primary border-t-transparent",
      currentColor: "border-current border-t-transparent",
    };

    return (
      <div
        ref={ref}
        className={`animate-spin rounded-full ${sizes[size]} ${colors[color]} ${className}`}
        role="status"
        aria-label="Loading"
        {...props}
      >
        <span className="sr-only">Loading...</span>
      </div>
    );
  }
);

Spinner.displayName = "Spinner";

export default Spinner;
