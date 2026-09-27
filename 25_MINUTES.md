No—25 minutes of exploration before writing anything is not normal for an interactive AI builder. An 8,529-token system prompt is large, but it is not the main problem. The likely issue is your agent’s workflow and context selection.

Prompt size and wall-clock time are separate concerns:

- 8,529 tokens: large-ish, but manageable on modern long-context models.
- 25 minutes of reading: usually caused by too many file reads, broad exploration, repeated planning, or lack of cached project/template knowledge.
- No output until analysis is “complete”: indicates the agent does not have clear execution milestones.

## What is probably happening

A typical inefficient loop looks like this:

1. Agent reads many template files.
2. Reads files it does not need.
3. Re-reads files after forgetting details.
4. Summarizes what it found.
5. Reads more files to verify the summary.
6. Plans again.
7. Finally begins editing.

For a React Native builder, this becomes especially expensive because projects contain many screens, components, assets, native files, and configuration files.

## The key design change

Do not ask the agent to “understand the whole template and codebase.”

Instead, separate the workflow into:

1. Static template knowledge
2. Current project state
3. Task-specific retrieval
4. Planning
5. Incremental implementation
6. Validation

The agent should only understand the part of the system relevant to the current task.

---

## 1. Pre-analyze the template

Templates are static. You should not make the agent analyze the full template on every generation.

Build the template once and store a compact digest:

{
  "template_name": "expo-tabs",
  "version": "3.2.1",
  "structure": {
    "app": "Expo Router file-based routes",
    "components": "Shared UI components",
    "store": "Zustand state management",
    "theme": "Centralized colors, spacing, and typography"
  },
  "important_files": [
    "app/_layout.tsx",
    "app/(tabs)/_layout.tsx",
    "package.json",
    "app.json"
  ],
  "generation_rules": [
    "Use TypeScript",
    "Use functional components",
    "Place routes under app/",
    "Reuse components from components/ui"
  ],
  "do_not_modify": [
    "package-lock.json",
    "ios/Pods",
    "android/build"
  ]
}
Cache this by template name and version/hash. Future sessions should load the digest instead of rereading the template.

This alone can eliminate a large portion of the 25 minutes.

---

## 2. Build a project index, not a full-project read

Create a lightweight repository manifest:

{
  "framework": "React Native",
  "router": "Expo Router",
  "state_management": "Zustand",
  "styling": "NativeWind",
  "navigation_root": "app/_layout.tsx",
  "screens": [
    "app/index.tsx",
    "app/settings.tsx"
  ],
  "components": [
    "components/Button.tsx",
    "components/Card.tsx"
  ],
  "native_configuration": {
    "android": "android/build.gradle",
    "ios": "ios/Podfile"
  }
}
Generate or update this index when the project changes.

The agent should use it to answer questions like:

- Where are routes defined?
- Which state-management library is used?
- Which components already exist?
- Has the user customized the theme?
- Is this an Expo or bare React Native project?

It should not read every file merely to answer those questions.

---

## 3. Use targeted retrieval

For a task such as “Add a profile screen,” retrieve only:

- Routing configuration
- Existing screen patterns
- Relevant UI components
- Theme files
- State/store files if needed
- package.json if dependencies might change

Ignore unrelated files.

For React Native specifically, avoid reading these unless the task requires them:

- node_modules
- ios/Pods
- android/build
- Full lockfiles
- Every screen
- Every component
- Asset binaries
- Generated native code

Read native configuration only for native or build-related tasks.

---

## 4. Enforce an exploration budget

The agent should not have unlimited “let me check one more thing” behavior.

Use limits such as:
max_exploration_files: 12
max_exploration_rounds: 3
max_file_read_chars: 12000
must_plan_after_exploration: true
must_generate_initial_files: true
A useful execution policy is:

- Spend at most one phase on discovery.
- Produce a short plan.
- Identify the first files to create or modify.
- Begin implementation.
- Retrieve more information only when the current step is blocked.

This prevents endless analysis.

---

## 5. Give the agent small execution milestones

Instead of:

> Analyze the template and codebase, then implement the application.

Use:

> 1. Inspect the project manifest and routing configuration.
> 2. Produce a five-step implementation plan.
> 3. Create the first route.
> 4. Create required components.
> 5. Wire navigation.
> 6. Run validation.
> 7. Continue to the next feature.

The agent should create value incrementally rather than trying to form a complete mental model first.

---

## 6. Reduce the system prompt

8,529 tokens is not inherently bad, but it may be bloated. Check whether it contains:

- Long examples
- Repeated instructions
- Entire coding guidelines
- Large JSON schemas
- React Native documentation
- General TypeScript rules
- Duplicated tool instructions
- Explanations the model already knows

Move most domain knowledge into retrieval or indexed rules. Keep the system prompt for non-negotiable behavior:

- Architecture rules
- File placement conventions
- Safety constraints
- Tool-use rules
- Output format
- Validation requirements
- What not to modify

A concise system prompt is generally easier to follow consistently.

For example, instead of embedding all React Native rules:

You are a React Native generation agent.

Before editing:
1. Inspect the project manifest.
2. Follow the detected router and styling conventions.
3. Reuse existing components when appropriate.
4. Modify only files required by the current task.
5. Validate changes with the configured linter or tests.
Then put detailed RN guidance in a versioned knowledge base.

---

## 7. Parallelize reads and cache aggressively

If the agent is reading 40 files sequentially, that can dominate the 25 minutes.

You can:

- Read independent files in parallel
- Cache unchanged files by content hash
- Cache template analysis
- Cache documentation retrieval
- Store AST/symbol summaries instead of full source
- Reuse previous session summaries
- Avoid rereading a file unless it changed

A coding agent should often retrieve symbols and summaries first, then read full files only when necessary.

---

## 8. Make progress visible

Even if analysis takes some time, the user should not wait in silence.

Emit progress such as:

Detected Expo Router project
Found 6 screens and 14 components
Loaded cached template knowledge
Selected 5 relevant files
Created implementation plan
Generating app/profile.tsx
This does not make the process faster by itself, but it makes the experience feel intentional rather than stalled.

---

## A better React Native builder pipeline

User request
   ↓
Classify task
   ↓
Load cached template digest
   ↓
Load current project manifest
   ↓
Retrieve only relevant files/symbols
   ↓
Generate short implementation plan
   ↓
Create/modify first file
   ↓
Retrieve additional context only if blocked
   ↓
Implement remaining changes
   ↓
Run lint, TypeScript, tests, or build
   ↓
Summarize changes and update project memory
The important difference is that writing begins before the entire system is understood.

## What good timing might look like

Exact timing depends on model speed and project size, but an interactive builder should usually target something like:

| Stage | Target |
| --- | --- |
| Detect project type | Seconds |
| Load cached template/index | Seconds |
| Select relevant files | Under a minute |
| Produce initial plan | Under a minute |
| Generate first file | Within a few minutes |
| Validate and iterate | Varies |

A 25-minute initial analysis phase would be more appropriate for an autonomous migration of a very large codebase—not for a typical React Native app-generation request.

## The core principle

Your agent should not be designed to “read everything, understand everything, then act.”

It should be designed to:

> Know enough about the current step, act, observe the result, and retrieve more information only when needed.

That is how you reduce both latency and context usage.