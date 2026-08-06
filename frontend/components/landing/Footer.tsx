import Link from "next/link";

type FooterLink = {
  label: string;
  href: string;
  external?: boolean;
};

const footerLinks: Record<string, FooterLink[]> = {
  Product: [
    { label: "Features", href: "#features" },
    { label: "Pricing", href: "/pricing" },
    { label: "Dashboard", href: "/dashboard" },
  ],
  Resources: [
    { label: "Documentation", href: "/docs" },
    { label: "Guides", href: "/guides" },
    { label: "API Reference", href: "/docs/api" },
  ],
  Community: [
    { label: "GitHub", href: "https://github.com/seekersstudio", external: true },
    { label: "Discord", href: "https://discord.gg/seekersstudio", external: true },
    { label: "Twitter", href: "https://twitter.com/seekersstudio", external: true },
  ],
};

export default function Footer() {
  return (
    <footer className="px-4 py-12 hairline-top">
      <div className="max-w-5xl mx-auto">
        <div className="grid grid-cols-2 md:grid-cols-4 gap-8 mb-12">
          {/* Brand */}
          <div className="col-span-2 md:col-span-1">
            <Link href="/" className="flex items-center gap-2 mb-4">
              <div className="w-7 h-7 rounded-lg bg-solana-purple flex items-center justify-center">
                <span className="text-white font-bold text-xs">S</span>
              </div>
              <span className="font-semibold text-sm">SeekerStudio</span>
            </Link>
            <p className="text-xs text-on-surface-variant">
              Build Solana Mobile apps with AI
            </p>
          </div>

          {/* Link columns */}
          {Object.entries(footerLinks).map(([category, links]) => (
            <div key={category}>
              <h4 className="text-xs font-mono uppercase tracking-wider text-on-surface-variant mb-3">
                {category}
              </h4>
              <ul className="space-y-2">
                {links.map((link) => (
                  <li key={link.label}>
                    <Link
                      href={link.href}
                      className="text-sm text-on-surface-variant hover:text-on-surface transition-colors"
                      {...(link.external ? { target: "_blank", rel: "noopener noreferrer" } : {})}
                    >
                      {link.label}
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>

        {/* Bottom bar */}
        <div className="pt-6 hairline-top flex flex-col sm:flex-row items-center justify-between gap-4">
          <p className="text-xs text-on-surface-variant">
            © 2026 SeekerStudio. All rights reserved.
          </p>
          <div className="flex gap-4">
            <Link href="/terms" className="text-xs text-on-surface-variant hover:text-on-surface">
              Terms
            </Link>
            <Link href="/privacy" className="text-xs text-on-surface-variant hover:text-on-surface">
              Privacy
            </Link>
          </div>
        </div>
      </div>
    </footer>
  );
}
