import Link from "next/link";
import { socials } from "./links";

// The marketing nav, shared by every public page so the chrome never drifts.
// A server component: it renders the same markup for everyone, so there is no
// reason to ship it to the client.
export default function MarketingNav() {
  return (
    <nav className="h-16 flex items-center justify-between px-6 md:px-12 max-w-[1500px] mx-auto w-full border-b border-rail">
      <Link href="/" className="flex items-center gap-2.5">
        <span className="inline-block w-2.5 h-2.5 rounded-[1px] bg-paper" />
        <span className="text-lead font-semibold text-paper">SeekerStudio</span>
      </Link>

      <div className="hidden md:flex items-center gap-8">
        <Link href="/docs" className="text-small text-graphite hover:text-paper transition-colors">
          Docs
        </Link>
        <Link href="/pricing" className="text-small text-graphite hover:text-paper transition-colors">
          Pricing
        </Link>
        <Link href="/dashboard" className="text-small text-graphite hover:text-paper transition-colors">
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
              className="text-dust hover:text-paper transition-colors"
              target="_blank"
              rel="noopener noreferrer"
            >
              <svg className="w-[18px] h-[18px]" fill="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                <path d={s.path} />
              </svg>
            </Link>
          ))}
        </div>

        <Link
          href="/login"
          className="h-9 px-4 rounded-1 bg-paper text-ink text-small font-medium inline-flex items-center hover:bg-white transition-colors"
        >
          Sign in
        </Link>
      </div>
    </nav>
  );
}
