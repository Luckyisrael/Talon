import type { Metadata } from "next";
import Link from "next/link";
import { ArrowRight } from "iconsax-reactjs";

export const metadata: Metadata = {
  title: "Not found",
};

// The catch-all for any unmatched URL. It renders inside the root layout, so it
// inherits the bench type and palette. Kept lean on purpose: a 404 is a dead
// end, not a destination, so it states the fact plainly and offers the two
// routes that actually go somewhere — home, and the app.
export default function NotFound() {
  return (
    <main className="flex-1 flex flex-col items-center justify-center px-6 py-24 text-center bg-void">
      {/* The machine reporting a fact, so it speaks in mono. */}
      <span className="t-label">404 · ROUTE NOT FOUND</span>

      <h1 className="t-heading-sm mt-4 mb-4 text-balance">
        We can&apos;t find that page
      </h1>
      <p className="t-body-sm text-fog max-w-md mb-8">
        The link may be broken, or the page may have moved. Nothing&apos;s wrong with your
        build — this URL just doesn&apos;t point anywhere.
      </p>

      <div className="flex flex-wrap items-center justify-center gap-3">
        <Link
          href="/"
          className="btn-go h-10 px-5 rounded-md text-sm inline-flex items-center gap-2"
        >
          Go home
          <ArrowRight size="14" variant="Linear" />
        </Link>
        <Link
          href="/dashboard"
          className="btn h-10 px-5 rounded-md text-sm inline-flex items-center"
        >
          Open the app
        </Link>
      </div>
    </main>
  );
}
