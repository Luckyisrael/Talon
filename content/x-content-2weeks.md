# SeekerStudio — 2-Week X Content Plan (Pre-Launch Awareness)

**Goal:** build attention and credibility *before* launch. Two accounts, two voices, one story.
**Window:** 14 days. **Accounts:**
- **@seekerstudio** — the product. Confident, visionary, benefit-first, clean.
- **@yourhandle** — you, the builder. First-person, honest, building-in-public. Amplifies the official account and adds the human story.

> Replace `@seekerstudio` and `@yourhandle` with the real handles. `[asset]` notes mark where to attach a screen recording, screenshot, or brand card — visuals are what make these land. Character counts are kept ≤280 for single posts unless marked **THREAD**.

---

## How to use this doc

1. Each day has an **Official** post and a **Personal** post. Post the official one first; reply/quote or repost from your personal account within a couple of hours so the algorithm sees engagement between them.
2. The two-week arc: **Week 1 = the "what" and "why"** (wedge, problem, differentiators). **Week 2 = proof and momentum** (agentic behavior, verification, ramp to waitlist/early access).
3. Every post ends with a reason to follow or a reason to reply. Don't post into the void.

### Honesty rules (this matters — the product's whole pitch is that it doesn't lie)
- We are **pre-launch**. No fabricated user counts, no fake testimonials, no invented launch date. "Building in public," "early access soon," "join the waitlist" — all true, all fine.
- Every capability claim below maps to something that actually exists in the build: baked-in skills, deterministic guardrails, the verify ladder, model-agnostic quality, MWA/Seed Vault-only. Keep it that way.

### Assets to prepare first (2–3 hours of work; reused all fortnight)
- **A1 — 30–60s screen recording:** type a prompt → watch an app appear → run it. Your single most valuable asset.
- **A2 — Wallet-connect clip:** a generated app doing a Mobile Wallet Adapter connect on a Seeker (or emulator).
- **A3 — 3–4 clean screenshots:** the IDE, the agent's trace/steps, a verify pass, a finished screen.
- **A4 — A brand card:** logo + one-line tagline, for the intro and recap posts.
- **A5 — The "it fixes its own errors" clip:** the agent hitting a typecheck error and repairing it.

### Discipline on tags & hashtags
- Tag `@solanamobile` only on genuinely ecosystem-relevant posts (Seeker, dApp store, MWA). Don't tag them every day — it reads as begging.
- Max **1–2 hashtags**, and only real ones: `#Solana` `#SolanaMobile` `#Seeker`. X rewards clean copy, not hashtag soup.
- Quote-tweet Solana Mobile / Seeker news with *your* angle — that's free, relevant reach.

---

# WEEK 1 — The Wedge

## Day 1 — The manifesto

**Official** · pillar: vision · `[A4 brand card]`
> Everyone's building AI that ships websites.
>
> We're building AI that ships apps for the phone that *is* your wallet.
>
> SeekerStudio: describe the mobile app you want → get a real Solana Seeker app. Wallet-connected, on-device, ready to run.
>
> Building in public. Follow along 👇

**Personal** · pillar: why-I'm-building
> For months I've been obsessed with one question:
>
> what if building a Solana mobile app was as easy as *describing* it?
>
> Not a web app in a wrapper. A real Seeker app — Mobile Wallet Adapter, Seed Vault, the whole thing.
>
> That's @seekerstudio. I'll be sharing the whole build here.

---

## Day 2 — The problem (name the enemy)

**Official** · pillar: positioning
> Generic AI builders give you a website and call it a mobile app.
>
> On Solana Seeker that falls apart instantly — no Mobile Wallet Adapter, no Seed Vault, no native feel.
>
> SeekerStudio is built for the device, not ported to it. That's the whole difference.

**Personal** · pillar: hot-take
> I tried building a Seeker app with a general AI coding tool.
>
> It imported a *browser* wallet library. On a phone. That has a hardware wallet built in. 🤦
>
> That moment is basically why @seekerstudio exists.

