import Link from "next/link";

export default function Home() {
  return (
    <div className="min-h-screen flex flex-col bg-background">
      {/* Navbar */}
      <nav className="h-20 flex items-center justify-between px-6 md:px-12 max-w-[1500px] mx-auto w-full">
        {/* Left: Logo */}
        <Link href="/" className="flex items-center gap-3">
          <div className="w-3 h-3 rounded-full bg-text-primary" />
          <span className="text-xl font-bold text-text-primary">SeekerStudio</span>
        </Link>

        {/* Center: Nav links */}
        <div className="hidden md:flex items-center gap-8">
          <Link href="/docs" className="text-base text-text-secondary hover:text-text-primary transition-colors">
            Resources
          </Link>
          <Link href="/pricing" className="text-base text-text-secondary hover:text-text-primary transition-colors">
            Pricing
          </Link>
          <Link href="/dashboard" className="text-base text-accent font-medium">
            App
          </Link>
        </div>

        {/* Right: Social + Sign in */}
        <div className="flex items-center gap-5">
          {/* Social icons */}
          <Link href="https://twitter.com/seekersstudio" className="text-text-secondary hover:text-text-primary transition-colors" target="_blank" rel="noopener noreferrer">
            <svg className="w-5 h-5" fill="currentColor" viewBox="0 0 24 24">
              <path d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117z"/>
            </svg>
          </Link>
          <Link href="https://youtube.com/@seekersstudio" className="text-text-secondary hover:text-text-primary transition-colors" target="_blank" rel="noopener noreferrer">
            <svg className="w-5 h-5" fill="currentColor" viewBox="0 0 24 24">
              <path d="M23.498 6.186a3.016 3.016 0 0 0-2.122-2.136C19.505 3.545 12 3.545 12 3.545s-7.505 0-9.377.505A3.017 3.017 0 0 0 .502 6.186C0 8.07 0 12 0 12s0 3.93.502 5.814a3.016 3.016 0 0 0 2.122 2.136c1.871.505 9.376.505 9.376.505s7.505 0 9.377-.505a3.015 3.015 0 0 0 2.122-2.136C24 15.93 24 12 24 12s0-3.93-.502-5.814zM9.545 15.568V8.432L15.818 12l-6.273 3.568z"/>
            </svg>
          </Link>
          <Link href="https://discord.gg/seekersstudio" className="text-text-secondary hover:text-text-primary transition-colors" target="_blank" rel="noopener noreferrer">
            <svg className="w-5 h-5" fill="currentColor" viewBox="0 0 24 24">
              <path d="M20.317 4.3698a19.7913 19.7913 0 00-4.8851-1.5152.0741.0741 0 00-.0785.0371c-.211.3753-.4447.8648-.6083 1.2495-1.8447-.2762-3.68-.2762-5.4868 0-.1636-.3933-.4058-.8742-.6177-1.2495a.077.077 0 00-.0785-.037 19.7363 19.7363 0 00-4.8852 1.515.0699.0699 0 00-.0321.0277C.5334 9.0458-.319 13.5799.0992 18.0578a.0824.0824 0 00.0312.0561c2.0528 1.5076 4.0413 2.4228 5.9929 3.0294a.0777.0777 0 00.0842-.0276c.4616-.6304.8731-1.2952 1.226-1.9942a.076.076 0 00-.0416-.1057c-.6528-.2476-1.2743-.5495-1.8722-.8923a.077.077 0 01-.0076-.1277c.1258-.0943.2517-.1923.3718-.2914a.0743.0743 0 01.0776-.0105c3.9278 1.7933 8.18 1.7933 12.0614 0a.0739.0739 0 01.0785.0095c.1202.099.246.1981.3728.2924a.077.077 0 01-.0066.1276 12.2986 12.2986 0 01-1.873.8914.0766.0766 0 00-.0407.1067c.3604.698.7719 1.3628 1.225 1.9932a.076.076 0 00.0842.0286c1.961-.6067 3.9495-1.5219 6.0023-3.0294a.077.077 0 00.0313-.0552c.5004-5.177-.8382-9.6739-3.5485-13.6604a.061.061 0 00-.0312-.0286zM8.02 15.3312c-1.1825 0-2.1569-1.0857-2.1569-2.419 0-1.3332.9555-2.4189 2.157-2.4189 1.2108 0 2.1757 1.0952 2.1568 2.419 0 1.3332-.9555 2.4189-2.1569 2.4189zm7.9748 0c-1.1825 0-2.1569-1.0857-2.1569-2.419 0-1.3332.9554-2.4189 2.1569-2.4189 1.2108 0 2.1757 1.0952 2.1568 2.419 0 1.3332-.946 2.4189-2.1568 2.4189z"/>
            </svg>
          </Link>
          <Link href="https://github.com/seekersstudio" className="text-text-secondary hover:text-text-primary transition-colors" target="_blank" rel="noopener noreferrer">
            <svg className="w-5 h-5" fill="currentColor" viewBox="0 0 24 24">
              <path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/>
            </svg>
          </Link>

          {/* Sign in */}
          <Link href="/login">
            <button className="h-10 px-5 rounded-full bg-text-primary text-background text-sm font-semibold hover:bg-text-secondary transition-colors">
              Sign in
            </button>
          </Link>
        </div>
      </nav>

      {/* Hero Section */}
      <main className="flex-1 flex flex-col items-center justify-center px-6 py-12">
        {/* Headline */}
        <h1 className="text-5xl md:text-7xl font-bold text-text-primary text-center mb-6 tracking-tight">
          Build Solana Mobile Apps
        </h1>

        {/* Subheadline */}
        <p className="text-xl text-text-secondary text-center mb-12 max-w-2xl">
          with AI
        </p>

        {/* Prompt Input Box */}
        <div className="w-full max-w-[950px] bg-surface-3 rounded-[20px] border border-surface-5 p-6 mb-8">
          {/* Top row */}
          <div className="flex items-center justify-between mb-6">
            <span className="text-lg text-text-muted">Describe the app you want to build...</span>
            <div className="w-8 h-8 rounded-full bg-surface-5 flex items-center justify-center">
              <svg className="w-4 h-4 text-text-secondary" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
              </svg>
            </div>
          </div>

          {/* Bottom row - toolbar */}
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-3">
              <button className="w-9 h-9 rounded-full bg-surface-5 flex items-center justify-center hover:bg-surface-4 transition-colors">
                <svg className="w-4 h-4 text-text-secondary" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
                </svg>
              </button>
              <button className="h-9 px-4 rounded-full bg-surface-5 flex items-center gap-2 text-sm text-text-secondary hover:bg-surface-4 transition-colors">
                <span>📱</span>
                <span>For Seeker</span>
                <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
                </svg>
              </button>
              <button className="h-9 px-4 rounded-full bg-surface-5 flex items-center gap-2 text-sm text-text-secondary hover:bg-surface-4 transition-colors">
                <span>⚡</span>
                <span>Auto</span>
                <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
                </svg>
              </button>
            </div>
            <button className="w-9 h-9 rounded-full bg-surface-5 flex items-center justify-center hover:bg-surface-4 transition-colors">
              <svg className="w-4 h-4 text-text-secondary" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 11a7 7 0 01-7 7m0 0a7 7 0 01-7-7m7 7v4m0 0H8m4 0h4m-4-8a3 3 0 01-3-3V5a3 3 0 116 0v6a3 3 0 01-3 3z" />
              </svg>
            </button>
          </div>
        </div>

        {/* Suggestion Chips */}
        <div className="flex flex-wrap justify-center gap-3">
          <button className="h-11 px-5 rounded-full bg-surface-3 border border-surface-5 flex items-center gap-3 text-sm font-medium text-text-primary hover:bg-surface-4 transition-colors">
            <span className="w-6 h-6 rounded-full bg-accent flex items-center justify-center text-xs">🎮</span>
            Create a Multiplayer Game
          </button>
          <button className="h-11 px-5 rounded-full bg-surface-3 border border-surface-5 flex items-center gap-3 text-sm font-medium text-text-primary hover:bg-surface-4 transition-colors">
            <span className="w-6 h-6 rounded-full bg-accent flex items-center justify-center text-xs">📦</span>
            Create a 3D Game
          </button>
          <button className="h-11 px-5 rounded-full bg-surface-3 border border-surface-5 flex items-center gap-3 text-sm font-medium text-text-primary hover:bg-surface-4 transition-colors">
            <span className="w-6 h-6 rounded-full bg-solana-purple flex items-center justify-center text-xs text-white">S</span>
            Solana Wallet App
          </button>
          <button className="h-11 px-5 rounded-full bg-surface-3 border border-surface-5 flex items-center gap-3 text-sm font-medium text-text-primary hover:bg-surface-4 transition-colors">
            <span className="w-6 h-6 rounded-full bg-surface-5 flex items-center justify-center text-xs">⚡</span>
            GitHub to Mobile App
          </button>
        </div>
      </main>
    </div>
  );
}
