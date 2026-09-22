# Hyperframes Composition Brief: Talon

## Objective
Create a short launch-style brag video for Talon — a coming-soon launch teaser that treats a browser IDE like a blockbuster, then lands the honest punchline: Pro is coming soon, building is open now.

## Output
- Composition directory: `brag-output-2026-09-21-232742/composition/`
- Rendered video: `brag-output-2026-09-21-232742/brag.mp4`
- Format: landscape — 1920x1080
- Duration: 20s

## Source Material
- Project root: `/home/lucky-israel/Desktop/Projects/Talon`
- Primary files read: `README.md`, `frontend/app/page.tsx`, `frontend/app/waitlist/page.tsx`, `frontend/app/globals.css`, `frontend/app/layout.tsx`, `frontend/components/marketing/MarketingNav.tsx`, `frontend/components/ide/AgentWorkspace.tsx`
- Product name: Talon
- Tagline / strongest claim: "Build Solana Mobile apps at the speed of thought"
- Key UI or visual moment to recreate: the agent workspace stream — plan-card checklist ticking through 4 steps, tool-trace rows, the verify-ladder chips (STATIC / RUNTIME / VISION / INTERACT) flipping PASS, and the phone preview lighting up with a pulsing LIVE lamp.
- Copy that must appear verbatim:
  - "Build Solana Mobile apps at the speed of thought" (hero — may be trimmed to a beat line if scene space demands; prefer verbatim)
  - "It won't ship broken code." (waitlist pillar)
  - "The waitlist is for Pro. Building is open now." (waitlist hero — the punchline; "Building is open now." must appear)
  - "20 prompts · 60 minutes device time · free" (waitlist CTA microcopy)
  - "EARLY ACCESS" badge; "COMING SOON" line
  - "Talon-native AI coding platform" (hero pill)
  - "No VS Code, no Android Studio, no local toolchain." (feature card — optional if space)

## Creative Direction
- Tone preset: `cinematic`
- Creative direction: coming soon launch teaser (user-provided `--tone "coming soon launch"`)
- Interpretation: trailer-scale pacing — one short declarative claim per scene, giant tight-tracked Inter on the void, dramatic scale-in reveals and slow crossfades, restraint everywhere except the single acid-lime accent and 2–3 big audio hits.
- Angle: A coming-soon launch teaser for a product that is quietly already usable. Black-void blockbuster trailer language, but the punchline is honest: the card says COMING SOON while the Free tier is open today — the project's own waitlist copy, not a invented joke.
- Hook: Void + the Talon mark (hairline square, lime dot) breathing once; display lines slam in: "ONE SENTENCE." then "A SHIPPED APP."
- Outro / punchline: Mark + "Talon" slam full-screen, EARLY ACCESS badge, "COMING SOON." then "Building is open now." and the mono microcopy "20 prompts · 60 minutes device time · free".
- Avoid:
  - Generic SaaS language ("streamline your workflow" class of copy)
  - Abstract filler visuals (color washes, particles, generic motion graphics)
  - Unrelated visual redesign — the composition must look like Talon's Linear-midnight instrument aesthetic

## Visual Identity
- Background: `#08090a` (void); panels `#0f1011` (carbon) / `#161718` (obsidian); hairline `#23252a` (graphite)
- Text: `#ffffff` (paper), `#d0d6e0` (mist), `#8a8f98` (fog), `#62666d` (ash)
- Accent: `#e4f222` (acid-lime — single chromatic action), soft `rgba(228,242,34,0.16)`, success `#27a644` (pulse-green)
- Hero floor gradient (the only decorative gradient): `linear-gradient(to bottom, #1a1918 10%, #2a2725 100%)`
- Display font: Inter Variable (weights ≤590, letter-spacing −0.022em). Ship a local `@font-face` (or fall back to system sans with the same tracking rules) — no named family without an in-file `@font-face`.
- Body/mono font: JetBrains Mono or generic `ui-monospace` for chips/gauges (uppercase, 11–12px, 0.08em tracking).
- Visual references from the project: Talon logo mark (18×18 hairline rounded square + 2.5r lime dot), pill badges, plan card with numbered checklist, verify-ladder chips, status lamps (6px dots with soft glow), hairline panel edges with 12px radii, mono gauge labels.

## Storyboard
Use the storyboard in `brag-output-2026-09-21-232742/brag-plan.md` as the creative contract.

Scene summary:
1. Hook: One sentence — 4s — Talon mark breathes; "ONE SENTENCE." / "A SHIPPED APP." slam in on void.
2. Reveal: The browser IDE — 4s — IDE window scales in on hero-floor gradient; composer types "Build a tip jar app with Solana wallet sign-in"; pill "Talon-native AI coding platform".
3. The plan executes — 4s — plan card lands; 4 checklist rows tick one by one; tool-trace rows stream beneath.
4. Verify, then live on device — 4s — 4 ladder chips flip PASS in sequence; "It won't ship broken code."; phone preview lights with LIVE lamp.
5. Outro: Coming soon — 4s — mark + "Talon" slam; EARLY ACCESS badge; "COMING SOON." → "Building is open now."; microcopy "20 prompts · 60 minutes device time · free".

