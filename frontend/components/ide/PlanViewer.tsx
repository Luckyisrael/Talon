"use client";

import { Check, ArrowRight, Box, Document, Star } from "iconsax-reactjs";

interface PlanData {
  screens: { name: string; description: string }[];
  features: string[];
  navigation: string;
  packages: string[];
}

interface PlanViewerProps {
  message: string;
  plan: PlanData;
  onApprove: () => void;
}

function renderMarkdown(text: string) {
  const lines = text.split("\n");
  const elements: React.ReactNode[] = [];

  let inList = false;
  let listItems: React.ReactNode[] = [];

  const flushList = () => {
    if (listItems.length > 0) {
      elements.push(
        <ul key={`list-${elements.length}`} className="space-y-1.5 my-3">
          {listItems}
        </ul>
      );
      listItems = [];
      inList = false;
    }
  };

  lines.forEach((line, i) => {
    const trimmed = line.trim();

    // Empty line
    if (!trimmed) {
      flushList();
      return;
    }

    // Bold text
    const formatInline = (s: string) => {
      const parts: React.ReactNode[] = [];
      const regex = /\*\*(.*?)\*\*/g;
      let lastIndex = 0;
      let match;
      while ((match = regex.exec(s)) !== null) {
        if (match.index > lastIndex) {
          parts.push(s.slice(lastIndex, match.index));
        }
        parts.push(
          <strong key={match.index} className="text-text-primary font-semibold">
            {match[1]}
          </strong>
        );
        lastIndex = regex.lastIndex;
      }
      if (lastIndex < s.length) {
        parts.push(s.slice(lastIndex));
      }
      return parts.length > 0 ? parts : s;
    };

    // Header
    if (trimmed.startsWith("### ")) {
      flushList();
      elements.push(
        <h3 key={i} className="text-sm font-semibold text-text-primary mt-4 mb-2">
          {formatInline(trimmed.slice(4))}
        </h3>
      );
      return;
    }
    if (trimmed.startsWith("## ")) {
      flushList();
      elements.push(
        <h2 key={i} className="text-base font-bold text-text-primary mt-5 mb-2">
          {formatInline(trimmed.slice(3))}
        </h2>
      );
      return;
    }

    // List item
    if (trimmed.startsWith("- ") || trimmed.startsWith("* ")) {
      inList = true;
      listItems.push(
        <li key={i} className="flex items-start gap-2 text-sm text-text-secondary">
          <span className="w-1.5 h-1.5 rounded-full bg-accent mt-1.5 flex-shrink-0" />
          <span>{formatInline(trimmed.slice(2))}</span>
        </li>
      );
      return;
    }

    // Numbered list
    const numberedMatch = trimmed.match(/^\d+\.\s+(.*)/);
    if (numberedMatch) {
      inList = true;
      listItems.push(
        <li key={i} className="flex items-start gap-2 text-sm text-text-secondary">
          <span className="text-accent font-mono text-xs mt-0.5 flex-shrink-0">
            {trimmed.match(/^(\d+)\./)?.[1]}.
          </span>
          <span>{formatInline(numberedMatch[1])}</span>
        </li>
      );
      return;
    }

    // Regular paragraph
    flushList();
    elements.push(
      <p key={i} className="text-sm text-text-secondary leading-relaxed my-1">
        {formatInline(trimmed)}
      </p>
    );
  });

  flushList();
  return elements;
}

export default function PlanViewer({ message, plan, onApprove }: PlanViewerProps) {
  return (
    <div className="h-full overflow-y-auto">
      <div className="max-w-2xl mx-auto px-8 py-8">
        {/* Header badge */}
        <div className="flex items-center gap-2 mb-6">
          <div className="h-6 px-3 rounded-full bg-accent/15 flex items-center">
            <span className="text-xs font-medium text-accent">Plan</span>
          </div>
        </div>

        {/* Message content */}
        <div className="mb-8">{renderMarkdown(message)}</div>

        {/* Plan sections */}
        <div className="space-y-5">
          {/* Screens */}
          {plan.screens.length > 0 && (
            <div className="bg-surface-2 rounded-xl p-5">
              <div className="flex items-center gap-2 mb-3">
                <Document size="14" className="text-accent" variant="Linear" />
                <p className="text-xs font-mono uppercase tracking-wider text-text-secondary">
                  Screens
                </p>
                <span className="ml-auto text-[10px] text-text-muted font-mono">
                  {plan.screens.length}
                </span>
              </div>
              <div className="space-y-3">
                {plan.screens.map((screen, i) => (
                  <div key={i} className="flex items-start gap-3 group">
                    <div className="w-7 h-7 rounded-lg bg-surface-3 flex items-center justify-center flex-shrink-0 mt-0.5">
                      <span className="text-[11px] font-mono text-accent">{i + 1}</span>
                    </div>
                    <div>
                      <p className="text-sm font-medium text-text-primary">{screen.name}</p>
                      <p className="text-xs text-text-secondary mt-0.5 leading-relaxed">
                        {screen.description}
                      </p>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Features */}
          {plan.features.length > 0 && (
            <div className="bg-surface-2 rounded-xl p-5">
              <div className="flex items-center gap-2 mb-3">
                <Star size="14" className="text-accent" variant="Linear" />
                <p className="text-xs font-mono uppercase tracking-wider text-text-secondary">
                  Features
                </p>
                <span className="ml-auto text-[10px] text-text-muted font-mono">
                  {plan.features.length}
                </span>
              </div>
              <div className="flex flex-wrap gap-2">
                {plan.features.map((feature, i) => (
                  <span
                    key={i}
                    className="h-7 px-3 rounded-full bg-surface-3 text-xs text-text-secondary flex items-center"
                  >
                    {feature}
                  </span>
                ))}
              </div>
            </div>
          )}

          {/* Navigation */}
          {plan.navigation && (
            <div className="bg-surface-2 rounded-xl p-5">
              <div className="flex items-center gap-2 mb-3">
                <ArrowRight size="14" className="text-accent" variant="Linear" />
                <p className="text-xs font-mono uppercase tracking-wider text-text-secondary">
                  Navigation
                </p>
              </div>
              <p className="text-sm text-text-secondary leading-relaxed">
                {plan.navigation}
              </p>
            </div>
          )}

          {/* Packages */}
          {plan.packages.length > 0 && (
            <div className="bg-surface-2 rounded-xl p-5">
              <div className="flex items-center gap-2 mb-3">
                <Box size="14" className="text-accent" variant="Linear" />
                <p className="text-xs font-mono uppercase tracking-wider text-text-secondary">
                  Packages
                </p>
              </div>
              <div className="flex flex-wrap gap-2">
                {plan.packages.map((pkg, i) => (
                  <code
                    key={i}
                    className="h-7 px-3 rounded-lg bg-surface-3 text-xs text-accent font-mono flex items-center"
                  >
                    {pkg}
                  </code>
                ))}
              </div>
            </div>
          )}
        </div>

        {/* Approve button */}
        <div className="mt-8 mb-12">
          <button
            onClick={onApprove}
            className="w-full h-12 rounded-xl bg-accent text-background font-medium text-sm flex items-center justify-center gap-2 hover:bg-accent-hover transition-colors"
          >
            <Check size="18" variant="Linear" />
            Approve &amp; Start Building
          </button>
          <p className="text-[11px] text-text-muted text-center mt-3">
            Switches to Build mode and begins generating code
          </p>
        </div>
      </div>
    </div>
  );
}
