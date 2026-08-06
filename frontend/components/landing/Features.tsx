import { TonalCard } from "@/components/ui";

const features = [
  {
    icon: "💬",
    title: "Describe",
    description: "Tell AI what you want to build. Natural language prompts, no code required.",
  },
  {
    icon: "⚡",
    title: "Generate",
    description: "AI writes React Native code with Solana Mobile SDK integration.",
  },
  {
    icon: "📱",
    title: "Preview",
    description: "Live preview on a virtual Seeker device. See your app in real-time.",
  },
];

export default function Features() {
  return (
    <section className="px-4 py-20">
      <div className="max-w-5xl mx-auto">
        {/* Section header */}
        <div className="text-center mb-12">
          <h2 className="text-2xl md:text-3xl font-bold text-on-surface mb-4">
            How it works
          </h2>
          <p className="text-on-surface-variant max-w-md mx-auto">
            Three steps from idea to working app
          </p>
        </div>

        {/* Bento Grid */}
        <div className="bento-grid">
          {features.map((feature, i) => (
            <TonalCard
              key={feature.title}
              variant="elevated"
              padding="lg"
              className={`
                ${i === 0 ? "md:col-span-4" : ""}
                ${i === 1 ? "md:col-span-4" : ""}
                ${i === 2 ? "md:col-span-4" : ""}
              `}
            >
              <div className="text-3xl mb-4">{feature.icon}</div>
              <h3 className="text-lg font-semibold text-on-surface mb-2">
                {feature.title}
              </h3>
              <p className="text-sm text-on-surface-variant">
                {feature.description}
              </p>
            </TonalCard>
          ))}
        </div>
      </div>
    </section>
  );
}
