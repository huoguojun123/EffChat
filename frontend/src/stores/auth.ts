import { create } from "zustand"
import type { RegisterResponse, User } from "@/types"
import { api } from "@/api/client"
import * as authApi from "@/api/auth"
import { clearAccountScopedState } from "./accountState"

interface AuthState {
  user: User | null
  token: string | null
  hydrated: boolean
  isLoading: boolean
  login: (username: string, password: string) => Promise<void>
  register: (username: string, password: string) => Promise<RegisterResponse>
  logout: () => void
  hydrate: () => Promise<void>
  syncStoredToken: (token: string | null) => Promise<void>
  setUser: (user: User) => void
}

let latestHydration = 0

function readStoredToken() {
  return typeof localStorage === "undefined" ? null : localStorage.getItem("token")
}

export const useAuthStore = create<AuthState>()((set, get) => ({
  user: null,
  token: readStoredToken(),
  hydrated: false,
  isLoading: false,

  login: async (username: string, password: string) => {
    const operation = ++latestHydration
    set({ isLoading: true })
    try {
      const res = await authApi.login(username, password)
      if (operation !== latestHydration) return
      localStorage.setItem("token", res.token)
      set({ user: res.user, token: res.token, hydrated: true })
    } finally {
      if (operation === latestHydration) set({ isLoading: false })
    }
  },

  register: async (username: string, password: string) => {
    const operation = ++latestHydration
    set({ isLoading: true })
    try {
      const res = await authApi.register(username, password)
      if (operation === latestHydration && res.token && res.user) {
        localStorage.setItem("token", res.token)
        set({ user: res.user, token: res.token, hydrated: true })
      }
      return res
    } finally {
      if (operation === latestHydration) set({ isLoading: false })
    }
  },

  logout: () => {
    latestHydration += 1
    clearAccountScopedState()
    localStorage.removeItem("token")
    set({ user: null, token: null, hydrated: true })
    window.location.href = "/login"
  },

  hydrate: async () => {
    const hydration = ++latestHydration
    const token = readStoredToken()
    if (!token) {
      set({ user: null, token: null, hydrated: true })
      return
    }
    set({ token, hydrated: false })
    try {
      const user = await api.get<User>("/users/me")
      if (hydration !== latestHydration || readStoredToken() !== token) return
      set({ user, hydrated: true })
    } catch {
      if (hydration !== latestHydration || readStoredToken() !== token) return
      clearAccountScopedState()
      localStorage.removeItem("token")
      set({ user: null, token: null, hydrated: true })
    }
  },

  syncStoredToken: async (token: string | null) => {
    if (token === get().token && get().hydrated) return
    latestHydration += 1
    clearAccountScopedState()
    set({ user: null, token, hydrated: token === null, isLoading: false })
    if (token) await get().hydrate()
  },

  setUser: (user: User) => set({ user }),
}))
