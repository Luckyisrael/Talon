import AsyncStorage from '@react-native-async-storage/async-storage'
import { create } from 'zustand'

// App store: client state with exactly one owner. Server state stays in
// TanStack Query (projects list, turns, session rows); this holds the small
// UI truth every tab reads: which project is active. Tapping a project on
// the dashboard pins it here (persisted), and Build, Diff, Device, and the
// drawer all resolve through useActiveProject, so one tap loads everywhere.
const ACTIVE_PROJECT_KEY = 'talon.activeProject'
export const ONBOARDED_KEY = 'talon.onboarded'

interface AppState {
  activeProjectId: number | null
  onboarded: boolean
  hydrated: boolean
  hydrate: () => Promise<void>
  setActiveProjectId: (id: number) => Promise<void>
  markOnboarded: () => Promise<void>
}

export const useAppStore = create<AppState>()((set) => ({
  activeProjectId: null,
  onboarded: false,
  hydrated: false,
  hydrate: async () => {
    try {
      const [pinned, seen] = await Promise.all([
        AsyncStorage.getItem(ACTIVE_PROJECT_KEY),
        AsyncStorage.getItem(ONBOARDED_KEY),
      ])
      set({
        activeProjectId: pinned ? Number(pinned) : null,
        onboarded: seen === '1',
        hydrated: true,
      })
    } catch {
      // Persistence is a nicety; tabs still resolve to the most recent.
      set({ hydrated: true })
    }
  },
  setActiveProjectId: async (id) => {
    set({ activeProjectId: id })
    try {
      await AsyncStorage.setItem(ACTIVE_PROJECT_KEY, String(id))
    } catch {
      // The pin below already switched tabs now; the restart just forgets.
    }
  },
  markOnboarded: async () => {
    set({ onboarded: true })
    try {
      await AsyncStorage.setItem(ONBOARDED_KEY, '1')
    } catch {
      // Worst case the tour replays once.
    }
  },
}))
