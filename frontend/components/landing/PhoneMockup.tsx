export default function PhoneMockup() {
  return (
    <section className="px-4 py-20 overflow-hidden">
      <div className="max-w-5xl mx-auto">
        <div className="relative mx-auto w-full max-w-md">
          {/* Phone frame */}
          <div className="relative bg-surface-container-highest rounded-[40px] p-3 shadow-lg">
            {/* Screen */}
            <div className="relative bg-surface rounded-[32px] overflow-hidden aspect-[9/19.5]">
              {/* Status bar */}
              <div className="h-12 flex items-center justify-between px-6 pt-2">
                <span className="text-[10px] font-mono text-on-surface-variant">9:41</span>
                <div className="flex gap-1">
                  <div className="w-3 h-2 bg-on-surface-variant rounded-sm" />
                  <div className="w-3 h-2 bg-on-surface-variant rounded-sm" />
                  <div className="w-4 h-2 bg-on-surface-variant rounded-sm" />
                </div>
              </div>

              {/* Content placeholder */}
              <div className="flex flex-col items-center justify-center h-full px-6 pb-20">
                <div className="w-16 h-16 rounded-2xl solana-gradient flex items-center justify-center mb-6">
                  <span className="text-white text-2xl font-bold">S</span>
                </div>
                <div className="w-32 h-3 bg-on-surface/20 rounded-full mb-3" />
                <div className="w-24 h-2 bg-on-surface/10 rounded-full mb-8" />
                <div className="w-full h-10 rounded-full bg-surface-container-high" />
              </div>

              {/* Bottom nav */}
              <div className="absolute bottom-0 left-0 right-0 h-16 flex items-center justify-around px-6 bg-surface-container-low">
                <div className="w-6 h-6 rounded-full bg-primary/20" />
                <div className="w-6 h-6 rounded-full bg-on-surface/10" />
                <div className="w-6 h-6 rounded-full bg-on-surface/10" />
              </div>
            </div>
          </div>

          {/* Glow effect */}
          <div className="absolute -inset-10 bg-gradient-to-r from-solana-purple/20 to-solana-green/20 blur-3xl -z-10" />
        </div>
      </div>
    </section>
  );
}
