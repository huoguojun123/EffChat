import { beforeEach, describe, expect, it, vi } from "vitest"
import type { User } from "@/types"
import { useAuthStore } from "@/stores/auth"
import { api, ApiError } from "@/api/client"
import * as authApi from "@/api/auth"
import { clearAccountScopedState } from "@/stores/accountState"

vi.mock("@/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/client")>()
  return { ...actual, api: { get: vi.fn() } }
})
vi.mock("@/api/auth", () => ({ login: vi.fn(), register: vi.fn() }))
vi.mock("@/stores/accountState", () => ({ clearAccountScopedState: vi.fn() }))

const apiGet = vi.mocked(api.get)
const loginMock = vi.mocked(authApi.login)
const clearAccountState = vi.mocked(clearAccountScopedState)
const storage = new Map<string, string>()

function user(id: number, username: string): User {
  return {
    id,
    username,
    role: "user",
    is_super_admin: false,
    is_active: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

beforeEach(() => {
  vi.clearAllMocks()
  storage.clear()
  vi.stubGlobal("localStorage", {
    getItem: vi.fn((key: string) => storage.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => storage.set(key, value)),
    removeItem: vi.fn((key: string) => storage.delete(key)),
  })
  useAuthStore.setState({ user: null, token: null, hydrated: true, hydrationError: null, isLoading: false })
})

describe("useAuthStore cross-tab synchronization", () => {
  it("keeps the stored token when hydration fails without an authentication response", async () => {
    storage.set("token", "still-valid-token")
    apiGet.mockRejectedValue(new ApiError(0, "网络连接失败，请检查后端服务或网络"))

    await useAuthStore.getState().hydrate()

    expect(storage.get("token")).toBe("still-valid-token")
    expect(clearAccountState).not.toHaveBeenCalled()
    expect(useAuthStore.getState()).toMatchObject({
      user: null,
      token: "still-valid-token",
      hydrated: false,
      hydrationError: "网络连接失败，请检查后端服务或网络",
    })
  })

  it("clears the stored token when hydration explicitly returns unauthorized", async () => {
    storage.set("token", "expired-token")
    apiGet.mockRejectedValue(new ApiError(401, "invalid or expired token"))

    await useAuthStore.getState().hydrate()

    expect(storage.has("token")).toBe(false)
    expect(clearAccountState).toHaveBeenCalledOnce()
    expect(useAuthStore.getState()).toMatchObject({ user: null, token: null, hydrated: true, hydrationError: null })
  })

  it("clears account state and hydrates the user for a token from another tab", async () => {
    const nextUser = user(2, "next")
    storage.set("token", "next-token")
    useAuthStore.setState({ user: user(1, "previous"), token: "previous-token", hydrated: true })
    apiGet.mockResolvedValue(nextUser)

    await useAuthStore.getState().syncStoredToken("next-token")

    expect(clearAccountState).toHaveBeenCalledOnce()
    expect(useAuthStore.getState()).toMatchObject({ user: nextUser, token: "next-token", hydrated: true })
  })

  it("does not let a stale hydration overwrite a newer cross-tab identity", async () => {
    const stale = deferred<User>()
    const current = deferred<User>()
    apiGet.mockReturnValueOnce(stale.promise).mockReturnValueOnce(current.promise)
    storage.set("token", "old-token")

    const staleHydration = useAuthStore.getState().hydrate()
    storage.set("token", "new-token")
    const currentHydration = useAuthStore.getState().syncStoredToken("new-token")
    current.resolve(user(2, "new"))
    await currentHydration
    stale.resolve(user(1, "old"))
    await staleHydration

    expect(useAuthStore.getState()).toMatchObject({ user: user(2, "new"), token: "new-token", hydrated: true })
  })

  it("moves immediately to logged-out state when another tab removes the token", async () => {
    useAuthStore.setState({ user: user(1, "previous"), token: "previous-token", hydrated: true })

    await useAuthStore.getState().syncStoredToken(null)

    expect(clearAccountState).toHaveBeenCalledOnce()
    expect(useAuthStore.getState()).toMatchObject({ user: null, token: null, hydrated: true })
    expect(apiGet).not.toHaveBeenCalled()
  })

  it("does not let an earlier login replace a newer cross-tab identity", async () => {
    const staleLogin = deferred<{ token: string; user: User }>()
    loginMock.mockReturnValue(staleLogin.promise)
    const pendingLogin = useAuthStore.getState().login("old", "password")

    storage.set("token", "new-token")
    apiGet.mockResolvedValue(user(2, "new"))
    await useAuthStore.getState().syncStoredToken("new-token")
    staleLogin.resolve({ token: "old-token", user: user(1, "old") })
    await pendingLogin

    expect(storage.get("token")).toBe("new-token")
    expect(useAuthStore.getState()).toMatchObject({ user: user(2, "new"), token: "new-token", hydrated: true })
  })
})
