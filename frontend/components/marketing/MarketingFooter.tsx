import Link from "next/link";
import { socials } from "./links";

// The marketing footer, shared by every public page. Every internal link here
// resolves to a real route — the deep "Resources" entries point at sections
// within /docs rather than pages that don't exist.
export default function MarketingFooter() {
  return (
    <footer className="py-12 px-4 md:px-6 border-t border-graphite">
      <div className="max-w-[1200px] mx-auto">
        <div className="grid grid-cols-2 md:grid-cols-4 gap-8 mb-12">
          <div>
            <div className="flex items-center gap-2 mb-4">
              <svg width="16" height="16" viewBox="0 0 18 18" fill="none" aria-hidden>
                <rect x="1" y="1" width="16" height="16" rx="4" stroke="#ffffff" strokeWidth="1.5" />
                <circle cx="9" cy="9" r="2.5" fill="#e4f222" />
              </svg>
              <span className="text-sm font-medium text-paper">SeekerStudio</span>
            </div>
            <p className="text-sm text-fog leading-relaxed">
              Build Solana Mobile apps with AI. From idea to device in minutes.
            </p>
          </div>

          <div>
            <h4 className="gauge mb-4">Product</h4>
            <ul className="space-y-2">
              <li><Link href="/pricing" className="text-sm text-fog hover:text-paper transition-colors">Pricing</Link></li>
              <li><Link href="/dashboard" className="text-sm text-fog hover:text-paper transition-colors">App</Link></li>
              <li><Link href="/docs" className="text-sm text-fog hover:text-paper transition-colors">Documentation</Link></li>
            </ul>
          </div>

          <div>
            <h4 className="gauge mb-4">Resources</h4>
            <ul className="space-y-2">
              <li><Link href="/docs#getting-started" className="text-sm text-fog hover:text-paper transition-colors">Getting started</Link></li>
              <li><Link href="/docs#solana-mobile" className="text-sm text-fog hover:text-paper transition-colors">Solana Mobile guide</Link></li>
              <li><Link href="/docs#quotas" className="text-sm text-fog hover:text-paper transition-colors">Quotas &amp; limits</Link></li>
            </ul>
          </div>

          <div>
            <h4 className="gauge mb-4">Community</h4>
            <ul className="space-y-2">
              {socials.map((s) => (
                <li key={s.label}>
                  <Link
                    href={s.href}
                    className="text-sm text-fog hover:text-paper transition-colors"
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    {s.label}
                  </Link>
                </li>
              ))}
            </ul>
          </div>
        </div>

        <div className="pt-8 border-t border-graphite flex flex-col md:flex-row items-center justify-between gap-4">
          <p className="font-mono text-xs text-ash">© 2026 SeekerStudio. All rights reserved.</p>
          <div className="flex items-center gap-6">
            <Link href="/privacy" className="font-mono text-xs text-ash hover:text-fog transition-colors">Privacy</Link>
            <Link href="/terms" className="font-mono text-xs text-ash hover:text-fog transition-colors">Terms</Link>
          </div>
        </div>
      </div>
    </footer>
  );
}