---

## Day 3 — Show, don't tell (demo teaser #1)

**Official** · pillar: demo · `[A1 prompt→app recording]`
> "Build me a SOL balance screen that connects my wallet."
>
> One sentence in. A working Seeker app out. 👇
>
> No boilerplate, no wallet-adapter rabbit hole. This is SeekerStudio.
>
> (early build — more soon)

**Personal** · pillar: behind-the-demo
> That clip @seekerstudio just posted?
>
> The part that still gets me: I didn't tell it *how* to connect the wallet. It already knew Mobile Wallet Adapter — the right way, the on-device way.
>
> More on how that works this week.

---

## Day 4 — Security as a structural constraint

**Official** · pillar: trust/security
> An AI that builds crypto apps should never see your seed phrase.
>
> SeekerStudio *can't* — it's structurally impossible. It only ever uses Mobile Wallet Adapter + Seed Vault, exactly like a real Seeker app.
>
> Security isn't a feature we added. It's a rule we enforce in code.

**Personal** · pillar: principle · `[A3 screenshot: guardrail in action]`
> One of the first things I hard-coded into @seekerstudio:
>
> it will refuse to import a browser wallet, and it will never touch key material. Not "we prompted it not to" — the guardrail blocks it before it runs.
>
> If your builder *can* leak a key, it eventually will.

---

## Day 5 — The deep differentiator: model-agnostic quality **THREAD**

**Official** · pillar: technical-moat · `[A3 screenshots]`
> Most AI app builders have a dirty secret: they're only as good as the model behind them. Swap in a cheaper model and the output falls apart.
>
> We refused to accept that. Here's how SeekerStudio ships correct Seeker apps *regardless* of the model 🧵

> 1/ Baked-in knowledge. The builder already knows Mobile Wallet Adapter, expo-router, Reanimated, and Seeker design — pinned to exact versions. The model doesn't have to remember; the knowledge is always in the room.

> 2/ Deterministic guardrails. The rules that matter aren't left to the model's judgment. It cannot import a browser wallet or handle a seed phrase — enforced in code, not in a prompt.

> 3/ A verification ladder. Every build is typechecked and linted. If it doesn't compile, the agent reads the error and fixes itself. Broken code never reaches you.

> 4/ Adaptive prompting. Weaker models get more scaffolding; stronger models get out of the way. The floor stays high no matter what's driving.

> 5/ The result: quality that doesn't depend on you paying for the biggest model — and we prove it with an evals harness that scores every model on the same yardstick.
>
> This is the part I'm proudest of. Early access soon. Follow @seekerstudio 👀

**Personal** · pillar: amplify + conviction (quote-tweet the thread)
> This thread is the heart of the whole project 👇
>
> Anyone can wrap a model in a nice UI. Making the output *good even when the model isn't* — that's the hard, boring, unglamorous work. It's also the only thing that matters.

---

## Day 6 — Skills baked in

**Official** · pillar: capability · `[A2 wallet-connect clip]`
> You shouldn't have to teach your AI how Solana Mobile works.
>
> SeekerStudio ships knowing:
> • Mobile Wallet Adapter (connect + sign)
> • expo-router navigation
> • Reanimated motion
> • Seeker screen design
>
> It builds like someone who's shipped on Seeker before.

**Personal** · pillar: craft
> Spent a week writing "skills" for @seekerstudio — little expert playbooks the builder pulls in exactly when a task needs them (wallet, navigation, animation, layout).
>
> It's the difference between an AI that *guesses* and one that *knows*.

---

## Day 7 — Community + soft CTA (recap + poll)

**Official** · pillar: engagement/poll
> Quick one for the Solana builders:
>
> what's stopped you from shipping a Seeker app?
>
> 🔷 Wallet integration pain
> 🔷 RN/Expo setup
> 🔷 No time / too fiddly
> 🔷 Didn't know where to start
>
> (building SeekerStudio to kill all four)

