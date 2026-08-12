import type { Metadata, Viewport } from "next";
import { IBM_Plex_Mono, IBM_Plex_Sans } from "next/font/google";
import "./globals.css";

// Two roles, one family. Sans is anything a person says or presses; mono is
// anything the machine reports — paths, traces, telemetry, code. Plex was drawn
// for exactly that pairing, so the two stay related at a glance without the
// interface looking like two different products stitched together.
const plexSans = IBM_Plex_Sans({
  variable: "--font-plex-sans",
  subsets: ["latin"],
});

// Plex Mono has no variable axis on Google Fonts, so the weights the interface
// actually uses are listed explicitly rather than silently falling back to 400.
const plexMono = IBM_Plex_Mono({
  variable: "--font-plex-mono",
  subsets: ["latin"],
  weight: ["400", "500", "600"],
});

export const metadata: Metadata = {
  title: "SeekerStudio",
  description:
    "Describe a Solana Mobile app, watch it get written, and run it on a live Seeker device without leaving the page.",
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  themeColor: "#0b0e13",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html
      lang="en"
      className={`${plexSans.variable} ${plexMono.variable} h-full antialiased`}
    >
      <body className="min-h-full flex flex-col">{children}</body>
    </html>
  );
}
