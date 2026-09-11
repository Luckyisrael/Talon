import type { Metadata, Viewport } from "next";
import { Inter, JetBrains_Mono } from "next/font/google";
import "./globals.css";

// Inter Variable carries all UI and headings (weights 300–590, tight tracking
// at display sizes, cv01/ss03/zero features). Berkeley Mono is not licensed
// for web embedding, so JetBrains Mono stands in per the system's own
// substitute list — reserved strictly for issue IDs, shortcuts, and metadata.
const inter = Inter({
  variable: "--font-inter-variable",
  subsets: ["latin"],
  weight: ["300", "400", "500", "600"],
});

const mono = JetBrains_Mono({
  variable: "--font-berkeley-mono",
  subsets: ["latin"],
  weight: ["400"],
});

export const metadata: Metadata = {
  title: {
    default: "Talon",
    template: "%s · Talon",
  },
  description:
    "Describe a Solana Mobile app, watch it get written, and run it on a live Seeker device without leaving the page.",
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  themeColor: "#08090a",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html
      lang="en"
      className={`${inter.variable} ${mono.variable} h-full antialiased`}
    >
      <body className="min-h-full flex flex-col">{children}</body>
    </html>
  );
}