**Personal** · pillar: week-1-recap
> Week 1 of building @seekerstudio in public, in one line:
>
> an AI builder that's actually *native* to Solana Seeker — knows the wallet, won't leak your keys, and won't hand you code that doesn't run.
>
> Week 2 I'll show it thinking and fixing its own mistakes. Follow along.

---

# WEEK 2 — Proof & Momentum

## Day 8 — The agent actually thinks **THREAD**

**Official** · pillar: agentic · `[A1 / A5 clips]`
> "AI builder" usually means fancy autocomplete.
>
> SeekerStudio is an *agent*. Give it a goal and it plans, writes, tests, and iterates — like a developer, not a text box 🧵

> 1/ It reads your project before it edits it. No blind overwrites.

> 2/ It works in steps you can watch: plan → build → verify → fix. The whole trace is visible, so you're never wondering what it did.

> 3/ It runs the checks itself and reacts to the results. A failure isn't the end of the turn — it's the start of the next fix.

> 4/ You get a working app *and* a readable trail of how it got there. Follow @seekerstudio — early access is close.

**Personal** · pillar: build-in-public
> The unlock for @seekerstudio wasn't a bigger model.
>
> It was giving the agent a real loop: plan, act, check, fix — and the discipline to not stop while something's broken.
>
> Turns out "keep going until it actually works" is a feature.

---

## Day 9 — "It won't give you code that doesn't compile"

**Official** · pillar: quality/verify · `[A5 self-repair clip]`
> Every build in SeekerStudio runs a verification ladder: typecheck → lint.
>
> If it fails, the agent fixes it and tries again. Only verified code reaches you.
>
> "It works on my machine" isn't good enough. It should work, period.

**Personal** · pillar: honest-proof
> I say "it won't hand you broken code" in every demo and people don't believe me.
>
> So, the boring truth: each build is typechecked and linted, and if it fails the agent repairs itself and re-runs — up to 6 times. Verified or it doesn't ship. 👇
>
> `[A3/A5 screenshots]`

---

## Day 10 — Behind the scenes (personal-heavy)

**Official** · pillar: build-in-public · `[A3 screenshot: agent trace]`
> A peek under the hood: every SeekerStudio build streams its steps live — the plan, each file it touches, each check it runs.
>
> Because you should be able to *trust* what your builder did, not just accept the output.

**Personal** · pillar: story/struggle
> Hardest problem this week on @seekerstudio:
>
> making a *small* model produce a correct wallet flow. Big models fake it fine. Small ones expose every gap in your scaffolding.
>
> Fixing it for the weak model made the whole thing better for the strong one. That's the game.

---

## Day 11 — Why mobile crypto, why now (vision)

**Official** · pillar: vision · tag `@solanamobile`
> The Seeker puts a wallet, a secure element, and a dApp store in your pocket.
>
> The missing piece isn't hardware — it's making it stupidly easy to *build* for it.
>
> That's the gap SeekerStudio fills. Mobile is where the next million Solana users actually are. #SolanaMobile

**Personal** · pillar: conviction
> I genuinely think the next wave of Solana apps won't be born on desktop.
>
> They'll be born on the phone people already carry their crypto on. @seekerstudio is a bet on that future — and on builders who want in without a month of setup.

---

## Day 12 — Demo teaser #2 (a real wallet action)

**Official** · pillar: demo · `[A2 send-SOL or balance clip]`
> From one prompt to a working "send SOL" screen — recipient, amount, sign with Mobile Wallet Adapter, handle the user cancelling.
>
> The stuff that normally eats your afternoon. Watch 👇

**Personal** · pillar: amplify
> Little things in this demo I care about a lot:
>
> it handles the *cancel* case. Real wallet UX isn't the happy path — it's what happens when the user backs out. @seekerstudio builds for that by default.

---

## Day 13 — Early access / waitlist push

**Official** · pillar: CTA/waitlist · `[A4 brand card]`
> Early access to SeekerStudio is coming.
>
> If you want to build a Solana Seeker app by describing it — and skip the wallet-adapter and Expo setup entirely — get on the list.
>
> 🔗 [waitlist link]
>
> Spots are limited at first. Follow + drop a 🔨 if you want in.

