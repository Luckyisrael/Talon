import Link from "next/link";
import { socials } from "./links";

// The marketing nav, shared by every public page so the chrome never drifts.
// A server component: it renders the same markup for everyone, so there is no
// reason to ship it to the client.
export default function MarketingNav() {
  return (
    <nav className="h-16 flex items-center justify-between px-4 md:px-6 max-w-[1200px] mx-auto w-full">
      <Link href="/" className="flex items-center gap-2">
        <svg width="18" height="18" viewBox="0 0 18 18" fill="none" aria-hidden>
          <rect x="1" y="1" width="16" height="16" rx="4" stroke="#ffffff" strokeWidth="1.5" />
          <circle cx="9" cy="9" r="2.5" fill="#e4f222" />
        </svg>
        <span className="text-base font-medium tracking-tight text-paper">SeekerStudio</span>
      </Link>

      <div className="hidden md:flex items-center gap-2">
        <Link href="/docs" className="text-[13px] text-mist hover:text-paper transition-colors px-3 py-2">
          Docs
        </Link>
        <Link href="/pricing" className="text-[13px] text-mist hover:text-paper transition-colors px-3 py-2">
          Pricing
        </Link>
        <Link href="/dashboard" className="text-[13px] text-mist hover:text-paper transition-colors px-3 py-2">
          App
        </Link>
      </div>

      <div className="flex items-center gap-5">
        <div className="hidden sm:flex items-center gap-4">
          {socials.map((s) => (
            <Link
              key={s.label}
              href={s.href}
              aria-label={s.label}
              className="text-ash hover:text-mist transition-colors"
              target="_blank"
              rel="noopener noreferrer"
            >
              <svg className="w-[18px] h-[18px]" fill="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                <path d={s.path} />
              </svg>
            </Link>
          ))}
        </div>

        <Link href="/login" className="btn-pill">
          Sign in
        </Link>
      </div>
    </nav>
  );
}