## Audio
- Audio role: cinematic support
- Audio arc: bed fades in under the hook, stays low and steady through plan/verify with light motion-matched accents, swells into the logo slam (~17.5s), stops clean under the final hold.
- Music: `happy-beats-business-moves-vol-12-by-ende-dot-app.mp3` (copied into `composition/assets/music/`)
- Music treatment: fade-in ~0.8s from 0; bed volume 0.30–0.35 (never above 0.5); swell at the outro slam; 1s fade-out to silence on the final hold.
- Music cue guidance: bundled preset `assets/music/cues/happy-beats-business-moves-vol-12-by-ende-dot-app.music-cues.json` (tempo ≈109.96 BPM). Strong-cue candidates in-window: 8.74s (plan card lands), 13.11s (verify payoff / phone live), 17.47s (outro mark slam). Lock 1–3 of these within ±0.15s. Beat grid (excerpt): 0.56, 1.09, 1.64, 2.19, 2.73, 3.27, 3.82, 4.39, 4.91, 5.34, 6.00, 6.56, 7.09, 7.64, 8.19, 8.74, 9.29, 9.83, 10.37, 10.93, 11.46, 12.02, 12.55, 13.11, 13.64, 14.20, 14.73, 15.29, 15.84, 16.38, 16.93, 17.47, 18.02, 18.56 — snap small sequential events to beats (±0.10s), but readable text lines must hold their floor (short label ≥0.8s settled; sentence ≥0.3s/word): for the 4-row checklist and 4-chip ladder, reveal quickly and hold the full set ≥1.2s, or snap to every other beat.
- Audio-reactive treatment: subtle; music RMS/bass may breathe the lime dot glow, hero glow, and final logo presence. No waveform/equalizer visuals.
- Audio-coupled moments:
  - Scene 1 — hook line slams (impact accent on line 1; line 2 quieter)
  - Scene 2 — IDE window reveal (one deep bell), composer typing (randomized keypress ticks), send pulse
  - Scene 3 — plan checklist ticking row by row (light drops; accent first + last rows only)
  - Scene 4 — chip flips (crisp accents), phone LIVE (satisfying bell — the climax)
  - Scene 5 — logo slam (heavy bell + music swell), lines arrive quietly
- SFX selection guidance: cinematic posture — 2–3 big hits total (`impact/impactBell_heavy_*` for reveal/slam, `impact/impactSoft_medium_*` for transitions) plus light `interface/drop_*` / `keyboard/keypress-*` accents exactly where motion calls for them. Read `<skill-dir>/assets/sfx/sfx-analysis.md`; prefer low/medium high-frequency-risk files. Never stack SFX against the music; no sound during holds.
- Exact SFX choice: Hyperframes should choose filenames, timestamps, density, and volume based on the implemented animation.
- Audio files: music copied to `composition/assets/music/`; Hyperframes copies any SFX it selects into `composition/assets/`.

## Hyperframes Instructions
Load the composition-building Hyperframes domain skills — `hyperframes-core` (composition contract + `data-*` timing), `hyperframes-animation` (motion), `hyperframes-creative` (design spec, beats, audio-reactive), `hyperframes-keyframes` (seek-safe keyframes), and `hyperframes-cli` (lint/check/render). /brag is its own workflow: do not enter the `hyperframes` entry-point intent interview and do not route into its generic promo / launch-video workflow. Prefer native Hyperframes conventions over anything in `/brag`.

Requirements:
- Show at least one real UI, copy, or visual element from the source project — the plan card, verify ladder, phone preview, and logo mark are all from Talon's real source.
- Keep all text readable in the final render.
- Keep the video within 15-25 seconds (target 20s).
- Include the planned music/SFX layer.
- Treat `/brag` audio notes as guidance, not a fixed cue sheet. Choose SFX after the visual animation exists.
- Treat music cue metadata as optional timing hints. Ignore cues that hurt readability, scene pacing, or the product story.
- Major reveals may move toward nearby strong cues within about 0.15s. Smaller entrances may align to nearby beat points within about 0.10s. Use only 1-3 strong cue locks.
- Use SFX to support motion and interaction; restraint when the edit is already busy.
- Honor planned music treatment (fade-in, swell at slam, 1s fade-out on final hold).
- When music is present, consider the audio-reactive workflow: extract audio data and use RMS/frequency bands for subtle, brand-specific motion (lime dot glow, logo presence). Avoid waveform/equalizer visuals, particles, strobing, heavy pulsing.
- Use local assets for audio and any runtime/media dependencies.
- Run `hyperframes check` before render — it is brag's single gate.
