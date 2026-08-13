import { describe, expect, it, vi } from "vitest"
import { SessionSkillMutationCoordinator } from "@/components/chat/sessionSkillMutation"

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

async function flushPromises() {
  await Promise.resolve()
  await Promise.resolve()
}

describe("SessionSkillMutationCoordinator", () => {
  it("serializes rapid toggles and applies only the latest canonical response", async () => {
    const first = deferred<{ skills_enabled: string[] }>()
    const second = deferred<{ skills_enabled: string[] }>()
    const update = vi.fn()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise)
    const applyLocal = vi.fn()
    const coordinator = new SessionSkillMutationCoordinator({
      update,
      reload: vi.fn(),
      applyLocal,
    })

    coordinator.toggle(7, [], "alpha")
    coordinator.toggle(7, ["alpha"], "beta")
    coordinator.toggle(7, ["alpha", "beta"], "alpha")

    expect(update).toHaveBeenCalledTimes(1)
    expect(update).toHaveBeenLastCalledWith(7, ["alpha"])
    expect(applyLocal).toHaveBeenLastCalledWith(7, ["beta"])

    first.resolve({ skills_enabled: ["alpha"] })
    await flushPromises()
    expect(update).toHaveBeenCalledTimes(2)
    expect(update).toHaveBeenLastCalledWith(7, ["beta"])
    expect(applyLocal).toHaveBeenLastCalledWith(7, ["beta"])

    second.resolve({ skills_enabled: ["server-approved", "beta"] })
    await flushPromises()
    expect(applyLocal).toHaveBeenLastCalledWith(7, ["beta", "server-approved"])
  })

  it("continues with newer intent after an older request fails", async () => {
    const first = deferred<{ skills_enabled: string[] }>()
    const second = deferred<{ skills_enabled: string[] }>()
    const reload = vi.fn()
    const applyLocal = vi.fn()
    const coordinator = new SessionSkillMutationCoordinator({
      update: vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise),
      reload,
      applyLocal,
    })

    coordinator.toggle(8, [], "alpha")
    coordinator.toggle(8, ["alpha"], "beta")
    first.reject(new Error("old request failed"))
    await flushPromises()

    expect(reload).not.toHaveBeenCalled()
    second.resolve({ skills_enabled: ["alpha", "beta"] })
    await flushPromises()
    expect(applyLocal).toHaveBeenLastCalledWith(8, ["alpha", "beta"])
  })

  it("reloads canonical state when a newer request fails after an older success", async () => {
    const first = deferred<{ skills_enabled: string[] }>()
    const second = deferred<{ skills_enabled: string[] }>()
    const applyLocal = vi.fn()
    const onError = vi.fn()
    const coordinator = new SessionSkillMutationCoordinator({
      update: vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise),
      reload: vi.fn().mockResolvedValue(["canonical"]),
      applyLocal,
      onError,
    })

    coordinator.toggle(9, [], "alpha")
    coordinator.toggle(9, ["alpha"], "beta")
    first.resolve({ skills_enabled: ["alpha"] })
    await flushPromises()
    second.reject(new Error("latest write failed"))
    await flushPromises()

    expect(applyLocal).toHaveBeenLastCalledWith(9, ["canonical"])
    expect(onError).not.toHaveBeenCalled()
  })

  it("keeps sessions independent across an A-B-A switch", async () => {
    const aFirst = deferred<{ skills_enabled: string[] }>()
    const bFirst = deferred<{ skills_enabled: string[] }>()
    const aSecond = deferred<{ skills_enabled: string[] }>()
    let aCalls = 0
    const update = vi.fn((sessionId: number) => {
      if (sessionId === 10) {
        aCalls += 1
        return aCalls === 1 ? aFirst.promise : aSecond.promise
      }
      return bFirst.promise
    })
    const applyLocal = vi.fn()
    const coordinator = new SessionSkillMutationCoordinator({ update, reload: vi.fn(), applyLocal })

    coordinator.toggle(10, [], "alpha")
    coordinator.toggle(11, [], "beta")
    coordinator.toggle(10, ["alpha"], "gamma")

    bFirst.resolve({ skills_enabled: ["beta"] })
    aFirst.resolve({ skills_enabled: ["alpha"] })
    await flushPromises()
    expect(update).toHaveBeenCalledWith(10, ["alpha", "gamma"])

    aSecond.resolve({ skills_enabled: ["gamma"] })
    await flushPromises()
    expect(applyLocal).toHaveBeenLastCalledWith(10, ["gamma"])
    expect(applyLocal).toHaveBeenCalledWith(11, ["beta"])
  })

  it("invalidates pending work and rebases a later idle mutation", async () => {
    const pending = deferred<{ skills_enabled: string[] }>()
    const applyLocal = vi.fn()
    const onError = vi.fn()
    const update = vi.fn()
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValueOnce({ skills_enabled: ["fresh"] })
    const coordinator = new SessionSkillMutationCoordinator({
      update,
      reload: vi.fn(),
      applyLocal,
      onError,
    })

    coordinator.toggle(12, [], "stale")
    coordinator.invalidateAll()
    pending.reject(new Error("late failure"))
    await flushPromises()
    expect(onError).not.toHaveBeenCalled()

    coordinator.toggle(12, ["external"], "external")
    await flushPromises()
    expect(update).toHaveBeenLastCalledWith(12, [])
    expect(applyLocal).toHaveBeenLastCalledWith(12, ["fresh"])
  })
})
