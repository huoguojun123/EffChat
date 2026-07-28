import { beforeEach, describe, expect, it, vi } from "vitest"
import { useSkillStore } from "@/stores/skills"
import { listSkills } from "@/api/skills"
import type { SkillDefinition } from "@/types"

vi.mock("@/api/skills", () => ({ listSkills: vi.fn() }))

const listSkillsMock = vi.mocked(listSkills)

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

function skill(id: string): SkillDefinition {
  return { id, name: id, description: "", enabled: true, authorized: true } as SkillDefinition
}

beforeEach(() => {
  vi.clearAllMocks()
  useSkillStore.getState().resetAccountState()
})

describe("useSkillStore account isolation", () => {
  it("ignores an old account response after account state is reset", async () => {
    const stale = deferred<{ skills: SkillDefinition[] }>()
    const current = deferred<{ skills: SkillDefinition[] }>()
    listSkillsMock.mockReturnValueOnce(stale.promise).mockReturnValueOnce(current.promise)

    const staleLoad = useSkillStore.getState().loadSkills()
    useSkillStore.getState().resetAccountState()
    const currentLoad = useSkillStore.getState().loadSkills()
    current.resolve({ skills: [skill("current")] })
    await currentLoad
    stale.resolve({ skills: [skill("stale")] })
    await staleLoad

    expect(useSkillStore.getState().skills.map((item) => item.id)).toEqual(["current"])
    expect(useSkillStore.getState()).toMatchObject({ loaded: true, isLoading: false })
  })
})
