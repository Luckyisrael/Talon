import AsyncStorage from '@react-native-async-storage/async-storage'

// Offline snapshot: the last fetched transcript per project, read-only.
// Rendered badged-stale while the network catches up, never written to:
// the server owns truth, this is just the fastest paint on a cold start.
const key = (projectId: number) => `talon.history.${projectId}`

export interface HistorySnapshot {
  at: number
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  turns: any[]
}

export async function saveSnapshot(projectId: number, turns: unknown[]): Promise<void> {
  try {
    await AsyncStorage.setItem(key(projectId), JSON.stringify({ at: Date.now(), turns: turns.slice(-60) }))
  } catch {
    // Cache failure must never break the transcript.
  }
}

export async function loadSnapshot(projectId: number): Promise<HistorySnapshot | null> {
  try {
    const raw = await AsyncStorage.getItem(key(projectId))
    if (!raw) return null
    const parsed = JSON.parse(raw) as HistorySnapshot
    if (!Array.isArray(parsed.turns)) return null
    return parsed
  } catch {
    return null
  }
}
