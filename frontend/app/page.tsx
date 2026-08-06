import Link from "next/link";
import { Add, ArrowDown, Microphone, Monitor, Message, Mobile, DocumentUpload, ShieldSecurity, Flash, Code, Box, Game, Send, Gameboy } from "iconsax-reactjs";

export default function Home() {
  return (
    <div className="min-h-screen flex flex-col bg-background">
      {/* Navbar */}
      <nav className="h-20 flex items-center justify-between px-6 md:px-12 max-w-[1500px] mx-auto w-full">
        <Link href="/" className="flex items-center gap-3">
          <div className="w-3 h-3 rounded-full bg-text-primary" />
          <span className="text-xl font-bold text-text-primary">SeekerStudio</span>
        </Link>

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

        <div className="flex items-center gap-5">
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

          <Link href="/login">
            <button className="h-10 px-5 rounded-full bg-text-primary text-background text-sm font-semibold hover:bg-text-secondary transition-colors">
              Sign in
            </button>
          </Link>
        </div>
      </nav>

      {/* Hero Section */}
      <main className="flex-1 flex flex-col items-center px-6 pt-20 pb-16">
        <h1 className="text-5xl md:text-7xl font-bold text-text-primary text-center mb-4 tracking-tight">
          Build Solana Mobile Apps
        </h1>

        <p className="text-lg md:text-xl text-text-secondary text-center mb-10 max-w-2xl">
          Describe your app, preview and test it live, then deploy to Solana Seeker all without leaving your browser.
        </p>

        {/* Prompt Input Box */}
        <div className="w-full max-w-[950px] bg-surface-3 rounded-[24px] p-6 mb-8">
          <div className="flex items-center justify-between mb-6">
            <span className="text-lg text-text-muted">Describe the app you want to build...</span>
            <div className="w-8 h-8 rounded-full bg-surface-5 flex items-center justify-center">
              <Monitor size="18" className="text-text-secondary" variant="Linear" />
            </div>
          </div>

          <div className="flex items-center justify-between">
            <div className="flex items-center gap-3">
              <button className="w-9 h-9 rounded-full bg-surface-5 flex items-center justify-center hover:bg-surface-4 transition-colors">
                <Add size="16" className="text-text-secondary" variant="Linear" />
              </button>
              <button className="h-9 px-4 rounded-full bg-surface-5 flex items-center gap-2 text-sm text-text-secondary hover:bg-surface-4 transition-colors">
                <Mobile size="14" variant="Linear" />
                <span>For Seeker</span>
                <ArrowDown size="12" variant="Linear" />
              </button>
              <button className="h-9 px-4 rounded-full bg-surface-5 flex items-center gap-2 text-sm text-text-secondary hover:bg-surface-4 transition-colors">
                <Flash size="14" variant="Linear" />
                <span>Auto</span>
                <ArrowDown size="12" variant="Linear" />
              </button>
            </div>
            <button className="w-9 h-9 rounded-full bg-surface-5 flex items-center justify-center hover:bg-surface-4 transition-colors">
              <Microphone size="16" className="text-text-secondary" variant="Linear" />
            </button>
          </div>
        </div>

        {/* Suggestion Chips */}
        <div className="flex flex-wrap justify-center gap-3 mb-20">
          <button className="h-11 px-5 rounded-full bg-surface-3 flex items-center gap-3 text-sm font-medium text-text-primary hover:bg-surface-4 transition-colors">
            <span className="w-6 h-6 rounded-full bg-accent flex items-center justify-center text-xs">🎮</span>
            Create a Multiplayer Game
          </button>
          <button className="h-11 px-5 rounded-full bg-surface-3 flex items-center gap-3 text-sm font-medium text-text-primary hover:bg-surface-4 transition-colors">
            <span className="w-6 h-6 rounded-full bg-accent flex items-center justify-center text-xs">📦</span>
            Create a 3D Game
          </button>
          <button className="h-11 px-5 rounded-full bg-surface-3 flex items-center gap-3 text-sm font-medium text-text-primary hover:bg-surface-4 transition-colors">
            <span className="w-6 h-6 rounded-full bg-solana-purple flex items-center justify-center text-xs text-white">S</span>
            Solana Wallet App
          </button>
          <button className="h-11 px-5 rounded-full bg-surface-3 flex items-center gap-3 text-sm font-medium text-text-primary hover:bg-surface-4 transition-colors">
            <span className="w-6 h-6 rounded-full bg-surface-5 flex items-center justify-center text-xs">⚡</span>
            GitHub to Mobile App
          </button>
        </div>

        {/* Features Section */}
        <div className="w-full max-w-[1200px] mx-auto">
          <div className="text-center mb-16">
            <h2 className="text-3xl md:text-4xl font-bold text-text-primary mb-4">
              Everything you need to ship
            </h2>
            <p className="text-lg text-text-secondary max-w-xl mx-auto">
              From idea to installed app on a real Android device — no local setup required.
            </p>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
            <div className="bg-surface-3 rounded-[20px] p-6">
              <div className="w-10 h-10 rounded-full bg-accent/10 flex items-center justify-center mb-4">
                <Message size="20" className="text-accent" variant="Linear" />
              </div>
              <h3 className="text-base font-semibold text-text-primary mb-2">AI-Powered Generation</h3>
              <p className="text-sm text-text-secondary leading-relaxed">
                Describe your app in plain English. Our AI generates production-ready React Native code with Solana Mobile SDK integration.
              </p>
            </div>

            <div className="bg-surface-3 rounded-[20px] p-6">
              <div className="w-10 h-10 rounded-full bg-solana-purple/10 flex items-center justify-center mb-4">
                <Mobile size="20" className="text-solana-purple" variant="Linear" />
              </div>
              <h3 className="text-base font-semibold text-text-primary mb-2">Live Device Preview</h3>
              <p className="text-sm text-text-secondary leading-relaxed">
                See your app running on a real Android emulator in real-time. Every change streams instantly to the virtual Seeker device.
              </p>
            </div>

            <div className="bg-surface-3 rounded-[20px] p-6">
              <div className="w-10 h-10 rounded-full bg-success/10 flex items-center justify-center mb-4">
                <DocumentUpload size="20" className="text-success" variant="Linear" />
              </div>
              <h3 className="text-base font-semibold text-text-primary mb-2">One-Click Deploy</h3>
              <p className="text-sm text-text-secondary leading-relaxed">
                Build and install your APK directly to the emulator. Test wallet connections, MWA protocol, and native features instantly.
              </p>
            </div>

            <div className="bg-surface-3 rounded-[20px] p-6">
              <div className="w-10 h-10 rounded-full bg-warning/10 flex items-center justify-center mb-4">
                <ShieldSecurity size="20" className="text-warning" variant="Linear" />
              </div>
              <h3 className="text-base font-semibold text-text-primary mb-2">Solana Native</h3>
              <p className="text-sm text-text-secondary leading-relaxed">
                Built-in MWA wallet integration, Keypair management, and Solana RPC. Everything your Seeker app needs, pre-configured.
              </p>
            </div>

            <div className="bg-surface-3 rounded-[20px] p-6">
              <div className="w-10 h-10 rounded-full bg-error/10 flex items-center justify-center mb-4">
                <Flash size="20" className="text-error" variant="Linear" />
              </div>
              <h3 className="text-base font-semibold text-text-primary mb-2">Instant Iteration</h3>
              <p className="text-sm text-text-secondary leading-relaxed">
                Chat with AI to refine your app. Fix bugs, add features, change designs — all through natural conversation.
              </p>
            </div>

            <div className="bg-surface-3 rounded-[20px] p-6">
              <div className="w-10 h-10 rounded-full bg-accent/10 flex items-center justify-center mb-4">
                <Code size="20" className="text-accent" variant="Linear" />
              </div>
              <h3 className="text-base font-semibold text-text-primary mb-2">Full IDE Experience</h3>
              <p className="text-sm text-text-secondary leading-relaxed">
                File tree, terminal, build output — all in your browser. No VS Code, no Android Studio, no local toolchain needed.
              </p>
            </div>
          </div>
        </div>

        {/* CTA Section */}
        <div className="w-full max-w-[800px] mx-auto mt-24 text-center">
          <h2 className="text-3xl md:text-4xl font-bold text-text-primary mb-4">
            Start building today
          </h2>
          <p className="text-lg text-text-secondary mb-8 max-w-lg mx-auto">
            Free tier includes 20 AI prompts and 60 minutes of device time. No credit card required.
          </p>
          <Link href="/register">
            <button className="h-12 px-8 rounded-full bg-text-primary text-background text-base font-semibold hover:bg-text-secondary transition-colors">
              Get Started Free
            </button>
          </Link>
        </div>
      </main>

      {/* Footer */}
      <footer className="py-12 px-6 md:px-12 bg-surface-1">
        <div className="max-w-[1200px] mx-auto">
          <div className="grid grid-cols-2 md:grid-cols-4 gap-8 mb-12">
            <div>
              <div className="flex items-center gap-3 mb-4">
                <div className="w-3 h-3 rounded-full bg-text-primary" />
                <span className="text-sm font-bold text-text-primary">SeekerStudio</span>
              </div>
              <p className="text-sm text-text-secondary leading-relaxed">
                Build Solana Mobile apps with AI. From idea to device in minutes.
              </p>
            </div>

            <div>
              <h4 className="text-sm font-semibold text-text-primary mb-4">Product</h4>
              <ul className="space-y-2">
                <li><Link href="/pricing" className="text-sm text-text-secondary hover:text-text-primary transition-colors">Pricing</Link></li>
                <li><Link href="/dashboard" className="text-sm text-text-secondary hover:text-text-primary transition-colors">App</Link></li>
                <li><Link href="/docs" className="text-sm text-text-secondary hover:text-text-primary transition-colors">Documentation</Link></li>
              </ul>
            </div>

            <div>
              <h4 className="text-sm font-semibold text-text-primary mb-4">Resources</h4>
              <ul className="space-y-2">
                <li><Link href="/docs/getting-started" className="text-sm text-text-secondary hover:text-text-primary transition-colors">Getting Started</Link></li>
                <li><Link href="/docs/solana-mobile" className="text-sm text-text-secondary hover:text-text-primary transition-colors">Solana Mobile Guide</Link></li>
                <li><Link href="/docs/api" className="text-sm text-text-secondary hover:text-text-primary transition-colors">API Reference</Link></li>
              </ul>
            </div>

            <div>
              <h4 className="text-sm font-semibold text-text-primary mb-4">Community</h4>
              <ul className="space-y-2">
                <li><Link href="https://discord.gg/seekersstudio" className="text-sm text-text-secondary hover:text-text-primary transition-colors" target="_blank" rel="noopener noreferrer">Discord</Link></li>
                <li><Link href="https://twitter.com/seekersstudio" className="text-sm text-text-secondary hover:text-text-primary transition-colors" target="_blank" rel="noopener noreferrer">Twitter</Link></li>
                <li><Link href="https://github.com/seekersstudio" className="text-sm text-text-secondary hover:text-text-primary transition-colors" target="_blank" rel="noopener noreferrer">GitHub</Link></li>
              </ul>
            </div>
          </div>

          <div className="pt-8 flex flex-col md:flex-row items-center justify-between gap-4">
            <p className="text-xs text-text-muted">
              © 2026 SeekerStudio. All rights reserved.
            </p>
            <div className="flex items-center gap-6">
              <Link href="/privacy" className="text-xs text-text-muted hover:text-text-secondary transition-colors">Privacy</Link>
              <Link href="/terms" className="text-xs text-text-muted hover:text-text-secondary transition-colors">Terms</Link>
            </div>
          </div>
        </div>
      </footer>
    </div>
  );
}