**Personal** · pillar: personal-invite
> If you've ever wanted to ship something on Solana Seeker but bounced off the setup —
>
> @seekerstudio early access is opening soon and I want *builders* in the first group.
>
> Reply or DM "seeker" and I'll make sure you're on the list. 🔨

---

## Day 14 — The recap that converts **THREAD**

**Official** · pillar: summary/CTA · `[A1 best clip]`
> Two weeks of building SeekerStudio in public. Here's the whole thing in 6 posts 🧵

> 1/ It's an AI builder *native* to Solana Seeker. Describe an app → get a real, wallet-connected mobile app. Not a website in a wrapper.

> 2/ It's secure by construction. Never sees a seed phrase, never imports a browser wallet. Mobile Wallet Adapter + Seed Vault only — enforced in code.

> 3/ It's an agent, not autocomplete. Plans, builds, verifies, and fixes its own errors — with the whole trace visible.

> 4/ It's model-agnostic. Baked-in Seeker knowledge, deterministic guardrails, and a verification ladder keep quality high no matter which model drives.

> 5/ It won't hand you broken code. Typecheck → lint on every build, repaired until it's verified.

> 6/ Early access is opening soon. If you want to build on Seeker the easy way: follow @seekerstudio and join 🔗 [waitlist link]. This is just the start.

**Personal** · pillar: gratitude + forward-look
> Two weeks ago I started sharing @seekerstudio publicly. The response from the Solana Mobile community has been unreal. 🙏
>
> We're just getting to the good part. Early access soon — and I'll keep building in the open the whole way.
>
> If you build on Seeker, come say hi.

---

# Reusable banks

## Hook lines (swap into any post)
- "Everyone's building AI that ships websites. We ship Seeker apps."
- "An AI that builds crypto apps should never see your seed phrase. Ours can't."
- "It won't hand you code that doesn't compile."
- "Built for the device, not ported to it."
- "Quality that doesn't depend on you paying for the biggest model."
- "Describe the app. Get the app. On Seeker."

## CTAs
- "Follow @seekerstudio — building in public."
- "Early access is coming. Join the waitlist 🔗 [link]."
- "Reply '🔨' if you'd use this."
- "DM 'seeker' for an early-access spot."
- "What would you build first? 👇"

## Engagement playbook (do daily, costs nothing)
- **Reply** to @solanamobile / Seeker / Solana dev posts with a genuine, on-topic take (not a plug). Visibility compounds.
- **Quote-tweet** ecosystem news ("Seeker did X") with your builder's angle.
- **Answer** "how do I build on Seeker?" questions in the wild — helpfully, then mention the tool only if it fits.
- **Repost** your own official posts from the personal account with an added line of commentary (don't just RT).
- Reply to *your own* threads with the demo clip — threads with media in replies get re-surfaced.

## Posting cadence
- 1 official + 1 personal per day is the floor. Threads on Day 5, 8, 14.
- Rough best windows (US/EU crypto-Twitter overlap): ~9–11am and ~4–7pm ET. Test and adjust.
- Space the official and personal posts a couple hours apart so they cross-pollinate.

## Metrics to watch (screenshot weekly)
- Follower growth on both accounts (personal usually outpaces official early — that's normal).
- Profile visits & link clicks on waitlist-CTA days (13, 14).
- Which pillar earns the most saves/replies — **make more of that next fortnight.**
- Waitlist signups per post (tag your links with UTM/source so you know what converted).

---

## Notes / to-fill before posting
- [ ] Real handles in place of `@seekerstudio` / `@yourhandle`
- [ ] Waitlist link live (even a simple form) before Day 13
- [ ] Assets A1–A5 recorded
- [ ] A pinned tweet on both profiles (use Day 1 official + Day 1 personal)
- [ ] Bios updated: what it is + "early access soon" + link
